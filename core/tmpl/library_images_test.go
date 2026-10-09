package tmpl

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// libraryTemplate returns a built-in template by its file name
func libraryTemplate(t *testing.T, file string) *Template {
	t.Helper()
	for _, tm := range Library() {
		if tm.File == file {
			return tm
		}
	}
	t.Fatalf("no template %s", file)
	return nil
}

// runLimited runs a template over data with a limit of steps; it fails the
// test on a panic or when the run takes too long
func runLimited(t *testing.T, tm *Template, data []byte, maxSteps int64) *Result {
	t.Helper()
	p, err := tm.Program()
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	res := p.Run(context.Background(), Env{Data: &mem{b: data}, MaxSteps: maxSteps})
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("%s: %d bytes took %v", tm.File, len(data), d)
	}
	return res
}

// checkTruncated runs the template over prefixes of data: no panics, no
// endless runs, no errors
func checkTruncated(t *testing.T, file string, data []byte) {
	t.Helper()
	tm := libraryTemplate(t, file)
	cuts := []int{0, 1, 2, 3, 4, 5, 7, 8, 9, 10, 11, 12, 13, 15, 16, 20, 24, 25, 30, 33, 37, 40, 41, 50, 64, 100}
	for i := 1; i < 16; i++ {
		cuts = append(cuts, len(data)*i/16)
	}
	cuts = append(cuts, len(data)-1, len(data)-2, len(data)-5)
	for _, n := range cuts {
		if n < 0 || n > len(data) {
			continue
		}
		res := runLimited(t, tm, data[:n], 5_000_000)
		if res.Err != nil {
			t.Errorf("%s cut at %d of %d: %v", file, n, len(data), res.Err)
		}
	}
	// Damaged: bytes changed in the middle
	for _, off := range []int{8, 12, 20, 30, 40, len(data) / 3, len(data) / 2} {
		if off >= len(data) {
			continue
		}
		for _, v := range []byte{0x00, 0xFF, 0x7F} {
			b := append([]byte(nil), data...)
			for k := off; k < off+4 && k < len(b); k++ {
				b[k] = v
			}
			res := runLimited(t, tm, b, 5_000_000)
			if res.Err != nil {
				t.Errorf("%s damaged at %d with %02X: %v", file, off, v, res.Err)
			}
		}
	}
}

// testImage is a small image with a gradient
func testImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 255 / w), uint8(y * 255 / h), uint8((x + y) * 7), 255})
		}
	}
	return img
}

// pngChunk makes a chunk with its CRC
func pngChunk(typ string, data []byte) []byte {
	b := make([]byte, 8, 12+len(data))
	binary.BigEndian.PutUint32(b, uint32(len(data)))
	copy(b[4:], typ)
	b = append(b, data...)
	return binary.BigEndian.AppendUint32(b, crc32.ChecksumIEEE(b[4:]))
}

// makePNG makes a paletted PNG with tEXt, tIME and pHYs chunks added after IHDR
func makePNG(t *testing.T) []byte {
	pal := append(color.Palette{color.NRGBA{1, 2, 3, 0}}, palette.Plan9[:15]...)
	img := image.NewPaletted(image.Rect(0, 0, 37, 21), pal)
	for i := range img.Pix {
		img.Pix[i] = uint8(i % 16)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	ihdrEnd := 8 + 12 + 13
	var out []byte
	out = append(out, b[:ihdrEnd]...)
	out = append(out, pngChunk("tEXt", []byte("Comment\x00made by the test"))...)
	out = append(out, pngChunk("tIME", []byte{0x07, 0xEA, 10, 9, 12, 30, 45})...)
	out = append(out, pngChunk("pHYs", []byte{0, 0, 0x0B, 0x13, 0, 0, 0x0B, 0x13, 1})...)
	out = append(out, pngChunk("iTXt", []byte("Title\x00\x00\x00en\x00Titel\x00Hello"))...)
	out = append(out, pngChunk("gAMA", []byte{0, 0, 0xB1, 0x8F})...)
	out = append(out, b[ihdrEnd:]...)
	return out
}

func TestPNGTemplate(t *testing.T) {
	data := makePNG(t)
	res := libraryRun(t, data, "x.png", "png.bt")
	if v := value(t, res, "chunk[0].ihdr.width"); v != "37" {
		t.Fatalf("width %s", v)
	}
	if v := value(t, res, "chunk[0].ihdr.height"); v != "21" {
		t.Fatalf("height %s", v)
	}
	if v := value(t, res, "chunk[0].ihdr.colorType"); !strings.HasPrefix(v, "INDEXED_COLOR") {
		t.Fatalf("color type %s", v)
	}
	if v := value(t, res, "chunk[0]"); v != "IHDR 13 bytes, 37x21" {
		t.Fatalf("IHDR shown as %q", v)
	}
	if v := value(t, res, "chunk[1].keyword"); v != `"Comment"` {
		t.Fatalf("keyword %s", v)
	}
	if v := value(t, res, "chunk[1].text"); v != `"made by the test"` {
		t.Fatalf("text %s", v)
	}
	if v := value(t, res, "chunk[2].lastModification"); v != "2026-10-09 12:30:45" {
		t.Fatalf("time %s", v)
	}
	if v := value(t, res, "chunk[3].phys.unitSpecifier"); !strings.HasPrefix(v, "METRE") {
		t.Fatalf("unit %s", v)
	}
	if v := value(t, res, "chunk[4].translatedKeyword"); v != `"Titel"` {
		t.Fatalf("iTXt %s", v)
	}
	if v := value(t, res, "chunk[5].gamma"); v != "45455" {
		t.Fatalf("gamma %s", v)
	}
	if v := value(t, res, "chunk[6].palette"); v != "[16]" {
		t.Fatalf("palette %s", v)
	}
	if v := value(t, res, "chunk[7]"); v != "tRNS 1 bytes" {
		t.Fatalf("tRNS shown as %q", v)
	}
	if v := value(t, res, "chunk[7].alpha"); v != "[1]" {
		t.Fatalf("tRNS alpha %s", v)
	}
	if v := value(t, res, "chunk[6].palette[0]"); v != "#010203" {
		t.Fatalf("palette[0] %s", v)
	}
	last := res.Root.Find("chunk").ChildCount() - 1
	if v := res.Root.Find("chunk").Child(last).Value(); v != "IEND 0 bytes" {
		t.Fatalf("last chunk %s", v)
	}
	crc := res.Root.Find("chunk[0].crc")
	if crc == nil || crc.Value() != "0x"+strings.ToUpper(hexOf(data[29:33])) {
		t.Fatalf("crc %v", crc)
	}
	if res.Root.Find("trailingData") != nil {
		t.Fatal("trailing data")
	}
	checkTruncated(t, "png.bt", data)

	// RGBA with transparency of a color
	var buf bytes.Buffer
	if err := png.Encode(&buf, testImage(50, 40)); err != nil {
		t.Fatal(err)
	}
	res = libraryRun(t, buf.Bytes(), "y.png", "png.bt")
	if v := value(t, res, "chunk[0].ihdr.width"); v != "50" {
		t.Fatalf("width %s", v)
	}
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	var sb strings.Builder
	for _, c := range b {
		sb.WriteByte(digits[c>>4])
		sb.WriteByte(digits[c&15])
	}
	return sb.String()
}

func TestJPEGTemplate(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, testImage(64, 48), &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	// A JFIF APP0, a comment, an Exif APP1 and a DRI after SOI
	jfif := []byte{0xFF, 0xE0, 0, 16, 'J', 'F', 'I', 'F', 0, 1, 2, 1, 0, 72, 0, 72, 0, 0}
	com := append([]byte{0xFF, 0xFE, 0, 2 + 5}, "hello"...)
	exif := append(makeExif(false), makeExif(true)...)
	dri := []byte{0xFF, 0xDD, 0, 4, 0, 0}
	var out []byte
	out = append(out, data[:2]...)
	out = append(out, jfif...)
	out = append(out, exif...)
	out = append(out, com...)
	out = append(out, dri...)
	out = append(out, data[2:]...)
	data = out

	res := libraryRun(t, data, "x.jpg", "jpeg.bt")
	segs := res.Root.Find("segment")
	if segs == nil {
		t.Fatal("no segments")
	}
	var shown []string
	for _, s := range segs.Children() {
		shown = append(shown, s.Value())
	}
	all := strings.Join(shown, "; ")
	for _, want := range []string{"SOI", "APP0 JFIF 1.02", "APP1 Exif", "COM", "DQT", "SOF0 64x48", "COM hello", "DHT", "SOS", "EOI"} {
		if !strings.Contains(all, want) {
			t.Errorf("no %q in %s", want, all)
		}
	}
	if v := value(t, res, "segment[1].identifier"); v != `"JFIF"` {
		t.Errorf("JFIF identifier %s", v)
	}
	if v := value(t, res, "segment[2].exif.ifd0.entry[0]"); v != `Make = AltHex` {
		t.Errorf("Exif Make %s", v)
	}
	if v := value(t, res, "segment[2].exif.ifd0.entry[1]"); v != "Orientation = 6" {
		t.Errorf("Exif Orientation %s", v)
	}
	if v := value(t, res, "segment[3].exif.ifd0.entry[0]"); v != `Make = AltHex` {
		t.Errorf("little-endian Exif Make %s", v)
	}
	if v := value(t, res, "segment[3].exif.ifd0.entry[1]"); v != "Orientation = 6" {
		t.Errorf("little-endian Exif Orientation %s", v)
	}
	if v := value(t, res, "segment[3].length"); v != fmt.Sprint(len(exif)/2-2) {
		t.Errorf("the length after little-endian Exif %s", v)
	}
	if v := value(t, res, "segment[4].comment"); v != `"hello"` {
		t.Errorf("comment %s", v)
	}
	if v := value(t, res, "segment[5].restartInterval"); v != "0" {
		t.Errorf("DRI %s", v)
	}
	// The SOF0 and SOS
	var sof, sos, eoi *Node
	for _, s := range segs.Children() {
		switch {
		case strings.HasPrefix(s.Value(), "SOF0"):
			sof = s
		case strings.HasPrefix(s.Value(), "SOS"):
			sos = s
		case s.Value() == "EOI":
			eoi = s
		}
	}
	if sof == nil || sos == nil || eoi == nil {
		t.Fatal("no SOF0, SOS or EOI")
	}
	if v := sof.Find("height").Value(); v != "48" {
		t.Errorf("height %s", v)
	}
	if v := sof.Find("numComponents").Value(); v != "3" {
		t.Errorf("components %s", v)
	}
	scan := sos.Find("scanData")
	if scan == nil || scan.Off+scan.Size != eoi.Off {
		t.Errorf("scan data %v, EOI at %d", scan, eoi.Off)
	}
	if eoi.Off+2 != int64(len(data)) {
		t.Errorf("EOI at %d of %d", eoi.Off, len(data))
	}
	checkTruncated(t, "jpeg.bt", data)

	// Restart markers in the scan: they stay in the scan data
	buf.Reset()
	if err := jpeg.Encode(&buf, testImage(200, 150), nil); err != nil {
		t.Fatal(err)
	}
	d2 := buf.Bytes()
	res = libraryRun(t, d2, "y.jpg", "jpeg.bt")
	n := res.Root.Find("segment").ChildCount()
	if v := res.Root.Find("segment").Child(n - 1).Value(); v != "EOI" {
		t.Errorf("last segment %s", v)
	}
}

// makeExif makes an APP1 segment with Exif: a TIFF header (big- or
// little-endian), IFD0 of Make and Orientation
func makeExif(little bool) []byte {
	var o interface {
		binary.ByteOrder
		binary.AppendByteOrder
	} = binary.BigEndian
	tiff := []byte{'M', 'M'}
	if little {
		o = binary.LittleEndian
		tiff = []byte{'I', 'I'}
	}
	tiff = o.AppendUint16(tiff, 42)
	tiff = o.AppendUint32(tiff, 8)
	tiff = o.AppendUint16(tiff, 2)
	// Make, ASCII, 7, the offset of the text
	tiff = o.AppendUint16(tiff, 0x010F)
	tiff = o.AppendUint16(tiff, 2)
	tiff = o.AppendUint32(tiff, 7)
	tiff = o.AppendUint32(tiff, uint32(8+2+2*12+4))
	// Orientation, SHORT, 1, 6
	tiff = o.AppendUint16(tiff, 0x0112)
	tiff = o.AppendUint16(tiff, 3)
	tiff = o.AppendUint32(tiff, 1)
	tiff = o.AppendUint16(tiff, 6)
	tiff = append(tiff, 0, 0, 0, 0, 0, 0)
	tiff = append(tiff, "AltHex\x00"...)
	seg := []byte{0xFF, 0xE1, 0, 0}
	seg = append(seg, "Exif\x00\x00"...)
	seg = append(seg, tiff...)
	binary.BigEndian.PutUint16(seg[2:], uint16(len(seg)-2))
	return seg
}

func TestGIFTemplate(t *testing.T) {
	pal := color.Palette(palette.Plan9[:8])
	var frames []*image.Paletted
	for i := 0; i < 2; i++ {
		img := image.NewPaletted(image.Rect(i*2, i, i*2+10, i+10), pal)
		for k := range img.Pix {
			img.Pix[k] = uint8((k + i) % 8)
		}
		frames = append(frames, img)
	}
	var buf bytes.Buffer
	err := gif.EncodeAll(&buf, &gif.GIF{Image: frames, Delay: []int{10, 20}, LoopCount: 0,
		Config: image.Config{Width: 20, Height: 15}})
	if err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	res := libraryRun(t, data, "x.gif", "gif.bt")
	if v := value(t, res, "screen.logicalScreenWidth"); v != "20" {
		t.Fatalf("width %s", v)
	}
	if v := value(t, res, "screen.logicalScreenHeight"); v != "15" {
		t.Fatalf("height %s", v)
	}
	images, loop, gce := 0, false, 0
	var shown []string
	for _, b := range res.Root.Find("block").Children() {
		v := b.Value()
		shown = append(shown, v)
		switch {
		case strings.HasPrefix(v, "Image "):
			images++
		case strings.HasPrefix(v, "Application NETSCAPE2.0"):
			loop = b.Find("loopCount") != nil
		case strings.HasPrefix(v, "Graphic control"):
			gce++
		}
	}
	if images != 2 || !loop || gce != 2 {
		t.Fatalf("images %d, loop %v, graphic control %d: %s", images, loop, gce, strings.Join(shown, "; "))
	}
	if !strings.Contains(strings.Join(shown, "; "), "Image 10x10 at 2,1") {
		t.Fatalf("blocks %s", strings.Join(shown, "; "))
	}
	if v := shown[len(shown)-1]; v != "Trailer" {
		t.Fatalf("last block %s", v)
	}
	if v := value(t, res, "block[1].delayTime"); v != "10" {
		t.Fatalf("delay %s", v)
	}
	checkTruncated(t, "gif.bt", data)

	// With a comment and a global color table
	var b2 bytes.Buffer
	if err := gif.Encode(&b2, testImage(30, 30), nil); err != nil {
		t.Fatal(err)
	}
	d := b2.Bytes()
	// A comment extension before the image, and an image of many small
	// sub-blocks before the trailer
	pos := len(d) - 1
	if d[pos] != 0x3B {
		t.Fatal("no trailer")
	}
	var extra []byte
	extra = append(extra, 0x21, 0xFE, 5, 'h', 'e', 'l', 'l', 'o', 3, 'a', 'b', 'c', 0)
	extra = append(extra, 0x2C, 0, 0, 0, 0, 1, 0, 1, 0, 0, 2)
	for k := 0; k < 5000; k++ {
		extra = append(extra, 1, byte(k))
	}
	extra = append(extra, 0)
	d = append(d[:pos:pos], append(extra, 0x3B)...)
	res = libraryRun(t, d, "y.gif", "gif.bt")
	if v := value(t, res, "screen.logicalScreenWidth"); v != "30" {
		t.Fatalf("width %s", v)
	}
	if res.Root.Find("globalColorTable") == nil {
		t.Fatal("no global color table")
	}
	blocks := res.Root.Find("block")
	n := blocks.ChildCount()
	if n < 4 {
		t.Fatalf("%d blocks", n)
	}
	if v := blocks.Child(n - 3).Value(); v != "Comment" {
		t.Fatalf("comment block %s", v)
	}
	if v := blocks.Child(n - 3).Find("commentData.subBlock[0].data").Value(); v != `"hello"` {
		t.Fatalf("comment %s", v)
	}
	img := blocks.Child(n - 2)
	if v := img.Value(); v != "Image 1x1 at 0,0" {
		t.Fatalf("image %s", v)
	}
	if more := img.Find("imageData.moreSubBlocks"); more == nil || more.Size != 2*(5000-4096) {
		t.Fatalf("more sub-blocks %v", more)
	}
	if v := blocks.Child(n - 1).Value(); v != "Trailer" {
		t.Fatalf("last block %s", v)
	}
}

// Every template over files of the system, when there are some
func TestImageTemplatesOnSystemFiles(t *testing.T) {
	found := 0
	for _, c := range []struct{ file, ext string }{
		{"png.bt", ".png"}, {"jpeg.bt", ".jpg"}, {"gif.bt", ".gif"},
	} {
		files := findFiles("/usr/share", c.ext, 3)
		if len(files) == 0 {
			t.Logf("no %s files", c.ext)
			continue
		}
		found += len(files)
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			res := libraryRun(t, data, f, c.file)
			if res.Root.Find("trailingData") != nil && c.file == "png.bt" {
				t.Logf("%s: trailing data", f)
			}
		}
	}
	if found == 0 {
		t.Skip("no image files in /usr/share")
	}
}

// findFiles finds up to n files with the extension, under 2 MB
func findFiles(root, ext string, n int) []string {
	var out []string
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if len(out) >= n {
			return filepath.SkipAll
		}
		if !d.IsDir() && strings.EqualFold(filepath.Ext(p), ext) {
			if info, err := d.Info(); err == nil && info.Size() > 100 && info.Size() < 2<<20 {
				out = append(out, p)
			}
		}
		return nil
	})
	return out
}

// A big JPEG: the scan of the entropy-coded data is fast enough
func TestJPEGTemplateBig(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	img := image.NewRGBA(image.Rect(0, 0, 1600, 1200))
	seed := uint32(1)
	for i := range img.Pix {
		seed = seed*1664525 + 1013904223
		img.Pix[i] = uint8(seed >> 24)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	res := runLimited(t, libraryTemplate(t, "jpeg.bt"), buf.Bytes(), 50_000_000)
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	segs := res.Root.Find("segment")
	if v := segs.Child(segs.ChildCount() - 1).Value(); v != "EOI" {
		t.Fatalf("last segment %s", v)
	}
	t.Logf("%d bytes in %v", buf.Len(), time.Since(start))
}
