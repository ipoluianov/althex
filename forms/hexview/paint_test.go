package hexview

import (
	"image"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ipoluianov/althex/core/doc"
)

func hugeDoc(t testing.TB, size int64) *doc.Document {
	t.Helper()
	path := filepath.Join(t.TempDir(), "huge.bin")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(size); err != nil {
		f.Close()
		t.Skipf("no sparse file here: %v", err)
	}
	f.Close()
	d, err := doc.Open(path, doc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// drawFrame draws the view as a paint would, without a window
func drawFrame(v *View, img *image.RGBA, scale float64) {
	v.ensureFont(scale)
	pc := &paintCtx{img: img, clip: img.Rect, pal: currentPalette()}
	v.draw(pc, img.Rect.Dx(), img.Rect.Dy())
}

// PERF-3: a frame of the view on a file of 100 GB, anywhere in it, takes
// up to 16 ms; on Full HD and on a 4K screen (scale 2)
func TestPerfPaint(t *testing.T) {
	if raceEnabled {
		t.Skip("timings are not checked with the race detector")
	}
	d := hugeDoc(t, 100<<30)
	r := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 2000; i++ {
		d.Insert(r.Int64N(d.Size()), []byte{byte(i)}, doc.EditOptions{})
	}
	for _, sc := range []struct {
		name  string
		w, h  int
		scale float64
	}{{"FullHD", 1920, 1080, 1}, {"4K", 3840, 2160, 2}} {
		t.Run(sc.name, func(t *testing.T) {
			v := NewView(d, DefaultOptions())
			v.focused = true
			v.SelectAll()
			img := image.NewRGBA(image.Rect(0, 0, sc.w, sc.h))
			drawFrame(v, img, sc.scale)
			const frames = 60
			var worst, total time.Duration
			for i := 0; i < frames; i++ {
				v.topRow = r.Int64N(v.maxTop())
				// Read the rows first: a frame shows what is in the cache
				drawFrame(v, img, sc.scale)
				time.Sleep(5 * time.Millisecond)
				start := time.Now()
				drawFrame(v, img, sc.scale)
				took := time.Since(start)
				total += took
				worst = max(worst, took)
			}
			t.Logf("%s: a frame takes %v on average, %v at most", sc.name, total/frames, worst)
			if avg := total / frames; avg > 16*time.Millisecond {
				t.Errorf("a frame takes %v", avg)
			}
		})
	}
}

func BenchmarkPaintFullHD(b *testing.B) {
	d := hugeDoc(b, 1<<30)
	v := NewView(d, DefaultOptions())
	img := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
	drawFrame(v, img, 1)
	time.Sleep(20 * time.Millisecond)
	for b.Loop() {
		drawFrame(v, img, 1)
	}
}

func TestParseHex(t *testing.T) {
	for _, c := range []struct {
		in   string
		want string
		ok   bool
	}{
		{"4D 5A 90", "4D 5A 90", true},
		{"4d5a", "4D 5A", true},
		{"0x4D, 0x5A", "4D 5A", true},
		{"4D 5", "", false},
		{"hello", "", false},
		{"4 D", "", false},
	} {
		got, ok := ParseHex(c.in)
		if ok != c.ok || (ok && FormatHex(got) != c.want) {
			t.Errorf("ParseHex(%q) = %q, %v", c.in, FormatHex(got), ok)
		}
	}
}
