package finalize

import (
	"context"

	"github.com/Adaetum/Adaetum/installer/engine"
)

// Simulate presents the final ISO handoff and optional Downloads choice without
// looking for an ISO or copying anything.
func (s *Step) Simulate(ctx context.Context, env *engine.Env, ui engine.UI) error {
	ui.Report(engine.Event{StepID: stepID, Message: "Confirming the example installer ISO is ready..."})
	source := env.Value("iso_path")
	if source == "" {
		source = "example/Rocky-demo.iso"
	}
	out := s.OutputFor(source)
	env.SetValue("installer_iso", out)
	answer, err := ui.Ask(ctx, engine.Screen{
		ID: "download-installer", Kind: engine.Confirm, Default: "no",
		Title: "Your installer is ready", Intro: out,
		Steps: []engine.Instruction{
			{Text: "Attach this ISO to your target machine or VM, and boot from it once"},
			{Text: "The Rocky Linux install then runs on its own"},
			{Text: "Detach the ISO when it reboots. First boot continues by itself (about 30 minutes)"},
		},
		Prompt:   "Save a copy to " + s.DownloadDir + "?",
		YesLabel: "Save a copy", NoLabel: "Not now",
	})
	if err != nil {
		return err
	}
	if answer == "yes" || answer == "y" || answer == "true" || answer == "Y" || answer == "YES" || answer == "Yes" {
		ui.Report(engine.Event{StepID: stepID, Message: "Downloads copy selected for the rehearsal; no file was copied."})
	} else {
		ui.Report(engine.Event{StepID: stepID, Message: "The example installer remains in the rehearsal handoff; no file was copied."})
	}
	return nil
}
