// Package hostready is the "host-readiness" procedure: is this machine ready to start
// bootstrap? It checks the three things first boot cannot do without, in the order it
// needs them: a route out, a synchronized clock (TLS depends on it, and the route is how
// the clock gets set), and enough free disk.
//
// It changes NOTHING. Every step is a wait step: the machine gets there by itself (a
// route comes up, the clock synchronizes), and the step says what it is waiting for and
// gives up with a reason if it never happens. That makes it safe to run any time, and
// it is the first step of the first-boot procedure when that is rebuilt: the same checks
// the shell script does ("Waiting for clock synchronization", "Waiting for network
// readiness"), with a stated reason instead of a silent loop.
//
// It talks to the machine only through the Host interface, so it is tested against a
// fake machine. The real one is internal/hostfacts.
package hostready

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/runs"
)

// Name is the procedure's name, as it appears in the API and the Runs page.
const Name = "host-readiness"

// Host is what the procedure needs to know about the machine. Every method is read-only.
type Host interface {
	HasDefaultRoute(ctx context.Context) (bool, error)
	// ClockSynchronized says whether the clock is synchronized; known is false when the
	// machine could not say.
	ClockSynchronized(ctx context.Context) (synced, known bool)
	FreeBytes(ctx context.Context, path string) (uint64, error)
}

// Inputs a run may set. Both are optional.
const (
	// InputMinFreeGiB is the free space required, in GiB. Default 20.
	InputMinFreeGiB = "min_free_gib"
	// InputPath is the filesystem to measure. Default /var, where container images and
	// storage live.
	InputPath = "path"
)

const (
	defaultMinFreeGiB = 20
	defaultPath       = "/var"
	gib               = 1 << 30

	// How long each wait may go without changing before it fails as stalled. They match
	// what the first-boot script allowed (about two minutes for the network, longer for
	// the clock, which needs the network first).
	networkStall = 3 * time.Minute
	clockStall   = 5 * time.Minute
)

// New returns the procedure, reading the machine through host.
func New(host Host) *runs.Procedure {
	return &runs.Procedure{
		Name:        Name,
		Description: "Checks that this machine can start the bootstrap: a route out, a synchronized clock, and enough free disk. It changes nothing.",
		Steps: []runs.Step{
			{
				Name:       "network",
				StallAfter: networkStall,
				Check: func(ctx context.Context, _ runs.Env) (runs.Status, error) {
					ok, err := host.HasDefaultRoute(ctx)
					if err != nil {
						return runs.Unknown, runs.BrokenBecause(runs.CodeConfig, err.Error())
					}
					return status(ok), nil
				},
				Verify: func(ctx context.Context, _ runs.Env) (runs.Progress, error) {
					ok, err := host.HasDefaultRoute(ctx)
					if err != nil {
						return runs.Progress{}, runs.BrokenBecause(runs.CodeConfig, err.Error())
					}
					if ok {
						return runs.Progress{State: runs.Ready}, nil
					}
					return runs.Progress{State: runs.Slow, Code: runs.CodeNetwork, Detail: "there is no route to the outside yet", Marker: "no-route"}, nil
				},
			},
			{
				Name:       "clock",
				Needs:      []string{"network"},
				StallAfter: clockStall,
				Check: func(ctx context.Context, _ runs.Env) (runs.Status, error) {
					synced, known := host.ClockSynchronized(ctx)
					switch {
					case !known:
						return runs.Unknown, nil
					case synced:
						return runs.Satisfied, nil
					}
					return runs.Unsatisfied, nil
				},
				Verify: func(ctx context.Context, _ runs.Env) (runs.Progress, error) {
					synced, known := host.ClockSynchronized(ctx)
					switch {
					case synced && known:
						return runs.Progress{State: runs.Ready}, nil
					case !known:
						return runs.Progress{State: runs.Slow, Code: runs.CodeUnknown, Detail: "the clock's state cannot be read", Marker: "unknown"}, nil
					}
					return runs.Progress{State: runs.Slow, Code: runs.CodeStarting, Detail: "waiting for the clock to synchronize", Marker: "waiting"}, nil
				},
			},
			{
				Name:  "disk",
				Needs: []string{"clock"},
				Check: func(ctx context.Context, env runs.Env) (runs.Status, error) {
					free, need, _, err := measure(ctx, host, env)
					if err != nil {
						return runs.Unknown, err
					}
					// A shortage is not an error for Check, it is "not yet".
					return status(free >= need), nil
				},
				// Free space does not grow by waiting, so a shortage is a failure at once.
				Verify: func(ctx context.Context, env runs.Env) (runs.Progress, error) {
					free, need, path, err := measure(ctx, host, env)
					if err != nil {
						return runs.Progress{}, err
					}
					if free >= need {
						return runs.Progress{State: runs.Ready}, nil
					}
					return runs.Progress{State: runs.Broken, Code: runs.CodeDisk, Detail: fmt.Sprintf("%.1f GiB is free on %s, and at least %d GiB is needed", float64(free)/gib, path, need/gib)}, nil
				},
			},
		},
	}
}

func status(ok bool) runs.Status {
	if ok {
		return runs.Satisfied
	}
	return runs.Unsatisfied
}

// measure reads the inputs and the free space. A bad input is a configuration error, not
// something to wait out.
func measure(ctx context.Context, host Host, env runs.Env) (free, need uint64, path string, err error) {
	path = defaultPath
	if value := env.Inputs[InputPath]; value != "" {
		path = value
	}
	minGiB := uint64(defaultMinFreeGiB)
	if value := env.Inputs[InputMinFreeGiB]; value != "" {
		parsed, parseErr := strconv.ParseUint(value, 10, 32)
		if parseErr != nil || parsed == 0 {
			return 0, 0, path, runs.BrokenBecause(runs.CodeConfig, fmt.Sprintf("%s must be a whole number of GiB, not %q", InputMinFreeGiB, value))
		}
		minGiB = parsed
	}
	free, readErr := host.FreeBytes(ctx, path)
	if readErr != nil {
		return 0, 0, path, runs.BrokenBecause(runs.CodeConfig, readErr.Error())
	}
	return free, minGiB * gib, path, nil
}
