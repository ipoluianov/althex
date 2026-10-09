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
	v := NewView(d)
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
	if !ok || pos != 21 || col != colHex || nib != 1 {
		t.Fatalf("hex hit: %d %d %d", pos, col, nib)
	}
	pos, col, _, _ = v.hit(v.xText+3*cw+1, y)
	if pos != 19 || col != colText {
		t.Fatalf("text hit: %d %d", pos, col)
	}
}
