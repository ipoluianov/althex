package patch

import (
	"bufio"
	"context"
	"io"

	"github.com/ipoluianov/althex/core/diff"
)

// IPS: "PATCH", then records, then "EOF" and, optionally, the size to
// truncate the result to (the extension of Lunar IPS). A record is a 3-byte
// offset and a 2-byte size (big-endian) with the bytes; a size of 0 is an RLE
// record: a 2-byte count and the byte to repeat. The bytes past the end of
// the source are zeros until a record writes them.
//
// IPS only overwrites, so it cannot express inserts and deletes: the patch is
// made from a byte by byte comparison of src with dst at the same offsets,
// the tail of a longer dst is written as records and a shorter dst is made
// by the truncate size. An offset over 16 MB cannot be written (ErrTooLarge).
const (
	ipsMagic  = "PATCH"
	ipsEOF    = 0x454F46 // "EOF": a record cannot start here
	ipsMaxOff = 0xFFFFFF
	ipsMaxRec = 0xFFFF
	// ipsMinRLE is the shortest run that is cheaper as an RLE record (8
	// bytes) than inside a record, counting the header of the record after it
	ipsMinRLE = 14
)

func put24(w *bufio.Writer, v int64) {
	w.Write([]byte{byte(v >> 16), byte(v >> 8), byte(v)})
}

func put16(w *bufio.Writer, v int) {
	w.Write([]byte{byte(v >> 8), byte(v)})
}

type ipsWriter struct {
	w   *bufio.Writer
	dst io.ReaderAt
	buf []byte
}

func (x *ipsWriter) record(off int64, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if off > ipsMaxOff {
		return ErrTooLarge
	}
	if off == ipsEOF {
		// start a byte earlier, with the byte dst has there
		var b [1]byte
		if err := readAt(x.dst, b[:], off-1); err != nil {
			return err
		}
		if err := x.record(off-1, []byte{b[0], data[0]}); err != nil {
			return err
		}
		return x.record(off+1, data[1:])
	}
	put24(x.w, off)
	put16(x.w, len(data))
	_, err := x.w.Write(data)
	return err
}

func (x *ipsWriter) rle(off int64, n int, v byte) error {
	if off > ipsMaxOff {
		return ErrTooLarge
	}
	if off == ipsEOF {
		if err := x.record(off, []byte{v}); err != nil {
			return err
		}
		off, n = off+1, n-1
		if n == 0 {
			return nil
		}
	}
	put24(x.w, off)
	put16(x.w, 0)
	put16(x.w, n)
	return x.w.WriteByte(v)
}

// span writes the bytes of dst at [off, off+n) as records
func (x *ipsWriter) span(off, n int64) error {
	for n > 0 {
		p := x.buf[:min(n, ipsMaxRec)]
		if err := readAt(x.dst, p, off); err != nil {
			return err
		}
		lit := 0
		for i := 0; i < len(p); {
			j := i + 1
			for j < len(p) && p[j] == p[i] {
				j++
			}
			if j-i >= ipsMinRLE {
				if err := x.record(off+int64(lit), p[lit:i]); err != nil {
					return err
				}
				if err := x.rle(off+int64(i), j-i, p[i]); err != nil {
					return err
				}
				lit = j
			}
			i = j
		}
		if err := x.record(off+int64(lit), p[lit:]); err != nil {
			return err
		}
		off += int64(len(p))
		n -= int64(len(p))
	}
	return nil
}

func makeIPS(ctx context.Context, w io.Writer, src io.ReaderAt, srcSize int64, dst io.ReaderAt, dstSize int64, progress func(int64)) error {
	if dstSize < srcSize && dstSize > ipsMaxOff {
		return ErrTooLarge
	}
	// records cost 5 bytes, so close differences go in one
	rs, err := diff.Compare(ctx, src, srcSize, dst, dstSize, diff.Options{MergeGap: 6}, progress)
	if err != nil {
		return err
	}
	bw := bufio.NewWriter(w)
	bw.WriteString(ipsMagic)
	x := &ipsWriter{w: bw, dst: dst, buf: make([]byte, ipsMaxRec)}
	for _, r := range rs {
		if r.Kind == diff.Equal || r.BLen == 0 {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := x.span(r.BOff, r.BLen); err != nil {
			return err
		}
	}
	bw.WriteString("EOF")
	if dstSize < srcSize {
		put24(bw, dstSize)
	}
	return bw.Flush()
}

type ipsRecord struct {
	off, n int64
	data   []byte // nil - RLE of val
	val    byte
}

func applyIPS(ctx context.Context, patch io.Reader, w io.Writer, src io.ReaderAt, srcSize int64) error {
	r := newReader(patch)
	var hdr [5]byte
	if err := r.full(hdr[:]); err != nil || string(hdr[:]) != ipsMagic {
		return ErrCorrupt
	}
	var recs []ipsRecord
	var end int64
	for {
		if err := r.full(hdr[:3]); err != nil {
			return err
		}
		if string(hdr[:3]) == "EOF" {
			break
		}
		rec := ipsRecord{off: int64(hdr[0])<<16 | int64(hdr[1])<<8 | int64(hdr[2])}
		if err := r.full(hdr[:2]); err != nil {
			return err
		}
		if size := int(hdr[0])<<8 | int(hdr[1]); size > 0 {
			rec.data = make([]byte, size)
			if err := r.full(rec.data); err != nil {
				return err
			}
			rec.n = int64(size)
		} else {
			if err := r.full(hdr[:3]); err != nil {
				return err
			}
			rec.n = int64(hdr[0])<<8 | int64(hdr[1])
			rec.val = hdr[2]
		}
		recs = append(recs, rec)
		end = max(end, rec.off+rec.n)
		if len(recs)%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
	}
	size := max(srcSize, end)
	// the truncate size; what follows it is ignored
	if n, _ := io.ReadFull(r.br, hdr[:3]); n == 3 {
		size = int64(hdr[0])<<16 | int64(hdr[1])<<8 | int64(hdr[2])
	}
	buf := make([]byte, min(int64(ioBuf), max(size, 1)))
	for pos := int64(0); pos < size; {
		if err := ctx.Err(); err != nil {
			return err
		}
		p := buf[:min(int64(len(buf)), size-pos)]
		k := min(max(srcSize-pos, 0), int64(len(p)))
		if err := readAt(src, p[:k], pos); err != nil {
			return err
		}
		clear(p[k:])
		if pos < end {
			// in the order of the patch, so later records win
			for _, rec := range recs {
				s, e := max(rec.off, pos), min(rec.off+rec.n, pos+int64(len(p)))
				if s >= e {
					continue
				}
				if rec.data == nil {
					for i := s; i < e; i++ {
						p[i-pos] = rec.val
					}
				} else {
					copy(p[s-pos:e-pos], rec.data[s-rec.off:])
				}
			}
		}
		if _, err := w.Write(p); err != nil {
			return err
		}
		pos += int64(len(p))
	}
	return nil
}
