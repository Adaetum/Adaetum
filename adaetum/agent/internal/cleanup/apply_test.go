package cleanup

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/inspect"
)

var now = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

func id(char string) string { return "sha256:" + strings.Repeat(char, 64) }

func item(char string) inspect.PlanItem {
	return inspect.PlanItem{ID: id(char), Names: []string{"docker.io/acme/web:" + char}, Repository: "docker.io/acme/web", Registry: "docker.io",
		Category: inspect.CategorySuperseded, CompressedBytes: 100}
}

func planOf(items ...inspect.PlanItem) inspect.CleanupPlan {
	return inspect.CleanupPlan{ID: "plan-1", Host: "node-a", CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute), Items: items}
}

// harness records every call so tests can assert what was and was not touched.
type harness struct {
	removed []string
	audit   []Entry
	// Overrides, set per test.
	eligible  map[string]bool    // ids the fresh plan still allows; nil = all of the plan's
	whyNot    map[string]string  // exclusion reasons in the fresh plan
	extra     []inspect.PlanItem // new items that appeared after the plan was made
	replanErr error
	inUse     func(id string) (bool, error)
	removeErr func(id string) error
	auditErr  func(count int) error
	clock     time.Time
	hostname  string
	replanned bool
	checked   []string
}

func (h *harness) deps(plan inspect.CleanupPlan) Deps {
	if h.clock.IsZero() {
		h.clock = now
	}
	if h.hostname == "" {
		h.hostname = "node-a"
	}
	return Deps{
		Hostname: func() (string, error) { return h.hostname, nil },
		Now:      func() time.Time { return h.clock },
		Replan: func(context.Context, inspect.PlanOptions) (inspect.CleanupPlan, error) {
			h.replanned = true
			if h.replanErr != nil {
				return inspect.CleanupPlan{}, h.replanErr
			}
			fresh := inspect.CleanupPlan{}
			for _, it := range plan.Items {
				if h.eligible == nil || h.eligible[it.ID] {
					fresh.Items = append(fresh.Items, it)
				} else {
					fresh.Excluded = append(fresh.Excluded, inspect.PlanExclusion{ID: it.ID, Reason: h.whyNot[it.ID]})
				}
			}
			fresh.Items = append(fresh.Items, h.extra...)
			return fresh, nil
		},
		InUse: func(_ context.Context, imageID string) (bool, error) {
			h.checked = append(h.checked, imageID)
			if h.inUse != nil {
				return h.inUse(imageID)
			}
			return false, nil
		},
		Remove: func(_ context.Context, imageID string) error {
			if h.removeErr != nil {
				if err := h.removeErr(imageID); err != nil {
					return err
				}
			}
			h.removed = append(h.removed, imageID)
			return nil
		},
		Audit: func(entry Entry) error {
			if h.auditErr != nil {
				if err := h.auditErr(len(h.audit)); err != nil {
					return err
				}
			}
			h.audit = append(h.audit, entry)
			return nil
		},
	}
}

func events(h *harness) string {
	var out []string
	for _, entry := range h.audit {
		out = append(out, entry.Event)
	}
	return strings.Join(out, ",")
}

func TestRemovesEligibleImagesAndAuditsEachStep(t *testing.T) {
	h := &harness{}
	plan := planOf(item("a"), item("b"))
	outcome, err := Apply(context.Background(), plan, Options{}, h.deps(plan))
	if err != nil {
		t.Fatal(err)
	}
	if len(h.removed) != 2 || len(outcome.Removed) != 2 || outcome.RemovedCompressedBytes() != 200 {
		t.Fatalf("unexpected result: removed=%v outcome=%+v", h.removed, outcome)
	}
	if got := events(h); got != "start,remove,removed,remove,removed,end" {
		t.Fatalf("the intent must be recorded before each removal: %s", got)
	}
	if h.audit[0].PlanID != "plan-1" || h.audit[1].Image != id("a") {
		t.Fatalf("audit entries must identify the plan and image: %+v", h.audit[:2])
	}
}

func TestSkipsImagesThatAreNoLongerEligibleAndSaysWhy(t *testing.T) {
	h := &harness{eligible: map[string]bool{id("a"): true}, whyNot: map[string]string{id("b"): "referenced by the cluster"}}
	plan := planOf(item("a"), item("b"))
	outcome, err := Apply(context.Background(), plan, Options{}, h.deps(plan))
	if err != nil {
		t.Fatal(err)
	}
	if len(h.removed) != 1 || h.removed[0] != id("a") {
		t.Fatalf("only the still-eligible image may be removed: %v", h.removed)
	}
	if len(outcome.Skipped) != 1 || !strings.Contains(outcome.Skipped[0].Reason, "referenced by the cluster") {
		t.Fatalf("the skip must carry the live reason: %+v", outcome.Skipped)
	}
}

func TestNeverTouchesImagesThatAreNotInThePlan(t *testing.T) {
	// A new image became eligible after the plan was made. The operator never
	// saw it, so it must not be removed.
	h := &harness{extra: []inspect.PlanItem{item("z")}}
	plan := planOf(item("a"))
	if _, err := Apply(context.Background(), plan, Options{}, h.deps(plan)); err != nil {
		t.Fatal(err)
	}
	for _, removed := range h.removed {
		if removed == id("z") {
			t.Fatal("an image that was not in the plan was removed")
		}
	}
}

func TestRechecksEveryImageImmediatelyBeforeRemoving(t *testing.T) {
	// A container starts on image "b" after the first image has been removed.
	h := &harness{inUse: func(imageID string) (bool, error) { return imageID == id("b"), nil }}
	plan := planOf(item("a"), item("b"), item("c"))
	outcome, err := Apply(context.Background(), plan, Options{}, h.deps(plan))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(h.removed, ",") != id("a")+","+id("c") {
		t.Fatalf("an image that became used must be skipped: %v", h.removed)
	}
	if len(outcome.Skipped) != 1 || outcome.Skipped[0].Reason != "a container started using it" {
		t.Fatalf("unexpected skips: %+v", outcome.Skipped)
	}
	if len(h.checked) != 3 {
		t.Fatalf("every image needs its own check, got %v", h.checked)
	}
}

func TestAnImageThatCannotBeCheckedIsNotRemoved(t *testing.T) {
	h := &harness{inUse: func(string) (bool, error) { return false, errors.New("runtime unavailable") }}
	plan := planOf(item("a"))
	outcome, err := Apply(context.Background(), plan, Options{}, h.deps(plan))
	if err != nil {
		t.Fatal(err)
	}
	if len(h.removed) != 0 || len(outcome.Failed) != 1 {
		t.Fatalf("an unverifiable image must be kept: removed=%v failed=%+v", h.removed, outcome.Failed)
	}
}

func TestDryRunChecksEverythingButRemovesNothing(t *testing.T) {
	h := &harness{}
	plan := planOf(item("a"), item("b"))
	outcome, err := Apply(context.Background(), plan, Options{DryRun: true}, h.deps(plan))
	if err != nil {
		t.Fatal(err)
	}
	if len(h.removed) != 0 || len(outcome.Removed) != 2 || !outcome.DryRun {
		t.Fatalf("a dry run must not remove: removed=%v outcome=%+v", h.removed, outcome)
	}
	if len(h.checked) != 2 || !h.replanned {
		t.Fatal("a dry run must perform the same live checks as a real run")
	}
	if got := events(h); got != "start,would-remove,would-remove,end" {
		t.Fatalf("unexpected audit: %s", got)
	}
	// A dry run must never leave a record that says something was removed.
	if end := h.audit[len(h.audit)-1].Reason; strings.HasPrefix(end, "removed") || !strings.Contains(end, "would remove") {
		t.Fatalf("a dry run's summary must not claim a removal: %q", end)
	}
}

func TestRefusesAnExpiredPlanBeforeDoingAnything(t *testing.T) {
	h := &harness{clock: now.Add(11 * time.Minute)}
	plan := planOf(item("a"))
	if _, err := Apply(context.Background(), plan, Options{}, h.deps(plan)); !errors.Is(err, ErrExpired) {
		t.Fatalf("expected ErrExpired, got %v", err)
	}
	if len(h.audit) != 0 || h.replanned || len(h.removed) != 0 {
		t.Fatal("nothing may happen for an expired plan")
	}
}

func TestRefusesAPlanFromAnotherServer(t *testing.T) {
	h := &harness{hostname: "node-b"}
	plan := planOf(item("a"))
	if _, err := Apply(context.Background(), plan, Options{}, h.deps(plan)); !errors.Is(err, ErrWrongHost) {
		t.Fatalf("expected ErrWrongHost, got %v", err)
	}
	if len(h.removed) != 0 || len(h.audit) != 0 {
		t.Fatal("nothing may happen for another server's plan")
	}
}

func TestRefusesTheWholePlanIfAnyIDIsMalformed(t *testing.T) {
	h := &harness{}
	bad := item("a")
	bad.ID = "--all"
	plan := planOf(item("b"), bad)
	if _, err := Apply(context.Background(), plan, Options{}, h.deps(plan)); err == nil {
		t.Fatal("a damaged plan must be refused, not partly applied")
	}
	if len(h.removed) != 0 || len(h.audit) != 0 || h.replanned {
		t.Fatal("not even the valid items may be removed from a damaged plan")
	}
}

func TestRemovesNothingWhenLiveStateCannotBeRead(t *testing.T) {
	h := &harness{replanErr: errors.New("api server unreachable")}
	plan := planOf(item("a"))
	if _, err := Apply(context.Background(), plan, Options{}, h.deps(plan)); err == nil {
		t.Fatal("expected an error")
	}
	if len(h.removed) != 0 {
		t.Fatal("without a fresh view nothing may be removed")
	}
}

func TestNoAuditTrailMeansNoDeletion(t *testing.T) {
	h := &harness{auditErr: func(int) error { return errors.New("disk full") }}
	plan := planOf(item("a"))
	if _, err := Apply(context.Background(), plan, Options{}, h.deps(plan)); err == nil {
		t.Fatal("expected an error")
	}
	if len(h.removed) != 0 || h.replanned {
		t.Fatal("nothing may be removed when the audit trail cannot be written")
	}
}

func TestStopsIfTheAuditTrailFailsMidRun(t *testing.T) {
	// start and the first image's "remove" intent are recorded, then the first
	// image is removed; recording that it was removed fails, so the run must stop
	// before touching the second image.
	h := &harness{auditErr: func(count int) error {
		if count >= 2 {
			return errors.New("disk full")
		}
		return nil
	}}
	plan := planOf(item("a"), item("b"), item("c"))
	outcome, err := Apply(context.Background(), plan, Options{}, h.deps(plan))
	if err != nil {
		t.Fatal(err)
	}
	if len(h.removed) != 1 || !strings.Contains(outcome.Stopped, "audit trail failed") {
		t.Fatalf("removal must stop when it can no longer be recorded: removed=%v stopped=%q", h.removed, outcome.Stopped)
	}
}

func TestStopsAfterRepeatedFailures(t *testing.T) {
	h := &harness{removeErr: func(string) error { return errors.New("busy") }}
	plan := planOf(item("a"), item("b"), item("c"), item("d"), item("e"))
	outcome, err := Apply(context.Background(), plan, Options{MaxFailures: 2}, h.deps(plan))
	if err != nil {
		t.Fatal(err)
	}
	if len(outcome.Failed) != 2 || !strings.Contains(outcome.Stopped, "2 failures") {
		t.Fatalf("expected to stop after two failures: %+v", outcome)
	}
	if len(h.checked) != 2 {
		t.Fatalf("no image past the failure limit may even be examined, checked %v", h.checked)
	}
}

func TestMaxImagesCapsASingleRun(t *testing.T) {
	h := &harness{}
	plan := planOf(item("a"), item("b"), item("c"))
	outcome, err := Apply(context.Background(), plan, Options{MaxImages: 2}, h.deps(plan))
	if err != nil {
		t.Fatal(err)
	}
	if len(h.removed) != 2 || !strings.Contains(outcome.Stopped, "limit of 2") {
		t.Fatalf("unexpected cap behavior: removed=%v stopped=%q", h.removed, outcome.Stopped)
	}
}

func TestCancellationStopsBetweenImages(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	h := &harness{}
	plan := planOf(item("a"), item("b"), item("c"))
	deps := h.deps(plan)
	remove := deps.Remove
	deps.Remove = func(c context.Context, imageID string) error {
		cancel() // the operator presses Ctrl-C after the first removal
		return remove(c, imageID)
	}
	outcome, err := Apply(ctx, plan, Options{}, deps)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.removed) != 1 || outcome.Stopped != "cancelled" {
		t.Fatalf("expected to stop after the first image: removed=%v stopped=%q", h.removed, outcome.Stopped)
	}
}

func TestRuntimeRemoveRefusesAnythingThatIsNotAnImageID(t *testing.T) {
	for _, bad := range []string{"", "--force", "-a", "sha256:short", id("a") + " extra", "../" + id("a")} {
		err := RuntimeRemove(context.Background(), bad)
		if err == nil || !strings.Contains(err.Error(), "not an image ID") {
			t.Fatalf("RuntimeRemove(%q) must refuse before running anything, got %v", bad, err)
		}
	}
}

func TestAuditLogIsAppendOnlyJSONLinesWithRestrictedPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "audit.log")
	for round := 0; round < 2; round++ {
		write, closeLog, err := OpenAuditLog(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := write(Entry{Time: now, Event: "start", PlanID: "p", Host: "h"}); err != nil {
			t.Fatal(err)
		}
		if err := closeLog(); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("reopening must append, not overwrite: %q", data)
	}
	var entry Entry
	if err := json.Unmarshal([]byte(lines[1]), &entry); err != nil || entry.Event != "start" {
		t.Fatalf("each line must be valid JSON: %v %+v", err, entry)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o640 {
		t.Fatalf("the audit log must not be world-readable, mode %v", info.Mode().Perm())
	}
}

func TestOnlyOneCleanupRunsAtATime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	unlock, err := Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(path); err == nil {
		t.Fatal("a second cleanup must be refused while the first runs")
	}
	unlock()
	again, err := Lock(path)
	if err != nil {
		t.Fatalf("the lock must be reusable after release: %v", err)
	}
	again()
}

func TestRuntimeConstantsMatchTheInspector(t *testing.T) {
	// RuntimeRemove spells the command and socket as literals; this keeps them
	// from drifting away from what the read-only inspector uses.
	source, err := os.ReadFile("local.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, literal := range []string{inspect.RKE2CrictlPath, inspect.ContainerdSocket} {
		if !strings.Contains(string(source), `"`+literal+`"`) {
			t.Fatalf("local.go must use the literal %q", literal)
		}
	}
}

func fakeCrictl(t *testing.T, behavior func(args []string) ([]byte, error)) (calls *[][]string) {
	t.Helper()
	original := crictl
	var recorded [][]string
	crictl = func(_ context.Context, args ...string) ([]byte, error) {
		recorded = append(recorded, args)
		return behavior(args)
	}
	t.Cleanup(func() { crictl = original })
	return &recorded
}

func TestRuntimeRemoveSucceedsWhenTheRuntimeDoes(t *testing.T) {
	calls := fakeCrictl(t, func([]string) ([]byte, error) { return nil, nil })
	if err := RuntimeRemove(context.Background(), id("a")); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || (*calls)[0][0] != "rmi" || (*calls)[0][1] != id("a") {
		t.Fatalf("expected a single rmi of the image: %v", *calls)
	}
}

func TestATimedOutRemovalThatFinishedCountsAsRemoved(t *testing.T) {
	// The real servers did exactly this: the call returned DeadlineExceeded, yet
	// the runtime went on to delete the image. The image being gone is the goal.
	calls := fakeCrictl(t, func(args []string) ([]byte, error) {
		if args[0] == "rmi" {
			return []byte("rpc error: code = DeadlineExceeded"), errors.New("exit status 1")
		}
		return []byte(`image "` + args[1] + `" not found`), errors.New("exit status 1")
	})
	if err := RuntimeRemove(context.Background(), id("a")); err != nil {
		t.Fatalf("an image that is gone must count as removed, got %v", err)
	}
	if len(*calls) != 2 || (*calls)[1][0] != "inspecti" {
		t.Fatalf("the image must be looked up after a failed removal: %v", *calls)
	}
}

func TestAFailedRemovalOfAnImageThatIsStillThereIsAnError(t *testing.T) {
	fakeCrictl(t, func(args []string) ([]byte, error) {
		if args[0] == "rmi" {
			return []byte("image is in use"), errors.New("exit status 1")
		}
		return []byte(`{"status":{"id":"x"}}`), nil // inspecti succeeds: it is still there
	})
	err := RuntimeRemove(context.Background(), id("a"))
	if err == nil || !strings.Contains(err.Error(), "image is in use") {
		t.Fatalf("a real failure must be reported with its cause, got %v", err)
	}
}

func TestAnUnreadableRuntimeIsNotMistakenForARemoval(t *testing.T) {
	// If the lookup fails for any reason other than "not found", the image is not
	// known to be gone, so the removal must not be reported as done.
	fakeCrictl(t, func(args []string) ([]byte, error) {
		return []byte("connection refused"), errors.New("exit status 1")
	})
	if err := RuntimeRemove(context.Background(), id("a")); err == nil {
		t.Fatal("an unverifiable removal must be an error")
	}
}

func TestTheRuntimeClientIsAlwaysGivenALongTimeout(t *testing.T) {
	// crictl defaults to 2 seconds, which deleting a large image exceeds. Read the
	// real wrapper's arguments through a stand-in executable-free check.
	source, err := os.ReadFile("local.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), `"--timeout", "4m"`) {
		t.Fatal("the crictl wrapper must pass a timeout long enough to delete a large image")
	}
}
