// Package webui serves the web interface from inside the agent binary.
//
// INTENT: so an operator can open https://<host>:8443 on any host, with the cluster
// down and nothing else installed, and get the full interface. That is what makes
// the host agent a real out-of-band troubleshooting tool rather than only an API.
//
// How it fits together:
//
//   - `npm run build` in agent/web writes the compiled interface to ui/dist, and
//     go:embed puts it in the binary, so the agent stays one file. ui/dist is not
//     committed; the tracked ui/placeholder.txt lets the package compile before a
//     build exists, and Handler then says plainly that the interface is not built.
//   - The pages are PUBLIC static files and hold no host data. All data comes from
//     /api, which needs the access token when the agent is reachable from the
//     network (cmd/adaetum-agent/exposure.go). The login screen is part of the
//     interface (web/src/pages/Login.tsx); the token stays in the browser tab.
//   - Nothing here calls out to the internet: fonts and scripts are bundled, so the
//     pages work on an isolated network.
//   - Unknown paths get index.html so deep links work; hashed asset files are
//     cached for a year and index.html never is, so an upgrade shows at once.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:ui
var embedded embed.FS

// Handler serves the interface built into this binary.
func Handler() http.Handler {
	built, err := fs.Sub(embedded, "ui/dist")
	if err != nil {
		return notBuilt()
	}
	return FromFS(built)
}

// FromFS serves an interface from any file system (tests use an in-memory one). A
// file system without index.html is reported as not built.
func FromFS(files fs.FS) http.Handler {
	if _, err := fs.Stat(files, "index.html"); err != nil {
		return notBuilt()
	}
	server := http.FileServer(http.FS(files))

	return secureHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The interface is read-only static content.
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// An /api path that no API route claimed is a missing endpoint, not a deep
		// link. Answering it with the app would make a client that asked for JSON
		// receive HTML with a 200, which is how "this agent has no cluster view" would
		// be mistaken for a working one.
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if info, err := fs.Stat(files, name); err != nil || info.IsDir() {
			// Not a file: a deep link into the app, so hand it the app. A missing
			// asset (a path with an extension) is a real 404, not the app.
			if path.Ext(name) != "" {
				http.NotFound(w, r)
				return
			}
			name = "index.html"
		}
		if name == "index.html" {
			// FileServer redirects explicit /index.html requests to /; ask for the
			// directory instead so every route to the app answers directly.
			r = withPath(r, "/")
		}

		if strings.HasPrefix(name, "assets/") {
			// Vite names these by content hash, so they never change.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		server.ServeHTTP(w, r)
	}))
}

func withPath(r *http.Request, p string) *http.Request {
	clone := r.Clone(r.Context())
	urlCopy := *r.URL
	urlCopy.Path = p
	clone.URL = &urlCopy
	return clone
}

func notBuilt() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "the web interface is not built into this agent. Run `npm run build` in agent/web, then rebuild the agent. The API still works.", http.StatusServiceUnavailable)
	})
}

// secureHeaders limits what the pages can do. The interface needs only its own
// files and the agent's API; inline styles are allowed because the design system
// sets some at run time.
func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}
