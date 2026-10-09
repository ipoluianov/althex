package patch

import (
	"bufio"
	"context"
	"encoding/binary"
	"hash"
	"hash/crc32"
	"io"

	"github.com/ipoluianov/althex/core/diff"
)

// BPS (beat): "BPS1", the source size, the target size and the metadata
// size as beat numbers, the metadata, then actions until the last 12 bytes:
// the CRC-32 of the source, of the target and of the patch before it
// (little-endian). An action is a number: (length-1)<<2 | command.
//
//	SourceRead  copies the source at the current offset of the target
//	TargetRead  copies the bytes that follow in the patch
//	SourceCopy  a signed number moves the source cursor, then copies from it
//	TargetCopy  a signed number moves the target cursor, then copies from the
//	            target written so far (it may overlap what it writes)
//
// Make takes a comparison with shifts: equal ranges in place are SourceRead,
// moved ones SourceCopy, the rest TargetRead. Apply keeps the last
// bpsWindow bytes of the target for TargetCopy; older ones are read back
// from w when it is an io.ReaderAt, otherwise it is an error.
const (
	bpsMagic  = "BPS1"
	bpsWindow = 16 << 20
)

const (
	bpsSourceRead = iota
	bpsTargetRead
	bpsSourceCopy
	bpsTargetCopy
)

// putNum writes a beat number: 7 bits a byte, the last one marked by the
// high bit, each byte after the first one less by one so that every number
// has one encoding
func putNum(w io.ByteWriter, v uint64) {
	for {
		x := byte(v & 0x7f)
		v >>= 7
		if v == 0 {
			w.WriteByte(0x80 | x)
			return
		}
		w.WriteByte(x)
		v--
	}
}

func getNum(r io.ByteReader) (uint64, error) {
	var v uint64
	shift := uint64(1)
	for i := 0; ; i++ {
		x, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		if i >= 9 {
			return 0, ErrCorrupt // over 63 bits
		}
		v += uint64(x&0x7f) * shift
		if x&0x80 != 0 {
			break
		}
		shift <<= 7
		v += shift
	}
	if v > 1<<62 {
		return 0, ErrCorrupt
	}
	return v, nil
}

func putSigned(w io.ByteWriter, v int64) {
	if v < 0 {
		putNum(w, uint64(-v)<<1|1)
	} else {
		putNum(w, uint64(v)<<1)
	}
}

func getSigned(r io.ByteReader) (int64, error) {
	v, err := getNum(r)
	if err != nil {
		return 0, err
	}
	if v&1 != 0 {
		return -int64(v >> 1), nil
	}
	return int64(v >> 1), nil
}

// crcWriter passes the bytes on and sums them
type crcWriter struct {
	w   *bufio.Writer
	sum hash.Hash32
}

func (c *crcWriter) Write(p []byte) (int, error) {
	c.sum.Write(p)
	return c.w.Write(p)
}

func (c *crcWriter) WriteByte(b byte) error {
	c.sum.Write([]byte{b})
	return c.w.WriteByte(b)
}

func makeBPS(ctx context.Context, w io.Writer, src io.ReaderAt, srcSize int64, dst io.ReaderAt, dstSize int64, progress func(int64)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	srcSum, dstSum := crc32.NewIEEE(), crc32.NewIEEE()
	srcDone, dstDone := hashOf(ctx, src, srcSize, srcSum), hashOf(ctx, dst, dstSize, dstSum)
	// actions cost a few bytes, so close differences go in one
	rs, err := diff.Compare(ctx, src, srcSize, dst, dstSize, diff.Options{Shifts: true, MergeGap: 4}, progress)
	if err != nil {
		return err
	}
	if err := <-srcDone; err != nil {
		return err
	}
	if err := <-dstDone; err != nil {
		return err
	}
	cw := &crcWriter{w: bufio.NewWriter(w), sum: crc32.NewIEEE()}
	cw.Write([]byte(bpsMagic))
	putNum(cw, uint64(srcSize))
	putNum(cw, uint64(dstSize))
	putNum(cw, 0)
	buf := make([]byte, ioBuf)
	var srcRel int64
	for _, r := range rs {
		switch {
		case r.Kind == diff.Equal && r.AOff == r.BOff:
			putNum(cw, uint64(r.ALen-1)<<2|bpsSourceRead)
		case r.Kind == diff.Equal:
			putNum(cw, uint64(r.ALen-1)<<2|bpsSourceCopy)
			putSigned(cw, r.AOff-srcRel)
			srcRel = r.AEnd()
		case r.BLen > 0:
			putNum(cw, uint64(r.BLen-1)<<2|bpsTargetRead)
			if err := copyFrom(ctx, cw, dst, r.BOff, r.BLen, buf); err != nil {
				return err
			}
		}
	}
	var tail [12]byte
	binary.LittleEndian.PutUint32(tail[0:], srcSum.Sum32())
	binary.LittleEndian.PutUint32(tail[4:], dstSum.Sum32())
	cw.Write(tail[:8])
	binary.LittleEndian.PutUint32(tail[8:], cw.sum.Sum32())
	cw.w.Write(tail[8:])
	return cw.w.Flush()
}

// target is the result being written, with its CRC and the last bytes
type target struct {
	w    *bufio.Writer
	out  io.Writer
	sum  hash.Hash32
	n    int64
	ring []byte // byte i of the target is at ring[i%len(ring)]
}

func (t *target) Write(p []byte) (int, error) {
	t.sum.Write(p)
	for q := p; len(q) > 0; {
		i := int(t.n % int64(len(t.ring)))
		k := copy(t.ring[i:], q)
		q = q[k:]
		t.n += int64(k)
	}
	return t.w.Write(p)
}

// read copies the target at off into p; off+len(p) <= t.n
func (t *target) read(p []byte, off int64) error {
	if t.n-off <= int64(len(t.ring)) {
		for q := p; len(q) > 0; {
			k := copy(q, t.ring[int(off%int64(len(t.ring))):])
			q = q[k:]
			off += int64(k)
		}
		return nil
	}
	ra, ok := t.out.(io.ReaderAt)
	if !ok {
		return ErrTooLarge
	}
	if err := t.w.Flush(); err != nil {
		return err
	}
	return readAt(ra, p, off)
}

func applyBPS(ctx context.Context, patch io.Reader, w io.Writer, src io.ReaderAt, srcSize int64) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	r := newReader(patch)
	r.sum = crc32.NewIEEE()
	var magic [4]byte
	if err := r.full(magic[:]); err != nil || string(magic[:]) != bpsMagic {
		return ErrCorrupt
	}
	var hdr [3]uint64
	for i := range hdr {
		v, err := getNum(r)
		if err != nil {
			return err
		}
		hdr[i] = v
	}
	if int64(hdr[0]) != srcSize {
		return ErrSourceMismatch
	}
	tgtSize := int64(hdr[1])
	if err := r.copyN(ctx, io.Discard, int64(hdr[2]), make([]byte, 4096)); err != nil {
		return err
	}
	srcSum := crc32.NewIEEE()
	srcDone := hashOf(ctx, src, srcSize, srcSum)
	t := &target{w: bufio.NewWriterSize(w, 64<<10), out: w, sum: crc32.NewIEEE(),
		ring: make([]byte, max(min(tgtSize, bpsWindow), 1))}
	buf := make([]byte, 64<<10)
	var srcRel, tgtRel int64
	for {
		// the actions end where the 12 bytes of checksums start
		if p, _ := r.br.Peek(13); len(p) < 13 {
			if len(p) < 12 {
				return ErrCorrupt
			}
			break
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		a, err := getNum(r)
		if err != nil {
			return err
		}
		n := int64(a>>2) + 1
		if n > tgtSize-t.n {
			return ErrCorrupt
		}
		switch a & 3 {
		case bpsSourceRead:
			if t.n+n > srcSize {
				return ErrCorrupt
			}
			err = copyFrom(ctx, t, src, t.n, n, buf)
		case bpsTargetRead:
			err = r.copyN(ctx, t, n, buf)
		case bpsSourceCopy:
			d, e := getSigned(r)
			if e != nil {
				return e
			}
			srcRel += d
			if srcRel < 0 || srcRel > srcSize-n {
				return ErrCorrupt
			}
			err = copyFrom(ctx, t, src, srcRel, n, buf)
			srcRel += n
		case bpsTargetCopy:
			d, e := getSigned(r)
			if e != nil {
				return e
			}
			tgtRel += d
			if tgtRel < 0 || tgtRel >= t.n {
				return ErrCorrupt
			}
			// a piece at a time, as it may copy what it writes
			for left := n; left > 0; {
				if err := ctx.Err(); err != nil {
					return err
				}
				p := buf[:min(int64(len(buf)), left, t.n-tgtRel)]
				if err := t.read(p, tgtRel); err != nil {
					return err
				}
				t.Write(p)
				tgtRel += int64(len(p))
				left -= int64(len(p))
			}
		}
		if err != nil {
			return err
		}
	}
	if t.n != tgtSize {
		return ErrCorrupt
	}
	var tail [12]byte
	if err := r.full(tail[:8]); err != nil {
		return err
	}
	patchSum := r.sum.Sum32()
	if err := r.full(tail[8:]); err != nil {
		return err
	}
	if err := <-srcDone; err != nil {
		return err
	}
	if binary.LittleEndian.Uint32(tail[8:]) != patchSum {
		return ErrChecksum
	}
	if binary.LittleEndian.Uint32(tail[0:]) != srcSum.Sum32() {
		return ErrSourceMismatch
	}
	if err := t.w.Flush(); err != nil {
		return err
	}
	if binary.LittleEndian.Uint32(tail[4:]) != t.sum.Sum32() {
		return ErrChecksum
	}
	return nil
}
