package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

// Which AI service the assistant uses is a setting people change from the web page, so
// the assistant calls the model through a Manager: it holds the current choice, can be
// swapped while running, and tests a candidate before adopting it.
//
// THE KEY. The provider/address/model are saved in the database, and so is a key typed
// into the page, in its own table (assistant_key), so it survives restarts and is the
// same for everyone using this cluster agent. This is a deliberate choice: the database
// lives in the agent's private directory (mode 700), so reading it already needs
// host-level access. The key is NEVER returned by any API or written to a log; the
// settings and status only say where it came from. A key the agent was started with
// (file or environment) is the fallback when none was typed.
//
// If the database later leaves that protection (backups, a shared volume), revisit
// this: encrypt the key at rest or hold it in OpenBao instead.
//
// WHO CAN CHANGE IT. A signed-in person, through the API below. The assistant itself
// has no tool for it: it cannot choose where its own conversations are sent.

// AssistantMigrations creates the table for the saved choice.
var AssistantMigrations = []string{
	`CREATE TABLE assistant_config (
		id         INTEGER PRIMARY KEY CHECK (id = 1),
		json       TEXT NOT NULL,
		changed_ms INTEGER NOT NULL
	)`,
	// The key typed into the page, kept apart from the settings so no code path that
	// returns settings can return it. provider records which service it belongs to.
	`CREATE TABLE assistant_key (
		id         INTEGER PRIMARY KEY CHECK (id = 1),
		provider   TEXT NOT NULL,
		api_key    TEXT NOT NULL,
		changed_ms INTEGER NOT NULL
	)`,
}

// KeySource says where the key in use came from, for display. Never the key itself.
const (
	KeyNone    = ""
	KeyEntered = "entered"
	KeyStartup = "startup"
)

// Manager implements the model call and its settings.
type Manager struct {
	DB *sql.DB
	// Startup is the choice made by the agent's flags, used until someone saves one.
	Startup Settings
	// StartupKeys are keys given when the agent started (a private file or the
	// environment): "anthropic" for Anthropic, "other" for everything else.
	StartupKeys map[string]string

	mu         sync.RWMutex
	cfg        Settings
	loaded     bool
	enteredKey string
}

func (m *Manager) load() {
	if m.loaded {
		return
	}
	m.loaded = true
	m.cfg = m.Startup
	if m.DB != nil {
		var saved string
		if err := m.DB.QueryRow(`SELECT json FROM assistant_config WHERE id = 1`).Scan(&saved); err == nil {
			var cfg Settings
			if json.Unmarshal([]byte(saved), &cfg) == nil && cfg.Provider != "" {
				m.cfg = cfg
			}
		}
	}
	if m.cfg.Provider == "" {
		m.cfg.Provider = "anthropic"
	}
	if m.DB != nil {
		var provider, key string
		if err := m.DB.QueryRow(`SELECT provider, api_key FROM assistant_key WHERE id = 1`).Scan(&provider, &key); err == nil && provider == m.cfg.Provider {
			m.enteredKey = key
		}
	}
}

// Settings returns the current choice.
func (m *Manager) Settings() Settings {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.load()
	return m.cfg
}

func (m *Manager) startupKey(provider string) string {
	if provider == "anthropic" {
		return m.StartupKeys["anthropic"]
	}
	return m.StartupKeys["other"]
}

// keyFor is the key to use with a provider: the one typed into the page, else the one
// the agent started with.
func (m *Manager) keyFor(provider string) (key, source string) {
	if m.enteredKey != "" {
		return m.enteredKey, KeyEntered
	}
	if k := m.startupKey(provider); k != "" {
		return k, KeyStartup
	}
	return "", KeyNone
}

// KeySource reports whether a key is available for the current provider and where it
// came from.
func (m *Manager) KeySource() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.load()
	_, source := m.keyFor(m.cfg.Provider)
	return source
}

// Status is what the page reads first: whether the assistant can be used, and why not.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.load()
	if !m.cfg.Enabled {
		return Status{Reason: OffReason}
	}
	key, _ := m.keyFor(m.cfg.Provider)
	p, reason := Resolve(m.cfg, key)
	if reason != "" {
		return Status{Reason: "The assistant is switched on but cannot start: " + reason}
	}
	return p.Status
}

// Notice is the operator-log line for the current choice ("" when off or unusable).
func (m *Manager) Notice() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.load()
	if !m.cfg.Enabled {
		return ""
	}
	key, _ := m.keyFor(m.cfg.Provider)
	p, _ := Resolve(m.cfg, key)
	return p.Notice
}

// NewMessage is the model call the conversation loop uses. It resolves the CURRENT
// settings on every call, so a change takes effect on the next message.
func (m *Manager) NewMessage(ctx context.Context, params anthropic.MessageNewParams) (*anthropic.Message, error) {
	m.mu.Lock()
	m.load()
	cfg := m.cfg
	key, _ := m.keyFor(cfg.Provider)
	m.mu.Unlock()
	if !cfg.Enabled {
		return nil, errors.New("the assistant is switched off")
	}
	p, reason := Resolve(cfg, key)
	if reason != "" {
		return nil, errors.New(reason)
	}
	params.Model = anthropic.Model(p.Status.Model)
	return p.Call(ctx, params)
}

// TestResult says whether a candidate setup works.
type TestResult struct {
	OK        bool   `json:"ok"`
	Error     string `json:"error,omitempty"`
	Provider  string `json:"provider,omitempty"`
	Model     string `json:"model,omitempty"`
	Endpoint  string `json:"endpoint,omitempty"`
	LatencyMs int64  `json:"latencyMs,omitempty"`
	Reply     string `json:"reply,omitempty"`
	// External is true when conversations would leave this machine.
	External bool `json:"external"`
}

func normalize(cfg Settings) Settings {
	cfg.Provider = strings.ToLower(strings.TrimSpace(cfg.Provider))
	cfg.URL = strings.TrimSpace(cfg.URL)
	cfg.Model = strings.TrimSpace(cfg.Model)
	if cfg.Provider == "" {
		cfg.Provider = "anthropic"
	}
	if p, ok := preset(cfg.Provider); ok && cfg.Provider != "openai" && cfg.URL == p.URL {
		cfg.URL = "" // a preset's address is implied, not saved
	}
	return cfg
}

func validKey(key string) error {
	if len(key) > 512 || strings.ContainsAny(key, " \t\r\n\x00") {
		return errors.New("that does not look like an API key (no spaces or line breaks, under 512 characters)")
	}
	return nil
}

// effectiveKey picks the key a candidate would use: the one just typed, else the key
// in use if the provider is not changing, else the startup key for that provider. A
// key entered for one provider is never silently sent to another.
func (m *Manager) effectiveKey(cfg Settings, typed string) string {
	if typed != "" {
		return typed
	}
	if m.enteredKey != "" && cfg.Provider == m.cfg.Provider {
		return m.enteredKey
	}
	return m.startupKey(cfg.Provider)
}

// Test tries a candidate without adopting it: one tiny message, no tools.
func (m *Manager) Test(ctx context.Context, cfg Settings, typedKey string) TestResult {
	cfg = normalize(cfg)
	if err := validKey(typedKey); err != nil {
		return TestResult{Error: err.Error()}
	}
	m.mu.Lock()
	m.load()
	key := m.effectiveKey(cfg, strings.TrimSpace(typedKey))
	m.mu.Unlock()

	p, reason := Resolve(cfg, key)
	result := TestResult{External: p.External}
	if reason != "" {
		result.Error = reason
		return result
	}
	result.Provider, result.Model, result.Endpoint = p.Status.Provider, p.Status.Model, p.Status.Endpoint

	testCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	started := time.Now()
	reply, err := p.Call(testCtx, anthropic.MessageNewParams{
		Model:     anthropic.Model(p.Status.Model),
		MaxTokens: 64,
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("Reply with the single word: ready"))},
	})
	result.LatencyMs = time.Since(started).Milliseconds()
	if err != nil {
		result.Error = scrub(err.Error(), key)
		return result
	}
	for _, block := range reply.Content {
		if text, ok := block.AsAny().(anthropic.TextBlock); ok {
			result.Reply = shorten(text.Text, 80)
			break
		}
	}
	if result.Reply == "" {
		result.Error = "the service answered but sent no text, so it may not be a chat model"
		return result
	}
	result.OK = true
	return result
}

func scrub(text, key string) string {
	if key != "" {
		text = strings.ReplaceAll(text, key, "…")
	}
	return text
}

// Set adopts a new choice. Switching ON needs a passing test, and an outside service
// needs confirmExternal because conversations would leave the machine. Switching OFF
// needs nothing. typedKey, if given, is saved (see THE KEY above).
func (m *Manager) Set(ctx context.Context, cfg Settings, typedKey string, confirmExternal bool) (TestResult, error) {
	cfg = normalize(cfg)
	typedKey = strings.TrimSpace(typedKey)
	if err := validKey(typedKey); err != nil {
		return TestResult{}, err
	}

	var result TestResult
	if cfg.Enabled {
		result = m.Test(ctx, cfg, typedKey)
		if result.External && !confirmExternal {
			return result, errors.New("this service is outside this machine, so what you say to the assistant and what it reads from the agent would be sent to it. Confirm that you want that")
		}
		if !result.OK {
			return result, errors.New("the service did not work, so the setting was not changed: " + result.Error)
		}
		cfg.Model = result.Model // adopt the model actually used (the default for Anthropic)
	}

	encoded, _ := json.Marshal(cfg)
	if m.DB != nil {
		if _, err := m.DB.ExecContext(ctx, `INSERT OR REPLACE INTO assistant_config(id, json, changed_ms) VALUES (1, ?, ?)`, string(encoded), time.Now().UnixMilli()); err != nil {
			return result, fmt.Errorf("the choice could not be saved: %w", err)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.load()
	switch {
	case typedKey != "":
		if err := m.saveKey(ctx, cfg.Provider, typedKey); err != nil {
			return result, err
		}
		m.enteredKey = typedKey
	case cfg.Provider != m.cfg.Provider && m.enteredKey != "":
		// A key entered for another service is not carried over.
		if err := m.saveKey(ctx, "", ""); err != nil {
			return result, err
		}
		m.enteredKey = ""
	}
	m.cfg = cfg
	return result, nil
}

// saveKey stores (or, with an empty key, deletes) the key typed into the page.
func (m *Manager) saveKey(ctx context.Context, provider, key string) error {
	if m.DB == nil {
		return nil
	}
	var err error
	if key == "" {
		_, err = m.DB.ExecContext(ctx, `DELETE FROM assistant_key WHERE id = 1`)
	} else {
		_, err = m.DB.ExecContext(ctx, `INSERT OR REPLACE INTO assistant_key(id, provider, api_key, changed_ms) VALUES (1, ?, ?, ?)`, provider, key, time.Now().UnixMilli())
	}
	if err != nil {
		return fmt.Errorf("the key could not be saved: %w", err)
	}
	return nil
}

// ForgetKey drops a key that was typed into the page, from memory and the database.
func (m *Manager) ForgetKey() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.saveKey(context.Background(), "", ""); err != nil {
		return err
	}
	m.enteredKey = ""
	return nil
}
