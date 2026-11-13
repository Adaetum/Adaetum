package main

import "net/http"

// hstsValue tells a browser to use HTTPS for this exact host for a year after it has
// loaded the agent once. It deliberately omits includeSubDomains and preload: the agent
// is served under shared names such as a tailnet's ts.net suffix, and a rule that
// reaches sibling hosts would break services this agent does not own.
const hstsValue = "max-age=31536000"

// withHSTS adds Strict-Transport-Security to responses sent over TLS. The agent cannot
// redirect a plain-HTTP request itself, because the Tailscale proxy never forwards one
// (it serves HTTPS only), so this is what upgrades every visit after the first. A browser
// ignores the header on plain HTTP, so it is only set when the request arrived over TLS.
func withHSTS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil {
			w.Header().Set("Strict-Transport-Security", hstsValue)
		}
		next.ServeHTTP(w, r)
	})
}
