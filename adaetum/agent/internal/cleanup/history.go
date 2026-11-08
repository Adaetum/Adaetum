package cleanup

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"time"
)

// Run is one cleanup run, reconstructed from the audit log. The audit log is the
// single record of what happened; history is a view of it, not a second store
// that could disagree with it.
type Run struct {
	PlanID    string     `json:"planId"`
	Trigger   string     `json:"trigger"`
	DryRun    bool       `json:"dryRun"`
	StartedAt time.Time  `json:"startedAt"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`
	// Removed counts images actually removed; in a dry run, images that would be.
	Removed                int    `json:"removed"`
	Skipped                int    `json:"skipped"`
	Failed                 int    `json:"failed"`
	RemovedCompressedBytes uint64 `json:"removedCompressedBytes"`
	Summary                string `json:"summary,omitempty"`
	// Complete is false for a run with no closing entry: it is still running, or
	// the agent stopped in the middle of it.
	Complete bool `json:"complete"`
}

// ReadHistory returns the most recent runs, newest first. A missing log means no
// runs yet. Lines that are not valid entries are ignored rather than failing the
// whole history, because a torn last line is possible after a crash.
func ReadHistory(path string, limit int) ([]Run, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return []Run{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var runs []Run
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var entry Entry
		if json.Unmarshal(scanner.Bytes(), &entry) != nil {
			continue
		}
		// A start entry begins a new run. Runs are sequential (a lock enforces
		// it), so everything until the next start belongs to the current one;
		// keying on the plan ID would merge a dry run with the real run of the
		// same plan.
		if entry.Event == "start" {
			runs = append(runs, Run{PlanID: entry.PlanID, Trigger: entry.Trigger, DryRun: entry.DryRun, StartedAt: entry.Time})
			continue
		}
		if len(runs) == 0 {
			continue
		}
		run := &runs[len(runs)-1]
		switch entry.Event {
		case "removed", "would-remove":
			run.Removed++
			run.RemovedCompressedBytes += entry.Bytes
		case "skip":
			run.Skipped++
		case "fail":
			run.Failed++
		case "end":
			ended := entry.Time
			run.EndedAt, run.Summary, run.Complete = &ended, entry.Reason, true
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Newest first, trimmed.
	for left, right := 0, len(runs)-1; left < right; left, right = left+1, right-1 {
		runs[left], runs[right] = runs[right], runs[left]
	}
	if limit > 0 && len(runs) > limit {
		runs = runs[:limit]
	}
	if runs == nil {
		runs = []Run{}
	}
	return runs, nil
}
