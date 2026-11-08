package cleanup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/inspect"
)

var utc = time.UTC

func at(year int, month time.Month, day, hour, minute int) time.Time {
	return time.Date(year, month, day, hour, minute, 0, 0, utc)
}

func TestNextRun(t *testing.T) {
	daily := Schedule{Frequency: FrequencyDaily, Time: "04:00"}
	weeklySunday := Schedule{Frequency: FrequencyWeekly, Weekday: 0, Time: "04:00"}
	monthly := Schedule{Frequency: FrequencyMonthly, DayOfMonth: 15, Time: "04:00"}

	cases := []struct {
		name     string
		schedule Schedule
		after    time.Time
		want     time.Time
	}{
		{"daily, before today's time", daily, at(2026, 11, 4, 3, 0), at(2026, 11, 4, 4, 0)},
		{"daily, exactly at the time runs tomorrow", daily, at(2026, 11, 4, 4, 0), at(2026, 11, 5, 4, 0)},
		{"daily, after today's time", daily, at(2026, 11, 4, 5, 0), at(2026, 11, 5, 4, 0)},
		{"daily across a month end", daily, at(2026, 11, 30, 5, 0), at(2026, 12, 1, 4, 0)},
		{"weekly from a Wednesday", weeklySunday, at(2026, 11, 4, 12, 0), at(2026, 11, 8, 4, 0)},
		{"weekly on the day, before the time", weeklySunday, at(2026, 11, 8, 3, 0), at(2026, 11, 8, 4, 0)},
		{"weekly on the day, after the time", weeklySunday, at(2026, 11, 8, 5, 0), at(2026, 11, 15, 4, 0)},
		{"monthly, before the day", monthly, at(2026, 11, 4, 12, 0), at(2026, 11, 15, 4, 0)},
		{"monthly, after the day", monthly, at(2026, 11, 15, 5, 0), at(2026, 12, 15, 4, 0)},
		{"monthly across the year end", monthly, at(2026, 12, 20, 0, 0), at(2027, 1, 15, 4, 0)},
		{"off never runs", Schedule{Frequency: FrequencyOff, Time: "04:00"}, at(2026, 11, 4, 12, 0), time.Time{}},
	}
	for _, c := range cases {
		if got := NextRun(c.schedule, c.after, utc); !got.Equal(c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestNextRunUsesTheServersLocalTime(t *testing.T) {
	// 03:00 UTC on Nov 4 is 21:00 on Nov 3 in UTC-6, so "04:00 daily" there is
	// still ahead on Nov 4, not Nov 5.
	chicago := time.FixedZone("UTC-6", -6*3600)
	got := NextRun(Schedule{Frequency: FrequencyDaily, Time: "04:00"}, at(2026, 11, 4, 3, 0), chicago)
	if want := time.Date(2026, 11, 4, 4, 0, 0, 0, chicago); !got.Equal(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestValidate(t *testing.T) {
	good := DefaultSchedule()
	if err := good.Validate(); err != nil {
		t.Fatalf("the default schedule must be valid: %v", err)
	}
	bad := map[string]func(*Schedule){
		"frequency": func(s *Schedule) { s.Frequency = "hourly" },
		"time":      func(s *Schedule) { s.Time = "25:00" },
		"time text": func(s *Schedule) { s.Time = "4am" },
		"weekday":   func(s *Schedule) { s.Weekday = 7 },
		"day":       func(s *Schedule) { s.DayOfMonth = 29 },
		"percent":   func(s *Schedule) { s.OnlyIfDiskOverPercent = 100 },
		"max low":   func(s *Schedule) { s.MaxImages = 0 },
		"max high":  func(s *Schedule) { s.MaxImages = maxScheduledMax + 1 },
		"registry":  func(s *Schedule) { s.AllowRegistries = []string{"not a registry!"} },
		"injection": func(s *Schedule) { s.AllowRegistries = []string{"docker.io; rm -rf /"} },
		"too many":  func(s *Schedule) { s.AllowRegistries = make([]string, 21) },
	}
	for name, mutate := range bad {
		schedule := DefaultSchedule()
		mutate(&schedule)
		if err := schedule.Validate(); err == nil {
			t.Errorf("%s: expected a validation error", name)
		}
	}
	ok := DefaultSchedule()
	ok.AllowRegistries = []string{"ghcr.io", "registry.example.com:5000"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("real registry names must be valid: %v", err)
	}
}

func newStore(t *testing.T) *ScheduleStore {
	t.Helper()
	return NewScheduleStore(filepath.Join(t.TempDir(), "state", "schedule.json"), utc)
}

func TestStoreDefaultsWhenNothingIsSaved(t *testing.T) {
	state, err := newStore(t).Load()
	if err != nil || state.Schedule.Frequency != FrequencyOff || state.NextRun != nil {
		t.Fatalf("an empty store must be an off schedule: %+v %v", state, err)
	}
}

func TestStoreSavesAtomicallyWithRestrictedPermissions(t *testing.T) {
	store := newStore(t)
	schedule := DefaultSchedule()
	schedule.Frequency = FrequencyDaily
	saved, err := store.SetSchedule(schedule, at(2026, 11, 4, 12, 0))
	if err != nil {
		t.Fatal(err)
	}
	if saved.NextRun == nil || !saved.NextRun.Equal(at(2026, 11, 5, 4, 0)) {
		t.Fatalf("saving must work out the next run: %+v", saved.NextRun)
	}
	loaded, err := store.Load()
	if err != nil || loaded.Schedule.Frequency != FrequencyDaily {
		t.Fatalf("round trip failed: %+v %v", loaded, err)
	}
	info, _ := os.Stat(store.path)
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(store.path))
	if len(entries) != 1 {
		t.Fatalf("no temporary files may be left behind: %v", entries)
	}
}

func TestStoreRejectsInvalidSchedulesAndNeverSavesThem(t *testing.T) {
	store := newStore(t)
	schedule := DefaultSchedule()
	schedule.Time = "nope"
	if _, err := store.SetSchedule(schedule, at(2026, 11, 4, 12, 0)); err == nil {
		t.Fatal("expected a validation error")
	}
	if _, err := os.Stat(store.path); err == nil {
		t.Fatal("an invalid schedule must not be written")
	}
}

func TestADamagedOrHandEditedFileIsNotTrustedButCanBeRepaired(t *testing.T) {
	store := newStore(t)
	os.MkdirAll(filepath.Dir(store.path), 0o750)

	os.WriteFile(store.path, []byte("{not json"), 0o640)
	if _, err := store.Load(); err == nil {
		t.Fatal("a damaged file must be an error, not a default")
	}
	// Valid JSON, but a schedule that would have been refused through the API.
	os.WriteFile(store.path, []byte(`{"schedule":{"frequency":"daily","time":"99:99","maxImages":10,"dayOfMonth":1}}`), 0o640)
	if _, err := store.Load(); err == nil {
		t.Fatal("a hand-edited invalid schedule must not be loaded")
	}
	// Saving a good schedule repairs it.
	good := DefaultSchedule()
	if _, err := store.SetSchedule(good, at(2026, 11, 4, 12, 0)); err != nil {
		t.Fatalf("repair failed: %v", err)
	}
	if _, err := store.Load(); err != nil {
		t.Fatalf("the repaired file must load: %v", err)
	}
}

// harness for scheduler decisions.
type schedulerHarness struct {
	store    *ScheduleStore
	planner  func(inspect.PlanOptions) (inspect.CleanupPlan, error)
	started  []Options
	startErr error
	disk     float64
	planned  []inspect.PlanOptions
}

func (h *schedulerHarness) scheduler() *Scheduler {
	return &Scheduler{
		Store:    h.store,
		Location: utc,
		Planner: func(_ context.Context, options inspect.PlanOptions) (inspect.CleanupPlan, error) {
			h.planned = append(h.planned, options)
			return h.planner(options)
		},
		Starter: starterFunc(func(_ inspect.CleanupPlan, options Options) (string, error) {
			h.started = append(h.started, options)
			return "job-1", h.startErr
		}),
		DiskUsedPercent: func() float64 { return h.disk },
	}
}

type starterFunc func(inspect.CleanupPlan, Options) (string, error)

func (f starterFunc) Start(plan inspect.CleanupPlan, options Options) (string, error) {
	return f(plan, options)
}

func newScheduler(t *testing.T, mutate func(*Schedule)) *schedulerHarness {
	t.Helper()
	h := &schedulerHarness{store: newStore(t), disk: 90}
	h.planner = func(inspect.PlanOptions) (inspect.CleanupPlan, error) {
		return inspect.CleanupPlan{Items: []inspect.PlanItem{{ID: id("a")}, {ID: id("b")}}}, nil
	}
	schedule := DefaultSchedule()
	schedule.Frequency = FrequencyDaily
	if mutate != nil {
		mutate(&schedule)
	}
	if _, err := h.store.SetSchedule(schedule, at(2026, 11, 4, 12, 0)); err != nil { // next: 11-05 04:00
		t.Fatal(err)
	}
	return h
}

func (h *schedulerHarness) state(t *testing.T) ScheduleState {
	t.Helper()
	state, err := h.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestNothingRunsBeforeItIsDue(t *testing.T) {
	h := newScheduler(t, nil)
	h.scheduler().Tick(context.Background(), at(2026, 11, 5, 3, 59))
	if len(h.started) != 0 || len(h.planned) != 0 {
		t.Fatal("a run must not start before its time")
	}
}

func TestADueRunStartsWithTheScheduledScopeAndCap(t *testing.T) {
	h := newScheduler(t, func(s *Schedule) { s.MaxImages = 7 })
	h.scheduler().Tick(context.Background(), at(2026, 11, 5, 4, 0))

	if len(h.started) != 1 {
		t.Fatalf("expected one run, got %d", len(h.started))
	}
	if got := h.started[0]; got.MaxImages != 7 || got.Trigger != "schedule" || got.DryRun {
		t.Fatalf("unexpected options: %+v", got)
	}
	state := h.state(t)
	if state.LastRun == nil || state.LastRun.Status != "started" || state.LastRun.JobID != "job-1" {
		t.Fatalf("the decision must be recorded: %+v", state.LastRun)
	}
	if state.NextRun == nil || !state.NextRun.Equal(at(2026, 11, 6, 4, 0)) {
		t.Fatalf("the next run must be scheduled: %v", state.NextRun)
	}
}

func TestTheScheduleLevelReachesThePlanner(t *testing.T) {
	h := newScheduler(t, func(s *Schedule) { s.Level = inspect.LevelCareful })
	h.scheduler().Tick(context.Background(), at(2026, 11, 5, 4, 0))
	if len(h.planned) != 1 || h.planned[0].Level != inspect.LevelCareful {
		t.Fatalf("the planner must use the schedule's level: %+v", h.planned)
	}
	if got := DefaultSchedule().Level; got != inspect.LevelAggressive {
		t.Fatalf("a new schedule defaults to aggressive, got %q", got)
	}
}

func TestTheScheduleScopeReachesThePlanner(t *testing.T) {
	h := newScheduler(t, func(s *Schedule) { s.IncludeOrphans = true; s.AllowRegistries = []string{"ghcr.io"} })
	h.scheduler().Tick(context.Background(), at(2026, 11, 5, 4, 0))
	if len(h.planned) != 1 || !h.planned[0].IncludeOrphans || len(h.planned[0].AllowedRegistries) != 1 {
		t.Fatalf("the planner must use the schedule's scope: %+v", h.planned)
	}
}

func TestCheckOnlySchedulesNeverRemove(t *testing.T) {
	h := newScheduler(t, func(s *Schedule) { s.CheckOnly = true })
	h.scheduler().Tick(context.Background(), at(2026, 11, 5, 4, 0))
	if len(h.started) != 1 || !h.started[0].DryRun {
		t.Fatalf("a check-only schedule must run as a dry run: %+v", h.started)
	}
}

func TestAMissedRunIsSkippedNotRunLate(t *testing.T) {
	h := newScheduler(t, nil)
	h.scheduler().Tick(context.Background(), at(2026, 11, 5, 7, 0)) // three hours late
	if len(h.started) != 0 {
		t.Fatal("a run that is hours late must not start by surprise")
	}
	state := h.state(t)
	if state.LastRun == nil || state.LastRun.Status != "skipped" || !strings.Contains(state.LastRun.Detail, "missed") {
		t.Fatalf("the skip must be explained: %+v", state.LastRun)
	}
	if !state.NextRun.Equal(at(2026, 11, 6, 4, 0)) {
		t.Fatalf("the next run must still be scheduled: %v", state.NextRun)
	}
}

func TestDiskConditionSkipsWhenTheDiskIsNotFullEnough(t *testing.T) {
	h := newScheduler(t, func(s *Schedule) { s.OnlyIfDiskOverPercent = 80 })
	h.disk = 60
	h.scheduler().Tick(context.Background(), at(2026, 11, 5, 4, 0))
	if len(h.started) != 0 || len(h.planned) != 0 {
		t.Fatal("below the threshold nothing may even be planned")
	}
	if state := h.state(t); state.LastRun.Status != "skipped" || !strings.Contains(state.LastRun.Detail, "60%") {
		t.Fatalf("unexpected: %+v", state.LastRun)
	}

	// The next day the disk is fuller, so it runs.
	h.disk = 85
	h.scheduler().Tick(context.Background(), at(2026, 11, 6, 4, 0))
	if len(h.started) != 1 {
		t.Fatal("at or above the threshold the run must start")
	}
}

func TestAnEmptyPlanStartsNothing(t *testing.T) {
	h := newScheduler(t, nil)
	h.planner = func(inspect.PlanOptions) (inspect.CleanupPlan, error) { return inspect.CleanupPlan{}, nil }
	h.scheduler().Tick(context.Background(), at(2026, 11, 5, 4, 0))
	if len(h.started) != 0 || h.state(t).LastRun.Status != "nothing-to-remove" {
		t.Fatalf("unexpected: %v %+v", h.started, h.state(t).LastRun)
	}
}

func TestAPlanningFailureIsRecordedAndNothingRuns(t *testing.T) {
	h := newScheduler(t, nil)
	h.planner = func(inspect.PlanOptions) (inspect.CleanupPlan, error) {
		return inspect.CleanupPlan{}, errors.New("cluster view unavailable")
	}
	h.scheduler().Tick(context.Background(), at(2026, 11, 5, 4, 0))
	state := h.state(t)
	if len(h.started) != 0 || state.LastRun.Status != "failed" || !strings.Contains(state.LastRun.Detail, "cluster view unavailable") {
		t.Fatalf("unexpected: %v %+v", h.started, state.LastRun)
	}
}

func TestARunAlreadyInProgressIsNotDoubledUp(t *testing.T) {
	h := newScheduler(t, nil)
	h.startErr = ErrBusy
	h.scheduler().Tick(context.Background(), at(2026, 11, 5, 4, 0))
	if state := h.state(t); state.LastRun.Status != "skipped" {
		t.Fatalf("a busy start must be a skip: %+v", state.LastRun)
	}
}

func TestAnOffScheduleDoesNothingEvenIfOverdue(t *testing.T) {
	h := newScheduler(t, func(s *Schedule) { s.Frequency = FrequencyOff })
	h.scheduler().Tick(context.Background(), at(2030, 1, 1, 4, 0))
	if len(h.started) != 0 || len(h.planned) != 0 {
		t.Fatal("an off schedule must never run")
	}
}

func TestAScheduleWithNoNextRunStartsCountingInsteadOfRunning(t *testing.T) {
	h := newScheduler(t, nil)
	h.store.update(func(s *ScheduleState) { s.NextRun = nil })
	h.scheduler().Tick(context.Background(), at(2026, 11, 5, 4, 0))
	if len(h.started) != 0 {
		t.Fatal("a schedule that lost its next run must not fire immediately")
	}
	if state := h.state(t); state.NextRun == nil || !state.NextRun.After(at(2026, 11, 5, 4, 0)) {
		t.Fatalf("it must work out the next run: %v", state.NextRun)
	}
}
