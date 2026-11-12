package runs

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestComposedChunksRunInOrderAndKeepTheirOwnDependencies(t *testing.T) {
	m, _ := newTestManager(t, &MemoryJournal{}, testPolicy())
	w := newWorld()
	// Two chunks that both have a step called "a", declared out of order within a chunk.
	first := &Procedure{Name: "one", Steps: []Step{w.step("b", "a"), w.step("a")}}
	second := &Procedure{Name: "two", Steps: []Step{w.step("a"), w.step("b", "a")}}
	both, err := Compose("both", "two chunks", first, second)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Register(both); err != nil {
		t.Fatal(err)
	}
	// The same step names exist in both chunks, so the world must tell them apart: wrap.
	snap := runToEnd(t, m, StartRequest{Procedure: "both"})
	if snap.State != StateSucceeded {
		t.Fatalf("got %s: %s", snap.State, snap.Detail)
	}
	var names []string
	for _, step := range snap.Steps {
		names = append(names, step.Name)
	}
	if got := strings.Join(names, ","); got != "one/a,one/b,two/a,two/b" {
		t.Fatalf("composite steps %s", got)
	}
}

func TestALaterChunkNeverStartsBeforeTheEarlierOneIsSatisfied(t *testing.T) {
	m, _ := newTestManager(t, &MemoryJournal{}, testPolicy())
	var order []string
	record := func(name string) Step {
		return Step{
			Name:  name,
			Check: func(context.Context, Env) (Status, error) { return Unsatisfied, nil },
			Apply: func(context.Context, Env) error { order = append(order, name); return nil },
			Verify: func(context.Context, Env) (Progress, error) {
				return Progress{State: Ready}, nil
			},
		}
	}
	// Chunk one is a diamond: two independent steps, then a join. Chunk two starts only
	// after the join.
	one := &Procedure{Name: "one", Steps: []Step{record("x"), record("y"), func() Step { s := record("join"); s.Needs = []string{"x", "y"}; return s }()}}
	two := &Procedure{Name: "two", Steps: []Step{record("z")}}
	both, err := Compose("both", "", one, two)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Register(both); err != nil {
		t.Fatal(err)
	}
	runToEnd(t, m, StartRequest{Procedure: "both"})
	if got := strings.Join(order, ","); got != "x,y,join,z" {
		t.Fatalf("ran %s, want the earlier chunk fully before the later one", got)
	}
}

func TestAFailureInAnEarlierChunkStopsTheLaterOnes(t *testing.T) {
	m, _ := newTestManager(t, &MemoryJournal{}, testPolicy())
	w := newWorld()
	bad := w.step("check")
	bad.Apply = func(context.Context, Env) error { return BrokenBecause(CodeConfig, "bad input") }
	both, _ := Compose("both", "", &Procedure{Name: "one", Steps: []Step{bad}}, &Procedure{Name: "two", Steps: []Step{w.step("later")}})
	if err := m.Register(both); err != nil {
		t.Fatal(err)
	}
	snap := runToEnd(t, m, StartRequest{Procedure: "both"})
	if snap.State != StateFailed || snap.FailedStep != "one/check" {
		t.Fatalf("got %s at %s", snap.State, snap.FailedStep)
	}
	if w.appliedCount("later") != 0 {
		t.Fatal("a later chunk ran after an earlier one failed")
	}
}

func TestComposingRejectsMalformedPartsAndEmptyLists(t *testing.T) {
	if _, err := Compose("x", ""); err == nil {
		t.Error("composing nothing should fail")
	}
	if _, err := Compose("x", "", &Procedure{Name: "bad"}); err == nil {
		t.Error("a malformed part should fail when composed, not when run")
	}
}

func TestAChunkStillRunsAloneAfterBeingComposed(t *testing.T) {
	m, _ := newTestManager(t, &MemoryJournal{}, testPolicy())
	w := newWorld()
	chunk := &Procedure{Name: "chunk", Steps: []Step{w.step("a"), w.step("b", "a")}}
	both, _ := Compose("both", "", chunk)
	_ = both
	if err := m.Register(chunk); err != nil {
		t.Fatal(err)
	}
	snap := runToEnd(t, m, StartRequest{Procedure: "chunk"})
	if snap.State != StateSucceeded || snap.Steps[0].Name != "a" {
		t.Fatalf("composing must not change the chunk itself: %+v", snap.Steps)
	}
}

func TestValidationStepsReportWhatIsWrongAndChangeNothing(t *testing.T) {
	m, _ := newTestManager(t, &MemoryJournal{}, testPolicy())
	problem := "BOOTSTRAP_BACKUP_URL is not an https /backup address"
	step := Validation("payload", nil, CodeConfig, func(context.Context, Env) (string, error) { return problem, nil })
	register(t, m, "intake", step)
	snap := runToEnd(t, m, StartRequest{Procedure: "intake"})
	if snap.State != StateFailed || snap.Failure != FailBroken || snap.Code != CodeConfig || snap.Detail != problem {
		t.Fatalf("got %s/%s/%s %q", snap.State, snap.Failure, snap.Code, snap.Detail)
	}
	for _, rec := range m.Events(snap.RunID) {
		if rec.Kind == KindStepApply {
			t.Fatal("a validation step must not apply anything")
		}
	}

	ok := Validation("payload", nil, CodeConfig, func(context.Context, Env) (string, error) { return "", nil })
	m2, _ := newTestManager(t, &MemoryJournal{}, testPolicy())
	register(t, m2, "intake", ok)
	if snap := runToEnd(t, m2, StartRequest{Procedure: "intake"}); snap.State != StateSucceeded {
		t.Fatalf("a clean validation should pass: %s", snap.State)
	}

	broken := Validation("payload", nil, CodeConfig, func(context.Context, Env) (string, error) { return "", errors.New("cannot read the file") })
	m3, _ := newTestManager(t, &MemoryJournal{}, testPolicy())
	register(t, m3, "intake", broken)
	if snap := runToEnd(t, m3, StartRequest{Procedure: "intake"}); snap.State != StateFailed {
		t.Fatalf("a check that cannot be made must fail, not pass: %s", snap.State)
	}
}
