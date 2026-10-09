package diff

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"runtime"
	"sync"
)

const (
	// chunkSize is what one goroutine compares at a time
	chunkSize = 2 << 20
	// cmpBlock is the step of the fast path: most blocks of similar files are equal
	cmpBlock   = 64 << 10
	maxWorkers = 8
)

// run is a differing span [start, end) at the same offsets of A and B
type run struct{ start, end int64 }

// compareBytes compares A and B at the same offsets (DIFF-1). The common part
// is cut into chunks that several goroutines compare at once; their results
// are joined in order, a round of chunks at a time, so the memory is bounded
// by the buffers of the goroutines. When the sizes differ, the tail of the
// longer input is Inserted or Deleted.
func compareBytes(ctx context.Context, a io.ReaderAt, aSize int64, b io.ReaderAt, bSize int64, bl *builder, progress func(int64)) error {
	n := min(aSize, bSize)
	workers := min(max(runtime.GOMAXPROCS(0), 1), maxWorkers)
	if c := int((n + chunkSize - 1) / chunkSize); c < workers {
		workers = max(c, 1)
	}
	bufs := make([][2][]byte, workers)
	type result struct {
		runs []run
		err  error
	}
	res := make([]result, workers)
	pos := int64(0) // everything before it is in the builder
	for start := int64(0); start < n; {
		if err := ctx.Err(); err != nil {
			return err
		}
		var wg sync.WaitGroup
		k := 0
		for ; k < workers && start+int64(k)*chunkSize < n; k++ {
			off := start + int64(k)*chunkSize
			ln := min(chunkSize, n-off)
			if bufs[k][0] == nil {
				bufs[k] = [2][]byte{make([]byte, chunkSize), make([]byte, chunkSize)}
			}
			i := k
			wg.Go(func() {
				x, y := bufs[i][0][:ln], bufs[i][1][:ln]
				r := result{runs: res[i].runs[:0]}
				if r.err = readAt(a, x, off); r.err == nil {
					r.err = readAt(b, y, off)
				}
				if r.err == nil {
					r.runs = diffRuns(x, y, off, r.runs)
				}
				res[i] = r
			})
		}
		wg.Wait()
		for i := range k {
			if res[i].err != nil {
				return res[i].err
			}
			for _, r := range res[i].runs {
				bl.equal(pos, pos, r.start-pos)
				if err := bl.differ(r.start, r.end-r.start, r.start, r.end-r.start); err != nil {
					return err
				}
				pos = r.end
			}
		}
		start = min(start+int64(k)*chunkSize, n)
		progress(start)
	}
	bl.equal(pos, pos, n-pos)
	if aSize > n {
		return bl.differ(n, aSize-n, n, 0)
	}
	if bSize > n {
		return bl.differ(n, 0, n, bSize-n)
	}
	return nil
}

// diffRuns appends the differing spans of x and y; base is their offset
func diffRuns(x, y []byte, base int64, runs []run) []run {
	for o := 0; o < len(x); o += cmpBlock {
		e := min(o+cmpBlock, len(x))
		if bytes.Equal(x[o:e], y[o:e]) {
			continue
		}
		i := o
		for i < e {
			// skip equal bytes a word at a time
			for i+8 <= e && binary.LittleEndian.Uint64(x[i:]) == binary.LittleEndian.Uint64(y[i:]) {
				i += 8
			}
			for i < e && x[i] == y[i] {
				i++
			}
			if i == e {
				break
			}
			j := i
			for j < e && x[j] != y[j] {
				j++
			}
			s, t := base+int64(i), base+int64(j)
			if n := len(runs); n > 0 && runs[n-1].end == s {
				runs[n-1].end = t
			} else {
				runs = append(runs, run{s, t})
			}
			i = j
		}
	}
	return runs
}
