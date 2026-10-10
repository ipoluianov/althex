package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"testing"
	"time"
)

// testCalls are what the test handler saw that the tests check
type testCalls struct {
	notes    chan string
	started  chan struct{}
	canceled chan struct{}
}

func newTestHandler() (Handler, *testCalls) {
	tc := &testCalls{notes: make(chan string, 10), started: make(chan struct{}, 10), canceled: make(chan struct{}, 10)}
	h := HandlerFunc(func(ctx context.Context, method string, params json.RawMessage) (any, error) {
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
		case "note":
			var s string
			json.Unmarshal(params, &s)
			tc.notes <- s
			return nil, nil
		case "sleep":
			var ms int
			json.Unmarshal(params, &ms)
			time.Sleep(time.Duration(ms) * time.Millisecond)
			return ms, nil
		case "wait":
			tc.started <- struct{}{}
			<-ctx.Done()
			tc.canceled <- struct{}{}
			return nil, ctx.Err()
		case "list":
			return []int{1, 2}, nil
		}
		return nil, Errorf(MethodNotFound, "method not found: %s", method)
	})
	return h, tc
}

// lineClient talks raw JSON lines to a server and collects the replies by id
type lineClient struct {
	t     *testing.T
	w     io.Writer
	lines chan []byte
	stash map[string]map[string]any
}

func newLineClient(t *testing.T, r io.Reader, w io.Writer) *lineClient {
	c := &lineClient{t: t, w: w, lines: make(chan []byte, 100), stash: map[string]map[string]any{}}
	go func() {
		sc := bufio.NewScanner(r)
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			c.lines <- append([]byte(nil), sc.Bytes()...)
		}
		close(c.lines)
	}()
	return c
}

func (c *lineClient) send(s string) {
	c.t.Helper()
	if _, err := io.WriteString(c.w, s+"\n"); err != nil {
		c.t.Fatal(err)
	}
}

// next returns the next line, failing after a while
func (c *lineClient) next() []byte {
	c.t.Helper()
	select {
	case l, ok := <-c.lines:
		if !ok {
			c.t.Fatal("stream ended")
		}
		return l
	case <-time.After(5 * time.Second):
		c.t.Fatal("no reply")
	}
	return nil
}

// none checks that nothing comes for a short while
func (c *lineClient) none() {
	c.t.Helper()
	select {
	case l := <-c.lines:
		c.t.Fatalf("unexpected reply %s", l)
	case <-time.After(50 * time.Millisecond):
	}
}

// reply returns the response with the id, keeping others for later
func (c *lineClient) reply(id int) map[string]any {
	c.t.Helper()
	key := strconv.Itoa(id)
	for {
		if m, ok := c.stash[key]; ok {
			delete(c.stash, key)
			return m
		}
		var m map[string]any
		if err := json.Unmarshal(c.next(), &m); err != nil {
			c.t.Fatal(err)
		}
		c.stash[string(mustJSON(m["id"]))] = m
	}
}

// call sends a request and waits for its response
func (c *lineClient) call(id int, method, params string) map[string]any {
	c.t.Helper()
	req := `{"jsonrpc":"2.0","id":` + strconv.Itoa(id) + `,"method":"` + method + `"`
	if params != "" {
		req += `,"params":` + params
	}
	c.send(req + "}")
	return c.reply(id)
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// errCode returns the error code of a response, 0 without error
func errCode(m map[string]any) int {
	e, ok := m["error"].(map[string]any)
	if !ok {
		return 0
	}
	return int(e["code"].(float64))
}
