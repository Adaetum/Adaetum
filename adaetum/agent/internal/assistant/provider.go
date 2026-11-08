package assistant

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// Settings is which AI service the assistant uses. It is a SETTING (changed from the
// web page, saved in the database), and contains no secret: the API key is held
// separately and never stored or shown (see Manager).
type Settings struct {
	// Enabled is the master switch. Off means no conversation is sent anywhere.
	Enabled bool `json:"enabled"`
	// Provider is one of the preset ids below, or "openai" for any other
	// OpenAI-compatible service (then URL is required).
	Provider string `json:"provider"`
	URL      string `json:"url,omitempty"`
	Model    string `json:"model,omitempty"`
}

// Preset describes a provider the page offers.
type Preset struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	URL      string `json:"url,omitempty"`
	Free     bool   `json:"free"`
	Local    bool   `json:"local"`
	NeedsKey bool   `json:"needsKey"`
	Hint     string `json:"hint"`
}

// Presets are the providers worth offering. Model names are deliberately NOT preset
// for the OpenAI-compatible ones: free model names change often, so the operator
// picks a current one from the service's own list.
func Presets() []Preset {
	return []Preset{
		{ID: "anthropic", Label: "Anthropic (Claude)", NeedsKey: true, Hint: "The best at using the tools and following the rehearse-first rule. Paid."},
		{ID: "gemini", Label: "Google Gemini", URL: "https://generativelanguage.googleapis.com/v1beta/openai", Free: true, NeedsKey: true, Hint: "Has a free tier: create a key in Google AI Studio."},
		{ID: "groq", Label: "Groq", URL: "https://api.groq.com/openai/v1", Free: true, NeedsKey: true, Hint: "Has a free tier: create a key in the Groq console."},
		{ID: "openrouter", Label: "OpenRouter", URL: "https://openrouter.ai/api/v1", Free: true, NeedsKey: true, Hint: "Several models are free to use; pick one marked free."},
		{ID: "ollama", Label: "Ollama on this machine", URL: "http://127.0.0.1:11434/v1", Free: true, Local: true, Hint: "Runs a model on this machine, no key, and nothing leaves it."},
		{ID: "openai", Label: "Another OpenAI-compatible service", NeedsKey: true, Hint: "Any service that speaks the OpenAI chat format. Needs its address."},
	}
}

func preset(id string) (Preset, bool) {
	for _, p := range Presets() {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}

// Provider is a Settings made ready to use.
type Provider struct {
	Call   func(context.Context, anthropic.MessageNewParams) (*anthropic.Message, error)
	Status Status
	// External is true when conversations would leave this machine.
	External bool
	// Notice is a one-line statement of where words go, for the operator's log.
	Notice string
}

// Resolve turns a Settings and a key into a Provider. A non-empty reason says what is
// wrong, in words a person can act on.
func Resolve(cfg Settings, key string) (Provider, string) {
	id := strings.ToLower(strings.TrimSpace(cfg.Provider))
	if id == "" {
		id = "anthropic"
	}
	p, known := preset(id)
	if !known {
		return Provider{}, fmt.Sprintf("unknown provider %q", cfg.Provider)
	}
	model := strings.TrimSpace(cfg.Model)

	if id == "anthropic" {
		if key == "" {
			return Provider{}, "no API key for Anthropic: enter one, or start the agent with -assistant-key-file or ANTHROPIC_API_KEY."
		}
		if model == "" {
			model = DefaultModel
		}
		client := anthropic.NewClient(option.WithAPIKey(key))
		return Provider{
			Call: func(ctx context.Context, params anthropic.MessageNewParams) (*anthropic.Message, error) {
				return client.Messages.New(ctx, params)
			},
			Status:   Status{Enabled: true, Model: model, Provider: "Anthropic", Endpoint: "api.anthropic.com"},
			External: true,
			Notice:   fmt.Sprintf("assistant: ON (Anthropic, model %s). Conversations and tool results are sent to the Anthropic API; changes need approval in the page.", model),
		}, ""
	}

	baseURL := strings.TrimSpace(cfg.URL)
	if baseURL == "" {
		baseURL = p.URL
	}
	if err := validateEndpoint(baseURL); err != nil {
		return Provider{}, err.Error()
	}
	if model == "" {
		return Provider{}, "name the model to use (pick a current free one from the service's own list)."
	}
	parsed, _ := url.Parse(baseURL)
	local := isLoopbackHost(parsed.Hostname())
	// Plain http would send the key and the conversation in the clear.
	if parsed.Scheme == "http" && !local {
		return Provider{}, "the address must be https unless it points at this machine."
	}
	if key == "" && !local {
		return Provider{}, "no API key for " + p.Label + ": enter one (a free key from the service works)."
	}

	adapter := &OpenAICompatible{BaseURL: baseURL, APIKey: key}
	where := "this machine only"
	if !local {
		where = "an outside service (" + parsed.Host + "), which may keep and train on it: test data only, no real secrets"
	}
	return Provider{
		Call:     adapter.NewMessage,
		Status:   Status{Enabled: true, Model: model, Provider: p.Label, Endpoint: parsed.Host},
		External: !local,
		Notice:   fmt.Sprintf("assistant: ON (%s, model %s). Conversations go to %s; changes need approval in the page.", id, model, where),
	}, ""
}

// validateEndpoint accepts an http(s) address naming a host, with no credentials in
// it, and refuses link-local space (cloud metadata services).
func validateEndpoint(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return fmt.Errorf("give the service's address, such as https://api.groq.com/openai/v1")
	}
	if parsed.User != nil {
		return fmt.Errorf("do not put a username or password in the address")
	}
	host := parsed.Hostname()
	if ip := net.ParseIP(host); ip != nil && (ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast()) {
		return fmt.Errorf("that address is not allowed (link-local, such as a cloud metadata service)")
	}
	if strings.EqualFold(host, "metadata.google.internal") {
		return fmt.Errorf("that address is not allowed (a cloud metadata service)")
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
