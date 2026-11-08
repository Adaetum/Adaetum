package sys

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Fake answers commands from a table, for tests. A rule matches when the full
// command line starts with its Prefix; the first match wins, so list specific
// rules before general ones. An unmatched command fails the test loudly instead
// of quietly succeeding.
type Fake struct {
	mu     sync.Mutex
	Rules  []Rule
	Calls  []string
	Inputs []string   // the stdin of each call, parallel to Calls
	Envs   [][]string // the extra environment of each call, parallel to Calls
}

type Rule struct {
	Prefix string
	Result Result
	// Then, when set, replaces Result after each use, so a command can change its
	// answer over time (for example, a workflow that registers on the third poll).
	Then []Result
}

func (f *Fake) Run(ctx context.Context, dir string, name string, args ...string) (Result, error) {
	return f.RunInput(ctx, dir, "", name, args...)
}

func (f *Fake) RunInput(ctx context.Context, dir string, stdin string, name string, args ...string) (Result, error) {
	return f.RunEnv(ctx, dir, nil, stdin, name, args...)
}

func (f *Fake) RunEnv(_ context.Context, _ string, env []string, stdin string, name string, args ...string) (Result, error) {
	line := strings.Join(append([]string{name}, args...), " ")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, line)
	f.Inputs = append(f.Inputs, stdin)
	f.Envs = append(f.Envs, env)
	for i := range f.Rules {
		r := &f.Rules[i]
		if !strings.HasPrefix(line, r.Prefix) {
			continue
		}
		res := r.Result
		if len(r.Then) > 0 {
			res, r.Then = r.Then[0], r.Then[1:]
		}
		return res, nil
	}
	return Result{}, fmt.Errorf("fake runner: no rule for %q", line)
}

// Ran reports whether any recorded command line starts with prefix.
func (f *Fake) Ran(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.Calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// Mux sends the named programs to Real and everything else to Fake. Tests use it
// to run git against real temporary repositories while faking gh and the Python
// scripts, which is where a mistake would otherwise lose someone's work.
type Mux struct {
	Real     Runner
	RealOnly []string
	Fake     *Fake
}

func (m Mux) Run(ctx context.Context, dir, name string, args ...string) (Result, error) {
	return m.RunInput(ctx, dir, "", name, args...)
}

func (m Mux) RunInput(ctx context.Context, dir, stdin, name string, args ...string) (Result, error) {
	return m.RunEnv(ctx, dir, nil, stdin, name, args...)
}

func (m Mux) RunEnv(ctx context.Context, dir string, env []string, stdin, name string, args ...string) (Result, error) {
	for _, n := range m.RealOnly {
		if n == name {
			return m.Real.RunEnv(ctx, dir, env, stdin, name, args...)
		}
	}
	return m.Fake.RunEnv(ctx, dir, env, stdin, name, args...)
}

// RunStream plays the rule's Stdout and Stderr as lines to onLine, then returns the
// result, so tests can exercise code that reacts to a program's live output.
func (f *Fake) RunStream(ctx context.Context, dir string, env []string, stdin, name string, args []string, onLine func(string)) (Result, error) {
	res, err := f.RunEnv(ctx, dir, env, stdin, name, args...)
	if err != nil {
		return res, err
	}
	if onLine != nil {
		for _, text := range []string{res.Stdout, res.Stderr} {
			for _, line := range strings.Split(text, "\n") {
				if line != "" {
					onLine(line)
				}
			}
		}
	}
	return res, nil
}

func (m Mux) RunStream(ctx context.Context, dir string, env []string, stdin, name string, args []string, onLine func(string)) (Result, error) {
	for _, n := range m.RealOnly {
		if n == name {
			if s, ok := m.Real.(Streamer); ok {
				return s.RunStream(ctx, dir, env, stdin, name, args, onLine)
			}
		}
	}
	return m.Fake.RunStream(ctx, dir, env, stdin, name, args, onLine)
}
