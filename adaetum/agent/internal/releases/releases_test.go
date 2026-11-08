package releases

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fixture(version string) Manifest {
	m := Manifest{Version: version, SourceCommit: strings.Repeat("a", 40), Images: map[string]string{}, Binaries: map[string]Binary{}}
	for _, c := range []string{"agent", "auth", "embed"} {
		m.Images[c] = "ghcr.io/adaetum/adaetum-" + c + "@sha256:" + strings.Repeat("b", 64)
	}
	for _, c := range []string{"agent", "auth"} {
		for _, arch := range []string{"amd64", "arm64"} {
			n := "adaetum-" + c + "-linux-" + arch
			m.Binaries[n] = Binary{URL: "https://github.com/Adaetum/Adaetum/releases/download/adaetum-v" + version + "/" + n, SHA256: strings.Repeat("c", 64)}
		}
	}
	return m
}
func TestLatestChoosesGreatestStableAndValidatesArtifacts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/api/") {
			fmt.Fprint(w, `[{"tag_name":"v9.0.0","assets":[{"name":"release.json"}]},{"tag_name":"adaetum-v1.2.3","assets":[{"name":"release.json"}]},{"tag_name":"adaetum-v2.0.0","prerelease":true,"assets":[{"name":"release.json"}]},{"tag_name":"adaetum-v1.10.0","assets":[{"name":"release.json"}]}]`)
			return
		}
		if !strings.Contains(r.URL.Path, "adaetum-v1.10.0") {
			t.Errorf("wrong asset path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(fixture("1.10.0"))
	}))
	defer server.Close()
	client := Client{HTTP: server.Client(), APIBase: server.URL + "/api", DownloadBase: server.URL}
	result, err := client.Latest(context.Background(), "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if !result.UpdateAvailable || result.Release.Version != "1.10.0" {
		t.Fatalf("result=%+v", result)
	}
}
func TestManifestRejectsWrongOriginOrChecksum(t *testing.T) {
	valid := fixture("1.2.3")
	if err := validate(valid, "1.2.3", "adaetum-v1.2.3"); err != nil {
		t.Fatal(err)
	}
	bad := fixture("1.2.3")
	bad.Images["agent"] = "ghcr.io/other/adaetum-agent@sha256:" + strings.Repeat("b", 64)
	if err := validate(bad, "1.2.3", "adaetum-v1.2.3"); err == nil {
		t.Fatal("accepted an unrelated image")
	}
	bad = fixture("1.2.3")
	b := bad.Binaries["adaetum-agent-linux-amd64"]
	b.URL = "https://example.com/agent"
	bad.Binaries["adaetum-agent-linux-amd64"] = b
	if err := validate(bad, "1.2.3", "adaetum-v1.2.3"); err == nil {
		t.Fatal("accepted an unrelated binary URL")
	}
	bad = fixture("1.2.3")
	b = bad.Binaries["adaetum-auth-linux-arm64"]
	b.SHA256 = "bad"
	bad.Binaries["adaetum-auth-linux-arm64"] = b
	if err := validate(bad, "1.2.3", "adaetum-v1.2.3"); err == nil {
		t.Fatal("accepted an invalid checksum")
	}
}

func TestReleaseAPIRequiresUserAndRemainsReadOnly(t *testing.T) {
	calls := 0
	api := API{Current: "1.0.0", Latest: func(_ context.Context, current string) (Check, error) {
		calls++
		return Check{Current: current, Release: fixture("1.2.3")}, nil
	}, RequireUser: func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer valid" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}}
	mux := http.NewServeMux()
	api.Register(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/releases/latest", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized || calls != 0 {
		t.Fatalf("unauthorized request: %d calls=%d", w.Code, calls)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/releases/latest", nil)
	req.Header.Set("Authorization", "Bearer valid")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK || calls != 1 {
		t.Fatalf("authorized request: %d calls=%d", w.Code, calls)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/v1/releases/latest", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed || calls != 1 {
		t.Fatalf("write request: %d calls=%d", w.Code, calls)
	}
}

func TestProposalRequiresMatchingPreviewAndAudit(t *testing.T) {
	calls, audits := 0, 0
	preview := Check{ManifestHash: strings.Repeat("d", 64), Release: fixture("1.2.3")}
	api := API{Latest: func(context.Context, string) (Check, error) { return preview, nil }, Propose: func(context.Context, Manifest) (Proposal, error) {
		calls++
		return Proposal{URL: "http://gitea/pulls/1"}, nil
	}, Audit: func(any) error { audits++; return nil }}
	mux := http.NewServeMux()
	api.Register(mux)
	attempt := func(hash string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/releases/proposals", strings.NewReader(`{"version":"1.2.3","manifestHash":"`+hash+`"}`))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	if w := attempt(strings.Repeat("e", 64)); w.Code != http.StatusConflict || calls != 0 || audits != 0 {
		t.Fatalf("changed preview: %d calls=%d audits=%d", w.Code, calls, audits)
	}
	if w := attempt(preview.ManifestHash); w.Code != http.StatusCreated || calls != 1 || audits != 2 {
		t.Fatalf("approved proposal: %d calls=%d audits=%d %s", w.Code, calls, audits, w.Body.String())
	}
}
