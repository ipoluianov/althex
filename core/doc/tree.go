package doc

import "math/rand/v2"

// piece is a run of bytes of a buffer (see Document.bufs)
type piece struct {
	buf int   // index in Document.bufs
	off int64 // in the buffer
	len int64
}

// node is a node of a persistent treap of pieces in document order. Nodes
// are never changed once made: an edit copies the path to the changed
// nodes, so every older root stays a complete snapshot of the document.
// That is what the history keeps (no bytes are copied for undo).
type node struct {
	p           piece
	left, right *node
	prio        uint32
	size        int64 // bytes in the subtree
	count       int   // pieces in the subtree
}

func (t *node) bytes() int64 {
	if t == nil {
		return 0
	}
	return t.size
}

func (t *node) pieces() int {
	if t == nil {
		return 0
	}
	return t.count
}

func newNode(p piece, prio uint32, left, right *node) *node {
	return &node{
		p:     p,
		left:  left,
		right: right,
		prio:  prio,
		size:  left.bytes() + p.len + right.bytes(),
		count: left.pieces() + 1 + right.pieces(),
	}
}

func leaf(p piece) *node {
	return newNode(p, rand.Uint32(), nil, nil)
}

// split returns the first k bytes and the rest; a piece across k is cut in two
func split(t *node, k int64) (*node, *node) {
	if t == nil {
		return nil, nil
	}
	ls := t.left.bytes()
	switch {
	case k <= ls:
		l, r := split(t.left, k)
		return l, newNode(t.p, t.prio, r, t.right)
	case k >= ls+t.p.len:
		l, r := split(t.right, k-ls-t.p.len)
		return newNode(t.p, t.prio, t.left, l), r
	}
	in := k - ls
	lp := piece{t.p.buf, t.p.off, in}
	rp := piece{t.p.buf, t.p.off + in, t.p.len - in}
	// Both halves keep the priority: it is not lower than that of the children
	return newNode(lp, t.prio, t.left, nil), newNode(rp, t.prio, nil, t.right)
}

// merge joins two trees, all of a before all of b
func merge(a, b *node) *node {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.prio > b.prio {
		return newNode(a.p, a.prio, a.left, merge(a.right, b))
	}
	return newNode(b.p, b.prio, merge(a, b.left), b.right)
}

// last returns the last piece; ok is false for an empty tree
func last(t *node) (p piece, ok bool) {
	if t == nil {
		return piece{}, false
	}
	for t.right != nil {
		t = t.right
	}
	return t.p, true
}

// withLast returns the tree with the last piece replaced
func withLast(t *node, p piece) *node {
	if t.right == nil {
		return newNode(p, t.prio, t.left, nil)
	}
	return newNode(t.p, t.prio, t.left, withLast(t.right, p))
}

// insertPiece puts the piece at offset off. A piece that continues the one
// before it in the same buffer extends it instead, so typing byte by byte
// does not make a piece per byte.
func insertPiece(t *node, off int64, p piece) *node {
	l, r := split(t, off)
	if lp, ok := last(l); ok && lp.buf == p.buf && lp.off+lp.len == p.off {
		lp.len += p.len
		return merge(withLast(l, lp), r)
	}
	return merge(merge(l, leaf(p)), r)
}

// deleteRange removes n bytes at off
func deleteRange(t *node, off, n int64) *node {
	l, rest := split(t, off)
	_, r := split(rest, n)
	return merge(l, r)
}

// walk calls fn for the pieces overlapping [from, to) in order, with the
// document offset of each; it stops when fn returns false
func walk(t *node, base, from, to int64, fn func(start int64, p piece) bool) bool {
	if t == nil || from >= to || base >= to || base+t.size <= from {
		return true
	}
	ls := t.left.bytes()
	if !walk(t.left, base, from, to, fn) {
		return false
	}
	start := base + ls
	if start < to && start+t.p.len > from {
		if !fn(start, t.p) {
			return false
		}
	}
	return walk(t.right, start+t.p.len, from, to, fn)
}

// buildTree makes a tree of the pieces in order
func buildTree(ps []piece) *node {
	var t *node
	for _, p := range ps {
		if p.len > 0 {
			t = merge(t, leaf(p))
		}
	}
	return t
}

// listPieces returns all the pieces in order
func listPieces(t *node) []piece {
	ps := make([]piece, 0, t.pieces())
	walk(t, 0, 0, t.bytes(), func(_ int64, p piece) bool {
		ps = append(ps, p)
		return true
	})
	return ps
}
