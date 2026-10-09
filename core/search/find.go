package search

import (
	"bytes"
	"context"
	"io"
	"slices"
)

// Hit is a match: Len bytes at Off
type Hit struct {
	Off int64
	Len int
}

// Options of FindAll
type Options struct {
	From, To  int64 // the range to search; To 0 - the end (size)
	Align     int   // only offsets that are multiples of it (0/1 - any)
	MaxHits   int   // stop after so many; 0 - no limit (FIND-7)
	Workers   int   // 0 - runtime.NumCPU()
	ChunkSize int   // 0 - 4 MB
}

// matcher is a pattern made ready for scanning
type matcher struct {
	sets []byteSet
	n    int

	// anchor is the longest run of fixed bytes, at anchorPos in the
	// pattern; bytes.Index finds it fast (SIMD), the rest is checked
	anchor    []byte
	anchorPos int
	exact     bool // the anchor is the whole pattern

	// Without fixed bytes: the position with the fewest allowed values;
	// up to 3 values are found with bytes.IndexByte, more with the table
	firstPos  int
	firstVals []byte
	firstSet  byteSet
}

func newMatcher(p Pattern) *matcher {
	m := &matcher{sets: p.sets, n: len(p.sets)}
	bestLen, run := 0, 0
	for i := range p.sets {
		if _, ok := p.sets[i].single(); ok {
			run++
			if run > bestLen {
				bestLen, m.anchorPos = run, i-run+1
			}
		} else {
			run = 0
		}
	}
	if bestLen > 0 {
		m.anchor = make([]byte, bestLen)
		for i := range m.anchor {
			m.anchor[i], _ = p.sets[m.anchorPos+i].single()
		}
		m.exact = bestLen == m.n
		return m
	}
	best := 257
	for i := range p.sets {
		if c := p.sets[i].count(); c < best {
			best, m.firstPos = c, i
		}
	}
	m.firstSet = p.sets[m.firstPos]
	if best <= 3 {
		m.firstVals = m.firstSet.values()
	}
	return m
}

// verify checks the positions outside the anchor at start i
func (m *matcher) verify(buf []byte, i int) bool {
	for k := 0; k < m.n; k++ {
		if !m.sets[k].has(buf[i+k]) {
			return false
		}
	}
	return true
}

// find calls fn with every start i < limit of a match that fits in buf, in
// increasing order; fn returning false stops
func (m *matcher) find(buf []byte, limit int, fn func(i int) bool) {
	maxStart := min(limit, len(buf)-m.n+1)
	if maxStart <= 0 {
		return
	}
	if m.anchor != nil {
		k := len(m.anchor)
		region := buf[m.anchorPos : maxStart-1+m.anchorPos+k]
		for p := 0; p+k <= len(region); {
			var j int
			if k == 1 {
				j = bytes.IndexByte(region[p:], m.anchor[0])
			} else {
				j = bytes.Index(region[p:], m.anchor)
			}
			if j < 0 {
				return
			}
			i := p + j // the start in buf, as region begins at anchorPos
			if (m.exact || m.verify(buf, i)) && !fn(i) {
				return
			}
			p = i + 1
		}
		return
	}
	region := buf[m.firstPos : maxStart+m.firstPos]
	if m.firstVals != nil {
		// The next place of every value; the nearest one is the candidate
		next := make([]int, len(m.firstVals))
		for v := range next {
			next[v] = -1
		}
		for p := 0; p < len(region); {
			cand := len(region)
			for v, b := range m.firstVals {
				if next[v] < p {
					j := bytes.IndexByte(region[p:], b)
					if j < 0 {
						next[v] = len(region)
					} else {
						next[v] = p + j
					}
				}
				cand = min(cand, next[v])
			}
			if cand >= len(region) {
				return
			}
			if m.verify(buf, cand) && !fn(cand) {
				return
			}
			p = cand + 1
		}
		return
	}
	for i, b := range region {
		if m.firstSet.has(b) && m.verify(buf, i) && !fn(i) {
			return
		}
	}
}

// FindAll calls onHit for every match in increasing order of offset, from
// one goroutine; onHit returning false stops (FIND-1..3, FIND-7). Matches
// that overlap are all reported: "AA" in "AAA" is at 0 and 1. The chunks
// are searched in parallel, each one with pattern length - 1 bytes of the
// next one, so a match on a border is found once, by the chunk it starts in.
// progress, when not nil, gets the bytes done from From.
func FindAll(ctx context.Context, r io.ReaderAt, size int64, p Pattern, opt Options, onHit func(Hit) bool, progress func(done int64)) error {
	n := p.Len()
	if n == 0 {
		return ErrEmptyPattern
	}
	from, to := span(opt.From, opt.To, size)
	if to-from < int64(n) {
		return ctx.Err()
	}
	m := newMatcher(p)
	cs := chunkSize(opt.ChunkSize)
	end := to - int64(n) + 1 // starts are in [from, end)
	chunks := (end - from + cs - 1) / cs
	align := int64(max(opt.Align, 1))
	maxHits := opt.MaxHits

	work := func(ctx context.Context, c int64) ([]uint32, error) {
		s := from + c*cs
		e := min(s+cs, end)
		bp := getBuf(int(e - s + int64(n) - 1))
		defer putBuf(bp)
		buf := *bp
		if err := readFull(r, buf, s); err != nil {
			return nil, err
		}
		var hits []uint32
		m.find(buf, int(e-s), func(i int) bool {
			if (s+int64(i))%align == 0 {
				hits = append(hits, uint32(i))
			}
			// No chunk gives more than all of them may be
			return maxHits <= 0 || len(hits) < maxHits
		})
		return hits, nil
	}
	count := 0
	emit := func(c int64, hits []uint32) bool {
		s := from + c*cs
		for _, h := range hits {
			if !onHit(Hit{Off: s + int64(h), Len: n}) {
				return false
			}
			count++
			if maxHits > 0 && count >= maxHits {
				return false
			}
		}
		if progress != nil {
			if c == chunks-1 {
				progress(to - from)
			} else {
				progress(min(s+cs, end) - from)
			}
		}
		return true
	}
	return ordered(ctx, opt.Workers, chunks, work, emit)
}

// nextChunk is the chunk size of FindNext: smaller than for FindAll, as the
// match is often near and the workers read ahead
const nextChunk = 1 << 20

// FindNext returns the first match at or after from, or when backward the
// last one that starts before from; ok false - none. Wrapping around is the
// caller's business. Backward goes over the chunks from the end, so a match
// near from is found without reading all before it.
func FindNext(ctx context.Context, r io.ReaderAt, size int64, p Pattern, from int64, backward bool) (Hit, bool, error) {
	n := p.Len()
	if n == 0 {
		return Hit{}, false, ErrEmptyPattern
	}
	if !backward {
		var hit Hit
		found := false
		err := FindAll(ctx, r, size, p, Options{From: from, MaxHits: 1, ChunkSize: nextChunk}, func(h Hit) bool {
			hit, found = h, true
			return false
		}, nil)
		return hit, found, err
	}
	end := min(from, size-int64(n)+1) // starts are in [0, end)
	if end <= 0 {
		return Hit{}, false, ctx.Err()
	}
	m := newMatcher(p)
	const cs = nextChunk
	chunks := (end + cs - 1) / cs
	work := func(ctx context.Context, c int64) (int64, error) {
		e := end - c*cs
		s := max(e-cs, 0)
		bp := getBuf(int(e - s + int64(n) - 1))
		defer putBuf(bp)
		buf := *bp
		if err := readFull(r, buf, s); err != nil {
			return -1, err
		}
		last := -1
		m.find(buf, int(e-s), func(i int) bool {
			last = i
			return true
		})
		if last < 0 {
			return -1, nil
		}
		return s + int64(last), nil
	}
	var hit Hit
	found := false
	err := ordered(ctx, 0, chunks, work, func(_ int64, off int64) bool {
		if off < 0 {
			return true
		}
		hit, found = Hit{Off: off, Len: n}, true
		return false
	})
	return hit, found, err
}

// Collect returns the matches as a list, at most maxHits of them (FIND-7);
// truncated tells that there are more
func Collect(ctx context.Context, r io.ReaderAt, size int64, p Pattern, opt Options, maxHits int, progress func(done int64)) (hits []Hit, truncated bool, err error) {
	if maxHits > 0 {
		opt.MaxHits = maxHits + 1
	}
	err = FindAll(ctx, r, size, p, opt, func(h Hit) bool {
		hits = append(hits, h)
		return true
	}, progress)
	if maxHits > 0 && len(hits) > maxHits {
		hits, truncated = slices.Clip(hits[:maxHits]), true
	}
	return hits, truncated, err
}
