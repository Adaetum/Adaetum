package accounts

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Guard decides who a request comes from. Two ways in:
//
//  1. THE ACCESS TOKEN (Authorization: Bearer). The emergency login for administrators
//     and the command line. It is checked here, in this process, and never asks the auth
//     service: if that service is down, crashed or not installed, the token still works.
//     An empty token means "none configured" and is never accepted.
//  2. A SESSION COOKIE from signing in with an account. Resolved by asking the auth
//     service, with the answer remembered for a few seconds so a page that makes thirty
//     calls does not make thirty lookups.
//
// People with the member role can read but not change anything; owners and admins can do
// both; the token can do everything. The rule is by HTTP method, so it covers every API
// that sits behind the guard without each one having to say so.
type Guard struct {
	Token  string
	Client *Client
	Now    func() time.Time

	mu    sync.Mutex
	cache map[[32]byte]cached
}

type cached struct {
	who   Identity
	ok    bool
	until time.Time
}

const (
	// A session that was just revoked can still work here for up to this long.
	positiveTTL = 5 * time.Second
	negativeTTL = 2 * time.Second
	maxCached   = 512
)

type contextKey struct{}

// FromContext returns who the request came from, for handlers behind the guard.
func FromContext(ctx context.Context) (Identity, bool) {
	who, ok := ctx.Value(contextKey{}).(Identity)
	return who, ok
}

func (g *Guard) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

// Authenticate returns who is asking, or false.
func (g *Guard) Authenticate(r *http.Request) (Identity, bool) {
	if g.Token != "" {
		if presented, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); found &&
			subtle.ConstantTimeCompare([]byte(presented), []byte(g.Token)) == 1 {
			return Identity{Role: "owner", Token: true}, true
		}
	}
	cookie := r.Header.Get("Cookie")
	// Cheap check first: a request with no sign-in cookie never reaches the auth service.
	if g.Client == nil || !strings.Contains(cookie, "better-auth") {
		return Identity{}, false
	}
	key := sha256.Sum256([]byte(cookie))
	now := g.now()
	g.mu.Lock()
	if hit, found := g.cache[key]; found && now.Before(hit.until) {
		g.mu.Unlock()
		return hit.who, hit.ok
	}
	g.mu.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	who, err := g.Client.Session(ctx, cookie)
	ok := err == nil
	// Only remember a definite answer. "The service did not answer" must not be cached as
	// "signed out", or a short outage would sign everyone out for longer than it lasts.
	if ok || err == ErrNoSession {
		ttl := negativeTTL
		if ok {
			ttl = positiveTTL
		}
		g.mu.Lock()
		if g.cache == nil || len(g.cache) >= maxCached {
			g.cache = make(map[[32]byte]cached)
		}
		g.cache[key] = cached{who: who, ok: ok, until: now.Add(ttl)}
		g.mu.Unlock()
	}
	return who, ok
}

func safe(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

// Middleware is the guard as handler middleware.
func (g *Guard) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		who, ok := g.Authenticate(r)
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="adaetum-agent"`)
			http.Error(w, "sign in, or present the access token", http.StatusUnauthorized)
			return
		}
		if !safe(r.Method) && !who.Token && who.Role != "owner" && who.Role != "admin" {
			http.Error(w, "your role can look but not change anything", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), contextKey{}, who)))
	})
}

// OwnerOnly additionally requires the owner role (or the token). Managing people is the
// one thing an admin cannot do.
func OwnerOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		who, ok := FromContext(r.Context())
		if !ok || (!who.Token && who.Role != "owner") {
			http.Error(w, "only an owner can manage people", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
