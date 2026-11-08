package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func testServer() *Server {
	return NewServer("adaetum-agent", "test", []Tool{
		{
			Name:        "echo",
			Description: "returns its argument",
			InputSchema: map[string]any{"type": "object"},
			Call: func(_ context.Context, arguments json.RawMessage) (any, error) {
				return map[string]string{"got": string(arguments)}, nil
			},
		},
		{
			Name:        "broken",
			InputSchema: map[string]any{"type": "object"},
			Call:        func(context.Context, json.RawMessage) (any, error) { return nil, errors.New("host unreachable") },
		},
		{
			Name:        "flip",
			Mutates:     true,
			InputSchema: map[string]any{"type": "object"},
			Call:        func(context.Context, json.RawMessage) (any, error) { return "done", nil },
		},
	})
}

// exchange sends the given lines and returns the decoded replies.
func exchange(t *testing.T, lines ...string) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	if err := testServer().Serve(context.Background(), strings.NewReader(strings.Join(lines, "\n")+"\n"), &out); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var replies []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var reply map[string]any
		if err := json.Unmarshal([]byte(line), &reply); err != nil {
			t.Fatalf("reply is not JSON: %q", line)
		}
		replies = append(replies, reply)
	}
	return replies
}

func TestInitializeAnswersWithTheServersVersionAndToolsCapability(t *testing.T) {
	replies := exchange(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`)
	result := replies[0]["result"].(map[string]any)
	if result["protocolVersion"] != protocolVersion {
		t.Fatalf("unexpected version: %v", result["protocolVersion"])
	}
	if _, ok := result["capabilities"].(map[string]any)["tools"]; !ok {
		t.Fatal("the tools capability must be declared")
	}
	if result["serverInfo"].(map[string]any)["name"] != "adaetum-agent" {
		t.Fatalf("unexpected server info: %v", result["serverInfo"])
	}
}

func TestNotificationsGetNoReply(t *testing.T) {
	replies := exchange(t,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","method":"nonsense"}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
	)
	if len(replies) != 1 || replies[0]["id"].(float64) != 2 {
		t.Fatalf("only the ping may be answered, even for an unknown notification: %v", replies)
	}
}

func TestEveryListedToolIsMarkedReadOnlyAndListedInOrder(t *testing.T) {
	replies := exchange(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	tools := replies[0]["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 3 || tools[0].(map[string]any)["name"] != "broken" || tools[1].(map[string]any)["name"] != "echo" || tools[2].(map[string]any)["name"] != "flip" {
		t.Fatalf("tools must be listed by name: %v", tools)
	}
	for _, raw := range tools {
		tool := raw.(map[string]any)
		annotations := tool["annotations"].(map[string]any)
		mutates := tool["name"] == "flip"
		if annotations["readOnlyHint"] != !mutates || annotations["destructiveHint"] != mutates {
			t.Fatalf("a tool is annotated wrongly (only the mutating one may say it changes things): %v", raw)
		}
	}
}

func TestToolCallReturnsTheResultAsJSONText(t *testing.T) {
	replies := exchange(t, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"a":1}}}`)
	result := replies[0]["result"].(map[string]any)
	if result["isError"] != false {
		t.Fatalf("unexpected error result: %v", result)
	}
	text := result["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, `{\"a\":1}`) && !strings.Contains(text, `{"a":1}`) {
		t.Fatalf("the arguments did not reach the tool: %s", text)
	}
}

func TestAFailingToolIsAToolErrorNotAProtocolError(t *testing.T) {
	replies := exchange(t, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"broken"}}`)
	if replies[0]["error"] != nil {
		t.Fatalf("a tool failure must not be a JSON-RPC error: %v", replies[0])
	}
	result := replies[0]["result"].(map[string]any)
	if result["isError"] != true || !strings.Contains(result["content"].([]any)[0].(map[string]any)["text"].(string), "host unreachable") {
		t.Fatalf("the model must see why the tool failed: %v", result)
	}
}

func TestProtocolErrors(t *testing.T) {
	cases := []struct {
		name string
		line string
		code float64
	}{
		{"unknown method", `{"jsonrpc":"2.0","id":1,"method":"resources/list"}`, codeMethodNotFound},
		{"unknown tool", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"rm_rf"}}`, codeInvalidParams},
		{"no tool name", `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}`, codeInvalidParams},
		{"bad version field", `{"jsonrpc":"1.0","id":1,"method":"ping"}`, codeInvalidRequest},
		{"not json", `{nope`, codeParse},
	}
	for _, c := range cases {
		replies := exchange(t, c.line)
		if len(replies) != 1 || replies[0]["error"] == nil || replies[0]["error"].(map[string]any)["code"].(float64) != c.code {
			t.Errorf("%s: expected error %v, got %v", c.name, c.code, replies)
		}
	}
}

func TestStdoutCarriesOneMessagePerLine(t *testing.T) {
	var out bytes.Buffer
	_ = testServer().Serve(context.Background(), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n"+`{"jsonrpc":"2.0","id":2,"method":"ping"}`+"\n"), &out)
	if lines := strings.Split(strings.TrimSpace(out.String()), "\n"); len(lines) != 2 {
		t.Fatalf("replies must be newline-delimited: %q", out.String())
	}
}

func TestAMessageWithoutATrailingNewlineIsStillHandled(t *testing.T) {
	var out bytes.Buffer
	_ = testServer().Serve(context.Background(), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`), &out)
	if !strings.Contains(out.String(), `"id":1`) {
		t.Fatalf("the last message before EOF was dropped: %q", out.String())
	}
}
