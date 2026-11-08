package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Adaetum/Adaetum/agent/internal/accounts"
)

const testToken = "an-access-token-that-is-long-enough-0123456789"

func serve(handler http.Handler, method, path, bearer string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(""))
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestWithNothingConfiguredThereIsNoLoginAtAll(t *testing.T) {
	// A loopback development agent: no token, no accounts, as before.
	if combinedGuard("", nil) != nil {
		t.Fatal("a guard appeared with nothing to check")
	}
}

func TestTheTokenAloneBehavesAsItAlwaysHas(t *testing.T) {
	guard := combinedGuard(testToken, nil)
	mux := http.NewServeMux()
	mountAccounts(mux, nil, guard)
	mux.HandleFunc("/api/v1/inspect", func(w http.ResponseWriter, _ *http.Request) {})
	handler := requireLogin(mux, guard)

	if got := serve(handler, "GET", "/api/v1/inspect", "").Code; got != 401 {
		t.Errorf("no token: %d", got)
	}
	if got := serve(handler, "GET", "/api/v1/inspect", testToken).Code; got != 200 {
		t.Errorf("the token: %d", got)
	}
	// The page asks whether accounts exist before anyone is signed in: it must be answered,
	// and say no, so the page asks for the token.
	recorder := serve(handler, "GET", "/api/auth-state", "")
	if recorder.Code != 200 || !strings.Contains(recorder.Body.String(), `"enabled":false`) {
		t.Errorf("auth-state: %d %s", recorder.Code, recorder.Body.String())
	}
	// Nothing about people or setup exists without an auth service.
	if got := serve(handler, "POST", "/api/setup", testToken).Code; got == 200 || got == 201 {
		t.Errorf("setup with no auth service: %d", got)
	}
}

func TestWithAnAuthServiceTheSignInPagesStayOpenAndEverythingElseDoesNot(t *testing.T) {
	// The auth service is unreachable here; what matters is which routes are guarded.
	auth := accounts.NewClient("http://127.0.0.1:1", "secret", nil)
	guard := combinedGuard(testToken, auth)
	mux := http.NewServeMux()
	mountAccounts(mux, auth, guard)
	mux.HandleFunc("/api/v1/inspect", func(w http.ResponseWriter, _ *http.Request) {})
	handler := requireLogin(mux, guard)

	if got := serve(handler, "GET", "/api/v1/inspect", "").Code; got != 401 {
		t.Errorf("an API call with no login: %d", got)
	}
	if got := serve(handler, "GET", "/api/v1/people", "").Code; got != 401 {
		t.Errorf("the people list with no login: %d", got)
	}
	// With the auth service down, the page is told so (and can offer the token) ...
	recorder := serve(handler, "GET", "/api/auth-state", "")
	if recorder.Code != 200 || !strings.Contains(recorder.Body.String(), `"unavailable":true`) {
		t.Errorf("auth-state with the auth service down: %d %s", recorder.Code, recorder.Body.String())
	}
	// ... and the token works regardless.
	if got := serve(handler, "GET", "/api/v1/inspect", testToken).Code; got != 200 {
		t.Errorf("the token with the auth service down: %d", got)
	}
	// What the browser must never reach through the agent.
	for _, path := range []string{"/internal/state", "/internal/snapshot", "/internal/people"} {
		if got := serve(handler, "GET", path, testToken).Code; got == 200 {
			t.Errorf("%s answered 200", path)
		}
	}
}

func TestAnAccountIsNamedInTheRecordAndTheTokenIsNot(t *testing.T) {
	request := httptest.NewRequest("GET", "/", nil)
	if personName(request) != "" {
		t.Error("a request with no identity named someone")
	}
}
