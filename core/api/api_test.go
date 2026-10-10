package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ipoluianov/althex/core/api"
	"github.com/ipoluianov/althex/core/tmpl"
	"github.com/ipoluianov/althex/rpc"
)

// a small BMP: the built-in template parses it
func bmp() []byte {
	b := make([]byte, 54+16)
	copy(b, "BM")
	put32 := func(off int, v uint32) {
		b[off], b[off+1], b[off+2], b[off+3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
	}
	put32(2, uint32(len(b)))
	put32(10, 54)
	put32(14, 40)
	put32(18, 2) // width
	put32(22, 2) // height
	b[26], b[28] = 1, 32
	put32(34, 16)
	copy(b[54:], "Hello, AltHex!!!")
	return b
}

func setup(t *testing.T, write bool) (*api.Service, *api.Files, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "x.bmp")
	if err := os.WriteFile(path, bmp(), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := &api.Files{AllowWrite: write}
	if _, err := ws.Add(path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ws.Close)
	return api.New(ws, tmpl.Library), ws, path
}

func call(t *testing.T, s *api.Service, method, params string) map[string]any {
	t.Helper()
	res, err := s.Call(context.Background(), method, json.RawMessage(params))
	if err != nil {
		t.Fatalf("%s %s: %v", method, params, err)
	}
	b, _ := json.Marshal(res)
	var m map[string]any
	json.Unmarshal(b, &m)
	return m
}

func callErr(t *testing.T, s *api.Service, method, params string) *rpc.Error {
	t.Helper()
	_, err := s.Call(context.Background(), method, json.RawMessage(params))
	var e *rpc.Error
	if !errors.As(err, &e) {
		t.Fatalf("%s %s: want an rpc error, got %v", method, params, err)
	}
	return e
}

func TestReadSearchHash(t *testing.T) {
	s, _, _ := setup(t, false)
	docs := call(t, s, "list_documents", `{}`)["documents"].([]any)
	if len(docs) != 1 || docs[0].(map[string]any)["id"] != "1" {
		t.Fatalf("documents: %v", docs)
	}
	r := call(t, s, "read", `{"offset": 54, "length": 5, "format": "hex"}`)
	if r["data"] != "48656c6c6f" {
		t.Errorf("hex: %v", r)
	}
	r = call(t, s, "read", `{"document": "1", "offset": 54, "length": 16, "format": "text"}`)
	if r["data"] != "Hello, AltHex!!!" {
		t.Errorf("text: %v", r)
	}
	r = call(t, s, "read", `{"length": 20}`)
	if d := r["data"].(string); !strings.HasPrefix(d, "00000000  42 4D") || !strings.Contains(d, "|BM") {
		t.Errorf("dump: %q", d)
	}
	r = call(t, s, "search", `{"text": "althex", "ignore_case": true}`)
	if offs := r["offsets"].([]any); len(offs) != 1 || offs[0].(float64) != 61 {
		t.Errorf("search: %v", r)
	}
	r = call(t, s, "search", `{"hex": "42 4D ?? 00"}`)
	if offs := r["offsets"].([]any); len(offs) != 1 || offs[0].(float64) != 0 {
		t.Errorf("search hex: %v", r)
	}
	r = call(t, s, "hash", `{"algorithms": ["crc32", "sum8"], "offset": 54, "length": 5}`)
	h := r["hashes"].(map[string]any)
	if h["crc32"] != "f7d18982" || h["sum8"] != "f4" { // "Hello"
		t.Errorf("hash: %v", h)
	}

	if e := callErr(t, s, "read", `{"document": "9"}`); e.Code != rpc.NotFound {
		t.Errorf("unknown document: %v", e)
	}
	if e := callErr(t, s, "read", `{"length": 2000000, "format": "hex"}`); e.Code != rpc.TooLarge {
		t.Errorf("too large: %v", e)
	}
	if e := callErr(t, s, "read", `{"offset": 1000}`); e.Code != rpc.InvalidParams {
		t.Errorf("outside: %v", e)
	}
	if e := callErr(t, s, "hash", `{"algorithms": ["nope"]}`); e.Code != rpc.InvalidParams {
		t.Errorf("bad algorithm: %v", e)
	}
	if e := callErr(t, s, "no_such_tool", `{}`); e.Code != rpc.MethodNotFound {
		t.Errorf("unknown method: %v", e)
	}
}

func TestTemplates(t *testing.T) {
	s, _, _ := setup(t, false)
	r := call(t, s, "list_templates", `{"document": "1"}`)
	if r["detected"] != "BMP image" {
		t.Errorf("detected: %v", r["detected"])
	}
	// get_structure applies the template that fits by itself
	r = call(t, s, "get_structure", `{"depth": 1}`)
	node := r["node"].(map[string]any)
	if r["template"] != "BMP image" || node["children_count"].(float64) < 2 {
		t.Fatalf("structure: %v", r)
	}
	r = call(t, s, "get_structure", `{"offset": 18, "depth": 0}`)
	if n := r["node"].(map[string]any); n["path"] != "info.width" || n["value"] != "2" {
		t.Errorf("at offset 18: %v", n)
	}
	r = call(t, s, "apply_template", `{"template": "BMP image"}`)
	if r["fields"].(float64) < 10 || r["root"] == nil {
		t.Errorf("apply: %v", r)
	}
	if e := callErr(t, s, "apply_template", `{"template": "no such"}`); e.Code != rpc.NotFound {
		t.Errorf("unknown template: %v", e)
	}
}

func TestEdits(t *testing.T) {
	s, _, path := setup(t, false)
	edits := `{"description": "test", "edits": [{"offset": 54, "overwrite": "68 65"}, {"offset": 0, "insert": "00"}, {"offset": 1, "delete": 1}]}`
	if e := callErr(t, s, "propose_edits", edits); e.Code != rpc.Forbidden {
		t.Fatalf("read-only workspace: %v", e)
	}

	s, _, path = setup(t, true)
	r := call(t, s, "propose_edits", edits)
	if r["status"] != "applied" {
		t.Fatalf("propose: %v", r)
	}
	// insert 00 at 0, then delete the 'B' that moved to 1: "M" follows the 00
	r = call(t, s, "read", `{"length": 2, "format": "hex"}`)
	if r["data"] != "004d" {
		t.Errorf("after the edits: %v", r)
	}
	r = call(t, s, "read", `{"offset": 54, "length": 5, "format": "text"}`)
	if r["data"] != "hello" {
		t.Errorf("overwrite: %v", r)
	}
	// Wrong edits change nothing
	for _, bad := range []string{
		`{"edits": [{"offset": 0, "overwrite": "zz"}]}`,
		`{"edits": [{"offset": 0, "overwrite": "00", "delete": 1}]}`,
		`{"edits": [{"offset": 1000, "insert": "00"}]}`,
		`{"edits": [{"offset": 60, "overwrite": "000000000000000000000000"}]}`,
		`{"edits": []}`,
	} {
		if e := callErr(t, s, "propose_edits", bad); e.Code != rpc.InvalidParams {
			t.Errorf("%s: %v", bad, e)
		}
	}
	call(t, s, "save_document", `{}`)
	b, _ := os.ReadFile(path)
	if len(b) != 70 || b[0] != 0 || b[1] != 'M' || string(b[54:59]) != "hello" {
		t.Errorf("saved: % X", b[:4])
	}
}

func TestOpenNeedsPermission(t *testing.T) {
	s, ws, path := setup(t, false)
	other := filepath.Join(filepath.Dir(path), "y.bin")
	os.WriteFile(other, []byte{1, 2, 3}, 0o644)
	if e := callErr(t, s, "open_document", `{"path": "`+filepath.ToSlash(other)+`"}`); e.Code != rpc.Forbidden {
		t.Fatalf("open: %v", e)
	}
	ws.AllowOpen = true
	r := call(t, s, "open_document", `{"path": "`+filepath.ToSlash(other)+`"}`)
	if r["size"].(float64) != 3 || r["read_only"] != true {
		t.Errorf("opened: %v", r)
	}
}

// TestMCP goes through the MCP server as an agent does
func TestMCP(t *testing.T) {
	s, _, _ := setup(t, false)
	mcp := rpc.NewMCP(rpc.Options{Name: "AltHex", Version: "test"}, s.Tools(), s, api.Instructions)
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mcp.ServeStream(ctx, inR, outW)
	out := bufio.NewScanner(outR)
	out.Buffer(nil, 1<<20)
	send := func(msg string) map[string]any {
		t.Helper()
		io.WriteString(inW, msg+"\n")
		if !out.Scan() {
			t.Fatal("no answer")
		}
		var m map[string]any
		json.Unmarshal(out.Bytes(), &m)
		return m
	}
	m := send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"tester","version":"1"}}}`)
	if res := m["result"].(map[string]any); !strings.Contains(res["instructions"].(string), "list_documents") {
		t.Errorf("initialize: %v", m)
	}
	io.WriteString(inW, `{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n")
	m = send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	tools := m["result"].(map[string]any)["tools"].([]any)
	if len(tools) != len(s.Tools()) {
		t.Errorf("tools: %d", len(tools))
	}
	for _, x := range tools {
		tool := x.(map[string]any)
		var schema map[string]any
		b, _ := json.Marshal(tool["inputSchema"])
		if err := json.Unmarshal(b, &schema); err != nil || schema["type"] != "object" {
			t.Errorf("%v: schema %s", tool["name"], b)
		}
	}
	m = send(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"read","arguments":{"offset":54,"length":5,"format":"text"}}}`)
	res := m["result"].(map[string]any)
	if res["isError"] == true || res["structuredContent"].(map[string]any)["data"] != "Hello" {
		t.Errorf("read: %v", m)
	}
	m = send(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"propose_edits","arguments":{"edits":[{"offset":0,"overwrite":"00"}]}}}`)
	if res := m["result"].(map[string]any); res["isError"] != true {
		t.Errorf("a forbidden edit must be a tool error: %v", m)
	}
}
