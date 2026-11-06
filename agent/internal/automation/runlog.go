// Package automation reads the state of the Ansible runner and the policies it
// enforces, so the agent can show whether automation is healthy and, later,
// decide when to trigger it.
//
// The runner is a container that runs a playbook on a cron loop and appends the
// output to a log file. That log is the only record of what each run did, so this
// package turns it into runs and a health summary.
//
// STATUS: read-only. It reports what the runner did and what platform.yaml asks
// for; it does not start anything.
//
// INTENT (not built): this becomes the engine that decides WHEN Ansible runs,
// replacing the runner's fixed ten-minute cron with triggers that react to state,
// while the cron stays as a safety net. The pieces, in order:
//
//  1. "Run now": create a Kubernetes Job from the runner's pod template for one
//     named playbook, token-gated and only from the host itself, with the manifest
//     checked by `kubectl --dry-run=server` first.
//  2. Condition triggers: a policy declares "run image-cleanup when a disk passes
//     X percent" or "when a node reports a new version", and the agent evaluates
//     those against the data it already collects.
//  3. Policy as configuration: platform.yaml stays the single public source for
//     what should be true (spec.imageCleanup is the first), Ansible roles make it
//     true, and this package shows whether the two agree (declared versus actual).
//
// The runner is currently unable to reach the node it runs on (see
// ansible/ansible-scripts/build-inventory); until that is deployed, the playbooks
// are meant to be run directly (ansible/playbooks/image-cleanup.yml).
package automation

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Run statuses.
const (
	StatusSuccess     = "success"
	StatusFailed      = "failed"      // a task failed on at least one host
	StatusUnreachable = "unreachable" // a host could not be reached
	StatusIncomplete  = "incomplete"  // no recap: still running, or killed
)

// HostResult is one host's line from an Ansible PLAY RECAP.
type HostResult struct {
	Host        string `json:"host"`
	OK          int    `json:"ok"`
	Changed     int    `json:"changed"`
	Unreachable int    `json:"unreachable"`
	Failed      int    `json:"failed"`
	Skipped     int    `json:"skipped"`
}

// Run is one execution of a playbook, reconstructed from the runner's log.
type Run struct {
	StartedAt time.Time    `json:"startedAt"`
	Playbook  string       `json:"playbook"`
	Status    string       `json:"status"`
	Hosts     []HostResult `json:"hosts"`
	// Changed is the number of changes made across all hosts.
	Changed int `json:"changed"`
	// Error is the first error the run reported, shortened; empty on success.
	Error string `json:"error,omitempty"`
}

var (
	// "[ansible-runner] 2026-11-03T09:40:06Z running playbooks/day2.yml"
	runMarker = regexp.MustCompile(`^\[ansible-runner\] (\S+) running (\S+)\s*$`)
	// "node-56843  : ok=0    changed=0    unreachable=1    failed=0    skipped=0 ..."
	recapLine = regexp.MustCompile(`^(\S+)\s*: ok=(\d+)\s+changed=(\d+)\s+unreachable=(\d+)\s+failed=(\d+)\s+skipped=(\d+)`)
	// The message inside Ansible's JSON-ish failure output.
	messageField = regexp.MustCompile(`"msg": "((?:[^"\\]|\\.)*)"`)
	ansiEscape   = regexp.MustCompile(`\x1b\[[0-9;]*m`)
)

const maxErrorLength = 220

// ParseRuns turns the runner's log into runs, newest first. A log tail that
// starts in the middle of a run is fine: text before the first run marker is
// ignored. limit bounds the result; zero means no limit.
func ParseRuns(log string, limit int) []Run {
	var runs []Run
	inRecap := false

	for _, raw := range strings.Split(log, "\n") {
		line := strings.TrimRight(ansiEscape.ReplaceAllString(raw, ""), "\r")

		if match := runMarker.FindStringSubmatch(line); match != nil {
			started, err := time.Parse(time.RFC3339, match[1])
			if err != nil {
				continue
			}
			runs = append(runs, Run{StartedAt: started, Playbook: match[2], Hosts: []HostResult{}})
			inRecap = false
			continue
		}
		if len(runs) == 0 {
			continue
		}
		run := &runs[len(runs)-1]

		switch {
		case strings.HasPrefix(line, "PLAY RECAP"):
			inRecap = true
		case inRecap && strings.TrimSpace(line) == "":
			inRecap = false
		case inRecap:
			if match := recapLine.FindStringSubmatch(line); match != nil {
				result := HostResult{Host: match[1]}
				result.OK, _ = strconv.Atoi(match[2])
				result.Changed, _ = strconv.Atoi(match[3])
				result.Unreachable, _ = strconv.Atoi(match[4])
				result.Failed, _ = strconv.Atoi(match[5])
				result.Skipped, _ = strconv.Atoi(match[6])
				run.Hosts = append(run.Hosts, result)
				run.Changed += result.Changed
			}
		case run.Error == "" && (strings.HasPrefix(line, "fatal:") || strings.HasPrefix(line, "[ERROR]")):
			run.Error = shortError(line)
		}
	}

	for index := range runs {
		runs[index].Status = statusOf(runs[index])
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
	return runs
}

// statusOf judges a run by its recap. A run with no recap has not finished.
func statusOf(run Run) string {
	if len(run.Hosts) == 0 {
		return StatusIncomplete
	}
	unreachable := false
	for _, host := range run.Hosts {
		if host.Failed > 0 {
			return StatusFailed
		}
		unreachable = unreachable || host.Unreachable > 0
	}
	if unreachable {
		return StatusUnreachable
	}
	return StatusSuccess
}

// shortError pulls the human message out of an Ansible error line, and keeps it
// short: the full line can be several hundred characters of JSON.
func shortError(line string) string {
	message := line
	if match := messageField.FindStringSubmatch(line); match != nil {
		message = match[1]
	} else {
		message = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(message, "[ERROR]:"), "fatal:"))
	}
	message = strings.ReplaceAll(message, `\"`, `"`)
	if len(message) > maxErrorLength {
		message = message[:maxErrorLength] + "…"
	}
	return message
}

// Health summarizes a list of runs (newest first) for the top of the tab.
type Health struct {
	Total     int `json:"total"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	// ConsecutiveFailures is how many of the newest runs in a row did not
	// succeed. Zero means the latest run succeeded.
	ConsecutiveFailures int `json:"consecutiveFailures"`
	// FailingSince is when the current failure streak began.
	FailingSince *time.Time `json:"failingSince,omitempty"`
	LastSuccess  *time.Time `json:"lastSuccess,omitempty"`
	LastError    string     `json:"lastError,omitempty"`
}

// Summarize computes Health. Incomplete runs are not counted as failures: the
// newest may simply still be running.
func Summarize(runs []Run) Health {
	health := Health{}
	streakOpen := true
	for index, run := range runs {
		switch run.Status {
		case StatusSuccess:
			health.Succeeded++
			if health.LastSuccess == nil {
				started := run.StartedAt
				health.LastSuccess = &started
			}
			streakOpen = false
		case StatusFailed, StatusUnreachable:
			health.Failed++
			if health.LastError == "" {
				health.LastError = run.Error
			}
			if streakOpen {
				health.ConsecutiveFailures++
				started := run.StartedAt
				health.FailingSince = &started // keeps moving back to the oldest in the streak
			}
		case StatusIncomplete:
			// An unfinished newest run neither extends nor breaks the streak.
			if index != 0 {
				streakOpen = false
			}
		}
	}
	health.Total = health.Succeeded + health.Failed
	return health
}
