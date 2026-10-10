package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"slices"
	"sync"
	"time"
)

// protocolVersions are the MCP revisions served, the latest first
var protocolVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// Tool is an operation offered to MCP clients
type Tool struct {
	Name, Title, Description string
	InputSchema              json.RawMessage // a JSON Schema object
	ReadOnly, Destructive    bool            // annotations readOnlyHint, destructiveHint
	Idempotent               bool            // annotation idempotentHint, sent only when true
}

// MCP is a Model Context Protocol server of tools: tools/call runs the
// Handler with the tool name as the method and the arguments as params
type MCP struct {
	opt          Options
	tools        []Tool
	byName       map[string]bool
	h            Handler
	instructions string

	mu       sync.Mutex
	sessions map[string]*session // of the HTTP transport, by Mcp-Session-Id
}

// session is one MCP client: the stdio stream, or an HTTP session
type session struct {
	proc      *processor
	transport string

	mu       sync.Mutex
	client   Client
	lastUsed time.Time
}

// NewMCP makes a server of the tools; instructions tell the model how to use them
func NewMCP(opt Options, tools []Tool, h Handler, instructions string) *MCP {
	m := &MCP{opt: opt, tools: tools, byName: map[string]bool{}, h: h,
		instructions: instructions, sessions: map[string]*session{}}
	for _, t := range tools {
		m.byName[t.Name] = true
	}
	return m
}

func (m *MCP) newSession(transport string) *session {
	s := &session{transport: transport, client: clientInfo{}.client(transport), lastUsed: time.Now()}
	s.proc = newProcessor(func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		return m.handle(ctx, s, method, params)
	})
	return s
}

// ServeStream serves MCP over the stdio transport: newline-delimited
// JSON-RPC messages on r and w. It returns when r ends, after the calls in
// flight are answered, or when ctx is done; then a goroutine may stay
// blocked reading r until it ends.
func (m *MCP) ServeStream(ctx context.Context, r io.Reader, w io.Writer) error {
	return serveStream(ctx, r, w, nil, m.newSession("stdio").proc)
}

// handle runs one MCP request or notification of a session
func (m *MCP) handle(ctx context.Context, s *session, method string, params json.RawMessage) (any, error) {
	switch method {
	case "initialize":
		var p struct {
			ProtocolVersion string     `json:"protocolVersion"`
			ClientInfo      clientInfo `json:"clientInfo"`
		}
		if err := unmarshalParams(params, &p); err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.client = p.ClientInfo.client(s.transport)
		s.mu.Unlock()
		v := protocolVersions[0]
		if slices.Contains(protocolVersions, p.ProtocolVersion) {
			v = p.ProtocolVersion
		}
		return map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": m.opt.Name, "version": m.opt.Version},
			"instructions":    m.instructions,
		}, nil
	case "notifications/initialized":
		return nil, nil
	case "notifications/cancelled":
		var p struct {
			RequestID json.RawMessage `json:"requestId"`
		}
		if json.Unmarshal(params, &p) == nil && len(p.RequestID) > 0 {
			s.proc.cancel(p.RequestID)
		}
		return nil, nil
	case "ping":
		return struct{}{}, nil
	case "tools/list":
		return map[string]any{"tools": m.listTools()}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := unmarshalParams(params, &p); err != nil {
			return nil, err
		}
		if !m.byName[p.Name] {
			return nil, Errorf(InvalidParams, "unknown tool: %s", p.Name)
		}
		if len(p.Arguments) == 0 || bytes.Equal(p.Arguments, nullID) {
			p.Arguments = json.RawMessage("{}")
		}
		s.mu.Lock()
		c := s.client
		s.mu.Unlock()
		return m.callTool(WithClient(ctx, c), p.Name, p.Arguments), nil
	}
	return nil, Errorf(MethodNotFound, "method not found: %s", method)
}

// unmarshalParams reads the params of a request; absent params are empty
func unmarshalParams(params json.RawMessage, v any) error {
	if len(params) == 0 {
		return nil
	}
	if err := json.Unmarshal(params, v); err != nil {
		return Errorf(InvalidParams, "invalid params: %v", err)
	}
	return nil
}

func (m *MCP) listTools() []any {
	type annotations struct {
		Title           string `json:"title,omitempty"`
		ReadOnlyHint    bool   `json:"readOnlyHint"`
		DestructiveHint bool   `json:"destructiveHint"`
		IdempotentHint  bool   `json:"idempotentHint,omitempty"`
		OpenWorldHint   bool   `json:"openWorldHint"`
	}
	type tool struct {
		Name        string          `json:"name"`
		Title       string          `json:"title,omitempty"`
		Description string          `json:"description,omitempty"`
		InputSchema json.RawMessage `json:"inputSchema"`
		Annotations annotations     `json:"annotations"`
	}
	list := make([]any, 0, len(m.tools))
	for _, t := range m.tools {
		schema := t.InputSchema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		list = append(list, tool{
			Name: t.Name, Title: t.Title, Description: t.Description, InputSchema: schema,
			Annotations: annotations{Title: t.Title, ReadOnlyHint: t.ReadOnly,
				DestructiveHint: t.Destructive, IdempotentHint: t.Idempotent},
		})
	}
	return list
}

// callTool runs a tool. Its failure is a result with isError, not a
// protocol error: the model reads it and can try otherwise.
func (m *MCP) callTool(ctx context.Context, name string, args json.RawMessage) any {
	type content struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type result struct {
		Content           []content       `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
		IsError           bool            `json:"isError,omitempty"`
	}
	fail := func(msg string) any {
		return result{Content: []content{{Type: "text", Text: msg}}, IsError: true}
	}
	v, err := m.h.Call(ctx, name, args)
	if err != nil {
		return fail(err.Error())
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return fail("cannot encode the result: " + err.Error())
	}
	var text bytes.Buffer
	json.Indent(&text, raw, "", "  ")
	r := result{Content: []content{{Type: "text", Text: text.String()}}}
	if len(raw) > 0 && raw[0] == '{' {
		r.StructuredContent = raw
	}
	return r
}
