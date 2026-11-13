package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/cleanup"
	"github.com/Adaetum/Adaetum/agent/internal/inspect"
)

const (
	stateDir     = "/var/lib/adaetum"
	defaultToken = "/etc/adaetum/agent-token"
	// Random tokens are 64 characters. The floor is 16 because a host's token may instead be
	// the operator's recovery password (seeded at first boot), which setup already requires
	// to be at least 16 characters and hard to guess.
	minTokenBytes = 16
	imageDiskPath = "/var/lib/rancher"
)

// cleanupConfig is what the operator asked for on the command line.
type cleanupConfig struct {
	allowChanges bool
	sshTarget    string
	tokenFile    string
}

// system is what the agent finds out about the machine it runs on. It is a value
// so the decision below can be tested without being root on an RKE2 host.
type system struct {
	euid       int
	hasRuntime bool
	// readToken returns the token file's contents and mode.
	readToken func(path string) (token string, mode fs.FileMode, err error)
}

func realSystem() system {
	_, runtimeErr := os.Stat(inspect.RKE2CrictlPath)
	return system{
		euid:       os.Geteuid(),
		hasRuntime: runtimeErr == nil,
		readToken: func(path string) (string, fs.FileMode, error) {
			info, err := os.Stat(path)
			if err != nil {
				return "", 0, err
			}
			data, err := os.ReadFile(path)
			return strings.TrimSpace(string(data)), info.Mode(), err
		},
	}
}

// decideCapabilities says whether the agent may delete images, and if not, why,
// in a sentence an operator can act on. Every condition must hold: the agent is
// deliberately hard to put in a state where it deletes by accident.
func decideCapabilities(host string, cfg cleanupConfig, sys system) (capabilities cleanup.Capabilities, token string) {
	capabilities = cleanup.Capabilities{
		Host: host,
		// Two commands, one per line: preview and save a plan, then rehearse it.
		Hint: "sudo adaetum-agent images plan -o plan.json\nsudo adaetum-agent images apply -plan plan.json -dry-run",
	}
	refuse := func(reason string) (cleanup.Capabilities, string) {
		capabilities.Reason = reason
		return capabilities, ""
	}

	switch {
	case cfg.sshTarget != "":
		return refuse(fmt.Sprintf("This agent is reading %s over SSH, so it can preview but never change anything there.", host))
	case !cfg.allowChanges:
		return refuse("Changes are switched off. Start the agent on the server with -allow-changes to enable them.")
	case sys.euid != 0:
		return refuse("The agent is not running as root, which removing images needs.")
	case !sys.hasRuntime:
		return refuse("This server does not run RKE2, so there are no container images to clean.")
	}

	value, reason := checkToken(cfg.tokenFile, sys)
	if reason != "" {
		return refuse("Changes need an access token, but " + reason)
	}

	capabilities.CanApply, capabilities.Hint = true, ""
	return capabilities, value
}

// setupCleanup registers the cleanup API on mux and, when changes are allowed,
// starts the scheduler. It returns a function that stops the scheduler.
func setupCleanup(mux *http.ServeMux, inspector *inspect.Inspector, cfg cleanupConfig) (stop func()) {
	host, _ := inspector.Hostname()
	capabilities, token := decideCapabilities(host, cfg, realSystem())
	capabilities.DefaultRegistries = inspect.DefaultAllowedRegistries

	api := &cleanup.API{
		Capabilities: capabilities,
		Planner:      inspector.PlanImageCleanup,
		HistoryPath:  auditLogPath,
		Token:        token,
		Now:          time.Now,
	}
	stop = func() {}

	if capabilities.CanApply {
		// Applying always uses the local inspector and runtime. The inspector given
		// here is local too (SSH mode never reaches this branch).
		local := inspect.NewInspector()
		manager := cleanup.NewManager(cleanup.Env{
			Hostname:  os.Hostname,
			Now:       time.Now,
			Lock:      func() (func(), error) { return cleanup.Lock(lockPath) },
			OpenAudit: func() (func(cleanup.Entry) error, func() error, error) { return cleanup.OpenAuditLog(auditLogPath) },
			Replan:    local.PlanImageCleanup,
			NewInUse:  local.ContainerUseChecker,
			Remove:    cleanup.RuntimeRemove,
		})
		store := cleanup.NewScheduleStore(filepath.Join(stateDir, "image-cleanup-schedule.json"), time.Local)
		api.Manager, api.Schedules = manager, store

		scheduler := &cleanup.Scheduler{
			Store:           store,
			Starter:         manager,
			Planner:         local.PlanImageCleanup,
			DiskUsedPercent: func() float64 { return diskUsedPercent(imageDiskPath) },
			Location:        time.Local,
		}
		ctx, cancel := context.WithCancel(context.Background())
		go scheduler.Run(ctx)
		stop = cancel
		fmt.Fprintln(os.Stderr, "image cleanup: changes enabled (token required); scheduler running")
	} else {
		fmt.Fprintln(os.Stderr, "image cleanup: preview only.", capabilities.Reason)
	}

	api.Register(mux)
	return stop
}

func diskUsedPercent(path string) float64 {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil || stat.Blocks == 0 {
		return 0
	}
	return 100 * (1 - float64(stat.Bavail)/float64(stat.Blocks))
}

// runToken creates the access token the UI needs to start a cleanup. The token
// is written to a file only root can read and is never printed, so it does not
// end up in a terminal history or a log.
func runToken(args []string) {
	if len(args) == 0 || args[0] != "create" {
		fmt.Fprintln(os.Stderr, "usage: adaetum-agent token create [-file path]")
		os.Exit(2)
	}
	flags := flag.NewFlagSet("token create", flag.ExitOnError)
	path := flags.String("file", defaultToken, "where to write the token")
	_ = flags.Parse(args[1:])

	fail := func(format string, a ...any) {
		fmt.Fprintf(os.Stderr, format+"\n", a...)
		os.Exit(1)
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		fail("cannot read random bytes: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(*path), 0o750); err != nil {
		fail("cannot create the directory: %v", err)
	}
	// O_EXCL: never overwrite a token that is already in use.
	file, err := os.OpenFile(*path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		fail("cannot create %s (it may already exist): %v", *path, err)
	}
	defer file.Close()
	if _, err := file.WriteString(hex.EncodeToString(random) + "\n"); err != nil {
		fail("cannot write the token: %v", err)
	}
	fmt.Printf("Token written to %s (readable only by its owner).\n", *path)
	fmt.Printf("Start the agent with:  adaetum-agent serve -allow-changes -token-file %s\n", *path)
	fmt.Printf("Paste the token into the UI when asked; show it with:  sudo cat %s\n", *path)
}
