package main

import (
	"github.com/Adaetum/Adaetum/installer/stages"
	"github.com/Adaetum/Adaetum/installer/sys"
	"testing"
)

func TestTargetedPublicationSelectsRecoveryRepositoryFirst(t *testing.T) {
	steps, err := chooseStages(stages.ISO("", &sys.Fake{}), "profile,render")
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 3 || steps[0].ID() != "repository" || steps[1].ID() != "profile" || steps[2].ID() != "render" {
		t.Fatalf("unexpected stages: %v", steps)
	}
}
