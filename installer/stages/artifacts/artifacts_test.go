package artifacts

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Adaetum/Adaetum/installer/engine"
	"github.com/Adaetum/Adaetum/installer/envfile"
	"github.com/Adaetum/Adaetum/installer/githubapi"
	"github.com/Adaetum/Adaetum/installer/sys"
	"golang.org/x/crypto/nacl/box"
)

const (
	repo    = "alice/Adaetum-cluster"
	ghToken = "gho_secrettoken"
)

// githubServer plays GitHub: environments, encrypted secrets, repository metadata,
// workflow dispatch and runs.
type githubServer struct {
	mu          sync.Mutex
	secrets     map[string]string // name -> scope
	envMissing  bool
	pushAllowed bool
	failing     map[string]bool // workflow file -> conclude "failure"
	runs        map[string][]int64
	next        int64
	fileOf      map[int64]string
	dispatches  []string
}

func newGitHub(t *testing.T) (*githubServer, githubapi.Client) {
	pub, _, _ := box.GenerateKey(rand.Reader)
	g := &githubServer{secrets: map[string]string{}, pushAllowed: true, failing: map[string]bool{}, runs: map[string][]int64{}, fileOf: map[int64]string{}, next: 100}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		p := r.URL.Path
		switch {
		case r.Method == http.MethodPut && strings.Contains(p, "/environments/") && !strings.Contains(p, "/secrets/"):
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(p, "/secrets/public-key"):
			if g.envMissing && strings.Contains(p, "/environments/") {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			fmt.Fprintf(w, `{"key":%q,"key_id":"k"}`, base64.StdEncoding.EncodeToString(pub[:]))
		case r.Method == http.MethodPut && strings.Contains(p, "/secrets/"):
			scope := "repo"
			if strings.Contains(p, "/environments/") {
				scope = "env"
			}
			g.secrets[p[strings.LastIndex(p, "/")+1:]] = scope
			w.WriteHeader(http.StatusCreated)
		case p == "/repos/"+repo:
			fmt.Fprintf(w, `{"full_name":%q,"permissions":{"push":%t}}`, repo, g.pushAllowed)
		case r.Method == http.MethodPost && strings.HasSuffix(p, "/dispatches"):
			file := strings.Split(p, "/")[len(strings.Split(p, "/"))-2]
			g.next++
			g.runs[file] = append([]int64{g.next}, g.runs[file]...)
			g.fileOf[g.next] = file
			g.dispatches = append(g.dispatches, file)
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(p, "/runs"):
			file := strings.Split(p, "/")[len(strings.Split(p, "/"))-2]
			var items []string
			for _, id := range g.runs[file] {
				items = append(items, fmt.Sprintf(`{"id":%d}`, id))
			}
			fmt.Fprintf(w, `{"workflow_runs":[%s]}`, strings.Join(items, ","))
		case strings.Contains(p, "/actions/runs/"):
			var id int64
			fmt.Sscanf(p[strings.LastIndex(p, "/")+1:], "%d", &id)
			conclusion := "success"
			if g.failing[g.fileOf[id]] {
				conclusion = "failure"
			}
			json.NewEncoder(w).Encode(map[string]string{"status": "completed", "conclusion": conclusion, "html_url": "https://example/run"})
		default:
			t.Errorf("unexpected GitHub call: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return g, githubapi.Client{Token: ghToken, Base: srv.URL, Sleep: func(time.Duration) {}}
}

func fixture(t *testing.T) (*Step, *engine.Env, *sys.Fake, *githubServer) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "Rocky-10.2-x86_64-minimal.iso"), []byte("iso-bytes"), 0o644)
	os.MkdirAll(filepath.Join(root, "dist", "ks-templates"), 0o755)
	os.WriteFile(filepath.Join(root, "dist", "ks-templates", "rocky10.ks"), []byte("# GOLDEN_ISO_KEY=rocky10/Rocky-10.2-x86_64-minimal.iso\n"), 0o644)
	envfile.Write(filepath.Join(root, ".env"), [][2]string{
		{"R2_ENDPOINT", "https://acct.r2.cloudflarestorage.com"}, {"R2_BUCKET", "iso"},
		{"R2_ACCESS_KEY_ID", "r2-key-id"}, {"R2_SECRET_ACCESS_KEY", "r2-secret-value"},
		{"CLOUDFLARE_API_TOKEN", "cfat_x"}, {"GITHUB_SYNC_TOKEN", ghToken}, {"BOOTSTRAP_BACKUP_PASSPHRASE", "pass"},
		{"ARGOCD_GITHUB_REPO_URL", "https://github.com/" + repo + ".git"}, {"ARGOCD_GITHUB_USERNAME", "alice"},
	})
	cache, err := cacheBuster(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := envfile.Upsert(filepath.Join(root, ".env"), "BOOTSTRAP_RUNTIME_ENV_CACHE_BUSTER", cache); err != nil {
		t.Fatal(err)
	}
	recovery, err := os.ReadFile(filepath.Join(root, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	g, client := newGitHub(t)
	f := &sys.Fake{Rules: []sys.Rule{
		{Prefix: "git config --get remote.origin.url", Result: sys.Result{Stdout: "https://github.com/" + repo + ".git\n"}},
		{Prefix: "git rev-parse --abbrev-ref HEAD", Result: sys.Result{Stdout: "main\n"}},
		{Prefix: "git diff", Result: sys.Result{}},
		{Prefix: "git ls-remote", Result: sys.Result{Stdout: "abc\tHEAD\n"}},
		{Prefix: "uv --version", Result: sys.Result{ExitCode: 127}},
		{Prefix: "python3 -c import jinja2", Result: sys.Result{}},
		{Prefix: "python3 ./tasks/scripts/compile-kickstarts.py", Result: sys.Result{}},
		{Prefix: "rclone lsjson", Result: sys.Result{Stdout: "[]"}},
		{Prefix: "rclone copyto", Result: sys.Result{}},
		{Prefix: "rclone cat cluster_r2:iso/recovery/", Result: sys.Result{Stdout: string(recovery)}},
		{Prefix: "task ", Result: sys.Result{}},
	}}
	s := New(f, root)
	s.GH = func(string) githubapi.Client { return client }
	s.Sleep = func(time.Duration) {}
	env := &engine.Env{}
	env.SetSecret("github_token", ghToken)
	env.SetValue("iso_path", filepath.Join(root, "Rocky-10.2-x86_64-minimal.iso"))
	return s, env, f, g
}

func TestTheWholeStagePublishesAndAllWorkflowsSucceed(t *testing.T) {
	s, env, f, g := fixture(t)
	if err := s.Apply(context.Background(), env, engine.Headless{}); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"ks-worker.yml", "ks-publish.yml", "iso-build.yml"} {
		found := false
		for _, d := range g.dispatches {
			found = found || d == file
		}
		if !found {
			t.Errorf("%s was not triggered", file)
		}
	}
	if g.secrets["BOOTSTRAP_BACKUP_PASSPHRASE"] != "env" || g.secrets["R2_BUCKET"] != "env" {
		t.Errorf("secrets were not synced to the environment: %v", g.secrets)
	}
	if _, ok := g.secrets["GITEA_PUSH_MIRROR_TOKEN"]; ok {
		t.Error("an empty value was synced")
	}
	if !f.Ran("rclone copyto") || !f.Ran("task bundle:publish:external") || !f.Ran("task build-iso") {
		t.Errorf("a step did not run: %v", f.Calls)
	}
	if !f.Ran("rclone copyto " + filepath.Join(s.Root, "Rocky-10.2-x86_64-minimal.iso") + " cluster_r2:iso/rocky10/Rocky-10.2-x86_64-minimal.iso") {
		t.Errorf("the image did not go to its GOLDEN_ISO_KEY: %v", f.Calls)
	}
	if !f.Ran("rclone copyto "+filepath.Join(s.Root, ".env")+" cluster_r2:iso/recovery/setup-") || !f.Ran("rclone cat cluster_r2:iso/recovery/setup-") {
		t.Error("setup recovery copy was not uploaded and verified")
	}
}

func TestRecoveryBackupFailsWhenReadbackDiffers(t *testing.T) {
	s, env, f, _ := fixture(t)
	f.Rules = append([]sys.Rule{{Prefix: "rclone cat cluster_r2:iso/recovery/", Result: sys.Result{Stdout: "wrong data"}}}, f.Rules...)
	if err := s.Apply(context.Background(), env, engine.Headless{}); err == nil || !strings.Contains(err.Error(), "could not be verified") {
		t.Fatalf("expected recovery verification failure, got %v", err)
	}
}

func TestNoCredentialIsEverOnACommandLine(t *testing.T) {
	s, env, f, _ := fixture(t)
	if err := s.Apply(context.Background(), env, engine.Headless{}); err != nil {
		t.Fatal(err)
	}
	for _, call := range f.Calls {
		for _, secret := range []string{ghToken, "r2-secret-value", "r2-key-id", "cfat_x"} {
			if strings.Contains(call, secret) {
				t.Errorf("%q appeared on a command line: %s", secret, call)
			}
		}
	}
	// rclone got its credentials through the environment.
	sawR2 := false
	for i, call := range f.Calls {
		if strings.HasPrefix(call, "rclone") {
			for _, e := range f.Envs[i] {
				sawR2 = sawR2 || e == "RCLONE_CONFIG_CLUSTER_R2_SECRET_ACCESS_KEY=r2-secret-value"
			}
		}
	}
	if !sawR2 {
		t.Error("rclone was not given its credentials through the environment")
	}
}

func TestAnImageAlreadyInR2WithTheSameSizeIsNotUploadedAgain(t *testing.T) {
	s, env, f, _ := fixture(t)
	f.Rules = append([]sys.Rule{{Prefix: "rclone lsjson", Result: sys.Result{Stdout: `[{"Size":9}]`}}}, f.Rules...)
	if err := s.Apply(context.Background(), env, engine.Headless{}); err != nil {
		t.Fatal(err)
	}
	if f.Ran("rclone copyto " + filepath.Join(s.Root, "Rocky-10.2-x86_64-minimal.iso")) {
		t.Fatal("an image of the same size was uploaded again")
	}
}

func TestAFailedImageUploadIsAWarningNotAStop(t *testing.T) {
	s, env, f, g := fixture(t)
	f.Rules = append([]sys.Rule{{Prefix: "rclone copyto " + filepath.Join(s.Root, "Rocky-10.2-x86_64-minimal.iso"), Result: sys.Result{ExitCode: 1, Stderr: "network down"}}}, f.Rules...)
	if err := s.Apply(context.Background(), env, engine.Headless{}); err != nil {
		t.Fatalf("a failed upload stopped setup: %v", err)
	}
	if len(g.dispatches) != 3 {
		t.Fatal("setup did not carry on to the workflows")
	}
}

func TestAWorkflowFailureFailsTheStageAfterTheOthersRun(t *testing.T) {
	s, env, _, g := fixture(t)
	g.failing["iso-build.yml"] = true
	err := s.Apply(context.Background(), env, engine.Headless{})
	if err == nil || !strings.Contains(err.Error(), "iso-build.yml") {
		t.Fatalf("err = %v", err)
	}
	if len(g.dispatches) != 3 {
		t.Fatal("a failing workflow stopped the others from being triggered")
	}
}

func TestAFailedWorkerWorkflowIsToleratedOnceTheBundleIsUploaded(t *testing.T) {
	s, env, _, g := fixture(t)
	g.failing["ks-worker.yml"] = true
	if err := s.Apply(context.Background(), env, engine.Headless{}); err != nil {
		t.Fatalf("the worker failure was not tolerated after a verified bundle upload: %v", err)
	}
}

func TestAFailedWorkerWorkflowCountsWhenTheBundleFailed(t *testing.T) {
	s, env, f, g := fixture(t)
	g.failing["ks-worker.yml"] = true
	f.Rules = append([]sys.Rule{{Prefix: "task bundle:publish:external", Result: sys.Result{ExitCode: 1}}}, f.Rules...)
	if err := s.Apply(context.Background(), env, engine.Headless{}); err == nil {
		t.Fatal("expected the worker failure to count when the bundle was not uploaded")
	}
}

func TestATokenWithoutPushAccessStopsBeforeAnythingIsPublished(t *testing.T) {
	s, env, f, g := fixture(t)
	g.pushAllowed = false
	err := s.Apply(context.Background(), env, engine.Headless{})
	if err == nil || !strings.Contains(err.Error(), "push access") {
		t.Fatalf("err = %v", err)
	}
	if f.Ran("task bundle") || len(g.dispatches) != 0 {
		t.Fatal("something was published after the credential check failed")
	}
}

func TestSecretsFallBackToTheRepositoryWhenEnvironmentsAreUnavailable(t *testing.T) {
	s, env, _, g := fixture(t)
	g.envMissing = true
	if err := s.Apply(context.Background(), env, engine.Headless{}); err != nil {
		t.Fatal(err)
	}
	if g.secrets["R2_BUCKET"] != "repo" {
		t.Fatalf("scope = %q, want a repo-level fallback", g.secrets["R2_BUCKET"])
	}
}

func TestAnInvalidBucketNameIsRefusedBeforeSyncing(t *testing.T) {
	_, client := newGitHub(t)
	_, err := syncSecrets(context.Background(), client, repo, "Prod", map[string]string{"R2_BUCKET": "Bad_Bucket!"})
	if err == nil || !strings.Contains(err.Error(), "R2_BUCKET") {
		t.Fatalf("err = %v", err)
	}
}

func TestMissingEarlierOutputsAreNamed(t *testing.T) {
	s, env, _, _ := fixture(t)
	os.Remove(filepath.Join(s.Root, ".env"))
	if err := s.Apply(context.Background(), env, engine.Headless{}); err == nil || !strings.Contains(err.Error(), ".env") {
		t.Fatalf("err = %v", err)
	}
}

func TestCacheBusterHashesTheRuntimeEnvWhenPresentAndOtherwiseDotEnv(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, ".env"), []byte("A=1\nBOOTSTRAP_RUNTIME_ENV_CACHE_BUSTER=old\nB=2\n"), 0o600)
	a, err := cacheBuster(root)
	if err != nil || len(a) != 16 {
		t.Fatalf("%q %v", a, err)
	}
	// Its own previous value must not change the result.
	os.WriteFile(filepath.Join(root, ".env"), []byte("A=1\nBOOTSTRAP_RUNTIME_ENV_CACHE_BUSTER=different\nB=2\n"), 0o600)
	if b, _ := cacheBuster(root); b != a {
		t.Fatalf("the buster depends on its own value: %s vs %s", a, b)
	}
	os.MkdirAll(filepath.Join(root, "dist"), 0o755)
	os.WriteFile(filepath.Join(root, "dist", "bootstrap-runtime.env"), []byte("payload"), 0o644)
	if c, _ := cacheBuster(root); c == a {
		t.Fatal("the runtime payload was ignored")
	}
}

func TestDefaultKeysAndGoldenKeys(t *testing.T) {
	for in, want := range map[string]string{
		"Rocky-10.2-x86_64-minimal.iso": "rocky10/Rocky-10.2-x86_64-minimal.iso",
		"Rocky-9.4-x86_64-dvd.iso":      "rocky9/Rocky-9.4-x86_64-dvd.iso",
		"ubuntu-24.04.1-live.iso":       "ubuntu2404/ubuntu-24.04.1-live.iso",
		"other.iso":                     "other.iso",
	} {
		if got := defaultKey(in); got != want {
			t.Errorf("defaultKey(%q) = %q, want %q", in, got, want)
		}
	}
}
