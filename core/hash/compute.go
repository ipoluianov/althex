package checksum

import (
	"context"
	"errors"
	gohash "hash"
	"io"
	"sync"
	"sync/atomic"
)

// ErrRange is returned for a negative offset or length
var ErrRange = errors.New("checksum: negative offset or length")

// partSize is how much is read at a time
const partSize = 1 << 20

// partsInFlight is how many parts are read ahead of the slowest algorithm,
// so that reading and hashing overlap
const partsInFlight = 4

// readFull reads exactly len(b) bytes at off; a short read is an error,
// since the size of the data was known before
func readFull(r io.ReaderAt, b []byte, off int64) error {
	m, err := r.ReadAt(b, off)
	if m == len(b) {
		return nil
	}
	if err == nil || err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return err
}

type part struct {
	buf  []byte
	data []byte
	left atomic.Int32 // algorithms still to take the part
}

// Compute hashes [off, off+n) of r with all the algorithms in one pass. It
// reads 1 MB at a time and feeds each algorithm on its own goroutine, so
// the time is that of the slowest one, not the sum. progress (may be nil)
// gets the bytes done by all the algorithms; it is called on the goroutine
// of Compute. ctx cancels. The sums are in the order of algs.
func Compute(ctx context.Context, r io.ReaderAt, off, n int64, algs []Algorithm, progress func(done int64)) ([][]byte, error) {
	if off < 0 || n < 0 {
		return nil, ErrRange
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	hs := make([]gohash.Hash, len(algs))
	for i, a := range algs {
		hs[i] = a.New()
	}
	if n > 0 && len(hs) > 0 {
		if err := feed(ctx, r, off, n, hs, progress); err != nil {
			return nil, err
		}
	}
	sums := make([][]byte, len(hs))
	for i, h := range hs {
		sums[i] = h.Sum(nil)
	}
	return sums, nil
}

func feed(ctx context.Context, r io.ReaderAt, off, n int64, hs []gohash.Hash, progress func(int64)) error {
	nparts := int(min(partsInFlight, (n+partSize-1)/partSize))
	free := make(chan *part, nparts)
	for range nparts {
		free <- &part{buf: make([]byte, min(partSize, n))}
	}

	var done atomic.Int64
	var wg sync.WaitGroup
	work := make([]chan *part, len(hs))
	for i, h := range hs {
		ch := make(chan *part, nparts)
		work[i] = ch
		wg.Go(func() {
			for p := range ch {
				// After a cancel the parts left are only passed through
				if ctx.Err() == nil {
					h.Write(p.data)
				}
				if p.left.Add(-1) == 0 {
					done.Add(int64(len(p.data)))
					free <- p
				}
			}
		})
	}
	stop := func() {
		for _, ch := range work {
			close(ch)
		}
		wg.Wait()
	}

	reported := int64(0)
	report := func() {
		if d := done.Load(); progress != nil && d != reported {
			reported = d
			progress(d)
		}
	}
	for pos := int64(0); pos < n; {
		var p *part
		select {
		case p = <-free:
		case <-ctx.Done():
			stop()
			return ctx.Err()
		}
		report()
		k := min(partSize, n-pos)
		if err := readFull(r, p.buf[:k], off+pos); err != nil {
			stop()
			return err
		}
		p.data = p.buf[:k]
		p.left.Store(int32(len(hs)))
		for _, ch := range work {
			ch <- p
		}
		pos += k
	}
	stop()
	if err := ctx.Err(); err != nil {
		return err
	}
	report()
	return nil
}
