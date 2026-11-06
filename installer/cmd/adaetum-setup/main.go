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
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Adaetum/Adaetum/installer/checkout"
	"github.com/Adaetum/Adaetum/installer/engine"
	"github.com/Adaetum/Adaetum/installer/stages"
	"github.com/Adaetum/Adaetum/installer/stages/media"
	"github.com/Adaetum/Adaetum/installer/sys"
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
	env := &engine.Env{DryRun: !*apply, Clean: *clean}
	ui := engine.Headless{Answers: given, Replay: *replay, Log: func(e engine.Event) {
		if e.Message != "" {
			fmt.Printf("  %s: %s\n", e.StepID, e.Message)
		}
	}}

	steps, err := chooseStages(stages.ISO(root, sys.Exec{}), *only)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	results, runErr := engine.Run(context.Background(), steps, env, ui)
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
