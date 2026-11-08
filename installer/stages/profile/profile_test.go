package profile

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Adaetum/Adaetum/installer/engine"
	"github.com/Adaetum/Adaetum/installer/platform"
	"github.com/Adaetum/Adaetum/installer/stages/providers"
	"github.com/Adaetum/Adaetum/installer/sys"
)

func ok(out string) sys.Result { return sys.Result{Stdout: out} }

type rehearsalUI struct{ screens []engine.Screen }

func (u *rehearsalUI) Report(engine.Event) {}
func (u *rehearsalUI) Ask(_ context.Context, screen engine.Screen) (string, error) {
	u.screens = append(u.screens, screen)
	if len(screen.Options) > 0 {
		return screen.Options[0], nil
	}
	if screen.Default != "" {
		return screen.Default, nil
	}
	return "yes", nil
}

func TestRehearsalCarriesProviderSelectionsWithoutRepeatingQuestions(t *testing.T) {
	env := &engine.Env{UIOnly: true}
	ui := &rehearsalUI{}
	if err := providers.New(&sys.Fake{}, "").Simulate(context.Background(), env, ui); err != nil {
		t.Fatal(err)
	}
	if err := (&Step{}).Simulate(context.Background(), env, ui); err != nil {
		t.Fatal(err)
	}
	if env.Value("domain") != "example.com" || env.Value("overlay_domain") != "adaetum-demo.ts.net" {
		t.Fatalf("provider values: %v", env.Values)
	}
	for _, screen := range ui.screens {
		if screen.ID == "domain" || screen.ID == "overlay-domain" || screen.ID == "apply-profile" || screen.ID == "tailscale-tailnet" {
			t.Fatalf("asked again for %s", screen.ID)
		}
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func isolate(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	for k, v := range map[string]string{
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.com", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.com",
		"GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_NOSYSTEM": "1", "GIT_TERMINAL_PROMPT": "0",
	} {
		t.Setenv(k, v)
	}
}

// repo returns a checkout with a published main and a template platform.yaml.
func repo(t *testing.T) (work, origin string) {
	t.Helper()
	isolate(t)
	root := t.TempDir()
	origin, work = filepath.Join(root, "origin.git"), filepath.Join(root, "work")
	git(t, root, "init", "--bare", "-b", "main", origin)
	git(t, root, "clone", origin, work)
	os.WriteFile(filepath.Join(work, "platform.yaml"), []byte("domain: adaetum.invalid\n"), 0o644)
	os.WriteFile(filepath.Join(work, "generated.env"), []byte("old\n"), 0o644)
	os.WriteFile(filepath.Join(work, "unrelated.txt"), []byte("mine\n"), 0o644)
	git(t, work, "add", ".")
	git(t, work, "commit", "-m", "start")
	git(t, work, "push", "origin", "HEAD:refs/heads/main")
	return work, origin
}

const placeholder = "domain=adaetum.invalid\noverlay_domain=example-tailnet.ts.net\n"

func step(t *testing.T, work string, f *sys.Fake) *Step {
	s := New(sys.Mux{Real: sys.Exec{}, RealOnly: []string{"git"}, Fake: f}, work)
	s.Propose = func(_ context.Context, out string, p platform.Proposal) error {
		return os.WriteFile(out, []byte("domain: "+p.Domain+"\noverlay: "+p.OverlayDomain+"\n"), 0o644)
	}
	s.Render = func(context.Context) error {
		return os.WriteFile(filepath.Join(work, "generated.env"), []byte("rendered\n"), 0o644)
	}
	return s
}

func rules(extra ...sys.Rule) *sys.Fake {
	base := []sys.Rule{
		{Prefix: "python3 --version", Result: ok("Python 3")},
		{Prefix: "python3 ./tasks/scripts/configure-platform-profile.py --show", Result: ok(placeholder)},
		{Prefix: "prek --version", Result: ok("prek 1")},
		{Prefix: "prek run --config .pre-commit-config.yaml --all-files", Result: ok("passed")},
	}
	return &sys.Fake{Rules: append(extra, base...)}
}

var answers = map[string]string{"domain": "Example.com", "overlay-domain": "tail1234.ts.net"}

func TestApplyWritesRendersCommitsAndPublishesTheProfile(t *testing.T) {
	work, origin := repo(t)
	s := step(t, work, rules())
	before := git(t, work, "rev-list", "--count", "HEAD")

	if err := s.Apply(context.Background(), &engine.Env{}, engine.Headless{Answers: answers}); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(filepath.Join(work, "platform.yaml")); info.Mode().Perm() != 0o644 {
		t.Errorf("platform.yaml is %v, want 0644 (it must not inherit the private temp file's mode)", info.Mode().Perm())
	}
	data, _ := os.ReadFile(filepath.Join(work, "platform.yaml"))
	if !strings.Contains(string(data), "domain: example.com") {
		t.Errorf("platform.yaml = %q (the domain should be lower-cased)", data)
	}
	if got := git(t, work, "log", "-1", "--format=%s"); got != "Configure Adaetum platform profile" {
		t.Errorf("commit message %q", got)
	}
	if after := git(t, work, "rev-list", "--count", "HEAD"); after == before {
		t.Error("nothing was committed")
	}
	if local, remote := git(t, work, "rev-parse", "HEAD"), git(t, origin, "rev-parse", "refs/heads/main"); local != remote {
		t.Errorf("origin main %s != HEAD %s", remote, local)
	}
	if got := git(t, work, "show", "HEAD:generated.env"); got != "rendered" {
		t.Errorf("the rendered output was not committed: %q", got)
	}
}

func TestAFailedCommitCheckStopsBeforeCommitting(t *testing.T) {
	work, _ := repo(t)
	f := rules(sys.Rule{Prefix: "prek run", Result: sys.Result{ExitCode: 1, Stdout: "check-yaml failed"}})
	s := step(t, work, f)
	head := git(t, work, "rev-parse", "HEAD")

	err := s.Apply(context.Background(), &engine.Env{}, engine.Headless{Answers: answers})
	if err == nil || !strings.Contains(err.Error(), "check-yaml failed") {
		t.Fatalf("err = %v", err)
	}
	if git(t, work, "rev-parse", "HEAD") != head {
		t.Error("a commit was made despite failing checks")
	}
}

func TestUnrelatedTrackedEditsAreNeverAbsorbed(t *testing.T) {
	work, _ := repo(t)
	os.WriteFile(filepath.Join(work, "unrelated.txt"), []byte("half-finished edit\n"), 0o644)
	// Not resumable: the python check says this is not a rendered profile.
	f := rules(sys.Rule{Prefix: "python3 -c", Result: sys.Result{ExitCode: 1}})
	err := step(t, work, f).Apply(context.Background(), &engine.Env{}, engine.Headless{Answers: answers})
	if err == nil || !strings.Contains(err.Error(), "commit or stash") {
		t.Fatalf("err = %v", err)
	}
	if got := git(t, work, "log", "-1", "--format=%s"); got != "start" {
		t.Errorf("a commit was made: %q", got)
	}
}

func TestMissingHookRunnerIsAnError(t *testing.T) {
	work, _ := repo(t)
	f := rules()
	f.Rules = append([]sys.Rule{
		{Prefix: "prek --version", Result: sys.Result{ExitCode: 127}},
		{Prefix: "pre-commit --version", Result: sys.Result{ExitCode: 127}},
	}, f.Rules...)
	if err := step(t, work, f).Apply(context.Background(), &engine.Env{}, engine.Headless{Answers: answers}); err == nil {
		t.Fatal("expected an error when no hook runner is installed")
	}
}

func TestADomainWithoutADotIsRefused(t *testing.T) {
	work, _ := repo(t)
	err := step(t, work, rules()).Apply(context.Background(), &engine.Env{}, engine.Headless{Answers: map[string]string{"domain": "localhost", "overlay-domain": "x.ts.net"}})
	if err == nil {
		t.Fatal("a bare hostname was accepted as a public domain")
	}
}

func TestSharedChoicesSkipTheQuestions(t *testing.T) {
	work, _ := repo(t)
	env := &engine.Env{Values: map[string]string{"domain": "example.com", "overlay_domain": "tail9.ts.net"}}
	// No answers at all: only the default-yes approval is asked.
	if err := step(t, work, rules()).Apply(context.Background(), env, engine.Headless{}); err != nil {
		t.Fatal(err)
	}
}

func TestCheckNeedsWorkWhileThePlaceholderRemains(t *testing.T) {
	work, _ := repo(t)
	got, err := step(t, work, rules()).Check(context.Background(), &engine.Env{})
	if err != nil || got.Status != engine.Needed {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestCheckIsDoneWhenConfiguredCleanAndPublished(t *testing.T) {
	work, _ := repo(t)
	f := rules()
	f.Rules = append([]sys.Rule{{Prefix: "python3 ./tasks/scripts/configure-platform-profile.py --show",
		Result: ok("domain=example.com\noverlay_domain=tail1.ts.net\n")}}, f.Rules...)
	got, err := step(t, work, f).Check(context.Background(), &engine.Env{})
	if err != nil || got.Status != engine.Done {
		t.Fatalf("%+v %v", got, err)
	}
}
