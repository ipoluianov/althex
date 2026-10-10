// Package api is what the external interfaces can do (EXT-6, EXT-7): list
// the open documents, read, search, compute hashes, apply templates and
// walk their trees, propose edits. The window and the CLI give it their
// documents through a Workspace; the transports (package rpc) call it by
// the names of its tools, the same over MCP and the local JSON-RPC API.
package api

import (
	"context"
	"errors"

	"github.com/ipoluianov/althex/core/doc"
	"github.com/ipoluianov/althex/rpc"
)

// DocInfo is a document as the clients see it
type DocInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Path     string `json:"path,omitempty"`
	Size     int64  `json:"size"`
	Modified bool   `json:"modified"`
	ReadOnly bool   `json:"read_only"`
	// Current: the document the user looks at
	Current bool `json:"current,omitempty"`
	// The caret and the selection in the window
	Cursor   int64 `json:"cursor"`
	SelStart int64 `json:"selection_start"`
	SelSize  int64 `json:"selection_size"`
}

// Edit is one change of a proposal; the offsets are of the document as the
// edits before it in the list left it
type Edit struct {
	Offset int64 `json:"offset"`
	// One of: the bytes written over, the bytes inserted, the bytes deleted
	Overwrite []byte `json:"-"`
	Insert    []byte `json:"-"`
	Delete    int64  `json:"delete,omitempty"`
}

// Proposal is what a client asks to change (EXT-8)
type Proposal struct {
	Doc         string
	Edits       []Edit
	Description string // why, in the client's words: shown to the user
}

// Workspace holds the documents: the tabs of the window, or the files of
// a command. Its methods are called from the goroutines of the connections.
type Workspace interface {
	Documents() []DocInfo
	// Snapshot gives the document to read: a snapshot is safe from any
	// goroutine and does not change
	Snapshot(id string) (*doc.Snapshot, DocInfo, error)
	// Open opens a file; the window asks the user first
	Open(ctx context.Context, client rpc.Client, path string) (DocInfo, error)
	// Apply makes the edits one step of the history, after the user agreed
	// (the window) or when writing is allowed (a command); ErrRejected when not
	Apply(ctx context.Context, client rpc.Client, p Proposal) (DocInfo, error)
	// Save writes the document to its file, asking the user first
	Save(ctx context.Context, client rpc.Client, id string) (DocInfo, error)
	// Show selects the bytes in the window; false - there is no window
	Show(id string, off, n int64) bool
}

// The errors of a Workspace
var (
	ErrNotFound  = errors.New("not found")
	ErrRejected  = errors.New("the user declined")
	ErrForbidden = errors.New("not allowed")
)

// rpcError gives the error its JSON-RPC code
func rpcError(err error) error {
	var e *rpc.Error
	switch {
	case err == nil:
		return nil
	case errors.As(err, &e):
		return err
	case errors.Is(err, ErrNotFound):
		return &rpc.Error{Code: rpc.NotFound, Message: err.Error()}
	case errors.Is(err, ErrRejected):
		return &rpc.Error{Code: rpc.Rejected, Message: err.Error()}
	case errors.Is(err, ErrForbidden):
		return &rpc.Error{Code: rpc.Forbidden, Message: err.Error()}
	}
	return err
}

func invalid(format string, args ...any) error {
	return &rpc.Error{Code: rpc.InvalidParams, Message: sprintf(format, args...)}
}

func tooLarge(format string, args ...any) error {
	return &rpc.Error{Code: rpc.TooLarge, Message: sprintf(format, args...)}
}
