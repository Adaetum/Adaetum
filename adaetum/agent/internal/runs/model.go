package runs

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// State is where a run is.
type State string

const (
	// StateRunning means the run has started and not finished. After an agent
	// restart a run in this state is resumed.
	StateRunning State = "running"
	// StateSucceeded means every step is satisfied.
	StateSucceeded State = "succeeded"
	// StateFailed means a step failed and automatic handling is finished.
	StateFailed State = "failed"
	// StateNeedsHuman means the engine stopped on purpose: it could not tell what
	// is true, or a destructive step has no approval. A person decides.
	StateNeedsHuman State = "needs-human"
	// StateCancelled means an operator stopped the run.
	StateCancelled State = "cancelled"
)

// Terminal reports whether the run will not continue by itself.
func (s State) Terminal() bool { return s != StateRunning }

// StepState is where one step is within a run.
type StepState string

const (
	StepPending  StepState = "pending"
	StepApplying StepState = "applying"
	StepWaiting  StepState = "waiting"
	StepDone     StepState = "done"
	StepFailed   StepState = "failed"
)

// FailureKind says why a step failed. It is a small fixed set so failures can be
// counted.
type FailureKind string

const (
	// FailTransient is retried automatically with backoff. It is reported only
	// after the retries run out (and then as FailBroken).
	FailTransient FailureKind = "transient"
	// FailBroken means retrying will not help; the reason says what is wrong.
	FailBroken FailureKind = "broken"
	// FailNeedsHuman means the engine stopped on purpose for a person to decide.
	FailNeedsHuman FailureKind = "needs-human"
	// FailStalled means a wait made no progress for the stall window.
	FailStalled FailureKind = "stalled"
	// FailTimeout means a wait hit its overall ceiling.
	FailTimeout FailureKind = "timeout"
	// FailCancelled means the run was cancelled.
	FailCancelled FailureKind = "cancelled"
	// FailJournal means a record could not be written, so work stopped rather than
	// continue unrecorded.
	FailJournal FailureKind = "journal"
)

// Reason codes. This list is closed on purpose: analytics needs a vocabulary it
// can count, and free text is where secrets leak. A step that returns a code not
// listed here is recorded as CodeOther.
const (
	CodeOther         = "other"
	CodeAlready       = "already"       // check found it already true
	CodeApplied       = "applied"       // apply made it true
	CodeStarting      = "starting"      // a thing is coming up normally
	CodeImagePull     = "image-pull"    // image cannot be pulled
	CodeImagePulling  = "image-pulling" // image is still being pulled (slow, fine)
	CodeCrashLoop     = "crash-loop"    // container keeps crashing
	CodeUnschedulable = "unschedulable" // pod cannot be placed
	CodeVolume        = "volume"        // storage not bound or attached
	CodeNetwork       = "network"       // cannot reach something it needs
	CodeAuth          = "auth"          // credentials refused
	CodeConfig        = "config"        // input or configuration is wrong
	CodeDisk          = "disk"          // not enough free disk space
	CodeSealed        = "sealed"        // OpenBao is sealed
	CodeUnknown       = "unknown"       // the check could not tell
	CodeApproval      = "approval"      // a destructive step has no approval
	CodeRetries       = "retries"       // automatic retries ran out
	CodeJournal       = "journal"       // a record could not be written
)

var knownCodes = map[string]bool{
	CodeOther: true, CodeAlready: true, CodeApplied: true, CodeStarting: true,
	CodeImagePull: true, CodeImagePulling: true, CodeCrashLoop: true,
	CodeUnschedulable: true, CodeVolume: true, CodeNetwork: true, CodeAuth: true,
	CodeConfig: true, CodeDisk: true, CodeSealed: true, CodeUnknown: true, CodeApproval: true,
	CodeRetries: true, CodeJournal: true,
}

// normalizeCode keeps the recorded vocabulary closed.
func normalizeCode(code string) string {
	if knownCodes[code] {
		return code
	}
	return CodeOther
}

// maxDetail bounds the human sentence in a record. It is for the person watching,
// is never analysed, and must not carry secrets.
const maxDetail = 300

// secretish catches the common ways a credential ends up inside an error message
// (a URL query string, a "password=..." fragment). It is a safety net, not a
// licence to put secrets in errors.
var secretish = regexp.MustCompile(`(?i)\b(token|secret|password|passwd|key|authorization)(=|:\s*)\S+`)

func trimDetail(detail string) string {
	detail = secretish.ReplaceAllString(strings.TrimSpace(detail), "${1}${2}REDACTED")
	if len(detail) > maxDetail {
		detail = detail[:maxDetail] + "..."
	}
	return detail
}

// Record kinds. A run's history is a sequence of these.
const (
	KindRunStarted  = "run.started"
	KindRunResumed  = "run.resumed"
	KindRunFinished = "run.finished"
	KindStepChecked = "step.checked"
	KindStepApply   = "step.applying"
	KindStepApplied = "step.applied"
	KindStepWaiting = "step.waiting"
	KindStepRetry   = "step.retry"
	KindStepDone    = "step.done"
	KindStepFailed  = "step.failed"
)

// Record is one fact about a run. It is append-only: nobody edits a record, and a
// run's state is derived by folding its records (see Fold).
type Record struct {
	RunID     string            `json:"runId"`
	Procedure string            `json:"procedure"`
	Kind      string            `json:"kind"`
	At        time.Time         `json:"at"`
	Step      string            `json:"step,omitempty"`
	Attempt   int               `json:"attempt,omitempty"`
	Outcome   string            `json:"outcome,omitempty"` // check result: satisfied, unsatisfied, unknown
	Code      string            `json:"code,omitempty"`    // from the closed list above
	Detail    string            `json:"detail,omitempty"`  // short human sentence, no secrets
	Failure   FailureKind       `json:"failure,omitempty"`
	State     State             `json:"state,omitempty"` // on run.finished
	Millis    int64             `json:"ms,omitempty"`    // how long the step or run took
	Inputs    map[string]string `json:"inputs,omitempty"`
	StartedBy string            `json:"startedBy,omitempty"`
	Approved  []string          `json:"approved,omitempty"` // destructive steps approved for this run
	StepNames []string          `json:"steps,omitempty"`    // on run.started: every step, in order, so a run shows what is still ahead
}

// StepStatus is one step's current standing within a run.
type StepStatus struct {
	Name     string      `json:"name"`
	State    StepState   `json:"state"`
	Attempts int         `json:"attempts"`
	Code     string      `json:"code,omitempty"`
	Detail   string      `json:"detail,omitempty"`
	Failure  FailureKind `json:"failure,omitempty"`
}

// Snapshot is a run as it stands, derived from its records.
type Snapshot struct {
	RunID      string            `json:"id"`
	Procedure  string            `json:"procedure"`
	Inputs     map[string]string `json:"inputs,omitempty"`
	StartedBy  string            `json:"startedBy,omitempty"`
	Approved   []string          `json:"approved,omitempty"`
	Attempt    int               `json:"attempt"` // how many times the run has been started (retries and resumes count)
	State      State             `json:"state"`
	Steps      []StepStatus      `json:"steps"`
	Started    time.Time         `json:"started"`
	Finished   time.Time         `json:"finished,omitzero"`
	FailedStep string            `json:"failedStep,omitempty"`
	Failure    FailureKind       `json:"failure,omitempty"`
	Code       string            `json:"code,omitempty"`
	Detail     string            `json:"detail,omitempty"`
}

// Fold turns a sequence of records into the runs they describe, in the order the
// runs first appear. It is the only way run state is computed, so state cannot
// disagree with history.
func Fold(records []Record) []Snapshot {
	var order []string
	byID := map[string]*Snapshot{}
	for _, rec := range records {
		snap := byID[rec.RunID]
		if snap == nil {
			snap = &Snapshot{RunID: rec.RunID, Procedure: rec.Procedure}
			byID[rec.RunID] = snap
			order = append(order, rec.RunID)
		}
		switch rec.Kind {
		case KindRunStarted, KindRunResumed:
			if rec.Kind == KindRunStarted {
				snap.Inputs, snap.StartedBy, snap.Approved = rec.Inputs, rec.StartedBy, rec.Approved
				for _, name := range rec.StepNames {
					snap.step(name)
				}
			}
			if rec.Kind == KindRunResumed && len(rec.Approved) > 0 {
				// A retry can approve more destructive steps; approvals only grow.
				have := map[string]bool{}
				for _, name := range snap.Approved {
					have[name] = true
				}
				for _, name := range rec.Approved {
					if !have[name] {
						snap.Approved = append(snap.Approved, name)
					}
				}
			}
			snap.Attempt++
			snap.State = StateRunning
			snap.Started, snap.Finished = rec.At, time.Time{}
			snap.FailedStep, snap.Failure, snap.Code, snap.Detail = "", "", "", ""
			for i := range snap.Steps {
				snap.Steps[i].State = StepPending
			}
		case KindRunFinished:
			snap.State, snap.Finished = rec.State, rec.At
			snap.Failure, snap.Code, snap.Detail = rec.Failure, rec.Code, rec.Detail
		default:
			step := snap.step(rec.Step)
			if step == nil {
				continue
			}
			step.Attempts = max(step.Attempts, rec.Attempt)
			switch rec.Kind {
			case KindStepApply:
				step.State = StepApplying
			case KindStepWaiting:
				step.State, step.Code, step.Detail = StepWaiting, rec.Code, rec.Detail
			case KindStepDone:
				step.State, step.Code, step.Detail, step.Failure = StepDone, rec.Code, rec.Detail, ""
			case KindStepFailed:
				step.State, step.Code, step.Detail, step.Failure = StepFailed, rec.Code, rec.Detail, rec.Failure
				snap.FailedStep = rec.Step
			}
		}
	}
	out := make([]Snapshot, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out
}

// step finds a step by name, adding it in first-seen order.
func (s *Snapshot) step(name string) *StepStatus {
	if name == "" {
		return nil
	}
	for i := range s.Steps {
		if s.Steps[i].Name == name {
			return &s.Steps[i]
		}
	}
	s.Steps = append(s.Steps, StepStatus{Name: name, State: StepPending})
	return &s.Steps[len(s.Steps)-1]
}

// Line formats a record as one log line: "[RUN]" and the time, then key=value pairs, in
// a fixed order, with empty fields left out. It is meant to be found with grep in the
// system journal and in the logs a host uploads:
//
//	[RUN] 2026-11-12T09:00:00Z run=run_ab12 procedure=host-readiness step=clock event=step.waiting code=starting detail="waiting for the clock to synchronize"
//
// Inputs are not written (only how many), because they are recorded in clear and a
// log is copied to places the journal is not. Detail was already redacted and trimmed
// when the record was made.
func (r Record) Line() string {
	var b strings.Builder
	b.WriteString("[RUN] ")
	b.WriteString(r.At.UTC().Format(time.RFC3339))
	add := func(key, value string) {
		if value == "" {
			return
		}
		b.WriteByte(' ')
		b.WriteString(key)
		b.WriteByte('=')
		if strings.ContainsAny(value, " \t\"=") {
			value = strconv.Quote(value)
		}
		b.WriteString(value)
	}
	add("run", r.RunID)
	add("procedure", r.Procedure)
	add("step", r.Step)
	add("event", r.Kind)
	if r.Attempt > 0 {
		add("attempt", strconv.Itoa(r.Attempt))
	}
	add("outcome", r.Outcome)
	add("state", string(r.State))
	add("failure", string(r.Failure))
	add("code", r.Code)
	if r.Millis > 0 {
		add("ms", strconv.FormatInt(r.Millis, 10))
	}
	if len(r.Inputs) > 0 {
		add("inputs", strconv.Itoa(len(r.Inputs)))
	}
	add("by", r.StartedBy)
	add("detail", r.Detail)
	return b.String()
}
