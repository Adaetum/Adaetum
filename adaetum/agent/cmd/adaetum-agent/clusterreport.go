package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/automation"
	"github.com/Adaetum/Adaetum/agent/internal/cluster"
	"github.com/Adaetum/Adaetum/agent/internal/inspect"
	"github.com/Adaetum/Adaetum/agent/internal/openbao"
	"github.com/Adaetum/Adaetum/agent/internal/tlscert"
)

// registerClusterReport serves the same /api/v1/inspect document a host agent serves, so
// every page that shows the cluster (overview, workloads, storage) works on the cluster
// agent too. It is built from Kubernetes alone, with the pod's read-only service account:
// the cluster agent has no host access, so Report.Host and the RKE2 service states are
// left empty. Details of one machine come from that machine's own agent.
//
// With -ssh (development) the report is read from that remote cluster instead.
func registerClusterReport(mux *http.ServeMux, sshTarget, openbaoURL string, guard func(http.Handler) http.Handler) {
	var inspector *inspect.Inspector
	if sshTarget != "" {
		inspector = newInspector(sshTarget)
	} else if reader := cluster.InClusterReader(); reader != nil {
		inspector = inspect.NewClusterInspector(reader.Get, reader.Raw)
		if openbaoURL != "" {
			inspector.Vault = func(ctx context.Context) *inspect.Vault {
				status, err := openbao.SealStatus(ctx, openbaoURL, &http.Client{Timeout: 5 * time.Second})
				return &inspect.Vault{Reachable: err == nil, Sealed: err == nil && status.Sealed}
			}
		}
	} else {
		return // not in a cluster: the page keeps saying there is no report here
	}
	report := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(inspector.CollectCluster(ctx))
	})
	// The report names every node and workload, so it needs a login like the rest of the
	// API. It is also what the page asks first: a 401 here is how the page learns to show
	// the sign-in screen before anything else.
	if guard != nil {
		mux.Handle("/api/v1/inspect", guard(report))
		return
	}
	mux.Handle("/api/v1/inspect", report)
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

// clusterAutomationSource lets the Automation page work on the cluster agent: the runner's
// Deployment and pod come from Kubernetes with the pod's read-only service account, like the
// rest of the cluster report. What lives INSIDE the runner pod (its run log, playbook files and
// platform profile) can only be read by running a command in it, which this agent's service
// account does not and must not do; each of those parts reports that reason and the page shows
// it, while everything else still reports.
type clusterAutomationSource struct {
	get func(context.Context, string, ...string) ([]byte, error)
	// why says why no server's agent could supply what is inside the runner pod; empty means
	// the default explanation.
	why string
}

func (s clusterAutomationSource) KubectlJSON(ctx context.Context, resources string, extra ...string) ([]byte, error) {
	return s.get(ctx, resources, extra...)
}

func (s clusterAutomationSource) ReadRunnerPod(context.Context, string, inspect.RunnerRead, string) (string, error) {
	if s.why != "" {
		return "", errors.New("no server's agent could read it for the cluster (" + s.why + ")")
	}
	return "", errors.New("the cluster agent reads Kubernetes only and cannot read inside the runner pod")
}

// registerClusterAutomation serves /api/v1/automation on the cluster agent (see
// clusterAutomationSource for what it can and cannot show). With -ssh (development) the
// remote host's inspector answers instead, as it does for the report.
func registerClusterAutomation(mux *http.ServeMux, sshTarget string, guard func(http.Handler) http.Handler, hosts *cluster.HostCaller) {
	var reader *cluster.KubeReader
	if sshTarget == "" {
		if reader = cluster.InClusterReader(); reader == nil {
			return
		}
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		w.Header().Set("Content-Type", "application/json")
		if sshTarget != "" {
			_ = json.NewEncoder(w).Encode((&automation.Collector{Source: newInspector(sshTarget), Now: time.Now}).Collect(ctx))
			return
		}
		// The runner's run history, playbooks and platform profile live inside the runner pod,
		// which only a server's own agent (with its kubectl) may read. Ask the servers' agents
		// first; if none can answer, show what Kubernetes alone says and why the rest is missing.
		why := ""
		if hosts != nil {
			body, _, err := hosts.First(ctx, cluster.ClusterAutomationPath)
			if err == nil {
				_, _ = w.Write(body)
				return
			}
			why = err.Error()
		}
		collector := &automation.Collector{Source: clusterAutomationSource{get: reader.Get, why: why}, Now: time.Now}
		_ = json.NewEncoder(w).Encode(collector.Collect(ctx))
	})
	if guard != nil {
		mux.Handle("/api/v1/automation", guard(handler))
		return
	}
	mux.Handle("/api/v1/automation", handler)
}
