package hexview

import (
	"github.com/ipoluianov/nui/ui"
)

func (v *View) keyDown(key ui.Key, mods ui.KeyModifiers) bool {
	ctrl := mods.Ctrl || mods.Cmd
	shift := mods.Shift
	bpr := int64(v.bpr)
	size := v.doc.Size()
	page := int64(max(1, v.rowsShown-1)) * bpr
	switch key {
	case ui.KeyArrowLeft:
		if v.col == colNum && v.digit > 0 && !shift {
			v.digit--
			v.moved(false)
			return true
		}
		v.moveCaret(v.caret-1, shift)
	case ui.KeyArrowRight:
		v.moveCaret(v.caret+1, shift)
	case ui.KeyArrowUp:
		if ctrl {
			v.scrollRows(-1)
			return true
		}
		v.moveCaret(v.caret-bpr, shift)
	case ui.KeyArrowDown:
		if ctrl {
			v.scrollRows(1)
			return true
		}
		v.moveCaret(v.caret+bpr, shift)
	case ui.KeyPageUp:
		v.setTop(v.topRow - page/bpr)
		v.moveCaret(v.caret-page, shift)
	case ui.KeyPageDown:
		v.setTop(v.topRow + page/bpr)
		v.moveCaret(min(v.caret+page, size), shift)
	case ui.KeyHome:
		if ctrl {
			v.moveCaret(0, shift)
		} else {
			v.moveCaret(v.caret-v.caret%bpr, shift)
		}
	case ui.KeyEnd:
		if ctrl {
			v.moveCaret(size, shift)
		} else {
			v.moveCaret(min(v.caret-v.caret%bpr+bpr-1, size), shift)
		}
	case ui.KeyTab:
		if ctrl {
			return false
		}
		v.col = 1 - v.col
		v.digit = 0
		v.moved(true)
	case ui.KeyInsert:
		if ctrl || shift {
			return false
		}
		v.SetInsertMode(!v.insert)
	case ui.KeyDelete:
		if ctrl || shift {
			return false
		}
		v.DeleteForward()
	case ui.KeyBackspace:
		v.DeleteBackward()
	default:
		return false
	}
	return true
}

// moveCaret puts the caret at pos; extend keeps the anchor (a selection)
func (v *View) moveCaret(pos int64, extend bool) {
	pos = max(0, min(pos, v.doc.Size()))
	v.caret = pos
	v.digit = 0
	v.moved(extend)
}

// moved shows the caret after it moved
func (v *View) moved(extend bool) {
	if !extend {
		v.anchor = v.caret
	}
	v.ensureCaretVisible()
	v.resetBlink()
	v.changed()
}

func (v *View) scrollRows(n int64) {
	v.setTop(v.topRow + n)
}

func (v *View) charTyped(r rune, mods ui.KeyModifiers) bool {
	if (mods.Ctrl || mods.Cmd) && !mods.Alt {
		return false
	}
	if mods.Alt && !mods.Ctrl {
		return false
	}
	if r < 32 || r == 127 {
		return false
	}
	if v.col == colNum {
		d := digitValue(r)
		if d < 0 || d >= v.opts.Mode.base() {
			return true // not a digit of the mode: nothing to type
		}
		v.typeDigit(d)
		return true
	}
	v.typeRune(r)
	return true
}

func digitValue(r rune) int {
	switch {
	case r >= '0' && r <= '9':
		return int(r - '0')
	case r >= 'a' && r <= 'f':
		return int(r-'a') + 10
	case r >= 'A' && r <= 'F':
		return int(r-'A') + 10
	}
	return -1
}

// ---- Mouse ----

// hit returns the position under the point (logical pixels of the view),
// the column and the digit of the byte; ok is false before the layout
func (v *View) hit(x, y int) (pos int64, col, digit int, ok bool) {
	if v.font == nil {
		return 0, 0, 0, false
	}
	px, py := v.toPx(x), v.toPx(y)
	cw := v.font.CharWidth
	row := v.topRow + int64(max(0, py-v.headerH)/v.font.LineHeight)
	j := 0
	if px >= v.xText-cw {
		col = colText
		j = max(0, min((px-v.xText)/cw, v.bpr-1))
	} else {
		col = colNum
		// The cell whose start is the last one left of the point (with half a gap)
		i := v.bpr - 1
		for ; i > 0 && px < v.cellX(i)-cw/2; i-- {
		}
		digit = max(0, min((px-v.cellX(i))/cw, v.opts.Mode.digits()-1))
		j = v.byteOfCell(i)
	}
	pos = row*int64(v.bpr) + int64(j)
	if size := v.doc.Size(); pos >= size {
		pos, digit = size, 0
	}
	return pos, col, digit, true
}

func (v *View) mouseDown(button ui.MouseButton, x, y int, mods ui.KeyModifiers) bool {
	v.Focus()
	if button != ui.MouseButtonLeft {
		return false
	}
	v.mouse.x, v.mouse.y = x, y
	if v.toPx(x) >= v.pxW-v.sbW {
		v.scrollBarDown(v.toPx(y))
		return true
	}
	pos, col, digit, ok := v.hit(x, y)
	if !ok {
		return true
	}
	v.col = col
	if mods.Shift {
		v.caret = pos
		v.digit = 0
		v.moved(true)
		return true
	}
	v.caret, v.anchor = pos, pos
	v.digit = digit
	if col != colNum || v.insert {
		v.digit = 0
	}
	v.mouse.selecting = true
	v.mouse.origin = pos
	v.moved(true)
	return true
}

func (v *View) mouseMove(x, y int, mods ui.KeyModifiers) bool {
	v.mouse.x, v.mouse.y = x, y
	if v.mouse.dragBar {
		v.scrollBarDrag(v.toPx(y))
		return true
	}
	if v.mouse.selecting {
		v.dragTo(x, y)
		return true
	}
	return false
}

func (v *View) mouseUp(button ui.MouseButton, x, y int, mods ui.KeyModifiers) bool {
	v.mouse.selecting = false
	v.mouse.dragBar = false
	return true
}

// dragTo selects from the byte the drag started on to the one under the point
func (v *View) dragTo(x, y int) {
	pos, _, _, ok := v.hit(x, y)
	if !ok {
		return
	}
	size := v.doc.Size()
	o := v.mouse.origin
	if pos >= o {
		v.anchor, v.caret = o, min(pos+1, size)
	} else {
		v.anchor, v.caret = min(o+1, size), pos
	}
	if v.caret != v.anchor {
		v.digit = 0
	}
	v.ensureCaretVisible()
	v.changed()
}

// autoScroll scrolls while a selection is dragged above or below the view
func (v *View) autoScroll() {
	py := v.toPx(v.mouse.y)
	switch {
	case py < v.headerH:
		v.scrollRows(-1)
	case py > v.pxH:
		v.scrollRows(1)
	default:
		return
	}
	v.dragTo(v.mouse.x, v.mouse.y)
}

func (v *View) mouseWheel(dx, dy int) bool {
	if f := v.Form(); f != nil && (f.KeyModifiers().Ctrl || f.KeyModifiers().Cmd) {
		if dy > 0 {
			v.SetFontSize(v.fontSize + 1)
		} else if dy < 0 {
			v.SetFontSize(v.fontSize - 1)
		}
		return true
	}
	v.scrollRows(int64(-dy * 3))
	if v.mouse.selecting {
		v.dragTo(v.mouse.x, v.mouse.y)
	}
	return true
}

func (v *View) scrollBarDown(py int) {
	ty, th, _ := v.scrollGeometry()
	switch {
	case py < ty:
		v.scrollRows(-int64(v.rowsShown - 1))
	case py >= ty+th:
		v.scrollRows(int64(v.rowsShown - 1))
	default:
		v.mouse.dragBar = true
		v.mouse.dragY = py
		v.mouse.dragTop = v.topRow
	}
}

func (v *View) scrollBarDrag(py int) {
	_, th, track := v.scrollGeometry()
	if track <= th {
		return
	}
	maxTop := float64(v.maxTop())
	delta := float64(py-v.mouse.dragY) * maxTop / float64(track-th)
	v.setTop(v.mouse.dragTop + int64(delta))
}
