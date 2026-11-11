package main

import (
	"github.com/Adaetum/Adaetum/installer/stages"
	"github.com/Adaetum/Adaetum/installer/sys"
	"os"
	"path/filepath"
	"testing"
	"time"
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

func TestEnvCandidatesPreferTheCheckoutEnvThenNewestRecoveryFile(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	downloads := filepath.Join(home, "Downloads")
	if err := os.MkdirAll(downloads, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path string, age time.Duration) {
		if err := os.WriteFile(path, []byte("X=1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		when := time.Now().Add(-age)
		if err := os.Chtimes(path, when, when); err != nil {
			t.Fatal(err)
		}
	}
	old, newer := filepath.Join(downloads, "recovery_setup-old"), filepath.Join(downloads, "recovery_setup-new")
	write(old, 48*time.Hour)
	write(newer, time.Hour)
	write(filepath.Join(downloads, "unrelated.txt"), 0)
	if got := envCandidates(root, home); len(got) != 2 || got[0] != newer || got[1] != old {
		t.Fatalf("without a .env: %v", got)
	}
	write(filepath.Join(root, ".env"), 0)
	if got := envCandidates(root, home); len(got) != 3 || got[0] != filepath.Join(root, ".env") || got[1] != newer {
		t.Fatalf("with a .env: %v", got)
	}
}
