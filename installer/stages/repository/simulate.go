package repository

import (
	"context"
	"strings"

	"github.com/Adaetum/Adaetum/installer/engine"
)

// Simulate presents the repository stage's device sign-in and private-repository
// choices without running gh or changing the checkout or its remotes.
func (s *Step) Simulate(ctx context.Context, _ *engine.Env, ui engine.UI) error {
	say := func(message string) { ui.Report(engine.Event{StepID: stepID, Message: message}) }
	say("Preparing the private recovery repository flow...")
	ui.Report(engine.Event{StepID: stepID, Kind: engine.KindCode, Message: "ADA3-7UM4", Link: "https://github.com/login/device"})
	_, err := ui.Ask(ctx, engine.Screen{
		ID: "github-signin", Kind: engine.Confirm, Default: "yes",
		Title:  "Authorize Adaetum with GitHub",
		Intro:  "Example device code only. This rehearsal does not open GitHub or contact your account.",
		Prompt: "Assume GitHub accepts the device code?", YesLabel: "Continue", NoLabel: "Cancel",
	})
	ui.Report(engine.Event{StepID: stepID, Kind: engine.KindCode})
	if err != nil {
		return err
	}
	say("GitHub identity accepted.")
	if err := engine.Approve(ctx, ui, "create-or-reuse-repository", "Create or reuse a private recovery repository now?"); err != nil {
		return err
	}
	repo, err := engine.AskText(ctx, ui, "repository", "Private recovery repository (owner/name)", "adaetum-operator/adaetum-cluster")
	if err != nil {
		return err
	}
	repo = strings.TrimSpace(repo)
	if repo == "" {
		repo = "adaetum-operator/adaetum-cluster"
	}
	if err := engine.ApproveCreate(ctx, ui, "create-repository", "Create "+repo+" as a private repository now?"); err != nil {
		return err
	}
	say("Private recovery repository connected: " + repo + ".")
	say("Adaetum upstream and workflow access confirmed.")
	return nil
}
