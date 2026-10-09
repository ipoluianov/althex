// Package hexview is the widget that shows and edits a document: the
// offsets, the bytes in hex and the bytes as text. It draws only the rows
// on the screen and never waits for a disk: bytes not read yet are shown as
// placeholders and drawn when they arrive.
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
	colHex  = 0
	colText = 1
)

// DefaultFontSize is the size of the text in logical pixels
const DefaultFontSize = 14

// View shows a document
type View struct {
	ui.Widget

	doc         *doc.Document
	unsubscribe func()

	// The font in pixels of the screen, made for fontSize at the scale
	font     *Font
	fontSize float64
	scale    float64

	// Layout in pixels of the screen, see layout
	bpr       int // bytes per row
	offDigits int
	padX      int
	xOff      int
	xHex      int
	xText     int
	headerH   int
	sbW       int
	pxW, pxH  int
	rowsShown int

	topRow int64

	// The caret is a position between bytes (0..Size); the selection is
	// [min(anchor, caret), max(anchor, caret))
	caret  int64
	anchor int64
	nibble int // 1: the low half of the byte at the caret is typed next
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

	// OnStateChanged is called when the caret, the selection, the mode or
	// the document change: for the status bar
	OnStateChanged func()
	// OnError reports an edit that failed
	OnError func(err error)
}

// NewView makes a view of the document
func NewView(d *doc.Document) *View {
	v := &View{doc: d, fontSize: DefaultFontSize, bpr: 16, caretOn: true}
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
	})
	v.SetOnFocusLost(func() {
		v.focused = false
		v.update()
	})
	v.AddTimer(100, v.timer)
	v.unsubscribe = d.Subscribe(v.docChanged)
	return v
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

// layout places the columns for the size in pixels of the screen
func (v *View) layout(w, h int) {
	f := v.font
	cw := f.CharWidth
	v.pxW, v.pxH = w, h
	v.offDigits = 8
	for size := v.doc.Size(); size>>(4*v.offDigits) > 0 && v.offDigits < 16; {
		v.offDigits++
	}
	v.padX = cw / 2
	v.sbW = int(math.Round(12 * v.scale))
	v.xOff = v.padX
	v.xHex = v.xOff + (v.offDigits+2)*cw
	v.xText = v.xHex + v.hexWidth() + 2*cw
	v.headerH = f.LineHeight + int(math.Round(4*v.scale))
	v.rowsShown = max(1, (h-v.headerH)/f.LineHeight)
}

// cellX returns the x of the hex cell of the i-th byte of a row; every
// 8 bytes are set off by a space
func (v *View) cellX(i int) int {
	cw := v.font.CharWidth
	return v.xHex + i*3*cw + (i/8)*cw
}

func (v *View) hexWidth() int {
	return v.cellX(v.bpr-1) + 2*v.font.CharWidth - v.xHex
}

// totalRows counts the rows, with the one where a byte can be added at the end
func (v *View) totalRows() int64 {
	return v.doc.Size()/int64(v.bpr) + 1
}

func (v *View) maxTop() int64 {
	return max(0, v.totalRows()-int64(v.rowsShown))
}

func (v *View) clampTop() {
	v.topRow = max(0, min(v.topRow, v.maxTop()))
}

// ensureCaretVisible scrolls so the row of the caret is on the screen
func (v *View) ensureCaretVisible() {
	row := v.caret / int64(v.bpr)
	if row < v.topRow {
		v.topRow = row
	} else if row >= v.topRow+int64(v.rowsShown) {
		v.topRow = row - int64(v.rowsShown) + 1
	}
	v.clampTop()
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
	v.nibble = 0
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
	v.caret, v.anchor, v.nibble = off, off, 0
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
	v.nibble = 0
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
