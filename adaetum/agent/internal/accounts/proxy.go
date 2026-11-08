package accounts

import (
	"encoding/json"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// maxPublicBody caps what a browser may send through the proxy: a sign-in form is tiny.
const maxPublicBody = 64 << 10

// PublicProxy passes the sign-in pages' calls to the auth service, and only those:
// /api/auth/*, /api/setup and /api/auth-state. Everything else, above all /internal/*,
// is not reachable through it. It runs BEFORE sign-in, so it cannot sit behind the guard.
//
// The browser's own Host header is kept, because the auth service accepts a form post only
// when its Origin matches the Host it was sent to; and any internal secret a client tries
// to send is removed.
func (c *Client) PublicProxy() http.Handler {
	target, _ := url.Parse(c.base)
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			r.Out.Host = r.In.Host
			r.Out.Header.Del(InternalHeader)
		},
		Transport: c.http.Transport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, _ error) {
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Path == "/api/auth-state" {
				// The page asks this to decide what to show. Say "accounts are down" instead
				// of failing, so it can offer the access token.
				_ = json.NewEncoder(w).Encode(AuthState{Enabled: true, Unavailable: true})
				return
			}
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":"sign-in is unavailable right now"}`))
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path != "/api/setup" && path != "/api/auth-state" && !strings.HasPrefix(path, "/api/auth/") {
			http.NotFound(w, r)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxPublicBody)
		proxy.ServeHTTP(w, r)
	})
}

// Disabled answers the public sign-in routes on an agent with no auth service, so the page
// knows to ask for the access token only.
func Disabled() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/auth-state" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(AuthState{Enabled: false})
			return
		}
		http.NotFound(w, r)
	})
}

// RegisterPublic mounts the sign-in routes on mux.
func RegisterPublic(mux *http.ServeMux, handler http.Handler) {
	mux.Handle("/api/auth-state", handler)
	mux.Handle("/api/setup", handler)
	mux.Handle("/api/auth/", handler)
}
