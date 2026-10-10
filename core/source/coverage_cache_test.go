package source

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
)

// switchable fails to read the second page while broken is set, and counts reads
type switchable struct {
	Memory
	broken atomic.Bool
	reads  atomic.Int32
	closed atomic.Bool
}

func (s *switchable) ReadAt(p []byte, off int64) (int, error) {
	s.reads.Add(1)
	if s.broken.Load() && off < 2*PageSize && off+int64(len(p)) > PageSize {
		return 0, errors.New("bad sector")
	}
	return s.Memory.ReadAt(p, off)
}

func (s *switchable) Close() error { s.closed.Store(true); return nil }

func pattern(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i * 7)
	}
	return b
}

func TestCacheAccessors(t *testing.T) {
	src := &switchable{Memory: *NewMemory(make([]byte, 100))}
	c := NewCache(src, 0) // raised to the minimum
	if c.maxPages != 4 {
		t.Fatalf("max pages %d", c.maxPages)
	}
	if c.Source() != src || c.Size() != 100 {
		t.Fatal("source or size")
	}
	if c.HasBadPages() {
		t.Fatal("bad pages before any read")
	}
	if err := c.Close(); err != nil || !src.closed.Load() {
		t.Fatal("close does not reach the source")
	}
}

func TestCacheInvalidateBadPage(t *testing.T) {
	data := pattern(3 * PageSize)
	src := &switchable{Memory: *NewMemory(data)}
	src.broken.Store(true)
	c := NewCache(src, 16)
	buf := make([]byte, 16)
	if _, err := c.ReadAt(buf, PageSize+1); err == nil {
		t.Fatal("a bad page reads")
	}
	if !c.HasBadPages() || c.Readable(PageSize+1) || !c.Readable(0) {
		t.Fatal("the bad page is not marked")
	}
	// The error is kept: no second read of the bad page
	reads := src.reads.Load()
	if _, err := c.ReadAt(buf, PageSize+2); err == nil {
		t.Fatal("a bad page reads the second time")
	}
	if src.reads.Load() != reads {
		t.Fatal("a bad page is read again")
	}
	// TryReadAt shows the bad bytes as zeros, the good ones as they are
	wide := bytes.Repeat([]byte{0xff}, 2*PageSize)
	for !c.TryReadAt(wide, PageSize/2, nil) {
		c.ReadAt(make([]byte, 1), PageSize/2) // load the first page
	}
	if !bytes.Equal(wide[:PageSize/2], data[PageSize/2:PageSize]) {
		t.Fatal("good bytes before the bad page")
	}
	if !bytes.Equal(wide[PageSize/2:PageSize/2+PageSize], make([]byte, PageSize)) {
		t.Fatal("bad bytes are not zeros")
	}
	if !bytes.Equal(wide[PageSize/2+PageSize:], data[2*PageSize:2*PageSize+PageSize/2]) {
		t.Fatal("good bytes after the bad page")
	}

	// Repaired and invalidated: read again
	src.broken.Store(false)
	c.Invalidate(PageSize+10, 1)
	if !c.Readable(PageSize + 1) {
		t.Fatal("an invalidated page is not readable")
	}
	if n, err := c.ReadAt(buf, PageSize+1); err != nil || n != len(buf) || !bytes.Equal(buf, data[PageSize+1:PageSize+17]) {
		t.Fatalf("after invalidate: %d %v", n, err)
	}
	if !c.HasBadPages() {
		t.Fatal("HasBadPages is cleared; it tells that a page has ever failed")
	}
}

func TestCacheInvalidateRange(t *testing.T) {
	data := pattern(4 * PageSize)
	src := &switchable{Memory: *NewMemory(data)}
	c := NewCache(src, 16)
	all := make([]byte, len(data))
	if _, err := c.ReadAt(all, 0); err != nil {
		t.Fatal(err)
	}
	if len(c.pages) != 4 {
		t.Fatalf("%d pages cached", len(c.pages))
	}
	c.Invalidate(0, 0)
	c.Invalidate(5, -1)
	if len(c.pages) != 4 {
		t.Fatal("an empty range drops pages")
	}
	// From the last byte of page 0 to the first of page 2
	c.Invalidate(PageSize-1, PageSize+2)
	for idx, want := range []bool{false, false, false, true} {
		if _, ok := c.pages[int64(idx)]; ok != want {
			t.Fatalf("page %d cached: %v", idx, ok)
		}
	}
	if c.lru.Len() != len(c.pages) {
		t.Fatal("the lru and the map differ")
	}
	// The data changed under the cache: an invalidated page shows it
	data[0] = ^data[0]
	data[3*PageSize] = ^data[3*PageSize]
	b := make([]byte, 1)
	c.ReadAt(b, 0)
	if b[0] != data[0] {
		t.Fatal("invalidated page is stale")
	}
	c.ReadAt(b, 3*PageSize)
	if b[0] == data[3*PageSize] {
		t.Fatal("a kept page is read again")
	}
	c.InvalidateAll()
	if len(c.pages) != 0 || c.lru.Len() != 0 {
		t.Fatal("pages kept after InvalidateAll")
	}
	c.ReadAt(b, 3*PageSize)
	if b[0] != data[3*PageSize] {
		t.Fatal("stale after InvalidateAll")
	}
}

func TestCacheEviction(t *testing.T) {
	data := pattern(10 * PageSize)
	src := &switchable{Memory: *NewMemory(data)}
	c := NewCache(src, 4)
	b := make([]byte, 1)
	for i := range 10 {
		c.ReadAt(b, int64(i)*PageSize)
	}
	if len(c.pages) != 4 || c.lru.Len() != 4 {
		t.Fatalf("%d pages kept", len(c.pages))
	}
	for idx := int64(6); idx < 10; idx++ {
		if _, ok := c.pages[idx]; !ok {
			t.Fatalf("recent page %d evicted", idx)
		}
	}
	// A touched page is kept over newer ones
	c.ReadAt(b, 6*PageSize)
	c.ReadAt(b, 0)
	if _, ok := c.pages[6]; !ok {
		t.Fatal("a touched page evicted")
	}
	if _, ok := c.pages[7]; ok {
		t.Fatal("the least recent page kept")
	}
}

// shortSource says it is larger than the bytes it has, like a file made
// shorter after it was opened
type shortSource struct {
	Memory
	size int64
}

func (s *shortSource) Size() int64 { return s.size }

func TestCacheShrunkSource(t *testing.T) {
	src := &shortSource{Memory: *NewMemory(pattern(100)), size: PageSize + 50}
	c := NewCache(src, 8)
	buf := make([]byte, 200)
	n, err := c.ReadAt(buf, 0)
	if n != 100 || err != io.ErrUnexpectedEOF {
		t.Fatalf("read of a shrunk source: %d %v", n, err)
	}
	if c.HasBadPages() {
		t.Fatal("a short page is a bad one")
	}
	// TryReadAt shows the missing bytes as zeros
	for i := range buf {
		buf[i] = 0xff
	}
	if !c.TryReadAt(buf, 0, nil) {
		t.Fatal("page 0 is cached")
	}
	if !bytes.Equal(buf[:100], pattern(100)) || !bytes.Equal(buf[100:], make([]byte, 100)) {
		t.Fatal("missing bytes are not zeros")
	}
	// Page 1 has no bytes at all
	if n, err := c.ReadAt(buf[:10], PageSize); n != 0 || err != io.ErrUnexpectedEOF {
		t.Fatalf("read of a missing page: %d %v", n, err)
	}
}

func TestCacheReadEdges(t *testing.T) {
	c := NewCache(NewMemory(pattern(10)), 4)
	if n, err := c.ReadAt(make([]byte, 4), 10); n != 0 || err != io.EOF {
		t.Fatalf("at the end: %d %v", n, err)
	}
	if n, err := c.ReadAt(make([]byte, 4), 8); n != 2 || err != io.EOF {
		t.Fatalf("across the end: %d %v", n, err)
	}
	// Nothing to read is always ready, the callback is not called
	called := false
	if !c.TryReadAt(make([]byte, 4), 10, func() { called = true }) {
		t.Fatal("a read past the end is not ready")
	}
	if !c.TryReadAt(nil, 0, func() { called = true }) || called {
		t.Fatal("an empty read")
	}
	// A read across the end fills only the bytes of the source
	buf := bytes.Repeat([]byte{0xee}, 6)
	c.ReadAt(make([]byte, 1), 0)
	if !c.TryReadAt(buf, 6, nil) {
		t.Fatal("not ready")
	}
	if !bytes.Equal(buf[:4], pattern(10)[6:]) || !bytes.Equal(buf[4:], []byte{0xee, 0xee}) {
		t.Fatalf("buf %x", buf)
	}
}

// gated blocks reads until released, so loads can be overlapped
type gated struct {
	Memory
	gate  chan struct{}
	reads atomic.Int32
}

func (g *gated) ReadAt(p []byte, off int64) (int, error) {
	g.reads.Add(1)
	<-g.gate
	return g.Memory.ReadAt(p, off)
}

func TestCacheConcurrentLoad(t *testing.T) {
	data := pattern(2 * PageSize)
	src := &gated{Memory: *NewMemory(data), gate: make(chan struct{})}
	c := NewCache(src, 8)

	var ready sync.WaitGroup
	ready.Add(2) // two callbacks on one page being loaded
	if c.TryReadAt(make([]byte, 10), 0, ready.Done) {
		t.Fatal("ready before loading")
	}
	if c.TryReadAt(make([]byte, 10), 5, ready.Done) {
		t.Fatal("ready while loading")
	}
	if c.TryReadAt(make([]byte, 10), 5, nil) {
		t.Fatal("ready while loading, no callback")
	}

	// Readers of the page being loaded wait for that load
	var wg sync.WaitGroup
	results := make([][]byte, 8)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b := make([]byte, 32)
			if _, err := c.ReadAt(b, int64(i)); err == nil {
				results[i] = b
			}
		}()
	}
	close(src.gate)
	wg.Wait()
	ready.Wait()
	for i, b := range results {
		if !bytes.Equal(b, data[i:i+32]) {
			t.Fatalf("reader %d got %x", i, b)
		}
	}
	if !c.TryReadAt(make([]byte, 10), 0, nil) {
		t.Fatal("not ready after loading")
	}
	if r := src.reads.Load(); r < 1 || r > 1+int32(len(results)) {
		t.Fatalf("%d reads", r)
	}
}

func TestCacheConcurrentMixed(t *testing.T) {
	data := pattern(6*PageSize + 17)
	c := NewCache(NewMemory(data), 4)
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b := make([]byte, PageSize+3)
			for i := range 200 {
				off := int64((g*7919 + i*104729) % len(data))
				switch i % 4 {
				case 0:
					n, _ := c.ReadAt(b, off)
					if !bytes.Equal(b[:n], data[off:off+int64(n)]) {
						t.Error("wrong bytes")
						return
					}
				case 1:
					if c.TryReadAt(b, off, nil) {
						n := min(int64(len(b)), int64(len(data))-off)
						if !bytes.Equal(b[:n], data[off:off+n]) {
							t.Error("wrong bytes from TryReadAt")
							return
						}
					}
				case 2:
					c.Invalidate(off, PageSize)
				case 3:
					c.Readable(off)
				}
			}
		}()
	}
	wg.Wait()
}
