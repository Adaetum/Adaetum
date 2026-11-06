package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/Adaetum/Adaetum/agent/internal/mcp"
)

// reply builds a Message the way the API would send it.
func reply(t *testing.T, stop string, blocks ...string) *anthropic.Message {
	t.Helper()
	raw := fmt.Sprintf(`{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","content":[%s],"stop_reason":%q,"usage":{"input_tokens":1,"output_tokens":1}}`,
		strings.Join(blocks, ","), stop)
	var message anthropic.Message
	if err := json.Unmarshal([]byte(raw), &message); err != nil {
		t.Fatal(err)
	}
	return &message
}

func textBlock(text string) string { return fmt.Sprintf(`{"type":"text","text":%q}`, text) }
func toolBlock(id, name, input string) string {
	return fmt.Sprintf(`{"type":"tool_use","id":%q,"name":%q,"input":%s}`, id, name, input)
}

type script struct {
	mu       sync.Mutex
	replies  []*anthropic.Message
	requests []anthropic.MessageNewParams
	err      error
}

func (s *script) next(_ context.Context, params anthropic.MessageNewParams) (*anthropic.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, params)
	if s.err != nil {
		return nil, s.err
	}
	if len(s.replies) == 0 {
		return nil, errors.New("script exhausted")
	}
	r := s.replies[0]
	s.replies = s.replies[1:]
	return r, nil
}

type toolLog struct {
	mu    sync.Mutex
	calls []string
}

func (l *toolLog) add(s string) { l.mu.Lock(); l.calls = append(l.calls, s); l.mu.Unlock() }
func (l *toolLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.calls...)
}

func newService(t *testing.T, sc *script, log *toolLog) *Service {
	t.Helper()
	tools := []mcp.Tool{
		{Name: "get_cluster", Description: "read the cluster", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			Call: func(context.Context, json.RawMessage) (any, error) {
				log.add("get_cluster")
				return map[string]any{"servers": 2}, nil
			}},
		{Name: "approve_host", Description: "approve a machine", Mutates: true,
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"hostId": map[string]any{"type": "string"}}, "required": []string{"hostId"}},
			Call: func(_ context.Context, raw json.RawMessage) (any, error) {
				log.add("approve_host " + string(raw))
				return map[string]string{"status": "approved"}, nil
			}},
		{Name: "broken", Description: "always fails", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			Call: func(context.Context, json.RawMessage) (any, error) { return nil, errors.New("host unreachable") }},
	}
	s := New(Config{Tools: tools, ApprovalTimeout: 2 * time.Second, MaxIterations: 5})
	s.newMessage = sc.next
	return s
}

func waitFor(t *testing.T, s *Service, id string, state string) View {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		v, _ := s.View(id, 0)
		if v.State == state {
			return v
		}
		time.Sleep(10 * time.Millisecond)
	}
	v, _ := s.View(id, 0)
	t.Fatalf("never reached %s, stuck at %s: %+v", state, v.State, v.Items)
	return v
}

func TestAReadingToolRunsWithoutAskingAndTheAnswerFollows(t *testing.T) {
	log := &toolLog{}
	sc := &script{replies: []*anthropic.Message{
		reply(t, "tool_use", textBlock("Let me check."), toolBlock("toolu_1", "get_cluster", `{}`)),
		reply(t, "end_turn", textBlock("Two servers, both fine.")),
	}}
	s := newService(t, sc, log)
	id := s.NewConversation()
	if err := s.Send(id, "how is the cluster?"); err != nil {
		t.Fatal(err)
	}
	s.Wait()

	view, _ := s.View(id, 0)
	if view.State != StateIdle || len(log.all()) != 1 {
		t.Fatalf("%+v %v", view, log.all())
	}
	kinds := []string{}
	for _, item := range view.Items {
		kinds = append(kinds, item.Kind)
	}
	if strings.Join(kinds, ",") != "user,assistant,tool,assistant" || view.Items[2].Tool.Status != ToolDone {
		t.Fatalf("unexpected transcript: %v %+v", kinds, view.Items[2].Tool)
	}
	// The second request must carry the tool result back, in one user message.
	last := sc.requests[1].Messages
	if len(last) != 3 || last[2].Role != anthropic.MessageParamRoleUser || len(last[2].Content) != 1 || last[2].Content[0].OfToolResult == nil {
		t.Fatalf("the tool result must go back as one user message: %+v", last)
	}
}

func TestAChangeWaitsForApprovalAndRunsOnlyAfterIt(t *testing.T) {
	log := &toolLog{}
	sc := &script{replies: []*anthropic.Message{
		reply(t, "tool_use", toolBlock("toolu_9", "approve_host", `{"hostId":"abc"}`)),
		reply(t, "end_turn", textBlock("Approved.")),
	}}
	s := newService(t, sc, log)
	id := s.NewConversation()
	s.Send(id, "approve abc")

	view := waitFor(t, s, id, StateWaitingApproval)
	call := view.Items[len(view.Items)-1].Tool
	if call.Status != ToolPending || !call.Mutates || len(log.all()) != 0 {
		t.Fatalf("a change must wait and not have run: %+v %v", call, log.all())
	}
	if err := s.Decide(id, "toolu_9", true); err != nil {
		t.Fatal(err)
	}
	s.Wait()
	if got := log.all(); len(got) != 1 || !strings.Contains(got[0], `"hostId":"abc"`) {
		t.Fatalf("it must run exactly once, with its input, after approval: %v", got)
	}
	done, _ := s.View(id, 0)
	if done.Items[1].Tool.Status != ToolDone {
		t.Fatalf("%+v", done.Items[1].Tool)
	}
}

func TestADeniedChangeNeverRunsAndTheModelIsToldNotToRetry(t *testing.T) {
	log := &toolLog{}
	sc := &script{replies: []*anthropic.Message{
		reply(t, "tool_use", toolBlock("toolu_9", "approve_host", `{"hostId":"abc"}`)),
		reply(t, "end_turn", textBlock("Understood, I won't.")),
	}}
	s := newService(t, sc, log)
	id := s.NewConversation()
	s.Send(id, "approve abc")
	waitFor(t, s, id, StateWaitingApproval)
	s.Decide(id, "toolu_9", false)
	s.Wait()

	if len(log.all()) != 0 {
		t.Fatal("a denied change must not run")
	}
	result := sc.requests[1].Messages[2].Content[0].OfToolResult
	if result == nil || !result.IsError.Value || !strings.Contains(fmt.Sprint(result.Content[0].OfText.Text), "did not approve") {
		t.Fatalf("the model must be told it was refused: %+v", result)
	}
	view, _ := s.View(id, 0)
	if view.Items[1].Tool.Status != ToolDenied {
		t.Fatalf("%+v", view.Items[1].Tool)
	}
}

func TestAChangeNobodyAnswersIsDeniedAfterTheTimeout(t *testing.T) {
	log := &toolLog{}
	sc := &script{replies: []*anthropic.Message{
		reply(t, "tool_use", toolBlock("toolu_9", "approve_host", `{"hostId":"abc"}`)),
		reply(t, "end_turn", textBlock("ok")),
	}}
	s := newService(t, sc, log)
	s.cfg.ApprovalTimeout = 100 * time.Millisecond
	id := s.NewConversation()
	s.Send(id, "approve abc")
	s.Wait()
	if len(log.all()) != 0 {
		t.Fatal("silence is not consent")
	}
}

func TestOnlyTheRightPendingCallCanBeDecided(t *testing.T) {
	s := newService(t, &script{}, &toolLog{})
	id := s.NewConversation()
	if err := s.Decide(id, "nope", true); !errors.Is(err, ErrNothingToDecide) {
		t.Fatalf("%v", err)
	}
	if err := s.Decide("missing", "x", true); !errors.Is(err, ErrNoConversation) {
		t.Fatalf("%v", err)
	}
}

func TestAFailingToolIsReportedToTheModelAsAnError(t *testing.T) {
	sc := &script{replies: []*anthropic.Message{
		reply(t, "tool_use", toolBlock("toolu_2", "broken", `{}`)),
		reply(t, "end_turn", textBlock("That failed: host unreachable.")),
	}}
	s := newService(t, sc, &toolLog{})
	id := s.NewConversation()
	s.Send(id, "do the broken thing")
	s.Wait()
	result := sc.requests[1].Messages[2].Content[0].OfToolResult
	if result == nil || !result.IsError.Value || !strings.Contains(result.Content[0].OfText.Text, "host unreachable") {
		t.Fatalf("%+v", result)
	}
}

func TestAnUnknownToolIsAnErrorNotACrash(t *testing.T) {
	sc := &script{replies: []*anthropic.Message{
		reply(t, "tool_use", toolBlock("toolu_3", "rm_everything", `{}`)),
		reply(t, "end_turn", textBlock("sorry")),
	}}
	s := newService(t, sc, &toolLog{})
	id := s.NewConversation()
	s.Send(id, "x")
	s.Wait()
	view, _ := s.View(id, 0)
	if view.Items[1].Tool.Status != ToolError {
		t.Fatalf("%+v", view.Items[1])
	}
}

func TestATurnIsBoundedAndSaysWhenItStops(t *testing.T) {
	var replies []*anthropic.Message
	for i := 0; i < 10; i++ {
		replies = append(replies, reply(t, "tool_use", toolBlock(fmt.Sprintf("toolu_%d", i), "get_cluster", `{}`)))
	}
	s := newService(t, &script{replies: replies}, &toolLog{})
	id := s.NewConversation()
	s.Send(id, "loop forever")
	s.Wait()
	view, _ := s.View(id, 0)
	last := view.Items[len(view.Items)-1]
	if last.Kind != KindNotice || !strings.Contains(last.Text, "Stopped after 5 steps") {
		t.Fatalf("%+v", last)
	}
}

func TestAnUnreachableAIServiceIsShownAndTheConversationRecovers(t *testing.T) {
	sc := &script{err: errors.New("connection refused")}
	s := newService(t, sc, &toolLog{})
	id := s.NewConversation()
	s.Send(id, "hello")
	s.Wait()
	view, _ := s.View(id, 0)
	if view.State != StateIdle || view.Items[len(view.Items)-1].Kind != KindError {
		t.Fatalf("%+v", view)
	}
	sc.mu.Lock()
	sc.err = nil
	sc.replies = []*anthropic.Message{reply(t, "end_turn", textBlock("back"))}
	sc.mu.Unlock()
	if err := s.Send(id, "again"); err != nil {
		t.Fatalf("a failed turn must not wedge the conversation: %v", err)
	}
	s.Wait()
}

func TestARefusalIsShownPlainly(t *testing.T) {
	s := newService(t, &script{replies: []*anthropic.Message{reply(t, "refusal")}}, &toolLog{})
	id := s.NewConversation()
	s.Send(id, "x")
	s.Wait()
	view, _ := s.View(id, 0)
	if view.Items[len(view.Items)-1].Kind != KindNotice {
		t.Fatalf("%+v", view.Items)
	}
}

func TestOnlyOneMessageAtATimeAndInputIsBounded(t *testing.T) {
	sc := &script{replies: []*anthropic.Message{reply(t, "tool_use", toolBlock("toolu_9", "approve_host", `{"hostId":"a"}`)), reply(t, "end_turn", textBlock("ok"))}}
	s := newService(t, sc, &toolLog{})
	id := s.NewConversation()
	s.Send(id, "go")
	waitFor(t, s, id, StateWaitingApproval)
	if err := s.Send(id, "another"); !errors.Is(err, ErrBusy) {
		t.Fatalf("%v", err)
	}
	s.Decide(id, "toolu_9", false)
	s.Wait()
	if err := s.Send(id, ""); err == nil {
		t.Error("empty")
	}
	if err := s.Send(id, strings.Repeat("x", maxMessageLength+1)); err == nil {
		t.Error("too long")
	}
}

func TestCancellingStopsAWaitingTurnAndDeniesThePendingChange(t *testing.T) {
	log := &toolLog{}
	sc := &script{replies: []*anthropic.Message{reply(t, "tool_use", toolBlock("toolu_9", "approve_host", `{"hostId":"a"}`))}}
	s := newService(t, sc, log)
	id := s.NewConversation()
	s.Send(id, "go")
	waitFor(t, s, id, StateWaitingApproval)
	s.Cancel(id)
	s.Wait()
	if len(log.all()) != 0 {
		t.Fatal("cancelling must not let the change through")
	}
}

func TestViewIsIncremental(t *testing.T) {
	sc := &script{replies: []*anthropic.Message{reply(t, "end_turn", textBlock("hi"))}}
	s := newService(t, sc, &toolLog{})
	id := s.NewConversation()
	s.Send(id, "hello")
	s.Wait()
	first, _ := s.View(id, 0)
	again, _ := s.View(id, first.Next)
	if len(first.Items) != 2 || len(again.Items) != 0 || again.Next != first.Next {
		t.Fatalf("%+v %+v", first, again)
	}
}

// The wire shapes: this one goes through the real SDK to a fake API server, so the
// request the SDK builds (tools, system prompt, history) and the response it parses
// are exactly what the real service would see.
func TestTheRealSDKSendsToolsAndHistoryAndParsesToolUse(t *testing.T) {
	var mu sync.Mutex
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var body map[string]any
		json.Unmarshal(data, &body)
		mu.Lock()
		bodies = append(bodies, body)
		n := len(bodies)
		mu.Unlock()
		if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "test-key" {
			http.Error(w, "bad request", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			fmt.Fprint(w, `{"id":"msg_1","type":"message","role":"assistant","model":"claude-opus-5-5","content":[{"type":"tool_use","id":"toolu_1","name":"get_cluster","input":{}}],"stop_reason":"tool_use","usage":{"input_tokens":5,"output_tokens":5}}`)
			return
		}
		fmt.Fprint(w, `{"id":"msg_2","type":"message","role":"assistant","model":"claude-opus-5-5","content":[{"type":"text","text":"All good."}],"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":5}}`)
	}))
	defer server.Close()

	log := &toolLog{}
	s := newService(t, nil, log)
	s.cfg.Client = anthropic.NewClient(option.WithBaseURL(server.URL), option.WithAPIKey("test-key"), option.WithMaxRetries(0))
	s.newMessage = func(ctx context.Context, params anthropic.MessageNewParams) (*anthropic.Message, error) {
		return s.cfg.Client.Messages.New(ctx, params)
	}
	id := s.NewConversation()
	s.Send(id, "status?")
	s.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		view, _ := s.View(id, 0)
		t.Fatalf("expected two requests: %d %+v", len(bodies), view.Items)
	}
	first := bodies[0]
	if first["model"] != DefaultModel || first["max_tokens"].(float64) != 16000 {
		t.Fatalf("model and limit: %v", first)
	}
	tools := first["tools"].([]any)
	if len(tools) != 3 {
		t.Fatalf("every tool must be offered: %v", tools)
	}
	byName := map[string]map[string]any{}
	for _, raw := range tools {
		tool := raw.(map[string]any)
		byName[tool["name"].(string)] = tool
	}
	schema := byName["approve_host"]["input_schema"].(map[string]any)
	if schema["type"] != "object" || schema["required"].([]any)[0] != "hostId" || byName["approve_host"]["description"] == "" {
		t.Fatalf("the schema must carry the required fields: %v", byName["approve_host"])
	}
	if !strings.Contains(first["system"].([]any)[0].(map[string]any)["text"].(string), "Adaetum") {
		t.Fatalf("the system prompt must be sent: %v", first["system"])
	}
	second := bodies[1]["messages"].([]any)
	if len(second) != 3 || second[1].(map[string]any)["role"] != "assistant" || second[2].(map[string]any)["role"] != "user" {
		t.Fatalf("history must be user, assistant(tool_use), user(tool_result): %v", second)
	}
	result := second[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	if result["type"] != "tool_result" || result["tool_use_id"] != "toolu_1" {
		t.Fatalf("the result must reference the tool use: %v", result)
	}
	if len(log.all()) != 1 {
		t.Fatalf("the tool ran once: %v", log.all())
	}
}

func TestTheAPIGatesEverythingExceptStatusBehindALogin(t *testing.T) {
	s := newService(t, &script{replies: []*anthropic.Message{reply(t, "end_turn", textBlock("hi"))}}, &toolLog{})
	api := &API{Service: s, Status: Status{Enabled: true, Model: DefaultModel}, RequireUser: func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer ok" {
				http.Error(w, "login", 401)
				return
			}
			next.ServeHTTP(w, r)
		})
	}}
	mux := http.NewServeMux()
	api.Register(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	do := func(method, path, body, token string) (int, map[string]any) {
		request, _ := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var out map[string]any
		json.NewDecoder(response.Body).Decode(&out)
		return response.StatusCode, out
	}
	if code, body := do("GET", "/api/v1/assistant/status", "", ""); code != 200 || body["enabled"] != true {
		t.Fatalf("status is public: %d %v", code, body)
	}
	for _, path := range []string{"/api/v1/assistant/conversations"} {
		if code, _ := do("POST", path, "", ""); code != 401 {
			t.Errorf("%s needs a login: %d", path, code)
		}
	}
	_, created := do("POST", "/api/v1/assistant/conversations", "", "ok")
	id := created["id"].(string)
	if code, _ := do("POST", "/api/v1/assistant/conversations/"+id+"/messages", `{"text":"hello"}`, "ok"); code != 202 {
		t.Fatalf("send: %d", code)
	}
	s.Wait()
	if code, view := do("GET", "/api/v1/assistant/conversations/"+id, "", "ok"); code != 200 || len(view["items"].([]any)) != 2 {
		t.Fatalf("%d %v", code, view)
	}
	if code, _ := do("POST", "/api/v1/assistant/conversations/"+id+"/approvals/zzz", `{"approve":true}`, "ok"); code != 404 {
		t.Fatalf("approving nothing: %d", code)
	}
}

func TestADisabledAssistantRefusesEverythingAndSaysWhy(t *testing.T) {
	api := &API{Status: Status{Reason: OffReason}}
	mux := http.NewServeMux()
	api.Register(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	response, _ := http.Post(server.URL+"/api/v1/assistant/conversations", "application/json", strings.NewReader("{}"))
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != 403 || !strings.Contains(string(body), "-enable-assistant") {
		t.Fatalf("%d %s", response.StatusCode, body)
	}
}

func TestThePageTheyAreLookingAtGoesToTheModelButNotIntoTheTranscript(t *testing.T) {
	sc := &script{replies: []*anthropic.Message{reply(t, "end_turn", textBlock("That server is fine."))}}
	s := newService(t, sc, &toolLog{})
	id := s.NewConversation()
	if err := s.SendInContext(id, "is this ok?", "Page: Overview\n1 problem: node-56843 is not ready"); err != nil {
		t.Fatal(err)
	}
	s.Wait()

	view, _ := s.View(id, 0)
	if view.Items[0].Text != "is this ok?" {
		t.Fatalf("the transcript must show only what the person typed: %q", view.Items[0].Text)
	}
	sent := sc.requests[0].Messages[0].Content[0].OfText.Text
	if !strings.HasPrefix(sent, "<page_context>\nPage: Overview") || !strings.HasSuffix(sent, "\n\nis this ok?") {
		t.Fatalf("the model must see the page, then their words: %q", sent)
	}
}

func TestAPageThatIsTooLongIsCutNotRefused(t *testing.T) {
	sc := &script{replies: []*anthropic.Message{reply(t, "end_turn", textBlock("ok"))}}
	s := newService(t, sc, &toolLog{})
	id := s.NewConversation()
	if err := s.SendInContext(id, "hi", strings.Repeat("x", maxPageContext*2)); err != nil {
		t.Fatalf("a long page must still be answerable: %v", err)
	}
	s.Wait()
	sent := sc.requests[0].Messages[0].Content[0].OfText.Text
	if len(sent) > maxPageContext+200 || !strings.Contains(sent, "[cut: the page is longer]") {
		t.Fatalf("the page must be cut and say so (%d bytes)", len(sent))
	}
}
