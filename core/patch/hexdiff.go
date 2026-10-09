package patch

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/ipoluianov/althex/core/diff"
)

// HexDiff is text in the spirit of the unified diff:
//
//	--- a/<name> <size>
//	+++ b/<name> <size>
//	@@ -0x00000010,4 +0x00000010,5 @@
//	-4D 5A 90 00
//	+4D 5A 91 00 FF
//
// A hunk is a differing range: the offset in the source in hex and the
// length, the same for the target, then the bytes of the source ('-' lines)
// and of the target ('+' lines), up to 16 a line. There are no context
// lines: the '-' bytes are what Apply checks against the source.

const hexLine = 16

// MakeHexDiff is Make for HexDiff with the names to show in the header
func MakeHexDiff(ctx context.Context, w io.Writer, srcName string, src io.ReaderAt, srcSize int64, dstName string, dst io.ReaderAt, dstSize int64, progress func(done int64)) error {
	rs, err := diff.Compare(ctx, src, srcSize, dst, dstSize, diff.Options{Shifts: true, MergeGap: 8}, progress)
	if err != nil {
		return err
	}
	bw := bufio.NewWriter(w)
	fmt.Fprintf(bw, "--- a/%s %d\n+++ b/%s %d\n", cleanName(srcName), srcSize, cleanName(dstName), dstSize)
	buf := make([]byte, 64<<10)
	for _, r := range rs {
		if r.Kind == diff.Equal {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		fmt.Fprintf(bw, "@@ -0x%08X,%d +0x%08X,%d @@\n", r.AOff, r.ALen, r.BOff, r.BLen)
		if err := hexLines(bw, '-', src, r.AOff, r.ALen, buf); err != nil {
			return err
		}
		if err := hexLines(bw, '+', dst, r.BOff, r.BLen, buf); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// cleanName keeps the header on one line
func cleanName(s string) string {
	return strings.Map(func(r rune) rune {
		if r < ' ' || r == 0x7f {
			return '?'
		}
		return r
	}, s)
}

func hexLines(w *bufio.Writer, sign byte, r io.ReaderAt, off, n int64, buf []byte) error {
	const digits = "0123456789ABCDEF"
	for n > 0 {
		p := buf[:min(int64(len(buf)), n)]
		if err := readAt(r, p, off); err != nil {
			return err
		}
		for i := 0; i < len(p); i += hexLine {
			w.WriteByte(sign)
			for j, c := range p[i:min(i+hexLine, len(p))] {
				if j > 0 {
					w.WriteByte(' ')
				}
				w.WriteByte(digits[c>>4])
				w.WriteByte(digits[c&15])
			}
			w.WriteByte('\n')
		}
		off += int64(len(p))
		n -= int64(len(p))
	}
	return nil
}

// srcWindow reads the source forwards in pieces, to check '-' lines
type srcWindow struct {
	r        io.ReaderAt
	size     int64
	buf      []byte
	off, len int64
}

func (s *srcWindow) get(off int64, n int) ([]byte, error) {
	if off < s.off || off+int64(n) > s.off+s.len {
		k := min(int64(len(s.buf)), s.size-off)
		if k < int64(n) {
			return nil, ErrCorrupt
		}
		if err := readAt(s.r, s.buf[:k], off); err != nil {
			return nil, err
		}
		s.off, s.len = off, k
	}
	i := off - s.off
	return s.buf[i : i+int64(n)], nil
}

// parseHex reads bytes as pairs of hex digits split by spaces
func parseHex(s []byte, out []byte) ([]byte, error) {
	out = out[:0]
	for i := 0; i < len(s); {
		if s[i] == ' ' {
			i++
			continue
		}
		if i+1 >= len(s) {
			return nil, ErrCorrupt
		}
		hi, ok1 := unhex(s[i])
		lo, ok2 := unhex(s[i+1])
		if !ok1 || !ok2 {
			return nil, ErrCorrupt
		}
		out = append(out, hi<<4|lo)
		i += 2
	}
	return out, nil
}

func unhex(c byte) (byte, bool) {
	switch {
	case '0' <= c && c <= '9':
		return c - '0', true
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10, true
	case 'A' <= c && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

// headerSize reads the size at the end of a "--- " or "+++ " line
func headerSize(line []byte, prefix string) (int64, error) {
	if !bytes.HasPrefix(line, []byte(prefix)) {
		return 0, ErrCorrupt
	}
	i := bytes.LastIndexByte(line, ' ')
	v, err := strconv.ParseInt(string(line[i+1:]), 10, 64)
	if err != nil || v < 0 || i < len(prefix) {
		return 0, ErrCorrupt
	}
	return v, nil
}

// parseSide reads "-0x10,4" of a hunk header
func parseSide(f string, sign byte) (off, n int64, err error) {
	if len(f) < 3 || f[0] != sign || (f[1:3] != "0x" && f[1:3] != "0X") {
		return 0, 0, ErrCorrupt
	}
	a, b, ok := strings.Cut(f[3:], ",")
	if !ok {
		return 0, 0, ErrCorrupt
	}
	off, err1 := strconv.ParseInt(a, 16, 64)
	n, err2 := strconv.ParseInt(b, 10, 64)
	if err1 != nil || err2 != nil || off < 0 || n < 0 {
		return 0, 0, ErrCorrupt
	}
	return off, n, nil
}

type hunk struct {
	aOff, aLen, bOff, bLen int64
	minus, plus            int64 // the bytes read so far
}

func applyHexDiff(ctx context.Context, patch io.Reader, w io.Writer, src io.ReaderAt, srcSize int64) error {
	br := bufio.NewReaderSize(patch, 64<<10)
	readLine := func() ([]byte, error) {
		line, err := br.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			return nil, ErrCorrupt
		}
		if err != nil && (err != io.EOF || len(line) == 0) {
			return nil, err
		}
		line = bytes.TrimSuffix(line, []byte("\n"))
		return bytes.TrimSuffix(line, []byte("\r")), nil
	}
	line, err := readLine()
	if err != nil {
		return eofCorrupt(err)
	}
	ss, err := headerSize(line, "--- ")
	if err != nil {
		return err
	}
	if ss != srcSize {
		return ErrSourceMismatch
	}
	if line, err = readLine(); err != nil {
		return eofCorrupt(err)
	}
	dstSize, err := headerSize(line, "+++ ")
	if err != nil {
		return err
	}
	bw := bufio.NewWriterSize(w, 64<<10)
	sw := &srcWindow{r: src, size: srcSize, buf: make([]byte, 64<<10)}
	buf := make([]byte, 64<<10)
	var aPos, out int64 // the source taken, the target written
	var h *hunk
	finish := func() error {
		if h != nil && (h.minus != h.aLen || h.plus != h.bLen) {
			return ErrCorrupt
		}
		h = nil
		return nil
	}
	var bytesBuf []byte
	for k := 0; ; k++ {
		line, err := readLine()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if k%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		switch {
		case len(line) == 0:
		case bytes.HasPrefix(line, []byte("@@ ")):
			if err := finish(); err != nil {
				return err
			}
			f := strings.Fields(string(line))
			if len(f) != 4 || f[3] != "@@" {
				return ErrCorrupt
			}
			h = &hunk{}
			if h.aOff, h.aLen, err = parseSide(f[1], '-'); err != nil {
				return err
			}
			if h.bOff, h.bLen, err = parseSide(f[2], '+'); err != nil {
				return err
			}
			if h.aOff < aPos || h.aOff > srcSize-h.aLen {
				return ErrCorrupt
			}
			if err := copyFrom(ctx, bw, src, aPos, h.aOff-aPos, buf); err != nil {
				return err
			}
			out += h.aOff - aPos
			aPos = h.aOff
			if h.bOff != out {
				return ErrCorrupt
			}
		case line[0] == '-' || line[0] == '+':
			if h == nil {
				return ErrCorrupt
			}
			if bytesBuf, err = parseHex(line[1:], bytesBuf); err != nil {
				return err
			}
			n := int64(len(bytesBuf))
			if line[0] == '-' {
				if h.plus > 0 || h.minus+n > h.aLen {
					return ErrCorrupt
				}
				have, err := sw.get(aPos, len(bytesBuf))
				if err != nil {
					return err
				}
				if !bytes.Equal(have, bytesBuf) {
					return ErrSourceMismatch
				}
				h.minus += n
				aPos += n
			} else {
				if h.plus+n > h.bLen {
					return ErrCorrupt
				}
				if _, err := bw.Write(bytesBuf); err != nil {
					return err
				}
				h.plus += n
				out += n
			}
		default:
			return ErrCorrupt
		}
	}
	if err := finish(); err != nil {
		return err
	}
	if err := copyFrom(ctx, bw, src, aPos, srcSize-aPos, buf); err != nil {
		return err
	}
	if out+srcSize-aPos != dstSize {
		return ErrCorrupt
	}
	return bw.Flush()
}
