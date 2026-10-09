// Package ops changes ranges of a document: fills them (EDIT-4) and
// applies bitwise operations to them (EDIT-5). A range can be gigabytes:
// the bytes are made and written a megabyte at a time, as one edit of the
// history. Call BeginGroup on the document before and EndGroup after, on
// the goroutine of the window: the work itself may run on another one.
package ops

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math/rand/v2"

	"github.com/ipoluianov/althex/core/doc"
)

// chunk is how much is made and written at once
const chunk = 1 << 20

var ErrEmptyPattern = errors.New("the pattern is empty")

// FillKind is what a fill writes
type FillKind int

const (
	// FillPattern repeats the bytes of the pattern
	FillPattern FillKind = iota
	// FillRandom writes random bytes
	FillRandom
	// FillIncrement writes numbers of Width bytes from Start by Step
	FillIncrement
)

// Fill describes a fill
type Fill struct {
	Kind    FillKind
	Pattern []byte
	// For FillIncrement: the first number, the step, the size of a number
	// (1, 2, 4 or 8 bytes) and its byte order
	Start, Step uint64
	Width       int
	BigEndian   bool
	Insert      bool // insert n bytes instead of overwriting
}

// DoFill fills n bytes at off. A fill with zeros takes no space whatever its size.
func DoFill(ctx context.Context, d *doc.Document, off, n int64, f Fill, progress func(done int64)) error {
	opt := doc.EditOptions{Kind: doc.KindFill}
	if f.Kind == FillPattern {
		if len(f.Pattern) == 0 {
			return ErrEmptyPattern
		}
		if allZero(f.Pattern) {
			if f.Insert {
				return d.InsertZeros(off, n, opt)
			}
			return d.OverwriteZeros(off, n, opt)
		}
	}
	if f.Kind == FillIncrement {
		switch f.Width {
		case 1, 2, 4, 8:
		default:
			f.Width = 1
		}
	}
	src := &filler{f: f, rnd: rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))}
	buf := make([]byte, min(n, chunk))
	for done := int64(0); done < n; {
		if err := ctx.Err(); err != nil {
			return err
		}
		k := min(int64(len(buf)), n-done)
		src.make(buf[:k], done)
		var err error
		if f.Insert {
			err = d.Insert(off+done, buf[:k], opt)
		} else {
			err = d.Overwrite(off+done, buf[:k], opt)
		}
		if err != nil {
			return err
		}
		done += k
		if progress != nil {
			progress(done)
		}
	}
	return nil
}

func allZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

// filler makes the bytes of a fill; pos is the offset in the range
type filler struct {
	f   Fill
	rnd *rand.Rand
}

func (s *filler) make(buf []byte, pos int64) {
	switch s.f.Kind {
	case FillPattern:
		p := s.f.Pattern
		for i := range buf {
			buf[i] = p[(pos+int64(i))%int64(len(p))]
		}
	case FillRandom:
		for i := 0; i < len(buf); i += 8 {
			var w [8]byte
			binary.LittleEndian.PutUint64(w[:], s.rnd.Uint64())
			copy(buf[i:], w[:])
		}
	case FillIncrement:
		width := int64(s.f.Width)
		var w [8]byte
		for i := range buf {
			at := pos + int64(i)
			num := s.f.Start + uint64(at/width)*s.f.Step
			if s.f.BigEndian {
				binary.BigEndian.PutUint64(w[:], num)
				buf[i] = w[8-width+at%width]
			} else {
				binary.LittleEndian.PutUint64(w[:], num)
				buf[i] = w[at%width]
			}
		}
	}
}

// Op is a bitwise operation on the bytes of a range
type Op int

const (
	OpAnd Op = iota
	OpOr
	OpXor
	OpNot
	OpShl // each byte shifted left by Bits
	OpShr
	OpRol // each byte rotated
	OpRor
	OpSwap    // the order of the bytes reversed in each group of Width bytes
	OpReverse // the order of all the bytes of the range reversed
	OpAdd     // the operand added to each byte (modulo 256)
	OpSub
)

// Transform describes an operation
type Transform struct {
	Op Op
	// Operand is repeated over the range for And, Or, Xor, Add, Sub
	Operand []byte
	Bits    int // for the shifts and rotations, 0..7
	Width   int // for Swap: 2, 4 or 8
}

// DoTransform applies the operation to n bytes at off. Reverse reads the
// range from both ends, so it works on any size too.
func DoTransform(ctx context.Context, d *doc.Document, off, n int64, t Transform, progress func(done int64)) error {
	switch t.Op {
	case OpAnd, OpOr, OpXor, OpAdd, OpSub:
		if len(t.Operand) == 0 {
			return ErrEmptyPattern
		}
	case OpSwap:
		switch t.Width {
		case 2, 4, 8:
		default:
			return errors.New("the group of bytes to swap is 2, 4 or 8")
		}
	}
	if t.Op == OpReverse {
		return reverse(ctx, d, off, n, progress)
	}
	opt := doc.EditOptions{Kind: "transform"}
	size := int64(chunk)
	if t.Op == OpSwap {
		size -= size % int64(t.Width)
	}
	buf := make([]byte, min(n, size))
	for done := int64(0); done < n; {
		if err := ctx.Err(); err != nil {
			return err
		}
		k := min(int64(len(buf)), n-done)
		b := buf[:k]
		if _, err := d.ReadAt(b, off+done); err != nil && err != io.EOF {
			return err
		}
		apply(t, b, done)
		if err := d.Overwrite(off+done, b, opt); err != nil {
			return err
		}
		done += k
		if progress != nil {
			progress(done)
		}
	}
	return nil
}

// apply changes the bytes; pos is their offset in the range (for the operand)
func apply(t Transform, b []byte, pos int64) {
	opnd := func(i int) byte {
		return t.Operand[(pos+int64(i))%int64(len(t.Operand))]
	}
	bits := uint(t.Bits & 7)
	switch t.Op {
	case OpAnd:
		for i := range b {
			b[i] &= opnd(i)
		}
	case OpOr:
		for i := range b {
			b[i] |= opnd(i)
		}
	case OpXor:
		for i := range b {
			b[i] ^= opnd(i)
		}
	case OpAdd:
		for i := range b {
			b[i] += opnd(i)
		}
	case OpSub:
		for i := range b {
			b[i] -= opnd(i)
		}
	case OpNot:
		for i := range b {
			b[i] = ^b[i]
		}
	case OpShl:
		for i := range b {
			b[i] <<= bits
		}
	case OpShr:
		for i := range b {
			b[i] >>= bits
		}
	case OpRol:
		for i := range b {
			b[i] = b[i]<<bits | b[i]>>(8-bits)
		}
	case OpRor:
		for i := range b {
			b[i] = b[i]>>bits | b[i]<<(8-bits)
		}
	case OpSwap:
		w := t.Width
		// A last group shorter than the width is left as it is
		for g := 0; g+w <= len(b); g += w {
			for i, j := g, g+w-1; i < j; i, j = i+1, j-1 {
				b[i], b[j] = b[j], b[i]
			}
		}
	}
}

// reverse reverses the order of the bytes of the range: the chunks from the
// two ends are swapped and reversed, meeting in the middle
func reverse(ctx context.Context, d *doc.Document, off, n int64, progress func(done int64)) error {
	opt := doc.EditOptions{Kind: "transform"}
	a := make([]byte, min(n/2+1, chunk))
	b := make([]byte, len(a))
	for lo, hi := int64(0), n; hi-lo > 1; {
		if err := ctx.Err(); err != nil {
			return err
		}
		k := min(int64(len(a)), (hi-lo)/2)
		if k == 0 {
			break
		}
		ca, cb := a[:k], b[:k]
		if _, err := d.ReadAt(ca, off+lo); err != nil && err != io.EOF {
			return err
		}
		if _, err := d.ReadAt(cb, off+hi-k); err != nil && err != io.EOF {
			return err
		}
		reverseBytes(ca)
		reverseBytes(cb)
		if err := d.Overwrite(off+lo, cb, opt); err != nil {
			return err
		}
		if err := d.Overwrite(off+hi-k, ca, opt); err != nil {
			return err
		}
		lo += k
		hi -= k
		if progress != nil {
			progress(2 * lo)
		}
	}
	return nil
}

func reverseBytes(b []byte) {
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
}
