package accounts

import (
	"context"
	"time"
)

// RunReplica is what a host that has joined a cluster runs: it tells its auth service to
// act as a replica (copy the cluster's accounts, never change them), then keeps the copy
// current. The mode is set from here, not by a flag, because only the agent knows whether
// this host has joined; the auth service remembers it across restarts.
//
// The auth service may not be up yet when the agent starts, so setting the mode is retried
// until it works. Nothing here can stop or slow the agent.
func RunReplica(ctx context.Context, local *Client, up Upstream, every time.Duration, report func(Outcome, error)) {
	for {
		err := local.SetMode(ctx, "replica")
		if err == nil {
			break
		}
		if report != nil {
			report(Outcome{}, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
	RunSync(ctx, local, up, every, report)
}
