package rpc

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"
)

var testTools = []Tool{
	{Name: "echo", Title: "Echo", Description: "returns the arguments", InputSchema: json.RawMessage(`{"type":"object"}`), ReadOnly: true, Idempotent: true},
	{Name: "fail", Description: "fails", Destructive: true},
	{Name: "list", Description: "returns an array", ReadOnly: true},
	{Name: "who", ReadOnly: true},
	{Name: "wait"},
}

func startMCP(t *testing.T) (*lineClient, *testCalls, chan error) {
	h, tc := newTestHandler()
	m := NewMCP(Options{Name: "althex", Version: "1.0"}, testTools, h, "use the tools")
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- m.ServeStream(context.Background(), inR, outW); outW.Close() }()
	t.Cleanup(func() { inW.Close() })
	return newLineClient(t, outR, inW), tc, done
}

func result(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	r, ok := m["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %v", m)
	}
	return r
}

func TestMCPInitialize(t *testing.T) {
	c, _, done := startMCP(t)
	r := result(t, c.call(1, "initialize", `{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"claude","version":"9"}}`))
	if r["protocolVersion"] != "2025-03-26" || r["instructions"] != "use the tools" {
		t.Fatalf("initialize: %v", r)
	}
	if info := r["serverInfo"].(map[string]any); info["name"] != "althex" || info["version"] != "1.0" {
		t.Fatalf("serverInfo: %v", info)
	}
	if string(mustJSON(r["capabilities"])) != `{"tools":{"listChanged":false}}` {
		t.Fatalf("capabilities: %v", r["capabilities"])
	}
	c.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	c.none()

	r = result(t, c.call(2, "initialize", `{"protocolVersion":"1999-01-01","clientInfo":{"name":"claude"}}`))
	if r["protocolVersion"] != protocolVersions[0] {
		t.Fatalf("unknown version: %v", r["protocolVersion"])
	}
	if r := c.call(3, "ping", ""); string(mustJSON(r["result"])) != "{}" {
		t.Fatalf("ping: %v", r)
	}
	if r := c.call(4, "resources/list", ""); errCode(r) != MethodNotFound {
		t.Fatalf("unknown method: %v", r)
	}

	// The handler learns the client
	r = result(t, c.call(5, "tools/call", `{"name":"who"}`))
	who := r["structuredContent"].(map[string]any)
	if who["name"] != "claude" || who["transport"] != "stdio" {
		t.Fatalf("who: %v", who)
	}

	c.w.(io.Closer).Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestMCPTools(t *testing.T) {
	c, _, _ := startMCP(t)
	r := result(t, c.call(1, "tools/list", `{"cursor":"x"}`))
	tools := r["tools"].([]any)
	if len(tools) != len(testTools) {
		t.Fatalf("tools: %v", tools)
	}
	echo := tools[0].(map[string]any)
	if echo["name"] != "echo" || echo["title"] != "Echo" || echo["description"] != "returns the arguments" ||
		string(mustJSON(echo["inputSchema"])) != `{"type":"object"}` {
		t.Fatalf("echo: %v", echo)
	}
	want := `{"destructiveHint":false,"idempotentHint":true,"openWorldHint":false,"readOnlyHint":true,"title":"Echo"}`
	if a := string(mustJSON(echo["annotations"])); a != want {
		t.Fatalf("annotations: %s", a)
	}
	fail := tools[1].(map[string]any)
	if a := string(mustJSON(fail["annotations"])); a != `{"destructiveHint":true,"openWorldHint":false,"readOnlyHint":false}` {
		t.Fatalf("fail annotations: %s", a)
	}
	if s := string(mustJSON(fail["inputSchema"])); s != `{"type":"object"}` {
		t.Fatalf("default schema: %s", s)
	}

	// Success: the text and the structured result
	r = result(t, c.call(2, "tools/call", `{"name":"echo","arguments":{"offset":16}}`))
	content := r["content"].([]any)[0].(map[string]any)
	if content["type"] != "text" || content["text"] != "{\n  \"offset\": 16\n}" {
		t.Fatalf("content: %v", content)
	}
	if string(mustJSON(r["structuredContent"])) != `{"offset":16}` || r["isError"] != nil {
		t.Fatalf("structured: %v", r)
	}
	// Missing arguments are an empty object
	r = result(t, c.call(3, "tools/call", `{"name":"echo"}`))
	if string(mustJSON(r["structuredContent"])) != `{}` {
		t.Fatalf("no arguments: %v", r)
	}
	// Not an object: text only
	r = result(t, c.call(4, "tools/call", `{"name":"list"}`))
	if _, ok := r["structuredContent"]; ok {
		t.Fatalf("array structured: %v", r)
	}

	// A failing tool is a result with isError
	r = result(t, c.call(5, "tools/call", `{"name":"fail","arguments":{}}`))
	content = r["content"].([]any)[0].(map[string]any)
	if r["isError"] != true || content["text"] != "no such thing" {
		t.Fatalf("tool error: %v", r)
	}
	// An unknown tool is a protocol error
	if r := c.call(6, "tools/call", `{"name":"nosuch"}`); errCode(r) != InvalidParams {
		t.Fatalf("unknown tool: %v", r)
	}
	if r := c.call(7, "tools/call", `[1]`); errCode(r) != InvalidParams {
		t.Fatalf("bad params: %v", r)
	}
}

func TestMCPCancel(t *testing.T) {
	c, tc, _ := startMCP(t)
	c.send(`{"jsonrpc":"2.0","id":"w","method":"tools/call","params":{"name":"wait"}}`)
	select {
	case <-tc.started:
	case <-time.After(5 * time.Second):
		t.Fatal("not started")
	}
	c.send(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"w","reason":"user"}}`)
	select {
	case <-tc.canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("not canceled")
	}
	// The canceled call gets no response: the next one is the ping's
	c.send(`{"jsonrpc":"2.0","id":8,"method":"ping"}`)
	var m map[string]any
	json.Unmarshal(c.next(), &m)
	if m["id"] != 8.0 {
		t.Fatalf("after cancel: %v", m)
	}
}
