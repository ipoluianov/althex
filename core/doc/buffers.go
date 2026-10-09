package doc

import (
	"io"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/ipoluianov/althex/core/source"
)

// buffer is where the bytes of pieces come from: the add buffer or a source
type buffer interface {
	io.ReaderAt
	// tryReadAt reads without waiting for a disk; false - not yet, onReady
	// is called (from another goroutine) when more of the bytes arrive
	tryReadAt(p []byte, off int64, onReady func()) bool
}

func (b *addBuffer) tryReadAt(p []byte, off int64, onReady func()) bool {
	b.ReadAt(p, off)
	return true
}

// srcBuffer reads a file as it was at one moment: when the document was
// opened or saved in place (a version of the file). Saving in place
// overwrites parts of the file; the bytes they had in each version are kept
// in the add buffer first (spans), as older states of the history refer to
// them. The versions of a file share its cache.
type srcBuffer struct {
	file *source.File
	// cache is replaced only when a save had to reopen the file (saveRewrite)
	cache atomic.Pointer[source.Cache]
	sz    int64 // of the file in this version
	add   *addBuffer

	mu    sync.RWMutex
	spans []span // sorted, not overlapping
}

// span is a range of the source kept in the add buffer
type span struct {
	off, len int64
	addOff   int64
}

func newSrcBuffer(f *source.File, cache *source.Cache, add *addBuffer) *srcBuffer {
	if cache == nil {
		cache = source.NewCache(f, source.DefaultCachePages)
	}
	b := &srcBuffer{file: f, sz: f.Size(), add: add}
	b.cache.Store(cache)
	return b
}

func (b *srcBuffer) size() int64 { return b.sz }

// spanAt returns the index of the first span that ends after off
func spanAt(spans []span, off int64) int {
	i, _ := slices.BinarySearchFunc(spans, off, func(s span, off int64) int {
		if s.off+s.len <= off {
			return -1
		}
		return 1
	})
	return i
}

// parts calls fn for the runs of [off, off+n): from the add buffer (kept) or the file
func (b *srcBuffer) parts(off, n int64, fn func(off, n int64, kept bool, addOff int64) bool) {
	b.mu.RLock()
	spans := b.spans
	b.mu.RUnlock()
	end := off + n
	i := spanAt(spans, off)
	for off < end {
		if i < len(spans) && spans[i].off <= off {
			s := spans[i]
			k := min(end, s.off+s.len) - off
			if !fn(off, k, true, s.addOff+off-s.off) {
				return
			}
			off += k
			i++
			continue
		}
		next := end
		if i < len(spans) {
			next = min(end, spans[i].off)
		}
		if !fn(off, next-off, false, 0) {
			return
		}
		off = next
	}
}

func (b *srcBuffer) ReadAt(p []byte, off int64) (int, error) {
	var err error
	n := 0
	b.parts(off, int64(len(p)), func(at, k int64, kept bool, addOff int64) bool {
		dst := p[at-off : at-off+k]
		var got int
		if kept {
			got, err = b.add.ReadAt(dst, addOff)
		} else {
			got, err = b.cache.Load().ReadAt(dst, at)
		}
		n += got
		if err == io.EOF && int64(got) == k {
			err = nil
		}
		return err == nil
	})
	return n, err
}

func (b *srcBuffer) tryReadAt(p []byte, off int64, onReady func()) bool {
	ready := true
	b.parts(off, int64(len(p)), func(at, k int64, kept bool, addOff int64) bool {
		dst := p[at-off : at-off+k]
		if kept {
			b.add.ReadAt(dst, addOff)
		} else if !b.cache.Load().TryReadAt(dst, at, onReady) {
			ready = false
		}
		return true
	})
	return ready
}

// unkept returns the ranges of [off, off+n) in this version that are read
// from the file
func (b *srcBuffer) unkept(off, n int64) []rng {
	n = min(n, b.sz-off)
	if n <= 0 {
		return nil
	}
	var rs []rng
	b.parts(off, n, func(at, k int64, kept bool, _ int64) bool {
		if !kept {
			rs = append(rs, rng{at, k})
		}
		return true
	})
	return rs
}

// addSpans adds spans that do not overlap the ones there are
func (b *srcBuffer) addSpans(added []span) {
	if len(added) == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	spans := append(slices.Clone(b.spans), added...)
	slices.SortFunc(spans, func(a, b span) int {
		switch {
		case a.off < b.off:
			return -1
		case a.off > b.off:
			return 1
		}
		return 0
	})
	b.spans = spans
}

// versions returns the buffers of the file of the document with their indexes
func (d *Document) versions() map[int]*srcBuffer {
	vs := make(map[int]*srcBuffer)
	for i, b := range d.bufs {
		if sb, ok := b.(*srcBuffer); ok && sameFile(sb.file, d.file) {
			vs[i] = sb
		}
	}
	return vs
}

// sameFile tells whether the handles are of one file (after recovery each
// version has a handle of its own)
func sameFile(a, b *source.File) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	sa, sb := a.Stat(), b.Stat()
	return sa.Ino != 0 && sa.Dev == sb.Dev && sa.Ino == sb.Ino
}

// unkeptUnion returns the ranges of [off, off+n) some version still reads
// from the file, merged and in order
func unkeptUnion(vs map[int]*srcBuffer, off, n int64) []rng {
	var all []rng
	for _, v := range vs {
		all = append(all, v.unkept(off, n)...)
	}
	slices.SortFunc(all, func(a, b rng) int {
		switch {
		case a.off < b.off:
			return -1
		case a.off > b.off:
			return 1
		}
		return 0
	})
	var union []rng
	for _, r := range all {
		if k := len(union) - 1; k >= 0 && r.off <= union[k].off+union[k].len {
			union[k].len = max(union[k].len, r.off+r.len-union[k].off)
			continue
		}
		union = append(union, r)
	}
	return union
}

// keepFile copies the bytes of [off, off+n) that versions of the file still
// read from it to the add buffer, once for all of them, before the file is
// overwritten there. Returns the new spans of each version.
func (d *Document) keepFile(off, n int64) (map[int][]span, error) {
	vs := d.versions()
	type copied struct {
		r      rng
		addOff int64
	}
	var copies []copied
	cache := d.src.cache.Load()
	for _, u := range unkeptUnion(vs, off, n) {
		addOff, err := d.add.appendFrom(cache, u.off, u.len)
		if err != nil {
			return nil, err
		}
		copies = append(copies, copied{u, addOff})
	}
	added := make(map[int][]span)
	for idx, v := range vs {
		var spans []span
		for _, r := range v.unkept(off, n) {
			for _, c := range copies {
				if r.off >= c.r.off && r.off+r.len <= c.r.off+c.r.len {
					spans = append(spans, span{r.off, r.len, c.addOff + r.off - c.r.off})
					break
				}
			}
		}
		v.addSpans(spans)
		added[idx] = spans
	}
	return added, nil
}

// toKeepFile is how many bytes keepFile would copy
func (d *Document) toKeepFile(off, n int64) int64 {
	total := int64(0)
	for _, u := range unkeptUnion(d.versions(), off, n) {
		total += u.len
	}
	return total
}
