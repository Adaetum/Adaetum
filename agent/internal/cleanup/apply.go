// Package cleanup removes container images that a plan from the inspect package
// found to be unneeded. It is the only code in the agent that deletes anything,
// and it is deliberately separate from inspection: the read-only inspector, and
// the SSH mode used for development against production hosts, cannot reach it.
//
// A plan is a preview, never an authorization. Apply trusts nothing in the plan
// file except which images the operator was shown. It re-derives eligibility
// from live state, re-checks every image immediately before removing it, writes
// an audit trail (and removes nothing if it cannot), and stops after a few
// failures.
package cleanup

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/inspect"
)

var (
	ErrExpired   = errors.New("the plan has expired; make a fresh one")
	ErrWrongHost = errors.New("the plan was made for a different server")
)

// Deps are the live-state seams. Production code wires real implementations;
// tests wire fakes, so every safety rule is exercised without a container runtime.
type Deps struct {
	Hostname func() (string, error)
	Now      func() time.Time
	// Replan builds a new plan from live state with the same options. It is how
	// Apply learns what is eligible now, instead of trusting the plan file.
	Replan func(ctx context.Context, options inspect.PlanOptions) (inspect.CleanupPlan, error)
	// InUse reports whether any container on this server holds the image right now.
	InUse func(ctx context.Context, id string) (bool, error)
	// Remove deletes one image by ID.
	Remove func(ctx context.Context, id string) error
	// Audit records an entry durably. If it fails, removal stops.
	Audit func(Entry) error
}

// Options bound a single apply.
type Options struct {
	// DryRun goes through every check and records what would be removed, but
	// never calls Remove.
	DryRun bool
	// MaxImages caps how many images one apply removes; zero means no cap.
	MaxImages int
	// MaxFailures stops the run after this many failed removals or checks;
	// zero means the default of three.
	MaxFailures int
	// Trigger is recorded on every audit entry: cli, ui, or schedule.
	Trigger string
}

const defaultMaxFailures = 3

// Entry is one line of the audit trail.
type Entry struct {
	Time time.Time `json:"time"`
	// Event is start, remove (intent, written before the deletion), removed
	// (written after it succeeded), would-remove (dry run), skip, fail, or end.
	Event  string `json:"event"`
	PlanID string `json:"planId"`
	Host   string `json:"host"`
	// Trigger says what started the run: cli, ui, or schedule.
	Trigger string `json:"trigger,omitempty"`
	// DryRun is set on the start entry.
	DryRun bool     `json:"dryRun,omitempty"`
	Image  string   `json:"image,omitempty"`
	Names  []string `json:"names,omitempty"`
	Reason string   `json:"reason,omitempty"`
	Bytes  uint64   `json:"compressedBytes,omitempty"`
}

type Skip struct {
	Item   inspect.PlanItem
	Reason string
}

type Failure struct {
	Item   inspect.PlanItem
	Reason string
}

// Outcome reports what an apply did.
type Outcome struct {
	DryRun  bool
	Removed []inspect.PlanItem // in a dry run, what would have been removed
	Skipped []Skip
	Failed  []Failure
	// Stopped says why the run ended early; empty when every item was handled.
	Stopped string
}

// RemovedCompressedBytes is the runtime-reported size of the removed images.
// The disk actually freed is larger, and is released as the runtime collects the
// layers afterwards.
func (outcome Outcome) RemovedCompressedBytes() uint64 {
	var total uint64
	for _, item := range outcome.Removed {
		total += item.CompressedBytes
	}
	return total
}

// Apply removes the images in plan that are still safe to remove.
func Apply(ctx context.Context, plan inspect.CleanupPlan, options Options, deps Deps) (Outcome, error) {
	outcome := Outcome{DryRun: options.DryRun}
	maxFailures := options.MaxFailures
	if maxFailures <= 0 {
		maxFailures = defaultMaxFailures
	}

	// Everything that can be wrong with the plan itself is checked before the
	// first side effect, including the audit trail.
	if deps.Now().After(plan.ExpiresAt) {
		return outcome, ErrExpired
	}
	host, err := deps.Hostname()
	if err != nil {
		return outcome, fmt.Errorf("hostname: %w", err)
	}
	if host != plan.Host {
		return outcome, fmt.Errorf("%w: plan is for %q, this is %q", ErrWrongHost, plan.Host, host)
	}
	for _, item := range plan.Items {
		// An ID that is not an image ID is a damaged or tampered plan; refuse the
		// whole thing rather than skip it, so a bad plan is noticed.
		if !inspect.ValidImageID(item.ID) {
			return outcome, fmt.Errorf("the plan contains %q, which is not an image ID", item.ID)
		}
	}

	record := func(event string, item *inspect.PlanItem, reason string) error {
		entry := Entry{Time: deps.Now().UTC(), Event: event, PlanID: plan.ID, Host: host, Trigger: options.Trigger, Reason: reason}
		if event == "start" {
			entry.DryRun = options.DryRun
		}
		if item != nil {
			entry.Image, entry.Names, entry.Bytes = item.ID, item.Names, item.CompressedBytes
		}
		return deps.Audit(entry)
	}

	// No audit trail, no deletion.
	if err := record("start", nil, fmt.Sprintf("%d images in plan, dry run: %v", len(plan.Items), options.DryRun)); err != nil {
		return outcome, fmt.Errorf("cannot write the audit trail, so nothing was removed: %w", err)
	}

	fresh, err := deps.Replan(ctx, plan.Options)
	if err != nil {
		return outcome, fmt.Errorf("cannot confirm what is safe right now, so nothing was removed: %w", err)
	}
	eligible := map[string]bool{}
	for _, item := range fresh.Items {
		eligible[item.ID] = true
	}
	whyNot := map[string]string{}
	for _, excluded := range fresh.Excluded {
		whyNot[excluded.ID] = excluded.Reason
	}

	stop := func(reason string) { outcome.Stopped = reason }
	handled := 0
	for _, item := range plan.Items {
		if err := ctx.Err(); err != nil {
			stop("cancelled")
			break
		}
		if options.MaxImages > 0 && handled >= options.MaxImages {
			stop(fmt.Sprintf("reached the limit of %d images", options.MaxImages))
			break
		}
		item := item

		// Only images from the plan are ever considered, and only if live state
		// still says they are eligible. Images that appeared since are not touched.
		if !eligible[item.ID] {
			reason := "no longer eligible"
			if why := whyNot[item.ID]; why != "" {
				reason += ": " + why
			} else {
				reason += ": it is no longer on this server"
			}
			outcome.Skipped = append(outcome.Skipped, Skip{Item: item, Reason: reason})
			if err := record("skip", &item, reason); err != nil {
				stop("audit trail failed: " + err.Error())
				break
			}
			continue
		}

		// The last check before deleting: a container may have started since the
		// plan was made, or since the previous image was removed.
		used, err := deps.InUse(ctx, item.ID)
		if err != nil {
			reason := "could not verify that no container uses it: " + err.Error()
			outcome.Failed = append(outcome.Failed, Failure{Item: item, Reason: reason})
			_ = record("fail", &item, reason)
			if len(outcome.Failed) >= maxFailures {
				stop(fmt.Sprintf("stopped after %d failures", len(outcome.Failed)))
				break
			}
			continue
		}
		if used {
			reason := "a container started using it"
			outcome.Skipped = append(outcome.Skipped, Skip{Item: item, Reason: reason})
			if err := record("skip", &item, reason); err != nil {
				stop("audit trail failed: " + err.Error())
				break
			}
			continue
		}

		handled++
		if options.DryRun {
			outcome.Removed = append(outcome.Removed, item)
			if err := record("would-remove", &item, ""); err != nil {
				stop("audit trail failed: " + err.Error())
				break
			}
			continue
		}

		// Record the intent before the deletion, so a crash mid-removal still
		// leaves a trail of what was being removed.
		if err := record("remove", &item, ""); err != nil {
			stop("audit trail failed: " + err.Error())
			break
		}
		if err := deps.Remove(ctx, item.ID); err != nil {
			reason := err.Error()
			outcome.Failed = append(outcome.Failed, Failure{Item: item, Reason: reason})
			_ = record("fail", &item, reason)
			if len(outcome.Failed) >= maxFailures {
				stop(fmt.Sprintf("stopped after %d failures", len(outcome.Failed)))
				break
			}
			continue
		}
		outcome.Removed = append(outcome.Removed, item)
		// Written only after the deletion succeeded, so the trail distinguishes
		// "was being removed" from "was removed".
		if err := record("removed", &item, ""); err != nil {
			stop("audit trail failed: " + err.Error())
			break
		}
	}

	// The audit trail must never claim a removal that did not happen, so a dry
	// run says "would remove".
	verb := "removed"
	if options.DryRun {
		verb = "dry run: would remove"
	}
	summary := fmt.Sprintf("%s %d, skipped %d, failed %d", verb, len(outcome.Removed), len(outcome.Skipped), len(outcome.Failed))
	if outcome.Stopped != "" {
		summary += "; stopped: " + outcome.Stopped
	}
	_ = record("end", nil, summary)
	return outcome, nil
}
