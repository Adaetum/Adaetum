package diagnostics

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Adaetum/Adaetum/installer/engine"
	"github.com/Adaetum/Adaetum/installer/sys"
)

type testUI struct{}

func (testUI) Ask(context.Context, engine.Screen) (string, error) { return "sensitive-answer", nil }
func (testUI) Report(engine.Event)                                {}
func (testUI) CanRetry() bool                                     { return true }
func (testUI) CanRetrySteps() bool                                { return true }

func TestTraceOmitsSecretsAndPreservesUI(t *testing.T) {
	root := t.TempDir()
	log, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	ui := InteractiveUI{UI: UI{UI: testUI{}, Log: log}}
	if !ui.CanRetry() || !ui.CanRetrySteps() {
		t.Fatal("retry behavior lost")
	}
	if _, err := ui.Ask(context.Background(), engine.Screen{ID: "token", Kind: engine.Secret}); err != nil {
		t.Fatal(err)
	}
	ui.Report(engine.Event{StepID: "repository", Kind: engine.KindCode, Message: "DEVICE-CODE"})
	ui.Report(engine.Event{StepID: "providers", Message: "failed with sensitive-answer"})
	runner := Runner{Base: sys.Exec{}, Log: log}
	_, err = runner.Run(context.Background(), root, "git", "--version", "sensitive-argument")
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(root, ".logs", "installer-*.log"))
	if err != nil || len(files) != 1 {
		t.Fatalf("logs: %v %v", files, err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, secret := range []string{"sensitive-answer", "DEVICE-CODE", "sensitive-argument"} {
		if strings.Contains(text, secret) {
			t.Fatalf("trace contains %q", secret)
		}
	}
	if !strings.Contains(text, "command=\"git\"") || !strings.Contains(text, "screen=\"token\"") {
		t.Fatalf("missing trace details: %s", text)
	}
	if _, ok := any(UI{UI: testUI{}, Log: log}).(engine.StageRetrier); ok {
		t.Fatal("headless wrapper became interactive")
	}
}

func TestRedactKnownSecrets(t *testing.T) {
	values := EnvValues([]string{"GITHUB_TOKEN=abc", "DOMAIN=example.org", "R2_ACCESS_KEY_ID='key123'"})
	got := Redact("abc key123 example.org", values...)
	if got != "[redacted] [redacted] example.org" {
		t.Fatal(got)
	}
}
