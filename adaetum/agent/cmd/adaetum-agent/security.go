package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/Adaetum/Adaetum/agent/internal/auditlog"
	"github.com/Adaetum/Adaetum/agent/internal/policy"
)

const defaultSecurityAudit = "/var/log/adaetum/security-changes.log"

// lateAvailability returns an availability function that reads the variable when it
// is asked, not when it is built. The launcher and the override are wired after the
// switchboard exists, so their availability is not known yet when levers are made.
// Until it is set, the lever reads as unavailable rather than as permission.
func lateAvailability(source *func() (bool, string)) func() (bool, string) {
	return func() (bool, string) {
		if *source == nil {
			return false, "still starting"
		}
		return (*source)()
	}
}

// ansibleLever is the Security page's entry for launching playbooks.
func ansibleLever(available func() (bool, string), startupOn, toggleable bool) policy.Lever {
	return policy.Lever{
		Key:            ansibleLeverKey,
		Title:          "Run automation tasks",
		Description:    "Lets people start automation tasks, such as installing or updating the agent, on servers from the Automation page. Preview is the default and every launch is recorded.",
		Risk:           "high",
		Toggleable:     toggleable,
		StartupDefault: startupOn,
		OffReason:      ansibleOffReason,
		Available:      available,
	}
}

// newSwitchboard builds the switchboard with its record. If the record cannot be
// opened, flips are refused (Audit is nil) rather than made unseen.
func newSwitchboard(store policy.Store, auditPath string, levers func() []policy.Lever) (*policy.Switchboard, func()) {
	var audit func(policy.AuditEntry) error
	closeLog := func() {}
	if store != nil {
		log, err := auditlog.Open(auditPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "security settings: changes are disabled because the record at %s cannot be written (%v). Pass -security-audit with a writable path.\n", auditPath, err)
		} else {
			audit = func(entry policy.AuditEntry) error { return log.Record(entry) }
			closeLog = func() { log.Close() }
		}
	}
	return policy.NewSwitchboard(store, audit, levers()...), closeLog
}

// registerSecurity adds the Security page's API and reports the final state once.
func registerSecurity(mux *http.ServeMux, board *policy.Switchboard, requireUser func(http.Handler) http.Handler) {
	(&policy.API{Board: board, RequireUser: requireUser, Who: personName}).Register(mux)
	for _, state := range board.List() {
		switch {
		case state.Enabled:
			fmt.Fprintf(os.Stderr, "security: %s: ON\n", state.Title)
		case !state.Available:
			fmt.Fprintf(os.Stderr, "security: %s: unavailable (%s)\n", state.Title, state.Reason)
		default:
			fmt.Fprintf(os.Stderr, "security: %s: off\n", state.Title)
		}
	}
}
