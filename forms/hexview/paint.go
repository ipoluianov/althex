package hexview

import (
	"image"
	"image/color"
	"math"

	"github.com/ipoluianov/althex/core/doc"
	"github.com/ipoluianov/nui/ui"
)

const digitChars = "0123456789ABCDEF"

// textContext is how many bytes around a row are read to decode the
// characters across its edges
const textContext = 3

// paintCtx is where a paint draws: pixels of the screen
type paintCtx struct {
	img    *image.RGBA
	clip   image.Rectangle
	ox, oy int // the top left corner of the view
	pal    *palette
}

func (v *View) paint(cnv *ui.Canvas) {
	if f := v.Form(); f != nil {
		v.form.Store(f)
	}
	scale := cnv.Scale()
	v.ensureFont(scale)
	pc := &paintCtx{img: cnv.RGBA(), pal: currentPalette()}
	pc.clip = image.Rect(cnv.ClipX(), cnv.ClipY(), cnv.ClipX()+cnv.ClipW(), cnv.ClipY()+cnv.ClipH())
	// The origin in pixels: the clip starts there unless a parent cuts the view
	pc.ox = int(math.Round(float64(cnv.TranslatedX()) * scale))
	pc.oy = int(math.Round(float64(cnv.TranslatedY()) * scale))
	if d := cnv.ClipX() - pc.ox; d >= -2 && d <= 2 {
		pc.ox = cnv.ClipX()
	}
	if d := cnv.ClipY() - pc.oy; d >= -2 && d <= 2 {
		pc.oy = cnv.ClipY()
	}
	v.draw(pc, v.toPx(v.Width()), v.toPx(v.Height()))
}

// rowCtx is what the drawing of a row needs
type rowCtx struct {
	off      int64 // of the row
	n        int   // bytes in the row
	y, base  int   // the top and the baseline
	ready    bool
	data     []byte // the bytes of the row
	selStart int64
	selEnd   int64
}

// draw draws the view of w x h pixels
func (v *View) draw(pc *paintCtx, w, h int) {
	v.layout(w, h)
	v.clampTop()
	pal := pc.pal
	f := v.font
	lh := f.LineHeight
	fillRect(pc.img, pc.clip, pc.ox, pc.oy, w, h, pal.back)

	snap := v.doc.Snapshot()
	size := snap.Size()
	bpr := v.bpr
	if len(v.rowBuf) != bpr {
		v.rowBuf = make([]byte, bpr)
		v.rowMark = make([]bool, bpr)
	}
	selStart, selLen := v.Selection()
	caretRow := v.caret / int64(bpr)
	caretByte := int(v.caret % int64(bpr))

	first := v.topRow * int64(bpr)
	last := min((v.topRow+int64(v.rowsShown)+1)*int64(bpr), size)
	v.hlBuf = v.hlBuf[:0]
	if v.Highlights != nil && first < last {
		v.hlBuf = append(v.hlBuf, v.Highlights(first, last)...)
	}

	v.drawHeader(pc, caretByte)
	baseline := (lh-f.Ascent-f.Descent)/2 + f.Ascent
	for r := 0; r <= v.rowsShown; r++ {
		row := v.topRow + int64(r)
		off := row * int64(bpr)
		if off > size {
			break
		}
		rc := rowCtx{off: off, y: pc.oy + v.headerH + r*lh, selStart: selStart, selEnd: selStart + selLen}
		rc.base = rc.y + baseline
		if row == caretRow {
			fillRect(pc.img, pc.clip, pc.ox, rc.y, w-v.sbW, lh, pal.rowCaret)
		}
		v.drawOffset(pc, &rc)
		rc.n, rc.ready = snap.TryReadAt(v.rowBuf, off, v.dataArrived)
		rc.data = v.rowBuf[:rc.n]
		snap.Edited(off, v.rowMark[:rc.n])
		v.drawBands(pc, &rc)
		v.drawNumbers(pc, &rc)
		v.drawText(pc, &rc, snap)
		v.drawFrames(pc, &rc)
		if row == caretRow {
			v.drawCaret(pc, rc.y, caretByte, snap)
		}
	}

	// The line between the number and the text columns
	sepX := pc.ox + v.xText - f.CharWidth
	fillRect(pc.img, pc.clip, sepX, pc.oy, max(1, v.toPx(1)), h, pal.column)
	v.drawScrollBar(pc)
}

// drawOffset writes the offset of the row in the base of the options,
// relative to the mark when asked
func (v *View) drawOffset(pc *paintCtx, rc *rowCtx) {
	f := v.font
	off := rc.off
	neg := false
	if v.opts.Relative {
		off -= v.opts.RelativeTo
		if off < 0 {
			neg, off = true, -off
		}
	}
	var buf [24]byte
	i := len(buf)
	base := int64(v.opts.OffsetBase)
	digits := v.offChars
	if v.opts.Relative {
		digits--
	}
	for n := 0; (n < digits || off > 0) && i > 1; n++ {
		i--
		buf[i] = digitChars[off%base]
		off /= base
	}
	if v.opts.Relative {
		i--
		buf[i] = '+'
		if neg {
			buf[i] = '-'
		}
	}
	x := pc.ox + v.xOff
	for _, c := range buf[i:] {
		f.DrawChar(pc.img, pc.clip, x, rc.base, rune(c), pc.pal.offset)
		x += f.CharWidth
	}
}

// fillCells fills the cells of the bytes of [a, b) in the row in both
// columns, joining the neighbors
func (v *View) fillCells(pc *paintCtx, rc *rowCtx, a, b int64, col color.RGBA) {
	a, b = max(a, rc.off), min(b, rc.off+int64(v.bpr))
	if a >= b {
		return
	}
	cw, lh := v.font.CharWidth, v.font.LineHeight
	in := func(cell int) bool {
		pos := rc.off + int64(v.byteOfCell(cell))
		return pos >= a && pos < b
	}
	for i := 0; i < v.bpr; i++ {
		if !in(i) {
			continue
		}
		wCell := v.cellW
		if i+1 < v.bpr && in(i+1) {
			wCell = v.cellX(i+1) - v.cellX(i)
		}
		fillRect(pc.img, pc.clip, pc.ox+v.cellX(i), rc.y, wCell, lh, col)
	}
	i0, i1 := int(a-rc.off), int(b-rc.off)
	fillRect(pc.img, pc.clip, pc.ox+v.xText+i0*cw, rc.y, (i1-i0)*cw, lh, col)
}

// drawBands draws the backgrounds: the highlights, then the selection
func (v *View) drawBands(pc *paintCtx, rc *rowCtx) {
	end := rc.off + int64(v.bpr)
	for _, hl := range v.hlBuf {
		if !hl.Frame && hl.Off < end && hl.Off+hl.Len > rc.off {
			v.fillCells(pc, rc, hl.Off, hl.Off+hl.Len, hl.Color)
		}
	}
	if rc.selStart < rc.selEnd {
		back := pc.pal.selBack
		if !v.focused {
			back = pc.pal.selBackInactive
		}
		v.fillCells(pc, rc, rc.selStart, rc.selEnd, back)
	}
}

// drawFrames draws the outlines of the highlights that have them
func (v *View) drawFrames(pc *paintCtx, rc *rowCtx) {
	end := rc.off + int64(v.bpr)
	lh := v.font.LineHeight
	for _, hl := range v.hlBuf {
		if !hl.Frame || hl.Off >= end || hl.Off+hl.Len <= rc.off {
			continue
		}
		for j := max(hl.Off, rc.off); j < min(hl.Off+hl.Len, end); j++ {
			x := pc.ox + v.cellX(v.cellOfByte(int(j-rc.off)))
			strokeRect(pc.img, pc.clip, x-1, rc.y, v.cellW+2, lh, hl.Color)
		}
	}
}

// byteColor returns the color of a byte: by the rules of the options, else by its kind
func (v *View) byteColor(pal *palette, b byte) color.RGBA {
	for _, r := range v.opts.ColorRules {
		if b >= r.From && b <= r.To {
			return r.Color
		}
	}
	switch {
	case b == 0:
		return pal.zero
	case b == 0xff:
		return pal.high
	case b >= 0x20 && b < 0x7f:
		return pal.text
	}
	return pal.control
}

// drawNumbers writes the bytes of the row in the number column
func (v *View) drawNumbers(pc *paintCtx, rc *rowCtx) {
	f := v.font
	cw, lh := f.CharWidth, f.LineHeight
	digits, base := v.opts.Mode.digits(), v.opts.Mode.base()
	for i := 0; i < v.bpr; i++ {
		j := v.byteOfCell(i)
		if j >= rc.n {
			continue
		}
		x := pc.ox + v.cellX(i)
		if !rc.ready {
			fillRect(pc.img, pc.clip, x, rc.y+lh/2-1, v.cellW-cw/3, 2, pc.pal.placeholder)
			continue
		}
		b := rc.data[j]
		col := v.byteColor(pc.pal, b)
		if v.rowMark[j] {
			col = pc.pal.edited
		}
		val := int(b)
		for d := digits - 1; d >= 0; d-- {
			f.DrawChar(pc.img, pc.clip, x+d*cw, rc.base, rune(digitChars[val%base]), col)
			val /= base
		}
	}
}

// drawText writes the characters of the row in the text column: a
// character of several bytes is drawn at its first one, the others stay empty
func (v *View) drawText(pc *paintCtx, rc *rowCtx, snap *doc.Snapshot) {
	f := v.font
	cw, lh := f.CharWidth, f.LineHeight
	pal := pc.pal
	if !rc.ready {
		for i := 0; i < rc.n; i++ {
			fillRect(pc.img, pc.clip, pc.ox+v.xText+i*cw+cw/4, rc.y+lh/2-1, cw/2, 2, pal.placeholder)
		}
		return
	}
	enc := v.opts.Encoding
	if enc == nil {
		for i, b := range rc.data {
			ch, col := rune('.'), pal.zero
			if b >= 0x20 && b < 0x7f {
				ch, col = rune(b), pal.text
			}
			if v.rowMark[i] {
				col = pal.edited
			}
			f.DrawChar(pc.img, pc.clip, pc.ox+v.xText+i*cw, rc.base, ch, col)
		}
		return
	}
	// The bytes around the row, to decode characters across its edges
	pre := int(min(rc.off, textContext))
	need := pre + rc.n + textContext
	if cap(v.textBuf) < need {
		v.textBuf = make([]byte, need)
	}
	buf := v.textBuf[:need]
	n, _ := snap.TryReadAt(buf, rc.off-int64(pre), nil)
	buf = buf[:n]
	unit := max(enc.Unit(), 1)
	// Characters start at offsets that are multiples of the unit
	i := 0
	if unit > 1 {
		i = int((int64(unit) - (rc.off-int64(pre))%int64(unit)) % int64(unit))
	}
	for i < pre+rc.n && i < len(buf) {
		r, size, ok := enc.Decode(buf, i)
		if size < 1 {
			size = unit
		}
		if i+size > pre { // the character covers bytes of the row
			cell := i - pre
			if !ok {
				// Not a character: each byte of it is a dot
				for k := max(cell, 0); k < min(cell+size, rc.n); k++ {
					v.drawTextChar(pc, rc, k, '.', pal.zero)
				}
			} else if cell >= 0 {
				col := pal.text
				if v.rowMark[cell] {
					col = pal.edited
				}
				v.drawTextChar(pc, rc, cell, r, col)
			}
		}
		i += size
	}
}

func (v *View) drawTextChar(pc *paintCtx, rc *rowCtx, cell int, r rune, col color.RGBA) {
	if r < 0x20 {
		r, col = '.', pc.pal.zero
	}
	v.font.DrawChar(pc.img, pc.clip, pc.ox+v.xText+cell*v.font.CharWidth, rc.base, r, col)
}

// drawHeader writes the numbers of the bytes in a row above the columns,
// the one of the caret marked
func (v *View) drawHeader(pc *paintCtx, caretByte int) {
	pal := pc.pal
	f := v.font
	cw := f.CharWidth
	y := pc.oy + (v.headerH-f.LineHeight)/2 + (f.LineHeight-f.Ascent-f.Descent)/2 + f.Ascent
	g := v.opts.Group
	for i := 0; i < v.bpr; i += g {
		// A group is labeled by its first byte
		col := pal.header
		if caretByte >= i && caretByte < i+g {
			col = pal.offset
		}
		x := pc.ox + v.cellX(i)
		if v.cellW >= 2*cw {
			f.DrawChar(pc.img, pc.clip, x, y, rune(digitChars[(i>>4)&15]), col)
			f.DrawChar(pc.img, pc.clip, x+cw, y, rune(digitChars[i&15]), col)
		}
	}
	for i := 0; i < v.bpr; i++ {
		col := pal.header
		if i == caretByte {
			col = pal.offset
		}
		f.DrawChar(pc.img, pc.clip, pc.ox+v.xText+i*cw, y, rune(digitChars[i&15]), col)
	}
	fillRect(pc.img, pc.clip, pc.ox, pc.oy+v.headerH-1, v.pxW-v.sbW, max(1, v.toPx(1)), pal.column)
}

// drawCaret draws the caret in its column and marks its byte in the other
func (v *View) drawCaret(pc *paintCtx, y, j int, snap *doc.Snapshot) {
	pal := pc.pal
	f := v.font
	cw, lh := f.CharWidth, f.LineHeight
	cellX := pc.ox + v.cellX(v.cellOfByte(j))
	tx := pc.ox + v.xText + j*cw
	activeX, otherX, otherW := cellX+v.digit*cw, tx, cw
	if v.col == colText {
		activeX, otherX, otherW = tx, cellX, v.cellW
	}
	strokeRect(pc.img, pc.clip, otherX-1, y, otherW+2, lh, pal.header)
	if !v.caretOn || !v.focused {
		strokeRect(pc.img, pc.clip, activeX-1, y, cw+2, lh, pal.caret)
		return
	}
	if v.insert {
		fillRect(pc.img, pc.clip, activeX-1, y, max(2, v.toPx(2)), lh, pal.caret)
		return
	}
	// Overwrite: a block with the character inverted
	fillRect(pc.img, pc.clip, activeX, y, cw, lh, pal.caret)
	var b [1]byte
	if n, _ := snap.TryReadAt(b[:], v.caret, nil); n == 1 {
		ch := rune(' ')
		if v.col == colText {
			ch = '.'
			if b[0] >= 0x20 && b[0] < 0x7f {
				ch = rune(b[0])
			}
		} else {
			ch = rune(v.digitChar(b[0], v.digit))
		}
		base := y + (lh-f.Ascent-f.Descent)/2 + f.Ascent
		f.DrawChar(pc.img, pc.clip, activeX, base, ch, pal.caretText)
	}
}

// digitChar returns the k-th digit (from the left) of the byte in the mode
func (v *View) digitChar(b byte, k int) byte {
	digits, base := v.opts.Mode.digits(), v.opts.Mode.base()
	val := int(b)
	for d := digits - 1; d > k; d-- {
		val /= base
	}
	return digitChars[val%base]
}

// ---- Scroll bar ----

// scrollGeometry returns the thumb of the scroll bar: its y and height in pixels
func (v *View) scrollGeometry() (thumbY, thumbH, track int) {
	track = v.pxH
	maxTop := v.maxTop()
	if maxTop == 0 {
		return 0, track, track
	}
	total := float64(v.totalRows())
	thumbH = max(v.toPx(20), int(float64(track)*float64(v.rowsShown)/total))
	thumbY = int(float64(track-thumbH) * float64(v.topRow) / float64(maxTop))
	return thumbY, thumbH, track
}

func (v *View) drawScrollBar(pc *paintCtx) {
	pal := pc.pal
	x := pc.ox + v.pxW - v.sbW
	fillRect(pc.img, pc.clip, x, pc.oy, v.sbW, v.pxH, pal.scrollTrack)
	if v.maxTop() == 0 {
		return
	}
	ty, th, _ := v.scrollGeometry()
	pad := max(2, v.toPx(2))
	fillRect(pc.img, pc.clip, x+pad, pc.oy+ty, v.sbW-2*pad, th, pal.scrollThumb)
	// The caret on the bar
	cy := int(float64(v.pxH) * float64(v.caret/int64(v.bpr)) / float64(v.totalRows()))
	fillRect(pc.img, pc.clip, x, pc.oy+cy, v.sbW, max(2, v.toPx(2)), pal.offset)
}
