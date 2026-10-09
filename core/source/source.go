// Package source gives the bytes of what a document is opened on: a file,
// a disk, the memory of a process. A source is never written while it is
// open; saving is done by the document (see core/doc).
package source

import (
	"errors"
	"io"
	"os"
	"sync/atomic"
	"time"
)

// Source is a read-only sequence of bytes of a known size
type Source interface {
	io.ReaderAt
	Size() int64
	Close() error
}

// ErrClosed is returned by a read from a closed source
var ErrClosed = errors.New("source is closed")

// Stat is what tells whether a file was changed by someone else. Dev and
// Ino tell a file from another one put in its place (saving makes a new file).
type Stat struct {
	Size     int64
	ModTime  time.Time
	Dev, Ino uint64
}

// Same tells whether it is the same file, not changed
func (s Stat) Same(o Stat) bool {
	return s.Size == o.Size && s.ModTime.Equal(o.ModTime) && s.Dev == o.Dev && s.Ino == o.Ino
}

func statOf(f *os.File) (Stat, error) {
	st, err := f.Stat()
	if err != nil {
		return Stat{}, err
	}
	s := Stat{Size: st.Size(), ModTime: st.ModTime()}
	s.Dev, s.Ino = fileID(f, st)
	return s, nil
}

// File is a source over a file opened for reading only. Other programs may
// keep writing, renaming and deleting it (see openShared).
type File struct {
	f    *os.File
	path string
	size atomic.Int64
	stat Stat
}

// OpenFile opens the file for reading
func OpenFile(path string) (*File, error) {
	f, err := openShared(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if st.IsDir() {
		f.Close()
		return nil, errors.New(path + " is a directory")
	}
	stat, err := statOf(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	c := &File{f: f, path: path, stat: stat}
	c.size.Store(st.Size())
	return c, nil
}

// Path is the file name the source was opened by
func (c *File) Path() string { return c.path }

// Size is the size of the file when it was opened or refreshed
func (c *File) Size() int64 { return c.size.Load() }

// Stat is the size and time of the file when it was opened
func (c *File) Stat() Stat { return c.stat }

// Refresh takes the size and time of the file again, after the document has
// written it in place
func (c *File) Refresh() error {
	if c.f == nil {
		return ErrClosed
	}
	st, err := statOf(c.f)
	if err != nil {
		return err
	}
	c.stat = st
	c.size.Store(st.Size)
	return nil
}

// StatNow returns the size and time of the opened file now (the name may
// lead to another file since)
func (c *File) StatNow() (Stat, error) {
	if c.f == nil {
		return Stat{}, ErrClosed
	}
	return statOf(c.f)
}

// ReadAt reads within the size the file had when it was opened
func (c *File) ReadAt(p []byte, off int64) (int, error) {
	if c.f == nil {
		return 0, ErrClosed
	}
	size := c.size.Load()
	if off >= size {
		return 0, io.EOF
	}
	if rest := size - off; int64(len(p)) > rest {
		n, err := c.f.ReadAt(p[:rest], off)
		if err == nil {
			err = io.EOF
		}
		return n, err
	}
	return c.f.ReadAt(p, off)
}

func (c *File) Close() error {
	if c.f == nil {
		return nil
	}
	err := c.f.Close()
	c.f = nil
	return err
}

// StatFile returns the size, time and identity of the file now
func StatFile(path string) (Stat, error) {
	f, err := openShared(path)
	if err != nil {
		return Stat{}, err
	}
	defer f.Close()
	return statOf(f)
}

// Memory is a source over bytes in memory
type Memory struct {
	data []byte
}

func NewMemory(data []byte) *Memory {
	return &Memory{data: data}
}

func (c *Memory) Size() int64 { return int64(len(c.data)) }

func (c *Memory) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(c.data)) {
		return 0, io.EOF
	}
	n := copy(p, c.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (c *Memory) Close() error { return nil }
