package doc

import "io"

// Kinds of edits, shown in the history
const (
	KindTyping = "typing"
	KindInsert = "insert"
	KindDelete = "delete"
	KindPaste  = "paste"
	KindFill   = "fill"
)

// EditOptions tell how an edit goes into the history
type EditOptions struct {
	// Kind names the edit in the history; "" - by the operation
	Kind string
	// Typing continues the last state if it was typing that ended where this
	// edit starts or started at the same offset, so a typed run is undone at once
	Typing bool
}

// Insert puts the bytes at off (0 <= off <= Size)
func (d *Document) Insert(off int64, data []byte, opt EditOptions) error {
	if len(data) == 0 {
		return nil
	}
	return d.change(opt, KindInsert, off, 0, func() (piece, error) {
		addOff, err := d.add.append(data)
		return piece{bufAdd, addOff, int64(len(data))}, err
	})
}

// InsertZeros puts n zero bytes at off
func (d *Document) InsertZeros(off, n int64, opt EditOptions) error {
	if n <= 0 {
		return nil
	}
	return d.change(opt, KindInsert, off, 0, func() (piece, error) {
		return piece{bufZeros, 0, n}, nil
	})
}

// InsertFrom puts n bytes read from r at off; for large data, it is read in parts
func (d *Document) InsertFrom(off int64, r io.ReaderAt, rOff, n int64, opt EditOptions) error {
	if n <= 0 {
		return nil
	}
	return d.change(opt, KindPaste, off, 0, func() (piece, error) {
		addOff, err := d.add.appendFrom(r, rOff, n)
		return piece{bufAdd, addOff, n}, err
	})
}

// Delete removes n bytes at off
func (d *Document) Delete(off, n int64, opt EditOptions) error {
	if n <= 0 {
		return nil
	}
	return d.change(opt, KindDelete, off, n, nil)
}

// Overwrite replaces the bytes at off with data; past the end it appends
func (d *Document) Overwrite(off int64, data []byte, opt EditOptions) error {
	if len(data) == 0 {
		return nil
	}
	return d.change(opt, KindInsert, off, int64(len(data)), func() (piece, error) {
		addOff, err := d.add.append(data)
		return piece{bufAdd, addOff, int64(len(data))}, err
	})
}

// OverwriteZeros replaces n bytes at off with zeros; past the end it appends
func (d *Document) OverwriteZeros(off, n int64, opt EditOptions) error {
	if n <= 0 {
		return nil
	}
	return d.change(opt, KindFill, off, n, func() (piece, error) {
		return piece{bufZeros, 0, n}, nil
	})
}

// change removes del bytes at off (as many as there are) and puts the piece
// that put returns there (nil - nothing), as one edit
func (d *Document) change(opt EditOptions, kind string, off, del int64, put func() (piece, error)) error {
	d.mu.Lock()
	err := d.changeLocked(opt, kind, off, del, put)
	// Inside a group the subscribers hear of it at EndGroup: a long
	// operation may edit from another goroutine
	inGroup := d.groupDeep > 0
	if inGroup && err == nil {
		d.groupChanged = true
	}
	d.mu.Unlock()
	if err == nil && !inGroup {
		d.notify()
	}
	return err
}

func (d *Document) changeLocked(opt EditOptions, kind string, off, del int64, put func() (piece, error)) error {
	if d.readOnly {
		return ErrReadOnly
	}
	size := d.hist.cur.root.bytes()
	if off < 0 || off > size {
		return ErrOutOfRange
	}
	del = min(del, size-off)
	if put == nil && del == 0 {
		return nil
	}
	var p piece
	if put != nil {
		var err error
		if p, err = put(); err != nil {
			return err
		}
	}
	if opt.Kind != "" {
		kind = opt.Kind
	}
	n := d.target(kind, off, opt.Typing)
	if del > 0 {
		n.root = deleteRange(n.root, off, del)
		d.journal.logDelete(n.ID, off, del)
	}
	if put != nil {
		n.root = insertPiece(n.root, off, p)
		d.journal.logInsert(n.ID, off, p)
	}
	n.Off, n.Len = off, p.len
	n.typingEnd = off + p.len
	n.sealed = !opt.Typing && d.groupNode != n
	d.publish()
	return nil
}

// target returns the state an edit at off goes to: the current one if the
// edit continues it, otherwise a new one after it
func (d *Document) target(kind string, off int64, typing bool) *HistNode {
	h := d.hist
	cur := h.cur
	if d.groupDeep > 0 {
		if d.groupNode == nil {
			d.groupNode = h.add(cur, h.nextID, d.groupKind, cur.root)
			h.cur = d.groupNode
		}
		return d.groupNode
	}
	// Typing goes on where it ended, or edits its last byte again (the low
	// half of a byte typed in hex)
	if typing && !cur.sealed && cur.Kind == kind && (cur.typingEnd == off || cur.Off == off) &&
		len(cur.Children) == 0 && cur != h.saved && cur.Parent != nil {
		return cur
	}
	n := h.add(cur, h.nextID, kind, cur.root)
	h.cur = n
	return n
}

// BeginGroup makes the edits until EndGroup one state of the history:
// undone at once (e.g. a replace of all the matches). Groups may nest.
// The subscribers are told of the edits of a group at its end, on the
// goroutine that calls EndGroup.
func (d *Document) BeginGroup(kind string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.groupDeep == 0 {
		d.groupKind = kind
		d.groupNode = nil
	}
	d.groupDeep++
}

func (d *Document) EndGroup() {
	d.mu.Lock()
	if d.groupDeep == 0 {
		d.mu.Unlock()
		return
	}
	d.groupDeep--
	changed := false
	if d.groupDeep == 0 {
		if d.groupNode != nil {
			d.groupNode.sealed = true
		}
		d.groupNode = nil
		changed = d.groupChanged
		d.groupChanged = false
	}
	d.mu.Unlock()
	if changed {
		d.notify()
	}
}

// CanUndo tells whether there is a state before the current one
func (d *Document) CanUndo() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	p := d.hist.cur.Parent
	return p != nil && !p.Lost
}

func (d *Document) CanRedo() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	next := d.redoTarget()
	return next != nil && !next.Lost
}

func (d *Document) redoTarget() *HistNode {
	cur := d.hist.cur
	if cur.redo != nil {
		return cur.redo
	}
	if len(cur.Children) > 0 {
		return cur.Children[len(cur.Children)-1]
	}
	return nil
}

// Undo goes to the state before the current one. Returns the state undone:
// its Off and Len tell where the edit was.
func (d *Document) Undo() (*HistNode, error) {
	d.mu.Lock()
	cur := d.hist.cur
	if cur.Parent == nil || cur.Parent.Lost || d.groupDeep > 0 {
		d.mu.Unlock()
		return nil, ErrNothingToDo
	}
	cur.Parent.redo = cur
	err := d.gotoLocked(cur.Parent)
	d.mu.Unlock()
	if err != nil {
		return nil, err
	}
	d.notify()
	return cur, nil
}

// Redo goes to the state undone last. Returns that state.
func (d *Document) Redo() (*HistNode, error) {
	d.mu.Lock()
	next := d.redoTarget()
	if next == nil || d.groupDeep > 0 {
		d.mu.Unlock()
		return nil, ErrNothingToDo
	}
	err := d.gotoLocked(next)
	d.mu.Unlock()
	if err != nil {
		return nil, err
	}
	d.notify()
	return next, nil
}

// Goto makes any state of the history the current one (EDIT-3)
func (d *Document) Goto(id int) error {
	d.mu.Lock()
	n, ok := d.hist.nodes[id]
	if !ok || d.groupDeep > 0 {
		d.mu.Unlock()
		return ErrNothingToDo
	}
	err := d.gotoLocked(n)
	d.mu.Unlock()
	if err == nil {
		d.notify()
	}
	return err
}

func (d *Document) gotoLocked(n *HistNode) error {
	if d.readOnly {
		return ErrReadOnly
	}
	if n.Lost {
		return ErrSourceLost
	}
	d.hist.cur.sealed = true
	d.hist.cur = n
	n.sealed = true
	d.journal.logGoto(n, n == d.hist.saved)
	d.publish()
	return nil
}

// History returns the first state of the history and the current one. The
// nodes may be read on the goroutine that edits the document.
func (d *Document) History() (root, cur *HistNode) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.hist.root, d.hist.cur
}
