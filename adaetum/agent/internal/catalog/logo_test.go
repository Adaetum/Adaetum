package catalog

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Adaetum/Adaetum/agent/internal/store"
)

const (
	goodLogo  = "11111111-2222-3333-4444-555555555555"
	svgLogo   = "aaaaaaaa-2222-3333-4444-555555555555"
	otherLogo = "bbbbbbbb-2222-3333-4444-555555555555"
)

// noIcons stands in for an icon set that has nothing, so tests never reach the real one.
func noIcons(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(server.Close)
	return server.URL
}

// emptyHub answers every search with no results, like a source that has nothing.
func emptyHub(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"packages":[],"items":[],"results":[]}`)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// logoHub answers searches with one trusted, one untrusted and one look-alike chart.
func logoHub(t *testing.T) (*API, *int) {
	t.Helper()
	fetched := new(int)
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/packages/search":
			q := r.URL.Query().Get("ts_query_web")
			switch q {
			case "gitea":
				fmt.Fprintf(w, `{"packages":[
				 {"name":"gitea-fork","stars":900,"logo_image_id":%q,"repository":{"name":"r","verified_publisher":true}},
				 {"name":"gitea","stars":3,"logo_image_id":%q,"repository":{"name":"spam"}},
				 {"name":"gitea","stars":500,"logo_image_id":%q,"repository":{"name":"gitea-charts","verified_publisher":true}}]}`, otherLogo, otherLogo, goodLogo)
			case "monitoring":
				fmt.Fprintf(w, `{"packages":[{"name":"monitoring","stars":2,"logo_image_id":%q,"repository":{"name":"nobody","verified_publisher":true}}]}`, otherLogo)
			default:
				fmt.Fprint(w, `{"packages":[]}`)
			}
		case r.URL.Path == "/image/"+goodLogo:
			*fetched++
			w.Header().Set("Content-Type", "image/png")
			w.Write([]byte("png-bytes"))
		case r.URL.Path == "/image/"+svgLogo:
			w.Header().Set("Content-Type", "image/svg+xml")
			w.Write([]byte("<svg><script>alert(1)</script></svg>"))
		case r.URL.Path == "/image/"+otherLogo:
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<script>alert(1)</script>"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(hub.Close)
	return &API{Client: &Client{DockerHubURL: emptyHub(t), ArtifactHubURL: hub.URL, IconsURL: noIcons(t), GitHubURL: emptyHub(t)}}, fetched
}

func getLogo(api *API, query string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	api.Register(mux)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest("GET", "/api/v1/catalog/logo?"+query, nil))
	return recorder
}

func TestALogoComesFromTheTrustedExactNameMatchAndIsCached(t *testing.T) {
	api, fetched := logoHub(t)
	for i := 0; i < 2; i++ {
		got := getLogo(api, "name=gitea")
		if got.Code != 200 || got.Body.String() != "png-bytes" || got.Header().Get("Content-Type") != "image/png" {
			t.Fatalf("%d %q %v", got.Code, got.Body, got.Header())
		}
	}
	if *fetched != 1 {
		t.Fatalf("the picture should be fetched once and cached, fetched %d", *fetched)
	}
}

func TestAGenericNameDoesNotBorrowSomeoneElsesLogo(t *testing.T) {
	api, _ := logoHub(t)
	if got := getLogo(api, "name=monitoring"); got.Code != http.StatusNotFound {
		t.Fatalf("a barely used chart, even from a verified publisher, must not supply a logo: %d", got.Code)
	}
}

func TestOnlyImagesOfKnownTypesPassAndSvgIsSandboxed(t *testing.T) {
	api, _ := logoHub(t)
	if got := getLogo(api, "image="+otherLogo); got.Code != http.StatusNotFound {
		t.Fatalf("HTML must never be passed on as a logo: %d", got.Code)
	}
	got := getLogo(api, "image="+svgLogo)
	csp := got.Header().Get("Content-Security-Policy")
	if got.Code != 200 || !strings.Contains(csp, "sandbox") || got.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("an SVG must be sandboxed: %d %v", got.Code, got.Header())
	}
}

func TestLogoInputIsValidatedBeforeAnythingIsFetched(t *testing.T) {
	api, _ := logoHub(t)
	for _, query := range []string{"image=../../etc/passwd", "image=http://evil.example/x.png", "name=a/b", "name=" + strings.Repeat("a", 80), ""} {
		if got := getLogo(api, query); got.Code != http.StatusNotFound {
			t.Fatalf("%q: %d", query, got.Code)
		}
	}
}

func TestARefusingSourceIsLeftAloneForAWhile(t *testing.T) {
	calls := 0
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(hub.Close)
	api := &API{Client: &Client{DockerHubURL: emptyHub(t), ArtifactHubURL: hub.URL, IconsURL: noIcons(t), GitHubURL: emptyHub(t)}}
	for _, name := range []string{"gitea", "grafana", "loki"} {
		if got := getLogo(api, "name="+name); got.Code != http.StatusServiceUnavailable {
			t.Fatalf("a refusing source is \"not now\", never \"no logo\": %s: %d", name, got.Code)
		}
	}
	if calls != 1 {
		t.Fatalf("after one refusal the source should be left alone, but it was asked %d times", calls)
	}
}

func TestAnAppFoundInTheIconSetNeverAsksArtifactHub(t *testing.T) {
	hubCalls := 0
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hubCalls++; http.NotFound(w, r) }))
	icons := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/argo-cd.png", "/longhorn.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write([]byte("icon"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(hub.Close)
	t.Cleanup(icons.Close)
	api := &API{Client: &Client{DockerHubURL: emptyHub(t), ArtifactHubURL: hub.URL, IconsURL: icons.URL, GitHubURL: emptyHub(t)}}
	for _, name := range []string{"argocd", "longhorn-system"} {
		if got := getLogo(api, "name="+name); got.Code != 200 || got.Body.String() != "icon" {
			t.Fatalf("%s: %d", name, got.Code)
		}
	}
	if hubCalls != 0 {
		t.Fatalf("an exact icon should not need Artifact Hub, asked %d times", hubCalls)
	}
}

func TestFoundAndMissingLogosSurviveARestart(t *testing.T) {
	db, err := store.OpenDatabase(filepath.Join(t.TempDir(), "agent.db"), Migrations)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	first, fetched := logoHub(t)
	first.DB = db
	if got := getLogo(first, "name=gitea"); got.Code != 200 {
		t.Fatalf("%d", got.Code)
	}
	if got := getLogo(first, "name=monitoring"); got.Code != 404 {
		t.Fatalf("%d", got.Code)
	}

	// A new agent process: empty memory, a source that would now answer nothing at all.
	dead := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(dead.Close)
	second := &API{Client: &Client{ArtifactHubURL: dead.URL, IconsURL: dead.URL, GitHubURL: dead.URL, DockerHubURL: dead.URL}, DB: db}
	got := getLogo(second, "name=gitea")
	if got.Code != 200 || got.Body.String() != "png-bytes" {
		t.Fatalf("the logo should come from the database: %d %q", got.Code, got.Body)
	}
	if got := getLogo(second, "name=monitoring"); got.Code != 404 {
		t.Fatalf("a remembered miss stays a miss: %d", got.Code)
	}
	if *fetched != 1 {
		t.Fatalf("fetched %d times", *fetched)
	}
}
