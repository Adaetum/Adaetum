package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Adaetum/Adaetum/agent/internal/runs"
)

func step(name string, done *bool, apply func() error) runs.Step {
	return runs.Step{
		Name: name,
		Check: func(context.Context, runs.Env) (runs.Status, error) {
			if *done {
				return runs.Satisfied, nil
			}
			return runs.Unsatisfied, nil
		},
		Apply: func(context.Context, runs.Env) error {
			if err := apply(); err != nil {
				return err
			}
			*done = true
			return nil
		},
	}
}

func once(ctx context.Context, procedures []*runs.Procedure, list bool, name string, inputs, approve []string) (code int, out, logs string) {
	var o, l bytes.Buffer
	code = runOnce(ctx, &o, &l, procedures, list, name, inputs, approve)
	return code, o.String(), l.String()
}

func TestRunOnceSucceedsAndSummarizesEachStep(t *testing.T) {
	a, b := false, false
	p := &runs.Procedure{Name: "ok", Steps: []runs.Step{step("first", &a, func() error { return nil }), step("second", &b, func() error { return nil })}}
	p.Steps[1].Needs = []string{"first"}
	code, out, logs := once(context.Background(), []*runs.Procedure{p}, false, "ok", nil, nil)
	if code != exitDone {
		t.Fatalf("exit %d\n%s\n%s", code, out, logs)
	}
	for _, want := range []string{"ok: succeeded", "first", "second", "done"} {
		if !strings.Contains(out, want) {
			t.Errorf("the summary is missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(logs, "[RUN] ") || !strings.Contains(logs, "by=run") {
		t.Errorf("the log lines should go to the log stream:\n%s", logs)
	}
	if strings.Contains(out, "[RUN]") {
		t.Errorf("log lines must not be mixed into the summary:\n%s", out)
	}
}

func TestRunOnceReportsAFailureWithItsPlaceAndReason(t *testing.T) {
	x := false
	p := &runs.Procedure{Name: "bad", Steps: []runs.Step{step("images", &x, func() error { return runs.BrokenBecause(runs.CodeImagePull, "cannot pull gitea") })}}
	code, out, _ := once(context.Background(), []*runs.Procedure{p}, false, "bad", nil, nil)
	if code != exitFailed || !strings.Contains(out, "stopped at images: cannot pull gitea") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestADestructiveStepStopsForAPersonUntilApproved(t *testing.T) {
	x := false
	burn := step("burn", &x, func() error { return nil })
	burn.Kind = runs.Destructive
	p := &runs.Procedure{Name: "seal", Steps: []runs.Step{burn}}
	code, out, _ := once(context.Background(), []*runs.Procedure{p}, false, "seal", nil, nil)
	if code != exitNeedsYou || x {
		t.Fatalf("exit %d, applied %v\n%s", code, x, out)
	}
	if code, out, _ := once(context.Background(), []*runs.Procedure{p}, false, "seal", nil, []string{"burn"}); code != exitDone || !x {
		t.Fatalf("approved: exit %d, applied %v\n%s", code, x, out)
	}
}

func TestInputsReachTheProcedure(t *testing.T) {
	var got string
	x := false
	p := &runs.Procedure{Name: "in", Steps: []runs.Step{{
		Name: "read",
		Check: func(_ context.Context, env runs.Env) (runs.Status, error) {
			got = env.Inputs["node"]
			return runs.Satisfied, nil
		},
		Apply: func(context.Context, runs.Env) error { x = true; return nil },
	}}}
	code, _, _ := once(context.Background(), []*runs.Procedure{p}, false, "in", []string{"node=node-1"}, nil)
	if code != exitDone || got != "node-1" || x {
		t.Fatalf("exit %d, input %q", code, got)
	}
}

func TestUsageMistakesExitWithTwoAndSayWhatIsAvailable(t *testing.T) {
	x := false
	p := &runs.Procedure{Name: "one", Steps: []runs.Step{step("a", &x, func() error { return nil })}}
	cases := []struct {
		name    string
		run     string
		inputs  []string
		wantLog string
	}{
		{"no name", "", nil, "name a procedure"},
		{"unknown name", "nope", nil, "available: one"},
		{"input without equals", "one", []string{"node"}, "name=value"},
		{"secret-looking input", "one", []string{"admin_password=x"}, "looks like a secret"},
	}
	for _, c := range cases {
		code, _, logs := once(context.Background(), []*runs.Procedure{p}, false, c.run, c.inputs, nil)
		if code != exitUsage || !strings.Contains(logs, c.wantLog) {
			t.Errorf("%s: exit %d, log %q", c.name, code, logs)
		}
	}
}

func TestListShowsEveryProcedureWithItsStepsAndKinds(t *testing.T) {
	x := false
	burn := step("burn", &x, func() error { return nil })
	burn.Kind = runs.Destructive
	p := &runs.Procedure{Name: "seal", Description: "Seals it.", Steps: []runs.Step{step("check", &x, func() error { return nil }), burn}}
	p.Steps[1].Needs = []string{"check"}
	code, out, _ := once(context.Background(), []*runs.Procedure{p}, true, "", nil, nil)
	if code != exitDone {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"seal", "Seals it.", "- check", "- burn  (destructive)"} {
		if !strings.Contains(out, want) {
			t.Errorf("the list is missing %q:\n%s", want, out)
		}
	}
}

func TestInterruptingStopsTheRunCleanly(t *testing.T) {
	started := make(chan struct{}, 1)
	p := &runs.Procedure{Name: "hang", Steps: []runs.Step{{
		Name:  "wait",
		Check: func(context.Context, runs.Env) (runs.Status, error) { return runs.Unsatisfied, nil },
		Apply: func(ctx context.Context, _ runs.Env) error {
			started <- struct{}{}
			<-ctx.Done()
			return errors.New("interrupted")
		},
	}}}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-started; cancel() }()
	code, out, _ := once(ctx, []*runs.Procedure{p}, false, "hang", nil, nil)
	if code != exitStopped || !strings.Contains(out, "cancelled") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}
