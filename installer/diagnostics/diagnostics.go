// Package diagnostics records setup progress without recording answers, command
// arguments, environment variables, or subprocess output that may hold secrets.
package diagnostics

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Adaetum/Adaetum/installer/engine"
	"github.com/Adaetum/Adaetum/installer/envfile"
	"github.com/Adaetum/Adaetum/installer/sys"
)

// Redact removes known runtime values from legacy script output before it is
// written to a detail log. Input and environment values are never recorded.
func Redact(text string, values ...string) string {
	for _, value := range values {
		if value != "" {
			quoted := strconv.Quote(value)
			text = strings.ReplaceAll(text, quoted[1:len(quoted)-1], "[redacted]")
			text = strings.ReplaceAll(text, value, "[redacted]")
		}
	}
	return text
}

func EnvValues(env []string) []string {
	var values []string
	for _, pair := range env {
		if key, value, ok := strings.Cut(pair, "="); ok && SensitiveKey(key) {
			values = append(values, strings.Trim(value, "\"'"))
		}
	}
	return values
}

func SensitiveKey(key string) bool {
	key = strings.ToUpper(key)
	for _, part := range []string{"TOKEN", "SECRET", "PASSWORD", "PASSPHRASE", "PRIVATE_KEY", "ACCESS_KEY", "CREDENTIAL"} {
		if strings.Contains(key, part) {
			return true
		}
	}
	return false
}

type Log struct {
	mu      sync.Mutex
	f       *os.File
	secrets []string
}

func Open(root string) (*Log, error) {
	dir := filepath.Join(root, ".logs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(dir, "installer-*.log")
	if err != nil {
		return nil, err
	}
	log := &Log{f: f}
	log.AddSecrets(EnvValues(os.Environ())...)
	for key, value := range envfile.Read(filepath.Join(root, ".env")) {
		if SensitiveKey(key) {
			log.AddSecrets(strings.Trim(value, "\"'"))
		}
	}
	log.Record("platform os=%s arch=%s go=%s", runtime.GOOS, runtime.GOARCH, runtime.Version())
	return log, nil
}
func (l *Log) Close() error { return l.f.Close() }
func (l *Log) AddSecrets(values ...string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, value := range values {
		if value != "" {
			l.secrets = append(l.secrets, value)
		}
	}
}

var tokenPattern = regexp.MustCompile(`(?i)(bearer\s+)[^\s"']+|\b(?:github_pat_|gh[opsu]_|tskey-)[A-Za-z0-9_-]+`)

func (l *Log) Record(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	message := Redact(fmt.Sprintf(format, args...), l.secrets...)
	message = tokenPattern.ReplaceAllString(message, "[redacted]")
	fmt.Fprintf(l.f, "%s %s\n", time.Now().UTC().Format(time.RFC3339Nano), message)
}

type Runner struct {
	Base sys.Runner
	Log  *Log
}

func (r Runner) done(name string, start time.Time, res sys.Result, err error) {
	if err != nil {
		r.Log.Record("command=%q duration=%s start_error=%q", name, time.Since(start), err.Error())
		return
	}
	r.Log.Record("command=%q duration=%s exit=%d", name, time.Since(start), res.ExitCode)
}
func (r Runner) Run(ctx context.Context, dir, name string, args ...string) (sys.Result, error) {
	start := time.Now()
	res, err := r.Base.Run(ctx, dir, name, args...)
	if name == "gh" && len(args) >= 2 && args[0] == "auth" && args[1] == "token" && err == nil && res.OK() {
		r.Log.AddSecrets(strings.TrimSpace(res.Stdout))
	}
	r.done(name, start, res, err)
	return res, err
}
func (r Runner) RunInput(ctx context.Context, dir, input, name string, args ...string) (sys.Result, error) {
	r.Log.AddSecrets(input)
	start := time.Now()
	res, err := r.Base.RunInput(ctx, dir, input, name, args...)
	r.done(name, start, res, err)
	return res, err
}
func (r Runner) RunEnv(ctx context.Context, dir string, env []string, input, name string, args ...string) (sys.Result, error) {
	r.Log.AddSecrets(EnvValues(env)...)
	r.Log.AddSecrets(input)
	start := time.Now()
	res, err := r.Base.RunEnv(ctx, dir, env, input, name, args...)
	r.done(name, start, res, err)
	return res, err
}
func (r Runner) RunStream(ctx context.Context, dir string, env []string, input, name string, args []string, line func(string)) (sys.Result, error) {
	r.Log.AddSecrets(EnvValues(env)...)
	start := time.Now()
	stream, ok := r.Base.(sys.Streamer)
	if !ok {
		res, err := r.Base.RunEnv(ctx, dir, env, input, name, args...)
		r.done(name, start, res, err)
		return res, err
	}
	res, err := stream.RunStream(ctx, dir, env, input, name, args, line)
	r.done(name, start, res, err)
	return res, err
}

type UI struct {
	engine.UI
	Log *Log
}

func (u UI) Ask(ctx context.Context, screen engine.Screen) (string, error) {
	u.Log.Record("screen=%q kind=%q opened", screen.ID, screen.Kind)
	start := time.Now()
	answer, err := u.UI.Ask(ctx, screen)
	if screen.Kind == engine.Secret && err == nil {
		u.Log.AddSecrets(answer)
	}
	u.Log.Record("screen=%q duration=%s answered=%t", screen.ID, time.Since(start), err == nil)
	return answer, err
}
func (u UI) Report(e engine.Event) {
	if e.Kind == engine.KindCode {
		u.Log.Record("event stage=%q kind=%q", e.StepID, e.Kind)
	} else {
		u.Log.Record("event stage=%q kind=%q message=%q", e.StepID, e.Kind, e.Message)
	}
	u.UI.Report(e)
}

type InteractiveUI struct{ UI }

func (u InteractiveUI) CanRetry() bool { v, ok := u.UI.UI.(engine.Retrier); return ok && v.CanRetry() }
func (u InteractiveUI) CanRetrySteps() bool {
	v, ok := u.UI.UI.(engine.StageRetrier)
	return ok && v.CanRetrySteps()
}

type step struct {
	engine.Step
	log *Log
}

func (s step) rememberSecrets(env *engine.Env) {
	for _, value := range env.Secrets {
		s.log.AddSecrets(value)
	}
}

func (s step) Check(ctx context.Context, env *engine.Env) (engine.Finding, error) {
	s.rememberSecrets(env)
	start := time.Now()
	f, err := s.Step.Check(ctx, env)
	s.log.Record("stage=%q check=%q detail=%q duration=%s error=%t", s.ID(), f.Status, f.Detail, time.Since(start), err != nil)
	if err != nil {
		s.log.Record("stage=%q check error=%q", s.ID(), err.Error())
	}
	return f, err
}
func (s step) Plan(ctx context.Context, env *engine.Env) ([]string, error) {
	start := time.Now()
	p, err := s.Step.Plan(ctx, env)
	s.log.Record("stage=%q plan duration=%s error=%t", s.ID(), time.Since(start), err != nil)
	if err != nil {
		s.log.Record("stage=%q plan error=%q", s.ID(), err.Error())
	}
	return p, err
}
func (s step) Apply(ctx context.Context, env *engine.Env, ui engine.UI) error {
	s.rememberSecrets(env)
	start := time.Now()
	s.log.Record("stage=%q apply started", s.ID())
	err := s.Step.Apply(ctx, env, ui)
	s.rememberSecrets(env)
	s.log.Record("stage=%q apply duration=%s error=%t", s.ID(), time.Since(start), err != nil)
	if err != nil {
		s.log.Record("stage=%q apply error=%q", s.ID(), err.Error())
	}
	return err
}
func (s step) Simulate(ctx context.Context, env *engine.Env, ui engine.UI) error {
	sim, ok := s.Step.(engine.Simulator)
	if !ok {
		return fmt.Errorf("%s: UI-only rehearsal is not implemented", s.ID())
	}
	return sim.Simulate(ctx, env, ui)
}
func Steps(in []engine.Step, log *Log) []engine.Step {
	out := make([]engine.Step, len(in))
	for i, s := range in {
		out[i] = step{Step: s, log: log}
	}
	return out
}
