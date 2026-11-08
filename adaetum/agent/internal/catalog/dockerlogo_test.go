package catalog

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// dockerLogoHub stands in for Docker Hub's catalog search and its logo store.
func dockerLogoHub(t *testing.T, results string) *API {
	t.Helper()
	var hub *httptest.Server
	hub = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/not-used":
		case "/api/search/v3/catalog/search":
			if r.Header.Get("Search-Version") != "v3" {
				http.Error(w, "version", 400)
				return
			}
			fmt.Fprintf(w, `{"results":%s}`, strings.ReplaceAll(results, "%[1]s", hub.URL))
		case "/repo-logos/dhi/widget/logo":
			w.Header().Set("Content-Type", "image/png")
			w.Write([]byte("docker-logo"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(hub.Close)
	host, _ := url.Parse(hub.URL)
	return &API{Client: &Client{
		ArtifactHubURL: emptyHub(t), IconsURL: noIcons(t), GitHubURL: emptyHub(t),
		DockerHubURL: hub.URL, DockerLogoHost: host.Host,
	}}
}

func TestDockerHubSuppliesAVerifiedExactNameLogo(t *testing.T) {
	api := dockerLogoHub(t, `[
	 {"name":"someone/widget","source":"community","star_count":2,"logo_url":{"large":"%[1]s/repo-logos/dhi/widget/logo"}},
	 {"name":"Widget","source":"verified_publisher","star_count":0,"logo_url":{"large":"%[1]s/repo-logos/dhi/widget/logo"}}]`)
	if got := getLogo(api, "name=widget"); got.Code != 200 || got.Body.String() != "docker-logo" {
		t.Fatalf("%d %q", got.Code, got.Body)
	}
}

func TestDockerHubLogosFromElsewhereOrLookAlikesAreIgnored(t *testing.T) {
	for name, results := range map[string]string{
		"an unknown publisher": `[{"name":"someone/widget","source":"community","star_count":3,"logo_url":{"large":"%[1]s/repo-logos/dhi/widget/logo"}}]`,
		"a gravatar":           `[{"name":"widget","source":"verified_publisher","logo_url":{"large":"http://gravatar.example/avatar/abc"}}]`,
		"outside the store":    `[{"name":"widget","source":"verified_publisher","logo_url":{"large":"%[1]s/uploads/face.png"}}]`,
		"another name":         `[{"name":"widget-pro","source":"verified_publisher","logo_url":{"large":"%[1]s/repo-logos/dhi/widget/logo"}}]`,
		"no logo":              `[{"name":"widget","source":"verified_publisher","logo_url":{}}]`,
	} {
		if got := getLogo(dockerLogoHub(t, results), "name=widget"); got.Code != http.StatusNotFound {
			t.Errorf("%s: should have no logo, got %d", name, got.Code)
		}
	}
}
