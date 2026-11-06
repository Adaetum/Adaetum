package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

// localChat is a fake OpenAI-compatible service on loopback, so it counts as "this
// machine" (no key, no confirmation needed).
func localChat(t *testing.T) *httptest.Server {
	server, _, _ := fakeChatService(t, 200, textReply, textReply)
	return server
}

func TestSettingsStartOffAndASavedChoiceNeedsAPassingTest(t *testing.T) {
	m := &Manager{}
	if m.Status().Enabled || !strings.Contains(m.Status().Reason, "off") {
		t.Fatalf("starts off: %+v", m.Status())
	}
	server := localChat(t)
	good := Settings{Enabled: true, Provider: "openai", URL: server.URL + "/v1", Model: "m"}

	bad := good
	bad.URL = server.URL + "/nope"
	if _, err := m.Set(context.Background(), bad, "", false); err == nil || m.Status().Enabled {
		t.Fatalf("a setup that does not work must not be adopted: %v", err)
	}
	result, err := m.Set(context.Background(), good, "", false)
	if err != nil || !result.OK || result.Reply != "All good." {
		t.Fatalf("%v %+v", err, result)
	}
	status := m.Status()
	if !status.Enabled || status.Model != "m" {
		t.Fatalf("%+v", status)
	}

	// Takes effect on the next message, and turning it off needs no test.
	reply, err := m.NewMessage(context.Background(), minimalParams())
	if err != nil || len(reply.Content) == 0 {
		t.Fatalf("%v", err)
	}
	if _, err := m.Set(context.Background(), Settings{Provider: "openai", URL: good.URL, Model: "m"}, "", false); err != nil {
		t.Fatal(err)
	}
	if m.Status().Enabled {
		t.Fatal("switching off must stick")
	}
	if _, err := m.NewMessage(context.Background(), minimalParams()); err == nil {
		t.Fatal("a switched-off assistant must not call out")
	}
}

func TestAnOutsideServiceNeedsConfirmationAndAKey(t *testing.T) {
	m := &Manager{}
	cfg := Settings{Enabled: true, Provider: "groq", Model: "m"}
	if r, err := m.Set(context.Background(), cfg, "", false); err == nil || !strings.Contains(r.Error, "API key") {
		t.Fatalf("no key must be said plainly: %v %+v", err, r)
	}
	// With a key but no confirmation it is refused before anything is sent.
	if _, err := m.Set(context.Background(), cfg, "k", false); err == nil || !strings.Contains(err.Error(), "Confirm") {
		t.Fatalf("an outside service needs confirmation: %v", err)
	}
	if m.Status().Enabled {
		t.Fatal("nothing may be adopted")
	}
}

func TestKeysAreNeverSharedAcrossProvidersOrReturned(t *testing.T) {
	server := localChat(t)
	m := &Manager{StartupKeys: map[string]string{"anthropic": "startup-claude-key"}}
	cfg := Settings{Enabled: true, Provider: "openai", URL: server.URL + "/v1", Model: "m"}
	if _, err := m.Set(context.Background(), cfg, "typed-secret-key", false); err != nil {
		t.Fatal(err)
	}
	if m.KeySource() != KeyEntered {
		t.Fatalf("source: %q", m.KeySource())
	}
	// Switching provider drops the typed key; Anthropic falls back to its startup key.
	cfg2 := Settings{Provider: "ollama", Model: "m"}
	if _, err := m.Set(context.Background(), cfg2, "", false); err != nil {
		t.Fatal(err)
	}
	if m.KeySource() != KeyNone {
		t.Fatalf("a key typed for one provider must not follow to another: %q", m.KeySource())
	}
	m.Set(context.Background(), Settings{Provider: "anthropic"}, "", false)
	if m.KeySource() != KeyStartup {
		t.Fatalf("startup key: %q", m.KeySource())
	}
	for _, view := range []any{m.Settings(), m.Status()} {
		encoded, _ := json.Marshal(view)
		if strings.Contains(string(encoded), "typed-secret-key") || strings.Contains(string(encoded), "startup-claude-key") {
			t.Fatalf("a key leaked: %s", encoded)
		}
	}
}

func TestErrorsDoNotEchoTheKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad credential topsecretkey123", 401)
	}))
	defer server.Close()
	m := &Manager{}
	result := m.Test(context.Background(), Settings{Provider: "openai", URL: server.URL + "/v1", Model: "m"}, "topsecretkey123")
	if result.OK || strings.Contains(result.Error, "topsecretkey123") {
		t.Fatalf("%+v", result)
	}
}

func TestResolveRules(t *testing.T) {
	cases := []struct {
		name   string
		cfg    Settings
		key    string
		reason string
	}{
		{"anthropic needs a key", Settings{Provider: "anthropic"}, "", "no API key"},
		{"anthropic defaults the model", Settings{Provider: "anthropic"}, "k", ""},
		{"a preset needs a model", Settings{Provider: "groq"}, "k", "name the model"},
		{"a preset needs a key", Settings{Provider: "groq", Model: "m"}, "", "no API key"},
		{"local needs no key", Settings{Provider: "ollama", Model: "m"}, "", ""},
		{"custom needs an address", Settings{Provider: "openai", Model: "m"}, "k", "address"},
		{"plain http to a remote host is refused", Settings{Provider: "openai", URL: "http://example.com/v1", Model: "m"}, "k", "https"},
		{"metadata addresses are refused", Settings{Provider: "openai", URL: "http://169.254.169.254/v1", Model: "m"}, "k", "not allowed"},
		{"credentials in the address are refused", Settings{Provider: "openai", URL: "https://u:p@example.com/v1", Model: "m"}, "k", "username"},
		{"unknown provider", Settings{Provider: "bing"}, "k", "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, reason := Resolve(tc.cfg, tc.key)
			if (tc.reason == "") != (reason == "") || !strings.Contains(reason, tc.reason) {
				t.Fatalf("reason %q, want %q", reason, tc.reason)
			}
		})
	}
}

func minimalParams() anthropic.MessageNewParams {
	return anthropic.MessageNewParams{
		MaxTokens: 16,
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("hi"))},
	}
}
