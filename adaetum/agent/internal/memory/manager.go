package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// Where embeddings come from is a setting people can change from the web page, so the
// embedder the memory service uses is a Manager: it holds the current choice, can be
// swapped at run time, and can test a candidate before it is adopted.
//
// The choices (Config.Kind):
//
//	builtin  a lexical stand-in that needs nothing (development; not semantic)
//	service  Adaetum's own embedding service, the dedicated pod (embed/)
//	openai   any external service that speaks the OpenAI embeddings API
//	none     no embedding: memories are found by keyword only
//
// Changing the choice never loses a memory. Vectors record the model that made them
// and are only compared with vectors of the same model, so after a change old vectors
// simply stop counting until "re-embed" (RequeueAll) makes new ones.

// Kinds of embedder.
const (
	KindBuiltin = "builtin"
	KindService = "service"
	KindOpenAI  = "openai"
	KindNone    = "none"
)

// Config is the chosen embedder.
type Config struct {
	Kind string `json:"kind"`
	// URL of the service (service, openai). For openai it is the full embeddings
	// endpoint, such as https://api.example.com/v1/embeddings.
	URL string `json:"url,omitempty"`
	// Model is the embedding model's name. For "service" it is learned from the
	// service itself when the choice is tested.
	Model string `json:"model,omitempty"`
}

// DefaultServiceURL is where the dedicated pod is expected to be in the cluster.
const DefaultServiceURL = "http://adaetum-embed.adaetum.svc/embed"

// TestResult says whether a candidate embedder works and how well.
type TestResult struct {
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
	Model     string `json:"model,omitempty"`
	Dim       int    `json:"dim,omitempty"`
	LatencyMs int64  `json:"latencyMs,omitempty"`
	// Semantic reports whether the embedder understands meaning: two sentences that
	// mean the same thing with no words in common must score clearly higher than an
	// unrelated one. A lexical stand-in fails this on purpose.
	Semantic  bool    `json:"semantic"`
	Related   float64 `json:"related"`
	Unrelated float64 `json:"unrelated"`
	// External is true when the text would leave the cluster.
	External bool `json:"external"`
}

// Manager is an Embedder that can be reconfigured while running.
type Manager struct {
	DB *sql.DB
	// APIKey is the key for the openai kind. It comes from how the agent was started
	// (a private file), is never stored in the database, and is never shown.
	APIKey string
	// Startup is the choice made by the agent's flags, used until someone changes it.
	Startup Config
	// Client is the HTTP client for service calls (nil: a default with a timeout).
	Client *http.Client

	mu      sync.RWMutex
	cfg     Config
	current Embedder
	loaded  bool
}

// Migrations for the saved choice.
var ManagerMigrations = []string{
	`CREATE TABLE embedding_config (
		id      INTEGER PRIMARY KEY CHECK (id = 1),
		json    TEXT NOT NULL,
		changed_ms INTEGER NOT NULL
	)`,
}

func (m *Manager) client() *http.Client {
	if m.Client != nil {
		return m.Client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// load reads the saved choice (or the startup one) the first time it is needed.
func (m *Manager) load() {
	if m.loaded {
		return
	}
	m.loaded = true
	m.cfg = m.Startup
	if m.cfg.Kind == "" {
		m.cfg.Kind = KindBuiltin
	}
	if m.DB != nil {
		var saved string
		if err := m.DB.QueryRow(`SELECT json FROM embedding_config WHERE id = 1`).Scan(&saved); err == nil {
			var cfg Config
			if json.Unmarshal([]byte(saved), &cfg) == nil && cfg.Kind != "" {
				m.cfg = cfg
			}
		}
	}
	if embedder, err := m.build(m.cfg); err == nil {
		m.current = embedder
	}
}

// Config returns the current choice.
func (m *Manager) Config() Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.load()
	return m.cfg
}

// Model implements Embedder.
func (m *Manager) Model() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.load()
	if m.current == nil {
		return "none"
	}
	return m.current.Model()
}

// Embed implements Embedder. With no embedder configured it returns an error, which the
// service treats like any embedding failure: keyword search carries on.
func (m *Manager) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	m.mu.Lock()
	m.load()
	current := m.current
	m.mu.Unlock()
	if current == nil {
		return nil, errors.New("embedding is switched off or its settings are not usable")
	}
	return current.Embed(ctx, texts)
}

// Enabled reports whether any embedder is in use.
func (m *Manager) Enabled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.load()
	return m.current != nil
}

// build makes an embedder for a Config without using it.
func (m *Manager) build(cfg Config) (Embedder, error) {
	switch cfg.Kind {
	case KindNone:
		return nil, nil
	case KindBuiltin:
		return HashEmbedder{}, nil
	case KindService:
		if err := ValidateURL(cfg.URL); err != nil {
			return nil, err
		}
		model := cfg.Model
		if model == "" {
			model = "minilm-l6-v2-int8"
		}
		return HTTPEmbedder{URL: cfg.URL, ModelName: model, Client: m.client()}, nil
	case KindOpenAI:
		if err := ValidateURL(cfg.URL); err != nil {
			return nil, err
		}
		if cfg.Model == "" {
			return nil, errors.New("name the embedding model to ask the service for")
		}
		if m.APIKey == "" {
			return nil, errors.New("this agent was started without an API key for external embedding services (-embedder-key-file)")
		}
		return OpenAIEmbedder{URL: cfg.URL, ModelName: cfg.Model, Key: m.APIKey, Client: m.client()}, nil
	default:
		return nil, fmt.Errorf("unknown kind %q", cfg.Kind)
	}
}

// IsExternal reports whether embedding with this config sends memory text outside the
// cluster: anything that is not loopback, a cluster-internal name, or a private
// address.
func IsExternal(cfg Config) bool {
	if cfg.Kind == KindBuiltin || cfg.Kind == KindNone {
		return false
	}
	parsed, err := url.Parse(cfg.URL)
	if err != nil {
		return true
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".svc") || strings.Contains(host, ".svc.") ||
		strings.HasSuffix(host, ".cluster.local") || strings.HasSuffix(host, ".local") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return !(ip.IsLoopback() || ip.IsPrivate())
	}
	// A bare single-label name (a Service in the same namespace) is internal.
	return strings.Contains(host, ".")
}

// ValidateURL accepts an http or https URL naming a host, with no credentials in it,
// and refuses addresses that reach cloud metadata services or link-local space.
func ValidateURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return errors.New("give the address of the service, for example http://adaetum-embed.adaetum.svc/embed")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("the address must start with http:// or https://")
	}
	if parsed.User != nil {
		return errors.New("do not put a username or password in the address")
	}
	host := parsed.Hostname()
	if ip := net.ParseIP(host); ip != nil && (ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast()) {
		return errors.New("that address is not allowed (link-local, such as a cloud metadata service)")
	}
	if strings.EqualFold(host, "metadata.google.internal") {
		return errors.New("that address is not allowed (a cloud metadata service)")
	}
	return nil
}

// Test tries a candidate embedder without adopting it: it embeds three short sentences
// and reports speed, the vector size, and whether the embedder is really semantic.
func (m *Manager) Test(ctx context.Context, cfg Config) TestResult {
	result := TestResult{External: IsExternal(cfg)}
	if cfg.Kind == KindNone {
		return TestResult{OK: true, Model: "none"}
	}
	candidate, err := m.build(cfg)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	// The service tells us its own model name; use it so a candidate is judged as it
	// would run, whatever name was typed.
	if cfg.Kind == KindService {
		if reported, err := probeModel(ctx, m.client(), cfg.URL); err == nil && reported != "" {
			candidate = HTTPEmbedder{URL: cfg.URL, ModelName: reported, Client: m.client()}
		}
	}

	sentences := []string{
		"The storage drive on the server is failing and needs to be replaced.", // a
		"A hard disk has hardware faults and must be swapped out.",             // b: same meaning, no shared key words
		"How do I bake sourdough bread?",                                       // c: unrelated
	}
	testCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	started := time.Now()
	vectors, err := candidate.Embed(testCtx, sentences)
	result.LatencyMs = time.Since(started).Milliseconds()
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.OK = true
	result.Model = candidate.Model()
	result.Dim = len(vectors[0])
	result.Related = cosine(vectors[0], vectors[1])
	result.Unrelated = cosine(vectors[0], vectors[2])
	result.Semantic = result.Related > result.Unrelated+0.15 && result.Related > 0.25
	return result
}

// probeModel asks an Adaetum embedding service which model it runs, from its /info.
func probeModel(ctx context.Context, client *http.Client, embedURL string) (string, error) {
	parsed, err := url.Parse(embedURL)
	if err != nil {
		return "", err
	}
	parsed.Path = "/info"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	var info struct {
		Model string `json:"model"`
	}
	if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&info) != nil {
		return "", errors.New("no /info")
	}
	return info.Model, nil
}

// Set adopts a new choice: it must work (Test), it is saved, and it takes effect at
// once. External services need confirmExternal, because memory text would leave the
// cluster.
func (m *Manager) Set(ctx context.Context, cfg Config, confirmExternal bool) (TestResult, error) {
	cfg.URL = strings.TrimSpace(cfg.URL)
	cfg.Model = strings.TrimSpace(cfg.Model)
	if IsExternal(cfg) && !confirmExternal {
		return TestResult{External: true}, errors.New("this service is outside the cluster, so what is remembered would be sent to it. Confirm that you want that")
	}
	result := m.Test(ctx, cfg)
	if !result.OK {
		return result, errors.New("the service did not work, so the setting was not changed: " + result.Error)
	}
	if cfg.Kind == KindService {
		cfg.Model = result.Model // adopt the model the service really runs
	}
	embedder, err := m.build(cfg)
	if err != nil {
		return result, err
	}
	encoded, _ := json.Marshal(cfg)
	if m.DB != nil {
		if _, err := m.DB.ExecContext(ctx, `INSERT OR REPLACE INTO embedding_config(id, json, changed_ms) VALUES (1, ?, ?)`, string(encoded), time.Now().UnixMilli()); err != nil {
			return result, fmt.Errorf("the choice could not be saved: %w", err)
		}
	}
	m.mu.Lock()
	m.loaded, m.cfg, m.current = true, cfg, embedder
	m.mu.Unlock()
	return result, nil
}

// Models lists, for each embedding model, how many stored vectors it made.
func (s *Service) Models(ctx context.Context) (map[string]int, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT e.model, count(*) FROM embeddings e JOIN memories m ON m.id = e.memory_id WHERE m.deleted_ms IS NULL GROUP BY e.model`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var model string
		var n int
		if err := rows.Scan(&model, &n); err != nil {
			return nil, err
		}
		out[model] = n
	}
	return out, rows.Err()
}

// RequeueAll makes every memory that lacks a vector from the CURRENT model queue for
// one, and drops vectors made by other models (they can no longer be compared, and
// keeping them would only make memories look searchable by meaning when they are not).
// It returns how many memories were queued.
func (s *Service) RequeueAll(ctx context.Context) (int, error) {
	embedder := s.embedder()
	if embedder == nil {
		return 0, errors.New("embedding is switched off")
	}
	model := embedder.Model()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM embeddings WHERE model != ?`, model); err != nil {
		return 0, err
	}
	now := s.now().UnixMilli()
	result, err := tx.ExecContext(ctx, `
		INSERT OR REPLACE INTO embedding_jobs(memory_id, state, attempts, next_try_ms, updated_ms)
		SELECT m.id, 'pending', 0, ?, ? FROM memories m
		WHERE m.deleted_ms IS NULL AND NOT EXISTS (SELECT 1 FROM embeddings e WHERE e.memory_id = m.id)`, now, now)
	if err != nil {
		return 0, err
	}
	n, _ := result.RowsAffected()
	return int(n), tx.Commit()
}

// SortedModels is Models as a stable list, for display.
func SortedModels(models map[string]int) []string {
	names := make([]string, 0, len(models))
	for name := range models {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
