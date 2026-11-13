package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/cluster"
	"github.com/Adaetum/Adaetum/agent/internal/inspect"
	"github.com/Adaetum/Adaetum/agent/internal/tlscert"
)

// registerClusterReport serves the same /api/v1/inspect document a host agent serves, so
// every page that shows the cluster (overview, workloads, storage) works on the cluster
// agent too. It is built from Kubernetes alone, with the pod's read-only service account:
// the cluster agent has no host access, so Report.Host and the RKE2 service states are
// left empty. Details of one machine come from that machine's own agent.
//
// With -ssh (development) the report is read from that remote cluster instead.
func registerClusterReport(mux *http.ServeMux, sshTarget string) {
	var inspector *inspect.Inspector
	if sshTarget != "" {
		inspector = newInspector(sshTarget)
	} else if reader := cluster.InClusterReader(); reader != nil {
		inspector = inspect.NewClusterInspector(reader.Get, reader.Raw)
	} else {
		return // not in a cluster: the page keeps saying there is no report here
	}
	mux.HandleFunc("/api/v1/inspect", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(inspector.CollectCluster(ctx))
	})
}

// podCertificateIdentity is what the cluster agent's certificate covers when it runs in a
// pod: its Service (a name and the stable cluster address hosts connect to), and loopback.
//
// It must NOT cover the pod's own name or IP, which change on every restart. The default
// identity is "this machine's names and addresses", so a restarted pod got a new
// certificate, and every host that had pinned the old fingerprint refused to talk to it.
// The second return is false outside a pod, where the default stays right.
func podCertificateIdentity(getenv func(string) string) (func() tlscert.Identity, bool) {
	clusterIP := net.ParseIP(getenv("ADAETUM_CLUSTER_AGENT_SERVICE_HOST"))
	if clusterIP == nil {
		return nil, false
	}
	names := []string{"localhost", "adaetum-cluster-agent", "adaetum-cluster-agent.adaetum.svc", "adaetum-cluster-agent.adaetum.svc.cluster.local"}
	ips := []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback, clusterIP}
	return func() tlscert.Identity { return tlscert.Identity{Names: names, IPs: ips} }, true
}
