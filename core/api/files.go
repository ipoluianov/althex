package api

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/ipoluianov/althex/core/doc"
	"github.com/ipoluianov/althex/rpc"
)

// Files is the workspace of a command without a window: the files given
// to it. There is no one to ask, so what may change anything is set by the
// one who starts it.
type Files struct {
	// AllowOpen lets the clients open other files
	AllowOpen bool
	// AllowWrite lets them edit and save; else the documents are read-only
	AllowWrite bool

	mu   sync.Mutex // the edits of the documents come one at a time
	docs []*fileDoc
	next int
}

type fileDoc struct {
	id string
	d  *doc.Document
}

// Add opens a file of the command line
func (f *Files) Add(path string) (DocInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.add(path)
}

func (f *Files) add(path string) (DocInfo, error) {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	for _, fd := range f.docs {
		if fd.d.Path() == path {
			return f.info(fd), nil
		}
	}
	d, err := doc.Open(path, doc.Options{ReadOnly: !f.AllowWrite})
	if err != nil {
		return DocInfo{}, err
	}
	f.next++
	fd := &fileDoc{id: fmt.Sprint(f.next), d: d}
	f.docs = append(f.docs, fd)
	return f.info(fd), nil
}

// Close closes the documents; edits not saved are lost
func (f *Files) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, fd := range f.docs {
		fd.d.Close()
	}
	f.docs = nil
}

func (f *Files) info(fd *fileDoc) DocInfo {
	return DocInfo{ID: fd.id, Name: fd.d.Name(), Path: fd.d.Path(), Size: fd.d.Size(),
		Modified: fd.d.Modified(), ReadOnly: fd.d.ReadOnly(), Current: fd == f.docs[0]}
}

func (f *Files) find(id string) (*fileDoc, error) {
	for _, fd := range f.docs {
		if fd.id == id {
			return fd, nil
		}
	}
	return nil, fmt.Errorf("%w: no document %q", ErrNotFound, id)
}

func (f *Files) Documents() []DocInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]DocInfo, 0, len(f.docs))
	for _, fd := range f.docs {
		out = append(out, f.info(fd))
	}
	return out
}

func (f *Files) Snapshot(id string) (*doc.Snapshot, DocInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fd, err := f.find(id)
	if err != nil {
		return nil, DocInfo{}, err
	}
	return fd.d.Snapshot(), f.info(fd), nil
}

func (f *Files) Open(ctx context.Context, client rpc.Client, path string) (DocInfo, error) {
	if !f.AllowOpen {
		return DocInfo{}, fmt.Errorf("%w: opening files (start with --allow-open)", ErrForbidden)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.add(path)
}

func (f *Files) Apply(ctx context.Context, client rpc.Client, p Proposal) (DocInfo, error) {
	if !f.AllowWrite {
		return DocInfo{}, fmt.Errorf("%w: editing (start with --allow-write)", ErrForbidden)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	fd, err := f.find(p.Doc)
	if err != nil {
		return DocInfo{}, err
	}
	if err := applyEdits(fd.d, p, "external: "+client.Name); err != nil {
		return DocInfo{}, err
	}
	return f.info(fd), nil
}

func (f *Files) Save(ctx context.Context, client rpc.Client, id string) (DocInfo, error) {
	if !f.AllowWrite {
		return DocInfo{}, fmt.Errorf("%w: saving (start with --allow-write)", ErrForbidden)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	fd, err := f.find(id)
	if err != nil {
		return DocInfo{}, err
	}
	if err := fd.d.Save(doc.SaveOptions{}); err != nil {
		return DocInfo{}, err
	}
	return f.info(fd), nil
}

func (f *Files) Show(id string, off, n int64) bool { return false }

// ApplyEdits makes the edits of a proposal one step of the history (EXT-8);
// when one fails the ones before it are undone
func ApplyEdits(d *doc.Document, p Proposal, kind string) error { return applyEdits(d, p, kind) }

func applyEdits(d *doc.Document, p Proposal, kind string) error {
	d.BeginGroup(kind)
	var err error
	done := 0
	opt := doc.EditOptions{Kind: kind}
	for _, e := range p.Edits {
		switch {
		case e.Overwrite != nil:
			err = d.Overwrite(e.Offset, e.Overwrite, opt)
		case e.Insert != nil:
			err = d.Insert(e.Offset, e.Insert, opt)
		default:
			err = d.Delete(e.Offset, e.Delete, opt)
		}
		if err != nil {
			break
		}
		done++
	}
	d.EndGroup()
	if err != nil && done > 0 {
		d.Undo() // the group: none of the proposal stays
	}
	return err
}
