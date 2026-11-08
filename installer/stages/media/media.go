package media

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Adaetum/Adaetum/installer/engine"
	"github.com/Adaetum/Adaetum/installer/sys"
)

const stepID = "media"

// Step is the installer-media stage.
type Step struct {
	Lib Library
	Run sys.Runner
	// Arch is this computer's architecture as Rocky names it, used to suggest the
	// matching image. It is a field so tests do not depend on the machine.
	Arch string
}

// New returns the stage for the checkout at root.
func New(run sys.Runner, root, home string) *Step {
	arch := "x86_64"
	if runtime.GOARCH == "arm64" {
		arch = "aarch64"
	}
	return &Step{Lib: Library{Root: root, Home: home, HTTP: &http.Client{}}, Run: run, Arch: arch}
}

func (s *Step) ID() string    { return stepID }
func (s *Step) Title() string { return "Rocky installer media and preflight" }

// Check never reports Done: the ISO is large and its verification needs the
// network, so setup verifies it when it runs rather than during a read-only look.
func (s *Step) Check(context.Context, *engine.Env) (engine.Finding, error) {
	found := s.Lib.Discover()
	if len(found) == 0 {
		return engine.Finding{Status: engine.Needed, Detail: "no Rocky 10 Minimal or DVD ISO found; one will be downloaded"}, nil
	}
	names := make([]string, len(found))
	for i, iso := range found {
		names[i] = filepath.Base(iso.Path)
	}
	return engine.Finding{Status: engine.Needed, Detail: "found " + strings.Join(names, ", ") + "; it will be verified against Rocky's published checksum"}, nil
}

func (s *Step) Plan(ctx context.Context, env *engine.Env) ([]string, error) {
	finding, _ := s.Check(ctx, env)
	return []string{
		"Use a Rocky Linux 10 Minimal or DVD installer ISO already on this computer, verified against Rocky's published SHA-256, or download one.",
		"Copy the ISO into this checkout if it is somewhere else.",
		"Run the setup readiness check.",
		"Right now: " + finding.Detail + ".",
	}, nil
}

func (s *Step) choose(ctx context.Context, ui engine.UI, id, prompt string, options []string) (string, error) {
	// The first option is the recommended one, so a silent run may take it.
	return engine.Choose(ctx, ui, id, prompt, options, options[0])
}

func (s *Step) Apply(ctx context.Context, env *engine.Env, ui engine.UI) error {
	say := func(msg string) { ui.Report(engine.Event{StepID: stepID, Message: msg}) }

	var path string
	var err error
	if found := s.Lib.Discover(); len(found) > 0 {
		path, err = s.useExisting(ctx, ui, found, say)
	} else {
		path, err = s.download(ctx, ui, say)
	}
	if err != nil {
		return err
	}
	env.SetValue("iso_path", path)

	say("Checking setup readiness...")
	py := "python3"
	if runtime.GOOS == "windows" {
		py = "python"
	}
	res, err := s.Run.Run(ctx, s.Lib.Root, py, "./.validator/validate-setup-readiness.py")
	if err != nil {
		return err
	}
	if !res.OK() {
		return fmt.Errorf("setup readiness is blocked after guided preparation:\n%s", strings.TrimSpace(res.Stdout+res.Stderr))
	}
	say("Setup readiness check passed.")
	return nil
}

func (s *Step) useExisting(ctx context.Context, ui engine.UI, found []ISO, say func(string)) (string, error) {
	chosen := found[0]
	if len(found) == 1 {
		say("Found supported installer media: " + chosen.Label())
	} else {
		labels := make([]string, len(found))
		for i, iso := range found {
			labels[i] = iso.Label()
		}
		// Several images on disk is a real choice; a silent run must name one.
		label, err := engine.Choose(ctx, ui, "media", "Choose discovered Rocky Linux installer media", labels, "")
		if err != nil {
			return "", err
		}
		for i, l := range labels {
			if l == label {
				chosen = found[i]
			}
		}
	}

	say("Verifying the existing ISO against Rocky's published SHA-256...")
	if err := s.Lib.Verify(ctx, chosen.Path); err != nil {
		return "", fmt.Errorf("the discovered Rocky installer ISO failed verification: %w", err)
	}
	say("Existing Rocky installer ISO verified. No download is needed.")

	if filepath.Dir(chosen.Path) == s.Lib.Root {
		return chosen.Path, nil
	}
	if err := engine.Approve(ctx, ui, "adopt-media", "Use this installer ISO and copy it into the Adaetum checkout?"); err != nil {
		return "", err
	}
	say("Copying " + filepath.Base(chosen.Path) + " into the checkout...")
	return s.Lib.Adopt(chosen.Path)
}

var sizes = map[string]string{
	"x86_64:minimal": "about 1.93 GiB", "aarch64:minimal": "about 2.23 GiB",
	"x86_64:dvd": "about 9.52 GiB", "aarch64:dvd": "about 9.00 GiB",
}

func (s *Step) download(ctx context.Context, ui engine.UI, say func(string)) (string, error) {
	say("No installer image found on this computer; choose one to download.")

	kind, err := s.choose(ctx, ui, "media-type", "Installer image type", []string{
		"Minimal (offline installer) — recommended, smaller download with Adaetum's required packages",
		"DVD (offline installer) — complete package repository, much larger download",
	})
	if err != nil {
		return "", err
	}
	imageType := "minimal"
	if strings.HasPrefix(kind, "DVD") {
		imageType = "dvd"
	}

	other := "aarch64"
	if s.Arch == "aarch64" {
		other = "x86_64"
	}
	archChoice, err := s.choose(ctx, ui, "media-arch", "Target machine architecture", []string{
		s.Arch + " — detected on this computer (recommended)",
		other + " — build for a different target machine",
	})
	if err != nil {
		return "", err
	}
	arch := strings.Fields(archChoice)[0]

	say(fmt.Sprintf("Rocky Linux %s %s for %s (%s).", Release, imageType, arch, sizes[arch+":"+imageType]))
	// In the window, selecting the image and target authorizes the download.
	// Headless replay keeps the explicit creation guard for large downloads.
	if r, ok := ui.(engine.StageRetrier); !ok || !r.CanRetrySteps() {
		if err := engine.ApproveCreate(ctx, ui, "download-media", "Download the official installer now?"); err != nil {
			return "", err
		}
	}

	var last time.Time
	path, err := s.Lib.Download(ctx, arch, imageType, Release, func(done, total int64) {
		if time.Since(last) < time.Second && done != total {
			return
		}
		last = time.Now()
		msg := GiB(done)
		if total > 0 {
			msg = fmt.Sprintf("%s of %s (%d%%)", GiB(done), GiB(total), done*100/total)
		}
		ui.Report(engine.Event{StepID: stepID, Kind: engine.Progress, Message: "Downloading: " + msg})
	})
	if err != nil {
		return "", err
	}
	say("Verified installer ISO: " + filepath.Base(path))
	return path, nil
}
