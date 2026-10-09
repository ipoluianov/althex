// Package diff compares two inputs of any size: byte by byte at the same
// offsets (DIFF-1) or with inserts and deletes aligned (DIFF-2). The result
// is a list of ranges that covers both inputs in order, which is also an edit
// script: taking A's bytes for the Equal ranges and B's bytes for the others
// gives B.
//
// The inputs are read in pieces through io.ReaderAt, so the memory does not
// grow with their size; the only exception is the index of the blocks of A in
// the mode with shifts, which is bounded too (see shifts.go).
package diff

import (
	"context"
	"errors"
	"io"
	"sort"
)

// ErrMaxRanges is returned with the ranges found so far when Options.MaxRanges
// is reached; the ranges then cover only the beginning of the inputs
var ErrMaxRanges = errors.New("diff: too many differences")

// Kind of a range
type Kind int

const (
	Equal    Kind = iota
	Changed       // both sides have bytes, and they differ
	Inserted      // the bytes are only in B
	Deleted       // the bytes are only in A
)

func (k Kind) String() string {
	switch k {
	case Equal:
		return "equal"
	case Changed:
		return "changed"
	case Inserted:
		return "inserted"
	case Deleted:
		return "deleted"
	}
	return "unknown"
}

// Range is a part of A and the part of B that stands for it
type Range struct {
	Kind       Kind
	AOff, ALen int64
	BOff, BLen int64
}

// AEnd is the offset in A after the range
func (r Range) AEnd() int64 { return r.AOff + r.ALen }

// BEnd is the offset in B after the range
func (r Range) BEnd() int64 { return r.BOff + r.BLen }

// Options of a comparison
type Options struct {
	// Shifts finds inserts and deletes (DIFF-2); false - byte by byte at the
	// same offsets (DIFF-1)
	Shifts bool
	// MergeGap makes one range of differences that have fewer equal bytes
	// between them than this; 0 - none are merged
	MergeGap int
	// BlockSize is the size of the anchors with shifts; 0 - 32
	BlockSize int
	// MaxRanges stops the comparison after so many differing ranges
	// (ErrMaxRanges); 0 - no limit
	MaxRanges int
}

const defaultBlockSize = 32

// Compare returns the ranges covering both inputs in order, Equal ones
// included. Progress, if not nil, is called from time to time with the bytes
// of A done (with shifts, A is read twice: the index is the first half).
func Compare(ctx context.Context, a io.ReaderAt, aSize int64, b io.ReaderAt, bSize int64, opt Options, progress func(done int64)) ([]Range, error) {
	if aSize < 0 || bSize < 0 {
		return nil, errors.New("diff: negative size")
	}
	if progress == nil {
		progress = func(int64) {}
	}
	bl := &builder{gap: int64(max(opt.MergeGap, 0)), max: max(opt.MaxRanges, 0)}
	var err error
	if opt.Shifts {
		bs := opt.BlockSize
		if bs <= 0 {
			bs = defaultBlockSize
		}
		err = compareShifts(ctx, a, aSize, b, bSize, bs, bl, progress)
	} else {
		err = compareBytes(ctx, a, aSize, b, bSize, bl, progress)
	}
	if err != nil && err != ErrMaxRanges {
		return nil, err
	}
	if err == nil {
		progress(aSize)
	}
	return bl.rs, err
}

// builder collects the ranges, joining neighbours of the same kind and
// differences closer than the gap
type builder struct {
	rs    []Range
	gap   int64
	max   int
	diffs int
}

func kindOf(aLen, bLen int64) Kind {
	switch {
	case aLen == 0:
		return Inserted
	case bLen == 0:
		return Deleted
	}
	return Changed
}

func join(dst *Range, r Range) {
	dst.ALen += r.ALen
	dst.BLen += r.BLen
	dst.Kind = kindOf(dst.ALen, dst.BLen)
}

// add appends the next range; false - it would be a differing range over the
// limit, and it is not added
func (b *builder) add(r Range) bool {
	if r.ALen == 0 && r.BLen == 0 {
		return true
	}
	n := len(b.rs)
	if r.Kind == Equal {
		if n > 0 && b.rs[n-1].Kind == Equal {
			join(&b.rs[n-1], r)
			b.rs[n-1].Kind = Equal
			return true
		}
		b.rs = append(b.rs, r)
		return true
	}
	r.Kind = kindOf(r.ALen, r.BLen)
	if n > 0 && b.rs[n-1].Kind != Equal {
		join(&b.rs[n-1], r)
		return true
	}
	// Equal ranges are always joined, so the one before an Equal one differs
	if n > 1 && b.rs[n-1].ALen < b.gap {
		join(&b.rs[n-2], b.rs[n-1])
		join(&b.rs[n-2], r)
		b.rs = b.rs[:n-1]
		return true
	}
	if b.max > 0 && b.diffs >= b.max {
		return false
	}
	b.diffs++
	b.rs = append(b.rs, r)
	return true
}

func (b *builder) equal(aOff, bOff, n int64) {
	b.add(Range{Kind: Equal, AOff: aOff, ALen: n, BOff: bOff, BLen: n})
}

func (b *builder) differ(aOff, aLen, bOff, bLen int64) error {
	if !b.add(Range{Kind: Changed, AOff: aOff, ALen: aLen, BOff: bOff, BLen: bLen}) {
		return ErrMaxRanges
	}
	return nil
}

// readAt fills buf from off; a short read is an error
func readAt(r io.ReaderAt, buf []byte, off int64) error {
	n, err := r.ReadAt(buf, off)
	if n == len(buf) {
		return nil
	}
	if err == nil || err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return err
}

// Summary counts the bytes of each kind; for Changed ones, the larger side
type Summary struct {
	Equal, Changed, Inserted, Deleted int64
	// Ranges is the number of differing ranges
	Ranges int
}

// Summarize counts the ranges
func Summarize(rs []Range) Summary {
	var s Summary
	for _, r := range rs {
		switch r.Kind {
		case Equal:
			s.Equal += r.ALen
			continue
		case Changed:
			s.Changed += max(r.ALen, r.BLen)
		case Inserted:
			s.Inserted += r.BLen
		case Deleted:
			s.Deleted += r.ALen
		}
		s.Ranges++
	}
	return s
}

// NextDiff returns the index of the first differing range that starts after
// offA in A; -1 - none
func NextDiff(rs []Range, offA int64) int {
	i := sort.Search(len(rs), func(i int) bool { return rs[i].AOff > offA })
	return nextFrom(rs, i)
}

// PrevDiff returns the index of the last differing range that starts before
// offA in A; -1 - none
func PrevDiff(rs []Range, offA int64) int {
	i := sort.Search(len(rs), func(i int) bool { return rs[i].AOff >= offA })
	return prevFrom(rs, i-1)
}

// NextDiffB is NextDiff by an offset in B
func NextDiffB(rs []Range, offB int64) int {
	i := sort.Search(len(rs), func(i int) bool { return rs[i].BOff > offB })
	return nextFrom(rs, i)
}

// PrevDiffB is PrevDiff by an offset in B
func PrevDiffB(rs []Range, offB int64) int {
	i := sort.Search(len(rs), func(i int) bool { return rs[i].BOff >= offB })
	return prevFrom(rs, i-1)
}

func nextFrom(rs []Range, i int) int {
	for ; i < len(rs); i++ {
		if rs[i].Kind != Equal {
			return i
		}
	}
	return -1
}

func prevFrom(rs []Range, i int) int {
	for ; i >= 0; i-- {
		if rs[i].Kind != Equal {
			return i
		}
	}
	return -1
}
