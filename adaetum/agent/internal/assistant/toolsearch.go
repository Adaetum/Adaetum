package assistant

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/Adaetum/Adaetum/agent/internal/mcp"
)

// TOOL SEARCH. Offering the model every tool on every request costs context in
// proportion to the number of tools, and that number will keep growing as more
// servers are connected. So when Config.CoreTools is set, a request carries only those
// tools plus search_tools; the model finds the rest by describing what it needs, and
// the tools that match are offered from then on in that conversation.
//
// It is done here, in the agent, rather than with a model provider's own tool-search
// feature, so it behaves the same for every provider, including the free
// OpenAI-compatible ones that have no such feature.
//
// Searching changes only what the model is TOLD about. Every tool still runs through
// the same execute path: a tool that changes something still stops for approval, and
// a tool the model calls without having found it does not run (see execute).

// SearchToolName is the one tool that is always offered when tools are deferred.
const SearchToolName = "search_tools"

const (
	defaultSearchLimit = 5
	maxSearchLimit     = 10
)

// deferring reports whether tools are being held back until searched for.
func (s *Service) deferring() bool { return len(s.core) > 0 }

// isCore reports whether a tool is always offered.
func (s *Service) isCore(name string) bool { return !s.deferring() || s.core[name] }

func searchTool() mcp.Tool {
	return mcp.Tool{
		Name: SearchToolName,
		Description: "Find tools you were not shown. Describe in a few words what you need to do " +
			"(for example \"grow a volume\" or \"approve a machine\"); the matching tools become " +
			"available to you immediately and you can call them by name. Read-only.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "What you need to do, in a few words."},
				"limit": map[string]any{"type": "integer", "description": fmt.Sprintf("How many tools to return (default %d, at most %d).", defaultSearchLimit, maxSearchLimit)},
			},
			"required": []string{"query"},
		},
	}
}

// offered returns the tools a request to the model carries, in a stable order.
func (s *Service) offered(c *conversation) []mcp.Tool {
	if !s.deferring() {
		return s.cfg.Tools
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	offered := make([]mcp.Tool, 0, len(s.core)+len(c.loaded)+1)
	for _, tool := range s.cfg.Tools {
		if s.core[tool.Name] || c.loaded[tool.Name] {
			offered = append(offered, tool)
		}
	}
	return append(offered, searchTool())
}

// system is the standing instructions, with a short note on how to find the tools
// that are not shown. The note lists groups, not tools, so it stays the same size as
// tools are added.
func (s *Service) system() string {
	if !s.deferring() {
		return s.cfg.System
	}
	counts := map[string]int{}
	for _, tool := range s.cfg.Tools {
		if !s.core[tool.Name] {
			group := tool.Group
			if group == "" {
				group = "other"
			}
			counts[group]++
		}
	}
	groups := make([]string, 0, len(counts))
	for group := range counts {
		groups = append(groups, group)
	}
	sort.Strings(groups)
	for i, group := range groups {
		groups[i] = fmt.Sprintf("%s (%d)", group, counts[group])
	}
	return s.cfg.System + "\n\nYou are shown only some of your tools. The rest are found with " + SearchToolName +
		": say in a few words what you need and the matching tools become available at once. Never guess a tool's name. " +
		"Tools you can search for, by group: " + strings.Join(groups, ", ") + "."
}

// load marks tools as offered for the rest of this conversation.
func (s *Service) load(c *conversation, names ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, name := range names {
		c.loaded[name] = true
	}
}

// runSearch answers a search_tools call: it loads the best matches and tells the model
// what they are. The definitions themselves arrive in the next request's tool list.
func (s *Service) runSearch(c *conversation, raw json.RawMessage) (string, bool) {
	var input struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &input); err != nil || strings.TrimSpace(input.Query) == "" {
		return "Say what you need in a few words, for example {\"query\": \"grow a volume\"}.", true
	}
	limit := input.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}
	found := s.search(input.Query, limit)
	if len(found) == 0 {
		return "No tool matches that. Try other words.", false
	}
	names := make([]string, len(found))
	var out strings.Builder
	out.WriteString("These tools are now available; call them by name:\n")
	for i, tool := range found {
		names[i] = tool.Name
		note := ""
		if tool.Mutates {
			note = " [changes something; the person is asked first]"
		}
		fmt.Fprintf(&out, "- %s: %s%s\n", tool.Name, firstSentence(tool.Description), note)
	}
	s.load(c, names...)
	return out.String(), false
}

// search ranks the tools that are not always offered against a query, best first.
// It is deliberately plain word matching (name, group and description), which needs
// nothing running and is predictable; a tool must share at least one word with the
// query to appear.
func (s *Service) search(query string, limit int) []mcp.Tool {
	words := tokens(query)
	type scored struct {
		tool  mcp.Tool
		score int
	}
	var ranked []scored
	for _, tool := range s.cfg.Tools {
		if s.core[tool.Name] {
			continue
		}
		name, group, description := tokenSet(tool.Name), tokenSet(tool.Group), tokenSet(tool.Description)
		score := 0
		for _, word := range words {
			switch {
			case name[word]:
				score += 4
			case prefixMatch(name, word):
				score += 2
			}
			if group[word] {
				score += 2
			}
			if description[word] {
				score++
			}
		}
		if score > 0 {
			ranked = append(ranked, scored{tool, score})
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].tool.Name < ranked[j].tool.Name
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	found := make([]mcp.Tool, len(ranked))
	for i, r := range ranked {
		found[i] = r.tool
	}
	return found
}

var stopWords = map[string]bool{
	"a": true, "an": true, "the": true, "to": true, "of": true, "on": true, "in": true, "for": true, "and": true,
	"or": true, "is": true, "it": true, "this": true, "that": true, "with": true, "my": true, "me": true, "i": true,
	"do": true, "how": true, "can": true, "you": true, "want": true, "need": true, "please": true,
}

// tokens splits text into lower-case words, with underscores treated as spaces (tool
// names are snake_case), common filler dropped and a plural 's' trimmed so "volumes"
// finds "volume".
func tokens(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	words := fields[:0]
	for _, field := range fields {
		if stopWords[field] {
			continue
		}
		if len(field) > 3 && strings.HasSuffix(field, "s") && !strings.HasSuffix(field, "ss") {
			field = field[:len(field)-1]
		}
		words = append(words, field)
	}
	return words
}

func tokenSet(text string) map[string]bool {
	set := map[string]bool{}
	for _, word := range tokens(text) {
		set[word] = true
	}
	return set
}

// prefixMatch lets "playbook" find a name word it starts, and the reverse for short
// stems, without matching on one or two letters.
func prefixMatch(set map[string]bool, word string) bool {
	if len(word) < 4 {
		return false
	}
	for candidate := range set {
		if len(candidate) >= 4 && (strings.HasPrefix(candidate, word) || strings.HasPrefix(word, candidate)) {
			return true
		}
	}
	return false
}

func firstSentence(text string) string {
	text = strings.TrimSpace(text)
	if i := strings.Index(text, ". "); i > 0 {
		text = text[:i+1]
	}
	if runes := []rune(text); len(runes) > 200 {
		text = string(runes[:200]) + "…"
	}
	return text
}
