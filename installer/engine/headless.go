package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

// ErrNotReplayable is returned to a replay run that reaches a question whose yes
// would create something new. Rerunning setup must reuse what exists, not make more.
var ErrNotReplayable = errors.New("a replay run will not create this on its own; run setup normally, or answer it explicitly")

// SecretVar names the environment variable that supplies a secret screen's answer.
func SecretVar(id string) string {
	return "ADAETUM_SECRET_" + strings.ToUpper(strings.ReplaceAll(id, "-", "_"))
}

// Headless answers screens from supplied values, then defaults, then fails. It
// is the front end for CI, reruns and machines with no display.
//
// Secrets are never taken from Answers (which come from the command line, where
// any user of the machine can read them): a secret screen is answered only from
// its environment variable, see SecretVar.
type Headless struct {
	Answers map[string]string
	Log     func(Event)
	// Replay makes the run reuse existing state only: questions that would create
	// something new fail unless answered explicitly.
	Replay bool
	// Getenv reads the environment; tests replace it. Nil means os.Getenv.
	Getenv func(string) string
}

func (h Headless) env(key string) string {
	if h.Getenv != nil {
		return h.Getenv(key)
	}
	return os.Getenv(key)
}

func (h Headless) Ask(_ context.Context, s Screen) (string, error) {
	if s.Kind == Secret {
		if _, given := h.Answers[s.ID]; given {
			return "", fmt.Errorf("%s: a secret cannot be given with --answer, because command lines are visible to other users; set %s in the environment instead", s.ID, SecretVar(s.ID))
		}
		if v := h.env(SecretVar(s.ID)); v != "" {
			return v, nil
		}
		return "", fmt.Errorf("%s: %w (set %s)", s.ID, ErrNoAnswer, SecretVar(s.ID))
	}
	if v, ok := h.Answers[s.ID]; ok {
		return v, nil
	}
	if h.Replay && s.Creates {
		return "", fmt.Errorf("%s: %w", s.ID, ErrNotReplayable)
	}
	if s.Default != "" {
		return s.Default, nil
	}
	return "", fmt.Errorf("%s: %w", s.ID, ErrNoAnswer)
}

func (h Headless) Report(e Event) {
	if h.Log != nil {
		h.Log(e)
	}
}
