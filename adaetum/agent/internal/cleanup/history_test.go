package cleanup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeLog(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "audit.log")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	return path
}

func line(event, plan, extra string) string {
	return `{"time":"2026-11-04T0` + "2" + `:00:00Z","event":"` + event + `","planId":"` + plan + `","host":"h"` + extra + `}`
}

func TestMissingLogMeansNoHistory(t *testing.T) {
	runs, err := ReadHistory(filepath.Join(t.TempDir(), "nope.log"), 10)
	if err != nil || len(runs) != 0 || runs == nil {
		t.Fatalf("expected an empty, non-nil list: %v %v", runs, err)
	}
}

func TestHistoryCountsTheOutcomeOfEachRun(t *testing.T) {
	path := writeLog(t,
		line("start", "p1", `,"trigger":"ui"`),
		line("remove", "p1", `,"image":"a","compressedBytes":100`),
		line("removed", "p1", `,"image":"a","compressedBytes":100`),
		line("remove", "p1", `,"image":"b","compressedBytes":50`),
		line("fail", "p1", `,"image":"b","reason":"busy"`),
		line("skip", "p1", `,"image":"c","reason":"in use"`),
		line("end", "p1", `,"reason":"removed 1, skipped 1, failed 1"`),
	)
	runs, err := ReadHistory(path, 10)
	if err != nil || len(runs) != 1 {
		t.Fatalf("got %v %v", runs, err)
	}
	run := runs[0]
	if run.Removed != 1 || run.Skipped != 1 || run.Failed != 1 || run.RemovedCompressedBytes != 100 || run.Trigger != "ui" || !run.Complete {
		t.Fatalf("an intent entry must not count as a removal: %+v", run)
	}
}

func TestADryRunAndARealRunOfTheSamePlanAreSeparateRuns(t *testing.T) {
	path := writeLog(t,
		line("start", "same", `,"trigger":"cli","dryRun":true`),
		line("would-remove", "same", `,"image":"a","compressedBytes":10`),
		line("end", "same", `,"reason":"dry run: would remove 1"`),
		line("start", "same", `,"trigger":"cli"`),
		line("remove", "same", `,"image":"a","compressedBytes":10`),
		line("removed", "same", `,"image":"a","compressedBytes":10`),
		line("end", "same", `,"reason":"removed 1"`),
	)
	runs, _ := ReadHistory(path, 10)
	if len(runs) != 2 {
		t.Fatalf("a repeated plan ID must not merge two runs: %+v", runs)
	}
	if !runs[1].DryRun || runs[0].DryRun {
		t.Fatalf("newest first, with the dry-run flag kept: %+v", runs)
	}
}

func TestHistoryIsNewestFirstAndLimited(t *testing.T) {
	var lines []string
	for _, plan := range []string{"p1", "p2", "p3"} {
		lines = append(lines, line("start", plan, ``), line("end", plan, ``))
	}
	runs, _ := ReadHistory(writeLog(t, lines...), 2)
	if len(runs) != 2 || runs[0].PlanID != "p3" || runs[1].PlanID != "p2" {
		t.Fatalf("unexpected order or limit: %+v", runs)
	}
}

func TestAnUnfinishedRunIsShownAsIncomplete(t *testing.T) {
	runs, _ := ReadHistory(writeLog(t, line("start", "p1", ``), line("remove", "p1", `,"image":"a"`)), 10)
	if len(runs) != 1 || runs[0].Complete || runs[0].EndedAt != nil {
		t.Fatalf("a run with no end entry is unfinished: %+v", runs)
	}
}

func TestATornOrGarbageLineDoesNotBreakHistory(t *testing.T) {
	path := writeLog(t, `not json at all`, line("start", "p1", ``), line("end", "p1", ``), `{"event":"start","planId":"p2","time":"2026-11`)
	runs, err := ReadHistory(path, 10)
	if err != nil || len(runs) != 1 || runs[0].PlanID != "p1" {
		t.Fatalf("bad lines must be skipped: %+v %v", runs, err)
	}
}

func TestHistoryReadsTimes(t *testing.T) {
	runs, _ := ReadHistory(writeLog(t, line("start", "p1", ``), line("end", "p1", ``)), 10)
	if !runs[0].StartedAt.Equal(time.Date(2026, 11, 4, 2, 0, 0, 0, time.UTC)) || runs[0].EndedAt == nil {
		t.Fatalf("unexpected times: %+v", runs[0])
	}
}
