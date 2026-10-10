package hexview

import (
	"image"
	"image/color"
	"math/rand/v2"
	"testing"
	"time"
)

// minimapOn makes a minimap of a 100 GB document with many ranges: the
// partitions, 2000 template fields and 100000 search hits
func minimapOn(t testing.TB) (*Minimap, *View) {
	d := hugeDoc(t, 100<<30)
	v := NewView(d, DefaultOptions())
	v.ensureFont(1)
	v.layout(1200, 900)
	size := d.Size()
	r := rand.New(rand.NewPCG(3, 4))
	var data MapData
	for i := int64(0); i < 8; i++ {
		data.Areas = append(data.Areas, MapRange{Off: i * size / 8, Len: size / 8, Color: color.RGBA{200, 100, 50, 120}, Name: "part"})
	}
	for i := 0; i < 2000; i++ {
		data.Areas = append(data.Areas, MapRange{Off: r.Int64N(size), Len: r.Int64N(1 << 20), Color: color.RGBA{50, 200, 50, 90}})
	}
	for i := 0; i < 100000; i++ {
		data.Marks = append(data.Marks, MapRange{Off: r.Int64N(size), Len: 4, Color: color.RGBA{255, 200, 0, 255}})
	}
	m := NewMinimap()
	m.View = func() *View { return v }
	m.Data = func() MapData { return data }
	m.scale = 1
	m.refresh()
	t.Cleanup(m.Close)
	return m, v
}

// The minimap shows where the view is, and binning many ranges and drawing
// stay cheap on a 100 GB document
func TestMinimapPaint(t *testing.T) {
	m, v := minimapOn(t)
	img := image.NewRGBA(image.Rect(0, 0, MinimapWidth, 1000))
	start := time.Now()
	m.draw(img, img.Rect, 0, 0, MinimapWidth, 1000)
	first := time.Since(start)
	// The window: the view is at the start, its frame on the first rows
	win := img.RGBAAt(MinimapWidth/2, 0)
	if win == (color.RGBA{}) {
		t.Fatal("nothing drawn")
	}
	// The view moved to the middle: the frame follows
	v.topRow = v.maxTop() / 2
	m.draw(img, img.Rect, 0, 0, MinimapWidth, 1000)
	if !raceEnabled && first > 50*time.Millisecond {
		t.Errorf("the first frame (binning 100000 ranges) takes %v", first)
	}
	if got := offsetIn(500, 1000, v.doc.Size()); got != 50<<30 {
		t.Errorf("offsetAt the middle: %d", got)
	}
	if !raceEnabled {
		res := testing.Benchmark(func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				m.draw(img, img.Rect, 0, 0, MinimapWidth, 1000)
			}
		})
		if per := time.Duration(res.NsPerOp()); per > 2*time.Millisecond {
			t.Errorf("a frame of the minimap takes %v", per)
		}
	}
}

func BenchmarkMinimapPaint(b *testing.B) {
	m, _ := minimapOn(b)
	img := image.NewRGBA(image.Rect(0, 0, 2*MinimapWidth, 2000))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.draw(img, img.Rect, 0, 0, 2*MinimapWidth, 2000)
	}
}

func BenchmarkMinimapBin(b *testing.B) {
	m, v := minimapOn(b)
	size := v.doc.Size()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.binSize = -1
		m.bin(size, 2000)
	}
}
