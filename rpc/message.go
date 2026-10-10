package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

const version = "2.0"

// message is any incoming JSON-RPC message: a request, a notification
// (no id) or a response (result or error, no method)
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

var nullID = json.RawMessage("null")

// validID tells whether id is a string, a number or null, the only kinds JSON-RPC allows
func validID(id json.RawMessage) bool {
	switch {
	case len(id) == 0:
		return false
	case id[0] == '"', id[0] == '-', id[0] >= '0' && id[0] <= '9':
		return true
	default:
		return bytes.Equal(id, nullID)
	}
}

// encodeError makes the response with an error
func encodeError(id json.RawMessage, e *Error) []byte {
	if len(id) == 0 {
		id = nullID
	}
	b, _ := json.Marshal(response{JSONRPC: version, ID: id, Error: e})
	return b
}

// asError turns what a Handler returned into a JSON-RPC error: an *Error
// keeps its code, anything else is an internal error
func asError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{Code: InternalError, Message: err.Error()}
}

// handleFunc runs one request or notification
type handleFunc func(ctx context.Context, method string, params json.RawMessage) (any, error)

// processor turns incoming messages into the replies, for one connection
// or session. It remembers the calls in flight so that a peer can cancel them.
type processor struct {
	handle handleFunc

	mu    sync.Mutex
	calls map[string]*call
}

// call is a request in flight
type call struct {
	cancel   context.CancelFunc
	canceled bool // by the peer: then it gets no response
}

func newProcessor(h handleFunc) *processor {
	return &processor{handle: h, calls: map[string]*call{}}
}

// process answers one message, which may be a batch. It returns the reply
// to send, nil when there is none (notifications and responses only).
// The calls of a batch run concurrently.
func (p *processor) process(ctx context.Context, data []byte) []byte {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil
	}
	if !json.Valid(data) {
		return encodeError(nil, &Error{Code: ParseError, Message: "parse error"})
	}
	if data[0] != '[' {
		return p.one(ctx, data)
	}
	var items []json.RawMessage
	if err := json.Unmarshal(data, &items); err != nil {
		return encodeError(nil, &Error{Code: ParseError, Message: "parse error"})
	}
	if len(items) == 0 {
		return encodeError(nil, &Error{Code: InvalidRequest, Message: "empty batch"})
	}
	replies := make([][]byte, len(items))
	var wg sync.WaitGroup
	for i, item := range items {
		wg.Go(func() { replies[i] = p.one(ctx, item) })
	}
	wg.Wait()
	var out []json.RawMessage
	for _, r := range replies {
		if r != nil {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return nil
	}
	b, _ := json.Marshal(out)
	return b
}

// one answers a single message
func (p *processor) one(ctx context.Context, data []byte) []byte {
	var m message
	if err := json.Unmarshal(data, &m); err != nil || data[0] != '{' {
		return encodeError(nil, &Error{Code: InvalidRequest, Message: "invalid request"})
	}
	if m.Method == "" && (m.Result != nil || m.Error != nil) {
		return nil // a response: this side sends no requests, nothing waits for it
	}
	if m.ID != nil && !validID(m.ID) {
		return encodeError(nil, &Error{Code: InvalidRequest, Message: "invalid id"})
	}
	if m.JSONRPC != version || m.Method == "" {
		if m.ID == nil {
			return nil
		}
		return encodeError(m.ID, &Error{Code: InvalidRequest, Message: "invalid request"})
	}
	if m.ID == nil {
		p.run(ctx, m.Method, m.Params) // a notification: no answer, even to an error
		return nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	key := idKey(m.ID)
	c := &call{cancel: cancel}
	p.mu.Lock()
	p.calls[key] = c
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		if p.calls[key] == c {
			delete(p.calls, key)
		}
		p.mu.Unlock()
	}()

	result, err := p.run(ctx, m.Method, m.Params)
	p.mu.Lock()
	canceled := c.canceled
	p.mu.Unlock()
	if canceled {
		return nil
	}
	if err != nil {
		return encodeError(m.ID, asError(err))
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return encodeError(m.ID, &Error{Code: InternalError, Message: "cannot encode the result: " + err.Error()})
	}
	b, err := json.Marshal(response{JSONRPC: version, ID: m.ID, Result: raw})
	if err != nil {
		return encodeError(m.ID, &Error{Code: InternalError, Message: "cannot encode the result: " + err.Error()})
	}
	return b
}

// run calls the handler; a panic becomes an internal error rather than
// taking the whole application down
func (p *processor) run(ctx context.Context, method string, params json.RawMessage) (result any, err error) {
	defer func() {
		if r := recover(); r != nil {
			result, err = nil, &Error{Code: InternalError, Message: fmt.Sprint("internal error: ", r)}
		}
	}()
	return p.handle(ctx, method, params)
}

// cancel stops the call with the id, which then gets no response
func (p *processor) cancel(id json.RawMessage) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c := p.calls[idKey(id)]; c != nil {
		c.canceled = true
		c.cancel()
	}
}

// cancelAll stops every call in flight, when the session ends
func (p *processor) cancelAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.calls {
		c.canceled = true
		c.cancel()
	}
}

// idKey makes the map key of an id, the same however it was spaced
func idKey(id json.RawMessage) string {
	var b bytes.Buffer
	if json.Compact(&b, id) != nil {
		return string(id)
	}
	return b.String()
}
