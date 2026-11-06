package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/Adaetum/Adaetum/agent/internal/auditlog"
	"github.com/Adaetum/Adaetum/agent/internal/cluster"
	"github.com/Adaetum/Adaetum/agent/internal/hostaccess"
	"github.com/Adaetum/Adaetum/agent/internal/policy"
)

const defaultHostAccessAudit = "/var/log/adaetum/host-access.log"

// hostAccessConfig is the operator's explicit request to override the security
// boundary (see docs/security-boundary.md): let the agent run commands on servers
// over SSH instead of only launching Ansible. Everything defaults to off.
type hostAccessConfig struct {
	allow       bool
	user        string
	hosts       string
	auditPath   string
	sshTarget   string
	token       string
	tokenFile   string
	nodes       cluster.NodeLister
	requireUser func(http.Handler) http.Handler
}

// hostAccessPrerequisites checks what the override needs that does not depend on
// whether it is switched on: not in development SSH mode, an ssh program, and an
// access token. A non-empty reason says what is missing. Flipping the switch can
// never get around these.
func hostAccessPrerequisites(cfg hostAccessConfig) string {
	if cfg.sshTarget != "" {
		return "This agent is reading another cluster over SSH for development, so it never runs commands on servers."
	}
	if _, err := exec.LookPath("ssh"); err != nil {
		return "The ssh program was not found on this machine."
	}
	if cfg.token == "" {
		_, tokenReason := checkToken(cfg.tokenFile, realSystem())
		if tokenReason == "" {
			tokenReason = "none is configured."
		}
		return "The override needs an access token, but " + tokenReason
	}
	return ""
}

// decideHostAccess says whether the startup flags alone turn the override on: the
// flag must be given and every prerequisite must hold. Default: off.
func decideHostAccess(cfg hostAccessConfig) (enabled bool, reason string) {
	if cfg.sshTarget != "" {
		return false, hostAccessPrerequisites(cfg)
	}
	if !cfg.allow {
		return false, hostaccess.OffReason
	}
	if reason := hostAccessPrerequisites(cfg); reason != "" {
		return false, reason
	}
	return true, ""
}

// hostSSHLeverKey is the Security page's key for the override.
const hostSSHLeverKey = "host_ssh"

// hostSSHLever is the Security page's entry for the override. It lives here, with
// the rest of the override's wiring, so the host agent's code never mentions it.
func hostSSHLever(available func() (bool, string), startupOn, toggleable bool) policy.Lever {
	return policy.Lever{
		Key:            hostSSHLeverKey,
		Title:          "Run commands directly on servers",
		Description:    "Skips Ansible: lets people run a command on a server over SSH. Use it to reach a machine directly when a playbook is not the right tool. Every command is recorded, and only the cluster's own servers can be reached.",
		Risk:           "highest",
		Toggleable:     toggleable,
		StartupDefault: startupOn,
		OffReason:      hostaccess.OffReason,
		Available:      available,
	}
}

// setupHostAccess registers the override's API on mux. It is always registered so the
// page can say that the override is off; the command endpoint refuses unless it is
// both available (prerequisites and a writable record) and switched on, which is
// asked on every request: the Security page's switch, or with none, the flag.
func setupHostAccess(mux *http.ServeMux, cfg hostAccessConfig, board func() *policy.Switchboard) (available func() (bool, string), stop func()) {
	reason := hostAccessPrerequisites(cfg)
	api := &hostaccess.API{Capabilities: hostaccess.Capabilities{}, RequireUser: cfg.requireUser}
	stop = func() {}

	if reason == "" {
		log, err := auditlog.Open(cfg.auditPath)
		if err != nil {
			// No record, no commands: the override will not run unseen.
			reason = fmt.Sprintf("Cannot write the record of commands at %s (%v), so none will run. Pass -host-ssh-audit with a writable path.", cfg.auditPath, err)
		} else {
			allowed := cfg.nodes
			if explicit := splitList(cfg.hosts); len(explicit) > 0 {
				// An explicit list is the operator naming exactly where commands may go,
				// and does not depend on Kubernetes being readable.
				allowed = func(context.Context) ([]string, error) { return explicit, nil }
			}
			api.Manager = &hostaccess.Manager{
				Exec:    hostaccess.SSHExec{User: cfg.user},
				Audit:   func(entry hostaccess.AuditEntry) error { return log.Record(entry) },
				Allowed: allowed,
			}
			stop = func() { log.Close() }
		}
	}
	api.Capabilities.Reason = reason
	api.Enabled = func() (bool, string) {
		if b := board(); b != nil {
			return b.Enabled(hostSSHLeverKey)
		}
		if !cfg.allow {
			return false, hostaccess.OffReason
		}
		return true, ""
	}

	if board() == nil {
		if on, _ := api.Enabled(); reason == "" && on {
			fmt.Fprintf(os.Stderr, "HOST ACCESS: ON. Commands may be run on servers over SSH as %q, skipping Ansible. Every command is recorded in %s.\n", cfg.user, cfg.auditPath)
		} else {
			fmt.Fprintln(os.Stderr, "host access: off. The agent changes servers only by launching Ansible playbooks.")
			if reason != "" && cfg.allow {
				fmt.Fprintln(os.Stderr, "host access: the override was requested but is unavailable:", reason)
			}
		}
	}
	api.Register(mux)
	return func() (bool, string) { return reason == "", reason }, stop
}

func splitList(text string) []string {
	var out []string
	for _, item := range strings.Split(text, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}
