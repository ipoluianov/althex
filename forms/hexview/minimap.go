package hexview

import (
	"context"
	"hash/fnv"
	"image"
	"image/color"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ipoluianov/althex/core/doc"
	checksum "github.com/ipoluianov/althex/core/hash"
	"github.com/ipoluianov/nui/ui"
)

// MapRange is a range of the document drawn on the minimap
type MapRange struct {
	Off, Len int64
	Color    color.RGBA
	// Name is shown when the mouse rests on the range; a named range is
	// activated by a double click (a partition: go to it)
	Name string
}

// MapData is what the minimap shows over the shading of the bytes
type MapData struct {
	// Areas are drawn wide: partitions, the fields of a template
	Areas []MapRange
	// Marks are drawn in a narrow lane on the right: bookmarks, search hits,
	// edits, unreadable sectors
	Marks []MapRange
}

// MinimapWidth is the width of the minimap in logical pixels
const MinimapWidth = 18

// minimapRefresh is how often the minimap asks for its ranges again
const minimapRefresh = 400 * time.Millisecond

// shadeBlocks is how many places of the document the shading samples (4 KB
// each): the cost does not depend on the size of the document
const shadeBlocks = 256

// Minimap is a narrow strip next to the hex view showing the whole
// document scaled (BOOT-2): the part on the screen, the partitions, the
// marks, and the entropy of the bytes sampled in the background. It is
// drawn from lists of ranges, binned to the pixel rows, so a document of
// 100 GB costs as little as a small one; no byte is read on a paint.
type Minimap struct {
	ui.Widget

	// View is the hex view the minimap follows (the focused one of a split)
	View func() *View
	// Data gives the ranges to draw; called on the UI goroutine every
	// minimapRefresh while the minimap is shown
	Data func() MapData
	// OnActivate is called on a double click on a named area
	OnActivate func(r MapRange)
	// OnContextMenu is called on a right click on a named area, at x, y of
	// the window
	OnContextMenu func(r MapRange, x, y int)
	// OnIdle is called with the timer of the minimap, shown or not
	OnIdle func()

	data    MapData
	fp      uint64
	fetched time.Time

	// The ranges binned to the pixel rows, for binSize and len(areaRow)
	binSize int64
	areaRow []color.RGBA
	markRow []color.RGBA
	edgeRow []bool // a named area starts here
	// rowCol is the color of each row of the wide lane, made of the shading
	// and the areas when either changes
	rowCol   []color.RGBA
	rowFor   []float64
	rowPal   *palette
	rowStale bool

	scale float64
	pxW   int
	pxH   int

	form atomic.Pointer[ui.Form]

	// The shading: the entropy of shadeBlocks samples, 0..8 (-1: not yet)
	shadeMu   sync.Mutex
	shade     []float64
	shadeSnap *doc.Snapshot
	shadeAt   time.Time
	shadeStop context.CancelFunc

	drag    bool
	tooltip string
}

func NewMinimap() *Minimap {
	m := &Minimap{}
	m.InitWidget()
	m.SetTypeName("Minimap")
	m.SetMinWidth(MinimapWidth)
	m.SetMaxWidth(MinimapWidth)
	m.SetXExpandable(false)
	m.SetYExpandable(true)
	m.SetOnPaint(m.paint)
	m.SetOnMouseDown(m.mouseDown)
	m.SetOnMouseMove(m.mouseMove)
	m.SetOnMouseUp(func(b ui.MouseButton, x, y int, mods ui.KeyModifiers) bool { m.drag = false; return true })
	m.SetOnMouseDblClick(m.dblClick)
	m.SetOnMouseWheel(func(dx, dy int) bool {
		if v := m.view(); v != nil {
			return v.mouseWheel(dx, dy)
		}
		return false
	})
	m.AddTimer(int(minimapRefresh/time.Millisecond), m.tick)
	return m
}

// Close stops the sampling
func (m *Minimap) Close() {
	m.shadeMu.Lock()
	if m.shadeStop != nil {
		m.shadeStop()
	}
	m.shadeMu.Unlock()
}

func (m *Minimap) view() *View {
	if m.View == nil {
		return nil
	}
	return m.View()
}

func (m *Minimap) update() {
	if f := m.Form(); f != nil {
		f.Update()
	}
}

// tick asks for the ranges again and repaints when they changed
func (m *Minimap) tick() {
	if m.OnIdle != nil {
		m.OnIdle()
	}
	if !m.IsVisible() || m.Width() <= 0 {
		return
	}
	if m.refresh() {
		m.update()
	}
}

// Refresh asks for the ranges now (they changed)
func (m *Minimap) Refresh() {
	if m.refresh() {
		m.update()
	}
}

// refresh gets the ranges; true when they differ from the ones drawn
func (m *Minimap) refresh() bool {
	if m.Data == nil {
		return false
	}
	d := m.Data()
	m.fetched = time.Now()
	fp := fingerprint(d)
	if fp == m.fp {
		return false
	}
	m.data, m.fp = d, fp
	m.binSize = -1 // binned again on the next paint
	return true
}

func fingerprint(d MapData) uint64 {
	h := fnv.New64a()
	var b [8 * 3]byte
	put := func(rs []MapRange) {
		for _, r := range rs {
			for i, v := range [...]uint64{uint64(r.Off), uint64(r.Len), uint64(r.Color.R)<<24 | uint64(r.Color.G)<<16 | uint64(r.Color.B)<<8 | uint64(r.Color.A)} {
				for k := 0; k < 8; k++ {
					b[i*8+k] = byte(v >> (8 * k))
				}
			}
			h.Write(b[:])
			h.Write([]byte(r.Name))
		}
		h.Write([]byte{0xFF})
	}
	put(d.Areas)
	put(d.Marks)
	return h.Sum64()
}

// rowOf is the pixel row of an offset on a strip of h rows
func rowOf(off, size int64, h int) int {
	if size <= 0 {
		return 0
	}
	return int(float64(off) / float64(size) * float64(h))
}

// bin draws the ranges into one color per pixel row: the larger areas first,
// so the smaller ones inside them stay seen
func (m *Minimap) bin(size int64, h int) {
	if m.binSize == size && len(m.areaRow) == h {
		return
	}
	m.binSize = size
	m.rowStale = true
	if cap(m.areaRow) < h {
		m.areaRow = make([]color.RGBA, h)
		m.markRow = make([]color.RGBA, h)
		m.edgeRow = make([]bool, h)
	}
	m.areaRow, m.markRow, m.edgeRow = m.areaRow[:h], m.markRow[:h], m.edgeRow[:h]
	clear(m.areaRow)
	clear(m.markRow)
	clear(m.edgeRow)
	if size <= 0 || h <= 0 {
		return
	}
	areas := append([]MapRange(nil), m.data.Areas...)
	sort.SliceStable(areas, func(i, j int) bool { return areas[i].Len > areas[j].Len })
	span := func(r MapRange) (int, int) {
		y0 := min(max(rowOf(r.Off, size, h), 0), h-1)
		y1 := min(max(int(math.Ceil(float64(r.Off+max(r.Len, 1))/float64(size)*float64(h))), y0+1), h)
		return y0, y1
	}
	for _, r := range areas {
		if r.Off >= size || r.Off+max(r.Len, 1) <= 0 {
			continue
		}
		y0, y1 := span(r)
		for y := y0; y < y1; y++ {
			m.areaRow[y] = r.Color
		}
		if r.Name != "" {
			m.edgeRow[y0] = true
		}
	}
	for _, r := range m.data.Marks {
		if r.Off >= size || r.Off+max(r.Len, 1) <= 0 {
			continue
		}
		y0, y1 := span(r)
		for y := y0; y < y1; y++ {
			m.markRow[y] = r.Color
		}
	}
}

// sampleShade samples the entropy of the document in the background when
// it has not been, or the document changed (at most every few seconds)
func (m *Minimap) sampleShade(snap *doc.Snapshot) {
	m.shadeMu.Lock()
	defer m.shadeMu.Unlock()
	if m.shadeSnap == snap {
		return
	}
	sameSize := m.shadeSnap != nil && m.shadeSnap.Size() == snap.Size()
	if sameSize && time.Since(m.shadeAt) < 5*time.Second {
		return
	}
	if m.shadeStop != nil {
		m.shadeStop()
	}
	m.shadeSnap, m.shadeAt = snap, time.Now()
	if !sameSize {
		m.shade = nil
	}
	size := snap.Size()
	if size == 0 {
		return
	}
	blocks := int(min(int64(shadeBlocks), size))
	ctx, cancel := context.WithCancel(context.Background())
	m.shadeStop = cancel
	go func() {
		es, err := checksum.EntropyBlocks(ctx, zerosOnError{snap}, 0, size, blocks, 4096)
		if err != nil {
			return
		}
		m.shadeMu.Lock()
		if m.shadeSnap == snap {
			m.shade = es
		}
		m.shadeMu.Unlock()
		if f := m.form.Load(); f != nil {
			f.Invoke(func() { f.Update() })
		}
	}()
}

// zerosOnError reads a bad sector as the zeros it is shown as
type zerosOnError struct{ s *doc.Snapshot }

func (z zerosOnError) ReadAt(p []byte, off int64) (int, error) {
	n, err := z.s.ReadAt(p, off)
	if err != nil && n == len(p) {
		return n, nil
	}
	return n, err
}

func (m *Minimap) toPx(x int) int { return int(math.Round(float64(x) * m.scale)) }

func (m *Minimap) paint(cnv *ui.Canvas) {
	if f := m.Form(); f != nil {
		m.form.Store(f)
	}
	m.scale = cnv.Scale()
	img := cnv.RGBA()
	clip := image.Rect(cnv.ClipX(), cnv.ClipY(), cnv.ClipX()+cnv.ClipW(), cnv.ClipY()+cnv.ClipH())
	ox := int(math.Round(float64(cnv.TranslatedX()) * m.scale))
	oy := int(math.Round(float64(cnv.TranslatedY()) * m.scale))
	if d := cnv.ClipX() - ox; d >= -2 && d <= 2 {
		ox = cnv.ClipX()
	}
	if d := cnv.ClipY() - oy; d >= -2 && d <= 2 {
		oy = cnv.ClipY()
	}
	if m.fetched.IsZero() {
		m.refresh()
	}
	m.draw(img, clip, ox, oy, m.toPx(m.Width()), m.toPx(m.Height()))
}

// draw draws the minimap of w x h pixels at ox, oy
func (m *Minimap) draw(img *image.RGBA, clip image.Rectangle, ox, oy, w, h int) {
	m.pxW, m.pxH = w, h
	pal := currentPalette()
	fillRect(img, clip, ox, oy, w, h, pal.scrollTrack)
	v := m.view()
	if v == nil || h <= 0 {
		return
	}
	snap := v.doc.Snapshot()
	size := snap.Size()
	m.bin(size, h)
	m.sampleShade(snap)
	m.shadeMu.Lock()
	shade := m.shade
	m.shadeMu.Unlock()

	sep := max(1, w/12)
	markW := max(3, w/4)
	areaW := w - markW - sep
	m.compose(pal, shade, h)
	r := image.Rect(ox, oy, ox+w, oy+h).Intersect(clip).Intersect(img.Rect)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := img.Pix[(y-img.Rect.Min.Y)*img.Stride:]
		set := func(x0, x1 int, c color.RGBA) {
			x0, x1 = max(x0, r.Min.X), min(x1, r.Max.X)
			for x := x0; x < x1; x++ {
				i := (x - img.Rect.Min.X) * 4
				row[i], row[i+1], row[i+2], row[i+3] = c.R, c.G, c.B, 255
			}
		}
		set(ox, ox+areaW, m.rowCol[y-oy])
		if mk := m.markRow[y-oy]; mk.A != 0 {
			set(ox+areaW+sep, ox+w, mk)
		}
	}
	if size <= 0 {
		return
	}
	// The part of the document on the screen
	top := v.TopOffset()
	shown := int64(max(v.rowsShown, 1)) * int64(v.bpr)
	y0 := rowOf(top, size, h)
	y1 := max(y0+m.toPx(3), rowOf(top+shown, size, h))
	y1 = min(y1, h)
	y0 = min(y0, y1-1)
	win := pal.caret
	win.A = 50
	fillRect(img, clip, ox, oy+y0, w, y1-y0, win)
	win.A = 200
	fillRect(img, clip, ox, oy+y0, w, 1, win)
	fillRect(img, clip, ox, oy+y1-1, w, 1, win)
	// The caret
	cy := min(rowOf(v.Caret(), size, h), h-1)
	fillRect(img, clip, ox, oy+cy, areaW, max(1, m.toPx(1)), pal.edited)
}

// compose makes the colors of the rows of the wide lane
func (m *Minimap) compose(pal *palette, shade []float64, h int) {
	if !m.rowStale && m.rowPal == pal && len(m.rowCol) == h && sameSlice(m.rowFor, shade) {
		return
	}
	m.rowStale, m.rowPal, m.rowFor = false, pal, shade
	if cap(m.rowCol) < h {
		m.rowCol = make([]color.RGBA, h)
	}
	m.rowCol = m.rowCol[:h]
	for y := 0; y < h; y++ {
		col := pal.scrollThumb
		if a := m.areaRow[y]; a.A != 0 {
			col = mix(col, color.RGBA{a.R, a.G, a.B, 255}, max(float64(a.A)/255, 0.6))
		}
		// The bytes: the color is full where the entropy is high, dim where
		// the bytes repeat (zeros, erased flash)
		if len(shade) > 0 {
			e := shade[min(y*len(shade)/h, len(shade)-1)]
			col = mix(mix(pal.scrollTrack, col, 0.35), col, e/8)
		}
		if m.edgeRow[y] && y > 0 {
			col = pal.back
		}
		m.rowCol[y] = col
	}
}

func sameSlice(a, b []float64) bool {
	return len(a) == len(b) && (len(a) == 0 || &a[0] == &b[0])
}

// mix is a + (b - a) * t
func mix(a, b color.RGBA, t float64) color.RGBA {
	t = min(max(t, 0), 1)
	f := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t + 0.5) }
	return color.RGBA{f(a.R, b.R), f(a.G, b.G), f(a.B, b.B), 255}
}

// offsetAt is the offset of the document at y (logical pixels)
func (m *Minimap) offsetAt(y int, size int64) int64 {
	return offsetIn(y, m.Height(), size)
}

// offsetIn is the offset at the row y of a strip of h rows
func offsetIn(y, h int, size int64) int64 {
	if h <= 0 || size <= 0 {
		return 0
	}
	y = min(max(y, 0), h-1)
	return int64(float64(y) / float64(h) * float64(size))
}

// scrollTo shows the place at y in the middle of the view
func (m *Minimap) scrollTo(y int) {
	v := m.view()
	if v == nil {
		return
	}
	off := m.offsetAt(y, v.doc.Size())
	rows := int64(max(v.rowsShown, 1))
	v.setTop(off/int64(v.bpr) - rows/2)
}

func (m *Minimap) mouseDown(b ui.MouseButton, x, y int, mods ui.KeyModifiers) bool {
	if b == ui.MouseButtonRight {
		if r, ok := m.areaAt(y); ok && m.OnContextMenu != nil {
			wx, wy := m.RectClientAreaOnWindow()
			m.OnContextMenu(r, wx+x, wy+y)
			return true
		}
		return false
	}
	if b != ui.MouseButtonLeft {
		return false
	}
	m.drag = true
	m.scrollTo(y)
	return true
}

func (m *Minimap) mouseMove(x, y int, mods ui.KeyModifiers) bool {
	if m.drag {
		m.scrollTo(y)
		return true
	}
	tip := ""
	if r, ok := m.areaAt(y); ok {
		tip = r.Name
	}
	if tip != m.tooltip {
		m.tooltip = tip
		m.SetTooltip(tip)
	}
	return true
}

// areaAt returns the smallest named area at y
func (m *Minimap) areaAt(y int) (MapRange, bool) {
	v := m.view()
	if v == nil {
		return MapRange{}, false
	}
	size := v.doc.Size()
	off := m.offsetAt(y, size)
	// A pixel row holds many bytes: an area within the row counts
	slack := int64(float64(size) / float64(max(m.Height(), 1)))
	var best MapRange
	found := false
	for _, r := range m.data.Areas {
		if r.Name == "" || off+slack < r.Off || off >= r.Off+r.Len {
			continue
		}
		if !found || r.Len < best.Len {
			best, found = r, true
		}
	}
	return best, found
}

func (m *Minimap) dblClick(b ui.MouseButton, x, y int, mods ui.KeyModifiers) bool {
	m.drag = false
	if r, ok := m.areaAt(y); ok && m.OnActivate != nil {
		m.OnActivate(r)
		return true
	}
	return false
}
