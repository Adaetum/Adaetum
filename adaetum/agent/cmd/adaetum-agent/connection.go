package main

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/membership"
)

// connectionReply is what the page's status indicator needs: which kind of agent it
// is talking to and, for a host, whether the cluster agent (the pod) is answering.
//
//	Role    "host" or "cluster"
//	Cluster (host only) "reachable", "unreachable", or "none" when this host has not
//	        joined a cluster agent, which is normal and not a fault.
type connectionReply struct {
	Role    string `json:"role"`
	Cluster string `json:"cluster,omitempty"`
}

// connectionCacheFor bounds how often a host probes the pod. Several open pages poll
// every 30 seconds each; they should share one answer, not each open a connection.
const connectionCacheFor = 15 * time.Second

// registerHostConnection serves GET /api/v1/connection on a host agent. It is read-only
// and sits behind the same login as the rest of the API (see requireLogin).
func registerHostConnection(mux *http.ServeMux, dataDir string) {
	var (
		mu       sync.Mutex
		checked  time.Time
		lastSeen string
	)
	mux.HandleFunc("GET /api/v1/connection", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if time.Since(checked) > connectionCacheFor {
			lastSeen = probeCluster(r.Context(), dataDir)
			checked = time.Now()
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(connectionReply{Role: "host", Cluster: lastSeen})
	})
}

func probeCluster(ctx context.Context, dataDir string) string {
	if dataDir == "" {
		return "none"
	}
	link, joined, err := membership.LoadLink(dataDir)
	if err != nil || !joined {
		return "none"
	}
	if link.Probe(ctx) != nil {
		return "unreachable"
	}
	return "reachable"
}

// registerClusterConnection is the cluster agent's side: if it answers, it is reachable.
func registerClusterConnection(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/connection", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(connectionReply{Role: "cluster"})
	})
}
