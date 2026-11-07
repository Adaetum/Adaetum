package media

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/Adaetum/Adaetum/installer/engine"
)

// Simulate reuses the media choices and download approval, but does not discover,
// download, verify, or copy an ISO.
func (s *Step) Simulate(ctx context.Context, env *engine.Env, ui engine.UI) error {
	say := func(message string) { ui.Report(engine.Event{StepID: stepID, Message: message}) }
	kinds := []string{
		"Minimal (offline installer) — recommended, smaller download with Adaetum's required packages",
		"DVD (offline installer) — complete package repository, much larger download",
	}
	kind, err := engine.Choose(ctx, ui, "media-type", "Installer image type", kinds, kinds[0])
	if err != nil {
		return err
	}
	imageType := "minimal"
	if strings.HasPrefix(kind, "DVD") {
		imageType = "dvd"
	}
	other := "aarch64"
	if s.Arch == "aarch64" {
		other = "x86_64"
	}
	architectures := []string{s.Arch + " — detected on this computer (recommended)", other + " — build for a different target machine"}
	architectureChoice, err := engine.Choose(ctx, ui, "media-arch", "Target machine architecture", architectures, architectures[0])
	if err != nil {
		return err
	}
	arch := strings.Fields(architectureChoice)[0]
	say("Rocky Linux " + Release + " " + imageType + " installer selected for " + arch + ".")
	if err := engine.ApproveCreate(ctx, ui, "download-media", "Download the official installer now?"); err != nil {
		return err
	}
	say("Downloading the example installer image...")
	say("Download complete; Rocky's published SHA-256 matches.")
	path := filepath.Join(s.Lib.Root, "dist", "Rocky-"+Release+"-"+arch+"-"+imageType+"-demo.iso")
	env.SetValue("iso_path", path)
	say("Setup readiness check passed.")
	return nil
}
