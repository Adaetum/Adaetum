package cleanup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/inspect"
)

// A schedule is unattended deletion, so it is deliberately narrower than a manual
// run: the same plan rules, the same audit trail and lock, a cap on images per
// run, an optional "only if a disk is this full" condition, and a check-only mode.

const (
	FrequencyOff     = "off"
	FrequencyDaily   = "daily"
	FrequencyWeekly  = "weekly"
	FrequencyMonthly = "monthly"

	// missedRunGrace is how late a run may start. If the agent or server was down
	// across the scheduled time, a run hours later would surprise the operator, so
	// a missed run is skipped and the next one is scheduled.
	missedRunGrace = 2 * time.Hour

	// A cleanup is bounded, but not tightly: a server with a long backlog of
	// images needs a few runs, not dozens.
	defaultMaxImages = 100
	maxScheduledMax  = 200
)

var (
	clockPattern    = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
	registryPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?(:[0-9]{1,5})?$`)
)

// Schedule is the operator's choice. It is stored on the server, not in the browser.
type Schedule struct {
	Frequency  string `json:"frequency"`
	Weekday    int    `json:"weekday"`    // 0 is Sunday; used when weekly
	DayOfMonth int    `json:"dayOfMonth"` // 1 to 28; used when monthly
	Time       string `json:"time"`       // HH:MM, the server's local time
	// Level is careful or aggressive; see inspect.LevelAggressive.
	Level string `json:"level"`
	// OnlyIfDiskOverPercent skips the run unless the disk is at least this full.
	// Zero means always run.
	OnlyIfDiskOverPercent int      `json:"onlyIfDiskOverPercent"`
	IncludeOrphans        bool     `json:"includeOrphans"`
	AllowRegistries       []string `json:"allowRegistries"`
	MaxImages             int      `json:"maxImages"`
	// CheckOnly runs every check and records what would be removed, deleting nothing.
	CheckOnly bool `json:"checkOnly"`
}

// DefaultSchedule is off, inside the maintenance window, with the narrow scope.
func DefaultSchedule() Schedule {
	return Schedule{Frequency: FrequencyOff, Weekday: 0, DayOfMonth: 1, Time: "04:00", Level: inspect.LevelAggressive, MaxImages: defaultMaxImages, AllowRegistries: []string{}}
}

// ValidateLevel checks a plan level from the operator or browser.
func ValidateLevel(level string) error {
	if level != inspect.LevelCareful && level != inspect.LevelAggressive {
		return fmt.Errorf("level must be careful or aggressive")
	}
	return nil
}

func (s Schedule) Validate() error {
	if err := ValidateLevel(s.Level); err != nil {
		return err
	}
	switch s.Frequency {
	case FrequencyOff, FrequencyDaily, FrequencyWeekly, FrequencyMonthly:
	default:
		return fmt.Errorf("frequency must be off, daily, weekly, or monthly")
	}
	if !clockPattern.MatchString(s.Time) {
		return fmt.Errorf("time must look like 04:00")
	}
	if s.Weekday < 0 || s.Weekday > 6 {
		return fmt.Errorf("weekday must be 0 (Sunday) to 6")
	}
	// Capped at 28 so every month has the day.
	if s.DayOfMonth < 1 || s.DayOfMonth > 28 {
		return fmt.Errorf("day of month must be 1 to 28")
	}
	if s.OnlyIfDiskOverPercent < 0 || s.OnlyIfDiskOverPercent > 99 {
		return fmt.Errorf("the disk percentage must be 0 to 99")
	}
	if s.MaxImages < 1 || s.MaxImages > maxScheduledMax {
		return fmt.Errorf("images per run must be 1 to %d", maxScheduledMax)
	}
	if err := ValidateRegistries(s.AllowRegistries); err != nil {
		return err
	}
	return nil
}

// ValidateRegistries checks registry names from the operator or browser.
func ValidateRegistries(registries []string) error {
	if len(registries) > 20 {
		return fmt.Errorf("at most 20 registries")
	}
	for _, registry := range registries {
		if !registryPattern.MatchString(registry) {
			return fmt.Errorf("%q is not a registry name", registry)
		}
	}
	return nil
}

func (s Schedule) planOptions() inspect.PlanOptions {
	return inspect.PlanOptions{Level: s.Level, AllowedRegistries: s.AllowRegistries, IncludeOrphans: s.IncludeOrphans}
}

// NextRun is the first scheduled time strictly after `after`, in location loc.
// It returns the zero time for a schedule that is off.
func NextRun(s Schedule, after time.Time, loc *time.Location) time.Time {
	if s.Frequency == FrequencyOff || !clockPattern.MatchString(s.Time) {
		return time.Time{}
	}
	var hour, minute int
	fmt.Sscanf(s.Time, "%d:%d", &hour, &minute)
	local := after.In(loc)
	at := func(year int, month time.Month, day int) time.Time {
		return time.Date(year, month, day, hour, minute, 0, 0, loc)
	}

	switch s.Frequency {
	case FrequencyDaily:
		candidate := at(local.Year(), local.Month(), local.Day())
		if !candidate.After(after) {
			candidate = candidate.AddDate(0, 0, 1)
		}
		return candidate
	case FrequencyWeekly:
		candidate := at(local.Year(), local.Month(), local.Day())
		candidate = candidate.AddDate(0, 0, (s.Weekday-int(candidate.Weekday())+7)%7)
		if !candidate.After(after) {
			candidate = candidate.AddDate(0, 0, 7)
		}
		return candidate
	default: // monthly
		candidate := at(local.Year(), local.Month(), s.DayOfMonth)
		if !candidate.After(after) {
			candidate = at(local.Year(), local.Month()+1, s.DayOfMonth)
		}
		return candidate
	}
}

// LastRun is what the scheduler last decided, shown beside the schedule.
type LastRun struct {
	At     time.Time `json:"at"`
	Status string    `json:"status"` // started, skipped, nothing-to-remove, failed
	Detail string    `json:"detail"`
	JobID  string    `json:"jobId,omitempty"`
}

// ScheduleState is a schedule plus what has happened under it.
type ScheduleState struct {
	Schedule Schedule   `json:"schedule"`
	NextRun  *time.Time `json:"nextRun,omitempty"`
	LastRun  *LastRun   `json:"lastRun,omitempty"`
}

// ScheduleStore keeps the schedule in one JSON file, written atomically.
type ScheduleStore struct {
	path string
	loc  *time.Location
	mu   sync.Mutex
}

func NewScheduleStore(path string, loc *time.Location) *ScheduleStore {
	return &ScheduleStore{path: path, loc: loc}
}

func (store *ScheduleStore) Load() (ScheduleState, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.load()
}

func (store *ScheduleStore) load() (ScheduleState, error) {
	data, err := os.ReadFile(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return ScheduleState{Schedule: DefaultSchedule()}, nil
	}
	if err != nil {
		return ScheduleState{}, err
	}
	var state ScheduleState
	if err := json.Unmarshal(data, &state); err != nil {
		return ScheduleState{}, fmt.Errorf("the saved schedule is damaged: %w", err)
	}
	// A hand-edited or damaged file must not become an unvalidated schedule.
	if err := state.Schedule.Validate(); err != nil {
		return ScheduleState{}, fmt.Errorf("the saved schedule is invalid: %w", err)
	}
	return state, nil
}

func (store *ScheduleStore) save(state ScheduleState) error {
	if err := os.MkdirAll(filepath.Dir(store.path), 0o750); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	// Write to a neighbor and rename, so a crash never leaves half a file.
	temp, err := os.CreateTemp(filepath.Dir(store.path), ".schedule-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Chmod(0o640); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), store.path)
}

// SetSchedule validates and saves a schedule and works out when it next runs.
func (store *ScheduleStore) SetSchedule(schedule Schedule, now time.Time) (ScheduleState, error) {
	if err := schedule.Validate(); err != nil {
		return ScheduleState{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	state, err := store.load()
	if err != nil {
		// Replacing a damaged file with a valid schedule is the way to repair it.
		state = ScheduleState{}
	}
	state.Schedule = schedule
	state.NextRun = nil
	if next := NextRun(schedule, now, store.loc); !next.IsZero() {
		state.NextRun = &next
	}
	return state, store.save(state)
}

func (store *ScheduleStore) update(change func(*ScheduleState)) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	state, err := store.load()
	if err != nil {
		return err
	}
	change(&state)
	return store.save(state)
}

// Starter begins a cleanup run; the job manager implements it.
type Starter interface {
	Start(plan inspect.CleanupPlan, options Options) (jobID string, err error)
}

// Scheduler decides, once a tick, whether a scheduled run is due and starts it.
type Scheduler struct {
	Store   *ScheduleStore
	Starter Starter
	Planner func(ctx context.Context, options inspect.PlanOptions) (inspect.CleanupPlan, error)
	// DiskUsedPercent reports how full the disk holding the images is.
	DiskUsedPercent func() float64
	Location        *time.Location
}

// Run ticks until the context ends.
func (scheduler *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			scheduler.Tick(ctx, now)
		}
	}
}

// Tick makes one scheduling decision. It never runs two things at once: the job
// manager refuses a second run while one is active.
func (scheduler *Scheduler) Tick(ctx context.Context, now time.Time) {
	state, err := scheduler.Store.Load()
	if err != nil || state.Schedule.Frequency == FrequencyOff {
		return
	}
	loc := scheduler.Location
	if loc == nil {
		loc = time.Local
	}
	if state.NextRun == nil {
		// A schedule without a next time (for example, a freshly repaired file)
		// starts counting from now instead of running immediately.
		next := NextRun(state.Schedule, now, loc)
		_ = scheduler.Store.update(func(s *ScheduleState) { s.NextRun = &next })
		return
	}
	if now.Before(*state.NextRun) {
		return
	}

	due := *state.NextRun
	next := NextRun(state.Schedule, now, loc)
	record := func(status, detail, jobID string) {
		_ = scheduler.Store.update(func(s *ScheduleState) {
			s.NextRun = &next
			s.LastRun = &LastRun{At: now, Status: status, Detail: detail, JobID: jobID}
		})
	}

	if now.Sub(due) > missedRunGrace {
		record("skipped", fmt.Sprintf("The run due at %s was missed because the agent or server was not running.", due.In(loc).Format("Mon 15:04")), "")
		return
	}
	if threshold := state.Schedule.OnlyIfDiskOverPercent; threshold > 0 {
		if used := scheduler.DiskUsedPercent(); used < float64(threshold) {
			record("skipped", fmt.Sprintf("The disk is %.0f%% full, below the %d%% this schedule waits for.", used, threshold), "")
			return
		}
	}

	plan, err := scheduler.Planner(ctx, state.Schedule.planOptions())
	if err != nil {
		record("failed", "Could not make a plan: "+err.Error(), "")
		return
	}
	if len(plan.Items) == 0 {
		record("nothing-to-remove", "Everything that could be removed safely was already gone.", "")
		return
	}
	jobID, err := scheduler.Starter.Start(plan, Options{DryRun: state.Schedule.CheckOnly, MaxImages: state.Schedule.MaxImages, Trigger: "schedule"})
	if err != nil {
		record("skipped", err.Error(), "")
		return
	}
	detail := fmt.Sprintf("Started on %d planned images.", len(plan.Items))
	if state.Schedule.CheckOnly {
		detail = fmt.Sprintf("Checked %d planned images without removing any.", len(plan.Items))
	}
	record("started", detail, jobID)
}
