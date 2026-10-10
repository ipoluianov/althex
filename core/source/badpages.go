package source

import "sort"

// BadPages returns the indexes of the pages in the cache that failed to be
// read, sorted (a page is PageSize bytes at index*PageSize); the pages
// already dropped from the cache are not known any more
func (c *Cache) BadPages() []int64 {
	if !c.bad.Load() {
		return nil
	}
	c.mu.Lock()
	var out []int64
	for idx, pg := range c.pages {
		if pg.err != nil {
			out = append(out, idx)
		}
	}
	c.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
