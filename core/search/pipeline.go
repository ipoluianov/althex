package search

import (
	"context"
	"fmt"
	"io"
	"runtime"
	"sync"
)

const (
	defaultChunk = 4 << 20
	// maxChunk keeps offsets inside a chunk in uint32
	maxChunk = 1 << 30
)

// ordered runs work(i) for i in [0, n) on several goroutines and passes the
// results to emit in the order of i, from the calling goroutine. At most
// 2*workers results are made ahead of emit, so the memory stays bounded
// whatever n is. emit returning false stops all.
func ordered[T any](ctx context.Context, workers int, n int64, work func(ctx context.Context, i int64) (T, error), emit func(i int64, v T) bool) error {
	if n <= 0 {
		return ctx.Err()
	}
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	workers = int(min(int64(workers), n))
	window := int(min(int64(2*workers), n))

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type result struct {
		v   T
		err error
	}
	// A slot holds the one result of i in flight with i%window; it is free
	// again before i+window is given out, so a send never blocks
	slots := make([]chan result, window)
	for i := range slots {
		slots[i] = make(chan result, 1)
	}
	jobs := make(chan int64, window)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				var r result
				if err := ctx.Err(); err != nil {
					r.err = err
				} else {
					r.v, r.err = work(ctx, i)
				}
				slots[i%int64(window)] <- r
			}
		}()
	}
	defer func() {
		close(jobs)
		cancel()
		wg.Wait()
	}()

	next := int64(0)
	for ; next < int64(window); next++ {
		jobs <- next
	}
	for i := int64(0); i < n; i++ {
		r := <-slots[i%int64(window)]
		if r.err != nil {
			return r.err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !emit(i, r.v) {
			return nil
		}
		if next < n {
			jobs <- next
			next++
		}
	}
	return nil
}

var bufPool sync.Pool // of *[]byte

func getBuf(n int) *[]byte {
	if p, ok := bufPool.Get().(*[]byte); ok && cap(*p) >= n {
		*p = (*p)[:n]
		return p
	}
	b := make([]byte, n)
	return &b
}

func putBuf(p *[]byte) { bufPool.Put(p) }

// readFull reads len(p) bytes at off; the data is known to be there, so a
// short read is an error
func readFull(r io.ReaderAt, p []byte, off int64) error {
	n, err := r.ReadAt(p, off)
	if n == len(p) && (err == nil || err == io.EOF) {
		return nil
	}
	if err == nil || err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return fmt.Errorf("search: read at %d: %w", off+int64(n), err)
}

// span clamps the range [from, to) to the data; to 0 - the end
func span(from, to, size int64) (int64, int64) {
	if to <= 0 || to > size {
		to = size
	}
	from = max(from, 0)
	return from, max(from, to)
}

func chunkSize(n int) int64 {
	if n <= 0 {
		return defaultChunk
	}
	return int64(min(n, maxChunk))
}
