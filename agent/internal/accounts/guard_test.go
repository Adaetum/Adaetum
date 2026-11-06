package accounts

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// A fake auth service that only answers get-session, to test the guard on its own.
type fakeSessions struct {
	calls atomic.Int32
	user  string // JSON for "user", or "" for signed out
	fail  atomic.Bool
}

func (f *fakeSessions) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.calls.Add(1)
	if f.fail.Load() {
		http.Error(w, "boom", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if f.user == "" {
		_, _ = w.Write([]byte("null"))
		return
	}
	_, _ = w.Write([]byte(`{"user":` + f.user + `}`))
}

func status(g *Guard, r *http.Request) int {
	recorder := httptest.NewRecorder()
	g.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })).ServeHTTP(recorder, r)
	return recorder.Code
}

func request(method string, headers map[string]string) *http.Request {
	r := httptest.NewRequest(method, "/api/v1/thing", nil)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

const cookie = "better-auth.session_token=abc.def"

func TestTokenWorksWithNoAuthService(t *testing.T) {
	g := &Guard{Token: "a-long-enough-access-token-value"}
	if got := status(g, request("POST", map[string]string{"Authorization": "Bearer a-long-enough-access-token-value"})); got != 200 {
		t.Fatalf("token with no auth service: %d", got)
	}
	if got := status(g, request("GET", map[string]string{"Authorization": "Bearer wrong"})); got != 401 {
		t.Fatalf("wrong token: %d", got)
	}
}

func TestEmptyTokenIsNeverAccepted(t *testing.T) {
	// "No token configured" must not turn into "an empty bearer is the token".
	g := &Guard{}
	for _, header := range []string{"Bearer ", "Bearer", ""} {
		if got := status(g, request("GET", map[string]string{"Authorization": header})); got != 401 {
			t.Errorf("Authorization %q: %d", header, got)
		}
	}
}

func TestSessionsAreResolvedByTheAuthServiceAndRemembered(t *testing.T) {
	fake := &fakeSessions{user: `{"id":"u1","email":"a@x.test","name":"A","role":"owner"}`}
	server := httptest.NewServer(fake)
	defer server.Close()
	now := time.Unix(1_700_000_000, 0)
	g := &Guard{Client: NewClient(server.URL, "s", nil), Now: func() time.Time { return now }}

	r := request("GET", map[string]string{"Cookie": cookie})
	if got := status(g, r); got != 200 {
		t.Fatalf("session: %d", got)
	}
	status(g, request("GET", map[string]string{"Cookie": cookie}))
	if fake.calls.Load() != 1 {
		t.Errorf("a second request within the window asked the service again (%d calls)", fake.calls.Load())
	}
	now = now.Add(positiveTTL + time.Second)
	status(g, request("GET", map[string]string{"Cookie": cookie}))
	if fake.calls.Load() != 2 {
		t.Errorf("after the window it should ask again (%d calls)", fake.calls.Load())
	}
}

func TestNoCookieNeverReachesTheAuthService(t *testing.T) {
	fake := &fakeSessions{user: `{"id":"u1","email":"a@x.test","name":"A","role":"owner"}`}
	server := httptest.NewServer(fake)
	defer server.Close()
	g := &Guard{Client: NewClient(server.URL, "s", nil)}
	if got := status(g, request("GET", nil)); got != 401 {
		t.Fatalf("no credentials: %d", got)
	}
	if got := status(g, request("GET", map[string]string{"Cookie": "theme=dark"})); got != 401 {
		t.Fatalf("an unrelated cookie: %d", got)
	}
	if fake.calls.Load() != 0 {
		t.Errorf("the auth service was asked %d times for requests that carried no sign-in cookie", fake.calls.Load())
	}
}

func TestSignedOutSessionIsRefusedAndAnOutageIsNotRemembered(t *testing.T) {
	fake := &fakeSessions{}
	server := httptest.NewServer(fake)
	defer server.Close()
	now := time.Unix(1_700_000_000, 0)
	g := &Guard{Token: "a-long-enough-access-token-value", Client: NewClient(server.URL, "s", nil), Now: func() time.Time { return now }}

	if got := status(g, request("GET", map[string]string{"Cookie": cookie})); got != 401 {
		t.Fatalf("a cookie for nobody: %d", got)
	}

	// The service breaks. The same cookie is refused, but the answer is NOT cached, so the
	// moment the service is back the person is back in, without waiting out a timer.
	fake.user = `{"id":"u1","email":"a@x.test","name":"A","role":"admin"}`
	fake.fail.Store(true)
	now = now.Add(negativeTTL + time.Second)
	if got := status(g, request("GET", map[string]string{"Cookie": cookie})); got != 401 {
		t.Fatalf("during an outage: %d", got)
	}
	fake.fail.Store(false)
	if got := status(g, request("GET", map[string]string{"Cookie": cookie})); got != 200 {
		t.Fatalf("right after the outage: %d", got)
	}
	// And throughout, the token still works.
	fake.fail.Store(true)
	if got := status(g, request("POST", map[string]string{"Authorization": "Bearer a-long-enough-access-token-value"})); got != 200 {
		t.Fatalf("token during an outage: %d", got)
	}
}

func TestRolesDecideWhoCanChange(t *testing.T) {
	cases := []struct {
		role   string
		method string
		want   int
	}{
		{"owner", "POST", 200}, {"admin", "POST", 200}, {"admin", "DELETE", 200},
		{"member", "GET", 200}, {"member", "POST", 403}, {"member", "PUT", 403}, {"member", "DELETE", 403},
		{"", "POST", 403}, {"stranger", "POST", 403},
	}
	for _, c := range cases {
		fake := &fakeSessions{user: `{"id":"u1","email":"a@x.test","name":"A","role":"` + c.role + `"}`}
		server := httptest.NewServer(fake)
		g := &Guard{Client: NewClient(server.URL, "s", nil)}
		if got := status(g, request(c.method, map[string]string{"Cookie": cookie})); got != c.want {
			t.Errorf("%q %s: got %d, want %d", c.role, c.method, got, c.want)
		}
		server.Close()
	}
}

func TestOwnerOnly(t *testing.T) {
	for role, want := range map[string]int{"owner": 200, "admin": 403, "member": 403} {
		fake := &fakeSessions{user: `{"id":"u1","email":"a@x.test","name":"A","role":"` + role + `"}`}
		server := httptest.NewServer(fake)
		g := &Guard{Token: "a-long-enough-access-token-value", Client: NewClient(server.URL, "s", nil)}
		handler := g.Middleware(OwnerOnly(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {})))
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request("GET", map[string]string{"Cookie": cookie}))
		if recorder.Code != want {
			t.Errorf("%s: got %d, want %d", role, recorder.Code, want)
		}
		recorder = httptest.NewRecorder()
		handler.ServeHTTP(recorder, request("POST", map[string]string{"Authorization": "Bearer a-long-enough-access-token-value"}))
		if recorder.Code != 200 {
			t.Errorf("the token should always count as an owner, got %d", recorder.Code)
		}
		server.Close()
	}
}
