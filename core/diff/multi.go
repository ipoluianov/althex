package diff

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"sync"
)

// MaxInputs is how many inputs CompareN takes at once
const MaxInputs = 64

// Span is a part of the inputs of CompareN where some of them differ from
// the first one. Mask has bit i set for each input i that differs there;
// bit 0, the first input, is set in every span.
type Span struct {
	Off, Len int64
	Mask     uint64
}

// End is the offset after the span
func (s Span) End() int64 { return s.Off + s.Len }

// Has tells whether input i differs in the span
func (s Span) Has(i int) bool { return i >= 0 && i < MaxInputs && s.Mask&(1<<uint(i)) != 0 }

// CompareN compares several inputs at the same offsets (DIFF-1), each with
// the first one. A byte of input i differs when it is not the byte of the
// first input at its offset, or when only one of the two has that offset
// (the other one is shorter). Returns the spans of differences in order;
// neighbours of the same mask are joined. Progress, if not nil, is called
// from time to time with the offset done, up to the size of the longest
// input. Of the options only MaxRanges is used: it limits the spans, and
// then ErrMaxRanges comes with the spans found so far.
func CompareN(ctx context.Context, inputs []io.ReaderAt, sizes []int64, opt Options, progress func(done int64)) ([]Span, error) {
	n := len(inputs)
	if n < 2 || n > MaxInputs || len(sizes) != n {
		return nil, fmt.Errorf("diff: 2 to %d inputs, each with its size", MaxInputs)
	}
	total := int64(0)
	for _, s := range sizes {
		if s < 0 {
			return nil, errors.New("diff: negative size")
		}
		total = max(total, s)
	}
	if progress == nil {
		progress = func(int64) {}
	}
	limit := max(opt.MaxRanges, 0)
	var spans []Span
	emit := func(off, end int64, mask uint64) bool {
		if k := len(spans) - 1; k >= 0 && spans[k].End() == off && spans[k].Mask == mask {
			spans[k].Len = end - spans[k].Off
			return true
		}
		if limit > 0 && len(spans) >= limit {
			return false
		}
		spans = append(spans, Span{Off: off, Len: end - off, Mask: mask})
		return true
	}

	base := make([]byte, min(chunkSize, total))
	bufs := make([][]byte, n)
	runs := make([][]run, n)
	errs := make([]error, n)
	type event struct {
		at  int64
		bit uint64
	}
	var events []event
	for off := int64(0); off < total; {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := min(off+chunkSize, total)
		baseEnd := min(max(sizes[0], off), end)
		if err := readAt(inputs[0], base[:baseEnd-off], off); err != nil {
			return nil, err
		}
		var wg sync.WaitGroup
		for i := 1; i < n; i++ {
			wg.Go(func() {
				runs[i], errs[i] = diffInput(inputs[i], sizes[i], base[:baseEnd-off], off, end, &bufs[i], runs[i][:0])
			})
		}
		wg.Wait()
		events = events[:0]
		for i := 1; i < n; i++ {
			if errs[i] != nil {
				return nil, errs[i]
			}
			for _, r := range runs[i] {
				events = append(events, event{r.start, 1 << uint(i)}, event{r.end, 1 << uint(i)})
			}
		}
		slices.SortFunc(events, func(x, y event) int { return cmp.Compare(x.at, y.at) })
		// The runs of an input do not overlap: each one starts and ends its bit
		mask := uint64(0)
		for k := 0; k < len(events); {
			at := events[k].at
			for k < len(events) && events[k].at == at {
				mask ^= events[k].bit
				k++
			}
			if k < len(events) && mask != 0 && events[k].at > at {
				if !emit(at, events[k].at, mask|1) {
					return spans, ErrMaxRanges
				}
			}
		}
		off = end
		progress(off)
	}
	return spans, nil
}

// diffInput finds where input r of the size differs from the bytes of the
// first input in [off, end) (base: as many of them as the first input has)
func diffInput(r io.ReaderAt, size int64, base []byte, off, end int64, buf *[]byte, out []run) ([]run, error) {
	baseEnd := off + int64(len(base))
	inEnd := min(max(size, off), end)
	common := min(baseEnd, inEnd)
	if common > off {
		if int64(cap(*buf)) < common-off {
			*buf = make([]byte, common-off)
		}
		p := (*buf)[:common-off]
		if err := readAt(r, p, off); err != nil {
			return out, err
		}
		out = diffRuns(base[:common-off], p, off, out)
	}
	// Past the end of one of the two
	if hi := max(baseEnd, inEnd); hi > common {
		if k := len(out) - 1; k >= 0 && out[k].end == common {
			out[k].end = hi
		} else {
			out = append(out, run{common, hi})
		}
	}
	return out, nil
}

// Regions joins the spans that touch into one, their masks together: the
// differences as they are gone through one after another
func Regions(spans []Span) []Span {
	var out []Span
	for _, s := range spans {
		if k := len(out) - 1; k >= 0 && out[k].End() == s.Off {
			out[k].Len += s.Len
			out[k].Mask |= s.Mask
			continue
		}
		out = append(out, s)
	}
	return out
}

// SpansIn returns the index of the first span that ends after off
func SpansIn(spans []Span, off int64) int {
	return sort.Search(len(spans), func(i int) bool { return spans[i].End() > off })
}

// WriteReportN writes a readable report of CompareN: the inputs with their
// sizes, then each difference (Regions) with its offset in hex and up to
// maxBytes of each input in hex, the first one and the ones that differ
// there. names may be shorter than the inputs: then the inputs are numbered.
func WriteReportN(w io.Writer, names []string, spans []Span, inputs []io.ReaderAt, sizes []int64, maxBytes int) error {
	bw := bufio.NewWriter(w)
	name := func(i int) string {
		if i < len(names) && names[i] != "" {
			return fmt.Sprintf("[%d] %s", i, names[i])
		}
		return fmt.Sprintf("[%d]", i)
	}
	for i := range inputs {
		fmt.Fprintf(bw, "%s: %d bytes\n", name(i), sizes[i])
	}
	regions := Regions(spans)
	total := int64(0)
	for _, r := range regions {
		total += r.Len
	}
	fmt.Fprintf(bw, "Differing ranges: %d, %d bytes\n", len(regions), total)
	buf := make([]byte, max(maxBytes, 0))
	for k, r := range regions {
		fmt.Fprintf(bw, "\n#%d 0x%08X+%d\n", k+1, r.Off, r.Len)
		for i := range inputs {
			if i > 0 && !r.Has(i) {
				continue
			}
			if r.Off >= sizes[i] {
				fmt.Fprintf(bw, "  %s: -\n", name(i))
				continue
			}
			if err := hexSide(bw, name(i), inputs[i], r.Off, min(r.Len, sizes[i]-r.Off), buf); err != nil {
				return err
			}
		}
	}
	return bw.Flush()
}
