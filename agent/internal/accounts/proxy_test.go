package accounts

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// An echo backend, to see exactly what the proxy lets through.
func echo(t *testing.T) (*Client, *http.Request) {
	t.Helper()
	var seen http.Request
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = *r.Clone(r.Context())
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(backend.Close)
	return NewClient(backend.URL, "secret", nil), &seen
}

func TestProxyOnlyReachesTheSignInRoutes(t *testing.T) {
	client, _ := echo(t)
	proxy := client.PublicProxy()
	for _, path := range []string{"/internal/state", "/internal/snapshot", "/internal/people", "/api/v1/people", "/api/auth", "/api/authx/sign-in", "/anything", "/"} {
		recorder := httptest.NewRecorder()
		proxy.ServeHTTP(recorder, httptest.NewRequest("GET", path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Errorf("%s reached the auth service (%d)", path, recorder.Code)
		}
	}
	for _, path := range []string{"/api/auth/get-session", "/api/auth/sign-in/email", "/api/setup", "/api/auth-state"} {
		recorder := httptest.NewRecorder()
		proxy.ServeHTTP(recorder, httptest.NewRequest("GET", path, nil))
		if recorder.Code != http.StatusOK {
			t.Errorf("%s was not passed on (%d)", path, recorder.Code)
		}
	}
}

func TestProxyKeepsTheBrowsersHostAndDropsAnyInternalSecret(t *testing.T) {
	client, seen := echo(t)
	request := httptest.NewRequest("POST", "/api/auth/sign-in/email", strings.NewReader("{}"))
	request.Host = "host-a.example:8443"
	request.Header.Set(InternalHeader, "a browser tried to send this")
	request.Header.Set("Origin", "https://host-a.example:8443")
	recorder := httptest.NewRecorder()
	client.PublicProxy().ServeHTTP(recorder, request)
	if seen.Host != "host-a.example:8443" {
		t.Errorf("the auth service saw Host %q: its origin check would refuse every sign-in", seen.Host)
	}
	if seen.Header.Get(InternalHeader) != "" {
		t.Error("a browser-supplied internal secret reached the auth service")
	}
	if seen.Header.Get("Origin") != "https://host-a.example:8443" {
		t.Error("Origin was not passed on")
	}
}

func TestProxyRefusesALargeBody(t *testing.T) {
	client, _ := echo(t)
	recorder := httptest.NewRecorder()
	client.PublicProxy().ServeHTTP(recorder, httptest.NewRequest("POST", "/api/auth/sign-in/email", strings.NewReader(strings.Repeat("x", maxPublicBody+10))))
	if recorder.Code == http.StatusOK {
		t.Fatalf("a %d byte sign-in form was accepted", maxPublicBody+10)
	}
}

func TestProxySaysAccountsAreDownInsteadOfFailing(t *testing.T) {
	client := NewClient("http://127.0.0.1:1", "secret", nil) // nothing listens there
	recorder := httptest.NewRecorder()
	client.PublicProxy().ServeHTTP(recorder, httptest.NewRequest("GET", "/api/auth-state", nil))
	if recorder.Code != 200 || !strings.Contains(recorder.Body.String(), `"unavailable":true`) {
		t.Fatalf("auth-state while the service is down: %d %s", recorder.Code, recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	client.PublicProxy().ServeHTTP(recorder, httptest.NewRequest("POST", "/api/auth/sign-in/email", strings.NewReader("{}")))
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("sign-in while the service is down: %d", recorder.Code)
	}
}

func TestAnAgentWithoutAuthSaysSoAndExposesNothing(t *testing.T) {
	mux := http.NewServeMux()
	RegisterPublic(mux, Disabled())
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest("GET", "/api/auth-state", nil))
	if !strings.Contains(recorder.Body.String(), `"enabled":false`) {
		t.Fatalf("got %s", recorder.Body.String())
	}
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest("POST", "/api/setup", strings.NewReader("{}")))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("setup on an agent with no auth service: %d", recorder.Code)
	}
}
