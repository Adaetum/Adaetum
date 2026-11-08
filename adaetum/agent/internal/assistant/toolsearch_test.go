package assistant

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Adaetum/Adaetum/agent/internal/mcp"
	"github.com/anthropics/anthropic-sdk-go"
)

func nothing() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

// deferredService offers one core tool and holds the others back.
func deferredService(t *testing.T, sc *script, log *toolLog) *Service {
	t.Helper()
	call := func(name string) func(context.Context, json.RawMessage) (any, error) {
		return func(_ context.Context, raw json.RawMessage) (any, error) {
			log.add(name + " " + string(raw))
			return map[string]string{"ok": name}, nil
		}
	}
	tools := []mcp.Tool{
		{Name: "get_cluster", Group: "cluster", Description: "Which servers run the agent.", InputSchema: nothing(), Call: call("get_cluster")},
		{Name: "approve_host", Group: "servers", Description: "Approve a machine that asked to join. Approve only a machine the person recognises.", Mutates: true,
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"hostId": map[string]any{"type": "string"}}, "required": []string{"hostId"}}, Call: call("approve_host")},
		{Name: "grow_volume", Group: "storage", Description: "Make a storage volume bigger in place.", Mutates: true, InputSchema: nothing(), Call: call("grow_volume")},
		{Name: "list_volumes", Group: "storage", Description: "Every storage volume, its size and how full it is.", InputSchema: nothing(), Call: call("list_volumes")},
	}
	s := New(Config{Tools: tools, CoreTools: []string{"get_cluster"}, ApprovalTimeout: 2 * time.Second, MaxIterations: 5})
	s.newMessage = sc.next
	return s
}

func offeredNames(request anthropic.MessageNewParams) []string {
	var names []string
	for _, tool := range request.Tools {
		names = append(names, tool.OfTool.Name)
	}
	return names
}

func TestOnlyCoreToolsAndSearchAreOffered(t *testing.T) {
	sc := &script{replies: []*anthropic.Message{reply(t, "end_turn", textBlock("hi"))}}
	s := deferredService(t, sc, &toolLog{})
	id := s.NewConversation()
	s.Send(id, "hello")
	s.Wait()

	if got := strings.Join(offeredNames(sc.requests[0]), ","); got != "get_cluster,search_tools" {
		t.Fatalf("a request must carry only the core tools and the search tool: %s", got)
	}
	system := sc.requests[0].System[0].Text
	if !strings.Contains(system, "servers (1)") || !strings.Contains(system, "storage (2)") {
		t.Fatalf("the instructions must say what groups can be searched for: %q", system)
	}
	if strings.Contains(system, "grow_volume") {
		t.Fatalf("the instructions must name groups, not every tool: %q", system)
	}
}

func TestSearchingMakesAToolAvailableButItStillAsksFirst(t *testing.T) {
	log := &toolLog{}
	sc := &script{replies: []*anthropic.Message{
		reply(t, "tool_use", toolBlock("toolu_s", "search_tools", `{"query":"approve a machine that wants to join"}`)),
		reply(t, "tool_use", toolBlock("toolu_a", "approve_host", `{"hostId":"abc"}`)),
		reply(t, "end_turn", textBlock("Approved.")),
	}}
	s := deferredService(t, sc, log)
	id := s.NewConversation()
	s.Send(id, "let abc join")

	view := waitFor(t, s, id, StateWaitingApproval)
	if call := view.Items[len(view.Items)-1].Tool; call.Name != "approve_host" || call.Status != ToolPending || len(log.all()) != 0 {
		t.Fatalf("a found tool that changes something must still wait for approval: %+v %v", call, log.all())
	}
	if err := s.Decide(id, "toolu_a", true); err != nil {
		t.Fatal(err)
	}
	s.Wait()

	if strings.Contains(strings.Join(offeredNames(sc.requests[0]), ","), "approve_host") {
		t.Fatal("it must not be offered before it is searched for")
	}
	if !strings.Contains(strings.Join(offeredNames(sc.requests[1]), ","), "approve_host") {
		t.Fatalf("it must be offered once found: %v", offeredNames(sc.requests[1]))
	}
	result := sc.requests[1].Messages[2].Content[0].OfToolResult
	if result == nil || !strings.Contains(result.Content[0].OfText.Text, "approve_host") || !strings.Contains(result.Content[0].OfText.Text, "the person is asked first") {
		t.Fatalf("the search result must name the tool and say it asks first: %+v", result)
	}
	if got := log.all(); len(got) != 1 || !strings.HasPrefix(got[0], "approve_host") {
		t.Fatalf("it must run once, after approval: %v", got)
	}
}

func TestAToolTheModelNeverFoundDoesNotRun(t *testing.T) {
	log := &toolLog{}
	sc := &script{replies: []*anthropic.Message{
		reply(t, "tool_use", toolBlock("toolu_g", "list_volumes", `{}`)),
		reply(t, "tool_use", toolBlock("toolu_h", "list_volumes", `{}`)),
		reply(t, "end_turn", textBlock("Done.")),
	}}
	s := deferredService(t, sc, log)
	id := s.NewConversation()
	s.Send(id, "how full are the volumes?")
	s.Wait()

	got := log.all()
	if len(got) != 1 {
		t.Fatalf("the first call must not run (it was never described to the model) and the second must: %v", got)
	}
	first := sc.requests[1].Messages[2].Content[0].OfToolResult
	if first == nil || first.IsError.Value != true || !strings.Contains(first.Content[0].OfText.Text, "not available to you yet") {
		t.Fatalf("the model must be told to look at it and ask again: %+v", first)
	}
	if !strings.Contains(strings.Join(offeredNames(sc.requests[1]), ","), "list_volumes") {
		t.Fatal("it must be offered from then on")
	}
}

func TestWithoutCoreToolsEverythingIsOfferedAsBefore(t *testing.T) {
	sc := &script{replies: []*anthropic.Message{reply(t, "end_turn", textBlock("hi"))}}
	s := newService(t, sc, &toolLog{})
	id := s.NewConversation()
	s.Send(id, "hello")
	s.Wait()
	names := strings.Join(offeredNames(sc.requests[0]), ",")
	if names != "get_cluster,approve_host,broken" {
		t.Fatalf("with no core list every tool is offered and there is no search tool: %s", names)
	}
}

func TestSearchRanksByWhatTheQuerySays(t *testing.T) {
	s := deferredService(t, &script{}, &toolLog{})
	cases := map[string]string{
		"make the volume bigger":    "grow_volume",
		"how full are my volumes":   "list_volumes",
		"approve a machine to join": "approve_host",
		"grow":                      "grow_volume",
	}
	for query, want := range cases {
		found := s.search(query, 5)
		if len(found) == 0 || found[0].Name != want {
			t.Errorf("%q: want %s first, got %v", query, want, found)
		}
	}
	if found := s.search("storage", 5); len(found) != 2 {
		t.Errorf("a group name must find every tool in the group: %v", found)
	}
	if found := s.search("completely unrelated cooking", 5); len(found) != 0 {
		t.Errorf("a query sharing no word with any tool must find nothing: %v", found)
	}
	if found := s.search("cluster servers", 5); len(found) == 1 && found[0].Name == "get_cluster" {
		t.Error("a core tool is already offered and must not be searched for")
	}
}
