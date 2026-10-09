package checksum

import (
	"context"
	"io"
	"math"
	"math/bits"
	"runtime"
	"sync"
	"sync/atomic"
)

// Stats is how many times each byte value occurs
type Stats struct {
	Count     int64
	Histogram [256]int64
}

// Add counts the bytes
func (s *Stats) Add(b []byte) {
	for len(b) > 0 {
		// Four tables in turn: counting into one table makes each count
		// wait for the one before when the same byte repeats. uint32 is
		// enough for a piece of 1 GB.
		k := min(len(b), 1<<30)
		var h [4][256]uint32
		p := b[:k]
		for len(p) >= 4 {
			h[0][p[0]]++
			h[1][p[1]]++
			h[2][p[2]]++
			h[3][p[3]]++
			p = p[4:]
		}
		for _, v := range p {
			h[0][v]++
		}
		for i := range 256 {
			s.Histogram[i] += int64(h[0][i]) + int64(h[1][i]) + int64(h[2][i]) + int64(h[3][i])
		}
		s.Count += int64(k)
		b = b[k:]
	}
}

// Merge adds the counts of o
func (s *Stats) Merge(o *Stats) {
	s.Count += o.Count
	for i, c := range o.Histogram {
		s.Histogram[i] += c
	}
}

// Entropy is the Shannon entropy in bits per byte, 0..8: 0 when all the
// bytes are the same, 8 when all the values are equally often
func (s *Stats) Entropy() float64 {
	if s.Count == 0 {
		return 0
	}
	total := float64(s.Count)
	e := 0.0
	for _, c := range s.Histogram {
		if c > 0 {
			p := float64(c) / total
			e -= p * math.Log2(p)
		}
	}
	return max(0, min(8, e))
}

// Analyze counts the bytes of [off, off+n) of r. The parts are counted in
// parallel and merged. progress (may be nil) gets the bytes done; it is
// called on the goroutine of Analyze. ctx cancels.
func Analyze(ctx context.Context, r io.ReaderAt, off, n int64, progress func(done int64)) (Stats, error) {
	if off < 0 || n < 0 {
		return Stats{}, ErrRange
	}
	if err := ctx.Err(); err != nil {
		return Stats{}, err
	}
	if n == 0 {
		return Stats{}, nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	parts := (n + partSize - 1) / partSize
	workers := int(min(int64(runtime.GOMAXPROCS(0)), parts))
	partial := make([]Stats, workers)
	donec := make(chan int64, workers)
	errc := make(chan error, workers)
	var next atomic.Int64
	var wg sync.WaitGroup
	for w := range workers {
		wg.Go(func() {
			buf := make([]byte, min(partSize, n))
			for ctx.Err() == nil {
				i := next.Add(1) - 1
				if i >= parts {
					return
				}
				pos := i * partSize
				k := min(partSize, n-pos)
				if err := readFull(r, buf[:k], off+pos); err != nil {
					errc <- err
					return
				}
				partial[w].Add(buf[:k])
				select {
				case donec <- k:
				case <-ctx.Done():
					return
				}
			}
		})
	}

	var done int64
	var err error
	for done < n && err == nil {
		select {
		case k := <-donec:
			done += k
			if progress != nil {
				progress(done)
			}
		case err = <-errc:
		case <-ctx.Done():
			err = ctx.Err()
		}
	}
	cancel()
	wg.Wait()
	if err != nil {
		return Stats{}, err
	}
	var s Stats
	for i := range partial {
		s.Merge(&partial[i])
	}
	return s, nil
}

// samplePiece is the size of a piece read when a block is sampled
const samplePiece = 4096

// EntropyBlocks splits [off, off+n) into the given number of blocks of
// (almost) equal size and returns the entropy of each, e.g. for a minimap.
// A block larger than sampleMax bytes is not read whole: only sampleMax
// bytes of it are, in pieces of 4 KB spread evenly over the block, so the
// time does not depend on the size of the file. sampleMax <= 0 reads all.
// Blocks are done in parallel; ctx cancels.
func EntropyBlocks(ctx context.Context, r io.ReaderAt, off, n int64, blocks int, sampleMax int64) ([]float64, error) {
	if off < 0 || n < 0 {
		return nil, ErrRange
	}
	if blocks <= 0 {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// bound is the start of block i; n*i may not fit in int64
	bound := func(i int) int64 {
		hi, lo := bits.Mul64(uint64(n), uint64(i))
		q, _ := bits.Div64(hi, lo, uint64(blocks))
		return int64(q)
	}
	res := make([]float64, blocks)
	bufSize := int64(partSize)
	if sampleMax > 0 {
		bufSize = min(bufSize, samplePiece)
	}
	bufSize = max(1, min(bufSize, n))

	var next atomic.Int64
	var errOnce sync.Once
	var firstErr error
	var wg sync.WaitGroup
	workers := min(runtime.GOMAXPROCS(0), blocks)
	for range workers {
		wg.Go(func() {
			buf := make([]byte, bufSize)
			for ctx.Err() == nil {
				i := int(next.Add(1) - 1)
				if i >= blocks {
					return
				}
				start, end := bound(i), bound(i+1)
				var s Stats
				if err := statBlock(ctx, r, off+start, end-start, sampleMax, buf, &s); err != nil {
					errOnce.Do(func() { firstErr = err })
					cancel()
					return
				}
				res[i] = s.Entropy()
			}
		})
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return res, nil
}

// statBlock counts a block of size bytes at off, or a sample of it
func statBlock(ctx context.Context, r io.ReaderAt, off, size, sampleMax int64, buf []byte, s *Stats) error {
	if sampleMax <= 0 || size <= sampleMax {
		for pos := int64(0); pos < size; {
			if err := ctx.Err(); err != nil {
				return err
			}
			k := min(int64(len(buf)), size-pos)
			if err := readFull(r, buf[:k], off+pos); err != nil {
				return err
			}
			s.Add(buf[:k])
			pos += k
		}
		return nil
	}
	pieces := (sampleMax + samplePiece - 1) / samplePiece
	piece := min(sampleMax/pieces, int64(len(buf)))
	for j := range pieces {
		if err := ctx.Err(); err != nil {
			return err
		}
		pos := int64(0)
		if pieces > 1 {
			pos = (size - piece) / (pieces - 1) * j
		}
		if err := readFull(r, buf[:piece], off+pos); err != nil {
			return err
		}
		s.Add(buf[:piece])
	}
	return nil
}
