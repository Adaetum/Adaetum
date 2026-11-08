package cluster

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/identity"
	"github.com/Adaetum/Adaetum/agent/internal/membership"
	"github.com/Adaetum/Adaetum/agent/internal/store"
)

// A real host (database, key, sender) talking to a real cluster API over TLS with a
// pinned certificate: the whole machine-to-machine path, nothing faked in between.
func TestAHostEnrollsAndDeliversItsCheckInsOverPinnedTLS(t *testing.T) {
	h := newHarness(t)
	tlsServer := httptest.NewTLSServer(h.server.Config.Handler)
	t.Cleanup(tlsServer.Close)
	sum := sha256.Sum256(tlsServer.Certificate().Raw)
	link := membership.Link{URL: tlsServer.URL, Fingerprint: hex.EncodeToString(sum[:])}

	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	key, err := identity.LoadOrCreateHostKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	result, err := membership.Enroll(ctx, link, membership.EnrollRequest{HostID: db.HostID(), Hostname: "node-50449", PublicKey: key.PublicKey(), JoinToken: h.mint()})
	if err != nil || result.Status != "approved" {
		t.Fatalf("enrollment: %+v %v", result, err)
	}

	db.Append(ctx, membership.CheckInKind, membership.CheckInPayload{Hostname: "node-50449", Version: "0.1.0"})
	db.Append(ctx, membership.CheckInKind, membership.CheckInPayload{Hostname: "node-50449", Version: "0.1.0"})
	transport := &membership.HTTPTransport{Link: link, HostID: db.HostID(), Key: key, Now: func() time.Time { return h.now }}
	if err := membership.SendPending(ctx, db, transport, 10); err != nil {
		t.Fatalf("delivery: %v", err)
	}
	if pending, _ := db.Pending(ctx, 10); len(pending) != 0 {
		t.Fatalf("everything should be delivered: %+v", pending)
	}

	view := clusterView(h)
	found := false
	for _, server := range view.Servers {
		if server.Server == "node-50449" && server.Status == membership.StatusOK {
			found = true
		}
	}
	if !found {
		t.Fatalf("the cluster must now see node-50449 as running the agent: %+v", view.Servers)
	}
}

func TestAHostRefusesAClusterAgentThatIsNotTheOnePinned(t *testing.T) {
	h := newHarness(t)
	tlsServer := httptest.NewTLSServer(h.server.Config.Handler)
	t.Cleanup(tlsServer.Close)
	wrong := membership.Link{URL: tlsServer.URL, Fingerprint: "00" + hex.EncodeToString(make([]byte, 31))}

	key, _ := identity.LoadOrCreateHostKey(t.TempDir())
	_, err := membership.Enroll(context.Background(), wrong, membership.EnrollRequest{HostID: randomID(t), Hostname: "x", PublicKey: key.PublicKey(), JoinToken: h.mint()})
	if err == nil {
		t.Fatal("a host must not hand its join token to an endpoint whose certificate it did not pin")
	}
	hosts, _ := h.registry.Hosts(context.Background())
	if len(hosts) != 0 {
		t.Fatal("nothing may have reached the cluster agent")
	}
}
