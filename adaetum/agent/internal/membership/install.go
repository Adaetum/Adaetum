package membership

import (
	"sort"
	"strings"
)

// Remote install and update of the host agent.
//
// INTENT. Ansible over SSH already gives Adaetum admin rights on every server, so
// the agent does not need a second way in: when the cluster agent sees a server
// that lacks the agent (or runs an old one), the fix is to run the host-agent
// playbook (ansible/playbooks/host-agent.yml) against that server. The same lever
// installs, upgrades, and (with the same playbook) repairs. This file decides WHAT
// should be installed WHERE; it does not run anything.
//
// STATUS: PlanInstall is real and tested. Running the plan is the launcher's job
// (internal/ansiblerun): the Cluster page links to the Automation page with the
// servers filled in, a person rehearses and then runs the host-agent playbook, and
// the agent actuates Ansible; it does not push anything itself. Because this is
// remote code execution as an administrator on every server, the launcher is
// token-gated, one run at a time, recorded, rehearsal-first, and limited to
// catalogued playbooks and variables (see docs/auth.md).

// InstallAction is one server that needs the agent put on it.
type InstallAction struct {
	Server string `json:"server"`
	// Kind is "install" (no agent there) or "upgrade" (an older agent).
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
}

const (
	InstallKind = "install"
	UpgradeKind = "upgrade"
)

// InstallPlan is the set of changes that would bring the cluster up to date.
type InstallPlan struct {
	Actions []InstallAction `json:"actions"`
	// Limit is the value for ansible-playbook's --limit: the servers to touch.
	Limit string `json:"limit"`
	// Skipped explains servers deliberately left alone, so a person does not wonder
	// why a red row has no action.
	Skipped []SkippedServer `json:"skipped,omitempty"`
}

// SkippedServer is a server the plan leaves alone, and why.
type SkippedServer struct {
	Server string `json:"server"`
	Reason string `json:"reason"`
}

// PlanInstall turns a cluster assessment into the servers to install on or upgrade.
//
// Only servers the cluster itself lists are ever planned. A machine that merely
// checked in from outside the cluster is never touched: installing software on a
// machine because it claimed a name would be exactly the trust mistake the join
// process exists to prevent.
//
// A quiet (stale) server is skipped, not retried: its agent exists and the machine
// may be off or cut off, so the right first step is to find out why (its own agent
// is reachable directly), not to push software at it.
func PlanInstall(assessment Assessment) InstallPlan {
	plan := InstallPlan{Actions: []InstallAction{}}
	for _, server := range assessment.Servers {
		switch server.Status {
		case StatusMissing:
			plan.Actions = append(plan.Actions, InstallAction{
				Server: server.Server, Kind: InstallKind,
				Reason: "no agent has ever reported from this server",
			})
		case StatusOutdated:
			plan.Actions = append(plan.Actions, InstallAction{
				Server: server.Server, Kind: UpgradeKind,
				Reason: "running agent " + server.Version + ", which is older than the newest known",
			})
		case StatusStale:
			plan.Skipped = append(plan.Skipped, SkippedServer{
				Server: server.Server,
				Reason: "its agent has gone quiet; check the server first (it may be off) rather than reinstalling",
			})
		}
	}
	sort.Slice(plan.Actions, func(a, b int) bool { return plan.Actions[a].Server < plan.Actions[b].Server })

	names := make([]string, 0, len(plan.Actions))
	for _, action := range plan.Actions {
		names = append(names, action.Server)
	}
	plan.Limit = strings.Join(names, ",")
	return plan
}
