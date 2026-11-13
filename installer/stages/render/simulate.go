package render

import (
	"context"

	"github.com/Adaetum/Adaetum/installer/engine"
)

// Simulate draws render-stage progress and successful results without creating
// .env, syncing repository secrets, rendering manifests, or publishing commits.
func (s *Step) Simulate(_ context.Context, _ *engine.Env, ui engine.UI) error {
	for _, message := range []string{
		"Validating the scoped Tailscale OAuth clients...",
		"Single-use node enrollment key minted.",
		"Recovery password chosen and your own copy confirmed (it also becomes each server's access token).",
		"Runtime values and GitOps configuration rendered.",
		"Repository validators passed.",
		"Rendered configuration committed and published.",
	} {
		ui.Report(engine.Event{StepID: stepID, Message: message})
	}
	return nil
}
