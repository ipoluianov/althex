package fs

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"
	"unicode/utf16"
)

// The helpers the ext and NTFS readers share: runs of a file and reading
// them, bounds-checked reads of the volume. The names have the x prefix so
// they do not meet those of the other readers of the package.

var errXDamaged = errors.New("damaged file system")

// xDamaged makes an error of a damaged structure
func xDamaged(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errXDamaged, fmt.Sprintf(format, args...))
}

func xLE16(b []byte) uint16 { return binary.LittleEndian.Uint16(b) }
func xLE32(b []byte) uint32 { return binary.LittleEndian.Uint32(b) }
func xLE64(b []byte) uint64 { return binary.LittleEndian.Uint64(b) }

// xRead reads n bytes of the volume at off; a short read is an error,
// so a structure that points past the end of an image is caught here
func xRead(r io.ReaderAt, size, off, n int64) ([]byte, error) {
	if off < 0 || n < 0 || off > size || n > size-off {
		return nil, xDamaged("read of %d bytes at %d is outside the volume", n, off)
	}
	b := make([]byte, n)
	got, err := r.ReadAt(b, off)
	if got == len(b) {
		return b, nil
	}
	if err == nil || err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return nil, err
}

// xNormalize sorts the runs of a file, fills the gaps with holes and makes
// them end at size: the result covers [0, size) exactly. Runs that overlap
// are a damaged file.
func xNormalize(runs []Extent, size int64) ([]Extent, error) {
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].FileOff < runs[j].FileOff })
	var out []Extent
	add := func(e Extent) {
		if e.Len <= 0 {
			return
		}
		if n := len(out); n > 0 {
			p := &out[n-1]
			if p.DiskOff == -1 && e.DiskOff == -1 || p.DiskOff >= 0 && p.DiskOff+p.Len == e.DiskOff {
				p.Len += e.Len
				return
			}
		}
		out = append(out, e)
	}
	var pos int64
	for _, e := range runs {
		if e.FileOff < pos {
			return nil, xDamaged("runs of a file overlap at %d", e.FileOff)
		}
		if pos >= size {
			break
		}
		if e.FileOff > pos {
			add(Extent{FileOff: pos, DiskOff: -1, Len: min(e.FileOff, size) - pos})
			pos = min(e.FileOff, size)
			if pos >= size {
				break
			}
		}
		if e.FileOff+e.Len > size {
			e.Len = size - e.FileOff
		}
		add(e)
		pos = e.FileOff + e.Len
	}
	if pos < size {
		add(Extent{FileOff: pos, DiskOff: -1, Len: size - pos})
	}
	return out, nil
}

// xRunsReader reads a file from runs that cover it (xNormalize); holes
// read as zeros
type xRunsReader struct {
	r    io.ReaderAt
	runs []Extent
	size int64
}

func (x *xRunsReader) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("negative offset")
	}
	if off >= x.size {
		return 0, io.EOF
	}
	var eof error
	if int64(len(p)) > x.size-off {
		p = p[:x.size-off]
		eof = io.EOF
	}
	i := sort.Search(len(x.runs), func(i int) bool { return x.runs[i].FileOff+x.runs[i].Len > off })
	n := 0
	for n < len(p) {
		if i >= len(x.runs) {
			return n, io.ErrUnexpectedEOF
		}
		e := x.runs[i]
		at := off + int64(n)
		in := at - e.FileOff
		m := min(int64(len(p)-n), e.Len-in)
		if e.DiskOff < 0 {
			clear(p[n : n+int(m)])
		} else {
			got, err := x.r.ReadAt(p[n:n+int(m)], e.DiskOff+in)
			if int64(got) < m {
				if err == nil || err == io.EOF {
					err = io.ErrUnexpectedEOF
				}
				return n + got, err
			}
		}
		n += int(m)
		i++
	}
	return n, eof
}

// xReadAll reads a whole file that is not larger than limit
func xReadAll(r io.ReaderAt, size, limit int64) ([]byte, error) {
	if size > limit {
		return nil, xDamaged("%d bytes is too large", size)
	}
	b := make([]byte, size)
	n, err := r.ReadAt(b, 0)
	if n == len(b) {
		return b, nil
	}
	if err == nil || err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return nil, err
}

// xUTF16 decodes UTF-16LE text
func xUTF16(b []byte) string {
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = xLE16(b[2*i:])
	}
	return string(utf16.Decode(u))
}

// xSplit splits a path into its names; "." is dropped and ".." goes up
func xSplit(path string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(path); i++ {
		if i < len(path) && path[i] != '/' {
			continue
		}
		s := path[start:i]
		start = i + 1
		switch s {
		case "", ".":
		case "..":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
		default:
			out = append(out, s)
		}
	}
	return out
}

// xJoin makes the path of the names
func xJoin(names []string) string {
	if len(names) == 0 {
		return "/"
	}
	s := ""
	for _, n := range names {
		s += "/" + n
	}
	return s
}

// xFileTime converts an NTFS time (100 ns since 1601)
func xFileTime(ft uint64) time.Time {
	if ft == 0 {
		return time.Time{}
	}
	const unixFrom1601 = 11644473600
	return time.Unix(int64(ft/1e7)-unixFrom1601, int64(ft%1e7)*100).UTC()
}
