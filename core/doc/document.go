// Package doc is the model of a document: the bytes of a source with the
// edits on top of it, the history of the edits, the journal that survives a
// crash, and saving. The source is not written until the document is saved.
//
// The document is a persistent tree of pieces (tree.go): an edit makes a new
// root in O(log n) of the number of pieces, whatever the size of the file.
// Edits come from one goroutine at a time; reading (Snapshot) is safe from
// any goroutine and never waits for an edit.
package doc

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/ipoluianov/althex/core/source"
)

var (
	ErrReadOnly    = errors.New("the document is read-only")
	ErrOutOfRange  = errors.New("offset out of range")
	ErrNothingToDo = errors.New("nothing to undo or redo")
)

// The buffers every document has; sources follow them
const (
	bufAdd   = 0
	bufZeros = 1
)

// Options of opening a document
type Options struct {
	// JournalDir is where the journal of edits is kept, so they can be
	// recovered after a crash; "" - no journal, the added bytes stay in memory
	JournalDir string
	ReadOnly   bool
}

// Document is an open source with edits
type Document struct {
	mu sync.Mutex // edits, history and saving

	path     string // "" - a new document not saved yet
	readOnly bool

	add  *addBuffer
	bufs []buffer
	// src is the buffer of the file the document is saved to, srcIdx its
	// index in bufs; nil when there is no such file
	src    *srcBuffer
	srcIdx int
	file   *source.File
	// Sources replaced by saving, kept open while older states use them
	oldFiles []*source.File

	snap atomic.Pointer[Snapshot]
	hist *history

	// The edit group (BeginGroup): its kind and the state it makes
	groupKind string
	groupNode *HistNode
	groupDeep int

	journal *journal

	listenersMu sync.Mutex
	listeners   map[int]func()
	nextListen  int
}

// Snapshot is the document at one moment. It does not change: later edits
// make new snapshots. Safe for use from any goroutine.
type Snapshot struct {
	root *node
	bufs []buffer
	src  int // the buffer of the file as saved; 0 - none
}

// Open opens the file
func Open(path string, opts Options) (*Document, error) {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	f, err := source.OpenFile(path)
	if err != nil {
		return nil, err
	}
	d := &Document{path: path, readOnly: opts.ReadOnly}
	if err := d.init(opts, path); err != nil {
		f.Close()
		return nil, err
	}
	d.attachFile(f)
	root := buildTree([]piece{{d.srcIdx, 0, f.Size()}})
	d.hist = newHistory(root, 0)
	d.publish()
	if err := d.journalCheckpoint(); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

// New makes a document of size zero bytes that is not saved yet (SRC-5)
func New(size int64, opts Options) (*Document, error) {
	d := &Document{readOnly: opts.ReadOnly}
	if err := d.init(opts, ""); err != nil {
		return nil, err
	}
	d.hist = newHistory(buildTree([]piece{{bufZeros, 0, size}}), 0)
	d.hist.saved = nil
	d.publish()
	if err := d.journalCheckpoint(); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

// FromBytes makes a document of the bytes that is not saved yet (SRC-5: from the clipboard)
func FromBytes(data []byte, opts Options) (*Document, error) {
	d, err := New(0, opts)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return d, nil
	}
	off, err := d.add.append(data)
	if err != nil {
		d.Close()
		return nil, err
	}
	d.hist.root.root = buildTree([]piece{{bufAdd, off, int64(len(data))}})
	d.publish()
	if err := d.journalCheckpoint(); err != nil {
		d.Close()
		return nil, err
	}
	return d, nil
}

// init makes the add buffer and the journal
func (d *Document) init(opts Options, path string) error {
	d.add = newMemoryAddBuffer()
	if opts.JournalDir != "" {
		j, add, err := createJournal(opts.JournalDir, path)
		if err != nil {
			return err
		}
		d.journal = j
		d.add = add
	}
	d.bufs = []buffer{d.add, zeroBuffer{}}
	return nil
}

// attachFile makes the file the one the document is saved to
func (d *Document) attachFile(f *source.File) {
	d.file = f
	d.src = newSrcBuffer(f, nil, d.add)
	d.srcIdx = len(d.bufs)
	d.bufs = append(d.bufs, d.src)
	if d.journal != nil {
		d.journal.logBuffer(d.srcIdx, true, f.Path(), f.Stat())
	}
}

// Close closes the sources and removes the journal: the edits not saved are dropped
func (d *Document) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	var err error
	if d.file != nil {
		err = d.file.Close()
	}
	for _, f := range d.oldFiles {
		f.Close()
	}
	d.oldFiles = nil
	if d.journal != nil {
		d.journal.remove()
		d.journal = nil
	}
	d.add.close()
	return err
}

// Path is the file of the document; "" - not saved yet
func (d *Document) Path() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.path
}

func (d *Document) ReadOnly() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.readOnly
}

// SetReadOnly forbids or allows editing (EDIT-8)
func (d *Document) SetReadOnly(ro bool) {
	d.mu.Lock()
	d.readOnly = ro
	d.mu.Unlock()
	d.notify()
}

// Modified tells whether the document differs from its file
func (d *Document) Modified() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.hist.cur != d.hist.saved
}

// FileChanged tells whether the file was changed on the disk by someone
// else since it was opened or saved (SRC-7)
func (d *Document) FileChanged() (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.file == nil {
		return false, nil
	}
	st, err := source.StatFile(d.path)
	if err != nil {
		return false, err
	}
	return !st.Same(d.file.Stat()), nil
}

// Snapshot returns the document as it is now
func (d *Document) Snapshot() *Snapshot {
	return d.snap.Load()
}

// Size is the size of the document now
func (d *Document) Size() int64 {
	return d.Snapshot().Size()
}

// ReadAt reads the document as it is now
func (d *Document) ReadAt(p []byte, off int64) (int, error) {
	return d.Snapshot().ReadAt(p, off)
}

// Subscribe calls fn after each change of the document: an edit, undo,
// saving. It is called on the goroutine that made the change. Returns the
// function that unsubscribes.
func (d *Document) Subscribe(fn func()) func() {
	d.listenersMu.Lock()
	defer d.listenersMu.Unlock()
	if d.listeners == nil {
		d.listeners = make(map[int]func())
	}
	id := d.nextListen
	d.nextListen++
	d.listeners[id] = fn
	return func() {
		d.listenersMu.Lock()
		delete(d.listeners, id)
		d.listenersMu.Unlock()
	}
}

func (d *Document) notify() {
	d.listenersMu.Lock()
	fns := make([]func(), 0, len(d.listeners))
	for _, fn := range d.listeners {
		fns = append(fns, fn)
	}
	d.listenersMu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

// publish makes the current state of the history the one readers see
func (d *Document) publish() {
	src := 0
	if d.file != nil {
		src = d.srcIdx
	}
	d.snap.Store(&Snapshot{root: d.hist.cur.root, bufs: d.bufs, src: src})
}

// Size of the snapshot
func (s *Snapshot) Size() int64 { return s.root.bytes() }

// Pieces is how many runs of bytes make the document
func (s *Snapshot) Pieces() int { return s.root.pieces() }

// ReadAt reads the snapshot. A part of the source that cannot be read
// (a bad sector) is returned as zeros with the error.
func (s *Snapshot) ReadAt(p []byte, off int64) (int, error) {
	size := s.Size()
	if off < 0 {
		return 0, ErrOutOfRange
	}
	if off >= size {
		return 0, io.EOF
	}
	end := min(off+int64(len(p)), size)
	var firstErr error
	walk(s.root, 0, off, end, func(start int64, pc piece) bool {
		from, to := max(start, off), min(start+pc.len, end)
		dst := p[from-off : to-off]
		n, err := s.bufs[pc.buf].ReadAt(dst, pc.off+from-start)
		if err != nil && !(err == io.EOF && n == len(dst)) {
			clear(dst[n:])
			if firstErr == nil {
				firstErr = fmt.Errorf("read at %d (buf %d off %d): %w", from+int64(n), pc.buf, pc.off+from-start, err)
			}
		}
		return true
	})
	n := int(end - off)
	if firstErr != nil {
		return n, firstErr
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// TryReadAt reads the snapshot if its bytes need not be waited for. If some
// are not read from the disk yet, it returns false, and onReady is called
// (from another goroutine) as they arrive. Returns how many bytes p got (it
// is shorter at the end of the document).
func (s *Snapshot) TryReadAt(p []byte, off int64, onReady func()) (int, bool) {
	size := s.Size()
	if off < 0 || off >= size {
		return 0, true
	}
	end := min(off+int64(len(p)), size)
	ready := true
	walk(s.root, 0, off, end, func(start int64, pc piece) bool {
		from, to := max(start, off), min(start+pc.len, end)
		if !s.bufs[pc.buf].tryReadAt(p[from-off:to-off], pc.off+from-start, onReady) {
			ready = false
		}
		return true
	})
	return int(end - off), ready
}

// Edited marks the bytes of [off, off+len(mark)) that differ from the file
// as saved, or may: the ones that are not the bytes of the file at the same
// offset
func (s *Snapshot) Edited(off int64, mark []bool) {
	end := min(off+int64(len(mark)), s.Size())
	clear(mark)
	if off >= end {
		return
	}
	walk(s.root, 0, off, end, func(start int64, pc piece) bool {
		if pc.buf == s.src && s.src != 0 && pc.off == start {
			return true
		}
		from, to := max(start, off), min(start+pc.len, end)
		for i := from; i < to; i++ {
			mark[i-off] = true
		}
		return true
	})
}

// zeroBuffer is any number of zero bytes: a new document, a filled range
type zeroBuffer struct{}

func (zeroBuffer) ReadAt(p []byte, off int64) (int, error) {
	clear(p)
	return len(p), nil
}

func (zeroBuffer) tryReadAt(p []byte, off int64, onReady func()) bool {
	clear(p)
	return true
}
