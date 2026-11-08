package catalog

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const digest = "sha256:5ab5b0b01097420873a3f3330f44248e34a3c2a3e8d5ee30658aeae8cb7435da"

// hubs stands in for Artifact Hub and Docker Hub.
func hubs(t *testing.T, chartRepoURL, chartVersion string) *Client {
	t.Helper()
	artifact := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/packages/search":
			fmt.Fprint(w, `{"packages":[
			 {"name":"uptime-kuma","description":"Monitor\nthings\u0007 `+strings.Repeat("x", 400)+`","version":"1.2.3","stars":4,"signed":true,"repository":{"name":"kuma-repo","verified_publisher":true,"organization_display_name":"Kuma Co"}},
			 {"name":"../escape","repository":{"name":"kuma-repo"}}]}`)
		case r.URL.Path == "/api/v1/packages/helm/kuma-repo/uptime-kuma":
			fmt.Fprintf(w, `{"name":"uptime-kuma","version":%q,"license":"MIT","signed":false,
			 "security_report_summary":{"critical":1,"high":2},
			 "containers_images":[{"image":"ghcr.io/kuma/app@sha256:abc"}],
			 "repository":{"url":%q,"name":"kuma-repo","verified_publisher":false,"user_alias":"someone"}}`, chartVersion, chartRepoURL)
		default:
			http.NotFound(w, r)
		}
	}))
	docker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/search/repositories/":
			fmt.Fprint(w, `{"results":[{"repo_name":"nginx","short_description":"web server","star_count":9,"pull_count":99,"is_official":true},{"repo_name":"bob/kuma","short_description":"hi","star_count":1}]}`)
		case "/v2/repositories/bob/kuma/":
			fmt.Fprint(w, `{"name":"kuma","namespace":"bob","description":"hi","star_count":1,"pull_count":10}`)
		case "/v2/repositories/bob/kuma/tags/latest":
			fmt.Fprintf(w, `{"name":"latest","digest":%q}`, digest)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(artifact.Close)
	t.Cleanup(docker.Close)
	return &Client{ArtifactHubURL: artifact.URL, DockerHubURL: docker.URL}
}

func TestSearchMergesBothSourcesAndCleansThirdPartyText(t *testing.T) {
	results, err := hubs(t, "https://charts.example.com", "1.2.3").Search(context.Background(), "uptime kuma")
	if err != nil {
		t.Fatal(err)
	}
	if len(results.Charts) != 1 || len(results.Images) != 2 {
		t.Fatalf("an unsafe chart name must be dropped, the rest kept: %+v", results)
	}
	chart := results.Charts[0]
	if chart.ID != "kuma-repo/uptime-kuma" || !chart.Verified || chart.Publisher != "Kuma Co" {
		t.Fatalf("%+v", chart)
	}
	if strings.ContainsAny(chart.Description, "\n\a") || len([]rune(chart.Description)) > maxText+1 {
		t.Fatalf("descriptions are shortened and made plain: %q", chart.Description)
	}
	official := results.Images[0]
	if official.ID != "library/nginx" || !official.Official || official.Publisher != "Docker Official Image" {
		t.Fatalf("an Official Image comes back without a namespace: %+v", official)
	}
}

func TestOneSourceBeingDownDoesNotLoseTheOther(t *testing.T) {
	client := hubs(t, "https://charts.example.com", "1.2.3")
	client.DockerHubURL = "http://127.0.0.1:1" // nothing listens here
	results, err := client.Search(context.Background(), "kuma")
	if err != nil || len(results.Charts) != 1 {
		t.Fatalf("charts must still come back: %v %+v", err, results)
	}
	if len(results.Notes) != 1 || !strings.Contains(results.Notes[0], "Docker Hub") {
		t.Fatalf("the missing source must be named: %v", results.Notes)
	}
}

func TestSearchRefusesUselessQueries(t *testing.T) {
	client := hubs(t, "https://x", "1")
	for _, bad := range []string{"", "a", strings.Repeat("a", 81), "kuma\x00"} {
		if _, err := client.Search(context.Background(), bad); err == nil {
			t.Fatalf("%q must be refused", bad)
		}
	}
}

func TestAChartPreviewWritesTheAppFilesTheApplicationSetExpects(t *testing.T) {
	p, err := hubs(t, "https://charts.example.com/", "1.2.3").Preview(context.Background(), PreviewRequest{Kind: KindChart, ID: "kuma-repo/uptime-kuma"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Applied || p.Name != "uptime-kuma" || p.Namespace != "uptime-kuma" || len(p.Files) != 2 {
		t.Fatalf("%+v", p)
	}
	app := p.Files[0]
	if app.Path != "pods/uptime-kuma/uptime-kuma.app.yaml" {
		t.Fatalf("the ApplicationSet only finds pods/*/*.app.yaml: %s", app.Path)
	}
	for _, want := range []string{`source_type: helm`, `source_repo_url: "https://charts.example.com/"`, `source_chart: "uptime-kuma"`, `source_target_revision: "1.2.3"`, `$values/pods/uptime-kuma/uptime-kuma-values.yaml`, `destination_namespace: "uptime-kuma"`} {
		if !strings.Contains(app.Content, want) {
			t.Fatalf("missing %q in:\n%s", want, app.Content)
		}
	}
	text := ""
	for _, f := range p.Flags {
		text += f.Level + ":" + f.Text + "\n"
	}
	for _, want := range []string{"bad:Artifact Hub's scan found 1 critical and 2 high", "warn:The chart isn't signed", "warn:Published by someone, who isn't verified", "ghcr.io/kuma/app"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
}

func TestAnOCIChartBecomesRepositoryPlusChartName(t *testing.T) {
	p, err := hubs(t, "oci://ghcr.io/quenchworks/charts/uptime-kuma", "0.0.11").Preview(context.Background(), PreviewRequest{Kind: KindChart, ID: "kuma-repo/uptime-kuma"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Files[0].Content, `source_repo_url: "ghcr.io/quenchworks/charts"`) || !strings.Contains(p.Files[0].Content, `source_chart: "uptime-kuma"`) {
		t.Fatalf("Argo CD wants the registry path and the chart separately:\n%s", p.Files[0].Content)
	}
}

func TestUnsafeChartFactsAreRefused(t *testing.T) {
	for name, tc := range map[string]struct{ repo, version string }{
		"plain http":        {"http://charts.example.com", "1.0.0"},
		"credentials":       {"https://user:pw@charts.example.com", "1.0.0"},
		"query":             {"https://charts.example.com?x=1", "1.0.0"},
		"yaml in a version": {"https://charts.example.com", "1.0.0\nsource_chart: evil"},
	} {
		if _, err := hubs(t, tc.repo, tc.version).Preview(context.Background(), PreviewRequest{Kind: KindChart, ID: "kuma-repo/uptime-kuma"}); err == nil {
			t.Fatalf("%s must be refused", name)
		}
	}
}

func TestAnImagePreviewPinsTheDigestAndSaysWhatItCannotKnow(t *testing.T) {
	p, err := hubs(t, "https://x", "1").Preview(context.Background(), PreviewRequest{Kind: KindImage, ID: "bob/kuma", Name: "my-kuma", Namespace: "tools"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "my-kuma" || p.Namespace != "tools" || len(p.Files) != 4 {
		t.Fatalf("%+v", p)
	}
	deployment := ""
	for _, f := range p.Files {
		if strings.HasSuffix(f.Path, "deployment.yaml") {
			deployment = f.Content
		}
	}
	if !strings.Contains(deployment, `"docker.io/bob/kuma@`+digest+`"`) {
		t.Fatalf("the image must be pinned by digest:\n%s", deployment)
	}
	if len(p.Needs) < 3 || !strings.Contains(strings.Join(p.Needs, " "), "port") {
		t.Fatalf("the guesses must be listed: %v", p.Needs)
	}
}

func TestPreviewRefusesAwkwardInput(t *testing.T) {
	client := hubs(t, "https://charts.example.com", "1.2.3")
	for name, request := range map[string]PreviewRequest{
		"no id":            {Kind: KindChart},
		"path in id":       {Kind: KindChart, ID: "a/../b"},
		"bad app name":     {Kind: KindChart, ID: "kuma-repo/uptime-kuma", Name: "Bad Name"},
		"reserved":         {Kind: KindChart, ID: "kuma-repo/uptime-kuma", Namespace: "kube-system"},
		"argocd namespace": {Kind: KindChart, ID: "kuma-repo/uptime-kuma", Namespace: "argocd"},
		"unknown kind":     {Kind: "script", ID: "a/b"},
		"bad tag":          {Kind: KindImage, ID: "bob/kuma", Tag: "x y"},
		"missing tag":      {Kind: KindImage, ID: "bob/kuma", Tag: "nope"},
		"missing chart":    {Kind: KindChart, ID: "kuma-repo/other"},
	} {
		if _, err := client.Preview(context.Background(), request); err == nil {
			t.Fatalf("%s must be refused", name)
		}
	}
}

func TestProductionTalksOnlyToTheTwoPublicHubs(t *testing.T) {
	c := &Client{}
	if c.artifactHub() != "https://artifacthub.io" || c.dockerHub() != "https://hub.docker.com" || c.icons() != "https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons/png" ||
		c.github() != "https://api.github.com" || c.dockerLogoHost() != "djeqr6to3dedg.cloudfront.net" || c.avatarHost() != "avatars.githubusercontent.com" {
		t.Fatalf("%s %s", c.artifactHub(), c.dockerHub())
	}
}

func TestTheRoutesNeedALoginAndAnswerPlainly(t *testing.T) {
	mux := http.NewServeMux()
	guard := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") == "" {
				http.Error(w, "no", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	(&API{Client: hubs(t, "https://charts.example.com", "1.2.3"), RequireUser: guard}).Register(mux)

	for _, path := range []string{"/api/v1/catalog/search?q=kuma", "/api/v1/catalog/preview?kind=chart&id=kuma-repo/uptime-kuma"} {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, httptest.NewRequest("GET", path, nil))
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("%s must need a login: %d", path, recorder.Code)
		}
		request := httptest.NewRequest("GET", path, nil)
		request.Header.Set("Authorization", "Bearer x")
		recorder = httptest.NewRecorder()
		mux.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", path, recorder.Code, recorder.Body)
		}
	}
	bad := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/api/v1/catalog/search?q=a", nil)
	request.Header.Set("Authorization", "Bearer x")
	mux.ServeHTTP(bad, request)
	if bad.Code != http.StatusBadRequest || !strings.Contains(bad.Body.String(), "2 to 80") {
		t.Fatalf("a useless query must be explained: %d %s", bad.Code, bad.Body)
	}
}
