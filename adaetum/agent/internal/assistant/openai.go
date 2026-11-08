package assistant

// An adapter that lets the assistant talk to any OpenAI-compatible chat service
// (Google Gemini's compatibility endpoint, Groq, OpenRouter, a local Ollama, ...)
// instead of Anthropic. It exists so the chat can be tried against a FREE model
// while the platform is in development.
//
// DESIGN: the conversation loop in assistant.go keeps one internal dialect (the
// Anthropic message shapes) and decides nothing about providers. This file is the
// only translator: it turns the loop's request into a chat/completions request and
// the reply back into the loop's shape. Approvals, the tool list, the transcript and
// the safety rules are therefore identical for every provider.
//
// WHAT TO EXPECT FROM FREE MODELS: they are weaker at choosing tools and following
// the "rehearse first" rule, and free tiers rate-limit hard (a 429 is reported to the
// person as such). That is safe by construction, because every change still waits
// for the person's approval; it can just be less helpful. Free tiers also commonly
// keep and train on what is sent: use them for testing, never with real secrets.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

// OpenAICompatible talks to a chat/completions endpoint.
type OpenAICompatible struct {
	// BaseURL is the service's API root, for example "https://api.groq.com/openai/v1";
	// "/chat/completions" is added.
	BaseURL string
	// APIKey is sent as a bearer token. Empty is allowed (a local Ollama has none).
	APIKey string
	HTTP   *http.Client
}

const (
	openAIResponseLimit = 4 << 20
	openAITimeout       = 2 * time.Minute
)

// NewMessage has the same shape as the Anthropic call so the loop can use either.
func (o *OpenAICompatible) NewMessage(ctx context.Context, params anthropic.MessageNewParams) (*anthropic.Message, error) {
	body, err := o.requestBody(params)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(o.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	if o.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+o.APIKey)
	}
	client := o.HTTP
	if client == nil {
		client = &http.Client{Timeout: openAITimeout}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, openAIResponseLimit))
	if err != nil {
		return nil, err
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return nil, errors.New("the AI service is rate limiting this key (free tiers do); wait a minute and try again")
	}
	if response.StatusCode/100 != 2 {
		return nil, fmt.Errorf("the AI service answered %d: %s", response.StatusCode, shorten(string(data), 300))
	}
	return o.parseReply(data, params.Model)
}

// chatMessage is one message in the chat/completions request.
type chatMessage struct {
	Role       string     `json:"role"`
	Content    *string    `json:"content"`
	ToolCalls  []chatCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type chatCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// requestBody translates the loop's request. It goes through JSON rather than the
// SDK's Go types so it depends only on the stable wire shape of the messages.
func (o *OpenAICompatible) requestBody(params anthropic.MessageNewParams) ([]byte, error) {
	rawMessages, err := json.Marshal(params.Messages)
	if err != nil {
		return nil, err
	}
	var history []struct {
		Role    string `json:"role"`
		Content []struct {
			Type      string          `json:"type"`
			Text      string          `json:"text"`
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			Input     json.RawMessage `json:"input"`
			ToolUseID string          `json:"tool_use_id"`
			Content   json.RawMessage `json:"content"`
		} `json:"content"`
	}
	if err := json.Unmarshal(rawMessages, &history); err != nil {
		return nil, err
	}

	var system []string
	for _, block := range params.System {
		system = append(system, block.Text)
	}
	messages := []chatMessage{}
	if len(system) > 0 {
		text := strings.Join(system, "\n\n")
		messages = append(messages, chatMessage{Role: "system", Content: &text})
	}
	for _, message := range history {
		var text []string
		assistantMessage := chatMessage{Role: "assistant"}
		for _, block := range message.Content {
			switch block.Type {
			case "text":
				text = append(text, block.Text)
			case "tool_use":
				call := chatCall{ID: block.ID, Type: "function"}
				call.Function.Name = block.Name
				call.Function.Arguments = string(block.Input)
				if call.Function.Arguments == "" {
					call.Function.Arguments = "{}"
				}
				assistantMessage.ToolCalls = append(assistantMessage.ToolCalls, call)
			case "tool_result":
				// One tool message per result, in order, as the format requires.
				result := toolResultText(block.Content)
				messages = append(messages, chatMessage{Role: "tool", ToolCallID: block.ToolUseID, Content: &result})
			}
		}
		joined := strings.Join(text, "\n")
		switch {
		case message.Role == "assistant":
			if joined != "" {
				assistantMessage.Content = &joined
			}
			messages = append(messages, assistantMessage)
		case joined != "":
			messages = append(messages, chatMessage{Role: "user", Content: &joined})
		}
	}

	type chatTool struct {
		Type     string `json:"type"`
		Function struct {
			Name        string         `json:"name"`
			Description string         `json:"description,omitempty"`
			Parameters  map[string]any `json:"parameters"`
		} `json:"function"`
	}
	rawTools, err := json.Marshal(params.Tools)
	if err != nil {
		return nil, err
	}
	var offered []struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		InputSchema map[string]any `json:"input_schema"`
	}
	if err := json.Unmarshal(rawTools, &offered); err != nil {
		return nil, err
	}
	tools := make([]chatTool, 0, len(offered))
	for _, tool := range offered {
		entry := chatTool{Type: "function"}
		entry.Function.Name, entry.Function.Description = tool.Name, tool.Description
		entry.Function.Parameters = tool.InputSchema
		if entry.Function.Parameters == nil {
			entry.Function.Parameters = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		tools = append(tools, entry)
	}

	request := map[string]any{"model": string(params.Model), "messages": messages}
	if len(tools) > 0 {
		request["tools"] = tools
	}
	// A modest cap: free tiers reject large ones, and chat answers are short.
	request["max_tokens"] = 4096
	return json.Marshal(request)
}

// toolResultText flattens a tool result (a string or a list of text blocks).
func toolResultText(raw json.RawMessage) string {
	var plain string
	if json.Unmarshal(raw, &plain) == nil {
		return plain
	}
	var blocks []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		parts := make([]string, 0, len(blocks))
		for _, block := range blocks {
			parts = append(parts, block.Text)
		}
		return strings.Join(parts, "\n")
	}
	return string(raw)
}

// parseReply turns a chat/completions answer into the loop's message shape.
func (o *OpenAICompatible) parseReply(data []byte, model anthropic.Model) (*anthropic.Message, error) {
	var answer struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content   *string    `json:"content"`
				ToolCalls []chatCall `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &answer); err != nil {
		return nil, fmt.Errorf("the AI service sent something unreadable: %s", shorten(string(data), 200))
	}
	if answer.Error != nil && len(answer.Choices) == 0 {
		return nil, fmt.Errorf("the AI service said: %s", shorten(answer.Error.Message, 300))
	}
	if len(answer.Choices) == 0 {
		return nil, errors.New("the AI service sent no answer")
	}
	choice := answer.Choices[0]

	content := []map[string]any{}
	if choice.Message.Content != nil && strings.TrimSpace(*choice.Message.Content) != "" {
		content = append(content, map[string]any{"type": "text", "text": *choice.Message.Content})
	}
	for index, call := range choice.Message.ToolCalls {
		id := call.ID
		if id == "" {
			// Some services leave it out; the loop needs a unique id to track approvals.
			id = fmt.Sprintf("call_%d_%d", time.Now().UnixNano()%1_000_000, index)
		}
		// Arguments arrive as a JSON string and models sometimes get it wrong: an
		// unreadable one becomes an empty object, and the tool reports what is missing.
		var input json.RawMessage = json.RawMessage("{}")
		var object map[string]any
		if json.Unmarshal([]byte(call.Function.Arguments), &object) == nil && object != nil {
			input = json.RawMessage(call.Function.Arguments)
		}
		content = append(content, map[string]any{"type": "tool_use", "id": id, "name": call.Function.Name, "input": input})
	}

	stop := "end_turn"
	switch {
	case len(choice.Message.ToolCalls) > 0:
		stop = "tool_use"
	case choice.FinishReason == "length":
		stop = "max_tokens"
	case choice.FinishReason == "content_filter":
		stop = "refusal"
	}
	encoded, err := json.Marshal(map[string]any{
		"id": "chatcmpl", "type": "message", "role": "assistant", "model": string(model),
		"content": content, "stop_reason": stop, "usage": map[string]int{"input_tokens": 0, "output_tokens": 0},
	})
	if err != nil {
		return nil, err
	}
	var message anthropic.Message
	if err := json.Unmarshal(encoded, &message); err != nil {
		return nil, err
	}
	return &message, nil
}

func shorten(text string, limit int) string {
	text = strings.TrimSpace(text)
	if len(text) > limit {
		return text[:limit] + "…"
	}
	return text
}
