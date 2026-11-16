package about

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const good = `summary: Photo library with face search.
facts:
  - label: Where its data lives
    value: A volume called immich-library.
watch:
  - The machine learning pod restarts often.
written:
  by: assistant
  date: 2026-11-16
  version: 1.2.3
`

func TestParseAcceptsAGoodFileAndRejectsBadOnes(t *testing.T) {
	if _, err := Parse([]byte(good)); err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string]string{
		"unknown field": good + "extra: 1\n",
		"markup":        strings.Replace(good, "Photo library", "<b>Photo</b> library", 1),
		"no summary":    strings.Replace(good, "Photo library with face search.", "", 1),
		"bad author":    strings.Replace(good, "assistant", "robot", 1),
		"bad date":      strings.Replace(good, "2026-11-16", "yesterday", 1),
		"too long":      strings.Replace(good, "Photo library with face search.", strings.Repeat("x", 500), 1),
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestName(t *testing.T) {
	if name, ok := Name("pods/media/plex.about.yaml"); !ok || name != "plex" {
		t.Fatalf("got %q %v", name, ok)
	}
	for _, path := range []string{"pods/media/plex.app.yaml", "docs/plex.about.yaml", "pods/.about.yaml"} {
		if _, ok := Name(path); ok {
			t.Errorf("%s should not match", path)
		}
	}
}

type fakeSource struct {
	paths []string
	files map[string]string
	fail  error
	calls int
}

func (f *fakeSource) Paths(context.Context) ([]string, error) {
	f.calls++
	return f.paths, f.fail
}
func (f *fakeSource) File(_ context.Context, path string) ([]byte, error) {
	if text, ok := f.files[path]; ok {
		return []byte(text), nil
	}
	return nil, errors.New("missing")
}

func get(api *API, app string) string {
	mux := http.NewServeMux()
	api.Register(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/about/"+app, nil))
	return rec.Body.String()
}

func TestServeAnswersPerApp(t *testing.T) {
	source := &fakeSource{paths: []string{"pods/media/plex.about.yaml", "pods/x/bad.about.yaml"}, files: map[string]string{"pods/media/plex.about.yaml": good, "pods/x/bad.about.yaml": "nope: 1\n"}}
	api := &API{Source: source}
	if body := get(api, "plex"); !strings.Contains(body, "Photo library") {
		t.Errorf("plex: %s", body)
	}
	if body := get(api, "bad"); !strings.Contains(body, "not valid") {
		t.Errorf("bad: %s", body)
	}
	if body := get(api, "other"); !strings.Contains(body, `"about":null`) {
		t.Errorf("other: %s", body)
	}
	if source.calls != 1 {
		t.Errorf("the index should be read once, was read %d times", source.calls)
	}
}

func TestAnUnreadableRepositoryIsNoDescription(t *testing.T) {
	api := &API{Source: &fakeSource{fail: errors.New("down")}}
	if body := get(api, "plex"); !strings.Contains(body, `"about":null`) {
		t.Errorf("got %s", body)
	}
}
