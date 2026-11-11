package cluster

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/identity"
	"github.com/Adaetum/Adaetum/agent/internal/membership"
	"github.com/Adaetum/Adaetum/agent/internal/store"
)

func enrolled(t *testing.T, r *Registry, hostname string, nodes []string) string {
	t.Helper()
	key, err := identity.LoadOrCreateHostKey(filepath.Join(t.TempDir(), "id"))
	if err != nil {
		t.Fatal(err)
	}
	secret, _, err := r.MintJoinToken(context.Background(), "test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	id := randomID(t)
	trust, reason, err := r.Enroll(context.Background(), membership.EnrollRequest{HostID: id, Hostname: hostname, PublicKey: key.PublicKey(), JoinToken: secret}, nodes, true, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s enrolled as %s: %s", hostname, trust, reason)
	return id
}

func TestReconcileDropsAHostKubernetesNoLongerListsOnlyAfterTwoReadsAndKeepsOthers(t *testing.T) {
	r, err := OpenRegistry(filepath.Join(t.TempDir(), "cluster.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	ctx := context.Background()
	nodes := []string{"node-a", "node-b"}
	a := enrolled(t, r, "node-a", nodes)
	b := enrolled(t, r, "node-b", nodes)
	byHand := enrolled(t, r, "laptop", nodes) // pending: never a node
	if err := r.Approve(ctx, byHand, "operator"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Ingest(ctx, b, []store.Event{{HostID: b, Seq: 1, Kind: membership.CheckInKind, At: clock, Payload: []byte("{}")}}); err != nil {
		t.Fatal(err)
	}
	gone := map[string]bool{}

	// node-b is deleted from Kubernetes. One read is not enough to drop it.
	if removed, err := r.Reconcile(ctx, []string{"node-a"}, gone); err != nil || len(removed) != 0 {
		t.Fatalf("first read: %v %v", removed, err)
	}
	if _, ok := r.PublicKey(b); !ok {
		t.Fatal("dropped after a single read")
	}
	// A read where it is back resets the count.
	if _, err := r.Reconcile(ctx, []string{"node-a", "node-b"}, gone); err != nil {
		t.Fatal(err)
	}
	if removed, _ := r.Reconcile(ctx, []string{"node-a"}, gone); len(removed) != 0 {
		t.Fatalf("the count was not reset: %v", removed)
	}
	removed, err := r.Reconcile(ctx, []string{"node-a"}, gone)
	if err != nil || len(removed) != 1 || removed[0] != "node-b" {
		t.Fatalf("second read: %v %v", removed, err)
	}
	if _, ok := r.PublicKey(b); ok {
		t.Fatal("the removed host can still speak")
	}
	if _, ok := r.PublicKey(a); !ok {
		t.Fatal("a listed node was dropped")
	}
	if _, ok := r.PublicKey(byHand); !ok {
		t.Fatal("a host a person approved was dropped for not being a node")
	}
	var events int
	_ = r.DB().QueryRow(`SELECT count(*) FROM events WHERE host_id = ?`, b).Scan(&events)
	if events != 0 {
		t.Fatalf("%d events of the removed host were left behind", events)
	}
}

func TestReconcileNeverActsOnAnEmptyNodeList(t *testing.T) {
	r, err := OpenRegistry(filepath.Join(t.TempDir(), "cluster.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	a := enrolled(t, r, "node-a", []string{"node-a"})
	gone := map[string]bool{}
	for i := 0; i < 3; i++ {
		if removed, err := r.Reconcile(context.Background(), nil, gone); err != nil || len(removed) != 0 {
			t.Fatalf("empty list: %v %v", removed, err)
		}
	}
	if _, ok := r.PublicKey(a); !ok {
		t.Fatal("an empty node list removed a host")
	}
}
