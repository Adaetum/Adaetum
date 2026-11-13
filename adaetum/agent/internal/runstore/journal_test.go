package runstore

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/runs"
	"github.com/Adaetum/Adaetum/agent/internal/store"
)

func openAt(t *testing.T, path string) *store.Store {
	t.Helper()
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestRecordsRoundTripInOrderAndOtherEventsAreIgnored(t *testing.T) {
	db := openAt(t, filepath.Join(t.TempDir(), "agent.db"))
	defer db.Close()
	ctx := context.Background()
	j := Journal{Store: db}

	if _, err := db.Append(ctx, "agent.checkin", map[string]string{"hostname": "node-1"}); err != nil {
		t.Fatal(err)
	}
	want := []runs.Record{
		{RunID: "run_1", Procedure: "up", Kind: runs.KindRunStarted, At: time.Unix(1, 0).UTC(), Inputs: map[string]string{"a": "b"}},
		{RunID: "run_1", Procedure: "up", Kind: runs.KindStepDone, Step: "x", Code: runs.CodeApplied, At: time.Unix(2, 0).UTC()},
		{RunID: "run_1", Procedure: "up", Kind: runs.KindRunFinished, State: runs.StateSucceeded, At: time.Unix(3, 0).UTC()},
	}
	for _, rec := range want {
		if err := j.Append(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	got, err := j.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("loaded %d records, want %d (the check-in must not be among them)", len(got), len(want))
	}
	for i := range want {
		if got[i].Kind != want[i].Kind || got[i].Step != want[i].Step || got[i].State != want[i].State || !got[i].At.Equal(want[i].At) {
			t.Fatalf("record %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestRecordsShareTheOutboxWithOtherEvents(t *testing.T) {
	db := openAt(t, filepath.Join(t.TempDir(), "agent.db"))
	defer db.Close()
	ctx := context.Background()
	if err := (Journal{Store: db}).Append(ctx, runs.Record{RunID: "run_1", Procedure: "up", Kind: runs.KindRunStarted}); err != nil {
		t.Fatal(err)
	}
	pending, err := db.Pending(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Kind != "runs.run.started" {
		t.Fatalf("a run record should be an unsent host event, got %+v", pending)
	}
}

func TestAnOversizedRecordIsRefusedSoItCannotJamTheOutbox(t *testing.T) {
	db := openAt(t, filepath.Join(t.TempDir(), "agent.db"))
	defer db.Close()
	big := runs.Record{RunID: "run_1", Procedure: "up", Kind: runs.KindRunStarted, Inputs: map[string]string{"k": strings.Repeat("x", maxRecordBytes)}}
	if err := (Journal{Store: db}).Append(context.Background(), big); err == nil {
		t.Fatal("a record over the limit was written")
	}
	if pending, _ := db.Pending(context.Background(), 10); len(pending) != 0 {
		t.Fatal("a refused record left something in the outbox")
	}
}

func TestAnUnreadableRecordIsSkippedNotFatal(t *testing.T) {
	db := openAt(t, filepath.Join(t.TempDir(), "agent.db"))
	defer db.Close()
	ctx := context.Background()
	if _, err := db.Append(ctx, KindPrefix+"future.thing", []int{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if err := (Journal{Store: db}).Append(ctx, runs.Record{RunID: "run_1", Procedure: "up", Kind: runs.KindRunStarted}); err != nil {
		t.Fatal(err)
	}
	got, err := (Journal{Store: db}).Load(ctx)
	if err != nil || len(got) != 1 {
		t.Fatalf("got %d records, err %v; the readable record should still load", len(got), err)
	}
}

// proc builds a one-step procedure whose step is applied once and then stays true.
func proc(built *bool, block chan struct{}) *runs.Procedure {
	return &runs.Procedure{Name: "up", Steps: []runs.Step{{
		Name: "thing",
		Check: func(context.Context, runs.Env) (runs.Status, error) {
			if *built {
				return runs.Satisfied, nil
			}
			return runs.Unsatisfied, nil
		},
		Apply: func(ctx context.Context, _ runs.Env) error {
			if block != nil {
				block <- struct{}{}
				<-ctx.Done()
				return ctx.Err()
			}
			*built = true
			return nil
		},
	}}}
}

func TestAFinishedRunIsStillThereAfterARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.db")
	ctx := context.Background()
	built := false

	db := openAt(t, path)
	first, err := runs.NewManager(runs.Options{Journal: Journal{Store: db}})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Register(proc(&built, nil)); err != nil {
		t.Fatal(err)
	}
	id, err := first.Start(ctx, runs.StartRequest{Procedure: "up", Inputs: map[string]string{"node": "node-1"}, StartedBy: "ops@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Wait(ctx, id); err != nil {
		t.Fatal(err)
	}
	first.Close()
	db.Close()

	db = openAt(t, path)
	defer db.Close()
	second, err := runs.NewManager(runs.Options{Journal: Journal{Store: db}})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := second.Register(proc(&built, nil)); err != nil {
		t.Fatal(err)
	}
	if err := second.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	snap, ok := second.Get(id)
	if !ok || snap.State != runs.StateSucceeded || snap.Inputs["node"] != "node-1" || snap.StartedBy != "ops@example.com" {
		t.Fatalf("the finished run did not survive a restart intact: %+v (found %v)", snap, ok)
	}
	if snap.Attempt != 1 {
		t.Fatalf("a finished run must not be restarted, attempt %d", snap.Attempt)
	}
}

func TestAnInterruptedRunResumesAfterARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.db")
	ctx := context.Background()
	built := false

	db := openAt(t, path)
	first, err := runs.NewManager(runs.Options{Journal: Journal{Store: db}})
	if err != nil {
		t.Fatal(err)
	}
	block := make(chan struct{}, 1)
	if err := first.Register(proc(&built, block)); err != nil {
		t.Fatal(err)
	}
	id, err := first.Start(ctx, runs.StartRequest{Procedure: "up"})
	if err != nil {
		t.Fatal(err)
	}
	<-block       // the step is in flight
	first.Close() // the agent stops (a reboot, an upgrade)
	db.Close()

	db = openAt(t, path)
	defer db.Close()
	second, err := runs.NewManager(runs.Options{Journal: Journal{Store: db}})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if err := second.Register(proc(&built, nil)); err != nil {
		t.Fatal(err)
	}
	if err := second.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	if err := second.Wait(ctx, id); err != nil {
		t.Fatal(err)
	}
	snap, _ := second.Get(id)
	if snap.State != runs.StateSucceeded || snap.Attempt != 2 || !built {
		t.Fatalf("got %s on attempt %d (built %v)", snap.State, snap.Attempt, built)
	}
}
