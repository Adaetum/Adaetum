package checkout

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindWalksUpToTheCheckout(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "platform.yaml"), []byte("x"), 0o644)
	deep := filepath.Join(root, "installer", "app", "build")
	os.MkdirAll(deep, 0o755)
	t.Setenv("ADAETUM_ROOT", "")
	t.Chdir(deep)
	got, err := Find()
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := filepath.EvalSymlinks(root); got != root && got != want {
		t.Fatalf("got %q want %q", got, root)
	}
}

func TestEnvOverrideMustBeACheckout(t *testing.T) {
	t.Setenv("ADAETUM_ROOT", t.TempDir())
	if _, err := Find(); err == nil {
		t.Fatal("a non-checkout was accepted")
	}
}
