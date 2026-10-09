package forms

import (
	"image/color"

	"github.com/ipoluianov/nui/ui"
)

// TabItem is a tab of a TabBar
type TabItem struct {
	Title    string
	Tooltip  string
	Modified bool
	ReadOnly bool
}

// TabBar is the row of the document tabs of a view: a click activates a
// tab, the cross or a middle click closes it, a drag moves it
type TabBar struct {
	ui.Widget

	items  []TabItem
	active int
	first  int // the first tab shown when they do not fit

	hover      int
	hoverClose bool
	hoverArrow int // -1 left, 1 right

	dragFrom int
	dragging bool
	downX    int

	closeButtons bool
	focusedView  bool

	OnSelect      func(i int)
	OnClose       func(i int)
	OnMove        func(from, to int)
	OnDoubleClick func(i int) // -1: the empty part of the bar
	OnContextMenu func(i int, x, y int)
	OnDropFromBar func()
}

const (
	tabPadding   = 10
	tabCloseSize = 14
	tabMinWidth  = 60
	tabMaxWidth  = 240
	tabArrowW    = 18
)

func NewTabBar() *TabBar {
	var c TabBar
	c.InitWidget()
	c.SetTypeName("TabBar")
	c.hover = -1
	c.dragFrom = -1
	c.closeButtons = true
	c.SetXExpandable(true)
	h := ui.ThemeRowHeight() + 6
	c.SetMinHeight(h)
	c.SetMaxHeight(h)
	c.SetOnPaint(c.paint)
	c.SetOnMouseDown(c.mouseDown)
	c.SetOnMouseUp(c.mouseUp)
	c.SetOnMouseMove(c.mouseMove)
	c.SetOnMouseLeave(func() {
		c.hover = -1
		c.hoverArrow = 0
		c.update()
	})
	c.SetOnMouseDblClick(func(button ui.MouseButton, x, y int, mods ui.KeyModifiers) bool {
		if button == ui.MouseButtonLeft && c.OnDoubleClick != nil {
			i, _ := c.tabAt(x)
			c.OnDoubleClick(i)
		}
		return true
	})
	c.SetOnMouseWheel(func(dx, dy int) bool {
		if dy > 0 {
			c.scrollBy(-1)
		} else if dy < 0 {
			c.scrollBy(1)
		}
		return true
	})
	return &c
}

func (c *TabBar) update() {
	if c.Form() != nil {
		c.Form().Update()
	}
}

// SetItems sets the tabs and the active one
func (c *TabBar) SetItems(items []TabItem, active int) {
	c.items = items
	c.active = active
	if c.first >= len(items) {
		c.first = max(0, len(items)-1)
	}
	c.ensureVisible(active)
	c.update()
}

// SetCloseButtons shows or hides the close buttons of the tabs
func (c *TabBar) SetCloseButtons(show bool) {
	c.closeButtons = show
	c.update()
}

// SetFocusedView marks the bar of the view with the focus
func (c *TabBar) SetFocusedView(focused bool) {
	if c.focusedView != focused {
		c.focusedView = focused
		c.update()
	}
}

func (c *TabBar) fontSize() float64 { return ui.ThemeFontSize() - 1 }

func (c *TabBar) tabWidth(i int) int {
	w, _, _ := ui.MeasureText(ui.ThemeFontFamily(), c.fontSize(), c.items[i].Title)
	w += tabPadding*2 + 12 // the state mark
	if c.closeButtons {
		w += tabCloseSize + 4
	}
	return max(tabMinWidth, min(w, tabMaxWidth))
}

// overflow reports whether the tabs do not fit, so the arrows are shown
func (c *TabBar) overflow() bool {
	total := 0
	for i := range c.items {
		total += c.tabWidth(i)
	}
	return total > c.Width()
}

func (c *TabBar) tabsRight() int {
	if c.overflow() {
		return c.Width() - 2*tabArrowW
	}
	return c.Width()
}

// tabRects returns the x and width of the shown tabs from first on
func (c *TabBar) tabX(i int) (int, int) {
	x := 0
	for k := c.first; k < i; k++ {
		x += c.tabWidth(k)
	}
	return x, c.tabWidth(i)
}

func (c *TabBar) ensureVisible(i int) {
	if i < 0 || i >= len(c.items) || c.Width() <= 0 {
		return
	}
	if i < c.first {
		c.first = i
		return
	}
	right := c.tabsRight()
	for c.first < i {
		x, w := c.tabX(i)
		if x+w <= right {
			break
		}
		c.first++
	}
}

func (c *TabBar) scrollBy(n int) {
	c.first = max(0, min(c.first+n, len(c.items)-1))
	c.update()
}

// tabAt returns the tab at x and whether x is on its close button; -1 if none
func (c *TabBar) tabAt(x int) (int, bool) {
	right := c.tabsRight()
	if x >= right {
		return -1, false
	}
	pos := 0
	for i := c.first; i < len(c.items); i++ {
		w := c.tabWidth(i)
		if x >= pos && x < pos+w {
			onClose := c.closeButtons && x >= pos+w-tabPadding-tabCloseSize && x < pos+w-tabPadding+2
			return i, onClose
		}
		pos += w
		if pos >= right {
			break
		}
	}
	return -1, false
}

func (c *TabBar) paint(cnv *ui.Canvas) {
	p := ui.CurrentPalette()
	w, h := c.Width(), c.Height()
	bg := ui.MixColors(p.Window, p.Base, 0.35)
	cnv.FillRect(0, 0, w, h, bg)
	cnv.FillRect(0, h-1, w, 1, p.Border)
	right := c.tabsRight()
	fs := c.fontSize()
	x := 0
	for i := c.first; i < len(c.items); i++ {
		tw := c.tabWidth(i)
		if x >= right {
			break
		}
		it := c.items[i]
		active := i == c.active
		tabBg := bg
		switch {
		case active:
			tabBg = p.Base
		case i == c.hover:
			tabBg = ui.MixColors(bg, p.Base, 0.5)
		}
		cnv.Save()
		cnv.TranslateAndClip(x, 0, min(tw, right-x), h)
		cnv.FillRect(0, 2, tw, h-2, tabBg)
		if active {
			accent := p.Highlight
			if !c.focusedView {
				accent = ui.MixColors(p.Highlight, p.Base, 0.5)
			}
			cnv.FillRect(0, 2, tw, 2, accent)
		} else {
			cnv.FillRect(0, h-1, tw, 1, p.Border)
		}
		cnv.FillRect(tw-1, 6, 1, h-12, p.Divider)
		// The state: a dot for the unsaved changes
		mx := tabPadding
		if it.Modified {
			fillCircle(cnv, mx+3, h/2, 4, colorModified.get())
		}
		textX := mx + 12
		textW := tw - textX - tabPadding
		if c.closeButtons {
			textW -= tabCloseSize + 4
		}
		col := p.Text
		if it.ReadOnly {
			col = p.DisabledText
		}
		if !active {
			col = ui.MixColors(col, tabBg, 0.2)
		}
		cnv.SetFontFamily(ui.ThemeFontFamily())
		cnv.SetFontSize(fs)
		cnv.SetColor(col)
		cnv.SetHAlign(ui.HAlignLeft)
		cnv.SetVAlign(ui.VAlignCenter)
		cnv.DrawText(textX, 2, textW, h-2, fitText(it.Title, fs, textW))
		if c.closeButtons && (active || i == c.hover) {
			cx := tw - tabPadding - tabCloseSize
			cy := (h - tabCloseSize) / 2
			xc := p.Text
			if i == c.hover && c.hoverClose {
				cnv.FillRect(cx, cy, tabCloseSize, tabCloseSize, ui.MixColors(tabBg, p.Text, 0.2))
			} else {
				xc = ui.MixColors(p.Text, tabBg, 0.4)
			}
			cnv.DrawLine(cx+4, cy+4, cx+tabCloseSize-4, cy+tabCloseSize-4, 1, xc)
			cnv.DrawLine(cx+tabCloseSize-4, cy+4, cx+4, cy+tabCloseSize-4, 1, xc)
		}
		cnv.Restore()
		x += tw
	}
	// Where a dragged tab goes
	if c.dragging && c.hover >= 0 && c.hover != c.dragFrom {
		dx, dw := c.tabX(c.hover)
		if c.hover > c.dragFrom {
			dx += dw - 2
		}
		cnv.FillRect(dx, 2, 2, h-2, p.Highlight)
	}
	if c.overflow() {
		ax := w - 2*tabArrowW
		cnv.FillRect(ax, 0, 2*tabArrowW, h, bg)
		for k, dir := range []int{-1, 1} {
			col := p.Text
			if (dir < 0 && c.first == 0) || (dir > 0 && c.first >= len(c.items)-1) {
				col = p.DisabledText
			} else if c.hoverArrow == dir {
				cnv.FillRect(ax+k*tabArrowW, 2, tabArrowW, h-2, ui.MixColors(bg, p.Base, 0.6))
			}
			cx := ax + k*tabArrowW + tabArrowW/2
			cy := h / 2
			if dir < 0 {
				cnv.FillTriangle(cx+3, cy-5, cx+3, cy+5, cx-3, cy, col)
			} else {
				cnv.FillTriangle(cx-3, cy-5, cx-3, cy+5, cx+3, cy, col)
			}
		}
	}
}

func fillCircle(cnv *ui.Canvas, cx, cy, r int, col color.RGBA) {
	for y := -r; y <= r; y++ {
		for x := -r; x <= r; x++ {
			d := x*x + y*y
			if d <= r*r-r {
				cnv.FillRect(cx+x, cy+y, 1, 1, col)
			} else if d <= r*r+r {
				cnv.FillRect(cx+x, cy+y, 1, 1, color.RGBA{col.R, col.G, col.B, 110})
			}
		}
	}
}

// fitText shortens the text with "…" to fit the width
func fitText(text string, size float64, width int) string {
	w, _, _ := ui.MeasureText(ui.ThemeFontFamily(), size, text)
	if w <= width {
		return text
	}
	r := []rune(text)
	for len(r) > 1 {
		r = r[:len(r)-1]
		s := string(r) + "…"
		if w, _, _ := ui.MeasureText(ui.ThemeFontFamily(), size, s); w <= width {
			return s
		}
	}
	return string(r)
}

func (c *TabBar) arrowAt(x int) int {
	if !c.overflow() || x < c.Width()-2*tabArrowW {
		return 0
	}
	if x < c.Width()-tabArrowW {
		return -1
	}
	return 1
}

func (c *TabBar) mouseDown(button ui.MouseButton, x, y int, mods ui.KeyModifiers) bool {
	if a := c.arrowAt(x); a != 0 {
		c.scrollBy(a)
		return true
	}
	i, onClose := c.tabAt(x)
	switch button {
	case ui.MouseButtonLeft:
		if i < 0 {
			return true
		}
		if onClose {
			if c.OnClose != nil {
				c.OnClose(i)
			}
			return true
		}
		c.dragFrom = i
		c.downX = x
		if c.OnSelect != nil {
			c.OnSelect(i)
		}
	case ui.MouseButtonMiddle:
		if i >= 0 && c.OnClose != nil {
			c.OnClose(i)
		}
	case ui.MouseButtonRight:
		if c.OnContextMenu != nil {
			wx, wy := c.RectClientAreaOnWindow()
			c.OnContextMenu(i, wx+x, wy+y)
		}
	}
	return true
}

func (c *TabBar) mouseMove(x, y int, mods ui.KeyModifiers) bool {
	i, onClose := c.tabAt(x)
	arrow := c.arrowAt(x)
	if c.dragFrom >= 0 && !c.dragging && abs(x-c.downX) > 8 {
		c.dragging = true
	}
	if i != c.hover || onClose != c.hoverClose || arrow != c.hoverArrow {
		c.hover, c.hoverClose, c.hoverArrow = i, onClose, arrow
		if i >= 0 {
			c.SetTooltip(c.items[i].Tooltip)
		} else {
			c.SetTooltip("")
		}
		c.update()
	}
	return true
}

func (c *TabBar) mouseUp(button ui.MouseButton, x, y int, mods ui.KeyModifiers) bool {
	if c.dragging {
		to, _ := c.tabAt(x)
		if to >= 0 && to != c.dragFrom && c.OnMove != nil {
			c.OnMove(c.dragFrom, to)
		}
	}
	c.dragging = false
	c.dragFrom = -1
	c.update()
	return true
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
