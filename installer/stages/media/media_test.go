package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Adaetum/Adaetum/installer/engine"
	"github.com/Adaetum/Adaetum/installer/sys"
)

const name = "Rocky-10.2-x86_64-minimal.iso"

var image = []byte(strings.Repeat("rocky-installer-bytes-", 4000))

func digestOf(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// mirror serves the image with range support, as Rocky's does.
func mirror(t *testing.T, sum string) (*httptest.Server, *int) {
	t.Helper()
	ranged := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/x86_64/"+name+".CHECKSUM", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "# comment\nSHA256 (%s) = %s\n", name, sum)
	})
	mux.HandleFunc("/x86_64/"+name, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			ranged++
		}
		http.ServeContent(w, r, name, time.Time{}, strings.NewReader(string(image)))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &ranged
}

func lib(t *testing.T, srv *httptest.Server) Library {
	return Library{Root: t.TempDir(), Home: t.TempDir(), Mirror: srv.URL}
}

func TestDiscoverFindsOnlySupportedImagesAndDedupes(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(home, "Downloads"), 0o755)
	for _, p := range []string{
		filepath.Join(root, name),
		filepath.Join(root, "Rocky-10.2-x86_64-boot.iso"),   // boot ISO: unsupported
		filepath.Join(root, "Rocky-9.4-x86_64-minimal.iso"), // wrong major version
		filepath.Join(home, "Downloads", "Rocky-10-aarch64-dvd1.iso"),
		filepath.Join(root, "notes.txt"),
	} {
		os.WriteFile(p, []byte("x"), 0o644)
	}
	got := Library{Root: root, Home: home}.Discover()
	if len(got) != 2 {
		t.Fatalf("found %d ISOs: %+v", len(got), got)
	}
	var dvd ISO
	for _, i := range got {
		if i.Arch == "aarch64" {
			dvd = i
		}
	}
	if dvd.Type != "dvd" || dvd.Version != "Rocky 10.0" {
		t.Fatalf("dvd1 / unversioned name parsed as %+v", dvd)
	}
}

func TestVerifyAcceptsTheRightImageAndRejectsATamperedOne(t *testing.T) {
	srv, _ := mirror(t, digestOf(image))
	l := lib(t, srv)
	good := filepath.Join(l.Root, name)
	os.WriteFile(good, image, 0o644)
	if err := l.Verify(context.Background(), good); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(good, append([]byte("x"), image...), 0o644)
	if err := l.Verify(context.Background(), good); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("a tampered image verified: %v", err)
	}
}

func TestDownloadVerifiesAndInstallsTheImage(t *testing.T) {
	srv, _ := mirror(t, digestOf(image))
	l := lib(t, srv)
	var last int64
	path, err := l.Download(context.Background(), "x86_64", "minimal", "10.2", func(done, _ int64) { last = done })
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(image) || last != int64(len(image)) {
		t.Fatalf("downloaded %d bytes, progress ended at %d", len(got), last)
	}
	if _, err := os.Stat(path + ".partial"); err == nil {
		t.Fatal("the partial file was left behind")
	}
}

func TestDownloadResumesAPartialFile(t *testing.T) {
	srv, ranged := mirror(t, digestOf(image))
	l := lib(t, srv)
	os.WriteFile(filepath.Join(l.Root, name+".partial"), image[:30000], 0o644)
	path, err := l.Download(context.Background(), "x86_64", "minimal", "10.2", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != string(image) {
		t.Fatal("the resumed image is wrong")
	}
	if *ranged == 0 {
		t.Fatal("the download did not resume with a range request")
	}
}

func TestAChecksumMismatchKeepsThePartialAndInstallsNothing(t *testing.T) {
	srv, _ := mirror(t, strings.Repeat("0", 64))
	l := lib(t, srv)
	if _, err := l.Download(context.Background(), "x86_64", "minimal", "10.2", nil); err == nil {
		t.Fatal("a mismatched image was accepted")
	}
	if _, err := os.Stat(filepath.Join(l.Root, name)); err == nil {
		t.Fatal("an unverified ISO was installed")
	}
	if _, err := os.Stat(filepath.Join(l.Root, name+".partial")); err != nil {
		t.Fatal("the partial file was not kept for resuming")
	}
}

func TestDownloadRefusesAnExistingImageThatDoesNotVerify(t *testing.T) {
	srv, _ := mirror(t, digestOf(image))
	l := lib(t, srv)
	os.WriteFile(filepath.Join(l.Root, name), []byte("corrupt"), 0o644)
	if _, err := l.Download(context.Background(), "x86_64", "minimal", "10.2", nil); err == nil {
		t.Fatal("a corrupt existing image was reused")
	}
}

func TestAdoptCopiesButNeverOverwrites(t *testing.T) {
	l := Library{Root: t.TempDir()}
	src := filepath.Join(t.TempDir(), name)
	os.WriteFile(src, []byte("a"), 0o644)
	dest, err := l.Adopt(src)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(dest); string(got) != "a" {
		t.Fatal("copy is wrong")
	}
	if _, err := l.Adopt(src); err == nil {
		t.Fatal("an existing ISO in the checkout was overwritten")
	}
	if _, err := l.Adopt(filepath.Join(t.TempDir(), "evil.iso")); err == nil {
		t.Fatal("an unsupported file name was adopted")
	}
}

func TestApplyUsesAnExistingImageFromDownloadsAndCopiesItIn(t *testing.T) {
	srv, _ := mirror(t, digestOf(image))
	l := lib(t, srv)
	os.MkdirAll(filepath.Join(l.Home, "Downloads"), 0o755)
	os.WriteFile(filepath.Join(l.Home, "Downloads", name), image, 0o644)
	f := &sys.Fake{Rules: []sys.Rule{{Prefix: "python3 ./.validator/validate-setup-readiness.py", Result: sys.Result{}}}}
	s := &Step{Lib: l, Run: f, Arch: "x86_64"}
	env := &engine.Env{}
	if err := s.Apply(context.Background(), env, engine.Headless{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(l.Root, name)); err != nil {
		t.Fatal("the ISO was not copied into the checkout")
	}
	if env.Value("iso_path") != filepath.Join(l.Root, name) {
		t.Fatalf("iso_path = %q", env.Value("iso_path"))
	}
}

func TestApplyDownloadsWhenNothingIsFoundUsingTheOperatorsChoices(t *testing.T) {
	srv, _ := mirror(t, digestOf(image))
	l := lib(t, srv)
	f := &sys.Fake{Rules: []sys.Rule{{Prefix: "python3 ./.validator/validate-setup-readiness.py", Result: sys.Result{}}}}
	s := &Step{Lib: l, Run: f, Arch: "x86_64"}
	ui := engine.Headless{Answers: map[string]string{"media-type": "minimal", "media-arch": "x86_64"}}
	if err := s.Apply(context.Background(), &engine.Env{}, ui); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(l.Root, name)); err != nil {
		t.Fatal("the ISO was not downloaded")
	}
}

func TestAChoiceThatIsNotOnOfferIsRefused(t *testing.T) {
	srv, _ := mirror(t, digestOf(image))
	l := lib(t, srv)
	s := &Step{Lib: l, Run: &sys.Fake{}, Arch: "x86_64"}
	err := s.Apply(context.Background(), &engine.Env{}, engine.Headless{Answers: map[string]string{"media-type": "boot"}})
	if err == nil {
		t.Fatal("an unsupported image type was accepted")
	}
}

func TestAFailingReadinessCheckStopsSetup(t *testing.T) {
	srv, _ := mirror(t, digestOf(image))
	l := lib(t, srv)
	os.WriteFile(filepath.Join(l.Root, name), image, 0o644)
	f := &sys.Fake{Rules: []sys.Rule{{Prefix: "python3 ./.validator/validate-setup-readiness.py", Result: sys.Result{ExitCode: 1, Stdout: "missing GITHUB token"}}}}
	err := (&Step{Lib: l, Run: f, Arch: "x86_64"}).Apply(context.Background(), &engine.Env{}, engine.Headless{})
	if err == nil || !strings.Contains(err.Error(), "missing GITHUB token") {
		t.Fatalf("err = %v", err)
	}
}

func TestAReplayRunNeverStartsADownload(t *testing.T) {
	srv, _ := mirror(t, digestOf(image))
	l := lib(t, srv)
	s := &Step{Lib: l, Run: &sys.Fake{}, Arch: "x86_64"}
	err := s.Apply(context.Background(), &engine.Env{}, engine.Headless{Replay: true})
	if !errors.Is(err, engine.ErrNotReplayable) {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(l.Root, name+".partial")); statErr == nil {
		t.Fatal("a download was started")
	}
}
