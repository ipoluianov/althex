package patch

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"hash/crc32"
	"io"

	"github.com/ipoluianov/althex/core/diff"
)

// AltHex patch (.ahp), all numbers are varints of encoding/binary: uvarint
// is LEB128 (7 bits a byte, low first, the high bit set on all bytes but the
// last), varint is a uvarint of the zigzag form of a signed number.
//
//	"AHPATCH1"           8 bytes
//	uvarint              the size of the source
//	[32]byte             SHA-256 of the source
//	uvarint              the size of the target
//	[32]byte             SHA-256 of the target
//	ops, in the order of the target, each a byte and its fields:
//	  0x01 copy          varint delta, uvarint length (> 0): copies length
//	                     bytes of the source from cursor+delta, then the cursor
//	                     is after them (it starts at 0)
//	  0x02 literal       uvarint length (> 0), then length bytes of the target
//	  0x00 end
//	uint32               CRC-32 (IEEE), little-endian, of all bytes before it
//
// The lengths of the ops add up to the size of the target. Apply checks the
// size and the hash of the source before it writes anything, and the hash
// of the target and the CRC at the end. Nothing may follow the CRC.
const (
	ahpMagic   = "AHPATCH1"
	ahpEnd     = 0
	ahpCopy    = 1
	ahpLiteral = 2
)

type ahpWriter struct {
	w   *bufio.Writer
	tmp [binary.MaxVarintLen64]byte
}

func (x *ahpWriter) Write(p []byte) (int, error) { return x.w.Write(p) }

func (x *ahpWriter) uvarint(v uint64) {
	x.w.Write(binary.AppendUvarint(x.tmp[:0], v))
}

func (x *ahpWriter) varint(v int64) {
	x.w.Write(binary.AppendVarint(x.tmp[:0], v))
}

func makeAHP(ctx context.Context, w io.Writer, src io.ReaderAt, srcSize int64, dst io.ReaderAt, dstSize int64, progress func(int64)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	srcSum, dstSum := sha256.New(), sha256.New()
	srcDone, dstDone := hashOf(ctx, src, srcSize, srcSum), hashOf(ctx, dst, dstSize, dstSum)
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
	crc := crc32.NewIEEE()
	x := &ahpWriter{w: bufio.NewWriter(io.MultiWriter(w, crc))}
	x.w.WriteString(ahpMagic)
	x.uvarint(uint64(srcSize))
	x.w.Write(srcSum.Sum(nil))
	x.uvarint(uint64(dstSize))
	x.w.Write(dstSum.Sum(nil))
	buf := make([]byte, ioBuf)
	var cursor int64
	for _, r := range rs {
		switch {
		case r.Kind == diff.Equal:
			x.w.WriteByte(ahpCopy)
			x.varint(r.AOff - cursor)
			x.uvarint(uint64(r.ALen))
			cursor = r.AEnd()
		case r.BLen > 0:
			x.w.WriteByte(ahpLiteral)
			x.uvarint(uint64(r.BLen))
			if err := copyFrom(ctx, x, dst, r.BOff, r.BLen, buf); err != nil {
				return err
			}
		}
	}
	x.w.WriteByte(ahpEnd)
	if err := x.w.Flush(); err != nil {
		return err
	}
	_, err = w.Write(binary.LittleEndian.AppendUint32(nil, crc.Sum32()))
	return err
}

func applyAHP(ctx context.Context, patch io.Reader, w io.Writer, src io.ReaderAt, srcSize int64) error {
	r := newReader(patch)
	r.sum = crc32.NewIEEE()
	var magic [8]byte
	if err := r.full(magic[:]); err != nil || string(magic[:]) != ahpMagic {
		return ErrCorrupt
	}
	var srcHash, dstHash [32]byte
	ss, err := binary.ReadUvarint(r)
	if err != nil {
		return eofCorrupt(err)
	}
	if err := r.full(srcHash[:]); err != nil {
		return err
	}
	ds, err := binary.ReadUvarint(r)
	if err != nil {
		return eofCorrupt(err)
	}
	if err := r.full(dstHash[:]); err != nil {
		return err
	}
	if int64(ss) != srcSize {
		return ErrSourceMismatch
	}
	dstSize := int64(ds)
	if dstSize < 0 {
		return ErrCorrupt
	}
	h := sha256.New()
	if err := <-hashOf(ctx, src, srcSize, h); err != nil {
		return err
	}
	if !bytes.Equal(h.Sum(nil), srcHash[:]) {
		return ErrSourceMismatch
	}
	h.Reset()
	bw := bufio.NewWriterSize(w, 64<<10)
	out := io.MultiWriter(bw, h)
	buf := make([]byte, 64<<10)
	var cursor, done int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		op, err := r.ReadByte()
		if err != nil {
			return err
		}
		if op == ahpEnd {
			break
		}
		var delta int64
		if op == ahpCopy {
			if delta, err = binary.ReadVarint(r); err != nil {
				return eofCorrupt(err)
			}
		} else if op != ahpLiteral {
			return ErrCorrupt
		}
		u, err := binary.ReadUvarint(r)
		if err != nil {
			return eofCorrupt(err)
		}
		n := int64(u)
		if n <= 0 || n > dstSize-done {
			return ErrCorrupt
		}
		if op == ahpCopy {
			cursor += delta
			if cursor < 0 || cursor > srcSize-n {
				return ErrCorrupt
			}
			err = copyFrom(ctx, out, src, cursor, n, buf)
			cursor += n
		} else {
			err = r.copyN(ctx, out, n, buf)
		}
		if err != nil {
			return err
		}
		done += n
	}
	if done != dstSize {
		return ErrCorrupt
	}
	sum := r.sum.Sum32()
	var tail [4]byte
	if err := r.full(tail[:]); err != nil {
		return err
	}
	if binary.LittleEndian.Uint32(tail[:]) != sum {
		return ErrChecksum
	}
	if _, err := r.br.ReadByte(); err != io.EOF {
		return ErrCorrupt
	}
	if err := bw.Flush(); err != nil {
		return err
	}
	if !bytes.Equal(h.Sum(nil), dstHash[:]) {
		return ErrChecksum
	}
	return nil
}
