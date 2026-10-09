package hexview

import (
	_ "embed"
	"image"
	"image/color"
	"math"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

//go:embed JetBrainsMono-Regular.ttf
var jetBrainsMono []byte

var (
	defaultFontOnce sync.Once
	defaultFont     *sfnt.Font
)

func parsedDefaultFont() *sfnt.Font {
	defaultFontOnce.Do(func() {
		defaultFont, _ = opentype.Parse(jetBrainsMono)
	})
	return defaultFont
}

// Font draws the cells of the view: a monospaced font at a size in pixels
// of the screen, with the glyphs rendered once and then copied
type Font struct {
	size float64
	face font.Face

	CharWidth  int
	Ascent     int
	Descent    int
	LineHeight int

	glyphs [256]*glyph // the characters the view shows are one byte each
}

// glyph is the alpha mask of a character, placed relative to the pen at the baseline
type glyph struct {
	mask   []byte
	w, h   int
	dx, dy int
}

// NewFont makes the font at the size in pixels
func NewFont(size float64) *Font {
	size = math.Max(4, math.Min(size, 200))
	face, _ := opentype.NewFace(parsedDefaultFont(), &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	f := &Font{size: size, face: face}
	m := face.Metrics()
	f.Ascent = m.Ascent.Ceil()
	f.Descent = m.Descent.Ceil()
	f.LineHeight = f.Ascent + f.Descent + int(math.Round(size*0.25))
	adv, ok := face.GlyphAdvance('0')
	if !ok {
		adv = fixed.I(int(size * 0.6))
	}
	f.CharWidth = max(1, adv.Round())
	return f
}

// Size returns the size of the font in pixels
func (f *Font) Size() float64 { return f.size }

func (f *Font) glyph(c byte) *glyph {
	if g := f.glyphs[c]; g != nil {
		return g
	}
	g := &glyph{}
	dr, mask, mp, _, ok := f.face.Glyph(fixed.P(0, 0), rune(c))
	if ok && !dr.Empty() {
		g = &glyph{w: dr.Dx(), h: dr.Dy(), dx: dr.Min.X, dy: dr.Min.Y}
		g.mask = make([]byte, g.w*g.h)
		for y := 0; y < g.h; y++ {
			for x := 0; x < g.w; x++ {
				_, _, _, a := mask.At(mp.X+x, mp.Y+y).RGBA()
				g.mask[y*g.w+x] = uint8(a >> 8)
			}
		}
	}
	f.glyphs[c] = g
	return g
}

// DrawChar draws the character (Latin-1) with the pen at x on the baseline
// y, in the clip rectangle of dst
func (f *Font) DrawChar(dst *image.RGBA, clip image.Rectangle, x, y int, c byte, col color.RGBA) {
	g := f.glyph(c)
	if g.w == 0 {
		return
	}
	blitMask(dst, clip, x+g.dx, y+g.dy, g.w, g.h, g.mask, col)
}

// DrawString draws ASCII text from x on the baseline y; returns the x after it
func (f *Font) DrawString(dst *image.RGBA, clip image.Rectangle, x, y int, s string, col color.RGBA) int {
	for i := 0; i < len(s); i++ {
		f.DrawChar(dst, clip, x, y, s[i], col)
		x += f.CharWidth
	}
	return x
}

// blitMask blends col into dst through the alpha mask at (x, y)
func blitMask(dst *image.RGBA, clip image.Rectangle, x, y, w, h int, mask []byte, col color.RGBA) {
	clip = clip.Intersect(dst.Rect)
	x0, y0 := max(x, clip.Min.X), max(y, clip.Min.Y)
	x1, y1 := min(x+w, clip.Max.X), min(y+h, clip.Max.Y)
	if x0 >= x1 || y0 >= y1 {
		return
	}
	cr, cg, cb := uint32(col.R), uint32(col.G), uint32(col.B)
	ca := uint32(col.A)
	for yy := y0; yy < y1; yy++ {
		row := dst.Pix[(yy-dst.Rect.Min.Y)*dst.Stride+(x0-dst.Rect.Min.X)*4:]
		m := mask[(yy-y)*w+(x0-x):]
		for xx := 0; xx < x1-x0; xx++ {
			a := uint32(m[xx])
			if a == 0 {
				continue
			}
			if ca != 255 {
				a = a * ca / 255
			}
			p := row[xx*4 : xx*4+4 : xx*4+4]
			if a == 255 {
				p[0], p[1], p[2], p[3] = uint8(cr), uint8(cg), uint8(cb), 255
				continue
			}
			ia := 255 - a
			p[0] = uint8((cr*a + uint32(p[0])*ia) / 255)
			p[1] = uint8((cg*a + uint32(p[1])*ia) / 255)
			p[2] = uint8((cb*a + uint32(p[2])*ia) / 255)
			p[3] = 255
		}
	}
}

// fillRect fills the rectangle of dst within clip with col, blending when it is translucent
func fillRect(dst *image.RGBA, clip image.Rectangle, x, y, w, h int, col color.RGBA) {
	r := image.Rect(x, y, x+w, y+h).Intersect(clip).Intersect(dst.Rect)
	if r.Empty() {
		return
	}
	if col.A == 255 {
		row := dst.Pix[(r.Min.Y-dst.Rect.Min.Y)*dst.Stride+(r.Min.X-dst.Rect.Min.X)*4:]
		n := r.Dx() * 4
		line := row[:n]
		for i := 0; i < n; i += 4 {
			line[i], line[i+1], line[i+2], line[i+3] = col.R, col.G, col.B, 255
		}
		for yy := r.Min.Y + 1; yy < r.Max.Y; yy++ {
			copy(dst.Pix[(yy-dst.Rect.Min.Y)*dst.Stride+(r.Min.X-dst.Rect.Min.X)*4:], line)
		}
		return
	}
	a := uint32(col.A)
	ia := 255 - a
	cr, cg, cb := uint32(col.R)*a, uint32(col.G)*a, uint32(col.B)*a
	for yy := r.Min.Y; yy < r.Max.Y; yy++ {
		row := dst.Pix[(yy-dst.Rect.Min.Y)*dst.Stride+(r.Min.X-dst.Rect.Min.X)*4:]
		for xx := 0; xx < r.Dx(); xx++ {
			p := row[xx*4 : xx*4+4 : xx*4+4]
			p[0] = uint8((cr + uint32(p[0])*ia) / 255)
			p[1] = uint8((cg + uint32(p[1])*ia) / 255)
			p[2] = uint8((cb + uint32(p[2])*ia) / 255)
		}
	}
}

// strokeRect draws the outline of the rectangle, a pixel wide
func strokeRect(dst *image.RGBA, clip image.Rectangle, x, y, w, h int, col color.RGBA) {
	fillRect(dst, clip, x, y, w, 1, col)
	fillRect(dst, clip, x, y+h-1, w, 1, col)
	fillRect(dst, clip, x, y, 1, h, col)
	fillRect(dst, clip, x+w-1, y, 1, h, col)
}
