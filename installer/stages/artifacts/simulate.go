package artifacts

import (
	"context"

	"github.com/Adaetum/Adaetum/installer/engine"
)

// Simulate reports a successful artifact pipeline without uploading files,
// syncing secrets, dispatching workflows, or
// building an ISO.
func (s *Step) Simulate(ctx context.Context, _ *engine.Env, ui engine.UI) error {
	for _, message := range []string{
		"Installer images uploaded to the example R2 bucket.",
		"Break-glass bundle built and published.",
		"Required secrets synchronized to the private repository.",
		"ks-worker, ks-publish, and iso-build workflows completed successfully.",
		"Local installer ISO build completed.",
	} {
		ui.Report(engine.Event{StepID: stepID, Message: message})
	}
	return nil
}
