package media

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverFindsLaunchCheckoutMedia(t *testing.T) {
	source := t.TempDir()
	path := filepath.Join(source, "Rocky-10.2-x86_64-minimal.iso")
	if err := os.WriteFile(path, []byte("iso"), 0644); err != nil {
		t.Fatal(err)
	}
	library := Library{Root: t.TempDir(), ExtraDirs: []string{source}}
	found := library.Discover()
	if len(found) != 1 || found[0].Path != path {
		t.Fatalf("found=%v", found)
	}
}
