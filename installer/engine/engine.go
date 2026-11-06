// Package engine is the one place setup logic lives. A front end (the Wails
// window today, the headless command for CI and reruns) only draws Screens and
// returns answers; it never decides what happens next. That is what keeps the
// window and the headless run ending in the same state.
package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Status is what a step's Check found.
type Status string

const (
	// Done means the world already matches what the step would produce; Apply is skipped.
	Done Status = "done"
	// Needed means Apply has work to do.
	Needed Status = "needed"
	// Blocked means the step cannot run until the operator fixes something. Detail says what.
	Blocked Status = "blocked"
)

// Finding is the result of a read-only Check.
type Finding struct {
	Status Status
	Detail string
}

// Env is what every step shares. DryRun is enforced by the Runner, not by each
// step, so a step cannot forget to honour it.
type Env struct {
	DryRun bool
	// ReportPlanProgress lets a window follow a read-only run through the same
	// stage list without marking planned work as applied.
	ReportPlanProgress bool
	// Values carries public, non-secret choices from one stage to the next, such as
	// the domain chosen in the providers stage. A stage that finds one missing asks
	// for it itself, so it also works run on its own. Secrets never go here.
	Values map[string]string
	// Secrets carries credentials from one stage to the next, in memory only. They
	// are never logged, never shown in a Result, and only leave the process through
	// the OS credential store or the provider they belong to.
	Secrets map[string]string
	// Clean ignores saved credentials and values and asks again.
	Clean bool
}

// Secret returns a shared credential, or "" if there is none.
func (e *Env) Secret(key string) string { return e.Secrets[key] }

// SetSecret records a credential for later stages. An empty value is ignored.
func (e *Env) SetSecret(key, value string) {
	if value == "" {
		return
	}
	if e.Secrets == nil {
		e.Secrets = map[string]string{}
	}
	e.Secrets[key] = value
}

// Value returns a shared choice, or "" if none has been made.
func (e *Env) Value(key string) string { return e.Values[key] }

// SetValue records a shared choice for later stages.
func (e *Env) SetValue(key, value string) {
	if e.Values == nil {
		e.Values = map[string]string{}
	}
	e.Values[key] = value
}

// Step is one stage of setup. Check and Plan must not change anything: they are
// what a dry run executes, and what "is this already done?" relies on.
type Step interface {
	ID() string
	Title() string
	Check(ctx context.Context, env *Env) (Finding, error)
	// Plan lists, in plain words, what Apply would do right now.
	Plan(ctx context.Context, env *Env) ([]string, error)
	// Apply changes the world. The Runner never calls it during a dry run.
	Apply(ctx context.Context, env *Env, ui UI) error
}

// Kind is the closed set of screens a front end must be able to draw. Adding a
// kind means changing every front end, so the set stays small on purpose.
type Kind string

const (
	Text    Kind = "text"
	Secret  Kind = "secret"
	Choice  Kind = "choice"
	Confirm Kind = "confirm"
)

// Screen is one question. Secret answers go to the engine and the OS credential
// store only; a front end must not keep, log or echo them.
type Screen struct {
	ID     string // stable name, so headless runs can supply the answer by key
	Kind   Kind
	Prompt string
	// Title, Intro, Steps and List describe a question that needs the operator to
	// do something first (make a token, attach a disk). They are shown as a
	// heading, one sentence, numbered actions and a compact list of facts, instead
	// of a paragraph. Prompt is still the question itself.
	Title     string
	Intro     string
	Steps     []Instruction
	ListTitle string
	List      []string
	// YesLabel and NoLabel name the buttons of a Confirm ("Save a copy", "Not now").
	YesLabel string
	NoLabel  string
	Options  []string // Choice only
	Default  string   // used by headless runs when no answer is supplied
	// Creates marks a question whose "yes" makes something new outside this machine
	// (a repository, a multi-gigabyte download). A replay run never says yes to one
	// of these on its own.
	Creates bool
	// Error is why the previous answer to this same question was rejected. A front
	// end shows it on the question so the operator can correct it in place.
	Error string
}

// Instruction is one numbered action on a screen. Link gets an "open" button and
// Command a "copy" button.
type Instruction struct {
	Text    string
	Link    string
	Command string
}

// Event is progress a front end shows while a step runs. Kind is empty for a
// plain note, or one of the Step* constants so a front end can mark a step as
// running, finished or failed without guessing from text.
type Event struct {
	StepID  string
	Kind    string
	Message string
	Status  string
	Detail  string
	Plan    []string
	// Link goes with a KindCode event: where to enter the code.
	Link string
}

const (
	StepStarted  = "step-started"
	StepFinished = "step-finished"
	StepFailed   = "step-failed"
	PlanStarted  = "plan-started"
	PlanFinished = "plan-finished"
	PlanFailed   = "plan-failed"
	// Code is a one-time code the operator must enter on a web page (GitHub's device
	// sign-in): Message is the code and Link is the page. The front end shows it
	// prominently with a button to open the page; the step carries on by itself once
	// the operator has entered it.
	KindCode = "code"
	// Progress is a status line that is replaced by the next one (a download's
	// percentage), not added to the log.
	Progress = "progress"
)

// UI is everything a step may ask of a front end.
type UI interface {
	Ask(ctx context.Context, s Screen) (string, error)
	Report(e Event)
}

// ErrNoAnswer is returned by a headless run when a screen has neither a supplied
// answer nor a default. Stopping beats hanging a CI job on a prompt.
var ErrNoAnswer = errors.New("no answer supplied and no default")

// Result records what happened to one step.
type Result struct {
	StepID  string
	Finding Finding
	Plan    []string // filled in a dry run
	Applied bool
}

// Run walks the steps in order. A blocked step stops a real run. In a dry run only
// Check and Plan execute, and a blocked step is reported rather than fatal.
func Run(ctx context.Context, steps []Step, env *Env, ui UI) ([]Result, error) {
	var results []Result
	for _, s := range steps {
		if env.DryRun && env.ReportPlanProgress {
			ui.Report(Event{StepID: s.ID(), Kind: PlanStarted})
		}
		finding, err := s.Check(ctx, env)
		if err != nil {
			if env.DryRun && env.ReportPlanProgress {
				ui.Report(Event{StepID: s.ID(), Kind: PlanFailed, Message: err.Error()})
			}
			return results, fmt.Errorf("%s: check: %w", s.ID(), err)
		}
		res := Result{StepID: s.ID(), Finding: finding}
		switch finding.Status {
		case Done:
			results = append(results, res)
			if env.DryRun && env.ReportPlanProgress {
				ui.Report(Event{StepID: s.ID(), Kind: PlanFinished, Status: string(finding.Status), Detail: finding.Detail})
			}
			continue
		case Blocked:
			results = append(results, res)
			// A dry run exists to show the whole journey, including what is in the
			// way, so it reports a blocked step and carries on. A real run stops.
			if env.DryRun {
				if env.ReportPlanProgress {
					ui.Report(Event{StepID: s.ID(), Kind: PlanFinished, Status: string(finding.Status), Detail: finding.Detail})
				}
				continue
			}
			return results, fmt.Errorf("%s: blocked: %s", s.ID(), finding.Detail)
		}
		if env.DryRun {
			plan, err := s.Plan(ctx, env)
			if err != nil {
				if env.ReportPlanProgress {
					ui.Report(Event{StepID: s.ID(), Kind: PlanFailed, Message: err.Error()})
				}
				return results, fmt.Errorf("%s: plan: %w", s.ID(), err)
			}
			res.Plan = plan
			results = append(results, res)
			if env.ReportPlanProgress {
				ui.Report(Event{StepID: s.ID(), Kind: PlanFinished, Status: string(finding.Status), Detail: finding.Detail, Plan: plan})
			}
			continue
		}
		ui.Report(Event{StepID: s.ID(), Kind: StepStarted})
		if err := s.Apply(ctx, env, ui); err != nil {
			ui.Report(Event{StepID: s.ID(), Kind: StepFailed, Message: err.Error()})
			results = append(results, res)
			return results, fmt.Errorf("%s: apply: %w", s.ID(), err)
		}
		ui.Report(Event{StepID: s.ID(), Kind: StepFinished})
		res.Applied = true
		results = append(results, res)
	}
	return results, nil
}

// ErrDeclined is returned when the operator answers no to a question the step
// cannot continue without. It stops the run cleanly; nothing has gone wrong.
var ErrDeclined = errors.New("stopped at the operator's request")

// Approve asks a yes/no question that defaults to yes. A headless run therefore
// proceeds unless an answer says otherwise, which is what "silent replay" means.
func Approve(ctx context.Context, ui UI, id, prompt string) error {
	return ApproveDefault(ctx, ui, id, prompt, true)
}

// ApproveCreate asks before creating something new outside this machine. It
// defaults to yes for an operator at the window, but a replay run (see Headless)
// refuses it unless an answer is supplied explicitly, so rerunning setup can never
// create a repository or start a large download by itself.
func ApproveCreate(ctx context.Context, ui UI, id, prompt string) error {
	answer, err := ui.Ask(ctx, Screen{ID: id, Kind: Confirm, Prompt: prompt, Default: "yes", Creates: true})
	if err != nil {
		return err
	}
	switch answer {
	case "yes", "y", "true", "Y", "YES", "Yes":
		return nil
	}
	return ErrDeclined
}

// ApproveDefault is Approve with a chosen default. Use a "no" default for a
// question where agreeing carries risk, so a silent run does not agree for you.
func ApproveDefault(ctx context.Context, ui UI, id, prompt string, yes bool) error {
	def := "no"
	if yes {
		def = "yes"
	}
	answer, err := ui.Ask(ctx, Screen{ID: id, Kind: Confirm, Prompt: prompt, Default: def})
	if err != nil {
		return err
	}
	switch answer {
	case "yes", "y", "true", "Y", "YES", "Yes":
		return nil
	}
	return ErrDeclined
}

// AskText asks for a line of text, offering def.
func AskText(ctx context.Context, ui UI, id, prompt, def string) (string, error) {
	return ui.Ask(ctx, Screen{ID: id, Kind: Text, Prompt: prompt, Default: def})
}

// Choose asks the operator to pick one of options and returns the option chosen.
// An answer may be the whole option or the start of it, case-insensitively, so a
// headless run can say "minimal" for "Minimal (offline installer) — ...". def is
// what a headless run uses when no answer is supplied; leave it empty when a
// silent pick would be a guess (which account or zone, for instance), so the run
// stops and asks for an explicit answer.
func Choose(ctx context.Context, ui UI, id, prompt string, options []string, def string) (string, error) {
	if len(options) == 0 {
		return "", fmt.Errorf("nothing to choose from for %q", prompt)
	}
	if len(options) == 1 {
		def = options[0]
	}
	answer, err := ui.Ask(ctx, Screen{ID: id, Kind: choiceKind, Prompt: prompt, Options: options, Default: def})
	if err != nil {
		return "", err
	}
	a := lower(answer)
	for _, o := range options {
		if lower(o) == a {
			return o, nil
		}
	}
	for _, o := range options {
		if a != "" && hasPrefix(lower(o), a) {
			return o, nil
		}
	}
	return "", fmt.Errorf("%q is not one of the choices for %q", answer, prompt)
}

const choiceKind = Choice

func lower(s string) string           { return strings.ToLower(strings.TrimSpace(s)) }
func hasPrefix(s, prefix string) bool { return strings.HasPrefix(s, prefix) }

// Retrier is implemented by a front end that can show a rejected answer and ask
// again on the same screen (the window). A headless run has nobody to retry.
type Retrier interface{ CanRetry() bool }

// AskValid asks a question and checks the answer, and keeps asking until the answer
// is accepted. check returns a short confirmation to show on success (for example
// "3 zones found") and an error to show when the answer is rejected. In the window
// the error appears on the same question and the operator corrects it; a headless
// run has no one to ask again, so a rejection ends the run with that error.
func AskValid(ctx context.Context, ui UI, s Screen, check func(answer string) (string, error)) (string, error) {
	retry := false
	if r, ok := ui.(Retrier); ok {
		retry = r.CanRetry()
	}
	for {
		answer, err := ui.Ask(ctx, s)
		if err != nil {
			return "", err
		}
		note, err := check(answer)
		if err == nil {
			if note != "" {
				ui.Report(Event{StepID: s.ID, Message: note})
			}
			return answer, nil
		}
		if !retry {
			return "", err
		}
		s.Error = err.Error()
	}
}

// ApproveScreen asks a yes/no question that carries its own heading, actions and
// button labels. Like Approve it defaults to yes.
func ApproveScreen(ctx context.Context, ui UI, s Screen) error {
	s.Kind = Confirm
	if s.Default == "" {
		s.Default = "yes"
	}
	answer, err := ui.Ask(ctx, s)
	if err != nil {
		return err
	}
	switch answer {
	case "yes", "y", "true", "Y", "YES", "Yes":
		return nil
	}
	return ErrDeclined
}
