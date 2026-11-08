package automation

import (
	"strings"
	"testing"
	"time"
)

// failingRun is the shape of a real run from the production runner: every ten
// minutes the controller could not SSH to node-56843.
const failingRun = `[ansible-runner] 2026-11-03T09:40:06Z running playbooks/day2.yml

PLAY [localhost] ***************************************************************

TASK [Get local hostname for break-glass targeting] ****************************
skipping: [localhost]

TASK [Gathering Facts] *********************************************************
[ERROR]: Task failed: Failed to connect to the host via ssh: ansible@node-56843: Permission denied (publickey,gssapi-keyex,gssapi-with-mic).
fatal: [node-56843]: UNREACHABLE! => {"changed": false, "msg": "Task failed: Failed to connect to the host via ssh: ansible@node-56843: Permission denied (publickey,gssapi-keyex,gssapi-with-mic).", "unreachable": true}

PLAY RECAP *********************************************************************
localhost                  : ok=0    changed=0    unreachable=0    failed=0    skipped=4    rescued=0    ignored=0
node-56843                 : ok=0    changed=0    unreachable=1    failed=0    skipped=0    rescued=0    ignored=0

`

func successRun(at string, changed int) string {
	return `[ansible-runner] ` + at + ` running playbooks/day2.yml

PLAY RECAP *********************************************************************
node-50449                 : ok=12   changed=` + strings.Repeat("1", 1) + `    unreachable=0    failed=0    skipped=3    rescued=0    ignored=0
node-56843                 : ok=12   changed=` + string(rune('0'+changed-1)) + `    unreachable=0    failed=0    skipped=3    rescued=0    ignored=0

`
}

func TestParsesARealUnreachableRun(t *testing.T) {
	runs := ParseRuns(failingRun, 0)
	if len(runs) != 1 {
		t.Fatalf("expected one run, got %+v", runs)
	}
	run := runs[0]
	if run.Status != StatusUnreachable || run.Playbook != "playbooks/day2.yml" {
		t.Fatalf("unexpected run: %+v", run)
	}
	if !run.StartedAt.Equal(time.Date(2026, 11, 3, 9, 40, 6, 0, time.UTC)) {
		t.Fatalf("unexpected start: %v", run.StartedAt)
	}
	if len(run.Hosts) != 2 || run.Hosts[1].Host != "node-56843" || run.Hosts[1].Unreachable != 1 || run.Hosts[0].Skipped != 4 {
		t.Fatalf("the recap must be read per host: %+v", run.Hosts)
	}
	if !strings.Contains(run.Error, "Permission denied") || strings.Contains(run.Error, "[ERROR]") {
		t.Fatalf("the error must be the readable message without markup: %q", run.Error)
	}
}

func TestASuccessfulRunCountsChanges(t *testing.T) {
	runs := ParseRuns(successRun("2026-11-04T01:00:00Z", 3), 0)
	if len(runs) != 1 || runs[0].Status != StatusSuccess || runs[0].Changed != 3 || runs[0].Error != "" {
		t.Fatalf("unexpected: %+v", runs)
	}
}

func TestAFailedTaskIsFailedEvenIfAnotherHostWasUnreachable(t *testing.T) {
	log := `[ansible-runner] 2026-11-04T01:00:00Z running playbooks/day2.yml
fatal: [node-a]: FAILED! => {"changed": false, "msg": "No package dnf-automatic available."}

PLAY RECAP *********************************************************************
node-a                     : ok=3    changed=0    unreachable=0    failed=1    skipped=0    rescued=0    ignored=0
node-b                     : ok=0    changed=0    unreachable=1    failed=0    skipped=0    rescued=0    ignored=0

`
	runs := ParseRuns(log, 0)
	if runs[0].Status != StatusFailed || runs[0].Error != "No package dnf-automatic available." {
		t.Fatalf("a task failure must outrank an unreachable host: %+v", runs[0])
	}
}

func TestARunWithNoRecapIsIncomplete(t *testing.T) {
	runs := ParseRuns("[ansible-runner] 2026-11-04T01:00:00Z running playbooks/day2.yml\n\nPLAY [all] ***\n", 0)
	if len(runs) != 1 || runs[0].Status != StatusIncomplete || len(runs[0].Hosts) != 0 {
		t.Fatalf("a run that never printed a recap has not finished: %+v", runs)
	}
}

func TestRunsComeNewestFirstAndAreLimited(t *testing.T) {
	log := successRun("2026-11-04T01:00:00Z", 2) + successRun("2026-11-04T01:10:00Z", 2) + successRun("2026-11-04T01:20:00Z", 2)
	runs := ParseRuns(log, 2)
	if len(runs) != 2 || runs[0].StartedAt.Minute() != 20 || runs[1].StartedAt.Minute() != 10 {
		t.Fatalf("expected the two newest, newest first: %+v", runs)
	}
}

func TestATailThatStartsMidRunIsHandled(t *testing.T) {
	// `tail -c` cuts the log anywhere, so the text before the first marker is a
	// fragment of an earlier run and must not be attributed to anything.
	log := "k [node-a]\nPLAY RECAP ***\nnode-a : ok=1 changed=9 unreachable=0 failed=0 skipped=0\n\n" + successRun("2026-11-04T01:00:00Z", 2)
	runs := ParseRuns(log, 0)
	if len(runs) != 1 || runs[0].Changed != 1+1 {
		t.Fatalf("the leading fragment must be ignored: %+v", runs)
	}
}

func TestColorCodesAndBadTimestampsDoNotBreakParsing(t *testing.T) {
	log := "\x1b[0;31m[ansible-runner] not-a-time running playbooks/day2.yml\x1b[0m\n" +
		"\x1b[1;32m[ansible-runner] 2026-11-04T01:00:00Z running playbooks/day2.yml\x1b[0m\n" +
		"PLAY RECAP ***\nnode-a : ok=1 changed=0 unreachable=0 failed=0 skipped=0\n\n"
	runs := ParseRuns(log, 0)
	if len(runs) != 1 || runs[0].Status != StatusSuccess {
		t.Fatalf("expected the one valid run: %+v", runs)
	}
}

func TestEmptyLogGivesAnEmptyNonNilList(t *testing.T) {
	if runs := ParseRuns("", 0); runs == nil || len(runs) != 0 {
		t.Fatalf("got %v", runs)
	}
}

func TestLongErrorsAreShortened(t *testing.T) {
	log := "[ansible-runner] 2026-11-04T01:00:00Z running playbooks/day2.yml\nfatal: [h]: FAILED! => {\"msg\": \"" + strings.Repeat("x", 500) + "\"}\n\nPLAY RECAP ***\nh : ok=0 changed=0 unreachable=0 failed=1 skipped=0\n\n"
	if got := ParseRuns(log, 0)[0].Error; len([]rune(got)) > maxErrorLength+1 {
		t.Fatalf("an error must be kept short for display, got %d characters", len(got))
	}
}

func run(minute int, status string) Run {
	return Run{StartedAt: time.Date(2026, 11, 4, 1, minute, 0, 0, time.UTC), Status: status, Error: status + "-error"}
}

func TestSummaryFindsTheCurrentFailureStreak(t *testing.T) {
	// Newest first: three failures, then a success, then an older failure.
	runs := []Run{run(30, StatusUnreachable), run(20, StatusFailed), run(10, StatusUnreachable), run(5, StatusSuccess), run(0, StatusFailed)}
	health := Summarize(runs)
	if health.ConsecutiveFailures != 3 {
		t.Fatalf("the streak ends at the newest success: %+v", health)
	}
	if health.FailingSince == nil || health.FailingSince.Minute() != 10 {
		t.Fatalf("the streak began with the oldest run in it: %v", health.FailingSince)
	}
	if health.LastSuccess == nil || health.LastSuccess.Minute() != 5 || health.Succeeded != 1 || health.Failed != 4 || health.Total != 5 {
		t.Fatalf("unexpected counts: %+v", health)
	}
	if health.LastError != "unreachable-error" {
		t.Fatalf("the newest error is the one to show: %q", health.LastError)
	}
}

func TestASuccessfulNewestRunMeansNoStreak(t *testing.T) {
	health := Summarize([]Run{run(30, StatusSuccess), run(20, StatusFailed)})
	if health.ConsecutiveFailures != 0 || health.FailingSince != nil || health.LastError != "failed-error" {
		t.Fatalf("a recovered runner has no current streak: %+v", health)
	}
}

func TestAnUnfinishedNewestRunNeitherExtendsNorBreaksTheStreak(t *testing.T) {
	health := Summarize([]Run{run(30, StatusIncomplete), run(20, StatusFailed), run(10, StatusFailed)})
	if health.ConsecutiveFailures != 2 || health.Total != 2 {
		t.Fatalf("a run still in progress must not hide a failing runner: %+v", health)
	}
}

func TestNoRunsIsAnEmptySummary(t *testing.T) {
	if health := Summarize(nil); health.Total != 0 || health.FailingSince != nil || health.LastSuccess != nil {
		t.Fatalf("got %+v", health)
	}
}
