package diff

import (
	"bufio"
	"fmt"
	"io"
)

// WriteReport writes a readable text report: the summary, then each
// differing range with its offsets in hex and up to maxBytes of each side in
// hex. A or B may be nil, then their bytes are not shown.
func WriteReport(w io.Writer, rs []Range, a, b io.ReaderAt, maxBytes int) error {
	bw := bufio.NewWriter(w)
	s := Summarize(rs)
	var aSize, bSize int64
	if n := len(rs); n > 0 {
		aSize, bSize = rs[n-1].AEnd(), rs[n-1].BEnd()
	}
	fmt.Fprintf(bw, "A: %d bytes, B: %d bytes\n", aSize, bSize)
	fmt.Fprintf(bw, "Differing ranges: %d\n", s.Ranges)
	fmt.Fprintf(bw, "Equal: %d, changed: %d, inserted: %d, deleted: %d bytes\n", s.Equal, s.Changed, s.Inserted, s.Deleted)
	buf := make([]byte, max(maxBytes, 0))
	k := 0
	for _, r := range rs {
		if r.Kind == Equal {
			continue
		}
		k++
		fmt.Fprintf(bw, "\n#%d %s A 0x%08X+%d B 0x%08X+%d\n", k, r.Kind, r.AOff, r.ALen, r.BOff, r.BLen)
		if err := hexSide(bw, "A", a, r.AOff, r.ALen, buf); err != nil {
			return err
		}
		if err := hexSide(bw, "B", b, r.BOff, r.BLen, buf); err != nil {
			return err
		}
	}
	return bw.Flush()
}

func hexSide(w *bufio.Writer, name string, r io.ReaderAt, off, n int64, buf []byte) error {
	if r == nil || n == 0 || len(buf) == 0 {
		return nil
	}
	p := buf[:min(int64(len(buf)), n)]
	if err := readAt(r, p, off); err != nil {
		return err
	}
	fmt.Fprintf(w, "  %s: % X", name, p)
	if int64(len(p)) < n {
		w.WriteString(" ...")
	}
	w.WriteByte('\n')
	return nil
}
