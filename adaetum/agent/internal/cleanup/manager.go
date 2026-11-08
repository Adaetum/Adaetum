package cleanup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/inspect"
)

// ErrBusy means a cleanup is already running, here or from the command line.
var ErrBusy = errors.New("another cleanup is already running")

// maxJobEntries bounds the progress kept in memory for one job. The audit log
// holds everything; this is only what the UI polls.
const (
	maxJobEntries = 1000
	keptJobs      = 20
)

type JobState string

const (
	JobRunning JobState = "running"
	JobDone    JobState = "done"
	JobFailed  JobState = "failed"
)

// OutcomeSummary is the result of a finished run, in the shape the UI shows.
type OutcomeSummary struct {
	DryRun                 bool   `json:"dryRun"`
	Removed                int    `json:"removed"`
	Skipped                int    `json:"skipped"`
	Failed                 int    `json:"failed"`
	RemovedCompressedBytes uint64 `json:"removedCompressedBytes"`
	Stopped                string `json:"stopped,omitempty"`
}

// Job is one run, as seen while it runs and after.
type Job struct {
	ID        string          `json:"id"`
	Trigger   string          `json:"trigger"`
	PlanID    string          `json:"planId"`
	DryRun    bool            `json:"dryRun"`
	State     JobState        `json:"state"`
	StartedAt time.Time       `json:"startedAt"`
	EndedAt   *time.Time      `json:"endedAt,omitempty"`
	Entries   []Entry         `json:"entries"`
	Outcome   *OutcomeSummary `json:"outcome,omitempty"`
	Error     string          `json:"error,omitempty"`
	// Total is the number of images the run will consider, for a progress bar.
	Total int `json:"total"`
}

// Env binds the manager to the real machine. Production code supplies the live
// implementations; tests supply fakes.
type Env struct {
	Hostname func() (string, error)
	Now      func() time.Time
	// Lock takes the cross-process lock shared with the command line.
	Lock func() (unlock func(), err error)
	// OpenAudit opens the audit trail for one run.
	OpenAudit func() (audit func(Entry) error, closeLog func() error, err error)
	Replan    func(ctx context.Context, options inspect.PlanOptions) (inspect.CleanupPlan, error)
	// NewInUse builds the per-image "is a container using this" check.
	NewInUse func(ctx context.Context) (func(context.Context, string) (bool, error), error)
	Remove   func(ctx context.Context, id string) error
}

// Manager runs at most one cleanup at a time and remembers recent ones.
type Manager struct {
	env    Env
	mu     sync.Mutex
	jobs   map[string]*Job
	order  []string
	active *Job
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewManager(env Env) *Manager {
	return &Manager{env: env, jobs: map[string]*Job{}}
}

// Start begins a run in the background and returns at once with its ID. It
// refuses if a run is already active or the cross-process lock is held.
func (m *Manager) Start(plan inspect.CleanupPlan, options Options) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active != nil {
		return "", ErrBusy
	}
	unlock, err := m.env.Lock()
	if err != nil {
		return "", fmt.Errorf("%w (%v)", ErrBusy, err)
	}
	audit, closeLog, err := m.env.OpenAudit()
	if err != nil {
		unlock()
		return "", fmt.Errorf("cannot open the audit log, so nothing was started: %w", err)
	}

	random := make([]byte, 6)
	if _, err := rand.Read(random); err != nil {
		unlock()
		_ = closeLog()
		return "", err
	}
	job := &Job{
		ID: hex.EncodeToString(random), Trigger: options.Trigger, PlanID: plan.ID, DryRun: options.DryRun,
		State: JobRunning, StartedAt: m.env.Now().UTC(), Entries: []Entry{}, Total: len(plan.Items),
	}
	if options.MaxImages > 0 && options.MaxImages < job.Total {
		job.Total = options.MaxImages
	}
	m.jobs[job.ID] = job
	m.order = append(m.order, job.ID)
	for len(m.order) > keptJobs {
		delete(m.jobs, m.order[0])
		m.order = m.order[1:]
	}
	m.active = job

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.wg.Add(1)
	go m.run(ctx, job, plan, options, audit, closeLog, unlock)
	return job.ID, nil
}

func (m *Manager) run(ctx context.Context, job *Job, plan inspect.CleanupPlan, options Options,
	audit func(Entry) error, closeLog func() error, unlock func()) {
	defer m.wg.Done()
	defer unlock()
	defer func() { _ = closeLog() }()

	finish := func(outcome *Outcome, err error) {
		m.mu.Lock()
		defer m.mu.Unlock()
		ended := m.env.Now().UTC()
		job.EndedAt = &ended
		job.State = JobDone
		if err != nil {
			job.State, job.Error = JobFailed, err.Error()
		}
		if outcome != nil {
			job.Outcome = &OutcomeSummary{
				DryRun: outcome.DryRun, Removed: len(outcome.Removed), Skipped: len(outcome.Skipped), Failed: len(outcome.Failed),
				RemovedCompressedBytes: outcome.RemovedCompressedBytes(), Stopped: outcome.Stopped,
			}
		}
		m.active, m.cancel = nil, nil
	}

	inUse, err := m.env.NewInUse(ctx)
	if err != nil {
		finish(nil, fmt.Errorf("cannot read this server's containers, so nothing was removed: %w", err))
		return
	}
	deps := Deps{
		Hostname: m.env.Hostname, Now: m.env.Now, Replan: m.env.Replan, InUse: inUse, Remove: m.env.Remove,
		// Every entry goes to the durable log first, then to the in-memory view the
		// UI polls. If the log write fails, Apply stops.
		Audit: func(entry Entry) error {
			if err := audit(entry); err != nil {
				return err
			}
			m.mu.Lock()
			if len(job.Entries) < maxJobEntries {
				job.Entries = append(job.Entries, entry)
			}
			m.mu.Unlock()
			return nil
		},
	}
	outcome, err := Apply(ctx, plan, options, deps)
	finish(&outcome, err)
}

// Get returns a snapshot of a job.
func (m *Manager) Get(id string) (Job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[id]
	if !ok {
		return Job{}, false
	}
	return snapshot(job), true
}

// Latest returns the most recent job, if any.
func (m *Manager) Latest() (Job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.order) == 0 {
		return Job{}, false
	}
	return snapshot(m.jobs[m.order[len(m.order)-1]]), true
}

// Cancel asks the active run to stop between images.
func (m *Manager) Cancel() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
	}
}

// Wait blocks until the active run ends. It exists for tests and shutdown.
func (m *Manager) Wait() { m.wg.Wait() }

func snapshot(job *Job) Job {
	copy := *job
	copy.Entries = append([]Entry{}, job.Entries...)
	return copy
}
