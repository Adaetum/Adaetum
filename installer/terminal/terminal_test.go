package terminal

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Adaetum/Adaetum/installer/engine"
)

func ask(t *testing.T, input string, s engine.Screen) (string, string, error) {
	t.Helper()
	var out bytes.Buffer
	got, err := NewWith(strings.NewReader(input), &out).Ask(context.Background(), s)
	return got, out.String(), err
}

func TestAChoiceTakesANumberOrTheDefault(t *testing.T) {
	s := engine.Screen{Kind: engine.Choice, Prompt: "What?", Options: []string{"Everything", "Nothing"}, Default: "Nothing"}
	if got, _, err := ask(t, "1\n", s); err != nil || got != "Everything" {
		t.Fatalf("got %q, %v", got, err)
	}
	if got, _, err := ask(t, "\n", s); err != nil || got != "Nothing" {
		t.Fatalf("an empty answer should take the default, got %q, %v", got, err)
	}
	got, out, err := ask(t, "9\nabc\n2\n", s)
	if err != nil || got != "Nothing" || strings.Count(out, "Enter one of the numbers") != 2 {
		t.Fatalf("bad numbers should be asked again: %q %q %v", got, out, err)
	}
}

func TestAConfirmDefaultsToTheScreensDefaultAndOnlyYesIsYes(t *testing.T) {
	no := engine.Screen{Kind: engine.Confirm, Prompt: "Remove?", Default: "no"}
	for in, want := range map[string]string{"\n": "no", "y\n": "yes", "YES\n": "yes", "n\n": "no", "maybe\n": "no"} {
		if got, _, err := ask(t, in, no); err != nil || got != want {
			t.Errorf("%q: got %q, %v, want %q", in, got, err, want)
		}
	}
	yes := engine.Screen{Kind: engine.Confirm, Default: "yes"}
	if got, _, _ := ask(t, "\n", yes); got != "yes" {
		t.Errorf("a yes default should be yes on enter, got %q", got)
	}
}

func TestTextUsesItsDefault(t *testing.T) {
	s := engine.Screen{Kind: engine.Text, Prompt: "Path", Default: "/tmp/.env"}
	if got, _, _ := ask(t, "\n", s); got != "/tmp/.env" {
		t.Errorf("got %q", got)
	}
	if got, _, _ := ask(t, "/x/y\n", s); got != "/x/y" {
		t.Errorf("got %q", got)
	}
}

func TestAClosedInputStopsInsteadOfGuessing(t *testing.T) {
	_, _, err := ask(t, "", engine.Screen{Kind: engine.Confirm, Default: "yes"})
	if !errors.Is(err, engine.ErrNoAnswer) {
		t.Fatalf("err = %v, want ErrNoAnswer so a closed pipe never answers yes", err)
	}
}

func TestASecretIsReadAsALineWhenNotOnATerminal(t *testing.T) {
	got, out, err := ask(t, "tskey-abc\n", engine.Screen{Kind: engine.Secret, Prompt: "Token"})
	if err != nil || got != "tskey-abc" {
		t.Fatalf("got %q, %v", got, err)
	}
	if strings.Contains(out, "tskey-abc") {
		t.Error("the secret was echoed")
	}
}
