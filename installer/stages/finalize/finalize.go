// Package finalize is the last setup stage: confirm the built installer ISO is
// there and newer than everything it was built from, tell the operator what to do
// with it, and offer a copy in their Downloads folder. It ports "Finalize
// installer" from tasks/scripts/run-opinionated-setup.sh.
package finalize

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Adaetum/Adaetum/installer/engine"
)

const stepID = "finalize"

// Step is the finalize stage.
type Step struct {
	Root string
	// DownloadDir is where the optional copy goes. New sets ~/Downloads, or
	// ADAETUM_INSTALLER_DOWNLOAD_DIR when that is set.
	DownloadDir string
}

// New returns the stage for the checkout at root.
func New(root, home string) *Step {
	dir := os.Getenv("ADAETUM_INSTALLER_DOWNLOAD_DIR")
	if dir == "" {
		dir = filepath.Join(home, "Downloads")
	}
	return &Step{Root: root, DownloadDir: dir}
}

func (s *Step) ID() string    { return stepID }
func (s *Step) Title() string { return "Finalize installer" }

// OutputFor names the installer built from a source ISO: dist/<name>-ks.iso.
func (s *Step) OutputFor(source string) string {
	base := filepath.Base(source)
	return filepath.Join(s.Root, "dist", strings.TrimSuffix(base, ".iso")+"-ks.iso")
}

func (s *Step) Check(context.Context, *engine.Env) (engine.Finding, error) {
	return engine.Finding{Status: engine.Needed, Detail: "runs after the installer is built and published"}, nil
}

func (s *Step) Plan(ctx context.Context, env *engine.Env) ([]string, error) {
	return []string{
		"Confirm the machine installer ISO was built and is newer than the configuration it embeds.",
		"Show how to use it: attach it to the target machine or VM, boot once, and detach it when the installer reboots.",
		"Offer to save a copy in your Downloads folder.",
	}, nil
}

// newestInput is the most recently changed file the installer is built from.
func (s *Step) newestInput() (string, error) {
	var newest string
	var newestTime int64
	for _, rel := range []string{".env", "pods/cluster-config/cluster-config.env", "platform.yaml", "dist/ks-templates/rocky10.ks"} {
		info, err := os.Stat(filepath.Join(s.Root, rel))
		if err != nil {
			continue
		}
		if t := info.ModTime().UnixNano(); t > newestTime {
			newest, newestTime = rel, t
		}
	}
	return newest, nil
}

func (s *Step) Apply(ctx context.Context, env *engine.Env, ui engine.UI) error {
	say := func(msg string) { ui.Report(engine.Event{StepID: stepID, Message: msg}) }

	source := env.Value("iso_path")
	if source == "" {
		return errors.New("the source installer ISO is unknown; run setup from the start")
	}
	out := s.OutputFor(source)
	info, err := os.Stat(out)
	if err != nil {
		return fmt.Errorf("the built installer ISO is missing: %s", out)
	}
	if newest, _ := s.newestInput(); newest != "" {
		in, _ := os.Stat(filepath.Join(s.Root, newest))
		if info.ModTime().Before(in.ModTime()) {
			return fmt.Errorf("the installer ISO is stale: %s is older than %s; rebuild it", out, newest)
		}
	}
	env.SetValue("installer_iso", out)
	say("Installer ISO built: " + filepath.Base(out))

	dest := filepath.Join(s.DownloadDir, filepath.Base(out))
	err = engine.ApproveScreen(ctx, ui, engine.Screen{
		ID: "download-installer", Title: "Your installer is ready",
		Intro: out,
		Steps: []engine.Instruction{
			{Text: "Attach this ISO to your target machine or VM, and boot from it once"},
			{Text: "The Rocky Linux install then runs on its own"},
			{Text: "Detach the ISO when it reboots. First boot continues by itself (about 30 minutes)"},
		},
		Prompt:   "Save a copy to " + s.DownloadDir + "?",
		YesLabel: "Save a copy", NoLabel: "Not now",
	})
	if errors.Is(err, engine.ErrDeclined) {
		say("The installer stays at " + out + ".")
		return nil
	}
	if err != nil {
		return err
	}
	return s.copyDown(out, dest, say)
}

// copyDown copies the ISO into place through a partial file, so a copy that is
// interrupted never leaves a truncated installer under the real name. A same-named
// older copy is replaced: it is an Adaetum-owned derived file, and keeping a stale
// one could run obsolete embedded bootstrap code.
func (s *Step) copyDown(src, dest string, say func(string)) error {
	if same, _ := sameFile(src, dest); same {
		say("The current installer is already in " + s.DownloadDir + ".")
		return nil
	}
	if _, err := os.Stat(dest); err == nil {
		say("Replacing the older installer in " + s.DownloadDir + ".")
	}
	if err := os.MkdirAll(s.DownloadDir, 0o755); err != nil {
		return fmt.Errorf("unable to create the installer download directory: %w", err)
	}
	partial := dest + ".partial"
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(partial)
		return fmt.Errorf("unable to copy the installer ISO: %w", err)
	}
	if err := out.Close(); err != nil {
		os.Remove(partial)
		return err
	}
	if err := os.Rename(partial, dest); err != nil {
		os.Remove(partial)
		return fmt.Errorf("unable to finish the installer copy: %w", err)
	}
	say("Installer saved to " + dest + ".")
	return nil
}

func sameFile(a, b string) (bool, error) {
	ia, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	ib, err := os.Stat(b)
	if err != nil || ia.Size() != ib.Size() {
		return false, nil
	}
	fa, err := os.Open(a)
	if err != nil {
		return false, err
	}
	defer fa.Close()
	fb, err := os.Open(b)
	if err != nil {
		return false, err
	}
	defer fb.Close()
	bufA, bufB := make([]byte, 1<<20), make([]byte, 1<<20)
	for {
		na, ea := io.ReadFull(fa, bufA)
		nb, eb := io.ReadFull(fb, bufB)
		if na != nb || !bytes.Equal(bufA[:na], bufB[:nb]) {
			return false, nil
		}
		if ea != nil || eb != nil {
			return ea == eb || (ea != nil && eb != nil), nil
		}
	}
}
