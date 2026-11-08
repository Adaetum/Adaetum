package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/ansiblerun"
	"github.com/Adaetum/Adaetum/agent/internal/policy"
)

const (
	defaultAnsibleDir   = "/opt/adaetum/ansible"
	defaultAnsibleAudit = "/var/log/adaetum/ansible-runs.log"
)

// ansibleConfig is what the operator asked for on the command line.
type ansibleConfig struct {
	allow       bool
	dir         string
	program     string
	inventory   string
	auditPath   string
	tokenFile   string
	sshTarget   string
	controller  string
	executor    string
	requireUser func(http.Handler) http.Handler
}

// allowPtr is the startup flag, for the Security page's default.
func (c *ansibleConfig) allowPtr() *bool { return &c.allow }

// addAnsibleFlags registers the launcher's flags. serve and cluster share them.
func addAnsibleFlags(flags *flag.FlagSet) *ansibleConfig {
	cfg := &ansibleConfig{}
	flags.BoolVar(&cfg.allow, "allow-ansible", false, "let the web page launch playbooks (needs Ansible, -token-file, and a writable -ansible-audit)")
	flags.StringVar(&cfg.dir, "ansible-dir", defaultAnsibleDir, "the repository's ansible directory (holds ansible.cfg and playbooks/)")
	flags.StringVar(&cfg.program, "ansible-playbook", "ansible-playbook", "the ansible-playbook program to run")
	flags.StringVar(&cfg.inventory, "ansible-inventory", "", "inventory file, overriding the one in ansible.cfg")
	flags.StringVar(&cfg.auditPath, "ansible-audit", defaultAnsibleAudit, "record of every launch (the launcher refuses to run if it cannot write here)")
	flags.StringVar(&cfg.executor, "ansible-executor", "local", "where to run playbooks: local, or kubernetes using the existing cluster runner")
	return cfg
}

// ansiblePrerequisites checks everything the launcher needs that does not depend on
// whether it is switched on: it never runs over SSH, Ansible and its directory are
// present, and there is an access token. A non-empty reason says what is missing, in
// words an operator can act on. The switch itself (a startup flag, or the Security
// page) is separate, so a prerequisite can never be bypassed by flipping it.
func ansiblePrerequisites(cfg ansibleConfig, sys system) (token, reason string) {
	if cfg.sshTarget != "" {
		return "", fmt.Sprintf("This agent is reading %s over SSH, so it never launches anything there.", cfg.sshTarget)
	}
	if _, err := exec.LookPath(cfg.program); err != nil {
		return "", fmt.Sprintf("Ansible is not installed here: %s was not found. Install Ansible or pass -ansible-playbook.", cfg.program)
	}
	if info, err := os.Stat(filepath.Join(cfg.dir, "ansible.cfg")); err != nil || info.IsDir() {
		return "", fmt.Sprintf("%s does not look like the ansible directory (no ansible.cfg). Pass -ansible-dir.", cfg.dir)
	}
	token, tokenReason := checkToken(cfg.tokenFile, sys)
	if tokenReason != "" {
		return "", "Launching playbooks needs an access token, but " + tokenReason
	}
	return token, ""
}

const ansibleOffReason = "Launching playbooks is switched off. Start the agent with -allow-ansible, or turn it on in Security settings (cluster agent)."

// ansibleLeverKey is the Security page's key for the launcher.
const ansibleLeverKey = "ansible_launcher"

// setupAnsible registers the launcher's API on mux and returns the function that
// reports whether the prerequisites hold (for the Security page), and one that waits
// for a running playbook and closes the record. board may be nil (an agent whose
// setting is fixed at startup by -allow-ansible).
func setupAnsible(mux *http.ServeMux, cfg ansibleConfig, board func() *policy.Switchboard) (available func() (bool, string), stop func()) {
	var token, reason string
	var executor ansiblerun.Executor
	var kubeJob *ansiblerun.KubernetesJob
	switch cfg.executor {
	case "", "local":
		token, reason = ansiblePrerequisites(cfg, realSystem())
		executor = ansiblerun.LocalExec{AnsiblePlaybook: cfg.program, Dir: cfg.dir, Inventory: cfg.inventory}
	case "kubernetes":
		if cfg.sshTarget != "" {
			reason = "Kubernetes jobs are unavailable in SSH development mode."
		} else if cfg.inventory != "" {
			reason = "Kubernetes jobs use the existing runner inventory; a local inventory override is not supported."
		} else if value, why := checkToken(cfg.tokenFile, realSystem()); why != "" {
			reason = "Launching playbooks needs an access token, but " + why
		} else {
			token = value
			var err error
			kubeJob, err = ansiblerun.NewKubernetesJob()
			if err != nil {
				reason = "Kubernetes jobs are unavailable: " + err.Error()
			} else {
				executor = kubeJob
			}
		}
	default:
		reason = "Unknown Ansible executor; choose local or kubernetes."
	}
	api := &ansiblerun.API{Capabilities: ansiblerun.Capabilities{Controller: cfg.controller, Catalog: ansiblerun.Catalog()}, RequireUser: cfg.requireUser, Who: personName}
	stop = func() {}

	if reason == "" {
		record, closeAudit, err := ansiblerun.OpenAuditFile(cfg.auditPath)
		if err != nil {
			// Without a record the launcher stays unavailable: it will not run Ansible as
			// an administrator without being able to say afterwards that it did.
			reason = fmt.Sprintf("Cannot write the record of launches at %s (%v), so nothing will be launched. Pass -ansible-audit with a writable path.", cfg.auditPath, err)
		} else {
			if api.RequireUser == nil {
				// Launching is never open: even on loopback it needs the token.
				api.RequireUser = bearerGuard(token)
			}
			manager := ansiblerun.NewManager(ansiblerun.Env{
				Exec:  executor,
				Audit: record,
			})
			api.Manager = manager
			stop = func() { manager.Cancel(); manager.Wait(); closeAudit() }
		}
	}
	api.Capabilities.Reason = reason
	availability := func() (bool, string) {
		if reason != "" {
			return false, reason
		}
		if kubeJob != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := kubeJob.Ready(ctx); err != nil {
				return false, "The Ansible runner is unavailable: " + err.Error()
			}
		}
		return true, ""
	}

	// The switch is asked on every request: the Security page (cluster agent) or, with
	// none, the startup flag.
	api.Enabled = func() (bool, string) {
		if b := board(); b != nil {
			return b.Enabled(ansibleLeverKey)
		}
		if ok, why := availability(); !ok {
			return false, why
		}
		if !cfg.allow {
			return false, ansibleOffReason
		}
		return true, ""
	}
	// With a switchboard the caller reports the final state once everything is wired.
	if board() == nil {
		switch {
		case reason != "":
			fmt.Fprintln(os.Stderr, "ansible launcher: unavailable.", reason)
		default:
			on, why := api.Enabled()
			if on {
				fmt.Fprintf(os.Stderr, "ansible launcher: on (token required; every launch recorded in %s)\n", cfg.auditPath)
			} else {
				fmt.Fprintln(os.Stderr, "ansible launcher: off.", why)
			}
		}
	}
	api.Register(mux)
	return availability, stop
}
