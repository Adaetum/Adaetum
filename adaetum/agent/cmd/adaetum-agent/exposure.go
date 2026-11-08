package main

import (
	"crypto/subtle"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"strings"
)

// A host agent must be reachable directly over the local network when everything
// else is down, so it can listen on every interface. That is only safe with a login
// and encryption, so listening beyond loopback is refused unless both are in place.

// listensBeyondLoopback reports whether an address would accept connections from
// other machines. An empty host, 0.0.0.0, and :: mean every interface.
func listensBeyondLoopback(address string) (bool, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false, fmt.Errorf("%q is not a host:port address: %w", address, err)
	}
	if host == "localhost" {
		return false, nil
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// A hostname could resolve to a LAN address; treat anything but localhost as exposed.
		return true, nil
	}
	return !ip.IsLoopback(), nil
}

// checkToken validates an access token file and returns its value. A non-empty
// reason says why the token cannot be used, in words an operator can act on.
func checkToken(path string, sys system) (token, reason string) {
	if path == "" {
		return "", "no access token is configured. Create one with `adaetum-agent token create`, then start the agent with -token-file."
	}
	value, mode, err := sys.readToken(path)
	switch {
	case err != nil:
		return "", "the access token file could not be read: " + err.Error()
	case mode.Perm()&fs.FileMode(0o077) != 0:
		return "", "the access token file must be readable only by root (mode 600)."
	case len(value) < minTokenBytes:
		return "", fmt.Sprintf("the access token must be at least %d characters. Create one with `adaetum-agent token create`.", minTokenBytes)
	}
	return value, ""
}

// bearerGuard returns middleware that lets a request through only if it carries the
// access token. It is the "user" boundary: a person with the token.
func bearerGuard(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			presented, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
			if !found || subtle.ConstantTimeCompare([]byte(presented), []byte(token)) != 1 {
				w.Header().Set("WWW-Authenticate", `Bearer realm="adaetum-agent"`)
				http.Error(w, "an access token is required", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// requireToken makes every API request present the token. The web interface's own
// files and the liveness check stay open: the pages hold no host data (they are
// the same static files for every host), and they must load before a person can
// sign in. /healthz reveals nothing but that the process is up.
func requireToken(next http.Handler, token string) http.Handler {
	guarded := bearerGuard(token)(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			guarded.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireSafeExposure stops the agent from listening beyond this machine unless
// it can protect what it shows, and returns the access token to enforce. It exits
// with an explanation rather than starting in a weaker mode.
func requireSafeExposure(address, sshTarget, tokenFile string) string {
	if sshTarget != "" {
		fmt.Fprintf(os.Stderr, "-listen %s would serve another machine's data (-ssh %s) to the network. Use a loopback address in SSH mode.\n", address, sshTarget)
		os.Exit(2)
	}
	token, reason := checkToken(tokenFile, realSystem())
	if reason != "" {
		fmt.Fprintf(os.Stderr, "-listen %s is reachable from the network, so it needs a login, but %s\n", address, reason)
		os.Exit(2)
	}
	return token
}
