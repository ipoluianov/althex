package rpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// ErrClosed is returned by the calls of a Conn that has ended
var ErrClosed = errors.New("rpc: connection closed")

// Conn calls a running server. Calls may be made from many goroutines at once.
type Conn struct {
	rw  io.ReadWriteCloser
	wmu sync.Mutex

	mu      sync.Mutex
	nextID  uint64
	pending map[uint64]chan *clientResponse
	err     error // why the reading stopped
	done    chan struct{}
}

type clientResponse struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *Error          `json:"error"`
}

type clientRequest struct {
	JSONRPC string  `json:"jsonrpc"`
	ID      *uint64 `json:"id,omitempty"`
	Method  string  `json:"method"`
	Params  any     `json:"params,omitempty"`
}

// Dial connects to a server: network is "unix" with the socket path, or
// "pipe" with the name of a Windows named pipe
func Dial(network, address string, timeout time.Duration) (*Conn, error) {
	var c net.Conn
	var err error
	switch network {
	case "unix":
		c, err = net.DialTimeout("unix", address, timeout)
	case "pipe":
		c, err = DialPipe(address, timeout)
	default:
		return nil, fmt.Errorf("rpc: unknown network %q", network)
	}
	if err != nil {
		return nil, err
	}
	return NewConn(c), nil
}

// NewConn makes a client on a stream; the Conn owns it and closes it
func NewConn(rw io.ReadWriteCloser) *Conn {
	c := &Conn{rw: rw, pending: map[uint64]chan *clientResponse{}, done: make(chan struct{})}
	go c.read()
	return c
}

// read passes each response to the call that waits for it
func (c *Conn) read() {
	sc := bufio.NewScanner(c.rw)
	sc.Buffer(make([]byte, 64<<10), maxMessage+2)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) > 0 && line[0] == '[' {
			var batch []clientResponse
			if json.Unmarshal(line, &batch) == nil {
				for i := range batch {
					c.deliver(&batch[i])
				}
			}
			continue
		}
		var r clientResponse
		if json.Unmarshal(line, &r) == nil {
			c.deliver(&r)
		}
	}
	err := sc.Err()
	if err == nil {
		err = ErrClosed
	}
	c.mu.Lock()
	c.err = err
	for id, ch := range c.pending {
		delete(c.pending, id)
		close(ch)
	}
	c.mu.Unlock()
	close(c.done)
}

func (c *Conn) deliver(r *clientResponse) {
	var id uint64
	if json.Unmarshal(r.ID, &id) != nil {
		return // a request or a notification from the server, or an error without id
	}
	c.mu.Lock()
	ch := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()
	if ch != nil {
		ch <- r
	}
}

func (c *Conn) send(req clientRequest) error {
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err = c.rw.Write(append(b, '\n'))
	return err
}

// Call calls method and unmarshals the result into result, which may be
// nil to drop it or a *json.RawMessage to keep it as is. An error the
// server answered with is returned as an *Error.
func (c *Conn) Call(ctx context.Context, method string, params any, result any) error {
	ch := make(chan *clientResponse, 1)
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return err
	}
	c.nextID++
	id := c.nextID
	c.pending[id] = ch
	c.mu.Unlock()

	if err := c.send(clientRequest{JSONRPC: version, ID: &id, Method: method, Params: params}); err != nil {
		c.forget(id)
		return err
	}
	select {
	case r, ok := <-ch:
		if !ok {
			c.mu.Lock()
			defer c.mu.Unlock()
			return c.err
		}
		if r.Error != nil {
			return r.Error
		}
		if result == nil {
			return nil
		}
		if len(r.Result) == 0 {
			r.Result = nullID
		}
		return json.Unmarshal(r.Result, result)
	case <-ctx.Done():
		c.forget(id)
		return ctx.Err()
	}
}

func (c *Conn) forget(id uint64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// Notify sends a notification, which has no answer
func (c *Conn) Notify(ctx context.Context, method string, params any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.send(clientRequest{JSONRPC: version, Method: method, Params: params})
}

// Initialize tells the server who calls; its Handler sees it in ClientFrom.
// Transport is not sent: the server knows how the client came.
func (c *Conn) Initialize(ctx context.Context, cl Client) error {
	params := struct {
		ClientInfo clientInfo `json:"clientInfo"`
	}{clientInfo{Name: cl.Name, Version: cl.Version}}
	return c.Call(ctx, "initialize", params, nil)
}

// Close ends the connection; calls waiting for answers return ErrClosed
func (c *Conn) Close() error {
	return c.rw.Close()
}

// Done is closed when the connection has ended
func (c *Conn) Done() <-chan struct{} {
	return c.done
}
