package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/identity"
	"github.com/Adaetum/Adaetum/agent/internal/membership"
	"github.com/Adaetum/Adaetum/agent/internal/store"
)

// NodeLister returns the servers Kubernetes lists as cluster nodes. It is how the
// cluster agent recognises a machine the cluster already trusts. The cluster agent
// asks Kubernetes rather than keeping its own list, so Kubernetes stays the one
// source of truth for which servers belong to the cluster.
type NodeLister func(ctx context.Context) ([]string, error)

// API is the cluster agent's HTTP surface. Its endpoints sit behind three different
// kinds of access, deliberately:
//
//	enroll              no login, but a valid one-time join token inside the request
//	events              a signature from an approved host (machine to machine)
//	cluster, join-tokens,
//	approve             a person, through RequireUser (user access)
type API struct {
	Registry *Registry
	Nodes    NodeLister
	// NodeKeys, when set, is how a host is vouched for without a token. Optional: without it
	// every enrollment needs a join token, as before.
	NodeKeys NodeKeyLister
	// RequireUser wraps a handler so only a signed-in person reaches it.
	RequireUser func(http.Handler) http.Handler
	// Membership tunes the missing/stale/outdated rules.
	Membership membership.Options
	Now        func() time.Time

	verifier *identity.Verifier
}

// Request size limits. Enrollment is small; an event batch is bounded by the
// registry's own limits and this cap on the body.
const (
	maxEnrollBody = 8 * 1024
	maxEventsBody = 2 * 1024 * 1024
)

// Handler returns the routes.
func (a *API) Handler() http.Handler {
	if a.Now == nil {
		a.Now = time.Now
	}
	a.verifier = &identity.Verifier{Now: a.Now, PublicKey: a.Registry.PublicKey}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/enroll", a.enroll)
	mux.HandleFunc("POST /api/v1/events", a.events)
	mux.Handle("GET /api/v1/cluster", a.RequireUser(http.HandlerFunc(a.clusterView)))
	mux.Handle("POST /api/v1/join-tokens", a.RequireUser(http.HandlerFunc(a.mintToken)))
	mux.Handle("POST /api/v1/hosts/{id}/approve", a.RequireUser(http.HandlerFunc(a.approve)))
	return mux
}

func (a *API) enroll(w http.ResponseWriter, r *http.Request) {
	var request membership.EnrollRequest
	if !decode(w, r, maxEnrollBody, &request) {
		return
	}
	// If Kubernetes cannot be read, enrollment still works but never auto-approves:
	// Decide turns "unknown" into "ask a person".
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	nodes, nodesErr := a.Nodes(ctx)

	// Vouching comes from Kubernetes alone: the node this host names must carry the very key it
	// presented. Nothing in the request can make it true.
	vouched := false
	if a.NodeKeys != nil && nodesErr == nil && request.PublicKey != "" {
		if keys, err := a.NodeKeys(ctx); err == nil {
			vouched = keys[identity.ShortName(request.Hostname)] == request.PublicKey
		}
	}

	trust, reason, err := a.Registry.Enroll(ctx, request, nodes, nodesErr == nil, vouched)
	if err != nil {
		http.Error(w, "enrollment failed", http.StatusInternalServerError)
		return
	}
	status := http.StatusOK
	if trust == identity.Rejected {
		status = http.StatusForbidden
	}
	writeJSON(w, status, map[string]string{"status": string(trust), "reason": reason})
}

func (a *API) events(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxEventsBody))
	if err != nil {
		http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		return
	}
	hostID, err := a.verifier.Verify(r, body)
	if err != nil {
		// One answer for every failure, so a caller learns nothing about which check failed.
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}
	var batch struct {
		Events []store.Event `json:"events"`
	}
	if err := json.Unmarshal(body, &batch); err != nil {
		http.Error(w, "the request is not valid JSON", http.StatusBadRequest)
		return
	}
	acked, err := a.Registry.Ingest(r.Context(), hostID, batch.Events)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"acked": acked})
}

// ClusterView is the dashboard's answer to "which servers are not running the agent?".
type ClusterView struct {
	Servers   []membership.ServerStatus `json:"servers"`
	Unmatched []membership.CheckIn      `json:"unmatched"`
	Hosts     []HostSummary             `json:"hosts"`
	Pending   []HostSummary             `json:"pending"`
	// Install is what would bring every server up to date: where to install or
	// upgrade the agent. Running it is not built yet (see membership.PlanInstall).
	Install membership.InstallPlan `json:"install"`
	// NodesError is set when Kubernetes could not be read. The host list is still
	// shown; the server comparison is not, because it would call every server missing.
	NodesError string `json:"nodesError,omitempty"`
}

func (a *API) clusterView(w http.ResponseWriter, r *http.Request) {
	hosts, err := a.Registry.Hosts(r.Context())
	if err != nil {
		http.Error(w, "cannot read the cluster", http.StatusInternalServerError)
		return
	}
	view := ClusterView{Servers: []membership.ServerStatus{}, Unmatched: []membership.CheckIn{}, Hosts: []HostSummary{}, Pending: []HostSummary{}, Install: membership.InstallPlan{Actions: []membership.InstallAction{}}}
	var checkIns []membership.CheckIn
	for _, host := range hosts {
		if host.Status == string(identity.Pending) {
			view.Pending = append(view.Pending, host)
			continue
		}
		view.Hosts = append(view.Hosts, host)
		if host.LastCheckIn != nil {
			checkIns = append(checkIns, membership.CheckIn{HostID: host.HostID, Hostname: host.Hostname, Version: host.Version, At: *host.LastCheckIn})
		}
	}

	nodes, err := a.Nodes(r.Context())
	if err != nil {
		view.NodesError = err.Error()
	} else {
		assessment := membership.Assess(nodes, checkIns, a.Now(), a.Membership)
		view.Servers = assessment.Servers
		view.Install = membership.PlanInstall(assessment)
		if assessment.Unmatched != nil {
			view.Unmatched = assessment.Unmatched
		}
	}
	writeJSON(w, http.StatusOK, view)
}

func (a *API) mintToken(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Note       string `json:"note"`
		TTLMinutes int    `json:"ttlMinutes"`
	}
	if !decode(w, r, 4*1024, &request) {
		return
	}
	if request.TTLMinutes == 0 {
		request.TTLMinutes = 60
	}
	secret, expires, err := a.Registry.MintJoinToken(r.Context(), request.Note, time.Duration(request.TTLMinutes)*time.Minute)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// This is the only time the secret exists in readable form.
	writeJSON(w, http.StatusOK, map[string]any{"secret": secret, "expiresAt": expires})
}

func (a *API) approve(w http.ResponseWriter, r *http.Request) {
	err := a.Registry.Approve(r.Context(), r.PathValue("id"), "operator")
	switch {
	case errors.Is(err, ErrNotPending):
		http.Error(w, err.Error(), http.StatusNotFound)
	case err != nil:
		http.Error(w, "cannot approve", http.StatusInternalServerError)
	default:
		writeJSON(w, http.StatusOK, map[string]string{"status": string(identity.Approved)})
	}
}

func decode(w http.ResponseWriter, r *http.Request, limit int64, into any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit)).Decode(into); err != nil {
		http.Error(w, "the request is not valid", http.StatusBadRequest)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
