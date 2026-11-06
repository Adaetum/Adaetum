package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/accounts"
	"github.com/Adaetum/Adaetum/agent/internal/agentapi"
	"github.com/Adaetum/Adaetum/agent/internal/assistant"
	"github.com/Adaetum/Adaetum/agent/internal/catalog"
	"github.com/Adaetum/Adaetum/agent/internal/cluster"
	"github.com/Adaetum/Adaetum/agent/internal/clustertools"
	"github.com/Adaetum/Adaetum/agent/internal/identity"
	"github.com/Adaetum/Adaetum/agent/internal/membership"
	"github.com/Adaetum/Adaetum/agent/internal/memory"
	"github.com/Adaetum/Adaetum/agent/internal/policy"
	"github.com/Adaetum/Adaetum/agent/internal/tlscert"
	"github.com/Adaetum/Adaetum/agent/internal/webui"
)

const clusterDatabaseName = "cluster.db"

// runCluster is the cluster-agent role: the cluster's single front door.
//
// SECURITY BOUNDARY (docs/security-boundary.md, enforced by
// .validator/validate-agent-boundary.py): this role has NO host-level access. It
// never runs a command on a server. It changes servers only by launching Ansible
// playbooks (internal/ansiblerun), which do the work over SSH. The one exception is
// the explicit, off-by-default override -allow-host-ssh (internal/hostaccess), which
// lives only here and never in the host agent.
//
// INTENT. Once Kubernetes is up with two or more nodes, the same binary runs as a
// pod. Host agents stay independent and directly reachable (that is the point of
// having both); this only adds the cluster-wide view. It:
//
//   - enrolls hosts (one-time join tokens; a host that is already a Kubernetes node
//     is approved at once, any other waits for a person) and accepts their signed
//     events, in internal/cluster;
//   - keeps its own Turso database and derives the cluster view from the events;
//   - compares Kubernetes' server list with the check-ins to show the servers that
//     are not running the agent, are quiet, or are out of date (membership.Assess);
//   - always serves HTTPS with its own certificate and requires the access token for
//     every person-facing endpoint (hosts authenticate by signature instead).
//
// NOT BUILT YET: running it as a pod (image, manifest in pods/, a volume for the
// database, the Lease for more than one replica), listing nodes from inside the
// cluster (today only `-ssh` dev mode can list nodes), and serving the web UI.
func runCluster(args []string) {
	flags := flag.NewFlagSet("cluster", flag.ExitOnError)
	listen := flags.String("listen", "127.0.0.1:8444", "address to listen on (always HTTPS)")
	dataDir := flags.String("data-dir", filepath.Join(stateDir, "cluster"), "directory for the cluster agent's database and certificate")
	tokenFile := flags.String("token-file", "", "file holding the access token people use (make one with `token create`)")
	ansible := addAnsibleFlags(flags)
	// THE SECURITY BOUNDARY: by default the cluster agent has no host-level access.
	// It changes servers only by launching Ansible. This flag is the explicit
	// override; see docs/security-boundary.md.
	allowHostSSH := flags.Bool("allow-host-ssh", false, "OVERRIDE: let people run commands on servers over SSH, skipping Ansible (off by default; needs a token and a writable -host-ssh-audit)")
	hostSSHUser := flags.String("host-ssh-user", "ansible", "login user for the override")
	hostSSHHosts := flags.String("host-ssh-hosts", "", "servers the override may reach, comma separated; when empty, the cluster's own servers")
	embedderKind := flags.String("embedder", "hash", "how memories are embedded: hash (a lexical development stand-in), http (the embedding service), or none")
	embedderURL := flags.String("embedder-url", "", "the embedding service's URL, for -embedder http")
	embedderModel := flags.String("embedder-model", "minilm-l6-v2-int8", "the model the embedding service runs, for -embedder http")
	embedderKeyFile := flags.String("embedder-key-file", "", "file holding the API key for an external OpenAI-compatible embedding service (mode 600); never stored or shown")
	enableAssistant := flags.Bool("enable-assistant", false, "START with the AI assistant on (it can also be set up from the AI model page; it sends conversations and what it reads to an external AI service)")
	assistantProviderFlag := flags.String("assistant-provider", "anthropic", "which AI service: anthropic, or an OpenAI-compatible one for free testing: gemini, groq, openrouter, ollama (local), or openai with -assistant-url")
	assistantURL := flags.String("assistant-url", "", "API root of an OpenAI-compatible service (a preset provider fills this in)")
	assistantModel := flags.String("assistant-model", "", "the model the assistant uses (default for anthropic: "+assistant.DefaultModel+"; required for other providers)")
	assistantKeyFile := flags.String("assistant-key-file", "", "file holding the AI service API key (mode 600); else ANTHROPIC_API_KEY (anthropic) or ASSISTANT_API_KEY (others)")
	securityAudit := flags.String("security-audit", defaultSecurityAudit, "record of every change to the Security settings (changes are refused if it cannot be written)")
	hostSSHAudit := flags.String("host-ssh-audit", defaultHostAccessAudit, "record of every command run through the override")
	sshTarget := flags.String("ssh", "", "DEVELOPMENT: list the cluster's servers by reading this host over SSH (read-only)")
	auth := addAuthFlags(flags)
	_ = flags.Parse(args)

	token, reason := checkToken(*tokenFile, realSystem())
	if reason != "" {
		fmt.Fprintln(os.Stderr, "the cluster agent needs a login for people, but", reason)
		os.Exit(2)
	}

	// People sign in with accounts as well as the token. The cluster agent's auth service is
	// the master copy of the accounts: hosts copy from it (see docs/recovery-login.md).
	authClient := auth.client()
	guard := combinedGuard(token, authClient)

	registry, err := cluster.OpenRegistry(filepath.Join(*dataDir, clusterDatabaseName))
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot open the cluster database:", err)
		os.Exit(1)
	}
	defer registry.Close()

	api := &cluster.API{
		Registry:    registry,
		Nodes:       nodeLister(*sshTarget),
		RequireUser: guard,
		// The cluster agent is the same binary as the host agents, so the newest agent
		// it knows of is itself: any host reporting an older version is out of date.
		Membership: membership.Options{LatestVersion: agentVersion},
		Now:        time.Now,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintln(w, "ok") })
	registerClusterConnection(mux)
	// The Security page's switches. The launcher and the override are available only
	// when their prerequisites hold; the page can flip them on and off at run time and
	// the change applies to every request, so to every server, at once.
	var board *policy.Switchboard
	boardNow := func() *policy.Switchboard { return board }
	var ansibleAvailable, hostAvailable func() (bool, string)

	var stopAnsible, stopHost, closeSecurity func()
	board, closeSecurity = newSwitchboard(registry, *securityAudit, func() []policy.Lever {
		return []policy.Lever{
			ansibleLever(lateAvailability(&ansibleAvailable), *ansible.allowPtr(), true),
			hostSSHLever(lateAvailability(&hostAvailable), *allowHostSSH, true),
		}
	})
	defer closeSecurity()
	// Launching playbooks (for example, installing or updating the agent on servers)
	// is how the cluster agent acts on the cluster: it actuates Ansible, it does not
	// push anything itself. In development (-ssh) it is unavailable.
	ansible.sshTarget, ansible.tokenFile, ansible.requireUser = *sshTarget, *tokenFile, guard
	ansible.controller, _ = os.Hostname()
	ansibleAvailable, stopAnsible = setupAnsible(mux, *ansible, boardNow)
	defer stopAnsible()

	hostAvailable, stopHost = setupHostAccess(mux, hostAccessConfig{
		allow: *allowHostSSH, user: *hostSSHUser, hosts: *hostSSHHosts, auditPath: *hostSSHAudit,
		sshTarget: *sshTarget, token: token, tokenFile: *tokenFile, nodes: api.Nodes, requireUser: guard,
	}, boardNow)
	defer stopHost()

	registerSecurity(mux, board, guard)
	// Memory: remembered facts, an embedding queue drained in the background, and
	// recall by meaning with a keyword fallback. Nothing waits on the embedder.
	startup := memory.Config{Kind: memory.KindBuiltin}
	switch *embedderKind {
	case "hash":
	case "http":
		if *embedderURL == "" {
			fmt.Fprintln(os.Stderr, "-embedder http needs -embedder-url")
			os.Exit(2)
		}
		startup = memory.Config{Kind: memory.KindService, URL: *embedderURL, Model: *embedderModel}
	case "none":
		startup = memory.Config{Kind: memory.KindNone}
	default:
		fmt.Fprintln(os.Stderr, "-embedder must be hash, http, or none")
		os.Exit(2)
	}
	// Where embeddings come from can be changed from the Embedding page; the saved
	// choice wins over these flags. An API key for an outside service is only ever read
	// from a private file given here, never typed into the page or stored.
	embedKey := ""
	if *embedderKeyFile != "" {
		key, reason := assistantKey(*embedderKeyFile)
		if reason != "" {
			fmt.Fprintln(os.Stderr, "-embedder-key-file:", reason)
			os.Exit(2)
		}
		embedKey = key
	}
	embedManager := &memory.Manager{DB: registry.DB(), Startup: startup, APIKey: embedKey}
	memoryService := &memory.Service{DB: registry.DB(), Embedder: embedManager}
	workerCtx, stopWorker := context.WithCancel(context.Background())
	// Kubernetes decides which servers belong: drop hosts it no longer lists.
	go cluster.RunReconcile(workerCtx, registry, api.Nodes, 5*time.Minute, func(removed []string, err error) {
		if err != nil {
			fmt.Fprintln(os.Stderr, "hosts: could not tidy the host list:", err)
			return
		}
		fmt.Fprintln(os.Stderr, "hosts: removed servers Kubernetes no longer lists:", strings.Join(removed, ", "))
	})
	defer stopWorker()
	go memoryService.Run(workerCtx)
	// The app explorer: read-only searches of public chart and image sources, and
	// previews of the files that would add an app. It installs nothing.
	(&catalog.API{RequireUser: guard, DB: registry.DB()}).Register(mux)
	(&memory.API{Service: memoryService, RequireUser: guard}).Register(mux)
	(&memory.EmbeddingAPI{Manager: embedManager, Service: memoryService, RequireUser: guard, KeyAvailable: embedKey != ""}).Register(mux)
	if embedManager.Config().Kind == memory.KindBuiltin {
		fmt.Fprintln(os.Stderr, "memory: using the lexical development embedder (not semantic); choose another on the Embedding page, see docs/memory.md")
	}

	// Sign-in pages, the people routes, and the routes hosts call (signed with the host's
	// key) to copy the accounts. All more specific than "/api/", so they win.
	mountAccounts(mux, authClient, guard)
	if authClient != nil {
		verifier := &identity.Verifier{Now: time.Now, PublicKey: registry.PublicKey}
		(&accounts.HostAPI{
			Local:  authClient,
			Verify: verifier.Verify,
			Seeded: func(hostID string, result accounts.ApplyResult) {
				fmt.Fprintf(os.Stderr, "accounts: host %s gave this cluster its accounts (version %d)\n", hostID, result.Version)
			},
		}).Register(mux)
	}
	mux.Handle("/api/", api.Handler())
	mux.Handle("/", webui.Handler())

	// The assistant acts through the agent's own API, in-process, with the same token a
	// person signs in with, so it can do what the page can and no more. Changes wait for
	// the person's approval in the page.
	// Which AI service the assistant uses is a setting changed from the page (saved in
	// the database; the page-typed key is kept in memory only). The flags give the
	// starting choice and the keys the agent was started with.
	startupKeys := map[string]string{}
	startupKeys["anthropic"], _ = assistantKey(*assistantKeyFile, "ANTHROPIC_API_KEY")
	startupKeys["other"], _ = assistantKey(*assistantKeyFile, "ASSISTANT_API_KEY")
	assistantManager := &assistant.Manager{
		DB:          registry.DB(),
		StartupKeys: startupKeys,
		Startup:     assistant.Settings{Enabled: *enableAssistant, Provider: *assistantProviderFlag, URL: *assistantURL, Model: *assistantModel},
	}
	assistantAPI := &assistant.API{RequireUser: guard, Manager: assistantManager}
	// The service always exists; the Manager decides, per request, whether it may run.
	self := agentapi.Client{HTTP: &http.Client{Transport: agentapi.ServeTransport{Handler: mux}}, URL: "http://agent", Token: token}
	assistantAPI.Service = assistant.New(assistant.Config{
		NewMessage: assistantManager.NewMessage,
		Tools:      clustertools.Tools(self, clustertools.Options{Via: policy.ViaAssistant}),
		CoreTools:  clustertools.AssistantCore,
	})
	defer assistantAPI.Service.Wait()
	if notice := assistantManager.Notice(); notice != "" {
		fmt.Fprintln(os.Stderr, notice)
	} else if *enableAssistant {
		fmt.Fprintln(os.Stderr, "assistant: enabled but not usable yet:", assistantManager.Status().Reason)
	}
	assistantAPI.Register(mux)

	certificates := tlscert.NewManager(filepath.Join(*dataDir, "tls"))
	_, leaf, err := certificates.Current()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot make a certificate:", err)
		os.Exit(1)
	}
	server := &http.Server{
		Addr:              *listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		TLSConfig:         &tls.Config{GetCertificate: certificates.GetCertificate, MinVersion: tls.VersionTLS12},
	}
	fmt.Fprintf(os.Stderr, "cluster agent listening on https://%s\n", *listen)
	fmt.Fprintln(os.Stderr, "certificate fingerprint (give this to `join -fingerprint`):", tlscert.Fingerprint(leaf))
	if err := server.ListenAndServeTLS("", ""); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// nodeLister returns how the cluster agent learns which servers Kubernetes lists.
//
// STUB for the in-cluster case: from inside a pod the right way is the Kubernetes
// API with the pod's service account, which is not written yet. Until then the
// lister reports an error, and the registry treats that as "unknown": it never
// auto-approves, and the cluster view says it could not compare. Safe, not useful.
// With -ssh (development) it reads the nodes of a remote cluster, read-only.
func nodeLister(sshTarget string) cluster.NodeLister {
	if sshTarget == "" {
		return func(context.Context) ([]string, error) {
			return nil, fmt.Errorf("listing servers from inside the cluster is not built yet; use -ssh for development")
		}
	}
	inspector := newInspector(sshTarget)
	return func(ctx context.Context) ([]string, error) {
		raw, err := inspector.KubectlJSON(ctx, "nodes")
		if err != nil {
			return nil, err
		}
		var list struct {
			Items []struct {
				Metadata struct {
					Name string `json:"name"`
				} `json:"metadata"`
			} `json:"items"`
		}
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, err
		}
		names := make([]string, 0, len(list.Items))
		for _, item := range list.Items {
			names = append(names, item.Metadata.Name)
		}
		return names, nil
	}
}

var _ membership.Ingester = (*cluster.Registry)(nil)

// assistantKey finds an AI service API key (env names the variables to try): a private file if one is named, else the
// environment. The key is never printed or logged. A non-empty reason says what is
// wrong, in words an operator can act on.
func assistantKey(file string, env ...string) (key, reason string) {
	if len(env) == 0 {
		env = []string{"ANTHROPIC_API_KEY"}
	}
	if file != "" {
		value, mode, err := realSystem().readToken(file)
		switch {
		case err != nil:
			return "", "the key file could not be read: " + err.Error()
		case mode.Perm()&0o077 != 0:
			return "", "the key file must be readable only by its owner (mode 600)."
		case value == "":
			return "", "the key file is empty."
		}
		return value, ""
	}
	for _, name := range env {
		if value := strings.TrimSpace(os.Getenv(name)); value != "" {
			return value, ""
		}
	}
	return "", "no API key: pass -assistant-key-file or set " + strings.Join(env, " or ") + "."
}
