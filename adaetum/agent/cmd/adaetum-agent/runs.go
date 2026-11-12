package main

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/Adaetum/Adaetum/agent/internal/hostfacts"
	"github.com/Adaetum/Adaetum/agent/internal/hostfiles"
	"github.com/Adaetum/Adaetum/agent/internal/inspect"
	"github.com/Adaetum/Adaetum/agent/internal/policy"
	"github.com/Adaetum/Adaetum/agent/internal/procedures/firstboot"
	"github.com/Adaetum/Adaetum/agent/internal/runs"
	"github.com/Adaetum/Adaetum/agent/internal/runsapi"
	"github.com/Adaetum/Adaetum/agent/internal/runstore"
	"github.com/Adaetum/Adaetum/agent/internal/store"
)

// runsConfig is how this agent is set up to run procedures.
type runsConfig struct {
	// allow lets the API start and retry runs. Reading and cancelling do not need it.
	// A host's switches are fixed when it starts, like -allow-ansible.
	allow bool
	// atStart names procedures the agent starts itself whenever it starts (for
	// example host-readiness).
	atStart string
}

func addRunsFlags(flags *flag.FlagSet) *runsConfig {
	cfg := &runsConfig{}
	flags.BoolVar(&cfg.allow, "allow-runs", false, "let the web page and API start procedures (needs -data-dir and an access token)")
	flags.StringVar(&cfg.atStart, "run-on-start", "", "comma-separated procedures the agent starts itself each time it starts, such as host-readiness (needs -data-dir). The agent never approves a destructive step on its own, so a procedure with one stops there and waits for a person")
	return cfg
}

// hostProcedures are the procedures a host agent knows: each first-boot chunk on its own
// (so it can be run and tested alone) and first-boot, which runs them in order. They are
// added one at a time as each old phase is rebuilt on the engine (see
// docs/firstboot-rework.md), and each reads or changes the machine only through an
// adapter.
func hostProcedures(inspector *inspect.Inspector) []*runs.Procedure {
	host := hostfacts.Facts{Inspector: inspector}
	files := hostfiles.FS{}
	procedures := firstboot.Chunks(host, files, randomBytes)
	if composite, err := firstboot.New(host, files, randomBytes); err != nil {
		// A bug in this build: say so, and still offer the chunks.
		fmt.Fprintln(os.Stderr, "the first-boot procedure could not be built:", err)
	} else {
		procedures = append(procedures, composite)
	}
	return procedures
}

// randomBytes is the machine's cryptographic random source.
func randomBytes(n int) ([]byte, error) {
	out := make([]byte, n)
	_, err := rand.Read(out)
	return out, err
}

// runsLeverKey is the Security page's key for starting runs.
const runsLeverKey = "runs"

// runsOffReason is the standing explanation while starting runs is off.
const runsOffReason = "Starting runs is switched off. Start the agent with -allow-runs."

// runsLever is the Security page's entry for starting runs. Starting a run lets a
// procedure change this machine, so it is a lever like launching playbooks: high risk,
// off unless asked for, and recorded.
func runsLever(available func() (bool, string), startupOn, toggleable bool) policy.Lever {
	return policy.Lever{
		Key:            runsLeverKey,
		Title:          "Start runs",
		Description:    "Lets people start procedures, such as bringing a node up or unsealing OpenBao, from the Runs page, and try them again. Watching and stopping runs is always allowed. Starting needs the access token and every run is recorded.",
		Risk:           "high",
		Toggleable:     toggleable,
		StartupDefault: startupOn,
		OffReason:      runsOffReason,
		Available:      available,
	}
}

// setupRuns starts the run engine on the agent's own database and mounts its API. It
// returns whether starting runs is possible at all (for the Security page's lever)
// and the function that stops the engine. Runs the agent was working on when it last
// stopped are resumed here.
//
// Starting a run is a powerful lever (a procedure can change this machine as root),
// so it follows the playbook launcher: off unless the agent was started with
// -allow-runs or the Security page switches it on, never without an access token, and
// always behind a login.
func setupRuns(mux *http.ServeMux, db *store.Store, cfg *runsConfig, tokenFile string, guard func(http.Handler) http.Handler, procedures []*runs.Procedure, board func() *policy.Switchboard) (availability func() (bool, string), stop func()) {
	noop := func() {}
	unavailable := func(reason string) func() (bool, string) {
		return func() (bool, string) { return false, reason }
	}
	if db == nil {
		reason := "Runs keep their history in the agent's local database. Start the agent with -data-dir."
		(&runsapi.API{Reason: reason, RequireUser: guard}).Register(mux)
		return unavailable(reason), noop
	}
	manager, err := runs.NewManager(runs.Options{
		Journal: runstore.Journal{Store: db},
		// One greppable line per record on stderr, which systemd sends to the journal.
		// While first boot is unfinished the host uploads that journal with the rest of
		// its logs, so a run can be read back with `task fetch-logs`.
		OnRecord: func(rec runs.Record) { fmt.Fprintln(os.Stderr, rec.Line()) },
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot start the run engine:", err)
		reason := "The run engine could not start: " + err.Error()
		(&runsapi.API{Reason: reason, RequireUser: guard}).Register(mux)
		return unavailable(reason), noop
	}
	for _, procedure := range procedures {
		if err := manager.Register(procedure); err != nil {
			// A malformed procedure is a bug in this build. Say so loudly, keep the others.
			fmt.Fprintf(os.Stderr, "procedure %s is not usable and was left out: %v\n", procedure.Name, err)
		}
	}
	if err := manager.Resume(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "cannot resume unfinished runs:", err)
	}
	startAtBoot(manager, cfg.atStart)

	// Whether runs CAN be started: the database is open and an access token is set up.
	// Checked on every request, so fixing the token file takes effect at once.
	availability = func() (bool, string) {
		if _, why := checkToken(tokenFile, realSystem()); why != "" {
			return false, "Starting runs needs an access token, but " + why
		}
		return true, ""
	}

	// Starting is never open: even on a loopback agent with no other login it needs the
	// token, whichever way it was switched on. This is decided here and not from the
	// switch, because the switchboard is built after this API and a gap in between
	// would leave starting open. (The playbook launcher does the same.)
	if guard == nil {
		if value, why := checkToken(tokenFile, realSystem()); why == "" {
			guard = bearerGuard(value)
		}
	}
	api := &runsapi.API{
		Manager: manager, RequireUser: guard, Who: personName,
		// Asked on every request: the Security page's switch when there is one, else
		// the startup flag. Reading and cancelling are never gated by it.
		Enabled: func() (bool, string) {
			if b := board(); b != nil {
				return b.Enabled(runsLeverKey)
			}
			if !cfg.allow {
				return false, runsOffReason
			}
			return availability()
		},
	}
	api.Register(mux)
	return availability, manager.Close
}

// startAtBoot starts the procedures named by -run-on-start. The agent starts them
// itself, so they need no login, but it never approves a destructive step: a procedure
// that has one stops there and waits for a person, so this cannot do anything
// irreversible on its own. A procedure that is already running (resumed from before a
// restart) is left alone rather than started twice.
func startAtBoot(manager *runs.Manager, names string) {
	running := map[string]bool{}
	for _, snap := range manager.List() {
		if snap.State == runs.StateRunning {
			running[snap.Procedure] = true
		}
	}
	for _, name := range strings.Split(names, ",") {
		name = strings.TrimSpace(name)
		if name == "" || running[name] {
			continue
		}
		if _, err := manager.Start(context.Background(), runs.StartRequest{Procedure: name, StartedBy: "boot"}); err != nil {
			fmt.Fprintf(os.Stderr, "cannot start %s at boot: %v\n", name, err)
		}
	}
}
