package hexview

import (
	"image"
	"image/color"
	"math"

	"github.com/ipoluianov/nui/ui"
)

// ListingLine is a line of a disassembly listing
type ListingLine struct {
	Addr    uint64
	Bytes   string // the bytes in hex
	Label   bool   // a label line: Text is the label
	Text    string // the instruction or the data directive
	Comment string
	Kind    int // LineCode, LineData, LineFunc
}

// Kinds of listing lines, for their colors
const (
	LineCode = iota
	LineData
	LineLabel
	LineFunc
)

// ListingSource gives the lines of a listing
type ListingSource interface {
	Len() int
	Line(i int) ListingLine
}

// Listing shows a disassembly listing: only the lines on the screen are
// made and drawn, so a listing of a million lines is cheap (FW-9)
type Listing struct {
	ui.Widget

	src      ListingSource
	font     *Font
	fontSize float64
	scale    float64
	top      int
	cur      int
	rows     int
	pxW, pxH int
	sbW      int
	dragBar  bool
	dragY    int
	dragTop  int

	// OnSelect is called when the current line changes (to sync the hex view)
	OnSelect func(i int)
	// OnActivate is called on a double click or Enter (go to the target)
	OnActivate func(i int)
	// OnKey gets the keys the listing does not handle (rename, comment)
	OnKey func(key ui.Key, mods ui.KeyModifiers) bool
}

func NewListing() *Listing {
	l := &Listing{fontSize: DefaultFontSize, cur: -1}
	l.InitWidget()
	l.SetTypeName("Listing")
	l.SetCanBeFocused(true)
	l.SetXExpandable(true)
	l.SetYExpandable(true)
	l.SetOnPaint(l.paint)
	l.SetOnMouseDown(l.mouseDown)
	l.SetOnMouseMove(l.mouseMove)
	l.SetOnMouseUp(func(b ui.MouseButton, x, y int, m ui.KeyModifiers) bool { l.dragBar = false; return true })
	l.SetOnMouseDblClick(func(b ui.MouseButton, x, y int, m ui.KeyModifiers) bool {
		if l.OnActivate != nil && l.cur >= 0 {
			l.OnActivate(l.cur)
		}
		return true
	})
	l.SetOnMouseWheel(func(dx, dy int) bool { l.scroll(-dy * 3); return true })
	l.SetOnKeyDown(l.keyDown)
	return l
}

// SetSource shows other lines
func (l *Listing) SetSource(src ListingSource) {
	l.src = src
	l.top, l.cur = 0, -1
	l.update()
}

func (l *Listing) update() {
	if f := l.Form(); f != nil {
		f.Update()
	}
}

func (l *Listing) count() int {
	if l.src == nil {
		return 0
	}
	return l.src.Len()
}

// Current returns the current line; -1 - none
func (l *Listing) Current() int { return l.cur }

// SetCurrent makes the line current and shows it, without OnSelect
func (l *Listing) SetCurrent(i int) {
	if i < 0 || i >= l.count() {
		return
	}
	l.cur = i
	if i < l.top || i >= l.top+max(l.rows, 1) {
		l.top = max(0, i-max(l.rows, 1)/3)
	}
	l.clampTop()
	l.update()
}

func (l *Listing) clampTop() {
	l.top = max(0, min(l.top, l.count()-max(l.rows, 1)))
}

func (l *Listing) scroll(n int) {
	l.top += n
	l.clampTop()
	l.update()
}

func (l *Listing) choose(i int) {
	if i < 0 || i >= l.count() {
		return
	}
	l.SetCurrent(i)
	if l.OnSelect != nil {
		l.OnSelect(i)
	}
}

func (l *Listing) keyDown(key ui.Key, mods ui.KeyModifiers) bool {
	page := max(1, l.rows-1)
	switch key {
	case ui.KeyArrowUp:
		l.choose(l.cur - 1)
	case ui.KeyArrowDown:
		l.choose(l.cur + 1)
	case ui.KeyPageUp:
		l.choose(max(0, l.cur-page))
	case ui.KeyPageDown:
		l.choose(min(l.count()-1, l.cur+page))
	case ui.KeyHome:
		l.choose(0)
	case ui.KeyEnd:
		l.choose(l.count() - 1)
	case ui.KeyEnter:
		if l.OnActivate != nil && l.cur >= 0 {
			l.OnActivate(l.cur)
		}
	default:
		if l.OnKey != nil {
			return l.OnKey(key, mods)
		}
		return false
	}
	return true
}

func (l *Listing) toPx(x int) int { return int(math.Round(float64(x) * l.scale)) }

func (l *Listing) lineAt(y int) int {
	if l.font == nil {
		return -1
	}
	return l.top + l.toPx(y)/l.font.LineHeight
}

func (l *Listing) mouseDown(b ui.MouseButton, x, y int, mods ui.KeyModifiers) bool {
	l.Focus()
	if b != ui.MouseButtonLeft {
		return false
	}
	if l.toPx(x) >= l.pxW-l.sbW {
		l.dragBar, l.dragY, l.dragTop = true, l.toPx(y), l.top
		return true
	}
	l.choose(l.lineAt(y))
	return true
}

func (l *Listing) mouseMove(x, y int, mods ui.KeyModifiers) bool {
	if !l.dragBar || l.pxH <= 0 {
		return false
	}
	total := l.count()
	l.top = l.dragTop + (l.toPx(y)-l.dragY)*total/max(l.pxH, 1)
	l.clampTop()
	l.update()
	return true
}

func (l *Listing) paint(cnv *ui.Canvas) {
	scale := cnv.Scale()
	if l.font == nil || l.scale != scale {
		l.scale = scale
		l.font = NewFont(l.fontSize * scale)
	}
	pal := currentPalette()
	img := cnv.RGBA()
	clip := image.Rect(cnv.ClipX(), cnv.ClipY(), cnv.ClipX()+cnv.ClipW(), cnv.ClipY()+cnv.ClipH())
	ox := int(math.Round(float64(cnv.TranslatedX()) * scale))
	oy := int(math.Round(float64(cnv.TranslatedY()) * scale))
	if d := cnv.ClipX() - ox; d >= -2 && d <= 2 {
		ox = cnv.ClipX()
	}
	if d := cnv.ClipY() - oy; d >= -2 && d <= 2 {
		oy = cnv.ClipY()
	}
	w, h := l.toPx(l.Width()), l.toPx(l.Height())
	l.pxW, l.pxH = w, h
	l.sbW = l.toPx(12)
	f := l.font
	cw, lh := f.CharWidth, f.LineHeight
	l.rows = max(1, h/lh)
	l.clampTop()
	fillRect(img, clip, ox, oy, w, h, pal.back)
	base := (lh-f.Ascent-f.Descent)/2 + f.Ascent
	xAddr := ox + cw/2
	xBytes := xAddr + 10*cw
	xText := xBytes + 18*cw
	n := l.count()
	for r := 0; r <= l.rows && l.top+r < n; r++ {
		i := l.top + r
		ln := l.src.Line(i)
		y := oy + r*lh
		if i == l.cur {
			back := pal.selBackInactive
			if l.IsFocused() {
				back = pal.selBack
			}
			fillRect(img, clip, ox, y, w-l.sbW, lh, back)
		}
		draw := func(x int, s string, col color.RGBA) int {
			for _, ch := range s {
				f.DrawChar(img, clip, x, y+base, ch, col)
				x += cw
			}
			return x
		}
		draw(xAddr, hex8(ln.Addr), pal.offset)
		if ln.Label {
			col := pal.high
			if ln.Kind == LineFunc {
				col = pal.edited
			}
			draw(xBytes, ln.Text+":", col)
			continue
		}
		draw(xBytes, ln.Bytes, pal.zero)
		col := pal.text
		if ln.Kind == LineData {
			col = pal.control
		}
		x := draw(xText, ln.Text, col)
		if ln.Comment != "" {
			draw(max(x+2*cw, xText+34*cw), "; "+ln.Comment, pal.header)
		}
	}
	// The scroll bar
	x := ox + w - l.sbW
	fillRect(img, clip, x, oy, l.sbW, h, pal.scrollTrack)
	if n > l.rows {
		th := max(l.toPx(20), h*l.rows/n)
		ty := (h - th) * l.top / max(1, n-l.rows)
		fillRect(img, clip, x+2, oy+ty, l.sbW-4, th, pal.scrollThumb)
	}
}

func hex8(v uint64) string {
	const digits = "0123456789ABCDEF"
	var b [16]byte
	n := 8
	if v > 0xFFFFFFFF {
		n = 16
	}
	for i := n - 1; i >= 0; i-- {
		b[i] = digits[v&15]
		v >>= 4
	}
	return string(b[:n])
}
