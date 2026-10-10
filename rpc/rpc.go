// Package rpc carries the external interfaces of the editor: a JSON-RPC 2.0
// local API over a stream (stdio, a unix socket, a Windows named pipe) and
// an MCP server over stdio and Streamable HTTP. The methods themselves are
// run by a Handler; this package only moves messages and keeps sessions.
package rpc

import (
	"context"
	"encoding/json"
	"fmt"
)

// maxMessage is the largest message accepted: enough for a big batch of
// bytes in hex, small enough that a broken client cannot exhaust memory
const maxMessage = 16 << 20

// Handler runs the methods. The result is marshalled to JSON; return an
// *Error to answer with a particular JSON-RPC error code.
type Handler interface {
	Call(ctx context.Context, method string, params json.RawMessage) (any, error)
}

// HandlerFunc lets a function be a Handler
type HandlerFunc func(ctx context.Context, method string, params json.RawMessage) (any, error)

// Call runs f
func (f HandlerFunc) Call(ctx context.Context, method string, params json.RawMessage) (any, error) {
	return f(ctx, method, params)
}

// Standard JSON-RPC error codes
const (
	ParseError     = -32700
	InvalidRequest = -32600
	MethodNotFound = -32601
	InvalidParams  = -32602
	InternalError  = -32603
)

// Application error codes, in the range JSON-RPC leaves to servers
const (
	Rejected  = -32001 // the user declined
	NotFound  = -32002
	Forbidden = -32003 // switched off or not allowed
	TooLarge  = -32004
)

// Error is a JSON-RPC error
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Error returns the message alone: it is what a user or a model reads,
// the code is there for programs
func (e *Error) Error() string {
	return e.Message
}

// Errorf makes an *Error with a formatted message
func Errorf(code int, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Options describe the server to its clients
type Options struct {
	Name    string // the server name told at initialize
	Version string
	// Transport goes into Client.Transport; Serve fills it in from the listener when empty
	Transport string
}

// Client tells a Handler who is connected
type Client struct {
	Name      string `json:"name"` // the name the client gave at initialize, "unknown" if none
	Version   string `json:"version,omitempty"`
	Transport string `json:"transport"` // "stdio", "socket", "pipe" or "http"
}

const unknownClient = "unknown"

type clientKey struct{}

// WithClient returns ctx carrying c
func WithClient(ctx context.Context, c Client) context.Context {
	return context.WithValue(ctx, clientKey{}, c)
}

// ClientFrom returns the client the context of a Handler call carries
func ClientFrom(ctx context.Context) Client {
	if c, ok := ctx.Value(clientKey{}).(Client); ok {
		return c
	}
	return Client{Name: unknownClient}
}

// clientInfo is the clientInfo of initialize, the same in MCP and in the local API
type clientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// client makes the Client of a connection from what it told at initialize
func (ci clientInfo) client(transport string) Client {
	c := Client{Name: ci.Name, Version: ci.Version, Transport: transport}
	if c.Name == "" {
		c.Name = unknownClient
	}
	return c
}
