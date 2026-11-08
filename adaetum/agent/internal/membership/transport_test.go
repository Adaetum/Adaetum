package membership

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Adaetum/Adaetum/agent/internal/store"
)

func openStore(t *testing.T, events int) *store.Store {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for i := 0; i < events; i++ {
		if _, err := db.Append(context.Background(), CheckInKind, CheckInPayload{Hostname: "h", Version: "1"}); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// transportFunc adapts a function to Transport.
type transportFunc func(context.Context, []store.Event) (int64, error)

func (f transportFunc) Send(ctx context.Context, events []store.Event) (int64, error) {
	return f(ctx, events)
}

func ingesting(db *store.Store, m *memoryIngester) Transport {
	return transportFunc(func(ctx context.Context, events []store.Event) (int64, error) {
		return m.Ingest(ctx, db.HostID(), events)
	})
}

func TestEveryPendingEventIsDeliveredInBatchesAndTheCursorAdvances(t *testing.T) {
	db := openStore(t, 7)
	cluster := newMemoryIngester()
	if err := SendPending(context.Background(), db, ingesting(db, cluster), 3); err != nil {
		t.Fatal(err)
	}
	if acked, _ := db.AckedSeq(context.Background()); acked != 7 {
		t.Fatalf("cursor at %d, want 7", acked)
	}
	if pending, _ := db.Pending(context.Background(), 10); len(pending) != 0 {
		t.Fatalf("nothing should remain: %+v", pending)
	}
	if cluster.acks[db.HostID()] != 7 {
		t.Fatalf("the cluster must hold all seven: %d", cluster.acks[db.HostID()])
	}
}

func TestAnUnreachableClusterLeavesEventsWaitingAndIsNotFatal(t *testing.T) {
	db := openStore(t, 3)
	down := transportFunc(func(context.Context, []store.Event) (int64, error) { return 0, errors.New("connection refused") })
	if err := SendPending(context.Background(), db, down, 10); err == nil {
		t.Fatal("the failure must be reported")
	}
	if pending, _ := db.Pending(context.Background(), 10); len(pending) != 3 {
		t.Fatalf("events must wait in the outbox: %+v", pending)
	}

	// The cluster comes up: everything is delivered, nothing lost.
	cluster := newMemoryIngester()
	if err := SendPending(context.Background(), db, ingesting(db, cluster), 10); err != nil || cluster.acks[db.HostID()] != 3 {
		t.Fatalf("recovery failed: %v %d", err, cluster.acks[db.HostID()])
	}
}

func TestARepeatedDeliveryIsHarmless(t *testing.T) {
	db := openStore(t, 2)
	cluster := newMemoryIngester()
	pending, _ := db.Pending(context.Background(), 10)
	cluster.Ingest(context.Background(), db.HostID(), pending)
	// The host never heard the reply, so it sends the same events again.
	if err := SendPending(context.Background(), db, ingesting(db, cluster), 10); err != nil {
		t.Fatal(err)
	}
	if len(cluster.seen[db.HostID()]) != 2 || cluster.acks[db.HostID()] != 2 {
		t.Fatalf("a retry must not duplicate: %+v", cluster.seen)
	}
}

func TestAnAcknowledgementBeyondWhatWasSentIsClamped(t *testing.T) {
	db := openStore(t, 2)
	liar := transportFunc(func(context.Context, []store.Event) (int64, error) { return 9999, nil })
	if err := SendPending(context.Background(), db, liar, 10); err != nil {
		t.Fatal(err)
	}
	if acked, _ := db.AckedSeq(context.Background()); acked != 2 {
		t.Fatalf("the cursor must never pass the newest event sent: %d", acked)
	}
	// An event recorded afterwards must still be pending.
	db.Append(context.Background(), CheckInKind, nil)
	if pending, _ := db.Pending(context.Background(), 10); len(pending) != 1 {
		t.Fatalf("a bad reply must not hide future events: %+v", pending)
	}
}

func TestAPartialAcknowledgementKeepsTheRestPending(t *testing.T) {
	db := openStore(t, 4)
	partial := transportFunc(func(_ context.Context, events []store.Event) (int64, error) { return events[1].Seq, nil })
	if err := SendPending(context.Background(), db, partial, 10); err == nil {
		t.Fatal("a partial acknowledgement is reported so it is retried")
	}
	if pending, _ := db.Pending(context.Background(), 10); len(pending) != 2 || pending[0].Seq != 3 {
		t.Fatalf("events 3 and 4 must remain: %+v", pending)
	}
}

func TestAnAcknowledgementOfNothingIsAnError(t *testing.T) {
	db := openStore(t, 2)
	none := transportFunc(func(context.Context, []store.Event) (int64, error) { return 0, nil })
	if err := SendPending(context.Background(), db, none, 10); err == nil {
		t.Fatal("zero progress must not look like success")
	}
}
