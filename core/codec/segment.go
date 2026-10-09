package codec

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Segment is a run of bytes at an address
type Segment struct {
	Addr uint64
	Data []byte
}

// maxAddr is the end of the address space of Intel HEX and S-records
const maxAddr = 1 << 32

// Bounds returns the lowest address of the segments and the address after
// the highest byte; 0, 0 for no data. Flatten makes a block of hi-lo bytes,
// so check it before flattening the segments of an untrusted file.
func Bounds(segs []Segment) (lo, hi uint64) {
	first := true
	for _, s := range segs {
		if len(s.Data) == 0 {
			continue
		}
		end := s.Addr + uint64(len(s.Data))
		if first || s.Addr < lo {
			lo = s.Addr
		}
		if first || end > hi {
			hi = end
		}
		first = false
	}
	return lo, hi
}

// Flatten makes one block from the lowest to the highest address of the
// segments, the gaps filled with fill. Where segments overlap, the later wins.
func Flatten(segs []Segment, fill byte) (base uint64, data []byte) {
	lo, hi := Bounds(segs)
	if hi == lo {
		return 0, nil
	}
	data = make([]byte, hi-lo)
	if fill != 0 {
		for i := range data {
			data[i] = fill
		}
	}
	for _, s := range segs {
		if len(s.Data) > 0 {
			copy(data[s.Addr-lo:], s.Data)
		}
	}
	return lo, data
}

// segBuilder collects the data records of a file into segments. Records
// usually follow each other, so most of them are appended to the last segment.
type segBuilder struct {
	segs  []Segment
	lines []int // the line each segment starts at, for errors
}

func (b *segBuilder) add(addr uint64, data []byte, line int) error {
	if addr+uint64(len(data)) > maxAddr {
		return fmt.Errorf("line %d: data beyond the 4 GiB address space", line)
	}
	if len(data) == 0 {
		return nil
	}
	if n := len(b.segs); n > 0 {
		last := &b.segs[n-1]
		if last.Addr+uint64(len(last.Data)) == addr {
			last.Data = append(last.Data, data...)
			return nil
		}
	}
	b.segs = append(b.segs, Segment{addr, append([]byte(nil), data...)})
	b.lines = append(b.lines, line)
	return nil
}

// result sorts the segments by address and merges the contiguous ones.
// Overlapping data is an error: a file that writes a byte twice is broken.
func (b *segBuilder) result() ([]Segment, error) {
	idx := make([]int, len(b.segs))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(i, j int) bool { return b.segs[idx[i]].Addr < b.segs[idx[j]].Addr })
	var out []Segment
	for _, i := range idx {
		s := b.segs[i]
		if n := len(out); n > 0 {
			last := &out[n-1]
			end := last.Addr + uint64(len(last.Data))
			if s.Addr < end {
				return nil, fmt.Errorf("line %d: data at 0x%X overlaps earlier data", b.lines[i], s.Addr)
			}
			if s.Addr == end {
				last.Data = append(last.Data, s.Data...)
				continue
			}
		}
		out = append(out, s)
	}
	return out, nil
}

// lineReader gives the lines of a text file with their numbers, trimmed
type lineReader struct {
	sc   *bufio.Scanner
	line int
}

func newLineReader(r io.Reader) *lineReader {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	return &lineReader{sc: sc}
}

func (l *lineReader) next() (string, bool) {
	if !l.sc.Scan() {
		return "", false
	}
	l.line++
	return strings.TrimSpace(l.sc.Text()), true
}

func (l *lineReader) err() error {
	if err := l.sc.Err(); err != nil {
		return fmt.Errorf("line %d: %w", l.line+1, err)
	}
	return nil
}

// decodeHexBytes decodes a record without its start mark
func decodeHexBytes(s string, line int) ([]byte, error) {
	if len(s)%2 != 0 {
		return nil, fmt.Errorf("line %d: odd number of hex digits", line)
	}
	out := make([]byte, len(s)/2)
	for i := range out {
		hi, ok1 := hexVal(s[2*i])
		lo, ok2 := hexVal(s[2*i+1])
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("line %d: not a hex digit in %q", line, s[2*i:2*i+2])
		}
		out[i] = hi<<4 | lo
	}
	return out, nil
}

func hexVal(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

const hexDigits = "0123456789ABCDEF"

func appendHexByte(dst []byte, b byte) []byte {
	return append(dst, hexDigits[b>>4], hexDigits[b&15])
}
