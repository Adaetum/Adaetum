package cluster

// The Runs page on the cluster agent: "reach out on load". Runs are the work a host's own
// agent does on that host, so the cluster agent holds none of its own. When a signed-in
// person opens the page it asks each approved host for its runs, live, and lists them
// by server.
//
// TRUST. The cluster calls a host only at the address and certificate pin the host itself
// reported in its signed check-in, and signs the request with the certificate every host
// already pinned (membership.SignAsCluster). The call is a read: the host's matching route
// is guarded as read-only, so nothing here can start, change or stop anything on a host.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/identity"
	"github.com/Adaetum/Adaetum/agent/internal/membership"
)

// The routes a host serves to its cluster agent. Both are reads: see membership/callback.go.
const (
	ClusterRunsPath       = "/api/v1/cluster/runs"
	ClusterAutomationPath = "/api/v1/cluster/automation"
)

// ServerRuns is one server's answer.
type ServerRuns struct {
	HostID   string            `json:"hostId"`
	Hostname string            `json:"hostname"`
	Runs     []json.RawMessage `json:"runs"`
	// Error is set, in words a person can act on, when the runs could not be read.
	Error string `json:"error,omitempty"`
}

// HostCaller makes the cluster agent's signed, read-only calls to its servers' agents.
type HostCaller struct {
	Registry *Registry
	// Sign adds the cluster's signature for a request to hostID.
	Sign func(r *http.Request, hostID string) error
	// Timeout bounds one address; zero means 3 seconds.
	Timeout time.Duration
	// Scheme is "https"; tests that use a plain server set "http".
	Scheme string
	// allowLoopback lets tests call a server on this machine; a real host never may be.
	allowLoopback bool
}

// Approved lists the servers that may be called.
func (c *HostCaller) Approved(ctx context.Context) ([]HostSummary, error) {
	hosts, err := c.Registry.Hosts(ctx)
	if err != nil {
		return nil, err
	}
	var approved []HostSummary
	for _, host := range hosts {
		if host.Status == string(identity.Approved) {
			approved = append(approved, host)
		}
	}
	return approved, nil
}

// Call reads path from one host's agent, trying each address it reported until one answers.
// The error is written for a person: it says what to do, not what the network said.
func (c *HostCaller) Call(ctx context.Context, host HostSummary, path string) ([]byte, error) {
	endpoint := host.Endpoint
	if endpoint == nil || endpoint.Port == 0 || endpoint.Fingerprint == "" || len(endpoint.Addresses) == 0 {
		return nil, errors.New("This server hasn't said where its agent can be reached yet. Its agent may need updating; it tells the cluster every few minutes.")
	}
	var last error
	for _, address := range prefer(endpoint.Addresses, c.allowLoopback) {
		body, err := c.fetch(ctx, host.HostID, address, endpoint, path)
		if err == nil {
			return body, nil
		}
		last = err
		if errors.Is(err, errRefused) || errors.Is(err, errPinned) {
			break // another address will give the same answer
		}
	}
	return nil, errors.New(friendly(last))
}

// First returns the first approved server's answer, trying them in turn. It is for what any
// one server can say for the whole cluster (the automation runner lives on one of them).
func (c *HostCaller) First(ctx context.Context, path string) (body []byte, hostname string, err error) {
	hosts, err := c.Approved(ctx)
	if err != nil {
		return nil, "", err
	}
	last := errors.New("no server has joined this cluster yet")
	for _, host := range hosts {
		body, err := c.Call(ctx, host, path)
		if err == nil {
			return body, host.Hostname, nil
		}
		last = fmt.Errorf("%s: %w", host.Hostname, err)
	}
	return nil, "", last
}

// FleetRuns serves GET /api/v1/fleet/runs.
type FleetRuns struct {
	Hosts       *HostCaller
	RequireUser func(http.Handler) http.Handler
}

// Register adds the route behind the sign-in guard.
func (f *FleetRuns) Register(mux *http.ServeMux) {
	var handler http.Handler = http.HandlerFunc(f.serve)
	if f.RequireUser != nil {
		handler = f.RequireUser(handler)
	}
	mux.Handle("GET /api/v1/fleet/runs", handler)
}

func (f *FleetRuns) serve(w http.ResponseWriter, r *http.Request) {
	approved, err := f.Hosts.Approved(r.Context())
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "the list of servers could not be read"})
		return
	}
	servers := make([]ServerRuns, len(approved))
	var wait sync.WaitGroup
	for index, host := range approved {
		wait.Add(1)
		go func() {
			defer wait.Done()
			servers[index] = f.ask(r.Context(), host)
		}()
	}
	wait.Wait()
	sort.SliceStable(servers, func(a, b int) bool { return servers[a].Hostname < servers[b].Hostname })
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"servers": servers})
}

// ask reads one host's runs.
func (f *FleetRuns) ask(ctx context.Context, host HostSummary) ServerRuns {
	result := ServerRuns{HostID: host.HostID, Hostname: host.Hostname, Runs: []json.RawMessage{}}
	raw, err := f.Hosts.Call(ctx, host, ClusterRunsPath)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	var body struct {
		Runs []json.RawMessage `json:"runs"`
	}
	if json.Unmarshal(raw, &body) != nil {
		result.Error = "This server's agent gave an answer the cluster could not read."
		return result
	}
	if body.Runs != nil {
		result.Runs = body.Runs
	}
	return result
}

var (
	errRefused = errors.New("refused")
	errPinned  = errors.New("pin")
)

func friendly(err error) string {
	switch {
	case errors.Is(err, errRefused):
		return "This server's agent didn't accept the cluster's request. It may not be joined to this cluster."
	case errors.Is(err, errPinned):
		return "This server's certificate changed since it last checked in. Try again in a few minutes."
	default:
		return "The cluster couldn't reach this server's agent."
	}
}

func (f *HostCaller) fetch(ctx context.Context, hostID, address string, endpoint *membership.Endpoint, path string) ([]byte, error) {
	timeout := f.Timeout
	if timeout == 0 {
		timeout = 3 * time.Second
	}
	scheme := f.Scheme
	if scheme == "" {
		scheme = "https"
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	url := scheme + "://" + net.JoinHostPort(address, strconv.Itoa(endpoint.Port)) + path
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if err := f.Sign(request, hostID); err != nil {
		return nil, err
	}
	client := membership.Link{URL: url, Fingerprint: endpoint.Fingerprint}.HTTPClient()
	client.Timeout = timeout
	response, err := client.Do(request)
	if err != nil {
		// The pin check is the only thing that fails with this text (see Link.client).
		if containsPin(err) {
			return nil, errPinned
		}
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusNotFound {
		return nil, errRefused
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	return io.ReadAll(io.LimitReader(response.Body, 4<<20))
}

func containsPin(err error) bool {
	return err != nil && strings.Contains(err.Error(), "pinned fingerprint")
}

// prefer puts private (LAN) addresses first: from inside a cluster they are the ones
// that usually answer, and a tailnet address is the one most likely to time out.
func prefer(addresses []string, allowLoopback bool) []string {
	// A host chooses what it reports, so the cluster only calls real unicast addresses:
	// never itself (loopback), never a link-local or unspecified one.
	var out []string
	for _, address := range addresses {
		ip := net.ParseIP(address)
		if ip != nil && (allowLoopback || !ip.IsLoopback()) && !ip.IsUnspecified() && !ip.IsLinkLocalUnicast() && !ip.IsMulticast() {
			out = append(out, address)
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return isPrivate(out[a]) && !isPrivate(out[b]) })
	if len(out) > 6 {
		out = out[:6]
	}
	return out
}

func isPrivate(address string) bool {
	ip := net.ParseIP(address)
	return ip != nil && ip.IsPrivate()
}
