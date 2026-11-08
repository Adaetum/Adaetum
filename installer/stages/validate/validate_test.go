package validate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Adaetum/Adaetum/installer/engine"
	"github.com/Adaetum/Adaetum/installer/platform"
	"github.com/Adaetum/Adaetum/installer/sys"
)

func newStep(t *testing.T, gh string) (*Step, string) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "Rocky-10.2-x86_64-minimal.iso"), []byte("x"), 0o644)
	f := &sys.Fake{Rules: []sys.Rule{
		{Prefix: "python3 --version", Result: sys.Result{Stdout: "Python"}},
		{Prefix: "gh auth token", Result: sys.Result{Stdout: gh + "\n"}},
	}}
	s := New(f, root)
	s.Validate = func(context.Context) error { return nil }
	s.Profile = func(context.Context) (platform.SetupValues, error) {
		return platform.SetupValues{ClusterDomain: "example.com", TailscaleDomain: "tail1.ts.net", ClusterTag: "tag:cluster",
			KSBaseURL: "https://bootstrap.example.com", R2Bucket: "iso"}, nil
	}
	return s, root
}

func ready() *engine.Env {
	e := &engine.Env{}
	e.SetSecret("cloudflare_token", "cfat_x")
	e.SetSecret("tailscale_oauth_client_id", "id")
	e.SetSecret("tailscale_oauth_client_secret", "sec")
	return e
}

func TestValidatePassesAndPublishesTheDerivedValues(t *testing.T) {
	s, root := newStep(t, "gho_abc")
	env := ready()
	if err := s.Apply(context.Background(), env, engine.Headless{}); err != nil {
		t.Fatal(err)
	}
	if env.Value("ks_base_url") != "https://bootstrap.example.com" || env.Value("r2_bucket") != "iso" || env.Secret("github_token") != "gho_abc" {
		t.Fatalf("%v", env.Values)
	}
	if env.Value("iso_path") != filepath.Join(root, "Rocky-10.2-x86_64-minimal.iso") {
		t.Fatalf("iso %q", env.Value("iso_path"))
	}
}

func TestEachMissingInputIsNamedWithItsSource(t *testing.T) {
	for name, mutate := range map[string]func(*engine.Env){
		"cloudflare": func(e *engine.Env) { delete(e.Secrets, "cloudflare_token") },
		"tailscale": func(e *engine.Env) {
			delete(e.Secrets, "tailscale_oauth_client_id")
			delete(e.Secrets, "tailscale_oauth_client_secret")
		},
	} {
		s, _ := newStep(t, "gho_abc")
		env := ready()
		mutate(env)
		err := s.Apply(context.Background(), env, engine.Headless{})
		if err == nil || !strings.Contains(err.Error(), "providers") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestAReadOnlyGitHubTokenIsRefusedEarly(t *testing.T) {
	s, _ := newStep(t, "not-a-git-token")
	if err := s.Apply(context.Background(), ready(), engine.Headless{}); err == nil || !strings.Contains(err.Error(), "git-capable") {
		t.Fatalf("err = %v", err)
	}
}

func TestNoInstallerISOStopsTheRun(t *testing.T) {
	s, root := newStep(t, "gho_abc")
	os.Remove(filepath.Join(root, "Rocky-10.2-x86_64-minimal.iso"))
	if err := s.Apply(context.Background(), ready(), engine.Headless{}); err == nil {
		t.Fatal("expected an error with no ISO")
	}
}

func TestAnInvalidProfileStopsBeforeAnythingElse(t *testing.T) {
	s, _ := newStep(t, "gho_abc")
	s.Validate = func(context.Context) error { return os.ErrInvalid }
	if err := s.Apply(context.Background(), ready(), engine.Headless{}); err == nil {
		t.Fatal("expected the profile error")
	}
}
