package main

import (
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWhichAddressesAcceptOtherMachines(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:8080":      false,
		"localhost:8080":      false,
		"[::1]:8080":          false,
		"0.0.0.0:8443":        true,
		":8443":               true,
		"[::]:8443":           true,
		"192.168.2.233:8443":  true,
		"100.97.103.126:8443": true,
		"node-a:8443":         true,
	}
	for address, want := range cases {
		got, err := listensBeyondLoopback(address)
		if err != nil || got != want {
			t.Errorf("%s: want %v, got %v (%v)", address, want, got, err)
		}
	}
	if _, err := listensBeyondLoopback("8443"); err == nil {
		t.Error("an address without a port must be rejected, not guessed")
	}
}

func TestATokenIsOnlyAcceptedWhenItIsLongPrivateAndReadable(t *testing.T) {
	long := strings.Repeat("a", minTokenBytes)
	cases := []struct {
		name   string
		path   string
		value  string
		mode   fs.FileMode
		err    error
		usable bool
	}{
		{"good", "/t", long, 0o600, nil, true},
		{"no path", "", long, 0o600, nil, false},
		{"unreadable", "/t", "", 0, errors.New("no such file"), false},
		{"world readable", "/t", long, 0o644, nil, false},
		{"group readable", "/t", long, 0o640, nil, false},
		{"too short", "/t", "short", 0o600, nil, false},
	}
	for _, c := range cases {
		sys := system{readToken: func(string) (string, fs.FileMode, error) { return c.value, c.mode, c.err }}
		token, reason := checkToken(c.path, sys)
		if c.usable != (reason == "" && token == c.value) {
			t.Errorf("%s: usable=%v but got token %q reason %q", c.name, c.usable, token, reason)
		}
		if !c.usable && reason == "" {
			t.Errorf("%s: a refusal must say why", c.name)
		}
	}
}

func TestEveryAPIRouteNeedsTheTokenButTheInterfaceFilesDoNot(t *testing.T) {
	token := strings.Repeat("t", minTokenBytes)
	handler := requireToken(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) }), token)

	status := func(path, header string) int {
		request := httptest.NewRequest("GET", path, nil)
		if header != "" {
			request.Header.Set("Authorization", header)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Code
	}

	for _, path := range []string{"/api", "/api/", "/api/v1/inspect", "/api/v1/automation", "/api/v1/images/history", "/api/v1/images/apply", "/api/anything"} {
		if status(path, "") != http.StatusUnauthorized {
			t.Errorf("%s must require the token", path)
		}
	}
	// The pages are the same static files for every host and must load before sign-in.
	for _, path := range []string{"/", "/healthz", "/assets/app.js", "/automation", "/apix"} {
		if status(path, "") != http.StatusOK {
			t.Errorf("%s holds no host data and must stay open", path)
		}
	}
	if status("/api/v1/inspect", "Bearer "+token) != http.StatusOK {
		t.Error("the right token must be accepted")
	}
	for _, bad := range []string{"Bearer wrong", "Bearer " + token + "x", "Bearer ", "Basic " + token, token, "bearer " + token} {
		if status("/api/v1/inspect", bad) != http.StatusUnauthorized {
			t.Errorf("%q must be refused", bad)
		}
	}
}

func TestTheUnauthorizedReplyAsksForABearerToken(t *testing.T) {
	handler := requireToken(http.NotFoundHandler(), strings.Repeat("t", minTokenBytes))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest("GET", "/api/v1/inspect", nil))
	if !strings.Contains(recorder.Header().Get("WWW-Authenticate"), "Bearer") {
		t.Fatal("clients need to know how to authenticate")
	}
}
