package doc

import "time"

// HistNode is a state of the document in the history of edits. The history
// is a tree: an edit after undo starts a new branch, the old one stays and
// can be gone back to (Document.Goto).
type HistNode struct {
	ID       int
	Parent   *HistNode
	Children []*HistNode
	// Kind names the edit that made the state, e.g. "typing", "paste"
	Kind string
	Time time.Time
	// Off and Len are where the last edit put or removed its bytes, for the
	// cursor after undo and redo
	Off, Len int64

	root *node
	// redo is the child redo goes to: the one undone last
	redo *HistNode
	// typingEnd is where the next typed byte continues this state
	typingEnd int64
	// sealed: the next edit starts a new state even if it could continue this one
	sealed bool
	// Lost: the state needs a file that was replaced (found on recovery)
	Lost bool
}

// History of the document
type history struct {
	root   *HistNode
	cur    *HistNode
	saved  *HistNode // the state in the file; nil - none of them
	nextID int
	nodes  map[int]*HistNode
}

func newHistory(root *node, id int) *history {
	h := &history{nodes: make(map[int]*HistNode)}
	n := h.add(nil, id, "open", root)
	h.root, h.cur, h.saved = n, n, n
	if id >= h.nextID {
		h.nextID = id + 1
	}
	return h
}

func (h *history) add(parent *HistNode, id int, kind string, root *node) *HistNode {
	n := &HistNode{ID: id, Parent: parent, Kind: kind, Time: time.Now(), root: root, sealed: true}
	if parent != nil {
		parent.Children = append(parent.Children, n)
		parent.redo = n
	}
	h.nodes[id] = n
	if id >= h.nextID {
		h.nextID = id + 1
	}
	return n
}
