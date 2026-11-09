package checkout

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	out, e := c.CombinedOutput()
	if e != nil {
		t.Fatalf("git %v: %v %s", args, e, out)
	}
	return strings.TrimSpace(string(out))
}
func TestPrepareKeepsSourceGitStateAndSecretsUntouched(t *testing.T) {
	root := t.TempDir()
	gitRun(t, root, "init", "-b", "feature")
	gitRun(t, root, "config", "user.name", "test")
	gitRun(t, root, "config", "user.email", "test@example.invalid")
	os.WriteFile(filepath.Join(root, "platform.yaml"), []byte("safe\n"), 0600)
	os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".env\n"), 0600)
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-m", "source")
	gitRun(t, root, "remote", "add", "origin", "https://github.com/Adaetum/Adaetum.git")
	os.WriteFile(filepath.Join(root, ".env"), []byte("secret"), 0600)
	head := gitRun(t, root, "rev-parse", "HEAD")
	w, err := Prepare(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if got := gitRun(t, w.Root, "branch", "--show-current"); got != "feature" {
		t.Fatalf("branch=%s", got)
	}
	if _, err := os.Stat(filepath.Join(w.Root, ".env")); !os.IsNotExist(err) {
		t.Fatal("copied source secrets")
	}
	gitRun(t, w.Root, "remote", "set-url", "origin", "https://github.com/example/recovery.git")
	if got := gitRun(t, root, "remote", "get-url", "origin"); got != "https://github.com/Adaetum/Adaetum.git" {
		t.Fatalf("source origin changed: %s", got)
	}
	if got := gitRun(t, root, "rev-parse", "HEAD"); got != head {
		t.Fatal("source HEAD changed")
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(w.Root); !os.IsNotExist(err) {
		t.Fatal("temporary checkout remained")
	}
}
func TestPrepareRejectsDirtyTrackedSource(t *testing.T) {
	root := t.TempDir()
	gitRun(t, root, "init", "-b", "main")
	gitRun(t, root, "config", "user.name", "test")
	gitRun(t, root, "config", "user.email", "test@example.invalid")
	p := filepath.Join(root, "platform.yaml")
	os.WriteFile(p, []byte("before"), 0600)
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-m", "initial")
	os.WriteFile(p, []byte("after"), 0600)
	if _, err := Prepare(context.Background(), root); err == nil {
		t.Fatal("accepted uncommitted tracked files")
	}
	data, _ := os.ReadFile(p)
	if string(data) != "after" {
		t.Fatal("modified source")
	}
}
func TestPrepareCopiesArchiveWithoutSecrets(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "platform.yaml"), []byte("safe"), 0600)
	os.WriteFile(filepath.Join(root, ".env"), []byte("secret"), 0600)
	os.Mkdir(filepath.Join(root, "installer"), 0755)
	os.WriteFile(filepath.Join(root, "installer", "main.go"), []byte("source"), 0644)
	w, err := Prepare(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err := os.Stat(filepath.Join(w.Root, ".env")); !os.IsNotExist(err) {
		t.Fatal("copied archive secrets")
	}
	if _, err := os.Stat(filepath.Join(w.Root, "installer", "main.go")); err != nil {
		t.Fatal(err)
	}
}

func TestIsolatedCheckoutCanPublishWithoutChangingSource(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	private := filepath.Join(root, "private.git")
	gitRun(t, root, "init", "--bare", "-b", "main", private)
	gitRun(t, root, "init", "-b", "feature", source)
	gitRun(t, source, "config", "user.name", "test")
	gitRun(t, source, "config", "user.email", "test@example.invalid")
	os.WriteFile(filepath.Join(source, "platform.yaml"), []byte("source profile\n"), 0644)
	gitRun(t, source, "add", ".")
	gitRun(t, source, "commit", "-m", "source")
	before := gitRun(t, source, "rev-parse", "HEAD")
	w, err := Prepare(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	gitRun(t, w.Root, "remote", "set-url", "origin", private)
	os.WriteFile(filepath.Join(w.Root, "platform.yaml"), []byte("recovery profile\n"), 0644)
	gitRun(t, w.Root, "add", "platform.yaml")
	gitRun(t, w.Root, "commit", "-m", "recovery")
	gitRun(t, w.Root, "push", "origin", "HEAD:refs/heads/main")
	if got := gitRun(t, private, "show", "main:platform.yaml"); got != "recovery profile" {
		t.Fatalf("private profile %q", got)
	}
	if got := gitRun(t, source, "rev-parse", "HEAD"); got != before {
		t.Fatal("source HEAD changed")
	}
	if got := gitRun(t, source, "branch", "--show-current"); got != "feature" {
		t.Fatal("source branch changed")
	}
	if data, _ := os.ReadFile(filepath.Join(source, "platform.yaml")); string(data) != "source profile\n" {
		t.Fatal("source profile changed")
	}
}

func TestPreparePreservesOnlyBackupPassphraseForReruns(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "platform.yaml"), []byte("safe"), 0600)
	os.Mkdir(filepath.Join(root, "installer"), 0755)
	os.WriteFile(filepath.Join(root, ".env"), []byte("BOOTSTRAP_BACKUP_PASSPHRASE=keep-old-backup\nCLOUDFLARE_API_TOKEN=do-not-copy\n"), 0600)
	w, err := Prepare(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	data, err := os.ReadFile(filepath.Join(w.Root, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "BOOTSTRAP_BACKUP_PASSPHRASE=keep-old-backup") || strings.Contains(string(data), "do-not-copy") {
		t.Fatal("copied incorrect credential state")
	}
}

func TestRetainInstallersSurvivesWorkspaceCleanup(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "platform.yaml"), []byte("safe"), 0600)
	os.Mkdir(filepath.Join(root, "installer"), 0755)
	w, err := Prepare(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(w.Root, "dist"), 0755)
	os.WriteFile(filepath.Join(w.Root, "dist", "server-ks.iso"), []byte("built installer"), 0644)
	paths, err := w.RetainInstallers(root)
	if err != nil || len(paths) != 1 {
		t.Fatalf("paths %v err %v", paths, err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(paths[0]); err != nil || string(data) != "built installer" {
		t.Fatalf("retained data %q: %v", data, err)
	}
}
