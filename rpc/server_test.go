package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func startPipe(t *testing.T) (*lineClient, *testCalls, chan error) {
	h, tc := newTestHandler()
	server, client := net.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- ServeConn(context.Background(), server, h, Options{Name: "althex", Version: "1.0", Transport: "stdio"})
	}()
	t.Cleanup(func() { client.Close() })
	return newLineClient(t, client, client), tc, done
}

func TestServeConnRequests(t *testing.T) {
	c, tc, _ := startPipe(t)

	r := c.call(1, "echo", `{"a":1}`)
	if string(mustJSON(r["result"])) != `{"a":1}` || r["jsonrpc"] != "2.0" {
		t.Fatalf("echo: %v", r)
	}

	// A notification gets no answer, but runs
	c.send(`{"jsonrpc":"2.0","method":"note","params":"hi"}`)
	if n := <-tc.notes; n != "hi" {
		t.Fatalf("note: %q", n)
	}
	c.send(`{"jsonrpc":"2.0","method":"nosuch"}`)
	c.none()

	if r := c.call(2, "nosuch", ""); errCode(r) != MethodNotFound {
		t.Fatalf("not found: %v", r)
	}
	if r := c.call(3, "plain", ""); errCode(r) != InternalError {
		t.Fatalf("plain error: %v", r)
	}
	if r := c.call(4, "panic", ""); errCode(r) != InternalError {
		t.Fatalf("panic: %v", r)
	}

}

func TestServeConnErrors(t *testing.T) {
	c, _, _ := startPipe(t)
	var m map[string]any

	c.send(`{bad`)
	json.Unmarshal(c.next(), &m)
	if errCode(m) != ParseError || m["id"] != nil {
		t.Fatalf("parse error: %v", m)
	}
	c.send(`{"jsonrpc":"1.0","id":5,"method":"echo"}`)
	json.Unmarshal(c.next(), &m)
	if errCode(m) != InvalidRequest || m["id"] != 5.0 {
		t.Fatalf("invalid request: %v", m)
	}
	c.send(`[]`)
	json.Unmarshal(c.next(), &m)
	if errCode(m) != InvalidRequest {
		t.Fatalf("empty batch: %v", m)
	}
	c.send(`{"jsonrpc":"2.0","id":{},"method":"echo"}`)
	json.Unmarshal(c.next(), &m)
	if errCode(m) != InvalidRequest {
		t.Fatalf("object id: %v", m)
	}
	c.send(`{"jsonrpc":"2.0","id":null,"method":"echo","params":7}`)
	m = nil
	json.Unmarshal(c.next(), &m)
	if v, ok := m["id"]; !ok || v != nil || m["result"] != 7.0 {
		t.Fatalf("null id: %v", m)
	}
	// A response from the peer is not answered
	c.send(`{"jsonrpc":"2.0","id":9,"result":null}`)
	c.none()
}

func TestServeConnBatch(t *testing.T) {
	c, tc, _ := startPipe(t)
	c.send(`[{"jsonrpc":"2.0","id":1,"method":"sleep","params":30},` +
		`{"jsonrpc":"2.0","method":"note","params":"b"},` +
		`{"jsonrpc":"2.0","id":2,"method":"nosuch"},` +
		`5]`)
	var batch []map[string]any
	if err := json.Unmarshal(c.next(), &batch); err != nil {
		t.Fatal(err)
	}
	if len(batch) != 3 {
		t.Fatalf("batch: %v", batch)
	}
	codes := map[string]int{}
	for _, m := range batch {
		codes[string(mustJSON(m["id"]))] = errCode(m)
	}
	if codes["1"] != 0 || codes["2"] != MethodNotFound || codes["null"] != InvalidRequest {
		t.Fatalf("batch codes: %v", codes)
	}
	if n := <-tc.notes; n != "b" {
		t.Fatal(n)
	}
	// Only notifications: nothing comes back
	c.send(`[{"jsonrpc":"2.0","method":"note","params":"c"}]`)
	<-tc.notes
	c.none()
}

func TestServeConnTooLarge(t *testing.T) {
	c, _, done := startPipe(t)
	go io.WriteString(c.w, `"`+strings.Repeat("x", maxMessage+10)+`"`+"\n")
	var m map[string]any
	json.Unmarshal(c.next(), &m)
	if errCode(m) != TooLarge {
		t.Fatalf("too large: %v", m)
	}
	if err := <-done; err == nil {
		t.Fatal("no error")
	}
}

func TestServeConnEnds(t *testing.T) {
	h, _ := newTestHandler()
	server, client := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ServeConn(ctx, server, h, Options{}) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ServeConn did not return on cancel")
	}
	client.Close()
}

func TestUnixSocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.sock")
	l, err := ListenUnix(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ListenUnix(path); err == nil {
		t.Fatal("listened twice")
	}
	h, _ := newTestHandler()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, l, h, Options{Name: "althex", Version: "1"}) }()

	conn, err := Dial("unix", path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	var who Client
	if err := conn.Call(ctx, "who", nil, &who); err != nil {
		t.Fatal(err)
	}
	if who != (Client{Name: "unknown", Transport: "socket"}) {
		t.Fatalf("before initialize: %+v", who)
	}
	if err := conn.Initialize(ctx, Client{Name: "tester", Version: "2"}); err != nil {
		t.Fatal(err)
	}
	if err := conn.Call(ctx, "who", nil, &who); err != nil {
		t.Fatal(err)
	}
	if who != (Client{Name: "tester", Version: "2", Transport: "socket"}) {
		t.Fatalf("after initialize: %+v", who)
	}

	// Errors keep their code and data
	err = conn.Call(ctx, "fail", nil, nil)
	var e *Error
	if !errors.As(err, &e) || e.Code != NotFound || e.Message != "no such thing" || e.Data != "x" {
		t.Fatalf("fail: %#v", err)
	}
	if err := conn.Call(ctx, "nosuch", nil, nil); !errors.As(err, &e) || e.Code != MethodNotFound {
		t.Fatalf("nosuch: %v", err)
	}

	var raw json.RawMessage
	if err := conn.Call(ctx, "echo", map[string]int{"b": 2}, &raw); err != nil || string(raw) != `{"b":2}` {
		t.Fatalf("raw: %s %v", raw, err)
	}

	// Concurrent calls, answered out of order
	var wg sync.WaitGroup
	errs := make(chan error, 50)
	for i := range 50 {
		wg.Go(func() {
			ms := (50 - i) % 7
			var got int
			if err := conn.Call(ctx, "sleep", ms, &got); err != nil {
				errs <- err
			} else if got != ms {
				errs <- fmt.Errorf("call %d: got %d", i, got)
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	// A call gives up with its context
	short, stop := context.WithTimeout(ctx, 20*time.Millisecond)
	defer stop()
	if err := conn.Call(short, "sleep", 500, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout: %v", err)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Serve: %v", err)
	}
	select {
	case <-conn.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("connection not closed by the server")
	}
	if err := conn.Call(context.Background(), "echo", 1, nil); err == nil {
		t.Fatal("call on a closed connection")
	}
}
