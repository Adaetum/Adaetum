package runs

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock makes waiting instant: Sleep moves time forward and returns. It also
// remembers every sleep, so tests can check backoff.
type fakeClock struct {
	mu    sync.Mutex
	now   time.Time
	slept []time.Duration
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.slept = append(c.slept, d)
	c.mu.Unlock()
	return nil
}

func (c *fakeClock) sleeps() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.slept...)
}

// world stands in for the real system the steps act on.
type world struct {
	mu      sync.Mutex
	built   map[string]bool
	applies map[string]int
	checks  map[string]int
	order   []string // the order steps were applied in
}

func newWorld() *world {
	return &world{built: map[string]bool{}, applies: map[string]int{}, checks: map[string]int{}}
}

func (w *world) step(name string, needs ...string) Step {
	return Step{
		Name:  name,
		Needs: needs,
		Check: func(context.Context, Env) (Status, error) {
			w.mu.Lock()
			defer w.mu.Unlock()
			w.checks[name]++
			if w.built[name] {
				return Satisfied, nil
			}
			return Unsatisfied, nil
		},
		Apply: func(context.Context, Env) error {
			w.mu.Lock()
			defer w.mu.Unlock()
			w.applies[name]++
			w.order = append(w.order, name)
			w.built[name] = true
			return nil
		},
	}
}

func (w *world) appliedCount(name string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.applies[name]
}

func testPolicy() Policy {
	return Policy{MaxAttempts: 5, RetryBase: 2 * time.Second, RetryMax: time.Minute, PollEvery: 5 * time.Second, StallAfter: time.Minute, Ceiling: time.Hour}
}

func newTestManager(t *testing.T, journal Journal, policy Policy) (*Manager, *fakeClock) {
	t.Helper()
	clock := &fakeClock{now: time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)}
	m, err := NewManager(Options{Journal: journal, Clock: clock, Policy: policy})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	return m, clock
}

func register(t *testing.T, m *Manager, name string, steps ...Step) {
	t.Helper()
	if err := m.Register(&Procedure{Name: name, Steps: steps}); err != nil {
		t.Fatal(err)
	}
}

func runToEnd(t *testing.T, m *Manager, req StartRequest) Snapshot {
	t.Helper()
	ctx := context.Background()
	id, err := m.Start(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Wait(ctx, id); err != nil {
		t.Fatal(err)
	}
	snap, ok := m.Get(id)
	if !ok {
		t.Fatalf("run %s is unknown", id)
	}
	return snap
}

func stepOf(t *testing.T, snap Snapshot, name string) StepStatus {
	t.Helper()
	for _, s := range snap.Steps {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("step %s not in run: %+v", name, snap.Steps)
	return StepStatus{}
}

func TestStepsRunInDependencyOrder(t *testing.T) {
	m, _ := newTestManager(t, &MemoryJournal{}, testPolicy())
	w := newWorld()
	// Declared out of order on purpose.
	register(t, m, "up", w.step("c", "b"), w.step("a"), w.step("b", "a"))
	snap := runToEnd(t, m, StartRequest{Procedure: "up"})
	if snap.State != StateSucceeded {
		t.Fatalf("state %s: %+v", snap.State, snap)
	}
	if got := strings.Join(w.order, ","); got != "a,b,c" {
		t.Fatalf("applied in order %s, want a,b,c", got)
	}
}

func TestRerunIsIdempotent(t *testing.T) {
	m, _ := newTestManager(t, &MemoryJournal{}, testPolicy())
	w := newWorld()
	register(t, m, "up", w.step("a"), w.step("b", "a"))
	runToEnd(t, m, StartRequest{Procedure: "up"})
	second := runToEnd(t, m, StartRequest{Procedure: "up"})
	if second.State != StateSucceeded {
		t.Fatalf("second run %s", second.State)
	}
	if w.appliedCount("a") != 1 || w.appliedCount("b") != 1 {
		t.Fatalf("a rerun repeated work: %v", w.applies)
	}
	if got := stepOf(t, second, "a"); got.State != StepDone || got.Code != CodeAlready {
		t.Fatalf("satisfied step should be done/already, got %+v", got)
	}
}

func TestResumeFinishesAnInterruptedRunWithoutRepeatingWork(t *testing.T) {
	journal := &MemoryJournal{}
	w := newWorld()
	w.built["a"] = true // the first attempt got this far before the agent stopped
	ctx := context.Background()
	for _, rec := range []Record{
		{RunID: "run_1", Procedure: "up", Kind: KindRunStarted, Inputs: map[string]string{"k": "v"}, StepNames: []string{"a", "b"}, At: time.Now()},
		{RunID: "run_1", Procedure: "up", Kind: KindStepDone, Step: "a", Code: CodeApplied, At: time.Now()},
	} {
		if err := journal.Append(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	m, _ := newTestManager(t, journal, testPolicy())
	register(t, m, "up", w.step("a"), w.step("b", "a"))
	if err := m.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Wait(ctx, "run_1"); err != nil {
		t.Fatal(err)
	}
	snap, _ := m.Get("run_1")
	if snap.State != StateSucceeded || snap.Attempt != 2 {
		t.Fatalf("want succeeded on the second attempt, got %s attempt %d", snap.State, snap.Attempt)
	}
	if w.appliedCount("a") != 0 || w.appliedCount("b") != 1 {
		t.Fatalf("resume repeated or skipped work: %v", w.applies)
	}
	if snap.Inputs["k"] != "v" {
		t.Fatalf("inputs were lost: %v", snap.Inputs)
	}
}

func TestResumeOfAnUnknownProcedureIsRecordedNotLeftRunning(t *testing.T) {
	journal := &MemoryJournal{}
	ctx := context.Background()
	_ = journal.Append(ctx, Record{RunID: "run_x", Procedure: "gone", Kind: KindRunStarted, At: time.Now()})
	m, _ := newTestManager(t, journal, testPolicy())
	if err := m.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	snap, _ := m.Get("run_x")
	if snap.State != StateFailed || snap.Code != CodeConfig {
		t.Fatalf("want failed/config, got %s/%s", snap.State, snap.Code)
	}
}

func TestTransientFailureIsRetriedWithBackoffFromCheck(t *testing.T) {
	m, clock := newTestManager(t, &MemoryJournal{}, testPolicy())
	w := newWorld()
	step := w.step("a")
	var calls atomic.Int32
	inner := step.Apply
	step.Apply = func(ctx context.Context, env Env) error {
		if calls.Add(1) <= 2 {
			return Transient(CodeNetwork, errors.New("connection refused"))
		}
		return inner(ctx, env)
	}
	register(t, m, "up", step)
	snap := runToEnd(t, m, StartRequest{Procedure: "up"})
	if snap.State != StateSucceeded {
		t.Fatalf("state %s: %s", snap.State, snap.Detail)
	}
	if got := clock.sleeps(); len(got) != 2 || got[0] != 2*time.Second || got[1] != 4*time.Second {
		t.Fatalf("backoff %v, want 2s then 4s", got)
	}
	// Three attempts each start from Check, and the last one's default Verify checks
	// once more after the apply: four in all.
	if w.checks["a"] != 4 {
		t.Fatalf("every attempt should start from Check, got %d checks", w.checks["a"])
	}
}

func TestExhaustedRetriesFailAndStopTheRun(t *testing.T) {
	policy := testPolicy()
	policy.MaxAttempts = 3
	m, _ := newTestManager(t, &MemoryJournal{}, policy)
	w := newWorld()
	flaky := w.step("a")
	flaky.Apply = func(context.Context, Env) error { return Transient(CodeNetwork, errors.New("still down")) }
	register(t, m, "up", flaky, w.step("b", "a"))
	snap := runToEnd(t, m, StartRequest{Procedure: "up"})
	if snap.State != StateFailed || snap.Failure != FailBroken || snap.Code != CodeRetries || snap.FailedStep != "a" {
		t.Fatalf("got %s/%s/%s at %s", snap.State, snap.Failure, snap.Code, snap.FailedStep)
	}
	if w.appliedCount("b") != 0 {
		t.Fatal("a step after a failed one must not run")
	}
}

func TestBrokenFailsAtOnceWithItsReason(t *testing.T) {
	m, clock := newTestManager(t, &MemoryJournal{}, testPolicy())
	w := newWorld()
	step := w.step("gitea")
	step.Verify = func(context.Context, Env) (Progress, error) {
		return Progress{State: Broken, Code: CodeImagePull, Detail: "gitea cannot pull its image"}, nil
	}
	register(t, m, "up", step, w.step("after", "gitea"))
	snap := runToEnd(t, m, StartRequest{Procedure: "up"})
	if snap.State != StateFailed || snap.Failure != FailBroken || snap.Code != CodeImagePull {
		t.Fatalf("got %s/%s/%s", snap.State, snap.Failure, snap.Code)
	}
	if len(clock.sleeps()) != 0 {
		t.Fatalf("a broken step must not wait, slept %v", clock.sleeps())
	}
	if w.appliedCount("after") != 0 {
		t.Fatal("dependents of a broken step must not run")
	}
}

func TestSlowButMovingWaitOutlastsTheStallWindow(t *testing.T) {
	policy := testPolicy() // stall window is one minute, poll every 5 seconds
	m, clock := newTestManager(t, &MemoryJournal{}, policy)
	w := newWorld()
	step := w.step("pull")
	var polls atomic.Int32
	step.Verify = func(context.Context, Env) (Progress, error) {
		n := polls.Add(1)
		if n > 60 { // 60 polls is five minutes, far past the stall window
			return Progress{State: Ready}, nil
		}
		return Progress{State: Slow, Code: CodeImagePulling, Detail: "pulling", Marker: string(rune('a'+n%26)) + string(rune('A'+n/26))}, nil
	}
	register(t, m, "up", step)
	snap := runToEnd(t, m, StartRequest{Procedure: "up"})
	if snap.State != StateSucceeded {
		t.Fatalf("a wait that keeps moving must succeed, got %s: %s", snap.State, snap.Detail)
	}
	if clock.Now().Sub(time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)) < 4*time.Minute {
		t.Fatal("test did not actually wait past the stall window")
	}
}

func TestStuckWaitFailsAsStalledAndKeepsItsReason(t *testing.T) {
	m, _ := newTestManager(t, &MemoryJournal{}, testPolicy())
	w := newWorld()
	step := w.step("pull")
	step.Verify = func(context.Context, Env) (Progress, error) {
		return Progress{State: Slow, Code: CodeImagePulling, Detail: "pulling gitea", Marker: "same"}, nil
	}
	register(t, m, "up", step)
	snap := runToEnd(t, m, StartRequest{Procedure: "up"})
	if snap.State != StateFailed || snap.Failure != FailStalled || snap.Code != CodeImagePulling {
		t.Fatalf("got %s/%s/%s", snap.State, snap.Failure, snap.Code)
	}
	// One waiting record for the whole stuck wait, not one per poll.
	waiting := 0
	for _, rec := range m.Events(snap.RunID) {
		if rec.Kind == KindStepWaiting {
			waiting++
		}
	}
	if waiting != 1 {
		t.Fatalf("a constant wait should record once, recorded %d", waiting)
	}
}

func TestEndlessProgressStillHitsTheCeiling(t *testing.T) {
	policy := testPolicy()
	policy.Ceiling = 2 * time.Minute
	m, _ := newTestManager(t, &MemoryJournal{}, policy)
	w := newWorld()
	step := w.step("churn")
	var n atomic.Int32
	step.Verify = func(context.Context, Env) (Progress, error) {
		return Progress{State: Slow, Code: CodeStarting, Marker: string(rune(n.Add(1)))}, nil
	}
	register(t, m, "up", step)
	snap := runToEnd(t, m, StartRequest{Procedure: "up"})
	if snap.Failure != FailTimeout {
		t.Fatalf("want a timeout, got %s", snap.Failure)
	}
}

func TestOnceOnlyStepNeverGuesses(t *testing.T) {
	m, _ := newTestManager(t, &MemoryJournal{}, testPolicy())
	w := newWorld()
	step := w.step("init-openbao")
	step.Kind = OnceOnly
	step.Check = func(context.Context, Env) (Status, error) { return Unknown, nil }
	register(t, m, "up", step)
	snap := runToEnd(t, m, StartRequest{Procedure: "up"})
	if snap.State != StateNeedsHuman || snap.Code != CodeUnknown {
		t.Fatalf("got %s/%s", snap.State, snap.Code)
	}
	if w.appliedCount("init-openbao") != 0 {
		t.Fatal("a once-only step was applied although the check could not tell")
	}
}

func TestConvergentStepAppliesWhenCheckCannotTell(t *testing.T) {
	m, _ := newTestManager(t, &MemoryJournal{}, testPolicy())
	w := newWorld()
	step := w.step("manifests")
	check := step.Check
	var asked atomic.Int32
	step.Check = func(ctx context.Context, env Env) (Status, error) {
		if asked.Add(1) == 1 {
			return Unknown, nil
		}
		return check(ctx, env)
	}
	register(t, m, "up", step)
	if snap := runToEnd(t, m, StartRequest{Procedure: "up"}); snap.State != StateSucceeded {
		t.Fatalf("got %s: %s", snap.State, snap.Detail)
	}
	if w.appliedCount("manifests") != 1 {
		t.Fatal("a convergent step should apply when it cannot tell")
	}
}

func TestDestructiveStepNeedsApproval(t *testing.T) {
	m, _ := newTestManager(t, &MemoryJournal{}, testPolicy())
	w := newWorld()
	burn := w.step("burn")
	burn.Kind = Destructive
	register(t, m, "seal", burn)

	snap := runToEnd(t, m, StartRequest{Procedure: "seal"})
	if snap.State != StateNeedsHuman || snap.Code != CodeApproval {
		t.Fatalf("got %s/%s", snap.State, snap.Code)
	}
	if w.appliedCount("burn") != 0 {
		t.Fatal("a destructive step ran without approval")
	}

	if err := m.Retry(context.Background(), snap.RunID, "ops@example.com", "burn"); err != nil {
		t.Fatal(err)
	}
	if err := m.Wait(context.Background(), snap.RunID); err != nil {
		t.Fatal(err)
	}
	after, _ := m.Get(snap.RunID)
	if after.State != StateSucceeded || after.Attempt != 2 || w.appliedCount("burn") != 1 {
		t.Fatalf("approved retry: %s attempt %d applies %d", after.State, after.Attempt, w.appliedCount("burn"))
	}
}

func TestApprovalsAreValidated(t *testing.T) {
	m, _ := newTestManager(t, &MemoryJournal{}, testPolicy())
	w := newWorld()
	register(t, m, "up", w.step("plain"))
	if _, err := m.Start(context.Background(), StartRequest{Procedure: "up", Approved: []string{"plain"}}); err == nil {
		t.Fatal("approving a non-destructive step should be refused")
	}
	if _, err := m.Start(context.Background(), StartRequest{Procedure: "up", Approved: []string{"nope"}}); err == nil {
		t.Fatal("approving a step that does not exist should be refused")
	}
	if _, err := m.Start(context.Background(), StartRequest{Procedure: "missing"}); err == nil {
		t.Fatal("starting an unknown procedure should be refused")
	}
}

func TestRetryOnlyForRunsThatStopped(t *testing.T) {
	m, _ := newTestManager(t, &MemoryJournal{}, testPolicy())
	w := newWorld()
	register(t, m, "up", w.step("a"))
	snap := runToEnd(t, m, StartRequest{Procedure: "up"})
	if err := m.Retry(context.Background(), snap.RunID, "ops"); err == nil {
		t.Fatal("a succeeded run must not be retried")
	}
	if err := m.Retry(context.Background(), "run_nope", "ops"); err == nil {
		t.Fatal("an unknown run must not be retried")
	}
}

func TestStepsSharingALockNeverOverlapAcrossRuns(t *testing.T) {
	m, _ := newTestManager(t, &MemoryJournal{}, testPolicy())
	var inside, worst atomic.Int32
	step := Step{
		Name:  "touch-openbao",
		Locks: []string{"openbao"},
		Check: func(context.Context, Env) (Status, error) { return Unsatisfied, nil },
		Apply: func(context.Context, Env) error {
			now := inside.Add(1)
			for {
				seen := worst.Load()
				if now <= seen || worst.CompareAndSwap(seen, now) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			inside.Add(-1)
			return nil
		},
		Verify: func(context.Context, Env) (Progress, error) { return Progress{State: Ready}, nil },
	}
	register(t, m, "touch", step)
	ctx := context.Background()
	var ids []string
	for i := 0; i < 6; i++ {
		id, err := m.Start(ctx, StartRequest{Procedure: "touch"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	for _, id := range ids {
		if err := m.Wait(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if worst.Load() != 1 {
		t.Fatalf("%d steps held the same lock at once", worst.Load())
	}
}

func TestStepWithoutARecordIsNotRun(t *testing.T) {
	journal := &MemoryJournal{FailAppend: func(rec Record) error {
		if rec.Kind == KindStepApply {
			return errors.New("disk full")
		}
		return nil
	}}
	m, _ := newTestManager(t, journal, testPolicy())
	w := newWorld()
	register(t, m, "up", w.step("a"))
	snap := runToEnd(t, m, StartRequest{Procedure: "up"})
	if snap.State != StateFailed || snap.Failure != FailJournal {
		t.Fatalf("got %s/%s", snap.State, snap.Failure)
	}
	if w.appliedCount("a") != 0 {
		t.Fatal("a step ran although it could not be recorded")
	}
}

func TestRunThatCannotBeRecordedDoesNotStart(t *testing.T) {
	journal := &MemoryJournal{FailAppend: func(Record) error { return errors.New("read-only") }}
	m, _ := newTestManager(t, journal, testPolicy())
	w := newWorld()
	register(t, m, "up", w.step("a"))
	if _, err := m.Start(context.Background(), StartRequest{Procedure: "up"}); err == nil {
		t.Fatal("a run that cannot be recorded must not start")
	}
	if w.appliedCount("a") != 0 {
		t.Fatal("work ran for a run that was never recorded")
	}
}

func blockingStep(name string, started chan<- struct{}) Step {
	return Step{
		Name:  name,
		Check: func(context.Context, Env) (Status, error) { return Unsatisfied, nil },
		Apply: func(ctx context.Context, _ Env) error {
			started <- struct{}{}
			<-ctx.Done()
			return ctx.Err()
		},
	}
}

func TestOperatorCancelIsRecorded(t *testing.T) {
	m, _ := newTestManager(t, &MemoryJournal{}, testPolicy())
	started := make(chan struct{}, 1)
	register(t, m, "hang", blockingStep("a", started))
	ctx := context.Background()
	id, err := m.Start(ctx, StartRequest{Procedure: "hang"})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if !m.Cancel(id) {
		t.Fatal("cancel did not find the run")
	}
	if err := m.Wait(ctx, id); err != nil {
		t.Fatal(err)
	}
	if snap, _ := m.Get(id); snap.State != StateCancelled {
		t.Fatalf("got %s", snap.State)
	}
}

func TestShutdownLeavesTheRunToBeResumed(t *testing.T) {
	journal := &MemoryJournal{}
	first, _ := newTestManager(t, journal, testPolicy())
	started := make(chan struct{}, 1)
	register(t, first, "up", blockingStep("a", started))
	id, err := first.Start(context.Background(), StartRequest{Procedure: "up"})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	first.Close() // the agent stops mid-step

	records, _ := journal.Load(context.Background())
	if snap := Fold(records)[0]; snap.State != StateRunning {
		t.Fatalf("a shutdown must not end the run, got %s", snap.State)
	}

	// The next start picks it up and, this time, the step can finish.
	second, _ := newTestManager(t, journal, testPolicy())
	w := newWorld()
	register(t, second, "up", w.step("a"))
	if err := second.Resume(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := second.Wait(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if snap, _ := second.Get(id); snap.State != StateSucceeded || snap.Attempt != 2 {
		t.Fatalf("got %s attempt %d", snap.State, snap.Attempt)
	}
}

func TestProcedureValidation(t *testing.T) {
	ok := func(name string, needs ...string) Step {
		return Step{Name: name, Needs: needs,
			Check: func(context.Context, Env) (Status, error) { return Satisfied, nil },
			Apply: func(context.Context, Env) error { return nil }}
	}
	cases := map[string]*Procedure{
		"no name":                   {Steps: []Step{ok("a")}},
		"no steps":                  {Name: "p"},
		"duplicate":                 {Name: "p", Steps: []Step{ok("a"), ok("a")}},
		"missing need":              {Name: "p", Steps: []Step{ok("a", "ghost")}},
		"cycle":                     {Name: "p", Steps: []Step{ok("a", "b"), ok("b", "a")}},
		"no check":                  {Name: "p", Steps: []Step{{Name: "a", Apply: func(context.Context, Env) error { return nil }}}},
		"once-only without apply":   {Name: "p", Steps: []Step{{Name: "a", Kind: OnceOnly, Check: func(context.Context, Env) (Status, error) { return Satisfied, nil }}}},
		"destructive without apply": {Name: "p", Steps: []Step{{Name: "a", Kind: Destructive, Check: func(context.Context, Env) (Status, error) { return Satisfied, nil }}}},
	}
	m, _ := newTestManager(t, &MemoryJournal{}, testPolicy())
	for name, proc := range cases {
		if err := m.Register(proc); err == nil {
			t.Errorf("%s: a malformed procedure was accepted", name)
		}
	}
}

func TestRecordsStayInsideTheClosedVocabularyAndHideSecrets(t *testing.T) {
	m, _ := newTestManager(t, &MemoryJournal{}, testPolicy())
	w := newWorld()
	step := w.step("login")
	step.Apply = func(context.Context, Env) error {
		return BrokenBecause("made-up-code", "login failed: token=abc123 was refused, password: hunter2")
	}
	register(t, m, "up", step)
	snap := runToEnd(t, m, StartRequest{Procedure: "up"})
	if snap.Code != CodeOther {
		t.Fatalf("an unlisted code should be recorded as other, got %q", snap.Code)
	}
	for _, rec := range m.Events(snap.RunID) {
		if strings.Contains(rec.Detail, "abc123") || strings.Contains(rec.Detail, "hunter2") {
			t.Fatalf("a secret reached the journal: %q", rec.Detail)
		}
	}
	if !strings.Contains(snap.Detail, "token=REDACTED") {
		t.Fatalf("expected a redacted detail, got %q", snap.Detail)
	}
}

func TestSnapshotListsEveryStepFromTheStart(t *testing.T) {
	m, _ := newTestManager(t, &MemoryJournal{}, testPolicy())
	w := newWorld()
	bad := w.step("b", "a")
	bad.Apply = func(context.Context, Env) error { return BrokenBecause(CodeConfig, "bad config") }
	register(t, m, "up", w.step("a"), bad, w.step("c", "b"))
	snap := runToEnd(t, m, StartRequest{Procedure: "up"})
	if len(snap.Steps) != 3 {
		t.Fatalf("the run should show all 3 steps, got %+v", snap.Steps)
	}
	if got := stepOf(t, snap, "c"); got.State != StepPending {
		t.Fatalf("a step that never ran should be pending, got %s", got.State)
	}
	if got := stepOf(t, snap, "b"); got.State != StepFailed || got.Failure != FailBroken {
		t.Fatalf("got %+v", got)
	}
}

func TestInputsThatLookLikeSecretsOrAreTooBigAreRefused(t *testing.T) {
	m, _ := newTestManager(t, &MemoryJournal{}, testPolicy())
	w := newWorld()
	register(t, m, "up", w.step("a"))
	ctx := context.Background()
	bad := map[string]map[string]string{
		"secret name": {"openbao_token": "x"},
		"password":    {"admin_password": "x"},
		"long value":  {"note": strings.Repeat("x", maxInputValue+1)},
		"long name":   {strings.Repeat("n", maxInputKey+1): "x"},
		"empty name":  {"": "x"},
	}
	for name, inputs := range bad {
		if _, err := m.Start(ctx, StartRequest{Procedure: "up", Inputs: inputs}); err == nil {
			t.Errorf("%s: inputs were accepted", name)
		}
	}
	many := map[string]string{}
	for i := 0; i <= maxInputs; i++ {
		many[strings.Repeat("k", 1)+string(rune('a'+i%26))+string(rune('a'+i/26))] = "v"
	}
	if _, err := m.Start(ctx, StartRequest{Procedure: "up", Inputs: many}); err == nil {
		t.Error("too many inputs were accepted")
	}
	if _, err := m.Start(ctx, StartRequest{Procedure: "up", Inputs: map[string]string{"node": "node-1"}}); err != nil {
		t.Errorf("ordinary inputs were refused: %v", err)
	}
}

func TestProceduresAreDescribedInRunOrder(t *testing.T) {
	m, _ := newTestManager(t, &MemoryJournal{}, testPolicy())
	w := newWorld()
	burn := w.step("burn", "b")
	burn.Kind = Destructive
	register(t, m, "seal", burn, w.step("b"))
	register(t, m, "alpha", w.step("only"))
	got := m.Procedures()
	if len(got) != 2 || got[0].Name != "alpha" || got[1].Name != "seal" {
		t.Fatalf("procedures should be listed by name: %+v", got)
	}
	steps := got[1].Steps
	if len(steps) != 2 || steps[0].Name != "b" || steps[1].Name != "burn" || steps[1].Kind != "destructive" {
		t.Fatalf("steps should be listed in run order with their kind: %+v", steps)
	}
}

func TestAWaitOnlyStepHasNoApplyAndJustWaits(t *testing.T) {
	m, clock := newTestManager(t, &MemoryJournal{}, testPolicy())
	var polls atomic.Int32
	step := Step{
		Name:  "clock",
		Check: func(context.Context, Env) (Status, error) { return Unsatisfied, nil },
		// No Apply: nothing to do but wait for the system to get there.
		Verify: func(context.Context, Env) (Progress, error) {
			if polls.Add(1) > 3 {
				return Progress{State: Ready}, nil
			}
			return Progress{State: Slow, Code: CodeStarting, Detail: "waiting for the clock", Marker: string(rune('a' + polls.Load()))}, nil
		},
	}
	register(t, m, "wait", step)
	snap := runToEnd(t, m, StartRequest{Procedure: "wait"})
	if snap.State != StateSucceeded {
		t.Fatalf("got %s: %s", snap.State, snap.Detail)
	}
	for _, rec := range m.Events(snap.RunID) {
		if rec.Kind == KindStepApply || rec.Kind == KindStepApplied {
			t.Fatalf("a wait-only step must not record an apply: %+v", rec)
		}
	}
	if len(clock.sleeps()) != 3 {
		t.Fatalf("expected three polls before ready, slept %v", clock.sleeps())
	}
}

func TestEveryRecordIsLoggedAsOneLineAfterItIsJournaled(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	journal := &MemoryJournal{}
	clock := &fakeClock{now: time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)}
	m, err := NewManager(Options{Journal: journal, Clock: clock, Policy: testPolicy(), OnRecord: func(rec Record) {
		// By the time it is logged it must already be in the journal.
		stored, _ := journal.Load(context.Background())
		found := false
		for _, s := range stored {
			if s.Kind == rec.Kind && s.Step == rec.Step && s.At.Equal(rec.At) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s was logged before it was journaled", rec.Kind)
		}
		mu.Lock()
		lines = append(lines, rec.Line())
		mu.Unlock()
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	w := newWorld()
	bad := w.step("images")
	bad.Apply = func(context.Context, Env) error {
		return BrokenBecause(CodeImagePull, "gitea cannot pull its image, token=abc123")
	}
	register(t, m, "up", w.step("clock"), bad)
	snap := runToEnd(t, m, StartRequest{Procedure: "up", Inputs: map[string]string{"node": "node-1"}, StartedBy: "ops@example.com"})
	if snap.State != StateFailed {
		t.Fatalf("got %s", snap.State)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(lines) != len(m.Events(snap.RunID)) {
		t.Fatalf("%d lines for %d records", len(lines), len(m.Events(snap.RunID)))
	}
	all := strings.Join(lines, "\n")
	for _, want := range []string{"[RUN] 2026-11-12T09:00:00Z", "run=" + snap.RunID, "procedure=up", "step=clock", "event=step.done", "code=applied", "event=run.finished", "state=failed", "failure=broken", "code=image-pull", "by=ops@example.com", "inputs=1"} {
		if !strings.Contains(all, want) {
			t.Errorf("the log is missing %q:\n%s", want, all)
		}
	}
	for _, leak := range []string{"abc123", "node-1"} {
		if strings.Contains(all, leak) {
			t.Errorf("%q must not reach the log:\n%s", leak, all)
		}
	}
	for _, line := range lines {
		if strings.Contains(line, "\n") {
			t.Errorf("a record must be exactly one line: %q", line)
		}
	}
}

func TestALogLineQuotesValuesSoItStaysParseable(t *testing.T) {
	line := Record{At: time.Unix(0, 0), RunID: "r", Kind: KindStepWaiting, Step: "a b", Detail: `say "hi" x=y`}.Line()
	for _, want := range []string{`step="a b"`, `detail="say \"hi\" x=y"`} {
		if !strings.Contains(line, want) {
			t.Errorf("want %s in %s", want, line)
		}
	}
}

func TestAnErrorAStepReturnsBecauseTheRunWasCancelledIsACancellationNotAFailure(t *testing.T) {
	m, _ := newTestManager(t, &MemoryJournal{}, testPolicy())
	started := make(chan struct{}, 1)
	register(t, m, "hang", Step{
		Name:  "a",
		Check: func(context.Context, Env) (Status, error) { return Unsatisfied, nil },
		Apply: func(ctx context.Context, _ Env) error {
			started <- struct{}{}
			<-ctx.Done()
			return errors.New("signal: killed") // not ctx.Err(): what a killed program reports
		},
	})
	id, _ := m.Start(context.Background(), StartRequest{Procedure: "hang"})
	<-started
	m.Cancel(id)
	_ = m.Wait(context.Background(), id)
	snap, _ := m.Get(id)
	if snap.State != StateCancelled {
		t.Fatalf("got %s (%s): a step's error after cancellation must not read as a failure", snap.State, snap.Failure)
	}
	for _, rec := range m.Events(id) {
		if rec.Kind == KindStepFailed {
			t.Fatalf("no step.failed should be recorded for a cancellation: %+v", rec)
		}
	}
}
