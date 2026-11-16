package cluster

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/membership"
	"github.com/Adaetum/Adaetum/agent/internal/store"
	"github.com/Adaetum/Adaetum/agent/internal/tlscert"
)

// clusterCertificate makes a certificate the way the cluster agent does.
func clusterCertificate(t *testing.T) (*tlscert.Manager, string) {
	t.Helper()
	manager := tlscert.NewManager(t.TempDir())
	_, leaf, err := manager.Current()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(leaf.Raw)
	return manager, hex.EncodeToString(sum[:])
}

// fakeHost is a host agent serving its read-only runs route behind the real verifier.
func fakeHost(t *testing.T, hostID, pinnedCluster string) (*httptest.Server, *membership.Endpoint) {
	t.Helper()
	verifier := &membership.ClusterVerifier{
		Link:   func() (membership.Link, bool) { return membership.Link{Fingerprint: pinnedCluster}, true },
		HostID: hostID,
		Now:    time.Now,
	}
	mux := http.NewServeMux()
	mux.Handle(ClusterRunsPath, verifier.Guard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"runs":[{"id":"r1","procedure":"unseal-openbao","state":"succeeded"}]}`))
	})))
	server := httptest.NewTLSServer(mux)
	t.Cleanup(server.Close)
	parsed, _ := url.Parse(server.URL)
	host, portText, _ := net.SplitHostPort(parsed.Host)
	port, _ := strconv.Atoi(portText)
	// Hosts report the key pin; the older whole-certificate pin is covered in tlscert's tests.
	return server, &membership.Endpoint{Port: port, Fingerprint: tlscert.KeyPin(server.Certificate()), Addresses: []string{host}}
}

func callerFor(h *harness, signer *tlscert.Manager) *HostCaller {
	return &HostCaller{
		Registry: h.registry, allowLoopback: true, Timeout: 2 * time.Second,
		Sign: func(r *http.Request, hostID string) error {
			cert, err := signer.GetCertificate(nil)
			if err != nil {
				return err
			}
			return membership.SignAsCluster(r, cert, hostID, time.Now())
		},
	}
}

func fleet(t *testing.T, h *harness, signer *tlscert.Manager) []ServerRuns {
	t.Helper()
	handler := &FleetRuns{Hosts: callerFor(h, signer)}
	recorder := httptest.NewRecorder()
	handler.serve(recorder, httptest.NewRequest("GET", "/api/v1/fleet/runs", nil).WithContext(context.Background()))
	var body struct {
		Servers []ServerRuns `json:"servers"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Servers
}

func TestFleetRunsReadsEachServersRunsWithTheClustersPinnedCertificate(t *testing.T) {
	h := newHarness(t)
	signer, clusterPin := clusterCertificate(t)
	a := h.newAgent("node-50449")
	_, endpoint := fakeHost(t, a.id, clusterPin)
	a.enroll(h.mint())
	a.send([]store.Event{a.event(1, membership.CheckInKind, membership.CheckInPayload{Hostname: "node-50449", Version: "1", Endpoint: endpoint})})

	servers := fleet(t, h, signer)
	if len(servers) != 1 || servers[0].Error != "" || len(servers[0].Runs) != 1 || !strings.Contains(string(servers[0].Runs[0]), "unseal-openbao") {
		t.Fatalf("expected the host's one run: %+v", servers)
	}
}

func TestAHostRefusesACertificateItNeverPinned(t *testing.T) {
	h := newHarness(t)
	_, otherPin := clusterCertificate(t)
	impostor, _ := clusterCertificate(t)
	a := h.newAgent("node-50449")
	_, endpoint := fakeHost(t, a.id, otherPin)
	a.enroll(h.mint())
	a.send([]store.Event{a.event(1, membership.CheckInKind, membership.CheckInPayload{Hostname: "node-50449", Version: "1", Endpoint: endpoint})})

	servers := fleet(t, h, impostor)
	if len(servers) != 1 || len(servers[0].Runs) != 0 || !strings.Contains(servers[0].Error, "didn't accept") {
		t.Fatalf("a request signed by an unpinned certificate must be refused: %+v", servers)
	}
}

func TestTheClusterRefusesAHostWhoseCertificateChanged(t *testing.T) {
	h := newHarness(t)
	signer, clusterPin := clusterCertificate(t)
	a := h.newAgent("node-50449")
	_, endpoint := fakeHost(t, a.id, clusterPin)
	endpoint.Fingerprint = strings.Repeat("ab", 32) // what the host reported is no longer what it serves
	a.enroll(h.mint())
	a.send([]store.Event{a.event(1, membership.CheckInKind, membership.CheckInPayload{Hostname: "node-50449", Version: "1", Endpoint: endpoint})})

	servers := fleet(t, h, signer)
	if len(servers) != 1 || !strings.Contains(servers[0].Error, "certificate changed") {
		t.Fatalf("a changed certificate must be named, not trusted: %+v", servers)
	}
}

func TestAServerThatHasNotSaidWhereItIsIsSaidSo(t *testing.T) {
	h := newHarness(t)
	signer, _ := clusterCertificate(t)
	a := h.newAgent("node-50449")
	a.enroll(h.mint())
	a.send([]store.Event{a.event(1, membership.CheckInKind, membership.CheckInPayload{Hostname: "node-50449", Version: "1"})})
	servers := fleet(t, h, signer)
	if len(servers) != 1 || !strings.Contains(servers[0].Error, "hasn't said where") {
		t.Fatalf("expected an explanation: %+v", servers)
	}
}

func TestOnlyApprovedServersAreAsked(t *testing.T) {
	h := newHarness(t)
	signer, _ := clusterCertificate(t)
	pending := h.newAgent("not-a-node") // not listed by Kubernetes, so it waits for a person
	pending.enroll(h.mint())
	if servers := fleet(t, h, signer); len(servers) != 0 {
		t.Fatalf("a pending host must not be called: %+v", servers)
	}
}

func TestPreferNeverCallsTheClusterItselfOrLinkLocal(t *testing.T) {
	got := prefer([]string{"127.0.0.1", "169.254.1.1", "100.64.0.2", "192.168.2.5", "::1", "nonsense"}, false)
	if len(got) != 2 || got[0] != "192.168.2.5" || got[1] != "100.64.0.2" {
		t.Fatalf("unexpected addresses: %v", got)
	}
}

func TestFirstAnswersFromTheFirstServerThatCanBeReached(t *testing.T) {
	h := newHarness(t)
	signer, clusterPin := clusterCertificate(t)

	// One server has never said where it is; the other answers.
	silent := h.newAgent("node-50449")
	silent.enroll(h.mint())
	silent.send([]store.Event{silent.event(1, membership.CheckInKind, membership.CheckInPayload{Hostname: "node-50449", Version: "1"})})
	reachable := h.newAgent("node-56843")
	_, endpoint := fakeHost(t, reachable.id, clusterPin)
	reachable.enroll(h.mint())
	reachable.send([]store.Event{reachable.event(1, membership.CheckInKind, membership.CheckInPayload{Hostname: "node-56843", Version: "1", Endpoint: endpoint})})

	body, hostname, err := callerFor(h, signer).First(t.Context(), ClusterRunsPath)
	if err != nil || hostname != "node-56843" || !strings.Contains(string(body), "unseal-openbao") {
		t.Fatalf("host=%q err=%v body=%s", hostname, err, body)
	}
}

func TestFirstSaysSoWhenNoServerCanBeReached(t *testing.T) {
	h := newHarness(t)
	signer, _ := clusterCertificate(t)
	if _, _, err := callerFor(h, signer).First(t.Context(), ClusterRunsPath); err == nil {
		t.Fatal("with no servers there is nothing to answer")
	}
}
