package hexview

import (
	"image"
	"image/color"
	"math"

	"github.com/ipoluianov/althex/core/doc"
	"github.com/ipoluianov/nui/ui"
)

const hexDigits = "0123456789ABCDEF"

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

// draw draws the view of w x h pixels
func (v *View) draw(pc *paintCtx, w, h int) {
	v.layout(w, h)
	v.clampTop()
	pal := pc.pal
	f := v.font
	cw, lh := f.CharWidth, f.LineHeight
	fillRect(pc.img, pc.clip, pc.ox, pc.oy, w, h, pal.back)

	snap := v.doc.Snapshot()
	size := snap.Size()
	bpr := v.bpr
	if len(v.rowBuf) != bpr {
		v.rowBuf = make([]byte, bpr)
		v.rowMark = make([]bool, bpr)
	}
	selStart, selLen := v.Selection()
	selEnd := selStart + selLen
	caretRow := v.caret / int64(bpr)
	caretCol := int(v.caret % int64(bpr))
	textW := w - v.sbW

	v.drawHeader(pc, caretCol)
	baseline := (lh-f.Ascent-f.Descent)/2 + f.Ascent
	var hexBuf [2]byte
	for r := 0; r <= v.rowsShown; r++ {
		row := v.topRow + int64(r)
		off := row * int64(bpr)
		if off > size {
			break
		}
		y := pc.oy + v.headerH + r*lh
		if row == caretRow {
			fillRect(pc.img, pc.clip, pc.ox, y, textW, lh, pal.rowCaret)
		}
		by := y + baseline

		// The offset
		x := pc.ox + v.xOff
		for i := v.offDigits - 1; i >= 0; i-- {
			f.DrawChar(pc.img, pc.clip, x, by, hexDigits[(off>>(4*i))&15], pal.offset)
			x += cw
		}

		n, ready := snap.TryReadAt(v.rowBuf, off, v.dataArrived)
		snap.Edited(off, v.rowMark[:n])
		// The selection of the row: one band in each column
		if a, b := max(selStart, off), min(selEnd, off+int64(n)); a < b {
			back := pal.selBack
			if !v.focused {
				back = pal.selBackInactive
			}
			i0, i1 := int(a-off), int(b-off)-1
			x0 := pc.ox + v.cellX(i0)
			fillRect(pc.img, pc.clip, x0, y, pc.ox+v.cellX(i1)+2*cw-x0, lh, back)
			fillRect(pc.img, pc.clip, pc.ox+v.xText+i0*cw, y, (i1-i0+1)*cw, lh, back)
		}
		for i := 0; i < n; i++ {
			hx := pc.ox + v.cellX(i)
			tx := pc.ox + v.xText + i*cw
			if !ready {
				fillRect(pc.img, pc.clip, hx, y+lh/2-1, 2*cw-cw/3, 2, pal.placeholder)
				fillRect(pc.img, pc.clip, tx+cw/4, y+lh/2-1, cw/2, 2, pal.placeholder)
				continue
			}
			b := v.rowBuf[i]
			col := byteColor(pal, b)
			if v.rowMark[i] {
				col = pal.edited
			}
			hexBuf[0], hexBuf[1] = hexDigits[b>>4], hexDigits[b&15]
			f.DrawChar(pc.img, pc.clip, hx, by, hexBuf[0], col)
			f.DrawChar(pc.img, pc.clip, hx+cw, by, hexBuf[1], col)
			ch, chCol := byte('.'), pal.zero
			if b >= 0x20 && b < 0x7f {
				ch, chCol = b, pal.text
			}
			if v.rowMark[i] {
				chCol = pal.edited
			}
			f.DrawChar(pc.img, pc.clip, tx, by, ch, chCol)
		}
		if row == caretRow {
			v.drawCaret(pc, y, caretCol, snap)
		}
	}

	// The line between the hex and the text columns
	sepX := pc.ox + v.xText - cw
	fillRect(pc.img, pc.clip, sepX, pc.oy, max(1, v.toPx(1)), h, pal.column)
	v.drawScrollBar(pc)
}

// drawHeader draws the numbers of the columns, the one of the caret marked
func (v *View) drawHeader(pc *paintCtx, caretCol int) {
	pal := pc.pal
	f := v.font
	cw := f.CharWidth
	y := pc.oy + (v.headerH-f.LineHeight)/2 + (f.LineHeight-f.Ascent-f.Descent)/2 + f.Ascent
	for i := 0; i < v.bpr; i++ {
		col := pal.header
		if i == caretCol {
			col = pal.offset
		}
		x := pc.ox + v.cellX(i)
		f.DrawChar(pc.img, pc.clip, x, y, hexDigits[(i>>4)&15], col)
		f.DrawChar(pc.img, pc.clip, x+cw, y, hexDigits[i&15], col)
		f.DrawChar(pc.img, pc.clip, pc.ox+v.xText+i*cw, y, hexDigits[i&15], col)
	}
	fillRect(pc.img, pc.clip, pc.ox, pc.oy+v.headerH-1, v.pxW-v.sbW, max(1, v.toPx(1)), pal.column)
}

// drawCaret draws the caret in its column and marks its byte in the other
func (v *View) drawCaret(pc *paintCtx, y, i int, snap *doc.Snapshot) {
	pal := pc.pal
	f := v.font
	cw, lh := f.CharWidth, f.LineHeight
	hx := pc.ox + v.cellX(i) + v.nibble*cw
	tx := pc.ox + v.xText + i*cw
	activeX, otherX, otherW := hx, tx, cw
	if v.col == colText {
		activeX, otherX, otherW = tx, pc.ox+v.cellX(i), 2*cw
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
		ch := byte(' ')
		if v.col == colText {
			if b[0] >= 0x20 && b[0] < 0x7f {
				ch = b[0]
			} else {
				ch = '.'
			}
		} else if v.nibble == 0 {
			ch = hexDigits[b[0]>>4]
		} else {
			ch = hexDigits[b[0]&15]
		}
		base := y + (lh-f.Ascent-f.Descent)/2 + f.Ascent
		f.DrawChar(pc.img, pc.clip, activeX, base, ch, pal.caretText)
	}
}

func byteColor(pal *palette, b byte) color.RGBA {
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
