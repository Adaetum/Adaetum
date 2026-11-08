package cluster

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Adaetum/Adaetum/agent/internal/assistant"
)

// The chosen AI service and the key typed into the page survive a restart (the tables
// are in the registry's schema), and "forget" removes the key for good.
func TestTheAssistantChoiceAndKeySurviveARestart(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"choices":[{"finish_reason":"stop","message":{"content":"ready"}}]}`)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "cluster.db")
	registry, err := OpenRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	m := &assistant.Manager{DB: registry.DB()}
	cfg := assistant.Settings{Enabled: true, Provider: "openai", URL: server.URL + "/v1", Model: "free-model"}
	if _, err := m.Set(context.Background(), cfg, "typed-key", false); err != nil {
		t.Fatal(err)
	}
	registry.Close()

	registry, err = OpenRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	again := &assistant.Manager{DB: registry.DB()}
	if got := again.Settings(); got != cfg {
		t.Fatalf("saved choice: %+v", got)
	}
	if again.KeySource() != assistant.KeyEntered {
		t.Fatal("a typed key must survive a restart")
	}
	if err := again.ForgetKey(); err != nil {
		t.Fatal(err)
	}
	third := &assistant.Manager{DB: registry.DB()}
	if third.KeySource() != assistant.KeyNone {
		t.Fatal("a forgotten key must be gone from the database")
	}
}
