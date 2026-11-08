// Package server is the embedding service's HTTP surface.
//
// The contract is small on purpose, because the cluster agent's client
// (agent/internal/memory.HTTPEmbedder) and this server are separate programs that must
// stay in step:
//
//	POST /embed    {"texts": ["...", "..."]}
//	200            {"model": "minilm-l6-v2-int8", "dim": 384, "vectors": [[...], [...]]}
//	GET  /healthz  200 while the process is up (liveness)
//	GET  /readyz   200 only if the model can embed right now (readiness)
//	GET  /info     what is running: model, dimension, engine, counters
//
// The service has no state. It keeps nothing between requests, so Kubernetes can stop
// it and start it on another node at any time.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Adaetum/Adaetum/embed/internal/engine"
)

// Limits. They bound what one caller can make a one-to-two-core pod do.
const (
	DefaultMaxBatch    = 64
	DefaultMaxTextSize = 16 * 1024
	maxBodyBytes       = 4 << 20
	requestTimeout     = 30 * time.Second
	queueTimeout       = 10 * time.Second
)

// Server serves an Engine.
type Server struct {
	Engine engine.Engine
	// Kind names the engine for /info ("onnx" or "hash").
	Kind string
	// Token, when set, must be presented as a bearer token on /embed. The pod is also
	// fenced by a NetworkPolicy; the token is a second lock.
	Token       string
	MaxBatch    int
	MaxTextSize int
	// Concurrency is how many embeds run at once (default 1: the pod is small).
	Concurrency int
	Now         func() time.Time

	once    sync.Once
	gate    chan struct{}
	started time.Time

	requests atomic.Int64
	texts    atomic.Int64
	failures atomic.Int64

	readyMu      sync.Mutex
	readyAt      time.Time
	readyOK      bool
	readyMessage string
}

func (s *Server) init() {
	s.once.Do(func() {
		if s.MaxBatch <= 0 {
			s.MaxBatch = DefaultMaxBatch
		}
		if s.MaxTextSize <= 0 {
			s.MaxTextSize = DefaultMaxTextSize
		}
		if s.Concurrency <= 0 {
			s.Concurrency = 1
		}
		if s.Now == nil {
			s.Now = time.Now
		}
		s.gate = make(chan struct{}, s.Concurrency)
		s.started = s.Now()
	})
}

// Handler returns the routes.
func (s *Server) Handler() http.Handler {
	s.init()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /embed", s.embed)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", s.ready)
	mux.HandleFunc("GET /info", s.info)
	return mux
}

func (s *Server) authorised(r *http.Request) bool {
	if s.Token == "" {
		return true
	}
	presented, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return found && subtle.ConstantTimeCompare([]byte(presented), []byte(s.Token)) == 1
}

func (s *Server) embed(w http.ResponseWriter, r *http.Request) {
	if !s.authorised(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="adaetum-embed"`)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "a token is required"})
		return
	}
	var body struct {
		Texts []string `json:"texts"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the request is not valid JSON of the form {\"texts\": [...]}"})
		return
	}
	switch {
	case len(body.Texts) == 0:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "send at least one text"})
		return
	case len(body.Texts) > s.MaxBatch:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "too many texts in one request"})
		return
	}
	for _, text := range body.Texts {
		if len(text) > s.MaxTextSize {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "a text is too long"})
			return
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	// Wait for a turn, but not forever: a caller that gives up is better served by a
	// quick "busy" than by a hung connection.
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	case <-time.After(queueTimeout):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the service is busy; try again shortly"})
		return
	case <-ctx.Done():
		return
	}

	s.requests.Add(1)
	vectors, err := s.Engine.Embed(ctx, body.Texts)
	if err != nil {
		s.failures.Add(1)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "embedding failed"})
		return
	}
	s.texts.Add(int64(len(body.Texts)))
	writeJSON(w, http.StatusOK, map[string]any{"model": s.Engine.Model(), "dim": s.Engine.Dim(), "vectors": vectors})
}

// ready proves the model works by embedding a short text, at most every ten seconds so
// a readiness probe cannot itself load the pod.
func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	s.readyMu.Lock()
	defer s.readyMu.Unlock()
	if s.Now().Sub(s.readyAt) > 10*time.Second {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		vectors, err := s.Engine.Embed(ctx, []string{"readiness check"})
		cancel()
		s.readyAt = s.Now()
		s.readyOK = err == nil && len(vectors) == 1 && len(vectors[0]) == s.Engine.Dim()
		s.readyMessage = "ok"
		if !s.readyOK {
			s.readyMessage = "the model could not embed a test text"
		}
	}
	status := http.StatusOK
	if !s.readyOK {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]string{"status": s.readyMessage})
}

func (s *Server) info(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"model":    s.Engine.Model(),
		"dim":      s.Engine.Dim(),
		"engine":   s.Kind,
		"uptime":   int64(s.Now().Sub(s.started).Seconds()),
		"requests": s.requests.Load(),
		"texts":    s.texts.Load(),
		"failures": s.failures.Load(),
		"maxBatch": s.MaxBatch,
		// Nothing is stored: this is the contract for operators, not a setting.
		"stateless": true,
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
