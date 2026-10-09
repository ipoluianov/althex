// Package hexview is the widget that shows and edits a document: the
// offsets, the bytes as numbers and the bytes as text. It draws only the
// rows on the screen and never waits for a disk: bytes not read yet are
// shown as placeholders and drawn when they arrive.
package hexview

import (
	"image/color"
	"math"
	"sync/atomic"
	"time"

	"github.com/ipoluianov/althex/core/doc"
	"github.com/ipoluianov/nui/ui"
)

// The columns the caret can be in
const (
	colNum  = 0 // the bytes as numbers
	colText = 1
)

// Mode is how the bytes are written in the number column (VIEW-5)
type Mode int

const (
	ModeHex Mode = iota
	ModeBin
	ModeOct
	ModeDec
)

// digits is how many digits a byte takes in the mode, base their base
func (m Mode) digits() int {
	switch m {
	case ModeBin:
		return 8
	case ModeOct, ModeDec:
		return 3
	}
	return 2
}

func (m Mode) base() int {
	switch m {
	case ModeBin:
		return 2
	case ModeOct:
		return 8
	case ModeDec:
		return 10
	}
	return 16
}

// TextEncoding decodes and encodes the text column (core/codec.Encoding is one)
type TextEncoding interface {
	Unit() int
	Decode(data []byte, i int) (r rune, size int, ok bool)
	Encode(s string) ([]byte, error)
}

// ColorRule paints the bytes from From to To (VIEW-6)
type ColorRule struct {
	From, To byte
	Color    color.RGBA
}

// Highlight marks a range of the document: a bookmark, a search hit, a difference
type Highlight struct {
	Off, Len int64
	Color    color.RGBA
	// Frame draws an outline instead of filling the background
	Frame bool
}

// Options are how the view shows the bytes
type Options struct {
	// BytesPerRow is 8..64; 0 - as many as fit the width (VIEW-1)
	BytesPerRow int
	// Group is 1, 2, 4, 8 or 16 bytes written together (VIEW-3)
	Group int
	// LittleEndian writes a group as a little-endian number: its bytes in reverse
	LittleEndian bool
	Mode         Mode
	// OffsetBase is 16, 10 or 8 (VIEW-2)
	OffsetBase int
	// Relative shows the offsets from RelativeTo (VIEW-2: from a mark)
	Relative   bool
	RelativeTo int64
	// Encoding of the text column; nil - ASCII
	Encoding   TextEncoding
	ColorRules []ColorRule
}

// DefaultOptions are 16 bytes a row in hex, ASCII text
func DefaultOptions() Options {
	return Options{BytesPerRow: 16, Group: 1, OffsetBase: 16}
}

// DefaultFontSize is the size of the text in logical pixels
const DefaultFontSize = 14

// View shows a document
type View struct {
	ui.Widget

	doc         *doc.Document
	unsubscribe func()
	opts        Options

	// The font in pixels of the screen, made for fontSize at the scale
	font     *Font
	fontSize float64
	scale    float64

	// Layout in pixels of the screen, see layout
	bpr       int // bytes per row
	offChars  int
	padX      int
	xOff      int
	xNum      int
	xText     int
	cellW     int
	headerH   int
	sbW       int
	pxW, pxH  int
	rowsShown int

	topRow int64

	// The caret is a position between bytes (0..Size); the selection is
	// [min(anchor, caret), max(anchor, caret))
	caret  int64
	anchor int64
	digit  int // the digit of the byte at the caret typed next (the hex half)
	col    int
	insert bool
	busy   bool // saving: no edits

	focused   bool
	caretOn   bool
	caretTime time.Time

	mouse struct {
		selecting bool
		origin    int64 // the byte the selection started on
		dragBar   bool
		dragY     int // pixels
		dragTop   int64
		x, y      int // logical, the last position
	}

	// The form to wake up when bytes arrive from the disk
	form        atomic.Pointer[ui.Form]
	repaintSent atomic.Bool

	// Buffers of a paint, kept to not allocate each time
	rowBuf  []byte
	rowMark []bool
	textBuf []byte
	hlBuf   []Highlight

	// Highlights returns the marks of the range [from, to) to draw
	Highlights func(from, to int64) []Highlight
	// OnStateChanged is called when the caret, the selection, the mode or
	// the document change: for the status bar
	OnStateChanged func()
	// OnScrolled is called when the first row shown changes (for a view
	// scrolled together with this one)
	OnScrolled func(topRow int64)
	// OnError reports an edit that failed
	OnError func(err error)
	// OnFocused is called when the view gets the focus
	OnFocused func()
}

// NewView makes a view of the document
func NewView(d *doc.Document, opts Options) *View {
	v := &View{doc: d, fontSize: DefaultFontSize, caretOn: true}
	v.opts = normalize(opts)
	v.bpr = max(v.opts.BytesPerRow, 8)
	v.InitWidget()
	v.SetTypeName("HexView")
	v.SetCanBeFocused(true)
	v.SetXExpandable(true)
	v.SetYExpandable(true)
	v.SetMouseCursor(ui.MouseCursorIBeam)
	v.SetOnPaint(v.paint)
	v.SetOnKeyDown(v.keyDown)
	v.SetOnChar(v.charTyped)
	v.SetOnMouseDown(v.mouseDown)
	v.SetOnMouseUp(v.mouseUp)
	v.SetOnMouseMove(v.mouseMove)
	v.SetOnMouseWheel(v.mouseWheel)
	v.SetOnFocused(func() {
		v.focused = true
		v.resetBlink()
		if v.OnFocused != nil {
			v.OnFocused()
		}
	})
	v.SetOnFocusLost(func() {
		v.focused = false
		v.update()
	})
	v.AddTimer(100, v.timer)
	v.unsubscribe = d.Subscribe(v.docChanged)
	return v
}

func normalize(o Options) Options {
	if o.BytesPerRow != 0 {
		o.BytesPerRow = max(1, min(o.BytesPerRow, 64))
	}
	switch o.Group {
	case 1, 2, 4, 8, 16:
	default:
		o.Group = 1
	}
	switch o.OffsetBase {
	case 8, 10, 16:
	default:
		o.OffsetBase = 16
	}
	if o.Mode < ModeHex || o.Mode > ModeDec {
		o.Mode = ModeHex
	}
	return o
}

// Close stops following the document
func (v *View) Close() {
	if v.unsubscribe != nil {
		v.unsubscribe()
		v.unsubscribe = nil
	}
}

// Document returns the document shown
func (v *View) Document() *doc.Document { return v.doc }

// Options returns how the view shows the bytes
func (v *View) Options() Options { return v.opts }

// SetOptions changes how the view shows the bytes, keeping the caret on the screen
func (v *View) SetOptions(o Options) {
	v.opts = normalize(o)
	v.digit = 0
	if v.font != nil && v.pxW > 0 {
		v.layout(v.pxW, v.pxH)
		v.ensureCaretVisible()
	}
	v.changed()
}

func (v *View) update() {
	if f := v.Form(); f != nil {
		f.Update()
	}
}

func (v *View) changed() {
	if v.OnStateChanged != nil {
		v.OnStateChanged()
	}
	v.update()
}

func (v *View) docChanged() {
	size := v.doc.Size()
	v.caret = min(v.caret, size)
	v.anchor = min(v.anchor, size)
	v.clampTop()
	v.changed()
}

// dataArrived is called from another goroutine when bytes are read from the disk
func (v *View) dataArrived() {
	f := v.form.Load()
	if f == nil || !v.repaintSent.CompareAndSwap(false, true) {
		return
	}
	f.Invoke(func() { v.repaintSent.Store(false) })
}

func (v *View) timer() {
	if v.focused {
		if time.Since(v.caretTime) > 530*time.Millisecond {
			v.caretOn = !v.caretOn
			v.caretTime = time.Now()
			v.update()
		}
	} else if !v.caretOn {
		v.caretOn = true
		v.update()
	}
	if v.mouse.selecting {
		v.autoScroll()
	}
}

func (v *View) resetBlink() {
	v.caretOn = true
	v.caretTime = time.Now()
	v.update()
}

// ---- Layout ----

// ensureFont makes the font for the scale of the screen
func (v *View) ensureFont(scale float64) {
	if v.font != nil && v.scale == scale && v.font.Size() == v.fontSize*scale {
		return
	}
	v.scale = scale
	v.font = NewFont(v.fontSize * scale)
}

// offsetChars is how many characters the offsets of the document take
func (v *View) offsetChars() int {
	size := v.doc.Size()
	n := 1
	for limit := uint64(v.opts.OffsetBase); uint64(size) >= limit && n < 22; limit *= uint64(v.opts.OffsetBase) {
		n++
	}
	if v.opts.OffsetBase == 16 {
		n = max(n, 8)
	}
	if v.opts.Relative {
		n++ // the sign
	}
	return n
}

// layout places the columns for the size in pixels of the screen
func (v *View) layout(w, h int) {
	f := v.font
	cw := f.CharWidth
	v.pxW, v.pxH = w, h
	v.offChars = v.offsetChars()
	v.padX = cw / 2
	v.sbW = int(math.Round(12 * v.scale))
	v.xOff = v.padX
	v.xNum = v.xOff + (v.offChars+2)*cw
	v.cellW = v.opts.Mode.digits() * cw
	v.bpr = v.opts.BytesPerRow
	if v.bpr == 0 {
		v.bpr = v.fitBytes(w)
	}
	v.xText = v.xNum + v.numWidth() + 2*cw
	v.headerH = f.LineHeight + int(math.Round(4*v.scale))
	v.rowsShown = max(1, (h-v.headerH)/f.LineHeight)
}

// fitBytes returns how many bytes a row can take in the width: whole
// groups, at least 8 or one group
func (v *View) fitBytes(w int) int {
	g := v.opts.Group
	best := max(g, 8)
	for n := best; n <= 64; n += g {
		v.bpr = n
		if v.xNum+v.numWidth()+3*v.font.CharWidth+n*v.font.CharWidth+v.sbW > w {
			break
		}
		best = n
	}
	return best
}

// sep8 tells whether every 8 bytes are set off by a space: when the groups are smaller
func (v *View) sep8() bool { return v.opts.Group < 8 && v.bpr > 8 }

// cellX returns the x of the cell at the i-th place of a row: a space after
// each group, one more after every 8 bytes
func (v *View) cellX(i int) int {
	cw := v.font.CharWidth
	x := v.xNum + i*v.cellW + (i/v.opts.Group)*cw
	if v.sep8() {
		x += (i / 8) * cw
	}
	return x
}

func (v *View) numWidth() int {
	return v.cellX(v.bpr-1) + v.cellW - v.xNum
}

// byteOfCell returns which byte of a row the i-th cell shows: in a
// little-endian group the bytes go in reverse
func (v *View) byteOfCell(i int) int {
	if !v.opts.LittleEndian || v.opts.Group == 1 {
		return i
	}
	g := v.opts.Group
	start := i - i%g
	last := min(start+g, v.bpr) - 1
	return start + (last - i)
}

// cellOfByte is the opposite of byteOfCell (reversing is its own inverse)
func (v *View) cellOfByte(j int) int { return v.byteOfCell(j) }

// totalRows counts the rows, with the one where a byte can be added at the end
func (v *View) totalRows() int64 {
	return v.doc.Size()/int64(v.bpr) + 1
}

func (v *View) maxTop() int64 {
	return max(0, v.totalRows()-int64(v.rowsShown))
}

func (v *View) clampTop() {
	old := v.topRow
	v.topRow = max(0, min(v.topRow, v.maxTop()))
	if v.topRow != old && v.OnScrolled != nil {
		v.OnScrolled(v.topRow)
	}
}

// setTop scrolls to the row and tells OnScrolled
func (v *View) setTop(row int64) {
	old := v.topRow
	v.topRow = row
	v.clampTop()
	if v.topRow != old && v.OnScrolled != nil {
		v.OnScrolled(v.topRow)
	}
	v.update()
}

// TopOffset returns the offset of the first row shown
func (v *View) TopOffset() int64 { return v.topRow * int64(v.bpr) }

// ScrollToOffset shows the row of the offset first, without telling OnScrolled
func (v *View) ScrollToOffset(off int64) {
	v.topRow = off / int64(v.bpr)
	v.topRow = max(0, min(v.topRow, v.maxTop()))
	v.update()
}

// ensureCaretVisible scrolls so the row of the caret is on the screen
func (v *View) ensureCaretVisible() {
	row := v.caret / int64(v.bpr)
	top := v.topRow
	if row < top {
		top = row
	} else if row >= top+int64(v.rowsShown) {
		top = row - int64(v.rowsShown) + 1
	}
	v.setTop(top)
}

// toPx converts logical pixels to pixels of the screen
func (v *View) toPx(x int) int {
	if v.scale == 0 {
		return x
	}
	return int(math.Round(float64(x) * v.scale))
}

// ---- State for the status bar and the commands ----

// Caret returns the offset of the caret
func (v *View) Caret() int64 { return v.caret }

// Selection returns the start and the length of the selection; 0 - none
func (v *View) Selection() (int64, int64) {
	a, b := min(v.anchor, v.caret), max(v.anchor, v.caret)
	return a, b - a
}

// InsertMode tells whether typing inserts bytes (or overwrites them)
func (v *View) InsertMode() bool { return v.insert }

func (v *View) SetInsertMode(on bool) {
	v.insert = on
	v.digit = 0
	v.changed()
}

// InTextColumn tells whether the caret is in the text column
func (v *View) InTextColumn() bool { return v.col == colText }

// SetBusy forbids edits while the document is being saved
func (v *View) SetBusy(busy bool) {
	v.busy = busy
	v.changed()
}

// GoTo puts the caret at the offset and shows it
func (v *View) GoTo(off int64) {
	off = max(0, min(off, v.doc.Size()))
	v.caret, v.anchor, v.digit = off, off, 0
	v.ensureCaretVisible()
	v.resetBlink()
	v.changed()
}

// Select selects n bytes at off, the caret at their end
func (v *View) Select(off, n int64) {
	size := v.doc.Size()
	off = max(0, min(off, size))
	v.anchor = off
	v.caret = min(off+n, size)
	v.digit = 0
	v.ensureCaretVisible()
	v.changed()
}

// SetFontSize changes the size of the text (zoom), in logical pixels
func (v *View) SetFontSize(size float64) {
	v.fontSize = max(6, min(size, 72))
	v.font = nil
	v.update()
}

func (v *View) FontSize() float64 { return v.fontSize }

// BytesPerRow is the number of bytes a row shows now
func (v *View) BytesPerRow() int { return v.bpr }

// palette is the colors of the view in the current theme
type palette struct {
	back, text, offset, zero, control, high, edited       color.RGBA
	selBack, selBackInactive, caret, caretText, rowCaret  color.RGBA
	placeholder, header, scrollTrack, scrollThumb, column color.RGBA
}

func currentPalette() *palette {
	if ui.IsDarkTheme {
		return &darkPalette
	}
	return &lightPalette
}

var darkPalette = palette{
	back:            ui.ColorFromHex("#1e1f22"),
	text:            ui.ColorFromHex("#d4d4d4"),
	offset:          ui.ColorFromHex("#6f8fb5"),
	zero:            ui.ColorFromHex("#5c6066"),
	control:         ui.ColorFromHex("#c99a5b"),
	high:            ui.ColorFromHex("#b48ead"),
	edited:          ui.ColorFromHex("#f05d5e"),
	selBack:         ui.ColorFromHex("#264f78"),
	selBackInactive: ui.ColorFromHex("#3a3d41"),
	caret:           ui.ColorFromHex("#e8e8e8"),
	caretText:       ui.ColorFromHex("#1e1f22"),
	rowCaret:        ui.ColorFromHex("#25272b"),
	placeholder:     ui.ColorFromHex("#45484d"),
	header:          ui.ColorFromHex("#7a7e85"),
	scrollTrack:     ui.ColorFromHex("#232428"),
	scrollThumb:     ui.ColorFromHex("#4a4d52"),
	column:          ui.ColorFromHex("#2b2d30"),
}

var lightPalette = palette{
	back:            ui.ColorFromHex("#ffffff"),
	text:            ui.ColorFromHex("#1f1f1f"),
	offset:          ui.ColorFromHex("#2f5f9a"),
	zero:            ui.ColorFromHex("#a0a4a8"),
	control:         ui.ColorFromHex("#9a5b00"),
	high:            ui.ColorFromHex("#8a3d8a"),
	edited:          ui.ColorFromHex("#d32f2f"),
	selBack:         ui.ColorFromHex("#add6ff"),
	selBackInactive: ui.ColorFromHex("#e2e4e8"),
	caret:           ui.ColorFromHex("#1f1f1f"),
	caretText:       ui.ColorFromHex("#ffffff"),
	rowCaret:        ui.ColorFromHex("#f4f6f8"),
	placeholder:     ui.ColorFromHex("#d6d9dd"),
	header:          ui.ColorFromHex("#6a6e75"),
	scrollTrack:     ui.ColorFromHex("#f0f0f0"),
	scrollThumb:     ui.ColorFromHex("#c1c4c8"),
	column:          ui.ColorFromHex("#e6e8eb"),
}
