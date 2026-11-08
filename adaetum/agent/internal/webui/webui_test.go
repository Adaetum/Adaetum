package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

var built = fstest.MapFS{
	"index.html":        {Data: []byte("<html>app</html>")},
	"assets/app-abc.js": {Data: []byte("console.log(1)")},
	"favicon.svg":       {Data: []byte("<svg/>")},
}

func get(handler http.Handler, method, path string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(method, path, nil))
	return recorder
}

func TestTheRootAndDeepLinksServeTheApp(t *testing.T) {
	handler := FromFS(built)
	for _, path := range []string{"/", "/index.html", "/automation", "/apps/something/deep"} {
		recorder := get(handler, "GET", path)
		if recorder.Code != 200 || !strings.Contains(recorder.Body.String(), "app") {
			t.Errorf("%s must serve the app: %d %q", path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestAMissingAssetIsA404NotTheApp(t *testing.T) {
	// Serving index.html for a missing script would make the browser try to run HTML.
	if code := get(FromFS(built), "GET", "/assets/gone.js").Code; code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", code)
	}
}

func TestAnUnclaimedAPIPathIsA404NotTheApp(t *testing.T) {
	for _, path := range []string{"/api", "/api/v1/cluster", "/api/v1/anything/else"} {
		recorder := get(FromFS(built), "GET", path)
		if recorder.Code != http.StatusNotFound || strings.Contains(recorder.Body.String(), "app") {
			t.Errorf("%s must be a plain 404: %d %q", path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestHashedAssetsCacheForeverButThePageNever(t *testing.T) {
	handler := FromFS(built)
	if cache := get(handler, "GET", "/assets/app-abc.js").Header().Get("Cache-Control"); !strings.Contains(cache, "immutable") {
		t.Fatalf("hashed assets are immutable: %q", cache)
	}
	for _, path := range []string{"/", "/automation", "/favicon.svg"} {
		if cache := get(handler, "GET", path).Header().Get("Cache-Control"); cache != "no-cache" {
			t.Errorf("%s must not be cached: %q", path, cache)
		}
	}
}

func TestEveryResponseCarriesSecurityHeaders(t *testing.T) {
	for _, path := range []string{"/", "/assets/app-abc.js", "/assets/gone.js"} {
		headers := get(FromFS(built), "GET", path).Header()
		if !strings.Contains(headers.Get("Content-Security-Policy"), "default-src 'self'") ||
			headers.Get("X-Content-Type-Options") != "nosniff" || headers.Get("X-Frame-Options") != "DENY" {
			t.Errorf("%s is missing security headers: %v", path, headers)
		}
	}
}

func TestOnlyReadsAreAllowed(t *testing.T) {
	for _, method := range []string{"POST", "PUT", "DELETE"} {
		if code := get(FromFS(built), method, "/").Code; code != http.StatusMethodNotAllowed {
			t.Errorf("%s must be refused: %d", method, code)
		}
	}
}

func TestPathTricksCannotEscapeTheInterfaceFiles(t *testing.T) {
	for _, path := range []string{"/../../etc/passwd", "/%2e%2e/%2e%2e/etc/passwd", "/assets/../../go.mod"} {
		recorder := get(FromFS(built), "GET", path)
		if strings.Contains(recorder.Body.String(), "root:") || strings.Contains(recorder.Body.String(), "module ") {
			t.Errorf("%s escaped the interface directory", path)
		}
	}
}

func TestWithoutABuildTheHandlerSaysSoPlainly(t *testing.T) {
	recorder := get(FromFS(fstest.MapFS{"placeholder.txt": {}}), "GET", "/")
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "npm run build") {
		t.Fatalf("expected a clear 503: %d %q", recorder.Code, recorder.Body.String())
	}
}

func TestTheEmbeddedHandlerCompilesAndRunsBeforeAnyBuildExists(t *testing.T) {
	// ui/dist is not committed, so in a fresh checkout the embedded interface is absent.
	if code := get(Handler(), "GET", "/").Code; code != http.StatusServiceUnavailable && code != http.StatusOK {
		t.Fatalf("unexpected status %d", code)
	}
}
