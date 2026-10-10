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
	name     string // the name of a document over a source without a file
	readOnly bool
	// writeDenied is why the file could not be opened for writing, so the
	// document was opened read-only (SRC-2); nil - it could
	writeDenied error

	add  *addBuffer
	bufs []buffer
	// src is the buffer of the file the document is saved to, srcIdx its
	// index in bufs; nil when there is no such file
	src    *srcBuffer
	srcIdx int
	file   *source.File
	// Sources replaced by saving, kept open while older states use them
	oldFiles []*source.File
	// owned is the source of OpenSource, closed with the document
	owned source.Source

	snap atomic.Pointer[Snapshot]
	hist *history

	// The edit group (BeginGroup): its kind and the state it makes
	groupKind string
	groupNode *HistNode
	groupDeep int
	// groupChanged: the group made edits the subscribers were not told of
	groupChanged bool

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
	// A file that cannot be written (no permission, locked by another
	// program) is opened read-only rather than failing when saved (SRC-2)
	if !opts.ReadOnly && !f.Device() {
		if err := source.CanWrite(path); err != nil {
			d.readOnly, d.writeDenied = true, err
		}
	}
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

// OpenSource opens a document over a source that is not a file of its own:
// a disk inside an image file, a partition, a file of a file system. It
// starts read-only and has no journal; it is saved as a copy under a name.
func OpenSource(src source.Source, name string) (*Document, error) {
	d := &Document{name: name, readOnly: true}
	if err := d.init(Options{}, ""); err != nil {
		return nil, err
	}
	d.src = newSourceBuffer(src, d.add)
	d.srcIdx = len(d.bufs)
	d.bufs = append(d.bufs, d.src)
	d.owned = src
	d.hist = newHistory(buildTree([]piece{{d.srcIdx, 0, src.Size()}}), 0)
	d.publish()
	return d, nil
}

// Name is the name of the document: of its file, or the one it was opened under
func (d *Document) Name() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.path != "" {
		return filepath.Base(d.path)
	}
	return d.name
}

// IsDevice tells whether the document is a disk or a partition of the computer
func (d *Document) IsDevice() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.file != nil && d.file.Device()
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
	if d.owned != nil {
		d.owned.Close()
		d.owned = nil
	}
	if d.journal != nil {
		d.journal.remove()
		d.journal = nil
	}
	d.add.close()
	return err
}

// JournalDir is the directory of the journal of the document; "" - none
func (d *Document) JournalDir() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.journal == nil {
		return ""
	}
	return d.journal.dir
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

// WriteDenied is why the file could not be opened for writing when the
// document was opened, which made it read-only (SRC-2); nil - it could
func (d *Document) WriteDenied() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.writeDenied
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
	// The bytes of a file or of a source (a disk, a snapshot compared) as
	// they are there are not edits
	src := 0
	if d.file != nil || d.owned != nil {
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

// Unreadable marks the bytes of [off, off+len(mark)) that could not be read
// from the disk (bad sectors: shown as zeros); false - none of them
func (s *Snapshot) Unreadable(off int64, mark []bool) bool {
	end := min(off+int64(len(mark)), s.Size())
	any := false
	walk(s.root, 0, off, end, func(start int64, pc piece) bool {
		sb, ok := s.bufs[pc.buf].(*srcBuffer)
		if !ok {
			return true
		}
		from, to := max(start, off), min(start+pc.len, end)
		cache := sb.cache.Load()
		if !cache.HasBadPages() {
			return true
		}
		// Page by page: a page is read or failed as a whole
		for p := from; p < to; {
			srcOff := pc.off + p - start
			next := min(to, p+source.PageSize-srcOff%source.PageSize)
			if !cache.Readable(srcOff) {
				for i := p; i < next; i++ {
					mark[i-off] = true
				}
				any = true
			}
			p = next
		}
		return true
	})
	return any
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
