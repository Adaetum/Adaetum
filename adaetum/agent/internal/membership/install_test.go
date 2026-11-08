package membership

import (
	"strings"
	"testing"
	"time"
)

func TestOnlyMissingAndOutdatedServersGetAnAction(t *testing.T) {
	a := Assess([]string{"ok-node", "missing-node", "stale-node", "old-node"}, []CheckIn{
		checkIn("1", "ok-node", "0.2.0", time.Minute),
		checkIn("3", "stale-node", "0.2.0", time.Hour),
		checkIn("4", "old-node", "0.1.0", time.Minute),
	}, now, Options{LatestVersion: "0.2.0"})
	plan := PlanInstall(a)

	got := map[string]string{}
	for _, action := range plan.Actions {
		got[action.Server] = action.Kind
	}
	if len(got) != 2 || got["missing-node"] != InstallKind || got["old-node"] != UpgradeKind {
		t.Fatalf("expected install on missing-node and upgrade on old-node: %+v", plan.Actions)
	}
	if plan.Limit != "missing-node,old-node" {
		t.Fatalf("the limit must name exactly the planned servers, sorted: %q", plan.Limit)
	}
}

func TestAQuietServerIsLeftAloneWithAnExplanation(t *testing.T) {
	plan := PlanInstall(Assess([]string{"quiet"}, []CheckIn{checkIn("1", "quiet", "0.2.0", time.Hour)}, now, Options{}))
	if len(plan.Actions) != 0 || len(plan.Skipped) != 1 || !strings.Contains(plan.Skipped[0].Reason, "check the server first") {
		t.Fatalf("a stale server needs a person, not a reinstall: %+v", plan)
	}
}

func TestMachinesOutsideTheClusterAreNeverPlanned(t *testing.T) {
	a := Assess([]string{"real-node"}, []CheckIn{checkIn("9", "claims-a-name", "0.0.1", time.Minute)}, now, Options{LatestVersion: "9.9.9"})
	for _, action := range PlanInstall(a).Actions {
		if action.Server == "claims-a-name" {
			t.Fatal("software must never be pushed to a machine just because it checked in")
		}
	}
}

func TestAHealthyClusterHasAnEmptyPlanAndNoLimit(t *testing.T) {
	plan := PlanInstall(Assess([]string{"a"}, []CheckIn{checkIn("1", "a", "1.0.0", time.Minute)}, now, Options{LatestVersion: "1.0.0"}))
	if len(plan.Actions) != 0 || plan.Limit != "" || plan.Actions == nil {
		t.Fatalf("nothing to do must be an empty list (not null) and an empty limit: %+v", plan)
	}
}
