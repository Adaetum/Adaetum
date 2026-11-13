package main

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHSTSIsSetOnlyOverTLSAndNeverReachesSubdomains(t *testing.T) {
	h := withHSTS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))

	secure := httptest.NewRequest(http.MethodGet, "https://agent.example.ts.net/", nil)
	secure.TLS = &tls.ConnectionState{}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, secure)
	got := rec.Header().Get("Strict-Transport-Security")
	if got != "max-age=31536000" {
		t.Fatalf("HSTS over TLS = %q", got)
	}
	if strings.Contains(got, "includeSubDomains") || strings.Contains(got, "preload") {
		t.Fatalf("HSTS must not reach sibling hosts: %q", got)
	}

	plain := httptest.NewRecorder()
	h.ServeHTTP(plain, httptest.NewRequest(http.MethodGet, "http://agent.example.ts.net/", nil))
	if v := plain.Header().Get("Strict-Transport-Security"); v != "" {
		t.Fatalf("HSTS was set on a plain-HTTP response: %q", v)
	}
}
