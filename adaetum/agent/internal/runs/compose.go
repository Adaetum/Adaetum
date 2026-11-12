package runs

import (
	"context"
	"fmt"
)

// Compose joins procedures into one that runs them in order. It is how first boot is
// built from chunks that can each be run and tested alone: every chunk stays a
// Procedure of its own, and the composite reuses its steps unchanged.
//
// The composite's steps are the chunks' steps with the chunk's name in front
// ("host-readiness/network"), so a run shows which chunk each step belongs to and two
// chunks may use the same step name. Within a chunk the dependencies are kept. Across
// chunks, each step that needed nothing waits for the previous chunk's final steps (the
// ones nothing else in that chunk depends on), so a later chunk never starts until the
// earlier one is satisfied, and a failure in an earlier chunk stops the rest.
func Compose(name, description string, parts ...*Procedure) (*Procedure, error) {
	if len(parts) == 0 {
		return nil, fmt.Errorf("procedure %s needs at least one part", name)
	}
	composite := &Procedure{Name: name, Description: description}
	var previousFinals []string
	for _, part := range parts {
		// validate also computes the part's run order and catches a malformed part now.
		if err := part.validate(); err != nil {
			return nil, fmt.Errorf("part of %s: %w", name, err)
		}
		needed := map[string]bool{}
		for _, step := range part.Steps {
			for _, need := range step.Needs {
				needed[need] = true
			}
		}
		var finals []string
		for _, step := range part.Steps {
			joined := step
			joined.Name = part.Name + "/" + step.Name
			joined.Needs = nil
			for _, need := range step.Needs {
				joined.Needs = append(joined.Needs, part.Name+"/"+need)
			}
			if len(step.Needs) == 0 {
				joined.Needs = append(joined.Needs, previousFinals...)
			}
			composite.Steps = append(composite.Steps, joined)
			if !needed[step.Name] {
				finals = append(finals, joined.Name)
			}
		}
		previousFinals = finals
	}
	return composite, nil
}

// Validation returns a step that checks something is acceptable and changes nothing:
// intake checks, and any "is this right?" gate. problem returns an empty string when all
// is well, or a short sentence saying what is wrong (never a secret). A problem is a
// failure at once, not something to wait out, so the step is Broken with that sentence.
//
// An error from problem means the check itself could not be made; the step fails with
// the error rather than guessing either way.
func Validation(name string, needs []string, code string, problem func(ctx context.Context, env Env) (string, error)) Step {
	return Step{
		Name:  name,
		Needs: needs,
		Check: func(ctx context.Context, env Env) (Status, error) {
			found, err := problem(ctx, env)
			if err != nil {
				return Unknown, err
			}
			if found == "" {
				return Satisfied, nil
			}
			return Unsatisfied, nil
		},
		Verify: func(ctx context.Context, env Env) (Progress, error) {
			found, err := problem(ctx, env)
			if err != nil {
				return Progress{}, err
			}
			if found == "" {
				return Progress{State: Ready}, nil
			}
			return Progress{State: Broken, Code: code, Detail: found}, nil
		},
	}
}
