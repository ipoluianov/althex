package source

import (
	"bytes"
	"errors"
	"io"
	"math/rand/v2"
	"sync"
	"testing"
)

func TestCacheReads(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 1))
	data := make([]byte, 5*PageSize+123)
	for i := range data {
		data[i] = byte(r.UintN(256))
	}
	c := NewCache(NewMemory(data), 4)
	for range 2000 {
		off := r.IntN(len(data))
		n := 1 + r.IntN(3*PageSize)
		buf := make([]byte, n)
		got, err := c.ReadAt(buf, int64(off))
		want := min(n, len(data)-off)
		if got != want || !bytes.Equal(buf[:got], data[off:off+got]) {
			t.Fatalf("read %d at %d: got %d bytes", n, off, got)
		}
		if want < n && err != io.EOF {
			t.Fatalf("read past the end: %v", err)
		}
	}
}

func TestCacheTryRead(t *testing.T) {
	data := bytes.Repeat([]byte{7}, 3*PageSize)
	c := NewCache(NewMemory(data), 16)
	buf := make([]byte, 2*PageSize)
	var wg sync.WaitGroup
	wg.Add(3) // the read touches three pages
	if c.TryReadAt(buf, 100, wg.Done) {
		t.Fatal("pages not read yet are ready")
	}
	wg.Wait()
	if !c.TryReadAt(buf, 100, nil) {
		t.Fatal("the pages are not ready after arriving")
	}
	if buf[0] != 7 || buf[len(buf)-1] != 7 {
		t.Fatal("wrong bytes")
	}
}

// failing fails to read a range, like a bad sector
type failing struct{ Memory }

func (f *failing) ReadAt(p []byte, off int64) (int, error) {
	if off < 2*PageSize && off+int64(len(p)) > PageSize {
		return 0, errors.New("bad sector")
	}
	return f.Memory.ReadAt(p, off)
}

func TestCacheBadSector(t *testing.T) {
	c := NewCache(&failing{*NewMemory(make([]byte, 3*PageSize))}, 16)
	buf := make([]byte, 10)
	if _, err := c.ReadAt(buf, 0); err != nil {
		t.Fatalf("a good page: %v", err)
	}
	if _, err := c.ReadAt(buf, PageSize+5); err == nil {
		t.Fatal("a bad page reads")
	}
	if c.Readable(PageSize + 5) {
		t.Fatal("a bad page is readable")
	}
	if _, err := c.ReadAt(buf, 2*PageSize); err != nil {
		t.Fatalf("the page after a bad one: %v", err)
	}
}
