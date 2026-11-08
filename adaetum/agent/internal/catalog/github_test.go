package catalog

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// githubHub stands in for GitHub's search and its avatar host.
func githubHub(t *testing.T, items string) *API {
	t.Helper()
	var hub *httptest.Server
	hub = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/search/repositories":
			fmt.Fprintf(w, `{"items":%s}`, strings.ReplaceAll(items, "%[1]s", hub.URL))
		case "/avatar/org":
			if r.URL.Query().Get("s") != "128" {
				http.Error(w, "size", 400)
				return
			}
			w.Header().Set("Content-Type", "image/png")
			w.Write([]byte("org-avatar"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(hub.Close)
	host, _ := url.Parse(hub.URL)
	return &API{Client: &Client{DockerHubURL: emptyHub(t),
		ArtifactHubURL: emptyHub(t), IconsURL: noIcons(t),
		GitHubURL: hub.URL, AvatarHost: host.Host,
	}}
}

func TestGitHubSuppliesAnOrganisationsAvatarForAWellKnownExactName(t *testing.T) {
	api := githubHub(t, `[
	 {"name":"widget-tools","stargazers_count":99999,"owner":{"type":"Organization","avatar_url":"%[1]s/avatar/org"}},
	 {"name":"widget","stargazers_count":5000,"owner":{"type":"Organization","avatar_url":"%[1]s/avatar/org"}}]`)
	got := getLogo(api, "name=widget")
	if got.Code != 200 || got.Body.String() != "org-avatar" {
		t.Fatalf("%d %q", got.Code, got.Body)
	}
}

func TestGitHubNeverShowsAPersonsFaceOrABarelyKnownRepository(t *testing.T) {
	for name, items := range map[string]string{
		"a person":       `[{"name":"widget","stargazers_count":9000,"owner":{"type":"User","avatar_url":"%[1]s/avatar/org"}}]`,
		"few stars":      `[{"name":"widget","stargazers_count":30,"owner":{"type":"Organization","avatar_url":"%[1]s/avatar/org"}}]`,
		"archived":       `[{"name":"widget","stargazers_count":9000,"archived":true,"owner":{"type":"Organization","avatar_url":"%[1]s/avatar/org"}}]`,
		"another name":   `[{"name":"widgets","stargazers_count":9000,"owner":{"type":"Organization","avatar_url":"%[1]s/avatar/org"}}]`,
		"somewhere else": `[{"name":"widget","stargazers_count":9000,"owner":{"type":"Organization","avatar_url":"http://evil.example/avatar/org"}}]`,
	} {
		if got := getLogo(githubHub(t, items), "name=widget"); got.Code != http.StatusNotFound {
			t.Errorf("%s: should have no logo, got %d", name, got.Code)
		}
	}
}

func TestAnOverloadedGitHubIsNotRememberedAsNoLogo(t *testing.T) {
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) }))
	t.Cleanup(hub.Close)
	api := &API{Client: &Client{DockerHubURL: emptyHub(t), ArtifactHubURL: emptyHub(t), IconsURL: noIcons(t), GitHubURL: hub.URL}}
	if got := getLogo(api, "name=widget"); got.Code != http.StatusServiceUnavailable {
		t.Fatalf("a refusing source is \"not now\": %d", got.Code)
	}
}
