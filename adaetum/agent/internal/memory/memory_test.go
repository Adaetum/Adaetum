package memory

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/store"
)

var t0 = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

func newService(t *testing.T, embedder Embedder) (*Service, *clock) {
	t.Helper()
	db, err := store.OpenDatabase(filepath.Join(t.TempDir(), "m.db"), Migrations)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c := &clock{now: t0}
	var n int64
	return &Service{DB: db, Embedder: embedder, Now: c.Now, NewID: func() string { return string(rune('a' + atomic.AddInt64(&n, 1))) }}, c
}

type brokenEmbedder struct{ calls int }

func (b *brokenEmbedder) Model() string { return "broken" }
func (b *brokenEmbedder) Embed(context.Context, []string) ([][]float32, error) {
	b.calls++
	return nil, errors.New("embedding service down")
}

func TestRememberNeverWaitsForOrDependsOnTheEmbedder(t *testing.T) {
	broken := &brokenEmbedder{}
	s, _ := newService(t, broken)
	memory, err := s.Remember(context.Background(), "node-a has a failing disk", []string{"hardware"}, "assistant")
	if err != nil || memory.Status != "queued" || broken.calls != 0 {
		t.Fatalf("remembering must be a plain write: %+v %v calls=%d", memory, err, broken.calls)
	}
	nothing, _ := newService(t, nil)
	if _, err := nothing.Remember(context.Background(), "works with no embedder at all", nil, "x"); err != nil {
		t.Fatalf("a missing embedder must not stop memory: %v", err)
	}
}

func TestAMemoryIsFoundByKeywordImmediatelyBeforeAnyVectorExists(t *testing.T) {
	s, _ := newService(t, HashEmbedder{})
	s.Remember(context.Background(), "The registry pull on node-b is slow because of the VPN", nil, "user")
	results, err := s.Search(context.Background(), "why is the registry slow", 5)
	if err != nil || len(results) != 1 || results[0].Match != "keyword" {
		t.Fatalf("a just-remembered memory must be findable at once: %+v %v", results, err)
	}
}

func TestTheWorkerEmbedsQueuedMemoriesAndSearchBecomesSemantic(t *testing.T) {
	s, _ := newService(t, HashEmbedder{})
	ctx := context.Background()
	s.Remember(ctx, "node-a disk is failing and needs replacing", nil, "u")
	s.Remember(ctx, "the cluster certificate renews every ninety days", nil, "u")
	if worked, err := s.ProcessBatch(ctx); err != nil || !worked {
		t.Fatalf("%v %v", worked, err)
	}
	status, _ := s.Status(ctx)
	if status.Embedded != 2 || status.Queued != 0 || status.Degraded {
		t.Fatalf("both should be embedded: %+v", status)
	}
	results, _ := s.Search(ctx, "disk failing node-a", 3)
	if len(results) == 0 || results[0].Match != "semantic" || !strings.Contains(results[0].Text, "disk") {
		t.Fatalf("the disk memory must rank first by similarity: %+v", results)
	}
	if again, _ := s.ProcessBatch(ctx); again {
		t.Fatal("a drained queue has no work")
	}
}

func TestAFailingEmbedderIsRetriedWithBackoffThenParkedAndNeverBlocksSearch(t *testing.T) {
	broken := &brokenEmbedder{}
	s, c := newService(t, broken)
	ctx := context.Background()
	s.Remember(ctx, "keep working when embedding is down", nil, "u")

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if worked, _ := s.ProcessBatch(ctx); !worked {
			t.Fatalf("attempt %d: the job should be due", attempt)
		}
		// Not due again until the backoff passes.
		if worked, _ := s.ProcessBatch(ctx); worked {
			t.Fatalf("attempt %d: it must wait out its backoff", attempt)
		}
		c.now = c.now.Add(maxBackoff + time.Second)
	}
	status, _ := s.Status(ctx)
	if status.Failed != 1 || !status.Degraded || !strings.Contains(status.LastError, "down") {
		t.Fatalf("after the retries the job is parked and the degradation is reported: %+v", status)
	}
	if worked, _ := s.ProcessBatch(ctx); worked {
		t.Fatal("a parked job is not retried automatically")
	}
	results, err := s.Search(ctx, "embedding down working", 3)
	if err != nil || len(results) != 1 || results[0].Match != "keyword" {
		t.Fatalf("search must still work, by keyword: %+v %v", results, err)
	}

	// Once the service is fixed, a person can retry the parked jobs.
	s.Embedder = HashEmbedder{}
	if n, _ := s.RetryFailed(ctx); n != 1 {
		t.Fatalf("expected one job requeued: %d", n)
	}
	s.ProcessBatch(ctx)
	if st, _ := s.Status(ctx); st.Embedded != 1 || st.Failed != 0 {
		t.Fatalf("%+v", st)
	}
}

func TestForgettingRemovesTheTextTheVectorAndTheJob(t *testing.T) {
	s, _ := newService(t, HashEmbedder{})
	ctx := context.Background()
	m, _ := s.Remember(ctx, "remember the old gateway password policy", nil, "u")
	s.ProcessBatch(ctx)
	if err := s.Forget(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if results, _ := s.Search(ctx, "gateway password policy", 5); len(results) != 0 {
		t.Fatalf("a forgotten memory must not be found: %+v", results)
	}
	if st, _ := s.Status(ctx); st.Total != 0 || st.Embedded != 0 {
		t.Fatalf("%+v", st)
	}
	if err := s.Forget(ctx, m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("forgetting twice is not found: %v", err)
	}
}

func TestSecretsAreRefusedBeforeTheyCanBeStoredOrSentOnward(t *testing.T) {
	s, _ := newService(t, nil)
	for _, text := range []string{
		"the key is sk-ant-api03-abcdefghijklmnop",
		"join with adj_" + strings.Repeat("a1", 16),
		"-----BEGIN OPENSSH PRIVATE KEY----- abc",
		"token " + strings.Repeat("ab", 32),
	} {
		if _, err := s.Remember(context.Background(), text, nil, "u"); err == nil {
			t.Errorf("%q must be refused", text[:20])
		}
	}
}

func TestInputLimits(t *testing.T) {
	s, _ := newService(t, nil)
	ctx := context.Background()
	if _, err := s.Remember(ctx, "   ", nil, "u"); err == nil {
		t.Error("empty")
	}
	if _, err := s.Remember(ctx, strings.Repeat("x ", maxTextLength), nil, "u"); err == nil {
		t.Error("too long")
	}
	if _, err := s.Remember(ctx, "ok", []string{"a,b"}, "u"); err == nil {
		t.Error("a tag with a comma")
	}
	if _, err := s.Remember(ctx, "ok", []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}, "u"); err == nil {
		t.Error("too many tags")
	}
	if _, err := s.Search(ctx, "  ", 3); err == nil {
		t.Error("an empty question")
	}
}

func TestVectorsFromADifferentModelAreNeverCompared(t *testing.T) {
	s, _ := newService(t, HashEmbedder{Dim: 64})
	ctx := context.Background()
	s.Remember(ctx, "alpha beta gamma delta", nil, "u")
	s.ProcessBatch(ctx)
	s.Embedder = HashEmbedder{Dim: 128} // a different space
	results, _ := s.Search(ctx, "alpha beta gamma delta", 3)
	for _, r := range results {
		if r.Match == "semantic" {
			t.Fatal("vectors from another model must not be compared")
		}
	}
	if len(results) != 1 || results[0].Match != "keyword" {
		t.Fatalf("it should still be found by keyword: %+v", results)
	}
}

func TestHTTPEmbedderChecksTheServicesAnswer(t *testing.T) {
	var reply string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Texts []string }
		json.NewDecoder(r.Body).Decode(&in)
		w.Write([]byte(reply))
	}))
	defer server.Close()
	e := HTTPEmbedder{URL: server.URL, ModelName: "minilm"}
	cases := map[string]string{
		"wrong count": `{"model":"minilm","vectors":[[1,2],[3,4]]}`,
		"wrong model": `{"model":"other","vectors":[[1,2]]}`,
		"empty vec":   `{"model":"minilm","vectors":[[]]}`,
		"junk":        `nope`,
	}
	for name, body := range cases {
		reply = body
		if _, err := e.Embed(context.Background(), []string{"x"}); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
	reply = `{"model":"minilm","vectors":[[0.5,0.5]]}`
	got, err := e.Embed(context.Background(), []string{"x"})
	if err != nil || len(got) != 1 || got[0][0] != 0.5 {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := (HTTPEmbedder{URL: "http://127.0.0.1:1", ModelName: "m"}).Embed(context.Background(), []string{"x"}); err == nil {
		t.Fatal("an unreachable service is an error, not a hang or a panic")
	}
}

func TestTheAPIRequiresALoginAndRoundTrips(t *testing.T) {
	s, _ := newService(t, HashEmbedder{})
	api := &API{Service: s, RequireUser: func(next http.Handler) http.Handler {
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
	do := func(method, path, body, token string) (int, string) {
		request, _ := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		buf := make([]byte, 8192)
		n, _ := response.Body.Read(buf)
		return response.StatusCode, string(buf[:n])
	}
	if code, _ := do("GET", "/api/v1/memory", "", ""); code != 401 {
		t.Fatalf("listing needs a login: %d", code)
	}
	if code, body := do("POST", "/api/v1/memory", `{"text":"node-c reboots on tuesdays","tags":["ops"],"source":"test"}`, "ok"); code != 200 || !strings.Contains(body, "queued") {
		t.Fatalf("%d %s", code, body)
	}
	if code, body := do("POST", "/api/v1/memory/search", `{"query":"when does node-c reboot"}`, "ok"); code != 200 || !strings.Contains(body, "node-c") {
		t.Fatalf("%d %s", code, body)
	}
	if code, body := do("GET", "/api/v1/memory/status", "", "ok"); code != 200 || !strings.Contains(body, "hash-lexical") {
		t.Fatalf("%d %s", code, body)
	}
}
