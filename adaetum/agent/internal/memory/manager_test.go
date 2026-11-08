package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Adaetum/Adaetum/agent/internal/store"
)

func openDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := store.OpenDatabase(path, append(append([]string{}, Migrations...), ManagerMigrations...))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// semanticServer is a fake embedding service that "understands" that disk, drive and
// storage mean the same thing: it maps each text to a vector by topic.
func semanticServer(t *testing.T, model string) *httptest.Server {
	t.Helper()
	topic := func(text string) []float32 {
		text = strings.ToLower(text)
		switch {
		case strings.Contains(text, "drive") || strings.Contains(text, "disk") || strings.Contains(text, "storage"):
			return []float32{1, 0, 0}
		case strings.Contains(text, "bread"):
			return []float32{0, 0, 1}
		}
		return []float32{0, 1, 0}
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/info" {
			json.NewEncoder(w).Encode(map[string]any{"model": model, "dim": 3})
			return
		}
		var in struct{ Texts []string }
		json.NewDecoder(r.Body).Decode(&in)
		vectors := make([][]float32, len(in.Texts))
		for i, text := range in.Texts {
			vectors[i] = topic(text)
		}
		json.NewEncoder(w).Encode(map[string]any{"model": model, "dim": 3, "vectors": vectors})
	}))
}

func TestAServiceThatUnderstandsMeaningIsRecognisedAsSemanticAndALexicalOneIsNot(t *testing.T) {
	good := semanticServer(t, "fake-semantic")
	defer good.Close()
	m := &Manager{}
	semantic := m.Test(context.Background(), Config{Kind: KindService, URL: good.URL + "/embed"})
	if !semantic.OK || !semantic.Semantic || semantic.Model != "fake-semantic" || semantic.Dim != 3 {
		t.Fatalf("%+v", semantic)
	}
	lexical := m.Test(context.Background(), Config{Kind: KindBuiltin})
	if !lexical.OK || lexical.Semantic {
		t.Fatalf("the lexical stand-in must be reported as not semantic: %+v", lexical)
	}
}

func TestATestReportsAServiceThatIsDownWithoutChangingAnything(t *testing.T) {
	m := &Manager{}
	result := m.Test(context.Background(), Config{Kind: KindService, URL: "http://127.0.0.1:1/embed"})
	if result.OK || result.Error == "" {
		t.Fatalf("%+v", result)
	}
}

func TestSettingAServiceAdoptsItsModelSavesItAndSurvivesARestart(t *testing.T) {
	server := semanticServer(t, "real-model-v1")
	defer server.Close()
	path := filepath.Join(t.TempDir(), "m.db")
	db := openDB(t, path)
	m := &Manager{DB: db, Startup: Config{Kind: KindBuiltin}}
	if m.Model() != "hash-lexical-256" {
		t.Fatalf("the startup choice applies until someone changes it: %s", m.Model())
	}
	if _, err := m.Set(context.Background(), Config{Kind: KindService, URL: server.URL + "/embed", Model: "typed-wrong-name"}, true); err != nil {
		t.Fatal(err)
	}
	if m.Model() != "real-model-v1" || m.Config().Model != "real-model-v1" {
		t.Fatalf("the model the service really runs must be adopted: %s %+v", m.Model(), m.Config())
	}
	// A restart (a new Manager on the same database, with a different startup flag).
	again := &Manager{DB: db, Startup: Config{Kind: KindNone}}
	if again.Config().Kind != KindService || again.Model() != "real-model-v1" {
		t.Fatalf("the saved choice must beat the startup flag: %+v", again.Config())
	}
}

func TestAChoiceThatDoesNotWorkIsNotAdoptedAndTheOldOneKeepsRunning(t *testing.T) {
	m := &Manager{Startup: Config{Kind: KindBuiltin}}
	if _, err := m.Set(context.Background(), Config{Kind: KindService, URL: "http://127.0.0.1:1/embed"}, true); err == nil {
		t.Fatal("a service that is down must be refused")
	}
	if m.Config().Kind != KindBuiltin || m.Model() != "hash-lexical-256" {
		t.Fatalf("the old choice must keep running: %+v", m.Config())
	}
}

func TestAnExternalServiceNeedsExplicitConfirmation(t *testing.T) {
	server := semanticServer(t, "x")
	defer server.Close()
	for _, c := range []struct {
		url      string
		external bool
	}{
		{"http://adaetum-embed.adaetum.svc/embed", false},
		{"http://adaetum-embed/embed", false},
		{"http://embed.adaetum.svc.cluster.local/embed", false},
		{"http://127.0.0.1:8700/embed", false},
		{"http://10.43.1.2/embed", false},
		{"https://api.openai.com/v1/embeddings", true},
		{"https://embeddings.example.com/embed", true},
		{"http://8.8.8.8/embed", true},
	} {
		if got := IsExternal(Config{Kind: KindService, URL: c.url}); got != c.external {
			t.Errorf("%s: external = %v, want %v", c.url, got, c.external)
		}
	}
	m := &Manager{APIKey: "k"}
	_, err := m.Set(context.Background(), Config{Kind: KindOpenAI, URL: "https://api.example.com/v1/embeddings", Model: "m"}, false)
	if err == nil || !strings.Contains(err.Error(), "outside the cluster") {
		t.Fatalf("sending memories out needs a confirmation: %v", err)
	}
}

func TestUnsafeAddressesAreRefused(t *testing.T) {
	for _, raw := range []string{"", "not a url", "ftp://x/embed", "http://user:pass@host/embed", "http://169.254.169.254/latest", "http://0.0.0.0/embed", "http://metadata.google.internal/x", "file:///etc/passwd"} {
		if err := ValidateURL(raw); err == nil {
			t.Errorf("%q must be refused", raw)
		}
	}
	for _, raw := range []string{"http://adaetum-embed.adaetum.svc/embed", "https://api.example.com/v1/embeddings"} {
		if err := ValidateURL(raw); err != nil {
			t.Errorf("%q is fine: %v", raw, err)
		}
	}
}

func TestAnExternalServiceNeedsTheStartupKeyAndASentKeyIsNeverStored(t *testing.T) {
	var seenAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		var in struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		// Answer out of order, as real services may: indexes must be honoured.
		data := make([]map[string]any, len(in.Input))
		for i := range in.Input {
			j := len(in.Input) - 1 - i
			data[i] = map[string]any{"index": j, "embedding": []float32{float32(j + 1), 0, 0}}
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer server.Close()
	db := openDB(t, filepath.Join(t.TempDir(), "m.db"))

	noKey := &Manager{DB: db}
	if _, err := noKey.Set(context.Background(), Config{Kind: KindOpenAI, URL: server.URL, Model: "m"}, true); err == nil || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("no key means no external service: %v", err)
	}

	m := &Manager{DB: db, APIKey: "sk-test-secret"}
	if _, err := m.Set(context.Background(), Config{Kind: KindOpenAI, URL: server.URL, Model: "embed-small"}, true); err != nil {
		t.Fatal(err)
	}
	vectors, err := m.Embed(context.Background(), []string{"a", "b", "c"})
	if err != nil || vectors[0][0] != 1 || vectors[2][0] != 3 {
		t.Fatalf("vectors must be matched to texts by index: %v %v", vectors, err)
	}
	if seenAuth != "Bearer sk-test-secret" || m.Model() != "openai:embed-small" {
		t.Fatalf("%q %q", seenAuth, m.Model())
	}
	var stored string
	db.QueryRow(`SELECT json FROM embedding_config WHERE id = 1`).Scan(&stored)
	if strings.Contains(stored, "sk-test") {
		t.Fatalf("the key must never be written to the database: %s", stored)
	}
}

func TestSwitchingEmbeddingOffFallsBackToKeywordsWithoutRaisingAnAlarm(t *testing.T) {
	db := openDB(t, filepath.Join(t.TempDir(), "m.db"))
	m := &Manager{DB: db, Startup: Config{Kind: KindBuiltin}}
	s := &Service{DB: db, Embedder: m}
	ctx := context.Background()
	s.Remember(ctx, "node-b reboots on tuesdays", nil, "u")
	if _, err := m.Set(ctx, Config{Kind: KindNone}, false); err != nil {
		t.Fatal(err)
	}
	if worked, _ := s.ProcessBatch(ctx); worked {
		t.Fatal("nothing is embedded while embedding is off")
	}
	results, err := s.Search(ctx, "when does node-b reboot", 3)
	if err != nil || len(results) != 1 || results[0].Match != "keyword" {
		t.Fatalf("%+v %v", results, err)
	}
	status, _ := s.Status(ctx)
	if status.Degraded {
		t.Fatal("turned off on purpose is not a failure")
	}
	if !strings.Contains(status.Embedder, "none") {
		t.Fatalf("%+v", status)
	}
}

func TestChangingTheModelMakesOldVectorsStopCountingUntilReembedded(t *testing.T) {
	server := semanticServer(t, "model-b")
	defer server.Close()
	db := openDB(t, filepath.Join(t.TempDir(), "m.db"))
	m := &Manager{DB: db, Startup: Config{Kind: KindBuiltin}}
	s := &Service{DB: db, Embedder: m}
	ctx := context.Background()

	s.Remember(ctx, "the storage drive on node-a is failing", nil, "u")
	s.ProcessBatch(ctx)
	if models, _ := s.Models(ctx); models["hash-lexical-256"] != 1 {
		t.Fatalf("%v", models)
	}

	if _, err := m.Set(ctx, Config{Kind: KindService, URL: server.URL + "/embed"}, true); err != nil {
		t.Fatal(err)
	}
	// The old vector belongs to another space: the memory is no longer "semantic".
	results, _ := s.Search(ctx, "hard disk faults", 3)
	for _, r := range results {
		if r.Match == "semantic" {
			t.Fatal("a vector from another model must not be compared")
		}
	}

	queued, err := s.RequeueAll(ctx)
	if err != nil || queued != 1 {
		t.Fatalf("%d %v", queued, err)
	}
	if models, _ := s.Models(ctx); len(models) != 0 {
		t.Fatalf("vectors from other models are dropped: %v", models)
	}
	s.ProcessBatch(ctx)
	found, _ := s.Search(ctx, "hard disk faults", 3)
	if len(found) != 1 || found[0].Match != "semantic" {
		t.Fatalf("after re-embedding with a real model, meaning matches words it does not share: %+v", found)
	}
}

func TestTheEmbeddingAPIShowsStaleVectorsAndNeedsALogin(t *testing.T) {
	db := openDB(t, filepath.Join(t.TempDir(), "m.db"))
	m := &Manager{DB: db, Startup: Config{Kind: KindBuiltin}}
	s := &Service{DB: db, Embedder: m}
	ctx := context.Background()
	s.Remember(ctx, "something to embed", nil, "u")
	s.ProcessBatch(ctx)
	m.Set(ctx, Config{Kind: KindNone}, false)

	api := &EmbeddingAPI{Manager: m, Service: s, RequireUser: func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer ok" {
				http.Error(w, "login", 401)
				return
			}
			next.ServeHTTP(w, r)
		})
	}}
	mux := http.NewServeMux()
	api.Register(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	do := func(method, path, body, token string) (int, map[string]any) {
		request, _ := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var out map[string]any
		json.NewDecoder(response.Body).Decode(&out)
		return response.StatusCode, out
	}
	if code, _ := do("GET", "/api/v1/embedding", "", ""); code != 401 {
		t.Fatalf("%d", code)
	}
	code, body := do("GET", "/api/v1/embedding", "", "ok")
	if code != 200 || body["staleVectors"].(float64) != 1 || body["enabled"] != false {
		t.Fatalf("a vector made before embedding was switched off is stale: %d %v", code, body)
	}
	if code, body := do("PUT", "/api/v1/embedding", `{"kind":"openai","url":"https://api.example.com/v1/embeddings","model":"m"}`, "ok"); code != 400 || !strings.Contains(body["error"].(string), "outside the cluster") {
		t.Fatalf("%d %v", code, body)
	}
	if code, body := do("POST", "/api/v1/embedding/test", `{"kind":"builtin"}`, "ok"); code != 200 || body["semantic"] != false {
		t.Fatalf("%d %v", code, body)
	}
	if code, _ := do("POST", "/api/v1/embedding/reembed", "", "ok"); code != 400 {
		t.Fatalf("re-embedding while switched off is refused: %d", code)
	}
}
