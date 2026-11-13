package runs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Errors the caller can act on. The API maps them to 400, 404 and 409; anything
// else is an internal failure.
var (
	ErrInvalid  = errors.New("invalid request")
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
)

// requestError carries a message for the person and the class it belongs to.
type requestError struct {
	class error
	msg   string
}

func (e *requestError) Error() string        { return e.msg }
func (e *requestError) Is(target error) bool { return target == e.class }

func requestErr(class error, format string, args ...any) error {
	return &requestError{class: class, msg: fmt.Sprintf(format, args...)}
}

// Policy is how long the engine waits and retries. A step can override the wait
// limits for itself.
type Policy struct {
	// MaxAttempts is how many times a step is tried when failures are Transient.
	MaxAttempts int
	// RetryBase and RetryMax shape the backoff between attempts (doubling).
	RetryBase time.Duration
	RetryMax  time.Duration
	// PollEvery is how often a wait asks Verify again.
	PollEvery time.Duration
	// StallAfter is how long a wait may go without its Marker changing before it
	// fails as stalled. Ceiling is the last-resort total.
	StallAfter time.Duration
	Ceiling    time.Duration
}

// DefaultPolicy suits first boot on ordinary hardware: patient with slow work,
// quick to give up on work that has stopped moving.
func DefaultPolicy() Policy {
	return Policy{
		MaxAttempts: 5,
		RetryBase:   2 * time.Second,
		RetryMax:    time.Minute,
		PollEvery:   5 * time.Second,
		StallAfter:  10 * time.Minute,
		Ceiling:     time.Hour,
	}
}

// Options configures a Manager.
type Options struct {
	Journal Journal
	Clock   Clock  // defaults to real time
	Policy  Policy // zero fields take DefaultPolicy values
	// OnRecord, when set, is called with each record AFTER it has been written to the
	// journal, in order. The agent uses it to write one log line per record, so a run
	// can be followed in the system journal and in the logs the host uploads. It must
	// be quick and must not call back into the Manager.
	OnRecord func(Record)
}

// StartRequest asks for a run.
type StartRequest struct {
	Procedure string
	// Inputs are journaled so the run can resume. Never put a secret in them.
	Inputs map[string]string
	// Approved names Destructive steps this run may apply.
	Approved []string
	// StartedBy is who asked (a person's email, "mcp", "boot"), for the record.
	StartedBy string
}

// Manager runs procedures. It is safe for concurrent use. Several runs can be
// active at once; steps that change the same resource wait for each other.
type Manager struct {
	journal  Journal
	clock    Clock
	policy   Policy
	locks    *Locks
	onRecord func(Record)

	mu      sync.Mutex
	procs   map[string]*Procedure
	live    map[string]*liveRun
	records map[string][]Record // mirror of the journal, per run, for state queries

	wg      sync.WaitGroup
	closing atomic.Bool
	root    context.Context
	stop    context.CancelFunc
}

type liveRun struct {
	id        string
	proc      *Procedure
	inputs    map[string]string
	approved  map[string]bool
	cancel    context.CancelFunc
	cancelled atomic.Bool // an operator cancelled it (as opposed to the agent shutting down)
	done      chan struct{}
}

// NewManager returns a Manager. Register procedures, then call Resume to pick up
// anything unfinished from before a restart.
func NewManager(opts Options) (*Manager, error) {
	if opts.Journal == nil {
		return nil, errors.New("a manager needs a journal")
	}
	clock := opts.Clock
	if clock == nil {
		clock = realClock{}
	}
	policy := opts.Policy
	def := DefaultPolicy()
	if policy.MaxAttempts <= 0 {
		policy.MaxAttempts = def.MaxAttempts
	}
	if policy.RetryBase <= 0 {
		policy.RetryBase = def.RetryBase
	}
	if policy.RetryMax <= 0 {
		policy.RetryMax = def.RetryMax
	}
	if policy.PollEvery <= 0 {
		policy.PollEvery = def.PollEvery
	}
	if policy.StallAfter <= 0 {
		policy.StallAfter = def.StallAfter
	}
	if policy.Ceiling <= 0 {
		policy.Ceiling = def.Ceiling
	}
	root, stop := context.WithCancel(context.Background())
	return &Manager{
		journal: opts.Journal, clock: clock, policy: policy, locks: newLocks(), onRecord: opts.OnRecord,
		procs: map[string]*Procedure{}, live: map[string]*liveRun{}, records: map[string][]Record{},
		root: root, stop: stop,
	}, nil
}

// Register adds a procedure. It is rejected here if it is malformed, so mistakes
// show up at start-up and not partway through a first boot.
func (m *Manager) Register(p *Procedure) error {
	if err := p.validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, dup := m.procs[p.Name]; dup {
		return fmt.Errorf("procedure %s is already registered", p.Name)
	}
	m.procs[p.Name] = p
	return nil
}

// Start begins a run and returns its ID. The start is recorded first; if it cannot
// be recorded, nothing runs.
func (m *Manager) Start(ctx context.Context, req StartRequest) (string, error) {
	m.mu.Lock()
	proc := m.procs[req.Procedure]
	m.mu.Unlock()
	if proc == nil {
		return "", requestErr(ErrInvalid, "unknown procedure %q", req.Procedure)
	}
	approved, err := checkApprovals(proc, req.Approved)
	if err != nil {
		return "", err
	}
	if err := checkInputs(req.Inputs); err != nil {
		return "", err
	}
	id, err := newRunID()
	if err != nil {
		return "", err
	}
	lr := &liveRun{id: id, proc: proc, inputs: copyMap(req.Inputs), approved: approved}
	rec := Record{
		RunID: id, Procedure: proc.Name, Kind: KindRunStarted, Inputs: lr.inputs,
		StartedBy: req.StartedBy, Approved: sortedKeys(approved), StepNames: proc.stepNames(),
	}
	if err := m.emit(ctx, rec); err != nil {
		return "", fmt.Errorf("the run was not started because it could not be recorded: %w", err)
	}
	m.launch(lr)
	return id, nil
}

// Retry runs a failed, stopped or cancelled run again from the top. Satisfied steps
// do nothing because every step checks first. approve names Destructive steps to
// approve from now on (the usual way out of a needs-human stop).
func (m *Manager) Retry(ctx context.Context, runID, who string, approve ...string) error {
	snap, ok := m.Get(runID)
	if !ok {
		return requestErr(ErrNotFound, "no run %s", runID)
	}
	if !snap.State.Terminal() || snap.State == StateSucceeded {
		return requestErr(ErrConflict, "run %s is %s and cannot be retried", runID, snap.State)
	}
	m.mu.Lock()
	proc := m.procs[snap.Procedure]
	busy := m.live[runID] != nil
	m.mu.Unlock()
	if proc == nil {
		return requestErr(ErrConflict, "procedure %s is not registered", snap.Procedure)
	}
	if busy {
		return requestErr(ErrConflict, "run %s is still finishing", runID)
	}
	extra, err := checkApprovals(proc, approve)
	if err != nil {
		return err
	}
	approved := map[string]bool{}
	for _, name := range snap.Approved {
		approved[name] = true
	}
	for name := range extra {
		approved[name] = true
	}
	if err := m.emit(ctx, Record{RunID: runID, Procedure: proc.Name, Kind: KindRunResumed, StartedBy: who, Detail: "retry", Approved: sortedKeys(extra)}); err != nil {
		return fmt.Errorf("the retry was not started because it could not be recorded: %w", err)
	}
	m.launch(&liveRun{id: runID, proc: proc, inputs: snap.Inputs, approved: approved})
	return nil
}

// Resume restarts runs that were in progress when the agent last stopped. Call it
// once at start-up after registering procedures. A resumed run starts from the top;
// satisfied steps do nothing, so no progress is lost and nothing is repeated.
func (m *Manager) Resume(ctx context.Context) error {
	records, err := m.journal.Load(ctx)
	if err != nil {
		return fmt.Errorf("reading the run journal: %w", err)
	}
	m.mu.Lock()
	for _, rec := range records {
		m.records[rec.RunID] = append(m.records[rec.RunID], rec)
	}
	m.mu.Unlock()
	for _, snap := range Fold(records) {
		if snap.State != StateRunning {
			continue
		}
		m.mu.Lock()
		proc := m.procs[snap.Procedure]
		m.mu.Unlock()
		if proc == nil {
			// An unknown procedure (an older or newer agent wrote it) cannot be run.
			// Say so in the record rather than leave the run "running" forever.
			if err := m.emit(ctx, Record{RunID: snap.RunID, Procedure: snap.Procedure, Kind: KindRunFinished, State: StateFailed, Failure: FailBroken, Code: CodeConfig, Detail: "this agent does not know the procedure"}); err != nil {
				return err
			}
			continue
		}
		approved := map[string]bool{}
		for _, name := range snap.Approved {
			approved[name] = true
		}
		if err := m.emit(ctx, Record{RunID: snap.RunID, Procedure: proc.Name, Kind: KindRunResumed, Detail: "resumed after restart"}); err != nil {
			return err
		}
		m.launch(&liveRun{id: snap.RunID, proc: proc, inputs: snap.Inputs, approved: approved})
	}
	return nil
}

// Cancel stops a run on an operator's request and records it as cancelled.
func (m *Manager) Cancel(runID string) bool {
	m.mu.Lock()
	lr := m.live[runID]
	m.mu.Unlock()
	if lr == nil {
		return false
	}
	lr.cancelled.Store(true)
	lr.cancel()
	return true
}

// Get returns a run's current state.
func (m *Manager) Get(runID string) (Snapshot, bool) {
	m.mu.Lock()
	records := append([]Record(nil), m.records[runID]...)
	m.mu.Unlock()
	if len(records) == 0 {
		return Snapshot{}, false
	}
	return Fold(records)[0], true
}

// List returns every known run, oldest first.
func (m *Manager) List() []Snapshot {
	m.mu.Lock()
	var all []Record
	for _, records := range m.records {
		all = append(all, records...)
	}
	m.mu.Unlock()
	sort.SliceStable(all, func(i, j int) bool { return all[i].At.Before(all[j].At) })
	return Fold(all)
}

// Events returns a run's records, oldest first.
func (m *Manager) Events(runID string) []Record {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Record(nil), m.records[runID]...)
}

// Wait blocks until the run is no longer being worked on, or ctx ends.
func (m *Manager) Wait(ctx context.Context, runID string) error {
	m.mu.Lock()
	lr := m.live[runID]
	m.mu.Unlock()
	if lr == nil {
		return nil
	}
	select {
	case <-lr.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close stops the engine at agent shutdown. Runs are interrupted but NOT recorded
// as finished or cancelled, so Resume picks them up at the next start.
func (m *Manager) Close() {
	m.closing.Store(true)
	m.stop()
	m.wg.Wait()
}

func (m *Manager) launch(lr *liveRun) {
	ctx, cancel := context.WithCancel(m.root)
	lr.cancel = cancel
	lr.done = make(chan struct{})
	m.mu.Lock()
	m.live[lr.id] = lr
	m.mu.Unlock()
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer func() {
			cancel()
			m.mu.Lock()
			delete(m.live, lr.id)
			m.mu.Unlock()
			close(lr.done)
		}()
		m.execute(ctx, lr)
	}()
}

func (m *Manager) execute(ctx context.Context, lr *liveRun) {
	began := m.clock.Now()
	for _, i := range lr.proc.order {
		if failure := m.runStep(ctx, lr, &lr.proc.Steps[i]); failure != nil {
			m.finish(ctx, lr, failure, began)
			return
		}
	}
	m.finish(ctx, lr, nil, began)
}

// finish records the end of a run. When the agent itself is shutting down it records
// nothing, so the run still looks unfinished and Resume restarts it.
func (m *Manager) finish(ctx context.Context, lr *liveRun, failure *Failure, began time.Time) {
	rec := Record{RunID: lr.id, Procedure: lr.proc.Name, Kind: KindRunFinished, Millis: m.clock.Now().Sub(began).Milliseconds()}
	switch {
	case failure == nil:
		rec.State = StateSucceeded
	case failure.Kind == FailCancelled:
		if m.closing.Load() && !lr.cancelled.Load() {
			return
		}
		rec.State, rec.Failure, rec.Code, rec.Detail = StateCancelled, FailCancelled, CodeOther, "cancelled by an operator"
	case failure.Kind == FailNeedsHuman:
		rec.State, rec.Failure, rec.Code, rec.Detail = StateNeedsHuman, failure.Kind, failure.Code, failure.Detail
	default:
		rec.State, rec.Failure, rec.Code, rec.Detail = StateFailed, failure.Kind, failure.Code, failure.Detail
	}
	_ = m.emit(ctx, rec) // nothing more can be done if even this cannot be written
}

// runStep takes the step's locks, then tries it, retrying Transient failures with
// backoff. Every try starts from Check, so a retry never repeats work that
// succeeded.
func (m *Manager) runStep(ctx context.Context, lr *liveRun, step *Step) *Failure {
	release, err := m.locks.Acquire(ctx, step.Locks)
	if err != nil {
		return asFailure(err)
	}
	defer release()

	var failure *Failure
	for attempt := 1; ; attempt++ {
		failure = m.tryStep(ctx, lr, step, attempt)
		if failure == nil {
			return nil
		}
		if failure.Kind != FailTransient {
			break
		}
		if attempt >= m.policy.MaxAttempts {
			failure = &Failure{Kind: FailBroken, Code: CodeRetries, Detail: trimDetail(fmt.Sprintf("gave up after %d attempts: %s", attempt, failure.Detail)), Err: failure.Err}
			break
		}
		wait := m.backoff(attempt)
		if err := m.emit(ctx, Record{RunID: lr.id, Procedure: lr.proc.Name, Kind: KindStepRetry, Step: step.Name, Attempt: attempt, Code: failure.Code, Detail: failure.Detail, Millis: wait.Milliseconds()}); err != nil {
			return journalFailure(err)
		}
		if err := m.clock.Sleep(ctx, wait); err != nil {
			return asFailure(err)
		}
	}
	// Whatever a step reports once the run's context has ended is a consequence of the
	// cancellation (a killed program, a closed connection), not a failure of its own, so
	// it is recorded as a cancellation. A journal failure stays a journal failure.
	if ctx.Err() != nil && failure.Kind != FailJournal {
		failure = &Failure{Kind: FailCancelled, Code: CodeOther, Detail: "cancelled", Err: ctx.Err()}
	}
	if failure.Kind != FailCancelled && failure.Kind != FailJournal {
		_ = m.emit(ctx, Record{RunID: lr.id, Procedure: lr.proc.Name, Kind: KindStepFailed, Step: step.Name, Failure: failure.Kind, Code: failure.Code, Detail: failure.Detail})
	}
	return failure
}

func (m *Manager) backoff(attempt int) time.Duration {
	d := m.policy.RetryBase
	for i := 1; i < attempt && d < m.policy.RetryMax; i++ {
		d *= 2
	}
	return min(d, m.policy.RetryMax)
}

// tryStep is one pass over a step: check, decide whether applying is allowed,
// apply, wait.
func (m *Manager) tryStep(ctx context.Context, lr *liveRun, step *Step, attempt int) *Failure {
	env := Env{RunID: lr.id, Inputs: copyMap(lr.inputs)}
	began := m.clock.Now()

	status, err := step.Check(ctx, env)
	if err != nil {
		return asFailure(err)
	}
	if err := m.emit(ctx, Record{RunID: lr.id, Procedure: lr.proc.Name, Kind: KindStepChecked, Step: step.Name, Attempt: attempt, Outcome: status.String()}); err != nil {
		return journalFailure(err)
	}
	if status == Satisfied {
		return m.stepDone(ctx, lr, step, attempt, began, CodeAlready)
	}

	// Whether the step may be applied depends on how risky repeating it is. Most
	// steps are Convergent and are applied whether the check said "no" or "cannot
	// tell". The others stop and ask rather than guess.
	switch step.Kind {
	case OnceOnly:
		if status == Unknown {
			return &Failure{Kind: FailNeedsHuman, Code: CodeUnknown, Detail: trimDetail(fmt.Sprintf("cannot tell whether %s was already done, and doing it twice is not safe", step.Name))}
		}
	case Destructive:
		if status == Unknown {
			return &Failure{Kind: FailNeedsHuman, Code: CodeUnknown, Detail: trimDetail(fmt.Sprintf("cannot tell the state of %s before a destructive step", step.Name))}
		}
		if !lr.approved[step.Name] {
			return &Failure{Kind: FailNeedsHuman, Code: CodeApproval, Detail: trimDetail(fmt.Sprintf("%s is destructive and has not been approved for this run", step.Name))}
		}
	}

	// A wait-only step has no Apply: there is nothing to do but wait for it to become
	// true. Otherwise: recorded before it runs, because a step that cannot be recorded
	// is not run.
	if step.Apply != nil {
		if err := m.emit(ctx, Record{RunID: lr.id, Procedure: lr.proc.Name, Kind: KindStepApply, Step: step.Name, Attempt: attempt}); err != nil {
			return journalFailure(err)
		}
		if err := step.Apply(ctx, env); err != nil {
			return asFailure(err)
		}
		if err := m.emit(ctx, Record{RunID: lr.id, Procedure: lr.proc.Name, Kind: KindStepApplied, Step: step.Name, Attempt: attempt}); err != nil {
			return journalFailure(err)
		}
	}
	if failure := m.await(ctx, lr, step, env, attempt); failure != nil {
		return failure
	}
	return m.stepDone(ctx, lr, step, attempt, began, CodeApplied)
}

func (m *Manager) stepDone(ctx context.Context, lr *liveRun, step *Step, attempt int, began time.Time, code string) *Failure {
	rec := Record{RunID: lr.id, Procedure: lr.proc.Name, Kind: KindStepDone, Step: step.Name, Attempt: attempt, Code: code, Millis: m.clock.Now().Sub(began).Milliseconds()}
	if err := m.emit(ctx, rec); err != nil {
		return journalFailure(err)
	}
	return nil
}

// await waits for an applied step to become ready. It keeps going while the step
// reports Slow with a changing Marker, fails at once on Broken, and fails as
// stalled when the Marker stops changing or as a timeout at the ceiling.
func (m *Manager) await(ctx context.Context, lr *liveRun, step *Step, env Env, attempt int) *Failure {
	verify := step.Verify
	if verify == nil {
		verify = func(ctx context.Context, env Env) (Progress, error) {
			status, err := step.Check(ctx, env)
			if err != nil {
				return Progress{}, err
			}
			if status == Satisfied {
				return Progress{State: Ready}, nil
			}
			return Progress{State: Slow, Code: CodeStarting, Detail: "waiting for " + step.Name, Marker: "unchanged"}, nil
		}
	}
	stall, ceiling := m.policy.StallAfter, m.policy.Ceiling
	if step.StallAfter > 0 {
		stall = step.StallAfter
	}
	if step.Ceiling > 0 {
		ceiling = step.Ceiling
	}

	start := m.clock.Now()
	lastChange := start
	lastMarker := ""
	first := true
	for {
		progress, err := verify(ctx, env)
		if err != nil {
			return asFailure(err)
		}
		switch progress.State {
		case Ready:
			return nil
		case Broken:
			return &Failure{Kind: FailBroken, Code: normalizeCode(progress.Code), Detail: trimDetail(progress.Detail)}
		}
		now := m.clock.Now()
		if first || progress.Marker != lastMarker {
			first, lastMarker, lastChange = false, progress.Marker, now
			// Only a change is recorded, so a long wait is a few records, not thousands.
			if err := m.emit(ctx, Record{RunID: lr.id, Procedure: lr.proc.Name, Kind: KindStepWaiting, Step: step.Name, Attempt: attempt, Code: progress.Code, Detail: progress.Detail}); err != nil {
				return journalFailure(err)
			}
		}
		if now.Sub(lastChange) >= stall {
			return &Failure{Kind: FailStalled, Code: normalizeCode(progress.Code), Detail: trimDetail(fmt.Sprintf("no progress for %s: %s", stall, progress.Detail))}
		}
		if now.Sub(start) >= ceiling {
			return &Failure{Kind: FailTimeout, Code: normalizeCode(progress.Code), Detail: trimDetail(fmt.Sprintf("not ready after %s: %s", ceiling, progress.Detail))}
		}
		if err := m.clock.Sleep(ctx, m.policy.PollEvery); err != nil {
			return asFailure(err)
		}
	}
}

// emit stamps a record, writes it to the journal and mirrors it in memory. A
// cancelled run still records how it ended, so the write ignores cancellation.
func (m *Manager) emit(ctx context.Context, rec Record) error {
	rec.At = m.clock.Now()
	if rec.Code != "" {
		rec.Code = normalizeCode(rec.Code)
	}
	rec.Detail = trimDetail(rec.Detail)
	if err := m.journal.Append(context.WithoutCancel(ctx), rec); err != nil {
		return err
	}
	m.mu.Lock()
	m.records[rec.RunID] = append(m.records[rec.RunID], rec)
	m.mu.Unlock()
	if m.onRecord != nil {
		m.onRecord(rec)
	}
	return nil
}

func journalFailure(err error) *Failure {
	return &Failure{Kind: FailJournal, Code: CodeJournal, Detail: trimDetail("a record could not be written: " + err.Error()), Err: err}
}

// String names a Status for the journal.
func (s Status) String() string {
	switch s {
	case Satisfied:
		return "satisfied"
	case Unsatisfied:
		return "unsatisfied"
	default:
		return "unknown"
	}
}

func checkApprovals(proc *Procedure, names []string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, name := range names {
		var found *Step
		for i := range proc.Steps {
			if proc.Steps[i].Name == name {
				found = &proc.Steps[i]
			}
		}
		switch {
		case found == nil:
			return nil, requestErr(ErrInvalid, "cannot approve %q: procedure %s has no such step", name, proc.Name)
		case found.Kind != Destructive:
			return nil, requestErr(ErrInvalid, "cannot approve %q: it is not a destructive step", name)
		}
		out[name] = true
	}
	return out, nil
}

func newRunID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("creating a run id: %w", err)
	}
	return "run_" + hex.EncodeToString(b[:]), nil
}

func copyMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func sortedKeys(in map[string]bool) []string {
	out := make([]string, 0, len(in))
	for k := range in {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Limits on run inputs. They are journaled, and a journaled record travels in the
// agent's event outbox, where the cluster rejects an event over 16 KiB and a
// rejected event would block every event behind it. So inputs stay small.
const (
	maxInputs     = 32
	maxInputKey   = 64
	maxInputValue = 256
)

var secretKey = regexp.MustCompile(`(?i)(secret|token|passw|credential|private|api[-_]?key|auth)`)

// checkInputs refuses inputs that are too big or whose names say they are secrets.
// Inputs are written to the journal in clear, so a secret has no place in them: a
// step that needs one reads it from the secret authority itself.
func checkInputs(inputs map[string]string) error {
	if len(inputs) > maxInputs {
		return requestErr(ErrInvalid, "a run takes at most %d inputs", maxInputs)
	}
	for key, value := range inputs {
		switch {
		case key == "" || len(key) > maxInputKey:
			return requestErr(ErrInvalid, "input names must be 1 to %d characters", maxInputKey)
		case len(value) > maxInputValue:
			return requestErr(ErrInvalid, "input %q is longer than %d characters", key, maxInputValue)
		case secretKey.MatchString(key):
			return requestErr(ErrInvalid, "input %q looks like a secret; inputs are recorded in clear, so a step must read secrets from the secret authority itself", key)
		}
	}
	return nil
}

// StepInfo describes a step without exposing how it runs.
type StepInfo struct {
	Name  string   `json:"name"`
	Needs []string `json:"needs,omitempty"`
	Locks []string `json:"locks,omitempty"`
	Kind  string   `json:"kind"`
}

// ProcedureInfo describes a registered procedure, for the API and the UI.
type ProcedureInfo struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Steps       []StepInfo `json:"steps"`
}

// Procedures lists the registered procedures, by name, with their steps in the
// order they run.
func (m *Manager) Procedures() []ProcedureInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ProcedureInfo, 0, len(m.procs))
	for _, proc := range m.procs {
		info := ProcedureInfo{Name: proc.Name, Description: proc.Description}
		for _, i := range proc.order {
			step := proc.Steps[i]
			info.Steps = append(info.Steps, StepInfo{Name: step.Name, Needs: step.Needs, Locks: step.Locks, Kind: step.Kind.String()})
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// String names a Kind for the API.
func (k Kind) String() string {
	switch k {
	case OnceOnly:
		return "once-only"
	case Destructive:
		return "destructive"
	default:
		return "convergent"
	}
}
