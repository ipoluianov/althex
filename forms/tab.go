package forms

import (
	"image/color"
	"path/filepath"
	"sort"

	"github.com/ipoluianov/althex/core/doc"
	"github.com/ipoluianov/althex/forms/hexview"
	"github.com/ipoluianov/nui/ui"
)

// Bookmark is a named range of a document (NAV-2)
type Bookmark struct {
	Name    string
	Off     int64
	Len     int64
	Color   string // "#rrggbb"
	Comment string `json:",omitempty"`
}

// navHistoryMax is how many places back the history keeps (NAV-3)
const navHistoryMax = 200

// Tab is an open document with its views
type Tab struct {
	doc   *doc.Document
	views []*hexview.View // two when split (VIEW-7)
	cur   int             // the view with the focus
	box   *ui.Panel       // what the tab shows: a view or a splitter of two
	split *ui.Splitter
	// syncScroll scrolls the views of a split together
	syncScroll bool
	syncing    bool

	bookmarks []Bookmark
	back, fwd []int64 // the places jumped from and back from
	// Extra marks drawn on the views: search hits
	marks func(from, to int64) []hexview.Highlight
	// cmp: the tab compares its document with another one
	cmp *Compare
	// tmpl: the template applied to the document
	tmpl *TemplateState
}

func newTab(d *doc.Document, opts hexview.Options, onView func(t *Tab, v *hexview.View)) *Tab {
	t := &Tab{doc: d, syncScroll: true}
	t.box = ui.NewPanel()
	t.box.SetPanelPadding(0)
	t.box.SetXExpandable(true)
	t.box.SetYExpandable(true)
	v := t.newView(opts, onView)
	t.box.AddWidget(0, 0, v)
	return t
}

func (t *Tab) newView(opts hexview.Options, onView func(t *Tab, v *hexview.View)) *hexview.View {
	v := hexview.NewView(t.doc, opts)
	v.Highlights = t.highlights
	t.views = append(t.views, v)
	v.OnFocused = func() {
		for i, w := range t.views {
			if w == v {
				t.cur = i
			}
		}
	}
	v.OnScrolled = func(top int64) {
		if !t.syncScroll || t.syncing {
			return
		}
		t.syncing = true
		for _, w := range t.views {
			if w != v {
				w.ScrollToOffset(v.TopOffset())
			}
		}
		t.syncing = false
	}
	if onView != nil {
		onView(t, v)
	}
	return v
}

// view returns the view with the focus
func (t *Tab) view() *hexview.View { return t.views[t.cur] }

// name is the file name of the document, or "Untitled"
func (t *Tab) name() string {
	name := T().Untitled
	if p := t.doc.Path(); p != "" {
		name = filepath.Base(p)
	}
	if t.cmp != nil {
		name += " ↔ " + filepath.Base(t.cmp.docB.Path())
	}
	return name
}

// setSplit shows the document in two views one above the other, or in one
func (t *Tab) setSplit(on bool, onView func(t *Tab, v *hexview.View)) {
	if t.cmp != nil || on == (len(t.views) == 2) {
		return
	}
	first := t.views[0]
	t.box.RemoveAllWidgets()
	if on {
		second := t.newView(first.Options(), onView)
		second.SetFontSize(first.FontSize())
		second.GoTo(first.Caret())
		t.split = ui.NewVSplitter()
		t.split.SetWidgets(first, second)
		t.box.AddWidget(0, 0, t.split)
		return
	}
	for _, v := range t.views[1:] {
		v.Close()
	}
	t.views = t.views[:1]
	t.cur = 0
	t.split = nil
	t.box.AddWidget(0, 0, first)
}

// close stops the views and closes the document
func (t *Tab) close() {
	if t.tmpl != nil && t.tmpl.cancel != nil {
		t.tmpl.cancel()
	}
	for _, v := range t.views {
		v.Close()
	}
	t.doc.Close()
	if t.cmp != nil {
		t.cmp.docB.Close()
	}
}

// jump goes to the offset, remembering where the caret was for Back
func (t *Tab) jump(off int64) {
	v := t.view()
	if from := v.Caret(); from != off {
		t.back = append(t.back, from)
		if len(t.back) > navHistoryMax {
			t.back = t.back[1:]
		}
		t.fwd = t.fwd[:0]
	}
	v.GoTo(off)
}

// goBack returns to the place jumped from
func (t *Tab) goBack() {
	if len(t.back) == 0 {
		return
	}
	v := t.view()
	t.fwd = append(t.fwd, v.Caret())
	off := t.back[len(t.back)-1]
	t.back = t.back[:len(t.back)-1]
	v.GoTo(off)
}

func (t *Tab) goForward() {
	if len(t.fwd) == 0 {
		return
	}
	v := t.view()
	t.back = append(t.back, v.Caret())
	off := t.fwd[len(t.fwd)-1]
	t.fwd = t.fwd[:len(t.fwd)-1]
	v.GoTo(off)
}

// highlights gives the views the bookmarks and the marks of the tab
func (t *Tab) highlights(from, to int64) []hexview.Highlight {
	var hs []hexview.Highlight
	for _, b := range t.bookmarks {
		n := max(b.Len, 1)
		if b.Off < to && b.Off+n > from {
			hs = append(hs, hexview.Highlight{Off: b.Off, Len: n, Color: bookmarkColor(b.Color)})
		}
	}
	hs = append(hs, t.templateMarks(from, to)...)
	if t.marks != nil {
		hs = append(hs, t.marks(from, to)...)
	}
	return hs
}

// bookmarkColors are offered for new bookmarks in turn
var bookmarkColors = []string{"#c9a227", "#3f9e5a", "#3d7fd1", "#b3549b", "#d0703a", "#2aa1a1"}

func bookmarkColor(hex string) color.RGBA {
	c := ui.ColorFromHex(hex)
	// Light enough to read the bytes on both themes
	c.A = 110
	return c
}

// toggleBookmark adds a bookmark for the selection (or the byte at the
// caret), or removes the one there is at the caret
func (t *Tab) toggleBookmark() {
	v := t.view()
	caret := v.Caret()
	for i, b := range t.bookmarks {
		if caret >= b.Off && caret < b.Off+max(b.Len, 1) {
			t.bookmarks = append(t.bookmarks[:i], t.bookmarks[i+1:]...)
			return
		}
	}
	off, n := v.Selection()
	if n == 0 {
		off, n = caret, 1
	}
	t.bookmarks = append(t.bookmarks, Bookmark{
		Name:  T().BookmarkName(len(t.bookmarks) + 1),
		Off:   off,
		Len:   n,
		Color: bookmarkColors[len(t.bookmarks)%len(bookmarkColors)],
	})
	sort.Slice(t.bookmarks, func(i, j int) bool { return t.bookmarks[i].Off < t.bookmarks[j].Off })
}

// nextBookmark goes to the bookmark after the caret (before it when back), wrapping around
func (t *Tab) nextBookmark(back bool) {
	if len(t.bookmarks) == 0 {
		return
	}
	caret := t.view().Caret()
	target := -1
	if back {
		for i := len(t.bookmarks) - 1; i >= 0; i-- {
			if t.bookmarks[i].Off < caret {
				target = i
				break
			}
		}
		if target < 0 {
			target = len(t.bookmarks) - 1
		}
	} else {
		for i, b := range t.bookmarks {
			if b.Off > caret {
				target = i
				break
			}
		}
		if target < 0 {
			target = 0
		}
	}
	t.jump(t.bookmarks[target].Off)
}
