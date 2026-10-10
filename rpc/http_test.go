package rpc

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func startHTTP(t *testing.T, token string) *httptest.Server {
	h, _ := newTestHandler()
	m := NewMCP(Options{Name: "althex", Version: "1.0"}, testTools, h, "")
	s := httptest.NewServer(m.HTTPHandler(token))
	t.Cleanup(s.Close)
	return s
}

// do sends a request; headers are name, value pairs
func do(t *testing.T, s *httptest.Server, method, body string, headers ...string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, s.URL, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := s.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(b)
}

const initBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","clientInfo":{"name":"web"}}}`

func TestHTTPSession(t *testing.T) {
	s := startHTTP(t, "")
	resp, body := do(t, s, "POST", initBody)
	id := resp.Header.Get(sessionHeader)
	if resp.StatusCode != 200 || id == "" || !strings.Contains(body, `"protocolVersion":"2025-06-18"`) {
		t.Fatalf("initialize: %d %q %s", resp.StatusCode, id, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content type %s", ct)
	}

	resp, _ = do(t, s, "POST", `{"jsonrpc":"2.0","method":"notifications/initialized"}`, sessionHeader, id)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("notification: %d", resp.StatusCode)
	}
	resp, body = do(t, s, "POST", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"who"}}`,
		sessionHeader, id, "MCP-Protocol-Version", "2025-06-18")
	if resp.StatusCode != 200 || !strings.Contains(body, `"name":"web"`) || !strings.Contains(body, `"transport":"http"`) {
		t.Fatalf("who: %d %s", resp.StatusCode, body)
	}
	resp, body = do(t, s, "POST", `[{"jsonrpc":"2.0","id":3,"method":"ping"},{"jsonrpc":"2.0","id":4,"method":"tools/list"}]`, sessionHeader, id)
	if resp.StatusCode != 200 || !strings.HasPrefix(body, "[") {
		t.Fatalf("batch: %d %s", resp.StatusCode, body)
	}

	if resp, _ := do(t, s, "POST", `{"jsonrpc":"2.0","id":2,"method":"ping"}`); resp.StatusCode != 400 {
		t.Fatalf("missing session: %d", resp.StatusCode)
	}
	if resp, _ := do(t, s, "POST", `{"jsonrpc":"2.0","id":2,"method":"ping"}`, sessionHeader, "nosuch"); resp.StatusCode != 404 {
		t.Fatalf("unknown session: %d", resp.StatusCode)
	}
	if resp, _ := do(t, s, "POST", `{bad`, sessionHeader, id); resp.StatusCode != 400 {
		t.Fatalf("parse error: %d", resp.StatusCode)
	}
	if resp, _ := do(t, s, "POST", `{"jsonrpc":"2.0","id":2,"method":"ping"}`, sessionHeader, id, "MCP-Protocol-Version", "1999-01-01"); resp.StatusCode != 400 {
		t.Fatalf("bad protocol version: %d", resp.StatusCode)
	}
	if resp, _ := do(t, s, "GET", "", sessionHeader, id); resp.StatusCode != 405 {
		t.Fatalf("GET: %d", resp.StatusCode)
	}

	if resp, _ := do(t, s, "DELETE", "", sessionHeader, id); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE: %d", resp.StatusCode)
	}
	if resp, _ := do(t, s, "POST", `{"jsonrpc":"2.0","id":5,"method":"ping"}`, sessionHeader, id); resp.StatusCode != 404 {
		t.Fatalf("after DELETE: %d", resp.StatusCode)
	}
}

func TestHTTPSecurity(t *testing.T) {
	s := startHTTP(t, "secret")
	auth := "Bearer secret"
	for _, origin := range []string{"http://evil.example", "http://localhost.evil.example", "null", "file://"} {
		if resp, _ := do(t, s, "POST", initBody, "Origin", origin, "Authorization", auth); resp.StatusCode != 403 {
			t.Errorf("origin %s: %d", origin, resp.StatusCode)
		}
	}
	for _, origin := range []string{"http://localhost", "http://localhost:6274", "http://127.0.0.1:8080", "http://[::1]:3000"} {
		if resp, _ := do(t, s, "POST", initBody, "Origin", origin, "Authorization", auth); resp.StatusCode != 200 {
			t.Errorf("origin %s: %d", origin, resp.StatusCode)
		}
	}
	if resp, _ := do(t, s, "POST", initBody); resp.StatusCode != 401 || resp.Header.Get("WWW-Authenticate") == "" {
		t.Errorf("no token: %d", resp.StatusCode)
	}
	if resp, _ := do(t, s, "POST", initBody, "Authorization", "Bearer wrong"); resp.StatusCode != 401 {
		t.Errorf("wrong token: %d", resp.StatusCode)
	}
	if resp, _ := do(t, s, "POST", initBody, "Authorization", "secret"); resp.StatusCode != 401 {
		t.Errorf("not bearer: %d", resp.StatusCode)
	}
}
