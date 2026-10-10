package hexview

import (
	"testing"

	"github.com/ipoluianov/althex/core/doc"
	"github.com/ipoluianov/nui/ui"
)

func newTestView(t *testing.T, data string) (*View, *doc.Document) {
	t.Helper()
	d, err := doc.FromBytes([]byte(data), doc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	v := NewView(d, DefaultOptions())
	v.ensureFont(1)
	v.layout(800, 600)
	return v, d
}

func content(t *testing.T, d *doc.Document) string {
	t.Helper()
	buf := make([]byte, d.Size())
	d.ReadAt(buf, 0)
	return string(buf)
}

func typeHex(v *View, s string) {
	for _, r := range s {
		v.charTyped(r, ui.KeyModifiers{})
	}
}

func TestTypeHexOverwrite(t *testing.T) {
	v, d := newTestView(t, "\x00\x00\x00")
	typeHex(v, "4d5A")
	if got := content(t, d); got != "MZ\x00" {
		t.Fatalf("got %q", got)
	}
	if v.Caret() != 2 {
		t.Fatalf("caret %d", v.Caret())
	}
	// The typed run is one undo
	v.Undo()
	if got := content(t, d); got != "\x00\x00\x00" {
		t.Fatalf("after undo %q", got)
	}
}

func TestTypeHexInsertAndAppend(t *testing.T) {
	v, d := newTestView(t, "AB")
	v.SetInsertMode(true)
	v.GoTo(1)
	typeHex(v, "3")
	if got := content(t, d); got != "A\x30B" {
		t.Fatalf("half a byte typed: %q", got)
	}
	typeHex(v, "1")
	if got := content(t, d); got != "A1B" {
		t.Fatalf("got %q", got)
	}
	// Overwrite past the end appends
	v.SetInsertMode(false)
	v.GoTo(d.Size())
	typeHex(v, "43")
	if got := content(t, d); got != "A1BC" {
		t.Fatalf("append: %q", got)
	}
}

func TestTextColumn(t *testing.T) {
	v, d := newTestView(t, "....")
	v.keyDown(ui.KeyTab, ui.KeyModifiers{})
	v.charTyped('h', ui.KeyModifiers{})
	v.charTyped('i', ui.KeyModifiers{})
	if got := content(t, d); got != "hi.." {
		t.Fatalf("got %q", got)
	}
}

func TestSelectionAndDelete(t *testing.T) {
	v, d := newTestView(t, "0123456789")
	v.GoTo(2)
	for i := 0; i < 3; i++ {
		v.keyDown(ui.KeyArrowRight, ui.KeyModifiers{Shift: true})
	}
	if start, n := v.Selection(); start != 2 || n != 3 {
		t.Fatalf("selection %d+%d", start, n)
	}
	v.keyDown(ui.KeyDelete, ui.KeyModifiers{})
	if got := content(t, d); got != "0156789" {
		t.Fatalf("got %q", got)
	}
	v.keyDown(ui.KeyBackspace, ui.KeyModifiers{})
	if got := content(t, d); got != "056789" {
		t.Fatalf("backspace: %q", got)
	}
	v.Undo()
	v.Undo()
	if got := content(t, d); got != "0123456789" {
		t.Fatalf("undo: %q", got)
	}
}

func TestNavigation(t *testing.T) {
	v, d := newTestView(t, string(make([]byte, 1000)))
	v.keyDown(ui.KeyEnd, ui.KeyModifiers{Ctrl: true})
	if v.Caret() != d.Size() {
		t.Fatalf("Ctrl+End: %d", v.Caret())
	}
	v.keyDown(ui.KeyHome, ui.KeyModifiers{})
	if v.Caret() != d.Size()-d.Size()%16 {
		t.Fatalf("Home: %d", v.Caret())
	}
	v.keyDown(ui.KeyArrowUp, ui.KeyModifiers{})
	if v.Caret() != d.Size()-d.Size()%16-16 {
		t.Fatalf("Up: %d", v.Caret())
	}
	if row := v.Caret() / 16; row < v.topRow || row >= v.topRow+int64(v.rowsShown) {
		t.Fatal("the caret is not on the screen")
	}
	v.keyDown(ui.KeyHome, ui.KeyModifiers{Ctrl: true})
	if v.Caret() != 0 || v.topRow != 0 {
		t.Fatalf("Ctrl+Home: %d, top %d", v.Caret(), v.topRow)
	}
}

func TestReadOnlyView(t *testing.T) {
	v, d := newTestView(t, "abc")
	d.SetReadOnly(true)
	typeHex(v, "00")
	v.keyDown(ui.KeyDelete, ui.KeyModifiers{})
	if got := content(t, d); got != "abc" {
		t.Fatalf("a read-only document changed: %q", got)
	}
}

func TestHit(t *testing.T) {
	v, _ := newTestView(t, string(make([]byte, 100)))
	cw, lh := v.font.CharWidth, v.font.LineHeight
	y := v.headerH + lh + lh/2 // the second row
	pos, col, nib, ok := v.hit(v.cellX(5)+cw+1, y)
	if !ok || pos != 21 || col != colNum || nib != 1 {
		t.Fatalf("hex hit: %d %d %d", pos, col, nib)
	}
	pos, col, _, _ = v.hit(v.xText+3*cw+1, y)
	if pos != 19 || col != colText {
		t.Fatalf("text hit: %d %d", pos, col)
	}
}

func TestTypeBinaryAndDecimal(t *testing.T) {
	v, d := newTestView(t, "\x00\x00")
	o := v.Options()
	o.Mode = ModeBin
	v.SetOptions(o)
	typeHex(v, "01001101")
	if got := content(t, d); got != "M\x00" || v.Caret() != 1 {
		t.Fatalf("binary: %q, caret %d", got, v.Caret())
	}
	o.Mode = ModeDec
	v.SetOptions(o)
	typeHex(v, "3") // 300 does not fit a byte
	if got := content(t, d); got != "M\x00" {
		t.Fatalf("decimal over 255 typed: %q", got)
	}
	typeHex(v, "090")
	if got := content(t, d); got != "MZ" {
		t.Fatalf("decimal: %q", got)
	}
}

func TestLittleEndianGroups(t *testing.T) {
	v, _ := newTestView(t, "\x01\x02\x03\x04\x05\x06\x07\x08")
	o := v.Options()
	o.Group, o.LittleEndian = 4, true
	v.SetOptions(o)
	v.layout(800, 600)
	// The first cell of a group shows its last byte
	if j := v.byteOfCell(0); j != 3 {
		t.Fatalf("cell 0 shows byte %d", j)
	}
	if j := v.byteOfCell(5); j != 6 {
		t.Fatalf("cell 5 shows byte %d", j)
	}
	cw, lh := v.font.CharWidth, v.font.LineHeight
	pos, _, _, _ := v.hit(v.cellX(0)+cw/2, v.headerH+lh/2)
	if pos != 3 {
		t.Fatalf("a click on the first cell is at %d", pos)
	}
}

func TestFitWidth(t *testing.T) {
	v, _ := newTestView(t, string(make([]byte, 100)))
	o := v.Options()
	o.BytesPerRow = 0
	v.SetOptions(o)
	v.layout(600, 400)
	narrow := v.BytesPerRow()
	v.layout(1600, 400)
	wide := v.BytesPerRow()
	if narrow < 8 || wide <= narrow || wide > 64 {
		t.Fatalf("bytes per row: %d at 600 px, %d at 1600 px", narrow, wide)
	}
}

// utf16le is a tiny UTF-16 LE encoding for the tests
type utf16le struct{}

func (utf16le) Unit() int { return 2 }
func (utf16le) Decode(data []byte, i int) (rune, int, bool) {
	if i+1 >= len(data) {
		return 0, 1, false
	}
	r := rune(data[i]) | rune(data[i+1])<<8
	return r, 2, r >= 0x20
}
func (utf16le) Encode(s string) ([]byte, error) {
	var out []byte
	for _, r := range s {
		out = append(out, byte(r), byte(r>>8))
	}
	return out, nil
}

func TestTextInEncoding(t *testing.T) {
	v, d := newTestView(t, "")
	o := v.Options()
	o.Encoding = utf16le{}
	v.SetOptions(o)
	v.keyDown(ui.KeyTab, ui.KeyModifiers{})
	v.charTyped('Ж', ui.KeyModifiers{})
	if got := content(t, d); got != "\x16\x04" || v.Caret() != 2 {
		t.Fatalf("got %q, caret %d", got, v.Caret())
	}
	if s := v.bytesToText([]byte{0x16, 0x04, 0x41, 0x00}); s != "ЖA" {
		t.Fatalf("text %q", s)
	}
}

// A row width that does not fit (a side panel opened) is narrowed, not cut
// off; the first byte on the screen stays there
func TestNarrowedRows(t *testing.T) {
	v, _ := newTestView(t, string(make([]byte, 1<<16)))
	o := v.Options()
	o.BytesPerRow = 16
	v.SetOptions(o)
	v.layout(1600, 400)
	if v.bpr != 16 {
		t.Fatalf("wide: %d bytes a row", v.bpr)
	}
	v.setTop(100) // byte 1600 at the top
	v.layout(400, 400)
	if v.bpr != 8 {
		t.Fatalf("narrow: %d bytes a row, want 8 (rows at round offsets)", v.bpr)
	}
	if first := v.topRow * int64(v.bpr); first > 1600 || first+int64(v.bpr) <= 1600 {
		t.Errorf("the top row starts at %d, not around 1600", first)
	}
	if right := v.xText + v.bpr*v.font.CharWidth; right > 400 {
		t.Errorf("the text column ends at %d of 400", right)
	}
	v.layout(1600, 400)
	if v.bpr != 16 {
		t.Errorf("wide again: %d", v.bpr)
	}
}

// A field or a difference chosen elsewhere: the caret at its start, so the
// status bar and the inspector show its bytes; clipped at the end
func TestSelectCaretAtStart(t *testing.T) {
	v, _ := newTestView(t, "0123456789")
	v.Select(3, 4)
	if s, n := v.Selection(); s != 3 || n != 4 || v.Caret() != 3 {
		t.Errorf("select: %d+%d, caret %d", s, n, v.Caret())
	}
	v.Select(8, 5)
	if s, n := v.Selection(); s != 8 || n != 2 || v.Caret() != 8 {
		t.Errorf("clipped: %d+%d, caret %d", s, n, v.Caret())
	}
}
