package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func openTemp(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "data", "agent.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

func TestTheHostIDIsMadeOnceAndSurvivesReopening(t *testing.T) {
	first, path := openTemp(t)
	id := first.HostID()
	if len(id) != 32 {
		t.Fatalf("expected a 128-bit hex ID, got %q", id)
	}
	first.Close()

	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if second.HostID() != id {
		t.Fatalf("the host ID changed on reopen: %s then %s", id, second.HostID())
	}

	other, _ := openTemp(t)
	if other.HostID() == id {
		t.Fatal("two databases must not share a host ID")
	}
}

func TestEventsGetGaplessSequenceNumbersAndKeepTheirPayload(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	fixed := time.Date(2026, 11, 4, 4, 0, 0, 123_000_000, time.UTC)
	s.now = func() time.Time { return fixed }

	first, err := s.Append(ctx, "agent.checkin", map[string]string{"hostname": "node-a"})
	if err != nil {
		t.Fatal(err)
	}
	second, _ := s.Append(ctx, "cleanup.finished", map[string]int{"removed": 12})
	if first.Seq != 1 || second.Seq != 2 {
		t.Fatalf("sequence must start at 1 and not skip: %d, %d", first.Seq, second.Seq)
	}
	if first.ID() != s.HostID()+":1" {
		t.Fatalf("unexpected event ID: %s", first.ID())
	}

	pending, err := s.Pending(ctx, 10)
	if err != nil || len(pending) != 2 {
		t.Fatalf("expected two pending events: %v %v", pending, err)
	}
	if pending[0].Kind != "agent.checkin" || !pending[0].At.Equal(fixed) || pending[0].HostID != s.HostID() {
		t.Fatalf("event was not stored faithfully: %+v", pending[0])
	}
	var payload map[string]string
	if err := json.Unmarshal(pending[0].Payload, &payload); err != nil || payload["hostname"] != "node-a" {
		t.Fatalf("payload did not survive: %s", pending[0].Payload)
	}
}

func TestSequenceNumbersContinueAfterReopening(t *testing.T) {
	first, path := openTemp(t)
	ctx := context.Background()
	first.Append(ctx, "a", nil)
	first.Append(ctx, "a", nil)
	first.Close()

	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	event, err := second.Append(ctx, "a", nil)
	if err != nil || event.Seq != 3 {
		t.Fatalf("a restart must continue the sequence, got %d %v", event.Seq, err)
	}
}

func TestPendingIsOldestFirstAndLimited(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		s.Append(ctx, "k", i)
	}
	pending, _ := s.Pending(ctx, 3)
	if len(pending) != 3 || pending[0].Seq != 1 || pending[2].Seq != 3 {
		t.Fatalf("expected events 1 to 3: %+v", pending)
	}
}

func TestAcknowledgedEventsAreNoLongerPending(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		s.Append(ctx, "k", i)
	}
	if err := s.Ack(ctx, 2); err != nil {
		t.Fatal(err)
	}
	pending, _ := s.Pending(ctx, 10)
	if len(pending) != 2 || pending[0].Seq != 3 {
		t.Fatalf("events 1 and 2 are delivered: %+v", pending)
	}
}

func TestTheAckCursorOnlyMovesForwardAndCannotPassTheNewestEvent(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	s.Append(ctx, "k", nil)
	s.Append(ctx, "k", nil)
	s.Append(ctx, "k", nil)

	if err := s.Ack(ctx, 3); err != nil {
		t.Fatal(err)
	}
	if err := s.Ack(ctx, 1); err != nil {
		t.Fatalf("a late acknowledgement must be harmless: %v", err)
	}
	if acked, _ := s.AckedSeq(ctx); acked != 3 {
		t.Fatalf("the cursor moved backwards to %d", acked)
	}
	if err := s.Ack(ctx, 99); err == nil {
		t.Fatal("acknowledging an event that does not exist would hide future events from delivery")
	}
	// A new event after the cursor must be pending.
	s.Append(ctx, "k", nil)
	if pending, _ := s.Pending(ctx, 10); len(pending) != 1 || pending[0].Seq != 4 {
		t.Fatalf("event 4 must be pending: %+v", pending)
	}
}

func TestTheCursorSurvivesReopening(t *testing.T) {
	first, path := openTemp(t)
	ctx := context.Background()
	first.Append(ctx, "k", nil)
	first.Append(ctx, "k", nil)
	first.Ack(ctx, 1)
	first.Close()

	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if pending, _ := second.Pending(ctx, 10); len(pending) != 1 || pending[0].Seq != 2 {
		t.Fatalf("delivery progress was lost: %+v", pending)
	}
}

func TestConcurrentAppendsNeverShareOrSkipASequenceNumber(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	const writers, each = 8, 10
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				if _, err := s.Append(ctx, "k", i); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	pending, _ := s.Pending(ctx, writers*each+1)
	if len(pending) != writers*each {
		t.Fatalf("expected %d events, got %d", writers*each, len(pending))
	}
	for index, event := range pending {
		if event.Seq != int64(index+1) {
			t.Fatalf("sequence has a gap or repeat at position %d: %d", index, event.Seq)
		}
	}
}

func TestAnEventNeedsAKindAndAPayloadThatEncodes(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()
	if _, err := s.Append(ctx, "", nil); err == nil {
		t.Fatal("an event without a kind is meaningless to central")
	}
	if _, err := s.Append(ctx, "k", make(chan int)); err == nil {
		t.Fatal("an unencodable payload must be an error, not a stored empty event")
	}
	if pending, _ := s.Pending(ctx, 10); len(pending) != 0 {
		t.Fatalf("a failed append must leave nothing behind: %+v", pending)
	}
}

func TestADatabaseFromANewerAgentIsRefused(t *testing.T) {
	s, path := openTemp(t)
	if _, err := s.db.Exec(`UPDATE meta SET value = '999' WHERE key = 'schema_version'`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := Open(path); err == nil {
		t.Fatal("opening a database written by a newer agent could corrupt it")
	}
}

func TestEventsWithPrefixReturnsOnlyThatFamilyOldestFirstEvenWhenAcknowledged(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	for _, kind := range []string{"agent.checkin", "runs.run.started", "agent.checkin", "runs.step.done"} {
		if _, err := db.Append(ctx, kind, map[string]string{"k": kind}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Ack(ctx, 4); err != nil { // everything is sent; Pending would now be empty
		t.Fatal(err)
	}
	got, err := db.EventsWithPrefix(ctx, "runs.")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Kind != "runs.run.started" || got[1].Kind != "runs.step.done" || got[0].Seq >= got[1].Seq {
		t.Fatalf("got %+v", got)
	}
	if _, err := db.EventsWithPrefix(ctx, ""); err == nil {
		t.Fatal("an empty prefix would return everything and should be refused")
	}
}
