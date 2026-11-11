// Package reset removes what setup created in Tailscale and Cloudflare R2 so the next
// install starts clean: the cluster's tailnet devices, the two Adaetum OAuth clients,
// and the install logs of every host it removed.
//
// It is not part of the setup journey. It runs alone (headless with --reset, or from
// the window) and it deletes things in the operator's real accounts, so it is built to
// be hard to trigger by accident:
//
//   - it reads the identifiers and credentials it needs from the operator's .env (or the
//     recovery file, which has the same format), so it can only touch what that file
//     records: devices carrying the tags setup applies, OAuth clients by recorded ID,
//     log folders named after the devices it removed;
//   - it lists everything first and asks, with a default of "no", so a headless run does
//     nothing unless it is explicitly told to;
//   - it deletes exactly the list that was shown, and the Python tools re-check each ID
//     against a fresh listing before deleting it.
//
// Provider logic stays in the existing bootstrap tools (bootstrap-tailscale.py and
// bootstrap-cloudflare.py); this package only drives them through installer/sys, with
// secrets on standard input and never in an argument list.
package reset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/Adaetum/Adaetum/installer/engine"
	"github.com/Adaetum/Adaetum/installer/envfile"
	"github.com/Adaetum/Adaetum/installer/sys"
)

const (
	stepID         = "reset"
	tailscaleTool  = "./tasks/scripts/bootstrap-tailscale.py"
	cloudflareTool = "./tasks/scripts/bootstrap-cloudflare.py"
)

// Step is the reset. Dir is the checkout that holds the bootstrap tools; EnvPath is the
// .env (or recovery file) naming what to remove, and defaults to Dir/.env.
type Step struct {
	Run     sys.Runner
	Dir     string
	EnvPath string
}

// New returns the reset for the checkout at dir, reading envPath (or dir/.env when empty).
func New(run sys.Runner, dir, envPath string) *Step {
	return &Step{Run: run, Dir: dir, EnvPath: envPath}
}

func (s *Step) ID() string    { return stepID }
func (s *Step) Title() string { return "Reset Tailscale and host logs" }

func (s *Step) envPath() string {
	if s.EnvPath != "" {
		return s.EnvPath
	}
	return filepath.Join(s.Dir, ".env")
}

func python() string {
	if runtime.GOOS == "windows" {
		return "python"
	}
	return "python3"
}

// inputs is what the .env records about the accounts to reset.
type inputs struct {
	tailnet, clusterTag     string
	operatorID, operatorKey string
	clientIDs               []string
	cfToken, cfAccount      string
	bucket                  string
}

func load(path string) (inputs, error) {
	v := envfile.Read(path)
	clean := func(k string) string { return strings.Trim(strings.TrimSpace(v[k]), "\"'") }
	in := inputs{
		tailnet:     clean("TAILSCALE_DOMAIN"),
		clusterTag:  clean("TAILSCALE_CLUSTER_TAG"),
		operatorID:  clean("TAILSCALE_OPERATOR_OAUTH_CLIENT_ID"),
		operatorKey: clean("TAILSCALE_OPERATOR_OAUTH_CLIENT_SECRET"),
		cfToken:     clean("CLOUDFLARE_API_TOKEN"),
		cfAccount:   clean("CLOUDFLARE_ACCOUNT_ID"),
		bucket:      clean("R2_BUCKET"),
	}
	for _, k := range []string{"TAILSCALE_OAUTH_CLIENT_ID", "TAILSCALE_OPERATOR_OAUTH_CLIENT_ID"} {
		if id := clean(k); id != "" {
			in.clientIDs = append(in.clientIDs, id)
		}
	}
	if in.clusterTag == "" {
		in.clusterTag = "tag:cluster"
	}
	if in.bucket == "" {
		in.bucket = "iso"
	}
	if in.tailnet == "" || in.operatorID == "" || in.operatorKey == "" {
		return in, errors.New("the .env has no Tailscale tailnet or operator OAuth client, so there is nothing safe to look up")
	}
	return in, nil
}

func (s *Step) Check(_ context.Context, _ *engine.Env) (engine.Finding, error) {
	if _, err := load(s.envPath()); err != nil {
		return engine.Finding{Status: engine.Blocked, Detail: err.Error() + " (" + s.envPath() + ")"}, nil
	}
	return engine.Finding{Status: engine.Needed, Detail: "will list what setup created in Tailscale and R2, then ask before removing it"}, nil
}

func (s *Step) Plan(_ context.Context, _ *engine.Env) ([]string, error) {
	return []string{
		"Read the Tailscale and Cloudflare details recorded in " + s.envPath() + ".",
		"List the cluster's Tailscale devices, the two Adaetum OAuth clients, and the install logs of those hosts.",
		"Ask before removing anything, then remove exactly what was listed.",
	}, nil
}

// device and client mirror the JSON the bootstrap tools print.
type device struct {
	ID       string   `json:"id"`
	Hostname string   `json:"hostname"`
	Name     string   `json:"name"`
	Tags     []string `json:"tags"`
	LastSeen string   `json:"lastSeen"`
	IsHost   bool     `json:"isHost"`
}

type client struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

func (s *Step) tool(ctx context.Context, stdin string, args ...string) (string, error) {
	res, err := s.Run.RunInput(ctx, s.Dir, stdin, python(), args...)
	if err != nil {
		return "", err
	}
	if !res.OK() {
		msg := strings.TrimSpace(res.Stderr)
		if msg == "" {
			msg = "exit status " + fmt.Sprint(res.ExitCode)
		}
		return "", errors.New(msg)
	}
	return res.Stdout, nil
}

func (s *Step) devices(ctx context.Context, in inputs, confirm []string) (found []device, deleted []string, err error) {
	args := []string{tailscaleTool, "--oauth-credentials-stdin", "--tailnet", in.tailnet, "--cluster-tag", in.clusterTag, "--reset-devices"}
	if len(confirm) > 0 {
		args = append(args, "--reset-confirm-ids", strings.Join(confirm, ","))
	}
	out, err := s.tool(ctx, in.operatorID+"\n"+in.operatorKey+"\n", args...)
	if err != nil {
		return nil, nil, fmt.Errorf("Tailscale devices: %w", err)
	}
	var parsed struct {
		Devices []device `json:"devices"`
		Deleted []string `json:"deleted"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		return nil, nil, fmt.Errorf("Tailscale devices: unreadable answer: %w", err)
	}
	return parsed.Devices, parsed.Deleted, nil
}

func (s *Step) clients(ctx context.Context, in inputs, token string, confirm []string) (found []client, deleted []string, err error) {
	args := []string{tailscaleTool, "--user-token-stdin", "--tailnet", in.tailnet, "--reset-oauth-clients", "--reset-client-ids", strings.Join(in.clientIDs, ",")}
	if len(confirm) > 0 {
		args = append(args, "--reset-confirm-ids", strings.Join(confirm, ","))
	}
	out, err := s.tool(ctx, token, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("Tailscale OAuth clients: %w", err)
	}
	var parsed struct {
		Clients []client `json:"clients"`
		Deleted []string `json:"deleted"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		return nil, nil, fmt.Errorf("Tailscale OAuth clients: unreadable answer: %w", err)
	}
	return parsed.Clients, parsed.Deleted, nil
}

// logs counts (or, with confirm, deletes) each host's R2 log objects.
func (s *Step) logs(ctx context.Context, in inputs, hosts []string, confirm bool) (map[string]int, error) {
	args := []string{cloudflareTool, "--token-stdin", "--account-id", in.cfAccount, "--bucket", in.bucket, "--delete-host-logs", strings.Join(hosts, ",")}
	if confirm {
		args = append(args, "--delete-host-logs-confirm")
	}
	out, err := s.tool(ctx, in.cfToken, args...)
	if err != nil {
		return nil, fmt.Errorf("R2 logs: %w", err)
	}
	var parsed struct {
		Found   map[string]int `json:"found"`
		Deleted map[string]int `json:"deleted"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		return nil, fmt.Errorf("R2 logs: unreadable answer: %w", err)
	}
	if confirm {
		return parsed.Deleted, nil
	}
	return parsed.Found, nil
}

// The choices, worded so a headless run can pick one with a short --answer such as
// reset-what=everything (engine.Choose matches the start of an option).
const (
	optEverything = "Everything listed"
	optPickHosts  = "Choose devices one by one"
	optNoClients  = "Devices and logs only, keep the OAuth clients"
	optClients    = "OAuth clients only"
	optNothing    = "Nothing, just look"
)

func (s *Step) Apply(ctx context.Context, _ *engine.Env, ui engine.UI) error {
	say := func(msg string) { ui.Report(engine.Event{StepID: stepID, Message: msg}) }
	in, err := load(s.envPath())
	if err != nil {
		return err
	}
	canLogs := in.cfToken != "" && in.cfAccount != ""

	devs, _, err := s.devices(ctx, in, nil)
	if err != nil {
		return err
	}
	var hosts []string
	for _, d := range devs {
		if d.IsHost && d.Hostname != "" {
			hosts = append(hosts, d.Hostname)
		}
	}
	sort.Strings(hosts)
	var logCounts map[string]int
	if len(hosts) > 0 && canLogs {
		if logCounts, err = s.logs(ctx, in, hosts, false); err != nil {
			return err
		}
	}

	say(summary(devs, nil, logCounts))
	if len(devs) == 0 && totalLogs(logCounts) == 0 && len(in.clientIDs) == 0 {
		say("Nothing to remove: no cluster devices, recorded OAuth clients or host logs were found.")
		return nil
	}

	var options []string
	if len(devs) > 0 || len(in.clientIDs) > 0 {
		options = append(options, optEverything)
	}
	if len(devs) > 1 {
		options = append(options, optPickHosts)
	}
	if len(devs) > 0 && len(in.clientIDs) > 0 {
		options = append(options, optNoClients)
	}
	if len(in.clientIDs) > 0 {
		options = append(options, optClients)
	}
	options = append(options, optNothing)
	what, err := engine.Choose(ctx, ui, "reset-what", "What would you like to remove?", options, optNothing)
	if err != nil {
		return err
	}
	if what == optNothing {
		say("Nothing was removed.")
		return engine.ErrDeclined
	}

	// Decide which devices go.
	chosen := devs
	switch what {
	case optClients:
		chosen = nil
	case optPickHosts:
		chosen = nil
		for _, d := range devs {
			name := d.Hostname
			if name == "" {
				name = d.Name
			}
			if err := engine.ApproveDefault(ctx, ui, "reset-device-"+d.ID, fmt.Sprintf("Remove %s (%s)?", name, strings.Join(d.Tags, ", ")), false); err == nil {
				chosen = append(chosen, d)
			} else if !errors.Is(err, engine.ErrDeclined) {
				return err
			}
		}
	}

	// OAuth clients can only be removed with the temporary full-access token, which is
	// not in the .env. It is asked for only now that the operator wants them gone.
	wantClients := what == optEverything || what == optClients
	if what == optPickHosts && len(in.clientIDs) > 0 {
		wantClients = engine.ApproveDefault(ctx, ui, "reset-clients", "Also remove the two Adaetum OAuth clients?", false) == nil
	}
	var clients []client
	token := ""
	if wantClients && len(in.clientIDs) > 0 {
		v, aerr := ui.Ask(ctx, engine.Screen{
			ID: "reset-tailscale-token", Kind: engine.Secret,
			Title:  "Removing the OAuth clients needs a temporary Tailscale access token",
			Intro:  "Create one at https://login.tailscale.com/admin/settings/keys (a 1-day API access token). Leave it blank to keep the OAuth clients.",
			Prompt: "Tailscale access token",
		})
		if aerr == nil {
			token = strings.TrimSpace(v)
		}
		if token == "" {
			say("No access token given, so the OAuth clients are kept.")
		} else if clients, _, err = s.clients(ctx, in, token, nil); err != nil {
			return err
		}
	}

	// Only the logs of hosts that are actually going are cleaned.
	var cleanHosts []string
	selected := map[string]int{}
	for _, d := range chosen {
		if d.IsHost && d.Hostname != "" {
			if _, seen := selected[d.Hostname]; !seen {
				cleanHosts = append(cleanHosts, d.Hostname)
			}
			selected[d.Hostname] = logCounts[d.Hostname]
		}
	}
	sort.Strings(cleanHosts)

	if len(chosen) == 0 && len(clients) == 0 {
		say("Nothing selected, so nothing was removed.")
		return nil
	}
	list := summary(chosen, clients, selected)
	for _, line := range strings.Split(list, "\n") {
		say(line)
	}
	if err := engine.ApproveScreen(ctx, ui, engine.Screen{
		ID: "reset-confirm", Default: "no", Title: "Remove these?",
		Intro:    list,
		Prompt:   "This cannot be undone.",
		YesLabel: "Remove them", NoLabel: "Keep everything",
	}); err != nil {
		if errors.Is(err, engine.ErrDeclined) {
			say("Nothing was removed.")
		}
		return err
	}

	if len(chosen) > 0 {
		ids := make([]string, 0, len(chosen))
		for _, d := range chosen {
			ids = append(ids, d.ID)
		}
		_, gone, err := s.devices(ctx, in, ids)
		if err != nil {
			return err
		}
		say(fmt.Sprintf("Removed %d Tailscale device(s).", len(gone)))
		// Logs follow only hosts that were really removed.
		removed := map[string]bool{}
		for _, d := range chosen {
			for _, g := range gone {
				if d.ID == g && d.IsHost {
					removed[d.Hostname] = true
				}
			}
		}
		var confirmed []string
		for h := range removed {
			confirmed = append(confirmed, h)
		}
		sort.Strings(confirmed)
		if len(confirmed) > 0 && canLogs {
			deleted, err := s.logs(ctx, in, confirmed, true)
			if err != nil {
				return err
			}
			say(fmt.Sprintf("Removed %d log file(s) for %d host(s).", totalLogs(deleted), len(confirmed)))
		}
	}
	if len(clients) > 0 {
		cids := make([]string, 0, len(clients))
		for _, c := range clients {
			cids = append(cids, c.ID)
		}
		_, gone, err := s.clients(ctx, in, token, cids)
		if err != nil {
			return err
		}
		say(fmt.Sprintf("Removed %d OAuth client(s).", len(gone)))
	}
	return nil
}

func totalLogs(m map[string]int) int {
	n := 0
	for _, c := range m {
		n += c
	}
	return n
}

// summary is the plain-words list the operator confirms.
func summary(devs []device, clients []client, logs map[string]int) string {
	var b strings.Builder
	if len(devs) > 0 {
		b.WriteString(fmt.Sprintf("Tailscale devices (%d):\n", len(devs)))
		for _, d := range devs {
			seen := d.LastSeen
			if seen == "" {
				seen = "never seen"
			}
			name := d.Hostname
			if name == "" {
				name = d.Name
			}
			b.WriteString(fmt.Sprintf("  • %s (%s), last seen %s\n", name, strings.Join(d.Tags, ", "), seen))
		}
	}
	if len(clients) > 0 {
		b.WriteString(fmt.Sprintf("OAuth clients (%d):\n", len(clients)))
		for _, c := range clients {
			b.WriteString(fmt.Sprintf("  • %s (%s)\n", c.Description, c.ID))
		}
	}
	if n := totalLogs(logs); n > 0 {
		b.WriteString(fmt.Sprintf("Install logs in R2: %d file(s) for %d host(s).\n", n, len(logs)))
	}
	return strings.TrimRight(b.String(), "\n")
}
