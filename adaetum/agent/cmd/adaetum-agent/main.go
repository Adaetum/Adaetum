// Command adaetum-agent is the Adaetum management agent.
//
// The same binary is installed in two places, and both are required:
//
//   - on every HOST, outside Kubernetes, so it keeps working and stays reachable
//     for troubleshooting and direct host actions when the cluster is down; and
//   - as a pod in the CLUSTER, once it is up, as the cluster's single front door
//     (see internal/membership, internal/cluster and docs/cluster.md; running it as a pod
//     is not built yet, but the role itself works with `cluster`).
//
// What works today, in the host role:
//
//	inspect   print a read-only report of the host and its RKE2 cluster
//	serve     serve that report, the Ansible jobs view, and image cleanup over HTTP
//	images    plan, apply, or run unused-image cleanup on this server
//	token     create the access token that changes and network access require
//	mcp       serve the read-only capabilities to AI assistants (Model Context Protocol)
//	join      enroll this host with a cluster agent (one-time token, pinned certificate)
//	embed     points at the separate embedding service program (embed/)
//	cluster   run the cluster-agent role: enroll hosts, receive events, show the cluster (dev)
//
// SECURITY BOUNDARY: the agent does not run commands on servers. Servers are changed
// by Ansible playbooks that the agent launches. A host agent acts only on its own
// machine; the cluster agent has no host-level access. Running commands directly over
// SSH is an explicit, off-by-default override on the cluster agent (-allow-host-ssh).
// See docs/security-boundary.md; .validator/validate-agent-boundary.py enforces it.
//
// Everything that reads is read-only. The only code that changes a server lives in
// internal/cleanup, behind root, an explicit flag, and a token. The project is in
// development: it is run against production only through SSH in read-only mode
// (-ssh), never installed on it.
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
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/automation"
	"github.com/Adaetum/Adaetum/agent/internal/catalog"
	"github.com/Adaetum/Adaetum/agent/internal/inspect"
	"github.com/Adaetum/Adaetum/agent/internal/policy"
	"github.com/Adaetum/Adaetum/agent/internal/tlscert"
	"github.com/Adaetum/Adaetum/agent/internal/webui"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "inspect":
		runInspect(os.Args[2:])
	case "serve":
		runServe(os.Args[2:])
	case "images":
		runImages(os.Args[2:])
	case "token":
		runToken(os.Args[2:])
	case "mcp":
		runMCP(os.Args[2:])
	case "version":
		// Used by the host-agent Ansible role to decide whether an upgrade is needed.
		fmt.Println(agentVersion)
	case "cluster":
		runCluster(os.Args[2:])
	case "join":
		runJoin(os.Args[2:])
	case "embed":
		runEmbed(os.Args[2:])
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: adaetum-agent inspect [-ssh target] | serve [-listen addr] [-ssh target] | images plan|apply|run | token create | mcp [-ssh target] | join | cluster | embed (see embed/) | version")
	os.Exit(2)
}

// newInspector returns a local inspector, or one that reads another host over
// SSH when target is set. Remote mode is for developing against machines that
// must not run the agent yet; it installs nothing on them.
func newInspector(sshTarget string) *inspect.Inspector {
	if sshTarget == "" {
		return inspect.NewInspector()
	}
	inspector, err := inspect.NewRemoteInspector(sshTarget)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	return inspector
}

func runInspect(args []string) {
	flags := flag.NewFlagSet("inspect", flag.ExitOnError)
	sshTarget := flags.String("ssh", "", "inspect this host over SSH (user@host) instead of the local machine")
	_ = flags.Parse(args)

	report := newInspector(*sshTarget).Collect(context.Background())
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// runServe exposes the inspection report. There is no authentication yet, so
// the default listener is loopback only; reach it through an SSH tunnel until
// the agent has its own auth.
func runServe(args []string) {
	flags := flag.NewFlagSet("serve", flag.ExitOnError)
	listen := flags.String("listen", "127.0.0.1:8080", "address to listen on; use 0.0.0.0:8443 to be reachable from the local network (needs -token-file, and serves HTTPS)")
	tlsDir := flags.String("tls-dir", filepath.Join(stateDir, "tls"), "where the self-signed certificate is kept when listening beyond this machine")
	sshTarget := flags.String("ssh", "", "inspect this host over SSH (user@host) instead of the local machine")
	allowChanges := flags.Bool("allow-changes", false, "let the UI remove container images and schedule cleanups (needs root, RKE2, and -token-file)")
	dataDir := flags.String("data-dir", "", "directory for the agent's local database (e.g. /var/lib/adaetum); off when empty")
	historyDir := flags.String("history-dir", "", "directory for the usage history database (workload CPU and memory over time, for sizing); defaults to -data-dir; off when both are empty")
	ansible := addAnsibleFlags(flags)
	tokenFile := flags.String("token-file", "", "file holding the access token that changes require (make one with `token create`)")
	auth := addAuthFlags(flags)
	_ = flags.Parse(args)

	exposed, err := listensBeyondLoopback(*listen)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	var accessToken string
	if exposed {
		accessToken = requireSafeExposure(*listen, *sshTarget, *tokenFile)
	}

	// People can also sign in with their own accounts when an auth service is configured;
	// the access token keeps working beside it, and alone when there is none.
	authClient := auth.client()
	guard := combinedGuard(accessToken, authClient)

	inspector := newInspector(*sshTarget)
	defer startHostIdentity(*dataDir, *sshTarget, authClient)()
	mux := http.NewServeMux()
	if *historyDir == "" {
		*historyDir = *dataDir
	}
	defer startUsageHistory(mux, *historyDir, inspector)()
	ansible.sshTarget, ansible.tokenFile = *sshTarget, *tokenFile
	ansible.requireUser = guard
	ansible.controller, _ = inspector.Hostname()
	// A host agent's switches are fixed when it starts (its flags); the Security page
	// shows them read-only. Turning them on and off at run time is the cluster
	// agent's feature (a host's own levers are not changed from the network).
	noBoard := func() *policy.Switchboard { return nil }
	ansibleAvailable, stopAnsible := setupAnsible(mux, *ansible, noBoard)
	defer stopAnsible()
	cleanupCfg := cleanupConfig{allowChanges: *allowChanges, sshTarget: *sshTarget, tokenFile: *tokenFile}
	stopCleanup := setupCleanup(mux, inspector, cleanupCfg)
	defer stopCleanup()
	host, _ := inspector.Hostname()
	imageCaps, _ := decideCapabilities(host, cleanupCfg, realSystem())
	board, closeSecurity := newSwitchboard(nil, "", func() []policy.Lever {
		// The cluster-only override is not offered on a host agent at all.
		return []policy.Lever{ansibleLever(ansibleAvailable, ansible.allow, false), {
			Key: "image_changes", Title: "Remove unused container images from this page",
			Description: "Lets the Storage page remove images on this server. It needs root, the access token, and -allow-changes.",
			Risk:        "medium", Toggleable: false, StartupDefault: true, OffReason: imageCaps.Reason,
			Available: func() (bool, string) { return imageCaps.CanApply, imageCaps.Reason },
		}}
	})
	defer closeSecurity()
	registerSecurity(mux, board, guard)
	registerHostConnection(mux, *dataDir)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/api/v1/inspect", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(inspector.Collect(ctx))
	})

	// Automation is read-only: runner state, run history, playbooks, and the
	// policies platform.yaml declares.
	automationCollector := &automation.Collector{Source: inspector, Now: time.Now}
	mux.HandleFunc("/api/v1/automation", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(automationCollector.Collect(ctx))
	})

	// The web interface is served by the agent itself, so a host is usable with
	// nothing else installed. It is the catch-all route; the API routes above are
	// more specific and win.
	// The app explorer (and the logos on the app cards) also works on a host agent, so
	// the Apps and Find apps pages behave the same wherever the page is served from.
	// With a token configured the routes need it; on plain loopback there is none.
	catalogAPI := &catalog.API{}
	if guard != nil {
		catalogAPI.RequireUser = guard
	}
	catalogAPI.Register(mux)
	mountAccounts(mux, authClient, guard)
	mux.Handle("/", webui.Handler())

	// Every API request needs a login whenever there is one to ask for: the token, an
	// account, or both. The sign-in pages themselves stay open (see requireLogin).
	var handler http.Handler = mux
	if guard != nil {
		handler = requireLogin(mux, guard)
	}
	server := &http.Server{Addr: *listen, ReadHeaderTimeout: 5 * time.Second}
	if exposed {
		// The login (token and/or account) only travels over TLS.
		certificates := tlscert.NewManager(*tlsDir)
		_, leaf, err := certificates.Current()
		if err != nil {
			fmt.Fprintln(os.Stderr, "cannot make a certificate, so the agent will not listen beyond this machine:", err)
			os.Exit(1)
		}
		server.TLSConfig = &tls.Config{GetCertificate: certificates.GetCertificate, MinVersion: tls.VersionTLS12}
		fmt.Fprintf(os.Stderr, "listening on https://%s (reachable from the network; token required)\n", *listen)
		fmt.Fprintln(os.Stderr, "certificate fingerprint (SHA-256), to check on first visit:", tlscert.Fingerprint(leaf))
		server.Handler = handler
		if err := server.ListenAndServeTLS("", ""); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	server.Handler = handler
	fmt.Fprintln(os.Stderr, "listening on", *listen)
	if err := server.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
