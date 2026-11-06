package repository

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Adaetum/Adaetum/installer/engine"
	"github.com/Adaetum/Adaetum/installer/sys"
)

func meta(visibility string, admin, fork bool, size int, branch string) string {
	return fmt.Sprintf(`{"full_name":"x","visibility":%q,"fork":%t,"size":%d,"default_branch":%q,"permissions":{"admin":%t}}`,
		visibility, fork, size, branch, admin)
}

var (
	notFound = sys.Result{ExitCode: 1, Stderr: "gh: Not Found (HTTP 404)"}
	okEmpty  = sys.Result{}
)

func ok(out string) sys.Result { return sys.Result{Stdout: out} }

func newStep(f *sys.Fake) *Step {
	s := New(f, "/checkout")
	s.Sleep = func(time.Duration) {}
	s.Render = func(context.Context) error { return nil }
	return s
}

func TestCheckIsBlockedWithoutGh(t *testing.T) {
	f := &sys.Fake{Rules: []sys.Rule{
		{Prefix: "git --version", Result: ok("git version 2")},
		{Prefix: "gh --version", Result: sys.Result{ExitCode: 127}},
	}}
	got, err := newStep(f).Check(context.Background(), &engine.Env{})
	if err != nil || got.Status != engine.Blocked {
		t.Fatalf("got %+v err=%v", got, err)
	}
}

func TestCheckNeedsARepositoryWhenOriginIsUpstream(t *testing.T) {
	f := &sys.Fake{Rules: []sys.Rule{
		{Prefix: "git --version", Result: ok("")},
		{Prefix: "gh --version", Result: ok("")},
		{Prefix: "git remote get-url origin", Result: ok("https://github.com/Adaetum/Adaetum.git\n")},
	}}
	got, _ := newStep(f).Check(context.Background(), &engine.Env{})
	if got.Status != engine.Needed {
		t.Fatalf("got %+v", got)
	}
}

func TestCheckIsDoneWhenEverythingIsInPlace(t *testing.T) {
	f := &sys.Fake{Rules: []sys.Rule{
		{Prefix: "git --version", Result: ok("")},
		{Prefix: "gh --version", Result: ok("")},
		{Prefix: "git remote get-url origin", Result: ok("https://github.com/alice/Adaetum-cluster.git\n")},
		{Prefix: "gh auth token", Result: ok("token")},
		{Prefix: "gh api repos/alice/adaetum-cluster/actions/workflows", Result: ok("2\n")},
		{Prefix: "gh api repos/alice/adaetum-cluster", Result: ok(meta("private", true, false, 0, "main"))},
		{Prefix: "git branch --show-current", Result: ok("main\n")},
		{Prefix: "git rev-parse HEAD", Result: ok("abc\n")},
		{Prefix: "git ls-remote --heads origin main", Result: ok("abc\trefs/heads/main\n")},
	}}
	got, err := newStep(f).Check(context.Background(), &engine.Env{})
	if err != nil || got.Status != engine.Done {
		t.Fatalf("got %+v err=%v", got, err)
	}
}

func TestAPublicOriginIsNeverUsable(t *testing.T) {
	f := &sys.Fake{Rules: []sys.Rule{}}
	s := newStep(f)
	for name, m := range map[string]repoMeta{
		"public": {Visibility: "public", Permissions: struct {
			Admin bool `json:"admin"`
		}{true}},
		"fork": {Visibility: "private", Fork: true, Permissions: struct {
			Admin bool `json:"admin"`
		}{true}},
		"not admin": {Visibility: "private"},
	} {
		if usable, _ := s.usable(context.Background(), "alice/x", m); usable {
			t.Errorf("%s repository was accepted", name)
		}
	}
}

func TestAPopulatedRepositoryWithoutTheMarkerIsRefused(t *testing.T) {
	f := &sys.Fake{Rules: []sys.Rule{{Prefix: "gh api repos/alice/x/contents/platform.yaml", Result: notFound}}}
	m := repoMeta{Visibility: "private", Size: 10}
	m.Permissions.Admin = true
	if usable, why := newStep(f).usable(context.Background(), "alice/x", m); usable || !strings.Contains(why, "does not look like Adaetum") {
		t.Fatalf("usable=%v why=%q", usable, why)
	}
}

func TestACollidingNameGetsTheNextFreeSuffix(t *testing.T) {
	f := &sys.Fake{Rules: []sys.Rule{
		{Prefix: "gh api repos/alice/Adaetum-cluster-2", Result: notFound},
		{Prefix: "gh api repos/alice/Adaetum-cluster/contents/platform.yaml", Result: notFound},
		{Prefix: "gh api repos/alice/Adaetum-cluster", Result: ok(meta("public", true, false, 5, "main"))},
	}}
	got, err := newStep(f).findPreferred(context.Background(), "alice")
	if err != nil || got != "alice/Adaetum-cluster-2" {
		t.Fatalf("got %q err=%v", got, err)
	}
}

func TestAnExistingRecoveryRepositoryIsPreferred(t *testing.T) {
	f := &sys.Fake{Rules: []sys.Rule{{Prefix: "gh api repos/alice/Adaetum-cluster", Result: ok(meta("private", true, false, 0, "main"))}}}
	got, err := newStep(f).findPreferred(context.Background(), "alice")
	if err != nil || got != "alice/Adaetum-cluster" {
		t.Fatalf("got %q err=%v", got, err)
	}
}

func TestAnOutageIsNotMistakenForAFreeName(t *testing.T) {
	f := &sys.Fake{Rules: []sys.Rule{{Prefix: "gh api repos/", Result: sys.Result{ExitCode: 1, Stderr: "HTTP 503 invalid character '<'"}}}}
	if _, _, err := newStep(f).lookup(context.Background(), "alice/x"); err == nil {
		t.Fatal("an outage was treated as a free name")
	}
}

// plainRunner hides the fake's streaming, so the terminal fallback is exercised.
type plainRunner struct{ sys.Runner }

func TestSignInFallsBackToTerminalInstructionsWhenGhCannotBeDrivenInTheWindow(t *testing.T) {
	f := &sys.Fake{Rules: []sys.Rule{
		{Prefix: "gh auth token", Result: sys.Result{ExitCode: 1}, Then: []sys.Result{{ExitCode: 1}, ok("token")}},
		{Prefix: "gh api user", Result: ok("alice\n")},
	}}
	step := newStep(f)
	step.Run = plainRunner{f}
	login, err := step.signIn(context.Background(), engine.Headless{}, func(string) {})
	if err != nil || login != "alice" {
		t.Fatalf("login=%q err=%v", login, err)
	}
	if f.Ran("gh auth login") {
		t.Fatal("without streaming the installer must not run gh's sign-in blind")
	}
}

// reporter collects the events a step reports.
type reporter struct {
	engine.Headless
	events []engine.Event
}

func (r *reporter) Report(e engine.Event) { r.events = append(r.events, e) }

func TestSignInRunsGhInTheWindowAndShowsTheOneTimeCode(t *testing.T) {
	f := &sys.Fake{Rules: []sys.Rule{
		{Prefix: "gh auth token", Result: sys.Result{ExitCode: 1}, Then: []sys.Result{{ExitCode: 1}, ok("token")}},
		{Prefix: "gh auth login --hostname github.com --web", Result: sys.Result{Stderr: "! First copy your one-time code: ABCD-1234\n- Press Enter to open github.com in your browser...\n"}},
		{Prefix: "gh api user", Result: ok("alice\n")},
	}}
	ui := &reporter{}
	login, err := newStep(f).signIn(context.Background(), ui, func(string) {})
	if err != nil || login != "alice" {
		t.Fatalf("login=%q err=%v", login, err)
	}
	var shown *engine.Event
	dismissed := false
	for i := range ui.events {
		if ui.events[i].Kind != engine.KindCode {
			continue
		}
		if ui.events[i].Message != "" && shown == nil {
			shown = &ui.events[i]
		}
		if ui.events[i].Message == "" {
			dismissed = true
		}
	}
	if shown == nil || shown.Message != "ABCD-1234" || !strings.Contains(shown.Link, "github.com/login/device") {
		t.Fatalf("the one-time code was not shown: %+v", ui.events)
	}
	if !dismissed {
		t.Fatal("the code was never taken off the screen after sign-in")
	}
}

func TestSignInRefreshesInTheWindowWhenGitHubRejectsTheSavedLogin(t *testing.T) {
	f := &sys.Fake{Rules: []sys.Rule{
		{Prefix: "gh auth token", Result: ok("token")},
		{Prefix: "gh auth refresh --hostname github.com", Result: sys.Result{Stderr: "! First copy your one-time code: WXYZ-9876\n"}},
		{Prefix: "gh api user", Result: sys.Result{ExitCode: 1, Stderr: "HTTP 401 Bad credentials"}, Then: []sys.Result{
			{ExitCode: 1, Stderr: "HTTP 401 Bad credentials"}, ok("alice\n")}},
	}}
	ui := &reporter{}
	if _, err := newStep(f).signIn(context.Background(), ui, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if !f.Ran("gh auth refresh") {
		t.Fatal("the saved login was not refreshed")
	}
}

func TestSignInRefreshesRejectedCredentials(t *testing.T) {
	f := &sys.Fake{Rules: []sys.Rule{
		{Prefix: "gh auth token", Result: ok("token")},
		{Prefix: "gh api user", Result: sys.Result{ExitCode: 1, Stderr: "HTTP 401 Bad credentials"}, Then: []sys.Result{
			{ExitCode: 1, Stderr: "HTTP 401 Bad credentials"}, ok("alice\n")}},
	}}
	login, err := newStep(f).signIn(context.Background(), engine.Headless{}, func(string) {})
	if err != nil || login != "alice" {
		t.Fatalf("login=%q err=%v", login, err)
	}
}

func TestDecliningStopsCleanly(t *testing.T) {
	f := &sys.Fake{Rules: []sys.Rule{{Prefix: "gh auth token", Result: sys.Result{ExitCode: 1}}}}
	_, err := newStep(f).signIn(context.Background(), engine.Headless{Answers: map[string]string{"github-signin": "no"}}, func(string) {})
	if !errors.Is(err, engine.ErrDeclined) {
		t.Fatalf("got %v", err)
	}
}

func TestActionsRetriesUntilWorkflowsAppear(t *testing.T) {
	f := &sys.Fake{Rules: []sys.Rule{
		{Prefix: "gh api repos/alice/x/actions/workflows", Result: ok("0"), Then: []sys.Result{ok("0"), ok("0"), ok("3")}},
		{Prefix: "gh api repos/alice/x", Result: ok(meta("private", true, false, 1, "main"))},
	}}
	if err := newStep(f).ensureActions(context.Background(), "alice/x", func(string) {}); err != nil {
		t.Fatal(err)
	}
}

func TestActionsFixesTheDefaultBranch(t *testing.T) {
	f := &sys.Fake{Rules: []sys.Rule{
		{Prefix: "gh api repos/alice/x/actions/workflows", Result: ok("1")},
		{Prefix: "gh api repos/alice/x/branches/main", Result: ok("{}")},
		{Prefix: "gh repo edit alice/x --default-branch main", Result: okEmpty},
		{Prefix: "gh api repos/alice/x", Result: ok(meta("private", true, false, 1, "develop"))},
	}}
	if err := newStep(f).ensureActions(context.Background(), "alice/x", func(string) {}); err != nil {
		t.Fatal(err)
	}
	if !f.Ran("gh repo edit alice/x --default-branch main") {
		t.Fatal("the default branch was not corrected")
	}
}

// ---- end to end with real git and a fake gh ---------------------------------

func sh(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestApplyPublishesThePublicTreeWithTheOperatorsProfile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	for k, v := range map[string]string{
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.com", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.com",
		"GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_NOSYSTEM": "1", "GIT_TERMINAL_PROMPT": "0",
	} {
		t.Setenv(k, v)
	}
	root := t.TempDir()
	public, private, work := filepath.Join(root, "public.git"), filepath.Join(root, "private.git"), filepath.Join(root, "work")

	// Canonical Adaetum, with a seed checkout to give it a main.
	sh(t, root, "init", "--bare", "-b", "main", public)
	seed := filepath.Join(root, "seed")
	sh(t, root, "clone", public, seed)
	write(t, filepath.Join(seed, "platform.yaml"), "domain: public.example\n")
	write(t, filepath.Join(seed, "from-public.txt"), "public tree\n")
	sh(t, seed, "add", ".")
	sh(t, seed, "commit", "-m", "public main")
	sh(t, seed, "push", "origin", "HEAD:refs/heads/main")

	// The operator's checkout: cloned from public, with their profile on a dev branch.
	sh(t, root, "init", "--bare", "-b", "main", private)
	sh(t, root, "clone", public, work)
	sh(t, work, "checkout", "-b", "dev")
	write(t, filepath.Join(work, "platform.yaml"), "domain: mine.example\n")
	sh(t, work, "commit", "-am", "my profile")
	// A stand-in for GitHub: the private repository's web URL resolves to the local bare repo.
	sh(t, work, "config", "url."+private+".insteadOf", "https://github.com/alice/Adaetum-cluster.git")
	sh(t, work, "remote", "add", "upstream", public)

	gh := &sys.Fake{Rules: []sys.Rule{
		{Prefix: "gh auth token", Result: ok("token")},
		{Prefix: "gh api user", Result: ok("alice\n")},
		{Prefix: "gh repo create alice/Adaetum-cluster --private", Result: okEmpty},
		{Prefix: "gh api repos/alice/Adaetum-cluster/actions/workflows", Result: ok("2")},
		{Prefix: "gh api repos/alice/Adaetum-cluster", Result: ok(meta("private", true, false, 0, "main")),
			Then: []sys.Result{notFound, notFound, notFound}},
	}}
	step := New(sys.Mux{Real: sys.Exec{}, RealOnly: []string{"git"}, Fake: gh}, work)
	step.Sleep = func(time.Duration) {}
	rendered := false
	step.Render = func(context.Context) error { rendered = true; return nil }

	var said []string
	ui := engine.Headless{
		Answers: map[string]string{"repository": "alice/Adaetum-cluster"},
		Log:     func(e engine.Event) { said = append(said, e.Message) },
	}
	if err := step.Apply(context.Background(), &engine.Env{}, ui); err != nil {
		t.Fatalf("apply: %v\n%s", err, strings.Join(said, "\n"))
	}

	if !rendered {
		t.Error("the profile was not re-rendered")
	}
	if got := sh(t, work, "branch", "--show-current"); got != "main" {
		t.Errorf("on %q, want main", got)
	}
	data, _ := os.ReadFile(filepath.Join(work, "platform.yaml"))
	if string(data) != "domain: mine.example\n" {
		t.Errorf("the operator's profile was lost: %q", data)
	}
	if _, err := os.Stat(filepath.Join(work, "from-public.txt")); err != nil {
		t.Error("the public tree was not applied")
	}
	if local, remote := sh(t, work, "rev-parse", "HEAD"), sh(t, private, "rev-parse", "refs/heads/main"); local != remote {
		t.Errorf("private main %s does not match HEAD %s", remote, local)
	}
	if got := sh(t, work, "remote", "get-url", "upstream"); got != public {
		t.Errorf("upstream changed to %q", got)
	}
	if got := sh(t, work, "remote", "get-url", "--push", "origin"); got != private && got != "https://github.com/alice/Adaetum-cluster.git" {
		t.Errorf("origin push URL is %q", got)
	}
	// The dev branch, with the operator's commit, must still exist.
	if got := sh(t, work, "branch", "--list", "dev"); got == "" {
		t.Error("the dev branch was lost")
	}
}

func TestUnpublishedLocalMainIsNotDiscarded(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	for k, v := range map[string]string{
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.com", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.com",
		"GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_NOSYSTEM": "1", "GIT_TERMINAL_PROMPT": "0",
	} {
		t.Setenv(k, v)
	}
	root := t.TempDir()
	public, private, work := filepath.Join(root, "public.git"), filepath.Join(root, "private.git"), filepath.Join(root, "work")
	sh(t, root, "init", "--bare", "-b", "main", public)
	sh(t, root, "init", "--bare", "-b", "main", private)
	seed := filepath.Join(root, "seed")
	sh(t, root, "clone", public, seed)
	write(t, filepath.Join(seed, "platform.yaml"), "a\n")
	sh(t, seed, "add", ".")
	sh(t, seed, "commit", "-m", "public")
	sh(t, seed, "push", "origin", "HEAD:refs/heads/main")
	sh(t, root, "clone", public, work)
	write(t, filepath.Join(work, "precious.txt"), "unpublished\n")
	sh(t, work, "add", ".")
	sh(t, work, "commit", "-m", "work only on local main")
	sh(t, work, "remote", "add", "upstream", public)
	sh(t, work, "remote", "set-url", "origin", private)

	step := New(sys.Exec{}, work)
	step.Render = func(context.Context) error { return nil }
	err := step.trackBranch(context.Background(), func(string) {})
	if err == nil || !strings.Contains(err.Error(), "not recoverable") {
		t.Fatalf("expected a refusal, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(work, "precious.txt")); statErr != nil {
		t.Fatal("the unpublished file was lost")
	}
}

func TestAReplayRunNeverCreatesTheRepository(t *testing.T) {
	f := &sys.Fake{Rules: []sys.Rule{
		{Prefix: "gh auth token", Result: ok("tok")},
		{Prefix: "git remote get-url origin", Result: ok("https://github.com/Adaetum/Adaetum.git\n")},
		{Prefix: "gh repo create", Result: okEmpty},
		{Prefix: "gh api repos/", Result: notFound},
		{Prefix: "git", Result: okEmpty},
	}}
	_, err := newStep(f).configure(context.Background(), engine.Headless{Replay: true}, func(string) {}, "alice")
	if !errors.Is(err, engine.ErrNotReplayable) {
		t.Fatalf("err = %v", err)
	}
	if f.Ran("gh repo create") {
		t.Fatal("a replay run created a repository")
	}
}

func TestACheckThatCannotReachGitHubReportsItInsteadOfFailing(t *testing.T) {
	f := &sys.Fake{Rules: []sys.Rule{
		{Prefix: "git --version", Result: ok("")},
		{Prefix: "gh --version", Result: ok("")},
		{Prefix: "git remote get-url origin", Result: ok("https://github.com/alice/Adaetum-cluster.git\n")},
		{Prefix: "gh auth token", Result: ok("tok")},
		{Prefix: "gh api repos/", Result: sys.Result{ExitCode: 1, Stderr: "HTTP 503 service unavailable"}},
	}}
	got, err := newStep(f).Check(context.Background(), &engine.Env{})
	if err != nil || got.Status != engine.Needed || !strings.Contains(got.Detail, "could not check") {
		t.Fatalf("%+v %v", got, err)
	}
}
