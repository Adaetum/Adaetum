package cleanup

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/inspect"
)

type managerHarness struct {
	mu          sync.Mutex
	locked      bool
	lockErr     error
	auditErr    error
	auditOpened int
	auditClosed int
	entries     []Entry
	removed     []string
	gate        chan struct{} // when set, Remove blocks until it is closed or released
	inUseErr    error
	now         time.Time
	manager     *Manager
}

func newManagerHarness() *managerHarness {
	h := &managerHarness{now: now}
	h.manager = NewManager(Env{
		Hostname: func() (string, error) { return "node-a", nil },
		Now:      func() time.Time { return h.now },
		Lock: func() (func(), error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			if h.lockErr != nil {
				return nil, h.lockErr
			}
			h.locked = true
			return func() { h.mu.Lock(); h.locked = false; h.mu.Unlock() }, nil
		},
		OpenAudit: func() (func(Entry) error, func() error, error) {
			if h.auditErr != nil {
				return nil, nil, h.auditErr
			}
			h.auditOpened++
			return func(entry Entry) error {
					h.mu.Lock()
					defer h.mu.Unlock()
					h.entries = append(h.entries, entry)
					return nil
				},
				func() error { h.auditClosed++; return nil }, nil
		},
		Replan: func(_ context.Context, _ inspect.PlanOptions) (inspect.CleanupPlan, error) {
			return planOf(item("a"), item("b"), item("c")), nil
		},
		NewInUse: func(context.Context) (func(context.Context, string) (bool, error), error) {
			if h.inUseErr != nil {
				return nil, h.inUseErr
			}
			return func(context.Context, string) (bool, error) { return false, nil }, nil
		},
		Remove: func(ctx context.Context, imageID string) error {
			if h.gate != nil {
				select {
				case <-h.gate:
				case <-ctx.Done():
				}
			}
			h.mu.Lock()
			h.removed = append(h.removed, imageID)
			h.mu.Unlock()
			return nil
		},
	})
	return h
}

func (h *managerHarness) isLocked() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.locked
}

func TestAJobRunsToCompletionAndReleasesEverything(t *testing.T) {
	h := newManagerHarness()
	jobID, err := h.manager.Start(planOf(item("a"), item("b"), item("c")), Options{Trigger: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	h.manager.Wait()

	job, ok := h.manager.Get(jobID)
	if !ok || job.State != JobDone || job.Outcome == nil || job.Outcome.Removed != 3 || job.Trigger != "ui" || job.EndedAt == nil {
		t.Fatalf("unexpected job: %+v", job)
	}
	if len(job.Entries) == 0 || job.Entries[0].Event != "start" || job.Entries[0].Trigger != "ui" {
		t.Fatalf("progress entries must be kept for the UI: %+v", job.Entries)
	}
	if h.isLocked() || h.auditClosed != 1 {
		t.Fatalf("the lock and audit log must be released: locked=%v closed=%d", h.isLocked(), h.auditClosed)
	}
}

func TestASecondRunIsRefusedWhileOneIsActive(t *testing.T) {
	h := newManagerHarness()
	h.gate = make(chan struct{})
	if _, err := h.manager.Start(planOf(item("a")), Options{}); err != nil {
		t.Fatal(err)
	}
	if !h.isLocked() {
		t.Fatal("the lock must be held for the whole run")
	}
	if _, err := h.manager.Start(planOf(item("b")), Options{}); !errors.Is(err, ErrBusy) {
		t.Fatalf("expected ErrBusy, got %v", err)
	}
	close(h.gate)
	h.manager.Wait()
	if _, err := h.manager.Start(planOf(item("c")), Options{}); err != nil {
		t.Fatalf("a new run must be allowed after the first finishes: %v", err)
	}
	h.manager.Wait()
}

func TestARunIsRefusedWhenTheCommandLineHoldsTheLock(t *testing.T) {
	h := newManagerHarness()
	h.lockErr = errors.New("another image cleanup is already running")
	if _, err := h.manager.Start(planOf(item("a")), Options{}); !errors.Is(err, ErrBusy) {
		t.Fatalf("expected ErrBusy, got %v", err)
	}
	if h.auditOpened != 0 || len(h.removed) != 0 {
		t.Fatal("nothing may happen without the lock")
	}
}

func TestNoAuditLogMeansNoRunAndTheLockIsReleased(t *testing.T) {
	h := newManagerHarness()
	h.auditErr = errors.New("read-only file system")
	if _, err := h.manager.Start(planOf(item("a")), Options{}); err == nil {
		t.Fatal("expected an error")
	}
	if h.isLocked() || len(h.removed) != 0 {
		t.Fatalf("a failed start must not leave the lock held or remove anything: locked=%v", h.isLocked())
	}
	if _, ok := h.manager.Latest(); ok {
		t.Fatal("a run that never started must not appear as a job")
	}
}

func TestCancelStopsARunBetweenImages(t *testing.T) {
	h := newManagerHarness()
	h.gate = make(chan struct{})
	jobID, err := h.manager.Start(planOf(item("a"), item("b"), item("c")), Options{})
	if err != nil {
		t.Fatal(err)
	}
	h.manager.Cancel()
	h.manager.Wait()
	job, _ := h.manager.Get(jobID)
	if job.State != JobDone || job.Outcome == nil || job.Outcome.Stopped != "cancelled" {
		t.Fatalf("a cancelled run must say so: %+v", job)
	}
	if len(h.removed) > 1 {
		t.Fatalf("cancelling must not let the run continue through the plan: %v", h.removed)
	}
}

func TestAPlanThatCannotBeAppliedFailsTheJobWithTheReason(t *testing.T) {
	h := newManagerHarness()
	h.now = now.Add(time.Hour) // the plan expired
	// Start itself reads the clock for the job's start time; expiry is judged by Apply.
	jobID, err := h.manager.Start(planOf(item("a")), Options{})
	if err != nil {
		t.Fatal(err)
	}
	h.manager.Wait()
	job, _ := h.manager.Get(jobID)
	if job.State != JobFailed || !strings.Contains(job.Error, "expired") || len(h.removed) != 0 {
		t.Fatalf("an expired plan must fail the job without removing anything: %+v", job)
	}
}

func TestAJobFailsIfContainersCannotBeRead(t *testing.T) {
	h := newManagerHarness()
	h.inUseErr = errors.New("runtime down")
	jobID, _ := h.manager.Start(planOf(item("a")), Options{})
	h.manager.Wait()
	job, _ := h.manager.Get(jobID)
	if job.State != JobFailed || len(h.removed) != 0 {
		t.Fatalf("unexpected: %+v", job)
	}
}

func TestOnlyRecentJobsAreKept(t *testing.T) {
	h := newManagerHarness()
	var first string
	for index := 0; index < keptJobs+3; index++ {
		id, err := h.manager.Start(planOf(item("a")), Options{})
		if err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			first = id
		}
		h.manager.Wait()
	}
	if _, ok := h.manager.Get(first); ok {
		t.Fatal("old jobs must be forgotten so memory stays bounded")
	}
	if latest, ok := h.manager.Latest(); !ok || latest.ID == first {
		t.Fatal("the latest job must be available")
	}
}

func TestProgressSnapshotsAreCopies(t *testing.T) {
	h := newManagerHarness()
	jobID, _ := h.manager.Start(planOf(item("a")), Options{})
	h.manager.Wait()
	first, _ := h.manager.Get(jobID)
	first.Entries[0].Event = "tampered"
	second, _ := h.manager.Get(jobID)
	if second.Entries[0].Event == "tampered" {
		t.Fatal("callers must not be able to alter the manager's record")
	}
}
