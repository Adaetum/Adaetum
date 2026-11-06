// Package mcp serves the agent's read-only capabilities over the Model Context
// Protocol, so AI assistants and other MCP clients can ask what the host and
// cluster look like.
//
// It is a thin transport. Every tool wraps a function the HTTP API already uses,
// so there is one implementation of each capability and the web UI, the API, and
// MCP cannot disagree. Tools are read-only by default. The exceptions are the
// explicit security-switch tools, which are marked Mutates so the client asks the
// person before each call, are held to stricter rules than the web page (a stated
// reason and a time limit), and are recorded as made through MCP. Nothing in this
// package runs a command or reaches a server: it only calls the cluster agent's API.
//
// INTENT: MCP is a standard front of the agent, equal to the HTTP API. Both the
// host agent and, later, the cluster agent expose it, so an assistant can ask any
// host, or the whole cluster, what is wrong. When the agent has network
// authentication of its own, an HTTP transport joins stdio; until then stdio over
// SSH (see the README) is the way to reach a remote host.
//
// Transport is stdio: one JSON-RPC message per line on stdin, replies on stdout.
// Nothing but protocol may be written to stdout; diagnostics go to stderr.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
)

// protocolVersion is the revision of the MCP specification this server speaks.
// If a client asks for a different one the server answers with this one and the
// client decides whether it can continue, as the specification describes.
const protocolVersion = "2025-06-18"

// JSON-RPC error codes.
const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

// Tool is one read-only capability.
type Tool struct {
	Name        string
	Description string
	// InputSchema is a JSON Schema object describing the arguments.
	InputSchema map[string]any
	// Group names what the tool is about (for example "playbooks" or an MCP server's
	// name). The assistant lists groups, not tools, so it can say what can be searched
	// for without describing every tool. Optional.
	Group string
	// Mutates marks a tool that changes something. It is advertised to the client as
	// not read-only and potentially destructive, so a well-behaved client asks the
	// person to approve each call: that human approval, not anything in the model's
	// own output, is the real gate. Everything else here only reads.
	Mutates bool
	// Call returns a value that is shown to the client as JSON text. A returned
	// error is reported to the model as a failed tool result, not a protocol error.
	Call func(ctx context.Context, arguments json.RawMessage) (any, error)
}

// Server answers MCP requests for a fixed set of tools.
type Server struct {
	Name    string
	Version string
	tools   map[string]Tool
}

// NewServer returns a server for the given tools.
func NewServer(name, version string, tools []Tool) *Server {
	byName := make(map[string]Tool, len(tools))
	for _, tool := range tools {
		byName[tool.Name] = tool
	}
	return &Server{Name: name, Version: version, tools: byName}
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Serve reads requests until the input closes or ctx ends. A client closing
// stdin is the normal way to stop, so it is not an error.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	reader := bufio.NewReader(in)
	encoder := json.NewEncoder(out)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if reply := s.handle(ctx, line); reply != nil {
				if encodeErr := encoder.Encode(reply); encodeErr != nil {
					return encodeErr
				}
			}
		}
		if errors.Is(err, io.EOF) || ctx.Err() != nil {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// handle returns the reply for one message, or nil when none is due.
func (s *Server) handle(ctx context.Context, line []byte) *response {
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		if len(trim(line)) == 0 {
			return nil
		}
		return failure(json.RawMessage("null"), codeParse, "invalid JSON")
	}
	// A message without an id is a notification: it never gets a reply, not even
	// an error.
	if len(req.ID) == 0 {
		return nil
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		return failure(req.ID, codeInvalidRequest, "not a JSON-RPC 2.0 request")
	}

	switch req.Method {
	case "initialize":
		return success(req.ID, map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": s.Name, "version": s.Version},
		})
	case "ping":
		return success(req.ID, map[string]any{})
	case "tools/list":
		return success(req.ID, map[string]any{"tools": s.list()})
	case "tools/call":
		return s.call(ctx, req)
	default:
		return failure(req.ID, codeMethodNotFound, "method not found: "+req.Method)
	}
}

func (s *Server) list() []map[string]any {
	names := make([]string, 0, len(s.tools))
	for name := range s.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	tools := make([]map[string]any, 0, len(names))
	for _, name := range names {
		tool := s.tools[name]
		tools = append(tools, map[string]any{
			"name":        tool.Name,
			"description": tool.Description,
			"inputSchema": tool.InputSchema,
			// Tools that only read say so, so clients may skip confirmation for them; tools
			// that change something say that too, so clients ask first.
			"annotations": map[string]any{"readOnlyHint": !tool.Mutates, "destructiveHint": tool.Mutates},
		})
	}
	return tools
}

func (s *Server) call(ctx context.Context, req request) *response {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil || params.Name == "" {
		return failure(req.ID, codeInvalidParams, "tools/call needs a tool name")
	}
	tool, ok := s.tools[params.Name]
	if !ok {
		return failure(req.ID, codeInvalidParams, "unknown tool: "+params.Name)
	}

	value, err := tool.Call(ctx, params.Arguments)
	if err != nil {
		return success(req.ID, toolResult(err.Error(), true))
	}
	text, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return success(req.ID, toolResult(fmt.Sprintf("cannot encode the result: %v", err), true))
	}
	return success(req.ID, toolResult(string(text), false))
}

func toolResult(text string, isError bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isError,
	}
}

func success(id json.RawMessage, result any) *response {
	return &response{JSONRPC: "2.0", ID: id, Result: result}
}

func failure(id json.RawMessage, code int, message string) *response {
	return &response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}}
}

func trim(line []byte) []byte {
	start, end := 0, len(line)
	for start < end && (line[start] == ' ' || line[start] == '\n' || line[start] == '\r' || line[start] == '\t') {
		start++
	}
	for end > start && (line[end-1] == ' ' || line[end-1] == '\n' || line[end-1] == '\r' || line[end-1] == '\t') {
		end--
	}
	return line[start:end]
}
