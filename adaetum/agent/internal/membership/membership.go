// Package membership is how host agents and the cluster agent know about each other.
//
// Adaetum runs the same binary in two places, and both are required:
//
//   - a HOST agent on every machine, outside Kubernetes. It must keep working,
//     and stay reachable for troubleshooting and direct host actions, when
//     Kubernetes is down. It therefore never depends on the cluster agent.
//   - a CLUSTER agent, a pod, once Kubernetes is up with two or more nodes. It is
//     the front door: one endpoint that collects what the hosts report and shows
//     the cluster as a whole. It is a convenience layered over the hosts, never a
//     replacement for them.
//
// Hosts tell the cluster about themselves by appending events to their local
// database (internal/store) and sending the ones not yet acknowledged. This
// package holds the three pieces that sit between those two ends:
//
//   - the check-in event every host records, so the cluster can tell which servers
//     run the agent (this file);
//   - Assess, which turns check-ins plus the list of cluster servers into the
//     "servers not running the agent" view the dashboard shows (assess.go);
//   - the delivery contract: a Sender loop on the host and an Ingester on the
//     cluster (transport.go).
//
// STATUS: the check-in, Assess, the host-side Sender, enrollment, and the signed
// HTTPS transport are real and tested. The receiving side is internal/cluster.
// Trust between the two is described in docs/auth.md.
package membership

import "time"

// CheckInKind is the event kind of a check-in. It is a contract between host and
// cluster agents: change it and old hosts stop being recognized.
const CheckInKind = "agent.checkin"

// CheckInPayload is what a host says about itself. Keep it small and stable: it
// is stored in every host's outbox and read by agents of different versions, so
// fields may be added but not renamed or removed.
type CheckInPayload struct {
	// Hostname is how the host names itself; it is matched against Kubernetes node
	// names, so it should be the machine's short name, not a display label.
	Hostname string `json:"hostname"`
	// Version is the agent build, so the dashboard can flag hosts running an old one.
	Version string `json:"version"`
}

// CheckIn is the latest check-in the cluster agent holds for one host. It is
// derived from the event stream; nothing edits it directly.
type CheckIn struct {
	HostID   string `json:"hostId"`
	Hostname string `json:"hostname"`
	Version  string `json:"version"`
	// At is when the host recorded the check-in (the host's clock), not when the
	// cluster received it.
	At time.Time `json:"at"`
}
