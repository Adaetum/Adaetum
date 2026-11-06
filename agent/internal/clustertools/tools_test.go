package clustertools

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Adaetum/Adaetum/agent/internal/agentapi"
	"github.com/Adaetum/Adaetum/agent/internal/mcp"
	"github.com/Adaetum/Adaetum/agent/internal/memory"
	"github.com/Adaetum/Adaetum/agent/internal/policy"
	"github.com/Adaetum/Adaetum/agent/internal/store"
)

type memSettings struct {
	mu   sync.Mutex
	data map[string]policy.Setting
}

func (m *memSettings) Setting(key string) (policy.Setting, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.data[key]
	return s, ok, nil
}
func (m *memSettings) SaveSetting(s policy.Setting) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data == nil {
		m.data = map[string]policy.Setting{}
	}
	m.data[s.Key] = s
	return nil
}

type env struct {
	api    agentapi.Client
	board  *policy.Switchboard
	memory *memory.Service
	audit  []policy.AuditEntry
}

// newEnv serves the REAL security and memory APIs (behind a token check) and
// reaches them in-process, exactly as the in-app assistant does.
func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{}
	ok := func() (bool, string) { return true, "" }
	e.board = policy.NewSwitchboard(&memSettings{}, func(a policy.AuditEntry) error { e.audit = append(e.audit, a); return nil },
		policy.Lever{Key: "ansible_launcher", Title: "Launch", Toggleable: true, Available: ok, OffReason: "off"},
		policy.Lever{Key: "host_ssh", Title: "Commands", Toggleable: true, Available: ok, OffReason: "off"})
	db, err := store.OpenDatabase(filepath.Join(t.TempDir(), "m.db"), memory.Migrations)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e.memory = &memory.Service{DB: db, Embedder: memory.HashEmbedder{}}

	guard := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer secret" {
				http.Error(w, "login", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	mux := http.NewServeMux()
	(&policy.API{Board: e.board, RequireUser: guard}).Register(mux)
	(&memory.API{Service: e.memory, RequireUser: guard}).Register(mux)
	e.api = agentapi.Client{HTTP: &http.Client{Transport: agentapi.ServeTransport{Handler: mux}}, URL: "http://agent", Token: "secret"}
	return e
}

func call(t *testing.T, tools []mcp.Tool, name string, args any) (any, error) {
	t.Helper()
	data, _ := json.Marshal(args)
	for _, tool := range tools {
		if tool.Name == name {
			return tool.Call(context.Background(), data)
		}
	}
	t.Fatalf("no tool %q", name)
	return nil, nil
}

func TestEveryToolThatChangesSomethingSaysSoAndReadersDoNot(t *testing.T) {
	changes := map[string]bool{
		"launch_playbook": true, "cancel_playbook_run": true, "run_host_command": true, "create_join_token": true,
		"approve_host": true, "set_security_setting": true, "forget": true, "set_embedding": true, "reembed_memories": true,
	}
	reads := []string{"get_embedding", "test_embedding", "get_cluster", "list_playbooks", "list_playbook_runs", "get_playbook_run", "get_security_settings", "recall", "remember", "search_apps", "preview_app_install"}
	seen := map[string]bool{}
	for _, tool := range Tools(agentapi.Client{}, Options{}) {
		seen[tool.Name] = true
		if want, listed := changes[tool.Name]; listed && tool.Mutates != want {
			t.Errorf("%s must be marked as changing things (the client or page asks the person first)", tool.Name)
		}
	}
	for _, name := range reads {
		if !seen[name] {
			t.Errorf("missing tool %s", name)
		}
	}
	for name := range changes {
		if !seen[name] {
			t.Errorf("missing tool %s", name)
		}
	}
	for _, tool := range Tools(agentapi.Client{}, Options{}) {
		if !changes[tool.Name] && tool.Mutates {
			t.Errorf("%s is marked as changing things but is not on the list of changers", tool.Name)
		}
	}
}

func TestEveryToolHasADescriptionAndAnObjectSchema(t *testing.T) {
	for _, tool := range Tools(agentapi.Client{}, Options{}) {
		if len(tool.Description) < 30 || tool.InputSchema["type"] != "object" {
			t.Errorf("%s needs a real description and an object schema", tool.Name)
		}
	}
}

func TestSecuritySwitchesCannotBeTurnedOnUnlessAllowedButAlwaysOff(t *testing.T) {
	e := newEnv(t)
	tools := Tools(e.api, Options{Via: policy.ViaAssistant, AllowSecurityOn: false})
	if _, err := call(t, tools, "set_security_setting", map[string]any{"key": "host_ssh", "enabled": true, "durationMinutes": 30, "reason": "checking the disk on node-a"}); err == nil || !strings.Contains(err.Error(), "Security page") {
		t.Fatalf("an assistant that has not been allowed must be pointed at the page: %v", err)
	}
	if on, _ := e.board.Enabled("host_ssh"); on || len(e.audit) != 0 {
		t.Fatal("nothing may have changed or reached the agent")
	}
	e.board.Set("host_ssh", policy.SetRequest{Enabled: true, Confirm: "host_ssh", By: "operator"})
	if _, err := call(t, tools, "set_security_setting", map[string]any{"key": "host_ssh", "enabled": false}); err != nil {
		t.Fatalf("turning off is always allowed: %v", err)
	}
	if on, _ := e.board.Enabled("host_ssh"); on {
		t.Fatal("it must be off")
	}
}

func TestAnAllowedAssistantsChangeIsRecordedWithHowAndWhy(t *testing.T) {
	e := newEnv(t)
	for _, via := range []string{policy.ViaMCP, policy.ViaAssistant} {
		e.audit = nil
		tools := Tools(e.api, Options{Via: via, AllowSecurityOn: true})
		if _, err := call(t, tools, "set_security_setting", map[string]any{"key": "ansible_launcher", "enabled": true, "durationMinutes": 30, "reason": "installing the agent on node-b as asked"}); err != nil {
			t.Fatal(err)
		}
		if len(e.audit) != 1 || e.audit[0].Via != via || !strings.Contains(e.audit[0].Reason, "node-b") {
			t.Fatalf("%s: the record must say how and why: %+v", via, e.audit)
		}
		if _, err := call(t, tools, "set_security_setting", map[string]any{"key": "ansible_launcher", "enabled": true, "reason": "no time limit given at all"}); err == nil {
			t.Fatalf("%s: the agent must still demand a time limit", via)
		}
		e.board.Set("ansible_launcher", policy.SetRequest{Enabled: false})
	}
}

func TestMemoryToolsRememberRecallAndForgetThroughTheRealAPI(t *testing.T) {
	e := newEnv(t)
	tools := Tools(e.api, Options{Via: policy.ViaAssistant})
	saved, err := call(t, tools, "remember", map[string]any{"text": "node-c reboots on tuesdays at 03:00", "tags": []string{"ops"}})
	if err != nil {
		t.Fatal(err)
	}
	id := saved.(map[string]any)["id"].(string)
	if saved.(map[string]any)["source"] != "assistant" {
		t.Fatalf("memory must record who saved it: %v", saved)
	}
	found, err := call(t, tools, "recall", map[string]any{"query": "when does node-c reboot"})
	if err != nil || len(found.(map[string]any)["results"].([]any)) != 1 {
		t.Fatalf("%v %v", found, err)
	}
	if _, err := call(t, tools, "remember", map[string]any{"text": "the token is sk-ant-api03-abcdefghijklmnop"}); err == nil {
		t.Fatal("a secret must be refused, in the agent's own words")
	}
	if _, err := call(t, tools, "forget", map[string]any{"id": id}); err != nil {
		t.Fatal(err)
	}
	if again, _ := call(t, tools, "recall", map[string]any{"query": "node-c reboot tuesdays"}); len(again.(map[string]any)["results"].([]any)) != 0 {
		t.Fatal("a forgotten memory must be gone")
	}
}

func TestTheAgentsRefusalsComeBackInItsOwnWords(t *testing.T) {
	e := newEnv(t)
	bad := agentapi.Client{HTTP: e.api.HTTP, URL: e.api.URL, Token: "wrong"}
	_, err := call(t, Tools(bad, Options{}), "recall", map[string]any{"query": "anything"})
	if err == nil {
		t.Fatal("a wrong token must fail")
	}
	good := Tools(e.api, Options{})
	if _, err := call(t, good, "get_playbook_run", map[string]any{}); err == nil {
		t.Fatal("a missing id must be refused before any request")
	}
}

func TestTheAssistantCanNeverAgreeToSendMemoriesOutsideTheCluster(t *testing.T) {
	e := newEnv(t)
	tools := Tools(e.api, Options{Via: policy.ViaAssistant})
	for _, name := range []string{"set_embedding", "test_embedding"} {
		if _, err := call(t, tools, name, map[string]any{"kind": "openai", "url": "https://api.example.com/v1/embeddings"}); err == nil || !strings.Contains(err.Error(), "Embedding page") {
			t.Errorf("%s must send an outside service to the page: %v", name, err)
		}
	}
}

func TestGetSecuritySettingsListsTheSwitches(t *testing.T) {
	e := newEnv(t)
	got, err := call(t, Tools(e.api, Options{}), "get_security_settings", map[string]any{})
	if err != nil || len(got.([]policy.State)) != 2 {
		t.Fatalf("%v %v", got, err)
	}
}

func TestEveryAssistantCoreToolExistsAndEveryToolIsGrouped(t *testing.T) {
	names := map[string]bool{}
	for _, tool := range Tools(agentapi.Client{}, Options{}) {
		names[tool.Name] = true
		if tool.Group == "" {
			t.Errorf("%s has no group, so it cannot be listed as something to search for", tool.Name)
		}
	}
	for _, name := range AssistantCore {
		if !names[name] {
			t.Errorf("the assistant's core tool %q does not exist", name)
		}
	}
}
