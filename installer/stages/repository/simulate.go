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
	for {
		say("Preparing the private recovery repository flow...")
		ui.Report(engine.Event{StepID: stepID, Kind: engine.KindCode, Message: "ADA3-7UM4", Link: "https://github.com/login/device"})
		_, err := ui.Ask(ctx, engine.Screen{
			ID: "github-signin", Kind: engine.Confirm, Default: "yes",
			Title:  "Connect your GitHub account",
			Prompt: "GitHub will continue automatically after the device code is accepted.", YesLabel: "Continue", NoLabel: "Cancel",
		})
		ui.Report(engine.Event{StepID: stepID, Kind: engine.KindCode})
		if err != nil {
			return err
		}
		say("GitHub identity accepted; continuing automatically.")
		ui.Report(engine.Event{StepID: stepID, Kind: "github-account", Message: "adaetum-operator"})
		owner, err := chooseRepositoryOwner(ctx, ui, []string{"adaetum-operator", "sanctuary-org"})
		if err != nil {
			return err
		}
		choice, err := ui.Ask(ctx, engine.Screen{ID: "create-or-reuse-repository", Kind: engine.Choice, Prompt: "How should Adaetum handle your recovery repository?", Options: []string{reuseRepository, createRepository}, Default: reuseRepository})
		if err != nil {
			return err
		}
		if choice == switchAccountAnswer {
			ui.Report(engine.Event{StepID: stepID, Kind: "github-account"})
			continue
		}
		if choice == reuseRepository {
			repo, err := engine.Choose(ctx, ui, "reuse-repository-select", "Which recovery repository would you like to use?", []string{owner + "/adaetum-cluster", owner + "/sanctuary-recovery"}, "")
			if err != nil {
				return err
			}
			say("Private recovery repository connected: " + repo + ".")
			say("Adaetum upstream and workflow access confirmed.")
			return nil
		}
		name, err := ui.Ask(ctx, engine.Screen{ID: "repository", Kind: engine.Text, Title: "Name your recovery repository", Intro: "It will be created as a private repository under " + owner + ".", Prompt: "Repository name", Default: "adaetum-cluster", YesLabel: "Create repository", Creates: true})
		if err != nil {
			return err
		}
		name = strings.TrimSpace(name)
		if name == "" {
			name = "adaetum-cluster"
		}
		repo := owner + "/" + name
		say("Private recovery repository connected: " + repo + ".")
		say("Adaetum upstream and workflow access confirmed.")
		return nil
	}
}
