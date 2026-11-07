package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fake struct {
	id      string
	finding Finding
	applied int
	planned int
	applyEr error
}

type recoveryUI struct {
	ask    func(context.Context, Screen) (string, error)
	events []Event
}

func (u *recoveryUI) CanRetrySteps() bool                               { return true }
func (u *recoveryUI) Ask(ctx context.Context, s Screen) (string, error) { return u.ask(ctx, s) }
func (u *recoveryUI) Report(e Event)                                    { u.events = append(u.events, e) }

func TestWindowRetriesOnlyTheFailedStage(t *testing.T) {
	for _, failure := range []error{ErrDeclined, errors.New("provider unavailable")} {
		t.Run(failure.Error(), func(t *testing.T) {
			a := &fake{id: "first", finding: Finding{Status: Needed}}
			b := &fake{id: "second", finding: Finding{Status: Needed}, applyEr: failure}
			c := &fake{id: "third", finding: Finding{Status: Needed}}
			asked := 0
			ui := &recoveryUI{ask: func(_ context.Context, s Screen) (string, error) {
				asked++
				if s.ID != "retry-second" || c.applied != 0 {
					t.Fatalf("unexpected retry: %+v", s)
				}
				b.applyEr = nil
				return "Retry this step", nil
			}}
			res, err := Run(context.Background(), []Step{a, b, c}, &Env{}, ui)
			if err != nil || asked != 1 || len(res) != 3 || a.applied != 1 || b.applied != 2 || c.applied != 1 {
				t.Fatalf("err=%v asks=%d results=%d applications=%d/%d/%d", err, asked, len(res), a.applied, b.applied, c.applied)
			}
		})
	}
}

func TestWindowRetryRechecksPartiallyCompletedWork(t *testing.T) {
	s := &fake{id: "publish", finding: Finding{Status: Needed}, applyEr: errors.New("connection lost")}
	ui := &recoveryUI{ask: func(_ context.Context, _ Screen) (string, error) {
		// The remote operation succeeded despite losing its response. Recheck
		// must recognize that result rather than publish the same work again.
		s.finding = Finding{Status: Done}
		return "Retry this step", nil
	}}
	res, err := Run(context.Background(), []Step{s}, &Env{}, ui)
	if err != nil || len(res) != 1 || s.applied != 1 || res[0].Finding.Status != Done {
		t.Fatalf("err=%v results=%+v applications=%d", err, res, s.applied)
	}
}

func TestWindowCanRecoverBlockedCheck(t *testing.T) {
	s := &fake{id: "check", finding: Finding{Status: Blocked, Detail: "missing access"}}
	ui := &recoveryUI{ask: func(_ context.Context, _ Screen) (string, error) {
		s.finding = Finding{Status: Needed}
		return "Retry this step", nil
	}}
	res, err := Run(context.Background(), []Step{s}, &Env{}, ui)
	if err != nil || len(res) != 1 || s.applied != 1 {
		t.Fatalf("err=%v results=%+v", err, res)
	}
}

func TestCancelDuringRecoveryEndsRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &fake{id: "a", finding: Finding{Status: Needed}, applyEr: errors.New("offline")}
	ui := &recoveryUI{ask: func(ctx context.Context, _ Screen) (string, error) {
		cancel()
		return "", ctx.Err()
	}}
	_, err := Run(ctx, []Step{s}, &Env{}, ui)
	if !errors.Is(err, context.Canceled) || s.applied != 1 {
		t.Fatalf("err=%v applications=%d", err, s.applied)
	}
}

type simulatedRetry struct {
	fake
	attempts int
}

func (s *simulatedRetry) Simulate(context.Context, *Env, UI) error {
	s.attempts++
	if s.attempts == 1 {
		return ErrDeclined
	}
	return nil
}

func TestWindowSimulationCanRetryWithoutRealActions(t *testing.T) {
	s := &simulatedRetry{fake: fake{id: "simulation"}}
	ui := &recoveryUI{ask: func(context.Context, Screen) (string, error) { return "Retry this step", nil }}
	res, err := Run(context.Background(), []Step{s}, &Env{DryRun: true, UIOnly: true}, ui)
	if err != nil || len(res) != 1 || s.attempts != 2 || s.applied != 0 {
		t.Fatalf("err=%v attempts=%d applied=%d", err, s.attempts, s.applied)
	}
}

func (f *fake) ID() string    { return f.id }
func (f *fake) Title() string { return f.id }
func (f *fake) Check(context.Context, *Env) (Finding, error) {
	return f.finding, nil
}
func (f *fake) Plan(context.Context, *Env) ([]string, error) {
	f.planned++
	return []string{"would do " + f.id}, nil
}
func (f *fake) Apply(context.Context, *Env, UI) error {
	f.applied++
	return f.applyEr
}

func TestADryRunNeverApplies(t *testing.T) {
	s := &fake{id: "a", finding: Finding{Status: Needed}}
	res, err := Run(context.Background(), []Step{s}, &Env{DryRun: true}, Headless{})
	if err != nil {
		t.Fatal(err)
	}
	if s.applied != 0 || s.planned != 1 || len(res[0].Plan) != 1 {
		t.Fatalf("applied=%d planned=%d plan=%v", s.applied, s.planned, res[0].Plan)
	}
}

func TestADoneStepIsSkipped(t *testing.T) {
	s := &fake{id: "a", finding: Finding{Status: Done}}
	if _, err := Run(context.Background(), []Step{s}, &Env{}, Headless{}); err != nil {
		t.Fatal(err)
	}
	if s.applied != 0 || s.planned != 0 {
		t.Fatalf("applied=%d planned=%d", s.applied, s.planned)
	}
}

func TestABlockedStepStopsTheRun(t *testing.T) {
	a := &fake{id: "a", finding: Finding{Status: Blocked, Detail: "no gh"}}
	b := &fake{id: "b", finding: Finding{Status: Needed}}
	res, err := Run(context.Background(), []Step{a, b}, &Env{}, Headless{})
	if err == nil || len(res) != 1 || b.applied != 0 {
		t.Fatalf("err=%v results=%d bApplied=%d", err, len(res), b.applied)
	}
}

func TestAFailedApplyStopsTheRun(t *testing.T) {
	a := &fake{id: "a", finding: Finding{Status: Needed}, applyEr: errors.New("boom")}
	b := &fake{id: "b", finding: Finding{Status: Needed}}
	if _, err := Run(context.Background(), []Step{a, b}, &Env{}, Headless{}); err == nil {
		t.Fatal("expected an error")
	}
	if b.applied != 0 {
		t.Fatal("a later step ran after a failure")
	}
}

func TestHeadlessPrefersSuppliedThenDefaultThenFails(t *testing.T) {
	h := Headless{Answers: map[string]string{"zone": "example.com"}}
	if v, _ := h.Ask(context.Background(), Screen{ID: "zone", Default: "x"}); v != "example.com" {
		t.Fatalf("got %q", v)
	}
	if v, _ := h.Ask(context.Background(), Screen{ID: "other", Default: "d"}); v != "d" {
		t.Fatalf("got %q", v)
	}
	if _, err := h.Ask(context.Background(), Screen{ID: "missing"}); !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("got %v", err)
	}
}

func TestADryRunReportsABlockedStepAndCarriesOn(t *testing.T) {
	a := &fake{id: "a", finding: Finding{Status: Blocked, Detail: "no gh"}}
	b := &fake{id: "b", finding: Finding{Status: Needed}}
	res, err := Run(context.Background(), []Step{a, b}, &Env{DryRun: true}, Headless{})
	if err != nil || len(res) != 2 || res[0].Finding.Status != Blocked {
		t.Fatalf("err=%v results=%+v", err, res)
	}
}

func TestASecretIsNeverTakenFromAnAnswerOnTheCommandLine(t *testing.T) {
	h := Headless{Answers: map[string]string{"cloudflare-token": "cfat_leaky"}, Getenv: func(string) string { return "" }}
	_, err := h.Ask(context.Background(), Screen{ID: "cloudflare-token", Kind: Secret})
	if err == nil || !strings.Contains(err.Error(), "ADAETUM_SECRET_CLOUDFLARE_TOKEN") {
		t.Fatalf("a secret was accepted from --answer, or the error does not say where to put it: %v", err)
	}
}

func TestASecretComesFromItsEnvironmentVariable(t *testing.T) {
	h := Headless{Getenv: func(k string) string {
		if k == "ADAETUM_SECRET_TAILSCALE_TOKEN" {
			return "tskey-x"
		}
		return ""
	}}
	got, err := h.Ask(context.Background(), Screen{ID: "tailscale-token", Kind: Secret})
	if err != nil || got != "tskey-x" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := h.Ask(context.Background(), Screen{ID: "other-secret", Kind: Secret}); !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("a missing secret should be ErrNoAnswer, got %v", err)
	}
}

func TestAReplayRunRefusesToCreateUnlessToldToExplicitly(t *testing.T) {
	h := Headless{Replay: true}
	err := ApproveCreate(context.Background(), h, "create-repository", "Create it?")
	if !errors.Is(err, ErrNotReplayable) {
		t.Fatalf("a replay run agreed to create something: %v", err)
	}
	h.Answers = map[string]string{"create-repository": "yes"}
	if err := ApproveCreate(context.Background(), h, "create-repository", "Create it?"); err != nil {
		t.Fatalf("an explicit yes was refused: %v", err)
	}
	// Reusing something that exists is still fine in a replay.
	if err := Approve(context.Background(), Headless{Replay: true}, "reuse-repository", "Reuse it?"); err != nil {
		t.Fatalf("a replay run refused to reuse: %v", err)
	}
	// And a normal headless run keeps its default-yes behaviour for the window's sake.
	if err := ApproveCreate(context.Background(), Headless{}, "x", "?"); err != nil {
		t.Fatalf("%v", err)
	}
}

type scripted struct {
	answers []string
	asked   []Screen
	retry   bool
	events  []Event
}

func (s *scripted) CanRetry() bool { return s.retry }
func (s *scripted) Report(e Event) { s.events = append(s.events, e) }
func (s *scripted) Ask(_ context.Context, sc Screen) (string, error) {
	s.asked = append(s.asked, sc)
	a := s.answers[0]
	s.answers = s.answers[1:]
	return a, nil
}

func TestAskValidShowsTheRejectionAndAsksAgainWhenTheFrontEndCanRetry(t *testing.T) {
	ui := &scripted{answers: []string{"bad", "good"}, retry: true}
	got, err := AskValid(context.Background(), ui, Screen{ID: "tok", Kind: Secret}, func(a string) (string, error) {
		if a == "bad" {
			return "", errors.New("nope")
		}
		return "accepted", nil
	})
	if err != nil || got != "good" {
		t.Fatalf("%q %v", got, err)
	}
	if len(ui.asked) != 2 || ui.asked[0].Error != "" || ui.asked[1].Error != "nope" {
		t.Fatalf("screens: %+v", ui.asked)
	}
	if len(ui.events) != 1 || ui.events[0].Message != "accepted" {
		t.Fatalf("the confirmation was not reported: %+v", ui.events)
	}
}

func TestAskValidEndsTheRunWhenThereIsNobodyToRetry(t *testing.T) {
	ui := &scripted{answers: []string{"bad", "good"}, retry: false}
	_, err := AskValid(context.Background(), ui, Screen{ID: "tok"}, func(string) (string, error) { return "", errors.New("nope") })
	if err == nil || len(ui.asked) != 1 {
		t.Fatalf("err=%v asked=%d", err, len(ui.asked))
	}
}
