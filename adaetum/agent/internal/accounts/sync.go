package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Keeping accounts current on every machine. The rules live here, in one pure function
// (Decide), so every case can be tested without a network:
//
//	nobody has accounts yet         -> nothing to do
//	only this machine has accounts  -> OFFER them to the cluster (bootstrap: the first
//	                                   owner is made on the first host, before any cluster)
//	only the cluster has accounts   -> COPY them here
//	both, same history, cluster newer -> COPY them here
//	both, same history, same version -> nothing to do
//	both, same history, this one newer -> REPLACE: the cluster wins (it may have been
//	                                   restored from an old backup; the people added since
//	                                   are lost, which is the accepted cost)
//	both, different histories       -> REPLACE: the cluster wins (this machine was rebuilt,
//	                                   or set up on its own before it joined)
//
// The cluster is always the master. A host is read-only for accounts after it joins, so
// it has nothing of its own to lose except what it set up before joining; the first
// handover (an empty cluster) is the one case where the host's list goes up instead.
// Only a differing account layout or an ownerless host list is a WARN, which never
// changes anything. The auth service applies its own rules on top (see
// auth/src/accounts.ts), so even a wrong decision here cannot install an empty or
// ownerless list.

// Upstream is where the master copy lives: the cluster agent, reached over the signed
// host channel.
type Upstream interface {
	State(ctx context.Context) (State, error)
	Snapshot(ctx context.Context) (json.RawMessage, error)
	// Seed offers a copy to a cluster that has no accounts.
	Seed(ctx context.Context, snapshot json.RawMessage) (ApplyResult, error)
}

// Local is this machine's auth service.
type Local interface {
	State(ctx context.Context) (State, error)
	Snapshot(ctx context.Context) (json.RawMessage, error)
	Apply(ctx context.Context, snapshot json.RawMessage) (ApplyResult, error)
	// Replace installs the cluster's list over this machine's, whatever its history.
	Replace(ctx context.Context, snapshot json.RawMessage) (ApplyResult, error)
}

type Action string

const (
	Nothing Action = "nothing"
	Pull    Action = "pull"
	Push    Action = "push"
	Replace Action = "replace"
	Warn    Action = "warn"
)

type Decision struct {
	Action Action
	Why    string
}

// Decide chooses what to do from the two machines' positions.
func Decide(local, up State) Decision {
	switch {
	case local.Schema != up.Schema:
		return Decision{Warn, fmt.Sprintf("the account layout differs (this machine %d, the cluster %d): upgrade the older one", local.Schema, up.Schema)}
	case local.Users == 0 && up.Users == 0:
		return Decision{Nothing, "nobody has an account yet"}
	case up.Users == 0:
		if local.Owners == 0 {
			return Decision{Warn, "this machine has people but no owner, so they cannot be offered to the cluster"}
		}
		return Decision{Push, "the cluster has no accounts yet: offering this machine's"}
	case local.Users == 0:
		return Decision{Pull, "this machine has no accounts yet"}
	case local.Epoch != up.Epoch:
		return Decision{Replace, "this machine and the cluster keep different account lists: the cluster's replaces this machine's"}
	case up.Version > local.Version:
		return Decision{Pull, "the cluster has newer accounts"}
	case up.Version == local.Version:
		return Decision{Nothing, "up to date"}
	default:
		return Decision{Replace, fmt.Sprintf("the cluster's accounts (version %d) are older than this machine's (%d): it may have been restored from an old backup, and the cluster still wins", up.Version, local.Version)}
	}
}

// Outcome is what one round did.
type Outcome struct {
	Action Action
	Detail string
}

func (o Outcome) String() string { return string(o.Action) + ": " + o.Detail }

// ErrUpstream wraps "the cluster could not be reached", which is normal and not a problem:
// a host must keep signing people in from its last copy.
var ErrUpstream = errors.New("the cluster could not be reached")

// SyncOnce looks at both ends and does what Decide says.
func SyncOnce(ctx context.Context, local Local, up Upstream) (Outcome, error) {
	theirs, err := up.State(ctx)
	if err != nil {
		return Outcome{}, fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	mine, err := local.State(ctx)
	if err != nil {
		return Outcome{}, fmt.Errorf("the sign-in service on this machine did not answer: %w", err)
	}
	decision := Decide(mine, theirs)
	switch decision.Action {
	case Pull, Replace:
		snapshot, err := up.Snapshot(ctx)
		if err != nil {
			return Outcome{}, fmt.Errorf("%w: %v", ErrUpstream, err)
		}
		install := local.Apply
		if decision.Action == Replace {
			install = local.Replace
		}
		result, err := install(ctx, snapshot)
		if err != nil {
			return Outcome{}, fmt.Errorf("the sign-in service on this machine did not take the copy: %w", err)
		}
		if result.Status == "rejected" {
			return Outcome{Warn, fmt.Sprintf("this machine refused the cluster's copy (%s): %s", result.Reason, result.Detail)}, nil
		}
		if decision.Action == Replace {
			return Outcome{Replace, fmt.Sprintf("replaced this machine's accounts with the cluster's, version %d (%s)", result.Version, decision.Why)}, nil
		}
		return Outcome{Pull, fmt.Sprintf("took the cluster's accounts, version %d", result.Version)}, nil
	case Push:
		snapshot, err := local.Snapshot(ctx)
		if errors.Is(err, ErrNobody) {
			return Outcome{Nothing, "nobody to offer"}, nil
		}
		if err != nil {
			return Outcome{}, fmt.Errorf("the sign-in service on this machine did not answer: %w", err)
		}
		result, err := up.Seed(ctx, snapshot)
		if err != nil {
			return Outcome{}, fmt.Errorf("%w: %v", ErrUpstream, err)
		}
		if result.Status == "rejected" {
			return Outcome{Warn, fmt.Sprintf("the cluster refused this machine's accounts (%s): %s", result.Reason, result.Detail)}, nil
		}
		return Outcome{Push, fmt.Sprintf("gave the cluster this machine's accounts, version %d", result.Version)}, nil
	default:
		return Outcome{decision.Action, decision.Why}, nil
	}
}

// RunSync repeats SyncOnce until ctx ends. A failed round is reported (once per distinct
// message, so an unreachable cluster does not flood the log) and the next round tries
// again: nothing here may stop the agent or slow anything else. Between rounds there is
// no state to lose: every decision is made from the two machines' current positions.
func RunSync(ctx context.Context, local Local, up Upstream, every time.Duration, report func(Outcome, error)) {
	if every <= 0 {
		every = 10 * time.Second
	}
	var last string
	say := func(o Outcome, err error) {
		message := o.String()
		if err != nil {
			message = "error: " + err.Error()
		}
		// Quiet "nothing to do" rounds; speak up on anything new.
		if o.Action == Nothing && err == nil {
			last = ""
			return
		}
		if message != last {
			last = message
			if report != nil {
				report(o, err)
			}
		}
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		outcome, err := SyncOnce(ctx, local, up)
		if ctx.Err() != nil {
			return
		}
		say(outcome, err)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
