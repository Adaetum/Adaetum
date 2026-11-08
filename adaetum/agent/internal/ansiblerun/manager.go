package ansiblerun

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// Status of a run.
type Status string

const (
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
	// StatusError means Ansible could not be started at all.
	StatusError Status = "error"
)

// ExecSpec is the fixed shape of one ansible-playbook invocation. Every field was
// produced by Validate, never copied from a request.
type ExecSpec struct {
	Playbook      string
	Limit         string
	Check         bool
	ExtraVarsFile string
}

// Executor runs a playbook and writes its output. It returns Ansible's exit code;
// the error is for failing to run it at all.
//
// Implementations: LocalExec (below). KubernetesJob (kubejob.go) is a stub.
type Executor interface {
	Run(ctx context.Context, spec ExecSpec, output io.Writer) (exitCode int, err error)
}

// AuditEntry is one line of the permanent record of what was launched.
type AuditEntry struct {
	Time     time.Time         `json:"time"`
	Event    string            `json:"event"` // "start" or "finish"
	ID       string            `json:"id"`
	Playbook string            `json:"playbook"`
	Limit    []string          `json:"limit"`
	Check    bool              `json:"check"`
	Vars     map[string]string `json:"vars"` // secrets are masked
	By       string            `json:"by"`
	Status   Status            `json:"status,omitempty"`
	ExitCode *int              `json:"exitCode,omitempty"`
}

// Env is everything a Manager needs from outside, so tests can supply fakes.
type Env struct {
	Exec Executor
	// Audit records an entry. If it fails for a "start", the run is refused: the
	// agent will not launch Ansible as an administrator without being able to say
	// afterwards that it did.
	Audit func(AuditEntry) error
	Now   func() time.Time
	// MaxDuration stops a run that never finishes. Zero means two hours.
	MaxDuration time.Duration
}

// Run is a launched playbook.
type Run struct {
	ID         string            `json:"id"`
	Playbook   string            `json:"playbook"`
	Title      string            `json:"title"`
	Limit      []string          `json:"limit"`
	Check      bool              `json:"check"`
	Vars       map[string]string `json:"vars"`
	Status     Status            `json:"status"`
	StartedBy  string            `json:"startedBy"`
	StartedAt  time.Time         `json:"startedAt"`
	FinishedAt *time.Time        `json:"finishedAt,omitempty"`
	ExitCode   *int              `json:"exitCode,omitempty"`
	Error      string            `json:"error,omitempty"`
}

// RunView is a run with a window of its output. Ask again with after set to
// NextLine to get only what is new.
type RunView struct {
	Run
	Lines []string `json:"lines"`
	// FirstLine is the number of the first returned line. It exceeds the requested
	// start when older output has been dropped to keep memory bounded.
	FirstLine int  `json:"firstLine"`
	NextLine  int  `json:"nextLine"`
	Dropped   bool `json:"dropped"`
}

// ErrBusy means another run is in progress.
var ErrBusy = errors.New("another playbook is already running; wait for it to finish or cancel it")

// ErrNotFound means no run has that ID (they are kept only for a while).
var ErrNotFound = errors.New("no such run")

const keepRuns = 20

type runState struct {
	run    Run
	output *outputBuffer
	cancel context.CancelFunc
}

// Manager runs one playbook at a time and remembers the last few.
type Manager struct {
	env Env

	mu      sync.Mutex
	runs    map[string]*runState
	order   []string // oldest first
	current *runState
	wg      sync.WaitGroup
}

// NewManager returns a Manager.
func NewManager(env Env) *Manager {
	if env.Now == nil {
		env.Now = time.Now
	}
	if env.MaxDuration <= 0 {
		env.MaxDuration = 2 * time.Hour
	}
	return &Manager{env: env, runs: map[string]*runState{}}
}

// Start validates, records, and launches a request in the background. by names the
// person who asked.
func (m *Manager) Start(request Request, by string) (string, error) {
	validated, err := Validate(request)
	if err != nil {
		return "", err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current != nil {
		return "", ErrBusy
	}

	id := newID()
	state := &runState{
		run: Run{
			ID: id, Playbook: validated.Playbook.ID, Title: validated.Playbook.Title, Limit: validated.Limit,
			Check: validated.Check, Vars: validated.Display, Status: StatusRunning, StartedBy: by, StartedAt: m.env.Now().UTC(),
		},
		output: newOutputBuffer(validated.Secrets),
	}
	if err := m.record(state.run, "start"); err != nil {
		return "", fmt.Errorf("the run was not started because it could not be recorded: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), m.env.MaxDuration)
	state.cancel = cancel
	m.current = state
	m.runs[id] = state
	m.order = append(m.order, id)
	for len(m.order) > keepRuns {
		delete(m.runs, m.order[0])
		m.order = m.order[1:]
	}

	m.wg.Add(1)
	go m.execute(ctx, state, validated)
	return id, nil
}

func (m *Manager) execute(ctx context.Context, state *runState, validated Validated) {
	defer m.wg.Done()
	defer state.cancel()

	exit, err := m.runPlaybook(ctx, state, validated)

	m.mu.Lock()
	defer m.mu.Unlock()
	finished := m.env.Now().UTC()
	state.run.FinishedAt = &finished
	state.output.Flush()
	switch {
	case ctx.Err() == context.Canceled:
		state.run.Status = StatusCancelled
	case ctx.Err() == context.DeadlineExceeded:
		state.run.Status, state.run.Error = StatusFailed, "stopped: it ran longer than the time allowed"
	case err != nil:
		state.run.Status, state.run.Error = StatusError, err.Error()
	case exit == 0:
		state.run.Status = StatusSucceeded
	default:
		state.run.Status = StatusFailed
	}
	if err == nil {
		state.run.ExitCode = &exit
	}
	_ = m.record(state.run, "finish") // best effort: the start was already recorded
	m.current = nil
}

// runPlaybook writes the variables to a private file, runs the playbook, and
// removes the file. The file is how secrets reach Ansible without appearing on a
// command line or in the process list.
func (m *Manager) runPlaybook(ctx context.Context, state *runState, validated Validated) (int, error) {
	file, err := os.CreateTemp("", "adaetum-vars-*.json") // created with mode 0600
	if err != nil {
		return 0, fmt.Errorf("cannot prepare the variables: %w", err)
	}
	defer os.Remove(file.Name())
	if err := json.NewEncoder(file).Encode(validated.Values); err != nil {
		file.Close()
		return 0, fmt.Errorf("cannot prepare the variables: %w", err)
	}
	if err := file.Close(); err != nil {
		return 0, err
	}

	spec := ExecSpec{
		Playbook:      validated.Playbook.File,
		Limit:         strings.Join(validated.Limit, ","),
		Check:         validated.Check,
		ExtraVarsFile: file.Name(),
	}
	return m.env.Exec.Run(ctx, spec, state.output)
}

func (m *Manager) record(run Run, event string) error {
	if m.env.Audit == nil {
		return nil
	}
	return m.env.Audit(AuditEntry{
		Time: m.env.Now().UTC(), Event: event, ID: run.ID, Playbook: run.Playbook, Limit: run.Limit,
		Check: run.Check, Vars: run.Vars, By: run.StartedBy, Status: run.Status, ExitCode: run.ExitCode,
	})
}

// Get returns a run and its output from line `after` on.
func (m *Manager) Get(id string, after int) (RunView, error) {
	m.mu.Lock()
	state, ok := m.runs[id]
	var run Run
	if ok {
		run = state.run
	}
	m.mu.Unlock()
	if !ok {
		return RunView{}, ErrNotFound
	}
	lines, first, next, dropped := state.output.From(after)
	return RunView{Run: run, Lines: lines, FirstLine: first, NextLine: next, Dropped: dropped}, nil
}

// List returns recent runs, newest first, without their output.
func (m *Manager) List() []Run {
	m.mu.Lock()
	defer m.mu.Unlock()
	runs := make([]Run, 0, len(m.order))
	for index := len(m.order) - 1; index >= 0; index-- {
		runs = append(runs, m.runs[m.order[index]].run)
	}
	return runs
}

// Cancel stops the run in progress, if any, and reports whether there was one.
func (m *Manager) Cancel() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil {
		return false
	}
	m.current.cancel()
	return true
}

// Wait blocks until the run in progress has finished. For tests and shutdown.
func (m *Manager) Wait() { m.wg.Wait() }

func newID() string {
	raw := make([]byte, 6)
	_, _ = rand.Read(raw)
	return hex.EncodeToString(raw)
}
