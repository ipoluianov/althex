package diff

import (
	"bytes"
	"context"
	"io"
)

// The comparison with shifts (DIFF-2) works like rsync or xdelta:
//
//  1. Index: every block of A at an offset that is a multiple of the block
//     size gets a hash. When A has more blocks than maxEntries, only every
//     step-th block is taken, so the index stays bounded (about 20 bytes a
//     block); then a common part is surely found when it is at least
//     (step+1)·blockSize long. A hash keeps at most maxPerHash offsets, the
//     first ones in A, so a file of zeros does not make a long chain.
//  2. Scan: a rolling hash runs over B. When the hash of a window is in the
//     index, the candidates are its offsets in A that are not before the end
//     of the last match, and also the nearest copy of the window in A within
//     smallGap of the offset that continues the last match in step with B
//     (small shifts in repeated data are found so). Each is checked byte by
//     byte and extended forwards up to probeLen; the longest wins, of equal
//     ones the nearest to that offset.
//  3. A match far ahead in A (skipping more of A than of B) is taken only if
//     it is long: at least 1/16 of the skipped bytes or probeLen. A short
//     match by chance must not throw away the rest of A, since the chain of
//     matches only goes forwards in A (greedy, in the order of B).
//  4. The match is extended forwards to its end and backwards to the end of
//     the last match; the scan goes on after it.
//  5. The gap between two matches: when the sides differ in size and both are
//     small (smallGap), the exact Myers diff gives the inserts and deletes
//     (if they are at most maxMyersD); otherwise the shorter side is compared byte by byte with the start of
//     the longer one, and the rest of the longer one is Inserted or Deleted.
//     A gap with sides of the same size is taken for an overwrite.
const (
	maxEntries = 1 << 22
	maxPerHash = 8
	probeLen   = 64 << 10
	smallGap   = 4 << 10
	maxMyersD  = 512
	scanBuf    = 1 << 20
	filterBits = 1 << 23
)

const (
	hashMul = 0x100000001b3 // odd, so the hash is a polynomial mod 2^64
	mix1    = 0xff51afd7ed558ccd
	mix2    = 0xc4ceb9fe1a85ec53
)

func mix(h uint64) uint64 {
	h ^= h >> 33
	h *= mix1
	h ^= h >> 33
	h *= mix2
	h ^= h >> 33
	return h
}

func hashBlock(p []byte) uint64 {
	var h uint64
	for _, c := range p {
		h = h*hashMul + uint64(c)
	}
	return h
}

// index of the blocks of A by hash: chains in buckets
type index struct {
	bs     int64
	stride int64 // bytes between the indexed blocks: step·bs
	mask   uint64
	heads  []uint32 // 1 + the first entry of a bucket; 0 - none
	next   []uint32 // 1 + the next entry of the bucket
	check  []uint32 // the high half of the hash, to skip other hashes quickly
	sample []uint32 // offset = sample·stride
	filter []uint64 // a bit by other bits of the hash, small enough for the cache
}

func (x *index) insert(h uint64, sample uint32) {
	b := h & x.mask
	c := uint32(h >> 32)
	same := 0
	for e := x.heads[b]; e != 0; e = x.next[e-1] {
		if x.check[e-1] == c {
			if same++; same >= maxPerHash {
				return
			}
		}
	}
	x.check = append(x.check, c)
	x.sample = append(x.sample, sample)
	x.next = append(x.next, x.heads[b])
	x.heads[b] = uint32(len(x.check))
	f := (h >> 40) % filterBits
	x.filter[f/64] |= 1 << (f % 64)
}

func (x *index) maybe(h uint64) bool {
	f := (h >> 40) % filterBits
	return x.filter[f/64]&(1<<(f%64)) != 0
}

func buildIndex(ctx context.Context, a io.ReaderAt, aSize int64, bs int, progress func(int64)) (*index, error) {
	blocks := aSize / int64(bs)
	step := max((blocks+maxEntries-1)/maxEntries, 1)
	entries := blocks / step
	buckets := uint64(1)
	for buckets < uint64(entries)*2 {
		buckets <<= 1
	}
	x := &index{
		bs:     int64(bs),
		stride: step * int64(bs),
		mask:   buckets - 1,
		heads:  make([]uint32, buckets),
		next:   make([]uint32, 0, entries),
		check:  make([]uint32, 0, entries),
		sample: make([]uint32, 0, entries),
		filter: make([]uint64, filterBits/64),
	}
	// read whole strides, about scanBuf at a time
	bufLen := max(scanBuf/x.stride, 1) * x.stride
	buf := make([]byte, min(bufLen, max(aSize, 1)))
	for base := int64(0); base < aSize; base += bufLen {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		ln := min(bufLen, aSize-base)
		p := buf[:ln]
		if err := readAt(a, p, base); err != nil {
			return nil, err
		}
		for o := int64(0); o+int64(bs) <= ln; o += x.stride {
			x.insert(mix(hashBlock(p[o:o+int64(bs)])), uint32((base+o)/x.stride))
		}
		progress((base + ln) / 2)
	}
	return x, nil
}

type shifter struct {
	ctx          context.Context
	a, b         io.ReaderAt
	aSize, bSize int64
	bs           int64
	x            *index
	bl           *builder
	progress     func(int64)
	aPos, bPos   int64 // everything before them is in the builder
	pa, pb       []byte
	cands        []int64
	trace        []int // for myers
}

func compareShifts(ctx context.Context, a io.ReaderAt, aSize int64, b io.ReaderAt, bSize int64, bs int, bl *builder, progress func(int64)) error {
	c := &shifter{ctx: ctx, a: a, b: b, aSize: aSize, bSize: bSize, bs: int64(bs), bl: bl, progress: progress,
		pa: make([]byte, max(probeLen, 2*smallGap+2*bs)), pb: make([]byte, max(probeLen, 2*smallGap+2*bs))}
	if aSize >= int64(bs) && bSize >= int64(bs) {
		x, err := buildIndex(ctx, a, aSize, bs, progress)
		if err != nil {
			return err
		}
		c.x = x
		if err := c.scan(); err != nil {
			return err
		}
	}
	return c.gap(c.aPos, c.aSize-c.aPos, c.bPos, c.bSize-c.bPos)
}

func (c *shifter) report() {
	if c.bSize > 0 {
		c.progress(c.aSize/2 + int64(float64(c.aSize/2)*float64(c.bPos)/float64(c.bSize)))
	}
}

// scan runs the rolling hash over B and takes the matches
func (c *shifter) scan() error {
	bs := c.bs
	pow := uint64(1) // hashMul^bs, to take the leaving byte out
	for range bs {
		pow *= hashMul
	}
	buf := make([]byte, max(scanBuf, 2*bs))
	var bufOff int64 // the offset of buf[0] in B
	bufLen := int64(0)
	// fill makes buf hold B from off
	fill := func(off int64) error {
		ln := min(int64(len(buf)), c.bSize-off)
		if err := readAt(c.b, buf[:ln], off); err != nil {
			return err
		}
		bufOff, bufLen = off, ln
		return nil
	}
	s := c.bPos
	valid := false
	var h uint64
	nextTick := s + scanBuf
	for s+bs <= c.bSize {
		if s >= nextTick {
			if err := c.ctx.Err(); err != nil {
				return err
			}
			c.report()
			nextTick = s + scanBuf
		}
		if !valid {
			if s < bufOff || s+bs > bufOff+bufLen {
				if err := fill(s); err != nil {
					return err
				}
			}
			i := s - bufOff
			h = hashBlock(buf[i : i+bs])
			valid = true
		} else {
			if s+bs > bufOff+bufLen {
				if err := fill(s - 1); err != nil {
					return err
				}
			}
			i := s - bufOff
			h = h*hashMul + uint64(buf[i+bs-1]) - pow*uint64(buf[i-1])
		}
		m := mix(h)
		if !c.x.maybe(m) {
			s++
			continue
		}
		i := s - bufOff
		ma, mb, ml, ok, err := c.match(m, s, buf[i:i+bs])
		if err != nil {
			return err
		}
		if !ok {
			s++
			continue
		}
		if err := c.gap(c.aPos, ma-c.aPos, c.bPos, mb-c.bPos); err != nil {
			return err
		}
		c.bl.equal(ma, mb, ml)
		c.aPos, c.bPos = ma+ml, mb+ml
		s = c.bPos
		valid = false
	}
	return nil
}

// match looks for the best match of the window w at s in B; it returns the
// match extended both ways
func (c *shifter) match(m uint64, s int64, w []byte) (ma, mb, ml int64, ok bool, err error) {
	x := c.x
	cm := uint32(m >> 32)
	c.cands = c.cands[:0]
	found := false
	for e := x.heads[m&x.mask]; e != 0; e = x.next[e-1] {
		if x.check[e-1] != cm {
			continue
		}
		found = true
		if off := int64(x.sample[e-1]) * x.stride; off >= c.aPos {
			c.cands = append(c.cands, off)
		}
	}
	if !found {
		return
	}
	inStep := c.aPos + (s - c.bPos)
	if p, e := c.near(inStep, w); e != nil {
		return 0, 0, 0, false, e
	} else if p >= 0 {
		c.cands = append(c.cands, p)
	}
	best, bestLen := int64(-1), int64(0)
	for _, off := range c.cands {
		p := c.pa[:c.bs]
		if err = readAt(c.a, p, off); err != nil {
			return
		}
		if !bytes.Equal(p, w) {
			continue
		}
		var n int64
		if n, err = c.forward(off+c.bs, s+c.bs, probeLen); err != nil {
			return
		}
		n += c.bs
		if skip := (off - c.aPos) - (s - c.bPos); skip > 0 && n < probeLen && n*16 < skip {
			continue
		}
		if n > bestLen || (n == bestLen && abs(off-inStep) < abs(best-inStep)) {
			best, bestLen = off, n
		}
	}
	if best < 0 {
		return
	}
	n := bestLen
	if n >= probeLen+c.bs {
		var more int64
		if more, err = c.forward(best+n, s+n, -1); err != nil {
			return
		}
		n += more
	}
	var back int64
	if back, err = c.backward(best, s, min(best-c.aPos, s-c.bPos)); err != nil {
		return
	}
	return best - back, s - back, n + back, true, nil
}

// near finds w in A about the offset that continues the last match in step
// with B, the closest one to it; -1 - none. Small inserts and deletes in
// repeated data are found this way, as the index keeps only a few offsets of
// a hash.
func (c *shifter) near(inStep int64, w []byte) (int64, error) {
	from := max(c.aPos, inStep-smallGap)
	to := min(c.aSize, inStep+smallGap+c.bs)
	if to-from < c.bs {
		return -1, nil
	}
	p := c.pa[:to-from]
	if err := readAt(c.a, p, from); err != nil {
		return 0, err
	}
	best := int64(-1)
	for o := 0; ; {
		i := bytes.Index(p[o:], w)
		if i < 0 {
			break
		}
		at := from + int64(o+i)
		if best < 0 || abs(at-inStep) < abs(best-inStep) {
			best = at
		}
		o += i + 1
	}
	return best, nil
}

func abs(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// forward counts the equal bytes of A from ao and B from bo, up to limit (-1 - no limit)
func (c *shifter) forward(ao, bo, limit int64) (int64, error) {
	left := min(c.aSize-ao, c.bSize-bo)
	if limit >= 0 {
		left = min(left, limit)
	}
	var n int64
	for n < left {
		k := min(int64(len(c.pa)), left-n)
		x, y := c.pa[:k], c.pb[:k]
		if err := readAt(c.a, x, ao+n); err != nil {
			return 0, err
		}
		if err := readAt(c.b, y, bo+n); err != nil {
			return 0, err
		}
		if e := firstDiff(x, y); e < len(x) {
			return n + int64(e), nil
		}
		n += k
		if n%(16<<20) == 0 {
			if err := c.ctx.Err(); err != nil {
				return 0, err
			}
		}
	}
	return n, nil
}

// backward counts the equal bytes of A before ao and B before bo, up to limit
func (c *shifter) backward(ao, bo, limit int64) (int64, error) {
	var n int64
	step := int64(256) // differences are usually near, so start small
	for n < limit {
		k := min(step, int64(len(c.pa)), limit-n)
		x, y := c.pa[:k], c.pb[:k]
		if err := readAt(c.a, x, ao-n-k); err != nil {
			return 0, err
		}
		if err := readAt(c.b, y, bo-n-k); err != nil {
			return 0, err
		}
		for i := k - 1; i >= 0; i-- {
			if x[i] != y[i] {
				return n + (k - 1 - i), nil
			}
		}
		n += k
		step *= 2
	}
	return n, nil
}

func firstDiff(x, y []byte) int {
	for o := 0; o < len(x); o += cmpBlock {
		e := min(o+cmpBlock, len(x))
		if bytes.Equal(x[o:e], y[o:e]) {
			continue
		}
		for i := o; i < e; i++ {
			if x[i] != y[i] {
				return i
			}
		}
	}
	return len(x)
}

// gap fills the part between two matches
func (c *shifter) gap(ao, al, bo, bl int64) error {
	switch {
	case al == 0 && bl == 0:
		return nil
	case al == 0 || bl == 0:
		return c.bl.differ(ao, al, bo, bl)
	}
	// equal sides are taken for an overwrite: compared at the same offsets
	if al != bl && al <= smallGap && bl <= smallGap {
		x, y := make([]byte, al), make([]byte, bl)
		if err := readAt(c.a, x, ao); err != nil {
			return err
		}
		if err := readAt(c.b, y, bo); err != nil {
			return err
		}
		if ops, ok := myers(x, y, maxMyersD, &c.trace); ok {
			for _, o := range ops {
				n := int64(o.n)
				switch o.kind {
				case Equal:
					c.bl.equal(ao, bo, n)
					ao += n
					bo += n
				case Deleted:
					if err := c.bl.differ(ao, n, bo, 0); err != nil {
						return err
					}
					ao += n
				case Inserted:
					if err := c.bl.differ(ao, 0, bo, n); err != nil {
						return err
					}
					bo += n
				}
			}
			return nil
		}
	}
	// the same offsets over the shorter side
	n := min(al, bl)
	var runs []run
	for o := int64(0); o < n; {
		if err := c.ctx.Err(); err != nil {
			return err
		}
		k := min(int64(len(c.pa)), n-o)
		x, y := c.pa[:k], c.pb[:k]
		if err := readAt(c.a, x, ao+o); err != nil {
			return err
		}
		if err := readAt(c.b, y, bo+o); err != nil {
			return err
		}
		runs = diffRuns(x, y, o, runs[:0])
		pos := o
		for _, r := range runs {
			c.bl.equal(ao+pos, bo+pos, r.start-pos)
			if err := c.bl.differ(ao+r.start, r.end-r.start, bo+r.start, r.end-r.start); err != nil {
				return err
			}
			pos = r.end
		}
		c.bl.equal(ao+pos, bo+pos, o+k-pos)
		o += k
	}
	return c.bl.differ(ao+n, al-n, bo+n, bl-n)
}
