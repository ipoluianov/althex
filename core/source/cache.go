package source

import (
	"container/list"
	"io"
	"sync"
)

// PageSize is the unit the cache reads and keeps
const PageSize = 64 << 10

// DefaultCachePages keeps 32 MB of a source in memory
const DefaultCachePages = 512

// maxBackgroundLoads is how many pages are read at once for TryReadAt
const maxBackgroundLoads = 4

// Cache keeps the recently read pages of a source. It is safe for use from
// several goroutines. A page that cannot be read (a bad sector) is kept with
// its error, so it is not read again and again; Invalidate drops it.
type Cache struct {
	src      Source
	maxPages int

	mu      sync.Mutex
	pages   map[int64]*page
	lru     *list.List // of *page, the most recent first
	loading map[int64]*load

	sem chan struct{}
}

type page struct {
	idx  int64
	data []byte // shorter than PageSize at the end of the source
	err  error
	elem *list.Element
}

// load is a page being read; the waiters are called once it is in the cache
type load struct {
	done    chan struct{}
	waiters []func()
}

func NewCache(src Source, maxPages int) *Cache {
	if maxPages < 4 {
		maxPages = 4
	}
	return &Cache{
		src:      src,
		maxPages: maxPages,
		pages:    make(map[int64]*page),
		lru:      list.New(),
		loading:  make(map[int64]*load),
		sem:      make(chan struct{}, maxBackgroundLoads),
	}
}

// Source is the source under the cache
func (c *Cache) Source() Source { return c.src }

func (c *Cache) Size() int64 { return c.src.Size() }

func (c *Cache) Close() error { return c.src.Close() }

// ReadAt reads through the cache, waiting for the pages that are not in it
func (c *Cache) ReadAt(p []byte, off int64) (int, error) {
	size := c.src.Size()
	if off >= size {
		return 0, io.EOF
	}
	n := 0
	for n < len(p) && off+int64(n) < size {
		pos := off + int64(n)
		pg := c.get(pos / PageSize)
		in := int(pos % PageSize)
		if pg.err != nil {
			return n, pg.err
		}
		if in >= len(pg.data) {
			return n, io.ErrUnexpectedEOF
		}
		n += copy(p[n:], pg.data[in:])
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// TryReadAt reads only if all the pages are in the cache. Otherwise it starts
// reading the missing ones in the background, calls onReady (from another
// goroutine) as each of them arrives, and returns false.
func (c *Cache) TryReadAt(p []byte, off int64, onReady func()) bool {
	size := c.src.Size()
	end := min(off+int64(len(p)), size)
	if off >= end {
		return true
	}
	c.mu.Lock()
	ready := true
	for idx := off / PageSize; idx <= (end-1)/PageSize; idx++ {
		if _, ok := c.pages[idx]; ok {
			continue
		}
		ready = false
		if l, ok := c.loading[idx]; ok {
			if onReady != nil {
				l.waiters = append(l.waiters, onReady)
			}
			continue
		}
		l := &load{done: make(chan struct{})}
		if onReady != nil {
			l.waiters = append(l.waiters, onReady)
		}
		c.loading[idx] = l
		go c.loadInBackground(idx, l)
	}
	if !ready {
		c.mu.Unlock()
		return false
	}
	// Copied under the lock: the pages cannot be evicted meanwhile
	n := 0
	for n < int(end-off) {
		pos := off + int64(n)
		pg := c.pages[pos/PageSize]
		c.touch(pg)
		in := int(pos % PageSize)
		if pg.err != nil || in >= len(pg.data) {
			// Unreadable bytes are shown as zeros; ReadAt reports the error
			k := min(PageSize-in, int(end-off)-n)
			clear(p[n : n+k])
			n += k
			continue
		}
		n += copy(p[n:end-off], pg.data[in:])
	}
	c.mu.Unlock()
	return true
}

// Readable tells whether the byte is not in a page that failed to be read;
// a page not in the cache counts as readable
func (c *Cache) Readable(off int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	pg, ok := c.pages[off/PageSize]
	return !ok || pg.err == nil
}

// Invalidate drops the cached pages of the range, so they are read again
func (c *Cache) Invalidate(off, n int64) {
	if n <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for idx := off / PageSize; idx <= (off+n-1)/PageSize; idx++ {
		if pg, ok := c.pages[idx]; ok {
			c.lru.Remove(pg.elem)
			delete(c.pages, idx)
		}
	}
}

// InvalidateAll drops all the cached pages
func (c *Cache) InvalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	clear(c.pages)
	c.lru.Init()
}

// get returns the page, reading it if it is not in the cache
func (c *Cache) get(idx int64) *page {
	c.mu.Lock()
	if pg, ok := c.pages[idx]; ok {
		c.touch(pg)
		c.mu.Unlock()
		return pg
	}
	if l, ok := c.loading[idx]; ok {
		c.mu.Unlock()
		<-l.done
		c.mu.Lock()
		pg, ok := c.pages[idx]
		c.mu.Unlock()
		if ok {
			return pg
		}
		return c.get(idx) // evicted at once by other reads
	}
	l := &load{done: make(chan struct{})}
	c.loading[idx] = l
	c.mu.Unlock()
	return c.loadPage(idx, l)
}

func (c *Cache) loadInBackground(idx int64, l *load) {
	c.sem <- struct{}{}
	defer func() { <-c.sem }()
	c.loadPage(idx, l)
}

// loadPage reads the page, puts it into the cache and tells the waiters
func (c *Cache) loadPage(idx int64, l *load) *page {
	pg := &page{idx: idx}
	n := PageSize
	if rest := c.src.Size() - idx*PageSize; rest < int64(n) {
		n = int(max(rest, 0))
	}
	buf := make([]byte, n)
	got, err := c.src.ReadAt(buf, idx*PageSize)
	if err == io.EOF {
		// A file made shorter since: the reader of the bytes past its end
		// has them elsewhere (see doc.srcBuffer), or gets ErrUnexpectedEOF
		err = nil
	}
	pg.data = buf[:got]
	pg.err = err

	c.mu.Lock()
	pg.elem = c.lru.PushFront(pg)
	c.pages[idx] = pg
	for c.lru.Len() > c.maxPages {
		old := c.lru.Remove(c.lru.Back()).(*page)
		delete(c.pages, old.idx)
	}
	delete(c.loading, idx)
	waiters := l.waiters
	c.mu.Unlock()
	close(l.done)
	for _, w := range waiters {
		w()
	}
	return pg
}

func (c *Cache) touch(pg *page) {
	c.lru.MoveToFront(pg.elem)
}
