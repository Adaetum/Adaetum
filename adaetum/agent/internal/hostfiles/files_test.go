package hostfiles

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestMkdirAllMakesTheDirectoryExactlyPrivateEvenIfItExisted(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "secrets")
	if err := (FS{}).MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(dir); info.Mode().Perm() != 0o700 {
		t.Fatalf("mode %v", info.Mode().Perm())
	}
	// An existing, too-open directory is tightened, not left as it was.
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := (FS{}).MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(dir); info.Mode().Perm() != 0o700 {
		t.Fatalf("an existing directory was left at %v", info.Mode().Perm())
	}
}

func TestWriteAtomicCreatesAPrivateFileAndReplacesInOneStep(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	if err := (FS{}).WriteAtomic(path, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v err %v", info.Mode().Perm(), err)
	}
	if err := (FS{}).WriteAtomic(path, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _ := (FS{}).Read(path); string(got) != "second" {
		t.Fatalf("got %q", got)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("a temporary file was left behind: %v", entries)
	}
}

func TestWriteAtomicLeavesNothingWhenItFails(t *testing.T) {
	dir := t.TempDir()
	if err := (FS{}).WriteAtomic(filepath.Join(dir, "missing-dir", "secret"), []byte("x"), 0o600); err == nil {
		t.Fatal("writing into a missing directory should fail")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("a failed write left %v", entries)
	}
}

func TestAMissingFileSaysSo(t *testing.T) {
	if _, err := (FS{}).Stat(filepath.Join(t.TempDir(), "none")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("got %v", err)
	}
}
