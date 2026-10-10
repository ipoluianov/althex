package diskimg

import (
	"container/list"
	"encoding/binary"
	"errors"
	"io"
	"sync"
)

// lru keeps the last used values by key: table pages, decoded clusters.
// The values are shared and must not be changed.
type lru[V any] struct {
	mu    sync.Mutex
	max   int
	items map[int64]*list.Element
	order list.List
}

type lruItem[V any] struct {
	key int64
	val V
}

func newLRU[V any](max int) *lru[V] {
	return &lru[V]{max: max, items: make(map[int64]*list.Element)}
}

// get returns the value of the key, loading it when it is not kept. The
// load runs without the lock: two readers may load the same value, which
// is cheaper than making one wait for the other's file read.
func (c *lru[V]) get(key int64, load func() (V, error)) (V, error) {
	c.mu.Lock()
	if e, ok := c.items[key]; ok {
		c.order.MoveToFront(e)
		v := e.Value.(*lruItem[V]).val
		c.mu.Unlock()
		return v, nil
	}
	c.mu.Unlock()
	v, err := load()
	if err != nil {
		return v, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[key]; ok {
		c.order.MoveToFront(e)
		return e.Value.(*lruItem[V]).val, nil
	}
	c.items[key] = c.order.PushFront(&lruItem[V]{key, v})
	if c.order.Len() > c.max {
		last := c.order.Back()
		c.order.Remove(last)
		delete(c.items, last.Value.(*lruItem[V]).key)
	}
	return v, nil
}

const pageSize = 4096

var errOutside = errors.New("a table is outside the file")

// pager reads the numbers of the tables of a container (block maps, L1/L2
// tables, grain tables) by pages of the file kept in an LRU
type pager struct {
	r     io.ReaderAt
	size  int64
	order binary.ByteOrder
	pages *lru[[]byte]
}

func newPager(r io.ReaderAt, size int64, order binary.ByteOrder) *pager {
	return &pager{r: r, size: size, order: order, pages: newLRU[[]byte](256)}
}

func (p *pager) page(po int64) ([]byte, error) {
	return p.pages.get(po, func() ([]byte, error) {
		b := make([]byte, min(pageSize, p.size-po))
		if err := readFull(p.r, b, po); err != nil {
			return nil, err
		}
		return b, nil
	})
}

// bytes returns n bytes at off, n up to pageSize
func (p *pager) bytes(off int64, n int) ([]byte, error) {
	if off < 0 || n < 0 || n > pageSize || off > p.size-int64(n) {
		return nil, errOutside
	}
	po := off &^ (pageSize - 1)
	pg, err := p.page(po)
	if err != nil {
		return nil, err
	}
	in := int(off - po)
	if in+n <= len(pg) {
		return pg[in : in+n], nil
	}
	// Across two pages
	b := append([]byte(nil), pg[in:]...)
	next, err := p.page(po + pageSize)
	if err != nil {
		return nil, err
	}
	return append(b, next[:n-len(b)]...), nil
}

func (p *pager) u32(off int64) (uint32, error) {
	b, err := p.bytes(off, 4)
	if err != nil {
		return 0, err
	}
	return p.order.Uint32(b), nil
}

func (p *pager) u64(off int64) (uint64, error) {
	b, err := p.bytes(off, 8)
	if err != nil {
		return 0, err
	}
	return p.order.Uint64(b), nil
}

func le16(b []byte) uint16 { return binary.LittleEndian.Uint16(b) }
func le32(b []byte) uint32 { return binary.LittleEndian.Uint32(b) }
func le64(b []byte) uint64 { return binary.LittleEndian.Uint64(b) }
func be32(b []byte) uint32 { return binary.BigEndian.Uint32(b) }
func be64(b []byte) uint64 { return binary.BigEndian.Uint64(b) }

// isPow2 tells whether v is a power of two
func isPow2(v int64) bool { return v > 0 && v&(v-1) == 0 }

// ceilDiv is a / b rounded up
func ceilDiv(a, b int64) int64 { return (a + b - 1) / b }
