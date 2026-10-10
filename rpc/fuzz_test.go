package rpc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/ipoluianov/althex/internal/fuzzguard"
)

// fuzzHandler answers at once: the fuzzing is of the protocol, not of the calls
var fuzzHandler = HandlerFunc(func(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case "echo":
		if params == nil {
			return nil, nil
		}
		return params, nil
	case "who":
		return ClientFrom(ctx), nil
	case "fail":
		return nil, &Error{Code: NotFound, Message: "no such thing", Data: "x"}
	case "plain":
		return nil, errors.New("boom")
	case "panic":
		panic("oops")
	case "list":
		return []int{1, 2}, nil
	}
	return nil, Errorf(MethodNotFound, "method not found: %s", method)
})

var fuzzTools = []Tool{
	{Name: "echo", Title: "Echo", InputSchema: json.RawMessage(`{"type":"object"}`), ReadOnly: true, Idempotent: true},
	{Name: "fail", Destructive: true},
	{Name: "list", ReadOnly: true},
	{Name: "who", ReadOnly: true},
	{Name: "panic"},
}

var rpcSeeds = []string{
	`{"jsonrpc":"2.0","id":1,"method":"echo","params":{"a":1}}`,
	`{"jsonrpc":"2.0","id":"x","method":"initialize","params":{"protocolVersion":"2025-03-26","clientInfo":{"name":"c","version":"1"}}}`,
	`[{"jsonrpc":"2.0","id":1,"method":"list"},{"jsonrpc":"2.0","method":"echo"},{"jsonrpc":"2.0","id":2,"method":"fail"}]`,
	`{"jsonrpc":"2.0","id":3,"method":"tools/list"}` + "\n" + `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"echo","arguments":{"x":[1,2]}}}`,
	`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}` + "\r\n" + `{"jsonrpc":"2.0","id":5,"method":"ping"}`,
	`{"jsonrpc":"2.0","id":6,"method":"panic"}` + "\n{bad\n[]\n[1]\n" + `{"jsonrpc":"1.0","id":{},"method":7}`,
}

// syncBuffer collects the replies, written from several goroutines
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

// checkReplies: each line written is one JSON value
func checkReplies(t *testing.T, out []byte) {
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(nil, 64<<20)
	for sc.Scan() {
		if !json.Valid(sc.Bytes()) {
			t.Fatalf("a reply is not JSON: %q", sc.Bytes())
		}
	}
}

type fuzzConn struct {
	io.Reader
	io.Writer
}

func (fuzzConn) Close() error { return nil }

func fuzzGrace(t *testing.T) {
	old := eofGrace
	eofGrace = 200 * time.Millisecond
	t.Cleanup(func() { eofGrace = old })
}

// FuzzServeConn feeds any stream to the JSON-RPC server
func FuzzServeConn(f *testing.F) {
	for _, s := range rpcSeeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		fuzzGrace(t)
		stop := fuzzguard.Start("ServeConn")
		defer stop()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var out syncBuffer
		ServeConn(ctx, fuzzConn{bytes.NewReader(in), &out}, fuzzHandler, Options{Name: "althex", Version: "1", Transport: "stdio"})
		checkReplies(t, out.b.Bytes())
	})
}

// FuzzMCP feeds any stream to the MCP server of the stdio transport
func FuzzMCP(f *testing.F) {
	for _, s := range rpcSeeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		fuzzGrace(t)
		stop := fuzzguard.Start("MCP")
		defer stop()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var out syncBuffer
		m := NewMCP(Options{Name: "althex", Version: "1"}, fuzzTools, fuzzHandler, "use the tools")
		m.ServeStream(ctx, bytes.NewReader(in), &out)
		checkReplies(t, out.b.Bytes())
	})
}

// FuzzHTTP sends any request to the Streamable HTTP transport: a session
// is started first, then the fuzzed body goes in it or without one
func FuzzHTTP(f *testing.F) {
	for i, s := range rpcSeeds {
		f.Add(uint8(i), "", []byte(s))
	}
	f.Add(uint8(1), "2025-06-18", []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	f.Fuzz(func(t *testing.T, flags uint8, version string, body []byte) {
		stop := fuzzguard.Start("HTTP")
		defer stop()
		m := NewMCP(Options{Name: "althex", Version: "1"}, fuzzTools, fuzzHandler, "")
		h := m.HTTPHandler("secret")
		send := func(method, session string, body []byte) *httptest.ResponseRecorder {
			r := httptest.NewRequest(method, "/mcp", bytes.NewReader(body))
			r.Header.Set("Authorization", "Bearer secret")
			if flags&4 != 0 {
				r.Header.Set("Authorization", "Bearer "+version)
			}
			if flags&8 != 0 {
				r.Header.Set("Origin", version)
			}
			if version != "" {
				r.Header.Set("MCP-Protocol-Version", version)
			}
			if session != "" {
				r.Header.Set(sessionHeader, session)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code == http.StatusOK && !json.Valid(w.Body.Bytes()) {
				t.Fatalf("a reply is not JSON: %q", w.Body.Bytes())
			}
			return w
		}
		session := ""
		if flags&1 != 0 {
			w := send(http.MethodPost, "", []byte(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"clientInfo":{"name":"f"}}}`))
			session = w.Header().Get(sessionHeader)
		}
		method := http.MethodPost
		if flags&2 != 0 {
			method = http.MethodDelete
		}
		send(method, session, body)
		if flags&16 != 0 {
			send(http.MethodPost, string(body[:min(len(body), 32)]), body)
		}
	})
}

// FuzzClient gives the client any answers to the calls it waits for
func FuzzClient(f *testing.F) {
	f.Add([]byte(`{"jsonrpc":"2.0","id":1,"result":{"a":1}}` + "\n" + `[{"id":2,"error":{"code":-32601,"message":"no"}},{"id":3,"result":null}]`))
	f.Add([]byte(`{"jsonrpc":"2.0","id":1,"result":"x"}` + "\n" + `{"id":1,"result":1}` + "\n" + `{"id":"2"}`))
	f.Fuzz(func(t *testing.T, in []byte) {
		stop := fuzzguard.Start("Client")
		defer stop()
		pr, pw := io.Pipe()
		c := NewConn(fuzzConn{pr, io.Discard})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var wg sync.WaitGroup
		for range 3 {
			wg.Go(func() {
				var res any
				c.Call(ctx, "echo", map[string]int{"a": 1}, &res)
			})
		}
		// The calls wait for answers before the answers come
		for {
			c.mu.Lock()
			n := len(c.pending)
			c.mu.Unlock()
			if n == 3 {
				break
			}
			time.Sleep(time.Millisecond)
		}
		go func() {
			pw.Write(in)
			pw.Close()
		}()
		wg.Wait()
		<-c.Done()
		pr.Close() // the reading may stop before the end (a line too long)
	})
}
