package runs

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

// Kind says how safe it is to repeat a step. It decides what the engine does when
// Check says "no" or "cannot tell".
type Kind int

const (
	// Convergent steps can be applied any time: applying twice has the effect of
	// applying once (a Kubernetes apply, a service enable). If Check cannot tell,
	// the engine applies.
	Convergent Kind = iota
	// OnceOnly steps must not be repeated blindly (initialise OpenBao, generate a
	// key). They are applied only when Check definitely says "no". If Check cannot
	// tell, the run stops for a person.
	OnceOnly
	// Destructive steps delete or burn something. They are never applied unless the
	// run was started with that step approved, and never on an automatic retry
	// unless it was approved for the run.
	Destructive
)

// Status is the answer to "is this already true?".
type Status int

const (
	// Unsatisfied means the system definitely is not in the wanted state.
	Unsatisfied Status = iota
	// Satisfied means it is, and healthy. Nothing more is done.
	Satisfied
	// Unknown means the check could not tell (an API did not answer sensibly).
	Unknown
)

// ProgressState is the answer to "has it become true yet?".
type ProgressState int

const (
	// Ready means the step's goal is met.
	Ready ProgressState = iota
	// Slow means it is not ready but is behaving normally; keep waiting.
	Slow
	// Broken means waiting will not help.
	Broken
)

// Progress is what Verify reports. A wait that says only "not ready" would hide
// the difference between a slow image pull and a crash loop, so it must say why.
type Progress struct {
	State ProgressState
	// Code is a reason from the closed list in model.go.
	Code string
	// Detail is a short sentence for the person watching. No secrets.
	Detail string
	// Marker summarises what has changed, for stall detection. While Marker keeps
	// changing the wait counts as progress; if it stays the same for the stall
	// window the step fails as stalled. A step chooses a Marker that moves when
	// real work moves (bytes pulled, pods ready) and ignores noise.
	Marker string
}

// Env is what a step receives about its run.
type Env struct {
	RunID  string
	Inputs map[string]string
}

// Step is one unit of work. Check, Apply and Verify must only act through the
// adapters their Procedure was built with.
type Step struct {
	Name string
	// Needs lists steps that must be satisfied first, by name.
	Needs []string
	// Locks names the resources this step changes ("openbao", "node:node-1"). Steps
	// in different runs that share a lock never overlap.
	Locks []string
	Kind  Kind

	// Check reports whether the step's goal already holds. It must read the real
	// system and never trust anything the engine remembers.
	Check func(ctx context.Context, env Env) (Status, error)
	// Apply makes the goal true. It may be nil for a step that changes nothing and only
	// waits for the system to get there by itself (a clock that is still
	// synchronizing, a route that is still coming up). The engine then goes straight
	// from "not yet" to waiting, using Verify. Only Convergent steps may omit it: a
	// once-only or destructive step exists to DO something.
	Apply func(ctx context.Context, env Env) error
	// Verify reports progress toward the goal after Apply. If nil, Check is used:
	// Satisfied is Ready, anything else is Slow with CodeStarting.
	Verify func(ctx context.Context, env Env) (Progress, error)

	// StallAfter and Ceiling override the policy for this step when non-zero.
	StallAfter time.Duration
	Ceiling    time.Duration
}

// Procedure is a named list of steps.
type Procedure struct {
	Name        string
	Description string
	Steps       []Step

	order []int // indexes of Steps in dependency order, set by validate
}

// validate checks the procedure is well formed and computes its run order. A
// procedure with a cycle or a missing dependency is a programming error and is
// rejected when registered, never discovered halfway through a first boot.
func (p *Procedure) validate() error {
	if p.Name == "" {
		return errors.New("a procedure needs a name")
	}
	if len(p.Steps) == 0 {
		return fmt.Errorf("procedure %s has no steps", p.Name)
	}
	index := map[string]int{}
	for i, step := range p.Steps {
		switch {
		case step.Name == "":
			return fmt.Errorf("procedure %s: step %d has no name", p.Name, i+1)
		case step.Check == nil:
			return fmt.Errorf("procedure %s: step %s has no Check", p.Name, step.Name)
		case step.Apply == nil && step.Kind != Convergent:
			return fmt.Errorf("procedure %s: step %s is %s and needs an Apply", p.Name, step.Name, step.Kind)
		}
		if _, dup := index[step.Name]; dup {
			return fmt.Errorf("procedure %s: duplicate step %s", p.Name, step.Name)
		}
		index[step.Name] = i
	}
	for _, step := range p.Steps {
		for _, need := range step.Needs {
			if _, ok := index[need]; !ok {
				return fmt.Errorf("procedure %s: step %s needs %s, which does not exist", p.Name, step.Name, need)
			}
		}
	}
	// Depth-first order, keeping declaration order among independent steps so a
	// procedure reads top to bottom the way it runs.
	const (
		unvisited = iota
		visiting
		visited
	)
	mark := make([]int, len(p.Steps))
	var order []int
	var visit func(i int) error
	visit = func(i int) error {
		switch mark[i] {
		case visited:
			return nil
		case visiting:
			return fmt.Errorf("procedure %s: step %s is part of a dependency cycle", p.Name, p.Steps[i].Name)
		}
		mark[i] = visiting
		needs := append([]string(nil), p.Steps[i].Needs...)
		sort.Strings(needs)
		for _, need := range needs {
			if err := visit(index[need]); err != nil {
				return err
			}
		}
		mark[i] = visited
		order = append(order, i)
		return nil
	}
	for i := range p.Steps {
		if err := visit(i); err != nil {
			return err
		}
	}
	p.order = order
	return nil
}

func (p *Procedure) stepNames() []string {
	names := make([]string, 0, len(p.order))
	for _, i := range p.order {
		names = append(names, p.Steps[i].Name)
	}
	return names
}
