package validate

import (
	"context"

	"github.com/Adaetum/Adaetum/installer/engine"
)

// Simulate shows the validation checklist as passing without calling providers,
// reading generated files, or checking for an ISO on disk.
func (s *Step) Simulate(_ context.Context, _ *engine.Env, ui engine.UI) error {
	for _, message := range []string{
		"Validating the reviewed platform profile...",
		"Cloudflare, GitHub, and Tailscale access accepted.",
		"Installer media accepted.",
		"All setup validation checks passed.",
	} {
		ui.Report(engine.Event{StepID: stepID, Message: message})
	}
	return nil
}
