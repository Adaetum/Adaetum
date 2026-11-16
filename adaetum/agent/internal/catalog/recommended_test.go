package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// artifactHubWith serves the named packages (repository/chart) and answers 404 for the rest.
func artifactHubWith(t *testing.T, have map[string]string, requests *atomic.Int32) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		id := strings.TrimPrefix(r.URL.Path, "/api/v1/packages/helm/")
		extra, ok := have[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		repository, name, _ := strings.Cut(id, "/")
		w.Write([]byte(`{"name":"` + name + `","version":"1.0.0","stars":7,` + extra + `"repository":{"name":"` + repository + `","verified_publisher":true}}`))
	}))
	t.Cleanup(server.Close)
	return &Client{ArtifactHubURL: server.URL}
}

func TestRecommendedKeepsTheListsOrderAndSaysWhyEachIsThere(t *testing.T) {
	var requests atomic.Int32
	have := map[string]string{}
	for _, app := range recommendedApps {
		have[app.id] = ""
	}
	apps, notes := artifactHubWith(t, have, &requests).Recommended(context.Background())
	if len(notes) != 0 || len(apps) != len(recommendedApps) {
		t.Fatalf("apps=%d notes=%v", len(apps), notes)
	}
	for index, app := range apps {
		if app.ID != recommendedApps[index].id || app.Why == "" || app.Stars != 7 || !app.Verified {
			t.Errorf("entry %d: %+v", index, app)
		}
	}
}

func TestRecommendedLeavesOutWhatCannotBeReadOrWasRetired(t *testing.T) {
	var requests atomic.Int32
	have := map[string]string{
		recommendedApps[0].id: "",
		recommendedApps[1].id: `"deprecated":true,`,
		// the rest are not on Artifact Hub (404)
	}
	apps, notes := artifactHubWith(t, have, &requests).Recommended(context.Background())
	if len(apps) != 1 || apps[0].ID != recommendedApps[0].id {
		t.Fatalf("only the healthy app belongs: %+v", apps)
	}
	if len(notes) != 1 {
		t.Fatalf("the gap is said once: %v", notes)
	}
}

func TestACompleteAnswerIsKeptButAnIncompleteOneIsSoonRetried(t *testing.T) {
	var requests atomic.Int32
	have := map[string]string{}
	for _, app := range recommendedApps {
		have[app.id] = ""
	}
	client := artifactHubWith(t, have, &requests)
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	var cache recommendedCache

	cache.get(context.Background(), client, clock)
	cache.get(context.Background(), client, clock)
	if got := int(requests.Load()); got != len(recommendedApps) {
		t.Fatalf("a second look inside the cache window must not ask Artifact Hub again: %d requests", got)
	}
	now = now.Add(recommendedFor + time.Minute)
	cache.get(context.Background(), client, clock)
	if got := int(requests.Load()); got != 2*len(recommendedApps) {
		t.Fatalf("after the window it is read again: %d requests", got)
	}

	// An incomplete answer is retried within a minute or so.
	delete(have, recommendedApps[0].id)
	now = now.Add(recommendedFor + time.Minute)
	cache.get(context.Background(), client, clock)
	before := requests.Load()
	now = now.Add(recommendedRetryFor + time.Second)
	cache.get(context.Background(), client, clock)
	if requests.Load() == before {
		t.Fatal("an incomplete answer must be retried soon, not kept for hours")
	}
}

func TestTheRecommendedRouteReturnsAListEvenWhenNothingLoads(t *testing.T) {
	var requests atomic.Int32
	api := &API{Client: artifactHubWith(t, map[string]string{}, &requests)}
	mux := http.NewServeMux()
	api.Register(mux)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest("GET", "/api/v1/catalog/recommended", nil))
	var body struct {
		Apps  []json.RawMessage `json:"apps"`
		Notes []string          `json:"notes"`
	}
	if recorder.Code != 200 || json.Unmarshal(recorder.Body.Bytes(), &body) != nil || body.Apps == nil || len(body.Apps) != 0 || len(body.Notes) != 1 {
		t.Fatalf("%d %s", recorder.Code, recorder.Body.String())
	}
}
