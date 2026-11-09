package finalize

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Adaetum/Adaetum/installer/engine"
)

const iso = "Rocky-10.2-x86_64-minimal.iso"

func setup(t *testing.T) (*Step, *engine.Env, string) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "dist"), 0o755)
	for _, f := range []string{"platform.yaml", ".env"} {
		os.WriteFile(filepath.Join(root, f), []byte("x"), 0o644)
	}
	out := filepath.Join(root, "dist", "Rocky-10.2-x86_64-minimal-ks.iso")
	os.WriteFile(out, []byte("installer-bytes"), 0o644)
	old := time.Now().Add(-time.Hour)
	os.Chtimes(filepath.Join(root, "platform.yaml"), old, old)
	os.Chtimes(filepath.Join(root, ".env"), old, old)

	s := &Step{Root: root, DownloadDir: filepath.Join(t.TempDir(), "Downloads")}
	env := &engine.Env{}
	env.SetValue("iso_path", filepath.Join(root, iso))
	return s, env, out
}

func TestFinalizeOffersACopyAndSavesIt(t *testing.T) {
	s, env, out := setup(t)
	if err := s.Apply(context.Background(), env, engine.Headless{}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(s.DownloadDir, filepath.Base(out)))
	if err != nil || string(got) != "installer-bytes" {
		t.Fatalf("%v %q", err, got)
	}
	if env.Value("installer_iso") != out {
		t.Fatalf("installer_iso = %q", env.Value("installer_iso"))
	}
	entries, _ := os.ReadDir(s.DownloadDir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".partial") {
			t.Fatal("a partial file was left behind")
		}
	}
}

func TestDecliningTheCopyLeavesDownloadsAlone(t *testing.T) {
	s, env, _ := setup(t)
	if err := s.Apply(context.Background(), env, engine.Headless{Answers: map[string]string{"download-installer": "no"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(s.DownloadDir); err == nil {
		t.Fatal("the downloads folder was created after the operator declined")
	}
}

func TestAStaleInstallerIsRefused(t *testing.T) {
	s, env, out := setup(t)
	past := time.Now().Add(-48 * time.Hour)
	os.Chtimes(out, past, past)
	err := s.Apply(context.Background(), env, engine.Headless{})
	if err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("err = %v", err)
	}
}

func TestAMissingInstallerIsRefused(t *testing.T) {
	s, env, out := setup(t)
	os.Remove(out)
	if err := s.Apply(context.Background(), env, engine.Headless{}); err == nil {
		t.Fatal("expected an error for a missing ISO")
	}
}

func TestAnOlderCopyInDownloadsIsReplaced(t *testing.T) {
	s, env, out := setup(t)
	os.MkdirAll(s.DownloadDir, 0o755)
	dest := filepath.Join(s.DownloadDir, filepath.Base(out))
	os.WriteFile(dest, []byte("obsolete installer"), 0o644)
	if err := s.Apply(context.Background(), env, engine.Headless{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(dest); string(got) != "installer-bytes" {
		t.Fatalf("the stale copy was kept: %q", got)
	}
}

func TestAnIdenticalCopyIsLeftAlone(t *testing.T) {
	s, env, out := setup(t)
	os.MkdirAll(s.DownloadDir, 0o755)
	dest := filepath.Join(s.DownloadDir, filepath.Base(out))
	os.WriteFile(dest, []byte("installer-bytes"), 0o644)
	before, _ := os.Stat(dest)
	time.Sleep(10 * time.Millisecond)
	if err := s.Apply(context.Background(), env, engine.Headless{}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(dest)
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("an identical copy was rewritten")
	}
}

func TestRetainsISOOutsideTemporaryCheckout(t *testing.T) {
	s, env, _ := setup(t)
	s.OutputRoot = t.TempDir()
	if err := s.Apply(context.Background(), env, engine.Headless{}); err != nil {
		t.Fatal(err)
	}
	retained := filepath.Join(s.OutputRoot, "dist", strings.TrimSuffix(iso, ".iso")+"-ks.iso")
	if data, err := os.ReadFile(retained); err != nil || string(data) != "installer-bytes" {
		t.Fatalf("retained ISO %q: %v", data, err)
	}
	if env.Value("installer_iso") != retained {
		t.Fatalf("result points to %s", env.Value("installer_iso"))
	}
}
