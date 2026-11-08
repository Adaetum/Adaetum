package accounts

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// authService is one real copy of the auth service (auth/ in this repository, run by bun),
// with its own database, listening on a socket. These tests exercise the real thing, so a
// change in Better Auth or in the service's rules shows up here and not in production.
type authService struct {
	Client *Client
	Dir    string
}

func startAuth(t *testing.T, mode string) *authService {
	t.Helper()
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Skip("bun is not installed, so the real auth service cannot be run")
	}
	root, _ := filepath.Abs("../../../auth")
	if _, err := os.Stat(filepath.Join(root, "src", "server.ts")); err != nil {
		t.Skip("auth/ is not present")
	}
	if _, err := os.Stat(filepath.Join(root, "node_modules")); err != nil {
		t.Skip("auth/ has no node_modules: run `bun install` in auth/")
	}
	// Unix socket paths are short on macOS; the test's own temp dir can be too long.
	dir, err := os.MkdirTemp("/tmp", "adaetum-auth-test-")
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "a.sock")
	command := exec.Command(bun, "src/server.ts")
	command.Dir = root
	command.Env = append(os.Environ(),
		"AUTH_SOCKET="+socket, "AUTH_STATE_DIR="+dir, "AUTH_MODE="+mode,
		"AUTH_INTERNAL_SECRET=go-test-internal-secret", "AUTH_SECRET=go-test-cookie-secret-go-test-cookie-secret",
		"AUTH_BASE_URL=http://agent.test", "NODE_ENV=development",
	)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_, _ = command.Process.Wait()
		os.RemoveAll(dir)
	})
	service := &authService{Client: NewUnixClient(socket, "go-test-internal-secret"), Dir: dir}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err := service.Client.AuthState(ctx)
		cancel()
		if err == nil {
			return service
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the auth service did not start")
	return nil
}

// kill stops the service (to test an outage).
func (s *authService) setupCode(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(s.Dir, "setup-code"))
	if err != nil {
		t.Fatalf("no setup code on the machine: %v", err)
	}
	return string(bytes.TrimSpace(data))
}

// browser is a pretend browser: it goes through the Go proxy, keeps cookies, and sends the
// Origin a real browser would.
type browser struct {
	t       *testing.T
	handler http.Handler
	cookie  string
}

func (b *browser) do(method, path string, body any) *httptest.ResponseRecorder {
	b.t.Helper()
	var reader *bytes.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	request := httptest.NewRequest(method, "http://agent.test"+path, reader)
	request.Host = "agent.test"
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		request.Header.Set("Origin", "http://agent.test")
	}
	if b.cookie != "" {
		request.Header.Set("Cookie", b.cookie)
	}
	recorder := httptest.NewRecorder()
	b.handler.ServeHTTP(recorder, request)
	for _, set := range recorder.Result().Cookies() {
		if set.Name == "better-auth.session_token" && set.Value != "" && set.MaxAge >= 0 {
			b.cookie = set.Name + "=" + set.Value
		}
	}
	return recorder
}

func (b *browser) signIn(email, password string) int {
	b.t.Helper()
	return b.do(http.MethodPost, "/api/auth/sign-in/email", map[string]string{"email": email, "password": password}).Code
}

const password = "correct horse battery staple"

// agentMux wires the pieces the way the agent does, around one auth service.
func agentMux(token string, service *authService) (*http.ServeMux, *Guard) {
	guard := &Guard{Token: token, Client: service.Client}
	mux := http.NewServeMux()
	RegisterPublic(mux, service.Client.PublicProxy())
	(&PeopleAPI{Client: service.Client, Guard: guard.Middleware}).Register(mux)
	mux.Handle("/api/v1/thing", guard.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		who, _ := FromContext(r.Context())
		_ = json.NewEncoder(w).Encode(who)
	})))
	return mux, guard
}

// setUp makes the first owner through the real flow: the code shown on the machine.
func setUp(t *testing.T, service *authService, b *browser, email string) {
	t.Helper()
	response := b.do(http.MethodPost, "/api/setup", map[string]string{"code": service.setupCode(t), "name": "Owner", "email": email, "password": password})
	if response.Code != http.StatusCreated {
		t.Fatalf("first-owner setup: %d %s", response.Code, response.Body.String())
	}
}
