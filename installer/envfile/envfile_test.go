package envfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUpsertReplacesInPlaceAndKeepsTheRest(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(p, []byte("A=1\nB=two=parts\nC=3\n"), 0o644)
	if err := Upsert(p, "B", "new"); err != nil {
		t.Fatal(err)
	}
	if err := Upsert(p, "D", "4"); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "A=1\nB=new\nC=3\nD=4\n" {
		t.Fatalf("got %q", got)
	}
	if info, _ := os.Stat(p); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v, want owner-only", info.Mode().Perm())
	}
}

func TestReadKeepsEqualsSignsInValuesAndToleratesAMissingFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(p, []byte("K=a=b==\n#C=x\n"), 0o600)
	if got := Read(p); got["K"] != "a=b==" || len(got) != 1 {
		t.Fatalf("%v", got)
	}
	if got := Read(filepath.Join(t.TempDir(), "none")); len(got) != 0 {
		t.Fatal("a missing file should read as empty")
	}
}
