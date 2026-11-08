package platform

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Adaetum/Adaetum/installer/sys"
)

func TestStandardDerivesTheLocalDomainByReplacingTheSuffix(t *testing.T) {
	p := Standard("example.test", "tail1234.ts.net")
	if p.LocalDomain != "example.local" {
		t.Fatalf("local domain %q, want example.local (not example.test.local)", p.LocalDomain)
	}
	if p.BootstrapURL != "https://bootstrap.example.test" || p.OverlayTag != "tag:cluster" || p.R2Bucket != "iso" {
		t.Fatalf("unexpected defaults: %+v", p)
	}
}

func TestShowParsesKeyValueLines(t *testing.T) {
	f := &sys.Fake{Rules: []sys.Rule{{Prefix: "python3 ./tasks/scripts/configure-platform-profile.py --show",
		Result: sys.Result{Stdout: "domain=adaetum.invalid\noverlay_domain=example-tailnet.ts.net\n"}}}}
	p, err := Scripts{Run: f}.Show(context.Background())
	if err != nil || p["domain"] != "adaetum.invalid" {
		t.Fatalf("%v %v", p, err)
	}
	if !p.Placeholder() {
		t.Fatal("the template profile was not recognised as a placeholder")
	}
}

func TestAConfiguredProfileIsNotAPlaceholder(t *testing.T) {
	if (Profile{"domain": "example.com", "overlay_domain": "tail1234.ts.net"}).Placeholder() {
		t.Fatal("a configured profile was taken for a placeholder")
	}
}

func TestRenderStopsOnAnInvalidProfile(t *testing.T) {
	f := &sys.Fake{Rules: []sys.Rule{{Prefix: "python3 ./tasks/scripts/validate-platform-profile.py",
		Result: sys.Result{ExitCode: 1, Stderr: "domain is required"}}}}
	err := Scripts{Run: f}.Render(context.Background())
	if err == nil || !strings.Contains(err.Error(), "domain is required") || f.Ran("python3 ./tasks/scripts/render-platform-profile.py") {
		t.Fatalf("err=%v", err)
	}
}

// The real scripts, run against this repository's checkout. They prove the Go
// side agrees with what the Python renderer actually reports.
func realScripts(t *testing.T) Scripts {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Skip(err)
	}
	if _, err := os.Stat(filepath.Join(root, "tasks/scripts/render-platform-profile.py")); err != nil {
		t.Skip("not running inside the Adaetum checkout")
	}
	if !sys.Has("python3") {
		t.Skip("python3 is not installed")
	}
	return Scripts{Run: sys.Exec{}, Dir: root}
}

func TestTheRealRendererOutputsIncludeFilesTheOldBashListMissed(t *testing.T) {
	out, err := realScripts(t).Outputs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, p := range out {
		have[p] = true
	}
	for _, want := range []string{"platform.yaml", "pods/cluster-config/cluster-config.env", "pods/operations/kured.app.yaml", "pods/ingress/nginx-routing/crafty-ingress.yaml"} {
		if !have[want] {
			t.Errorf("the renderer's output list lacks %s", want)
		}
	}
}

func TestTheRealProfileValidatesAndYieldsSetupValues(t *testing.T) {
	s := realScripts(t)
	if err := s.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
	v, err := s.SetupValues(context.Background())
	if err != nil || v.KSBaseURL == "" || v.TailscaleDomain == "" || v.R2Bucket == "" {
		t.Fatalf("%+v %v", v, err)
	}
}
