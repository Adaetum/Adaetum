package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Adaetum/Adaetum/embed/internal/engine"
)

func post(t *testing.T, s *Server, body string, token string) (int, map[string]any) {
	t.Helper()
	request := httptest.NewRequest("POST", "/embed", strings.NewReader(body))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	recorder := httptest.NewRecorder()
	s.Handler().ServeHTTP(recorder, request)
	var out map[string]any
	_ = json.Unmarshal(recorder.Body.Bytes(), &out)
	return recorder.Code, out
}

func TestEmbedReturnsOneUnitVectorPerTextWithTheModelName(t *testing.T) {
	s := &Server{Engine: engine.Hash{}, Kind: "hash"}
	code, out := post(t, s, `{"texts":["the disk is failing","certificates renew every ninety days"]}`, "")
	if code != 200 || out["model"] != "hash-lexical-256" || out["dim"].(float64) != 256 {
		t.Fatalf("%d %v", code, out)
	}
	vectors := out["vectors"].([]any)
	if len(vectors) != 2 {
		t.Fatalf("one vector per text: %d", len(vectors))
	}
	var norm float64
	for _, v := range vectors[0].([]any) {
		norm += v.(float64) * v.(float64)
	}
	if norm < 0.999 || norm > 1.001 {
		t.Fatalf("vectors must be unit length for cosine similarity: %v", norm)
	}
}

func TestRequestsAreValidatedAndBounded(t *testing.T) {
	s := &Server{Engine: engine.Hash{}, MaxBatch: 3, MaxTextSize: 20}
	cases := map[string]string{
		"not json":      `nope`,
		"no texts":      `{"texts":[]}`,
		"missing field": `{}`,
		"too many":      `{"texts":["a","b","c","d"]}`,
		"too long":      `{"texts":["` + strings.Repeat("x", 21) + `"]}`,
	}
	for name, body := range cases {
		if code, _ := post(t, s, body, ""); code != http.StatusBadRequest {
			t.Errorf("%s must be refused with 400, got %d", name, code)
		}
	}
	if code, _ := post(t, s, `{"texts":["ok","fine","good"]}`, ""); code != 200 {
		t.Fatalf("the limit itself is allowed: %d", code)
	}
}

func TestATokenIsRequiredWhenConfigured(t *testing.T) {
	s := &Server{Engine: engine.Hash{}, Token: "a-long-enough-secret"}
	for _, token := range []string{"", "wrong", "a-long-enough-secre"} {
		if code, _ := post(t, s, `{"texts":["x"]}`, token); code != http.StatusUnauthorized {
			t.Errorf("token %q must be refused: %d", token, code)
		}
	}
	if code, _ := post(t, s, `{"texts":["x"]}`, "a-long-enough-secret"); code != 200 {
		t.Fatal("the right token must work")
	}
}

type slowEngine struct {
	engine.Hash
	release chan struct{}
}

func (e slowEngine) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	select {
	case <-e.release:
	case <-ctx.Done():
	}
	return e.Hash.Embed(ctx, texts)
}

func TestACallerWaitingForABusyServiceIsToldSoNotLeftHanging(t *testing.T) {
	release := make(chan struct{})
	s := &Server{Engine: slowEngine{release: release}, Concurrency: 1}
	s.init()
	s.gate <- struct{}{} // occupy the only slot
	done := make(chan int, 1)
	go func() {
		code, _ := post(t, s, `{"texts":["x"]}`, "")
		done <- code
	}()
	select {
	case code := <-done:
		t.Fatalf("it should be waiting its turn, not answering %d yet", code)
	case <-time.After(100 * time.Millisecond):
	}
	<-s.gate
	close(release)
	if code := <-done; code != 200 {
		t.Fatalf("once a slot frees the request must be served: %d", code)
	}
}

type brokenEngine struct{ engine.Hash }

func (brokenEngine) Embed(context.Context, []string) ([][]float32, error) {
	return nil, errors.New("model exploded: secret internal detail")
}

func TestAnEngineFailureIsA500ThatLeaksNothing(t *testing.T) {
	s := &Server{Engine: brokenEngine{}}
	code, out := post(t, s, `{"texts":["x"]}`, "")
	if code != 500 || strings.Contains(out["error"].(string), "secret") {
		t.Fatalf("%d %v", code, out)
	}
}

func TestReadinessProvesTheModelWorksAndLivenessDoesNotDependOnIt(t *testing.T) {
	good := &Server{Engine: engine.Hash{}}
	bad := &Server{Engine: brokenEngine{}}
	get := func(s *Server, path string) int {
		recorder := httptest.NewRecorder()
		s.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", path, nil))
		return recorder.Code
	}
	if get(good, "/readyz") != 200 || get(good, "/healthz") != 200 {
		t.Fatal("a working model is ready and alive")
	}
	if get(bad, "/readyz") != http.StatusServiceUnavailable {
		t.Fatal("a model that cannot embed must not receive traffic")
	}
	if get(bad, "/healthz") != 200 {
		t.Fatal("the process is alive even when the model is not: restarting would not necessarily help, and readiness already keeps traffic away")
	}
}

func TestInfoDescribesWhatIsRunningAndCountsWork(t *testing.T) {
	s := &Server{Engine: engine.Hash{}, Kind: "hash"}
	post(t, s, `{"texts":["a","b"]}`, "")
	recorder := httptest.NewRecorder()
	s.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/info", nil))
	var info map[string]any
	json.Unmarshal(recorder.Body.Bytes(), &info)
	if info["engine"] != "hash" || info["texts"].(float64) != 2 || info["requests"].(float64) != 1 || info["stateless"] != true {
		t.Fatalf("%v", info)
	}
}
