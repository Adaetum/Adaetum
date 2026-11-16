package membership

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/tlscert"
)

func clusterSetup(t *testing.T) (*tlscert.Manager, *ClusterVerifier, time.Time) {
	t.Helper()
	manager := tlscert.NewManager(t.TempDir())
	_, leaf, err := manager.Current()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(leaf.Raw)
	now := time.Now()
	return manager, &ClusterVerifier{
		Link:   func() (Link, bool) { return Link{Fingerprint: hex.EncodeToString(sum[:])}, true },
		HostID: "host-a",
		Now:    func() time.Time { return now },
	}, now
}

func signed(t *testing.T, manager *tlscert.Manager, path, hostID string, now time.Time) *http.Request {
	t.Helper()
	request := httptest.NewRequest("GET", path, nil)
	cert, _ := manager.GetCertificate(nil)
	if err := SignAsCluster(request, cert, hostID, now); err != nil {
		t.Fatal(err)
	}
	return request
}

func TestARequestSignedByThePinnedCertificateIsAcceptedOnce(t *testing.T) {
	manager, verifier, now := clusterSetup(t)
	request := signed(t, manager, "/api/v1/cluster/runs", "host-a", now)
	if err := verifier.Verify(request); err != nil {
		t.Fatalf("a valid request was refused: %v", err)
	}
	if err := verifier.Verify(request); err == nil {
		t.Fatal("the same request must not be accepted twice")
	}
}

func TestACapturedRequestCannotBeAimedElsewhere(t *testing.T) {
	manager, verifier, now := clusterSetup(t)

	other := signed(t, manager, "/api/v1/cluster/runs", "host-b", now)
	if verifier.Verify(other) == nil {
		t.Error("a request signed for another host must be refused")
	}
	moved := signed(t, manager, "/api/v1/cluster/runs", "host-a", now)
	moved.URL.Path = "/api/v1/runs"
	if verifier.Verify(moved) == nil {
		t.Error("a request moved to another path must be refused")
	}
	old := signed(t, manager, "/api/v1/cluster/runs", "host-a", now.Add(-5*time.Minute))
	if verifier.Verify(old) == nil {
		t.Error("a stale request must be refused")
	}
}

func TestAnotherCertificateOrNoLinkIsRefused(t *testing.T) {
	_, verifier, now := clusterSetup(t)
	stranger := tlscert.NewManager(t.TempDir())
	if verifier.Verify(signed(t, stranger, "/x", "host-a", now)) == nil {
		t.Error("a certificate this host never pinned must be refused")
	}
	verifier.Link = func() (Link, bool) { return Link{}, false }
	manager, _, _ := clusterSetup(t)
	if verifier.Verify(signed(t, manager, "/x", "host-a", now)) == nil {
		t.Error("a host that has not joined has no cluster to accept")
	}
}

func TestTheGuardOnlyLetsReadsThrough(t *testing.T) {
	manager, verifier, now := clusterSetup(t)
	handler := verifier.Guard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))

	read := httptest.NewRecorder()
	handler.ServeHTTP(read, signed(t, manager, "/r", "host-a", now))
	if read.Code != http.StatusOK {
		t.Errorf("a signed read must pass: %d", read.Code)
	}
	write := signed(t, manager, "/r", "host-a", now)
	write.Method = http.MethodPost
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, write)
	if denied.Code != http.StatusUnauthorized {
		t.Errorf("a write must never pass this guard: %d", denied.Code)
	}
}

func TestAKeyPinSurvivesTheClusterRemakingItsCertificateAndAnOldPinStillWorks(t *testing.T) {
	manager, _, now := clusterSetup(t)
	_, leaf, _ := manager.Current()
	wholeCert := hex.EncodeToString(func() []byte { s := sha256.Sum256(leaf.Raw); return s[:] }())

	for name, pin := range map[string]string{"key pin": tlscert.KeyPin(leaf), "pre-key-pin link": wholeCert} {
		verifier := &ClusterVerifier{Link: func() (Link, bool) { return Link{Fingerprint: pin}, true }, HostID: "host-a", Now: func() time.Time { return now }}
		if err := verifier.Verify(signed(t, manager, "/x", "host-a", now)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
