package assistant

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeChatService answers like an OpenAI-compatible endpoint: first a tool call, then
// text. It records the requests so the translation can be checked.
func fakeChatService(t *testing.T, status int, first, second string) (*httptest.Server, *[]map[string]any, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var bodies []map[string]any
	var auth []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(data, &body)
		mu.Lock()
		bodies = append(bodies, body)
		auth = append(auth, r.Header.Get("Authorization"))
		n := len(bodies)
		mu.Unlock()
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if n == 1 {
			fmt.Fprint(w, first)
		} else {
			fmt.Fprint(w, second)
		}
	}))
	t.Cleanup(server.Close)
	return server, &bodies, &auth
}

const toolCallReply = `{"choices":[{"finish_reason":"tool_calls","message":{"content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_cluster","arguments":"{}"}}]}}]}`
const textReply = `{"choices":[{"finish_reason":"stop","message":{"content":"All good."}}]}`

func TestAnOpenAICompatibleServiceRunsATurnThroughTheSameLoop(t *testing.T) {
	server, bodies, auth := fakeChatService(t, 200, toolCallReply, textReply)
	log := &toolLog{}
	s := newService(t, nil, log)
	adapter := &OpenAICompatible{BaseURL: server.URL + "/v1", APIKey: "free-key"}
	s.newMessage = adapter.NewMessage
	s.cfg.Model = "llama-test"

	id := s.NewConversation()
	if err := s.Send(id, "status?"); err != nil {
		t.Fatal(err)
	}
	s.Wait()

	view, _ := s.View(id, 0)
	last := view.Items[len(view.Items)-1]
	if last.Kind != KindAssistant || last.Text != "All good." {
		t.Fatalf("the model's text must reach the transcript: %+v", view.Items)
	}
	if len(log.all()) != 1 {
		t.Fatalf("the tool call must run once: %v", log.all())
	}

	if len(*bodies) != 2 || (*auth)[0] != "Bearer free-key" {
		t.Fatalf("two requests with the key: %d %v", len(*bodies), *auth)
	}
	first := (*bodies)[0]
	if first["model"] != "llama-test" {
		t.Fatalf("model: %v", first["model"])
	}
	messages := first["messages"].([]any)
	if messages[0].(map[string]any)["role"] != "system" || !strings.Contains(messages[0].(map[string]any)["content"].(string), "Adaetum") {
		t.Fatalf("the system prompt must lead: %v", messages[0])
	}
	tools := first["tools"].([]any)
	if len(tools) != 3 {
		t.Fatalf("every tool is offered: %v", tools)
	}
	fn := tools[0].(map[string]any)["function"].(map[string]any)
	if tools[0].(map[string]any)["type"] != "function" || fn["name"] == "" || fn["parameters"].(map[string]any)["type"] != "object" {
		t.Fatalf("tools must be function definitions with an object schema: %v", tools[0])
	}

	// Second request: system, user, assistant(tool_calls), tool(result).
	second := (*bodies)[1]["messages"].([]any)
	if len(second) != 4 {
		t.Fatalf("history shape: %v", second)
	}
	call := second[2].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
	if call["id"] != "call_1" || call["function"].(map[string]any)["name"] != "get_cluster" {
		t.Fatalf("the model's own tool call must be replayed: %v", call)
	}
	result := second[3].(map[string]any)
	if result["role"] != "tool" || result["tool_call_id"] != "call_1" || result["content"] == "" {
		t.Fatalf("the result must reference the call: %v", result)
	}
}

func TestAChangeFromAnOpenAICompatibleModelStillWaitsForApproval(t *testing.T) {
	call := `{"choices":[{"finish_reason":"tool_calls","message":{"tool_calls":[{"id":"call_9","type":"function","function":{"name":"approve_host","arguments":"{\"hostId\":\"a\"}"}}]}}]}`
	server, _, _ := fakeChatService(t, 200, call, textReply)
	log := &toolLog{}
	s := newService(t, nil, log)
	s.newMessage = (&OpenAICompatible{BaseURL: server.URL + "/v1"}).NewMessage

	id := s.NewConversation()
	s.Send(id, "approve it")
	view := waitFor(t, s, id, StateWaitingApproval)
	if len(log.all()) != 0 {
		t.Fatalf("a change must not run before approval: %v", log.all())
	}
	if view.Items[len(view.Items)-1].Tool.Status != ToolPending {
		t.Fatalf("the change must be shown as pending: %+v", view.Items)
	}
	if err := s.Decide(id, "call_9", false); err != nil {
		t.Fatal(err)
	}
	s.Wait()
	if len(log.all()) != 0 {
		t.Fatalf("a denied change must never run: %v", log.all())
	}
}

func TestMissingToolCallIDsAndBrokenArgumentsAreRepaired(t *testing.T) {
	call := `{"choices":[{"finish_reason":"tool_calls","message":{"tool_calls":[{"type":"function","function":{"name":"get_cluster","arguments":"{not json"}}]}}]}`
	server, _, _ := fakeChatService(t, 200, call, textReply)
	log := &toolLog{}
	s := newService(t, nil, log)
	s.newMessage = (&OpenAICompatible{BaseURL: server.URL + "/v1"}).NewMessage
	id := s.NewConversation()
	s.Send(id, "go")
	s.Wait()
	view, _ := s.View(id, 0)
	var ran bool
	for _, item := range view.Items {
		if item.Tool != nil {
			ran = item.Tool.ID != "" && string(item.Tool.Input) == "{}"
		}
	}
	if !ran {
		t.Fatalf("the call needs an id and an empty-object input: %+v", view.Items)
	}
}

func TestServiceErrorsAreReportedPlainlyWithoutTheKey(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		want   string
	}{
		{429, `{"error":{"message":"slow down"}}`, "rate limiting"},
		{401, `{"error":{"message":"bad key"}}`, "401"},
		{200, `{"error":{"message":"model not found"}}`, "model not found"},
		{200, `<html>`, "unreadable"},
	} {
		server, _, _ := fakeChatService(t, tc.status, tc.body, tc.body)
		s := newService(t, nil, &toolLog{})
		s.newMessage = (&OpenAICompatible{BaseURL: server.URL + "/v1", APIKey: "secret-key-value"}).NewMessage
		id := s.NewConversation()
		s.Send(id, "hi")
		s.Wait()
		view, _ := s.View(id, 0)
		last := view.Items[len(view.Items)-1]
		if last.Kind != KindError || !strings.Contains(last.Text, tc.want) || strings.Contains(last.Text, "secret-key-value") {
			t.Fatalf("%d %s: %+v", tc.status, tc.body, last)
		}
	}
}

func TestLengthAndFilterFinishReasonsMapToTheLoopsOwnNotices(t *testing.T) {
	for reason, want := range map[string]string{"length": "cut off", "content_filter": "declined"} {
		body := fmt.Sprintf(`{"choices":[{"finish_reason":%q,"message":{"content":"partial"}}]}`, reason)
		server, _, _ := fakeChatService(t, 200, body, body)
		s := newService(t, nil, &toolLog{})
		s.newMessage = (&OpenAICompatible{BaseURL: server.URL + "/v1"}).NewMessage
		id := s.NewConversation()
		s.Send(id, "hi")
		s.Wait()
		view, _ := s.View(id, 0)
		if last := view.Items[len(view.Items)-1]; last.Kind != KindNotice || !strings.Contains(last.Text, want) {
			t.Fatalf("%s: %+v", reason, view.Items)
		}
	}
}
