// Command adaetum-setup is the headless front end: no window, no prompts. It
// drives the same engine the window does, which makes it the path for CI,
// reruns, and machines with no display.
//
// It dry-runs unless told to --apply. A headless run answers every question with
// a supplied --answer or the question's default, and the defaults are "yes", so
// an apply with no answers would publish things without asking. --replay is the
// safe rerun: it reuses what exists and refuses to create anything new.
//
// Secrets are never taken from --answer (command lines are visible to other users
// of the machine). A secret question is answered from its environment variable,
// ADAETUM_SECRET_<ID> with dashes as underscores, for example
// ADAETUM_SECRET_CLOUDFLARE_TOKEN.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Adaetum/Adaetum/installer/checkout"
	"github.com/Adaetum/Adaetum/installer/diagnostics"
	"github.com/Adaetum/Adaetum/installer/engine"
	"github.com/Adaetum/Adaetum/installer/stages"
	"github.com/Adaetum/Adaetum/installer/stages/media"
	"github.com/Adaetum/Adaetum/installer/stages/reset"
	"github.com/Adaetum/Adaetum/installer/sys"
	"github.com/Adaetum/Adaetum/installer/terminal"
)

type answers map[string]string

func (a answers) String() string { return fmt.Sprint(map[string]string(a)) }
func (a answers) Set(v string) error {
	key, val, ok := strings.Cut(v, "=")
	if !ok || key == "" {
		return fmt.Errorf("want id=value, got %q", v)
	}
	a[key] = val
	return nil
}

func main() {
	apply := flag.Bool("apply", false, "make the changes; without this setup only shows what it would do")
	dryRun := flag.Bool("dry-run", false, "show what setup would do and change nothing (refuses --apply)")
	replay := flag.Bool("replay", false, "reuse existing state only: never create a repository or start a large download unprompted")
	clean := flag.Bool("clean", false, "ignore saved credentials and values and ask again")
	only := flag.String("only", "", "run only these stages, comma separated (for example --only render,artifacts)")
	downloadISO := flag.Bool("download-iso", false, "only download and verify a Rocky installer ISO into the checkout, then exit")
	arch := flag.String("arch", "x86_64", "with --download-iso: x86_64 or aarch64")
	imageType := flag.String("type", "minimal", "with --download-iso: minimal or dvd")
	release := flag.String("release", media.Release, "with --download-iso: the Rocky Linux release")
	reset := flag.Bool("reset", false, "list what setup created in Tailscale and R2 (devices, OAuth clients, host logs); with --apply and --answer reset-confirm=yes, remove it")
	quick := flag.Bool("quick", false, "with --reset: skip the menu and remove only the cluster's Tailscale devices and their R2 logs (keeps the OAuth clients and SSH rule), after one confirmation")
	envPath := flag.String("env", "", "with --reset: the .env or recovery file naming what to reset (default: .env in the checkout)")
	given := answers{}
	flag.Var(given, "answer", "answer a question: id=value (repeatable)")
	flag.Parse()

	if *dryRun && *apply {
		fmt.Fprintln(os.Stderr, "--dry-run and --apply contradict each other")
		os.Exit(2)
	}
	root, err := checkout.Find()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if *downloadISO {
		os.Exit(downloadInstaller(root, *arch, *imageType, *release))
	}
	if *reset {
		os.Exit(runReset(root, *envPath, given, *apply, *quick))
	}
	env := &engine.Env{DryRun: !*apply, Clean: *clean}
	var ui engine.UI = engine.Headless{Answers: given, Replay: *replay, Log: func(e engine.Event) {
		if e.Message != "" {
			fmt.Printf("  %s: %s\n", e.StepID, e.Message)
		}
	}}

	var log *diagnostics.Log
	var runner sys.Runner = sys.Exec{}
	if *apply {
		log, err = diagnostics.Open(root)
		if err != nil {
			fmt.Fprintln(os.Stderr, "could not create installer log:", err)
			os.Exit(1)
		}
		log.Record("headless setup started")
		runner = diagnostics.Runner{Base: runner, Log: log}
		ui = diagnostics.UI{UI: ui, Log: log}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	workRoot := root
	var workspace *checkout.Workspace
	if *apply {
		var prepareErr error
		workspace, prepareErr = checkout.Prepare(ctx, root)
		if prepareErr != nil {
			fmt.Fprintln(os.Stderr, "could not prepare isolated setup checkout:", prepareErr)
			os.Exit(1)
		}
		workRoot = workspace.Root
	}
	steps, err := chooseStages(stages.ISOFromSource(workRoot, root, runner), *only)
	if err != nil {
		if workspace != nil {
			_ = workspace.Close()
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if log != nil {
		steps = diagnostics.Steps(steps, log)
	}
	results, runErr := engine.Run(ctx, steps, env, ui)
	if workspace != nil && env.Value("installer_iso") == "" {
		paths, retainErr := workspace.RetainInstallers(root)
		for _, path := range paths {
			fmt.Fprintln(os.Stderr, "Installer retained:", path)
		}
		if retainErr != nil {
			fmt.Fprintln(os.Stderr, "could not retain installer:", retainErr)
			if runErr == nil {
				runErr = fmt.Errorf("retain installer: %w", retainErr)
			}
		}
	}
	if workspace != nil {
		if cleanupErr := workspace.Close(); cleanupErr != nil {
			fmt.Fprintln(os.Stderr, "could not remove temporary setup checkout:", cleanupErr)
			if runErr == nil {
				runErr = cleanupErr
			}
		}
	}
	if log != nil {
		log.Record("headless setup finished stopped=%t", runErr != nil)
		_ = log.Close()
	}
	blocked := false
	for _, r := range results {
		fmt.Printf("%-18s %s\n", r.StepID, r.Finding.Status)
		if r.Finding.Detail != "" {
			fmt.Printf("    %s\n", r.Finding.Detail)
		}
		for _, line := range r.Plan {
			fmt.Printf("    - %s\n", line)
		}
		blocked = blocked || r.Finding.Status == engine.Blocked
	}
	if runErr != nil {
		fmt.Fprintln(os.Stderr, "setup stopped:", runErr)
		os.Exit(1)
	}
	if blocked {
		os.Exit(1)
	}
}

// resetStep builds the reset, in its quick form when asked.
func resetStep(root, envPath string, quick bool) *reset.Step {
	step := reset.New(sys.Exec{}, root, envPath)
	step.Quick = quick
	return step
}

// runReset lists, and with --apply and an explicit reset-confirm=yes answer removes, what
// setup created in Tailscale and R2. It uses a plain runner and no installer log: the log
// records command lines and this command's inputs are credentials.
func runReset(root, envPath string, given answers, apply, quick bool) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	// In a terminal, ask. Under a script, or when answers were given on the command
	// line, run headless: every question takes its safe default unless answered.
	interactive := terminal.IsInteractive() && len(given) == 0
	var ui engine.UI = engine.Headless{Answers: given, Log: func(e engine.Event) {
		if e.Message != "" {
			fmt.Printf("  %s: %s\n", e.StepID, e.Message)
		}
	}}
	if interactive {
		ui = terminal.New()
	}
	if envPath == "" {
		home, _ := os.UserHomeDir()
		found := envCandidates(root, home)
		switch {
		case interactive:
			def := ""
			if len(found) > 0 {
				def = found[0]
			}
			choice, err := askEnvFile(ctx, ui, found, def)
			if err != nil {
				fmt.Fprintln(os.Stderr, "reset stopped:", err)
				return 1
			}
			envPath = choice
		case len(found) > 0:
			envPath = found[0]
		}
	}
	env := &engine.Env{DryRun: !apply}
	results, err := engine.Run(ctx, []engine.Step{resetStep(root, envPath, quick)}, env, ui)
	for _, r := range results {
		fmt.Printf("%-18s %s\n", r.StepID, r.Finding.Status)
		if r.Finding.Detail != "" {
			fmt.Printf("    %s\n", r.Finding.Detail)
		}
		for _, line := range r.Plan {
			fmt.Printf("    - %s\n", line)
		}
		if r.Finding.Status == engine.Blocked {
			return 1
		}
	}
	if errors.Is(err, engine.ErrDeclined) {
		// Declining (the default for a headless run) is how a look-only run ends;
		// nothing was removed and that is not a failure.
		return 0
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "reset stopped:", err)
		return 1
	}
	return 0
}

// envCandidates lists the files that could name what to reset, best first: the
// checkout's own .env, then recovery files downloaded by setup, newest first.
func envCandidates(root, home string) []string {
	var out []string
	if info, err := os.Stat(filepath.Join(root, ".env")); err == nil && !info.IsDir() {
		out = append(out, filepath.Join(root, ".env"))
	}
	matches, _ := filepath.Glob(filepath.Join(home, "Downloads", "recovery_setup-*"))
	type file struct {
		path string
		mod  time.Time
	}
	var files []file
	for _, m := range matches {
		if info, err := os.Stat(m); err == nil && !info.IsDir() {
			files = append(files, file{m, info.ModTime()})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	for _, f := range files {
		out = append(out, f.path)
	}
	return out
}

// askEnvFile lets the operator pick one of the files found, or type another path.
func askEnvFile(ctx context.Context, ui engine.UI, found []string, def string) (string, error) {
	if len(found) == 0 {
		return ui.Ask(ctx, engine.Screen{ID: "reset-env", Kind: engine.Text, Title: "Which file names what to reset?",
			Intro: "No .env or downloaded recovery file was found. Give the path to one.", Prompt: "Path to the .env or recovery file"})
	}
	const other = "Another file..."
	pick, err := engine.Choose(ctx, ui, "reset-env", "Which file names what to reset?", append(append([]string{}, found...), other), def)
	if err != nil {
		return "", err
	}
	if pick != other {
		return pick, nil
	}
	return ui.Ask(ctx, engine.Screen{ID: "reset-env-path", Kind: engine.Text, Prompt: "Path to the .env or recovery file"})
}

// downloadInstaller is what `task iso:download` runs: fetch an installer into the
// checkout, resuming a partial download, and verify it against Rocky's published
// SHA-256.
func downloadInstaller(root, arch, imageType, release string) int {
	lib := media.Library{Root: root}
	last := time.Time{}
	path, err := lib.Download(context.Background(), arch, imageType, release, func(done, total int64) {
		if time.Since(last) < time.Second && done != total {
			return
		}
		last = time.Now()
		if total > 0 {
			fmt.Printf("\r%s of %s (%d%%)", media.GiB(done), media.GiB(total), done*100/total)
		}
	})
	fmt.Println()
	if err != nil {
		fmt.Fprintln(os.Stderr, "download failed:", err)
		return 1
	}
	fmt.Println("Verified installer ISO:", path)
	return 0
}

// chooseStages keeps the stages named in only, in their normal order. A targeted
// rerun skips earlier stages, so what they would have gathered (credentials, the
// chosen domain) is not in memory: stages that need it say so and stop.
func chooseStages(all []engine.Step, only string) ([]engine.Step, error) {
	if strings.TrimSpace(only) == "" {
		return all, nil
	}
	want := map[string]bool{}
	for _, id := range strings.Split(only, ",") {
		want[strings.TrimSpace(id)] = true
	}
	// Publication stages must establish the private destination first. In an
	// isolated checkout, origin initially identifies the source and may be public.
	if want["profile"] || want["render"] || want["artifacts"] {
		want["repository"] = true
	}
	var out []engine.Step
	for _, s := range all {
		if want[s.ID()] {
			out = append(out, s)
			delete(want, s.ID())
		}
	}
	for id := range want {
		var ids []string
		for _, s := range all {
			ids = append(ids, s.ID())
		}
		return nil, fmt.Errorf("unknown stage %q; the stages are: %s", id, strings.Join(ids, ", "))
	}
	return out, nil
}
