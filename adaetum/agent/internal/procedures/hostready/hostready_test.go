package hostready

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/runs"
)

// fakeHost is a machine whose answers change as it is asked, like a real one coming up.
type fakeHost struct {
	mu sync.Mutex
	// routeAfter and clockAfter say after how many questions the thing becomes true;
	// -1 means never.
	routeAfter, clockAfter int
	routeAsked, clockAsked int
	clockUnknown           bool
	routeErr               error
	free                   uint64
	freeErr                error
	asked                  []string
}

func (h *fakeHost) HasDefaultRoute(context.Context) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.routeAsked++
	h.asked = append(h.asked, "route")
	if h.routeErr != nil {
		return false, h.routeErr
	}
	return h.routeAfter >= 0 && h.routeAsked > h.routeAfter, nil
}

func (h *fakeHost) ClockSynchronized(context.Context) (bool, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clockAsked++
	h.asked = append(h.asked, "clock")
	if h.clockUnknown {
		return false, false
	}
	return h.clockAfter >= 0 && h.clockAsked > h.clockAfter, true
}

func (h *fakeHost) FreeBytes(_ context.Context, path string) (uint64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.asked = append(h.asked, "disk:"+path)
	return h.free, h.freeErr
}

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *clock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
	return nil
}

func run(t *testing.T, host Host, inputs map[string]string) runs.Snapshot {
	t.Helper()
	m, err := runs.NewManager(runs.Options{
		Journal: &runs.MemoryJournal{},
		Clock:   &clock{now: time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)},
		Policy:  runs.Policy{PollEvery: 5 * time.Second, StallAfter: 10 * time.Minute, Ceiling: time.Hour},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.Register(New(host)); err != nil {
		t.Fatalf("the procedure is malformed: %v", err)
	}
	id, err := m.Start(context.Background(), runs.StartRequest{Procedure: Name, Inputs: inputs, StartedBy: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Wait(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	snap, _ := m.Get(id)
	return snap
}

func healthy() *fakeHost { return &fakeHost{routeAfter: 0, clockAfter: 0, free: 100 * gib} }

func TestAHealthyMachineIsReady(t *testing.T) {
	snap := run(t, healthy(), nil)
	if snap.State != runs.StateSucceeded {
		t.Fatalf("got %s: %s", snap.State, snap.Detail)
	}
	for _, step := range snap.Steps {
		if step.State != runs.StepDone || step.Code != runs.CodeAlready {
			t.Errorf("%s should already be in place, got %+v", step.Name, step)
		}
	}
}

func TestTheStepsRunInTheOrderTheMachineNeedsThem(t *testing.T) {
	host := healthy()
	snap := run(t, host, nil)
	var order []string
	for _, step := range snap.Steps {
		order = append(order, step.Name)
	}
	if strings.Join(order, ",") != "network,clock,disk" {
		t.Fatalf("steps are listed %v", order)
	}
	first := strings.Join(host.asked[:3], ",")
	if first != "route,clock,disk:/var" {
		t.Fatalf("the machine was asked in the order %s", first)
	}
}

func TestItWaitsForTheRouteAndTheClockThenSucceeds(t *testing.T) {
	host := &fakeHost{routeAfter: 3, clockAfter: 4, free: 100 * gib}
	snap := run(t, host, nil)
	if snap.State != runs.StateSucceeded {
		t.Fatalf("got %s: %s %s", snap.State, snap.Code, snap.Detail)
	}
	if host.routeAsked < 3 || host.clockAsked < 4 {
		t.Fatalf("it should have waited: route asked %d, clock asked %d", host.routeAsked, host.clockAsked)
	}
}

func TestANetworkThatNeverComesUpStallsWithTheReason(t *testing.T) {
	snap := run(t, &fakeHost{routeAfter: -1, free: 100 * gib}, nil)
	if snap.State != runs.StateFailed || snap.Failure != runs.FailStalled || snap.Code != runs.CodeNetwork || snap.FailedStep != "network" {
		t.Fatalf("got %s/%s/%s at %s", snap.State, snap.Failure, snap.Code, snap.FailedStep)
	}
	for _, step := range snap.Steps {
		if step.Name != "network" && step.State != runs.StepPending {
			t.Errorf("%s must not run after the network failed: %+v", step.Name, step)
		}
	}
}

func TestAClockThatNeverSynchronizesStallsAndSaysSo(t *testing.T) {
	snap := run(t, &fakeHost{routeAfter: 0, clockAfter: -1, free: 100 * gib}, nil)
	if snap.Failure != runs.FailStalled || snap.Code != runs.CodeStarting || snap.FailedStep != "clock" {
		t.Fatalf("got %s/%s at %s", snap.Failure, snap.Code, snap.FailedStep)
	}
}

func TestAClockItCannotReadIsReportedAsUnknownNotAsUnsynchronized(t *testing.T) {
	snap := run(t, &fakeHost{routeAfter: 0, clockUnknown: true, free: 100 * gib}, nil)
	if snap.Failure != runs.FailStalled || snap.Code != runs.CodeUnknown {
		t.Fatalf("got %s/%s: an unreadable clock must not be called merely unsynchronized", snap.Failure, snap.Code)
	}
}

func TestTooLittleDiskFailsAtOnceWithTheNumbers(t *testing.T) {
	snap := run(t, &fakeHost{free: 12 * gib}, nil)
	if snap.State != runs.StateFailed || snap.Failure != runs.FailBroken || snap.Code != runs.CodeDisk || snap.FailedStep != "disk" {
		t.Fatalf("got %s/%s/%s at %s", snap.State, snap.Failure, snap.Code, snap.FailedStep)
	}
	if !strings.Contains(snap.Detail, "12.0 GiB") || !strings.Contains(snap.Detail, "20 GiB") || !strings.Contains(snap.Detail, "/var") {
		t.Fatalf("the detail should say how much there is and how much is needed: %q", snap.Detail)
	}
}

func TestTheRequirementAndThePathCanBeChanged(t *testing.T) {
	host := &fakeHost{free: 12 * gib}
	snap := run(t, host, map[string]string{InputMinFreeGiB: "10", InputPath: "/data"})
	if snap.State != runs.StateSucceeded {
		t.Fatalf("12 GiB should satisfy a 10 GiB requirement: %s %s", snap.State, snap.Detail)
	}
	if !contains(host.asked, "disk:/data") {
		t.Fatalf("the chosen path was not measured: %v", host.asked)
	}
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

func TestBadInputsAreAConfigurationFailureNotAWait(t *testing.T) {
	for _, bad := range []string{"lots", "0", "-5", "1.5"} {
		snap := run(t, healthy(), map[string]string{InputMinFreeGiB: bad})
		if snap.State != runs.StateFailed || snap.Code != runs.CodeConfig {
			t.Errorf("min_free_gib=%q: got %s/%s", bad, snap.State, snap.Code)
		}
	}
}

func TestAnUnreadableMachineIsReportedNotWaitedOn(t *testing.T) {
	snap := run(t, &fakeHost{routeErr: errors.New("permission denied")}, nil)
	if snap.State != runs.StateFailed || snap.Code != runs.CodeConfig || !strings.Contains(snap.Detail, "permission denied") {
		t.Fatalf("got %s/%s: %s", snap.State, snap.Code, snap.Detail)
	}
	snap = run(t, &fakeHost{routeAfter: 0, clockAfter: 0, freeErr: errors.New("df failed")}, nil)
	if snap.Code != runs.CodeConfig || snap.FailedStep != "disk" {
		t.Fatalf("got %s at %s", snap.Code, snap.FailedStep)
	}
}

func TestRunningItAgainOnAMachineThatCameUpPasses(t *testing.T) {
	host := &fakeHost{routeAfter: -1, clockAfter: 0, free: 100 * gib}
	m, _ := runs.NewManager(runs.Options{
		Journal: &runs.MemoryJournal{},
		Clock:   &clock{now: time.Now()},
		Policy:  runs.Policy{PollEvery: 5 * time.Second, StallAfter: 10 * time.Minute, Ceiling: time.Hour},
	})
	defer m.Close()
	if err := m.Register(New(host)); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	id, _ := m.Start(ctx, runs.StartRequest{Procedure: Name})
	_ = m.Wait(ctx, id)
	if snap, _ := m.Get(id); snap.State != runs.StateFailed {
		t.Fatalf("first run: %s", snap.State)
	}
	host.mu.Lock()
	host.routeAfter = 0 // the cable is plugged in
	host.mu.Unlock()
	if err := m.Retry(ctx, id, "ops"); err != nil {
		t.Fatal(err)
	}
	_ = m.Wait(ctx, id)
	if snap, _ := m.Get(id); snap.State != runs.StateSucceeded || snap.Attempt != 2 {
		t.Fatalf("after retry: %s attempt %d", snap.State, snap.Attempt)
	}
}
