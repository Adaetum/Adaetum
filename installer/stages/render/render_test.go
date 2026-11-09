package render

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Adaetum/Adaetum/installer/engine"
	"github.com/Adaetum/Adaetum/installer/envfile"
	"github.com/Adaetum/Adaetum/installer/sys"
)

const ghToken = "gho_supersecrettoken"

// harness runs git for real against temporary repositories, and plays the
// existing scripts, recording every command so tests can check what was (and was
// not) put on a command line.
type harness struct {
	t        *testing.T
	root     string
	private  string
	calls    []string
	envs     [][]string
	fake     *sys.Fake
	tmpMode  os.FileMode
	tmpPath  string
	tmpBody  string
	genFails bool
}

func (h *harness) Run(ctx context.Context, dir, name string, args ...string) (sys.Result, error) {
	return h.RunEnv(ctx, dir, nil, "", name, args...)
}
func (h *harness) RunInput(ctx context.Context, dir, stdin, name string, args ...string) (sys.Result, error) {
	return h.RunEnv(ctx, dir, nil, stdin, name, args...)
}

func (h *harness) RunEnv(ctx context.Context, dir string, env []string, stdin, name string, args ...string) (sys.Result, error) {
	h.calls = append(h.calls, strings.Join(append([]string{name}, args...), " "))
	h.envs = append(h.envs, env)
	switch {
	case name == "git":
		return sys.Exec{}.RunEnv(ctx, dir, env, stdin, name, args...)
	case name == "bash" && strings.HasSuffix(args[0], "generate-env-files.sh"):
		// Play generate-env-files.sh: inspect the secrets file it was handed, then
		// write .env the way it would.
		h.tmpPath = args[2]
		if info, err := os.Stat(h.tmpPath); err == nil {
			h.tmpMode = info.Mode().Perm()
		}
		body, _ := os.ReadFile(h.tmpPath)
		h.tmpBody = string(body)
		if h.genFails {
			return sys.Result{ExitCode: 1, Stderr: "boom"}, nil
		}
		in := envfile.Read(h.tmpPath)
		for k, v := range in {
			envfile.Upsert(filepath.Join(h.root, ".env"), k, v)
		}
		return sys.Result{}, nil
	case len(args) > 0 && strings.HasSuffix(args[0], "render-platform-profile.py") && contains(args, "--render-pods"):
		// Play the renderer: change one tracked output and add a new one.
		os.WriteFile(filepath.Join(h.root, "rendered/a.yaml"), []byte("rendered v2\n"), 0o644)
		os.WriteFile(filepath.Join(h.root, "rendered/new.yaml"), []byte("brand new\n"), 0o644)
		return sys.Result{}, nil
	}
	return h.fake.RunEnv(ctx, dir, env, stdin, name, args...)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func g(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	for k, v := range map[string]string{
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@example.com", "GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@example.com",
		"GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_NOSYSTEM": "1",
	} {
		t.Setenv(k, v)
	}
	base := t.TempDir()
	private, work := filepath.Join(base, "private.git"), filepath.Join(base, "work")
	g(t, base, "init", "--bare", "-b", "main", private)
	g(t, base, "clone", private, work)
	os.MkdirAll(filepath.Join(work, "rendered"), 0o755)
	os.WriteFile(filepath.Join(work, "platform.yaml"), []byte("domain: example.com\n"), 0o644)
	os.WriteFile(filepath.Join(work, "rendered/a.yaml"), []byte("rendered v1\n"), 0o644)
	os.WriteFile(filepath.Join(work, "unrelated.txt"), []byte("original\n"), 0o644)
	g(t, work, "add", ".")
	g(t, work, "commit", "-m", "start")
	g(t, work, "push", "origin", "HEAD:refs/heads/main")
	g(t, work, "remote", "set-url", "origin", "https://github.com/alice/Adaetum-cluster.git")
	g(t, work, "config", "url."+private+".insteadOf", "https://github.com/alice/Adaetum-cluster.git")
	g(t, work, "fetch", "origin")
	// A real, unrelated edit to a tracked file that must not be committed.
	os.WriteFile(filepath.Join(work, "unrelated.txt"), []byte("half-finished edit\n"), 0o644)

	fake := &sys.Fake{Rules: []sys.Rule{
		{Prefix: "python3 --version", Result: sys.Result{Stdout: "Python"}},
		{Prefix: "python3 -c", Result: sys.Result{Stdout: "platform.yaml\nrendered/a.yaml\nrendered/new.yaml\nrendered/not-created.yaml\n"}},
		{Prefix: "python3 ./tasks/scripts/bootstrap-tailscale.py --oauth-credentials-stdin --validate-operator-oauth-only", Result: sys.Result{Stdout: "TAILSCALE_OPERATOR_OAUTH_CLIENT_ID=op-id\n"}},
		{Prefix: "python3 ./tasks/scripts/bootstrap-tailscale.py --oauth-credentials-stdin", Result: sys.Result{Stdout: "TAILSCALE_OAUTH_CLIENT_ID=id\nTAILSCALE_OAUTH_CLIENT_SECRET=sec\nTAILSCALE_CLUSTER_TAG=tag:cluster\nTAILSCALE_ADVERTISE_TAGS=tag:rocky10,tag:server,tag:cluster\nTAILSCALE_AUTHKEY=tskey-auth-minted\n"}},
		{Prefix: "bash ./tasks/scripts/validate-", Result: sys.Result{}},
		{Prefix: "python3 ./", Result: sys.Result{}},
	}}
	return &harness{t: t, root: work, private: private, fake: fake}
}

func ready() *engine.Env {
	e := &engine.Env{}
	for k, v := range map[string]string{
		"cloudflare_token": "cfat_x", "github_token": ghToken, "tailscale_oauth_client_id": "id", "tailscale_oauth_client_secret": "sec",
		"tailscale_operator_oauth_client_id": "op-id", "tailscale_operator_oauth_client_secret": "op-sec",
	} {
		e.SetSecret(k, v)
	}
	for k, v := range map[string]string{"ks_base_url": "https://bootstrap.example.com", "tailscale_domain": "tail1.ts.net", "iso_path": "", "r2_bucket": "iso", "cluster_tag": "tag:cluster"} {
		e.SetValue(k, v)
	}
	return e
}

func (h *harness) step() *Step {
	s := New(h, h.root)
	s.Random = func(b []byte) (int, error) {
		for i := range b {
			b[i] = byte(i + 1)
		}
		return len(b), nil
	}
	return s
}

func (h *harness) env() *engine.Env {
	e := ready()
	e.SetValue("iso_path", filepath.Join(h.root, "Rocky-10.2-x86_64-minimal.iso"))
	return e
}

func TestRenderPublishesExactlyTheRenderedFilesAndNothingElse(t *testing.T) {
	h := newHarness(t)
	before := g(t, h.root, "rev-parse", "HEAD")
	if err := h.step().Apply(context.Background(), h.env(), engine.Headless{}); err != nil {
		t.Fatal(err)
	}
	if g(t, h.root, "rev-parse", "HEAD") == before {
		t.Fatal("nothing was committed")
	}
	files := g(t, h.root, "show", "--name-only", "--format=", "HEAD")
	for _, want := range []string{"rendered/a.yaml", "rendered/new.yaml"} {
		if !strings.Contains(files, want) {
			t.Errorf("%s was not committed:\n%s", want, files)
		}
	}
	if strings.Contains(files, "unrelated.txt") {
		t.Error("an unrelated edit was swept into the recovery commit")
	}
	if got, _ := os.ReadFile(filepath.Join(h.root, "unrelated.txt")); string(got) != "half-finished edit\n" {
		t.Errorf("the unrelated edit was lost: %q", got)
	}
	if local, remote := g(t, h.root, "rev-parse", "HEAD"), g(t, h.private, "rev-parse", "refs/heads/main"); local != remote {
		t.Errorf("origin %s != HEAD %s", remote, local)
	}
}

func TestNoSecretEverAppearsOnACommandLine(t *testing.T) {
	h := newHarness(t)
	if err := h.step().Apply(context.Background(), h.env(), engine.Headless{}); err != nil {
		t.Fatal(err)
	}
	for _, call := range h.calls {
		for _, secret := range []string{ghToken, "cfat_x", "sec", "op-sec", "tskey-auth-minted"} {
			if strings.Contains(call, secret) && secret != "sec" { // "sec" is too short to search for blindly
				t.Errorf("secret %q appeared on a command line: %s", secret, call)
			}
			if secret == "sec" && strings.Contains(call, "--oauth-client-secret") {
				t.Errorf("the OAuth secret went on a command line: %s", call)
			}
		}
	}
	sawHeader := false
	for _, env := range h.envs {
		for _, e := range env {
			sawHeader = sawHeader || strings.HasPrefix(e, "GIT_CONFIG_VALUE_1=AUTHORIZATION: basic ")
		}
	}
	if !sawHeader {
		t.Error("the GitHub credential never reached git through the environment")
	}
}

func TestTheSecretsFileIsOwnerOnlyHasTheCredentialsAndIsRemoved(t *testing.T) {
	h := newHarness(t)
	if err := h.step().Apply(context.Background(), h.env(), engine.Headless{}); err != nil {
		t.Fatal(err)
	}
	if h.tmpMode != 0o600 {
		t.Errorf("secrets file mode %v, want 0600", h.tmpMode)
	}
	for _, want := range []string{"CLOUDFLARE_API_TOKEN=cfat_x", "GITHUB_SYNC_TOKEN=" + ghToken, "KS_BASE_URL=https://bootstrap.example.com", "R2_BUCKET=iso", "BOOTSTRAP_BACKUP_PASSPHRASE="} {
		if !strings.Contains(h.tmpBody, want) {
			t.Errorf("secrets file is missing %q", want)
		}
	}
	if _, err := os.Stat(h.tmpPath); err == nil {
		t.Error("the secrets file was left behind")
	}
}

func TestTheSecretsFileIsRemovedEvenWhenRenderingFails(t *testing.T) {
	h := newHarness(t)
	h.genFails = true
	err := h.step().Apply(context.Background(), h.env(), engine.Headless{})
	if err == nil || !strings.Contains(err.Error(), "Details:") {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(h.tmpPath); statErr == nil {
		t.Error("the secrets file was left behind after a failure")
	}
}

func TestAnExistingBackupPassphraseIsKept(t *testing.T) {
	h := newHarness(t)
	envfile.Upsert(filepath.Join(h.root, ".env"), "BOOTSTRAP_BACKUP_PASSPHRASE", "keep-me-please")
	if err := h.step().Apply(context.Background(), h.env(), engine.Headless{}); err != nil {
		t.Fatal(err)
	}
	if got := envfile.Read(filepath.Join(h.root, ".env"))["BOOTSTRAP_BACKUP_PASSPHRASE"]; got != "keep-me-please" {
		t.Fatalf("the passphrase was replaced: %q", got)
	}
}

func TestAFirstRunGeneratesAPassphrase(t *testing.T) {
	h := newHarness(t)
	if err := h.step().Apply(context.Background(), h.env(), engine.Headless{}); err != nil {
		t.Fatal(err)
	}
	got := envfile.Read(filepath.Join(h.root, ".env"))["BOOTSTRAP_BACKUP_PASSPHRASE"]
	if len(got) != 24 {
		t.Fatalf("generated passphrase %q has length %d, want 24", got, len(got))
	}
}

func TestAMissingPassphraseWithPriorStateIsNeverSilentlyRegenerated(t *testing.T) {
	h := newHarness(t)
	envfile.Upsert(filepath.Join(h.root, ".env"), "KS_SHARED_TOKEN", "from-an-earlier-run")
	err := h.step().Apply(context.Background(), h.env(), engine.Headless{})
	if err == nil || !strings.Contains(err.Error(), "orphan") {
		t.Fatalf("err = %v", err)
	}
	t.Setenv("SETUP_ALLOW_BACKUP_PASSPHRASE_ROTATE", "1")
	h2 := newHarness(t)
	envfile.Upsert(filepath.Join(h2.root, ".env"), "KS_SHARED_TOKEN", "from-an-earlier-run")
	if err := h2.step().Apply(context.Background(), h2.env(), engine.Headless{}); err != nil {
		t.Fatalf("explicit rotation was refused: %v", err)
	}
}

func TestMissingEarlierStageOutputsAreNamed(t *testing.T) {
	h := newHarness(t)
	e := h.env()
	delete(e.Secrets, "cloudflare_token")
	if err := h.step().Apply(context.Background(), e, engine.Headless{}); err == nil || !strings.Contains(err.Error(), "cloudflare_token") {
		t.Fatalf("err = %v", err)
	}
}

func TestARejectedOAuthClientStopsBeforeAnythingIsWritten(t *testing.T) {
	h := newHarness(t)
	h.fake.Rules = append([]sys.Rule{{Prefix: "python3 ./tasks/scripts/bootstrap-tailscale.py --oauth-credentials-stdin --tailnet", Result: sys.Result{ExitCode: 1, Stderr: "invalid_client"}}}, h.fake.Rules...)
	if err := h.step().Apply(context.Background(), h.env(), engine.Headless{}); err == nil {
		t.Fatal("expected the OAuth failure")
	}
	if h.tmpPath != "" {
		t.Error("secrets were handed to a script after the OAuth client failed validation")
	}
}

func TestReusedRecoveryRequiresExistingBackupPassphrase(t *testing.T) {
	h := newHarness(t)
	env := h.env()
	env.SetValue("existing_recovery", "true")
	t.Setenv("ADAETUM_SECRET_BOOTSTRAP_BACKUP_PASSPHRASE", "")
	if err := h.step().Apply(context.Background(), env, engine.Headless{}); err == nil || !strings.Contains(err.Error(), "passphrase") {
		t.Fatalf("expected passphrase requirement, got %v", err)
	}
	h = newHarness(t)
	env = h.env()
	env.SetValue("existing_recovery", "true")
	t.Setenv("ADAETUM_SECRET_BOOTSTRAP_BACKUP_PASSPHRASE", "restored-backup-key")
	if err := h.step().Apply(context.Background(), env, engine.Headless{}); err != nil {
		t.Fatal(err)
	}
	if got := envfile.Read(filepath.Join(h.root, ".env"))["BOOTSTRAP_BACKUP_PASSPHRASE"]; got != "restored-backup-key" {
		t.Fatal("did not preserve restored backup passphrase")
	}
}
