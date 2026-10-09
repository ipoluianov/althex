package doc

import (
	"io"
	"os"
	"sync"
)

// addBuffer keeps the bytes added to the document, appended and never
// changed, so the pieces of every state of the history stay valid. With a
// file (the journal's) the bytes are written there at once and read back
// through the cache of the system: a large paste does not stay in memory,
// and the bytes outlive a crash. Without a file they are kept in memory.
type addBuffer struct {
	mu   sync.RWMutex
	f    *os.File
	size int64

	// In memory: chunks of chunkSize, never moved once full
	chunks [][]byte
}

const addChunkSize = 1 << 20

func newMemoryAddBuffer() *addBuffer {
	return &addBuffer{}
}

// newFileAddBuffer appends to the file, which may already hold size bytes
func newFileAddBuffer(f *os.File, size int64) *addBuffer {
	return &addBuffer{f: f, size: size}
}

func (b *addBuffer) Size() int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.size
}

// append adds the bytes and returns their offset in the buffer
func (b *addBuffer) append(data []byte) (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	off := b.size
	if b.f != nil {
		if _, err := b.f.WriteAt(data, off); err != nil {
			return 0, err
		}
		b.size += int64(len(data))
		return off, nil
	}
	for len(data) > 0 {
		if len(b.chunks) == 0 || len(b.chunks[len(b.chunks)-1]) == addChunkSize {
			b.chunks = append(b.chunks, make([]byte, 0, addChunkSize))
		}
		ch := &b.chunks[len(b.chunks)-1]
		k := min(addChunkSize-len(*ch), len(data))
		*ch = append(*ch, data[:k]...)
		data = data[k:]
		b.size += int64(k)
	}
	return off, nil
}

// appendFrom adds n bytes read from r at off, in parts, and returns their offset
func (b *addBuffer) appendFrom(r io.ReaderAt, off, n int64) (int64, error) {
	start := b.Size()
	buf := make([]byte, min(n, addChunkSize))
	for done := int64(0); done < n; {
		k := min(int64(len(buf)), n-done)
		if _, err := r.ReadAt(buf[:k], off+done); err != nil && err != io.EOF {
			return 0, err
		}
		if _, err := b.append(buf[:k]); err != nil {
			return 0, err
		}
		done += k
	}
	return start, nil
}

func (b *addBuffer) ReadAt(p []byte, off int64) (int, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if off >= b.size {
		return 0, io.EOF
	}
	want := min(int64(len(p)), b.size-off)
	if b.f != nil {
		n, err := b.f.ReadAt(p[:want], off)
		if err == nil && int64(n) < int64(len(p)) {
			err = io.EOF
		}
		return n, err
	}
	n := 0
	for int64(n) < want {
		pos := off + int64(n)
		ch := b.chunks[pos/addChunkSize]
		n += copy(p[n:want], ch[pos%addChunkSize:])
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// sync makes the bytes durable
func (b *addBuffer) sync() error {
	if b.f == nil {
		return nil
	}
	return b.f.Sync()
}

func (b *addBuffer) close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.f == nil {
		b.chunks = nil
		return nil
	}
	err := b.f.Close()
	b.f = nil
	return err
}
