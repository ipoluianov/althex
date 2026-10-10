package doc

import (
	"sort"

	"github.com/ipoluianov/althex/core/source"
)

// Range is a run of bytes of a document
type Range struct {
	Off, Len int64
}

// EditedRanges returns the runs of the snapshot that may differ from the
// file as saved (as Edited marks them), merged and in order; at most limit
// of them, the last one then reaching to the end of the last run (an
// overview, the minimap, needs no more). It walks the pieces, not the bytes.
func (s *Snapshot) EditedRanges(limit int) []Range {
	var out []Range
	walk(s.root, 0, 0, s.Size(), func(start int64, pc piece) bool {
		if pc.buf == s.src && s.src != 0 && pc.off == start {
			return true
		}
		if n := len(out); n > 0 && out[n-1].Off+out[n-1].Len == start {
			out[n-1].Len += pc.len
			return true
		}
		if len(out) >= limit && limit > 0 {
			last := &out[len(out)-1]
			last.Len = start + pc.len - last.Off
			return true
		}
		out = append(out, Range{start, pc.len})
		return true
	})
	return out
}

// UnreadableRanges returns the runs of the snapshot that could not be read
// from the disk (bad sectors), as far as the page cache still knows them, in
// order
func (s *Snapshot) UnreadableRanges() []Range {
	var out []Range
	bad := make(map[*source.Cache][]int64)
	walk(s.root, 0, 0, s.Size(), func(start int64, pc piece) bool {
		sb, ok := s.bufs[pc.buf].(*srcBuffer)
		if !ok {
			return true
		}
		cache := sb.cache.Load()
		if cache == nil || !cache.HasBadPages() {
			return true
		}
		pages, seen := bad[cache]
		if !seen {
			pages = cache.BadPages()
			bad[cache] = pages
		}
		// The bad pages inside the piece: [pc.off, pc.off+pc.len) of the source
		i := sort.Search(len(pages), func(i int) bool { return (pages[i]+1)*source.PageSize > pc.off })
		for ; i < len(pages) && pages[i]*source.PageSize < pc.off+pc.len; i++ {
			from := max(pages[i]*source.PageSize, pc.off)
			to := min((pages[i]+1)*source.PageSize, pc.off+pc.len)
			off := start + from - pc.off
			if n := len(out); n > 0 && out[n-1].Off+out[n-1].Len == off {
				out[n-1].Len += to - from
				continue
			}
			out = append(out, Range{off, to - from})
		}
		return true
	})
	return out
}
