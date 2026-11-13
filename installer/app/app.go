package main

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/Adaetum/Adaetum/installer/checkout"
	"github.com/Adaetum/Adaetum/installer/diagnostics"
	"github.com/Adaetum/Adaetum/installer/engine"
	"github.com/Adaetum/Adaetum/installer/stages"
	"github.com/Adaetum/Adaetum/installer/sys"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App is what the page can call. It stays thin: it forwards the engine's
// questions to the page and the operator's answers back, and nothing here
// decides what setup does next.
type App struct {
	ctx context.Context

	mu      sync.Mutex
	current string // the step now running
	running bool
	cancel  context.CancelFunc
	answers chan string
	fresh   bool // set from the first screen: ignore everything saved on this run
}

func NewApp() *App { return &App{answers: make(chan string, 1)} }

// SetFresh is the first screen's "start fresh" switch. It makes the next run behave like
// `task init:clean`: saved credentials and values are ignored and asked for again.
func (a *App) SetFresh(on bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.fresh = on
}

func (a *App) startup(ctx context.Context) { a.ctx = ctx }

// Stage is one row in the page's step list.
type Stage struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// StageResult is what a dry run found for one stage.
type StageResult struct {
	ID     string   `json:"id"`
	Status string   `json:"status"`
	Detail string   `json:"detail"`
	Plan   []string `json:"plan"`
}

// Report is a whole run. Stopped is why it ended early, if it did; the results
// gathered before that are still in Results.
type Report struct {
	Results []StageResult `json:"results"`
	Stopped string        `json:"stopped"`
}

// Instruction is one numbered action on a question.
type Instruction struct {
	Text    string `json:"text"`
	Link    string `json:"link"`
	Command string `json:"command"`
}

// Screen is a question for the operator, drawn by the page.
type Screen struct {
	StepID    string        `json:"stepId"` // the step asking, so the page can show it
	Title     string        `json:"title"`
	Intro     string        `json:"intro"`
	Steps     []Instruction `json:"steps"`
	ListTitle string        `json:"listTitle"`
	List      []string      `json:"list"`
	YesLabel  string        `json:"yesLabel"`
	NoLabel   string        `json:"noLabel"`
	ID        string        `json:"id"`
	Kind      string        `json:"kind"`
	Prompt    string        `json:"prompt"`
	Options   []string      `json:"options"`
	Default   string        `json:"default"`
	// Error is why the previous answer to this question was rejected, shown on the
	// same question so it can be corrected in place.
	Error string `json:"error"`
}

// Note is progress from a running step.
type Note struct {
	StepID  string `json:"stepId"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
	Link    string `json:"link"`
}

// steps builds the stage list for the checkout, or for no checkout at all (the
// titles do not need one; running them does, and says so).
func (a *App) steps() ([]engine.Step, error) {
	return a.stepsWithRunner(sys.Exec{})
}

func (a *App) stepsWithRunner(run sys.Runner) ([]engine.Step, error) {
	root, err := checkout.Find()
	if err != nil {
		return stages.ISO("", run), err
	}
	return stages.ISO(root, run), nil
}

// Stages lists the install path's steps in order.
func (a *App) Stages() []Stage {
	steps, _ := a.steps()
	var out []Stage
	for _, s := range steps {
		out = append(out, Stage{ID: s.ID(), Title: s.Title()})
	}
	return out
}

func report(results []engine.Result, err error) Report {
	var out []StageResult
	for _, r := range results {
		out = append(out, StageResult{ID: r.StepID, Status: string(r.Finding.Status), Detail: r.Finding.Detail, Plan: r.Plan})
	}
	rep := Report{Results: out}
	if err != nil {
		rep.Stopped = err.Error()
	}
	return rep
}

// DryRun checks and plans every stage and changes nothing.
func (a *App) DryRun() Report {
	steps, err := a.steps()
	if err != nil {
		return Report{Stopped: err.Error()}
	}
	return report(engine.Run(a.ctx, steps, &engine.Env{DryRun: true}, windowUI{app: a}))
}

// DryRunMode reports whether this window was launched in UI-only rehearsal mode.
func (a *App) DryRunMode() bool { return os.Getenv("ADAETUM_SETUP_DRY_RUN") == "1" }

// Start runs setup in the background. Window dry runs use each stage's UI-only
// rehearsal path; ordinary runs perform the real checks and applies.
func (a *App) Start() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.running {
		return "setup is already running"
	}
	dryRun := a.DryRunMode()
	var log *diagnostics.Log
	var runner sys.Runner = sys.Exec{}
	if !dryRun {
		root, err := checkout.Find()
		if err != nil {
			return err.Error()
		}
		log, err = diagnostics.Open(root)
		if err != nil {
			return "could not create installer log: " + err.Error()
		}
		runner = diagnostics.Runner{Base: runner, Log: log}
		log.Record("setup window started")
	}
	root, err := checkout.Find()
	clean := a.fresh || os.Getenv("ADAETUM_CLEAN") == "1" // Start already holds a.mu
	var workspace *checkout.Workspace
	workRoot := root
	if err == nil && !dryRun {
		workspace, err = checkout.Prepare(a.ctx, root)
		if err == nil {
			workRoot = workspace.Root
			// Reuse everything an earlier run made from .env unless the operator asked to
			// start fresh. Sign-ins are validated before use, so a stale one only costs a
			// question; generated values (Worker tokens, R2 credentials) must be reused.
			if !clean {
				if err = workspace.CarryAll(root); err != nil {
					_ = workspace.Close()
				}
			}
		}
	}
	var steps []engine.Step
	if err == nil {
		steps = stages.ISOFromSource(workRoot, root, runner)
	}
	if err != nil {
		if log != nil {
			_ = log.Close()
		}
		return err.Error()
	}
	if log != nil {
		steps = diagnostics.Steps(steps, log)
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.running, a.cancel = true, cancel
	go func() {
		var ui engine.UI = windowUI{app: a}
		if log != nil {
			ui = diagnostics.InteractiveUI{UI: diagnostics.UI{UI: ui, Log: log}}
		}
		if dryRun {
			ui = rehearsalUI{UI: ui, ctx: ctx}
		}
		// ADAETUM_CLEAN=1 (set by `task init:clean`) or the first screen's "start fresh"
		// ignores saved credentials and values.
		runEnv := &engine.Env{
			Clean: clean, DryRun: dryRun, UIOnly: dryRun,
		}
		results, runErr := engine.Run(ctx, steps, runEnv, ui)
		if workspace != nil && runEnv.Value("installer_iso") == "" {
			paths, retainErr := workspace.RetainInstallers(root)
			for _, path := range paths {
				ui.Report(engine.Event{StepID: "finalize", Message: "Installer retained: " + path})
			}
			if retainErr != nil {
				ui.Report(engine.Event{StepID: "finalize", Message: "Could not retain installer: " + retainErr.Error()})
				if runErr == nil {
					runErr = retainErr
				}
			}
		}
		if workspace != nil {
			// Keep what this run generated: it is already on GitHub and the Worker.
			if persistErr := workspace.Persist(root); persistErr != nil {
				ui.Report(engine.Event{StepID: "finalize", Message: "Could not save the generated values to .env: " + persistErr.Error()})
				if runErr == nil {
					runErr = persistErr
				}
			}
			if cleanupErr := workspace.Close(); cleanupErr != nil {
				ui.Report(engine.Event{StepID: "finalize", Message: "Could not remove the temporary setup checkout: " + cleanupErr.Error()})
				if runErr == nil {
					runErr = cleanupErr
				}
			}
		}
		rep := report(results, runErr)
		if log != nil {
			log.Record("setup window finished stopped=%t", rep.Stopped != "")
			_ = log.Close()
		}
		a.mu.Lock()
		a.running, a.cancel = false, nil
		a.mu.Unlock()
		runtime.EventsEmit(a.ctx, "finished", rep)
	}()
	return ""
}

// Pace only the UI-only window so working states remain visible during review.
type rehearsalUI struct {
	engine.UI
	ctx context.Context
}

func (u rehearsalUI) wait(ctx context.Context) error {
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (u rehearsalUI) Ask(ctx context.Context, screen engine.Screen) (string, error) {
	answer, err := u.UI.Ask(ctx, screen)
	if err != nil {
		return "", err
	}
	if err := u.wait(ctx); err != nil {
		return "", err
	}
	return answer, nil
}

func (u rehearsalUI) Report(event engine.Event) {
	u.UI.Report(event)
	if event.Kind == engine.StepStarted || (event.Message != "" && (event.Kind == "" || event.Kind == engine.Progress)) {
		_ = u.wait(u.ctx)
	}
}

func (u rehearsalUI) CanRetry() bool { r, ok := u.UI.(engine.Retrier); return ok && r.CanRetry() }
func (u rehearsalUI) CanRetrySteps() bool {
	r, ok := u.UI.(engine.StageRetrier)
	return ok && r.CanRetrySteps()
}

// Answer delivers the operator's reply to the question on screen.
func (a *App) Answer(value string) {
	select {
	case a.answers <- value:
	default:
	}
}

// Cancel stops a running setup and unblocks any question waiting for an answer.
func (a *App) Cancel() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cancel != nil {
		a.cancel()
	}
}

// windowUI is the engine's UI as the window: a question becomes an event and
// waits for Answer; progress becomes an event.
type windowUI struct{ app *App }

func (w windowUI) Ask(ctx context.Context, s engine.Screen) (string, error) {
	// A stale answer from an earlier question must not answer this one.
	select {
	case <-w.app.answers:
	default:
	}
	var steps []Instruction
	for _, in := range s.Steps {
		steps = append(steps, Instruction{Text: in.Text, Link: in.Link, Command: in.Command})
	}
	w.app.mu.Lock()
	step := w.app.current
	w.app.mu.Unlock()
	runtime.EventsEmit(w.app.ctx, "screen", Screen{StepID: step, Title: s.Title, Intro: s.Intro, Steps: steps, ListTitle: s.ListTitle, List: s.List, YesLabel: s.YesLabel, NoLabel: s.NoLabel, ID: s.ID, Kind: string(s.Kind), Prompt: s.Prompt, Options: s.Options, Default: s.Default, Error: s.Error})
	select {
	case v := <-w.app.answers:
		return v, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (w windowUI) Report(e engine.Event) {
	if e.Kind == engine.StepStarted || e.Kind == engine.StepReset {
		w.app.mu.Lock()
		w.app.current = e.StepID
		w.app.mu.Unlock()
	}
	runtime.EventsEmit(w.app.ctx, "note", Note{StepID: e.StepID, Kind: e.Kind, Message: e.Message, Link: e.Link})
}

// CanRetry tells the engine a rejected answer can be shown on the question and
// asked again, which is what the window does.
func (windowUI) CanRetry() bool { return true }

// A window keeps a failed stage available until the operator retries or cancels.
func (windowUI) CanRetrySteps() bool { return true }
