package tmpl

import (
	"context"
	"encoding/binary"
	"testing"
)

// Every built-in template compiles
func TestLibraryCompiles(t *testing.T) {
	if len(Library()) == 0 {
		t.Fatal("no built-in templates")
	}
	for _, tm := range Library() {
		if _, err := tm.Program(); err != nil {
			t.Errorf("%s: %v", tm.File, err)
		}
		if tm.Meta.Name == "" || (len(tm.Meta.Magic) == 0 && len(tm.Meta.Extensions) == 0) {
			t.Errorf("%s: no name, magic or extensions", tm.File)
		}
	}
}

// makeBMP makes a 2x2 bitmap of 24 bits
func makeBMP() []byte {
	b := make([]byte, 54+16)
	copy(b, "BM")
	binary.LittleEndian.PutUint32(b[2:], uint32(len(b)))
	binary.LittleEndian.PutUint32(b[10:], 54)
	binary.LittleEndian.PutUint32(b[14:], 40)
	binary.LittleEndian.PutUint32(b[18:], 2)
	binary.LittleEndian.PutUint32(b[22:], 2)
	binary.LittleEndian.PutUint16(b[26:], 1)
	binary.LittleEndian.PutUint16(b[28:], 24)
	return b
}

// libraryRun detects the template of the data and runs it
func libraryRun(t *testing.T, data []byte, fileName, wantTemplate string) *Result {
	t.Helper()
	m := &mem{b: data}
	tm := Detect(Library(), m, fileName)
	if tm == nil || tm.File != wantTemplate {
		got := "none"
		if tm != nil {
			got = tm.File
		}
		t.Fatalf("detected %s, want %s", got, wantTemplate)
	}
	p, err := tm.Program()
	if err != nil {
		t.Fatal(err)
	}
	res := p.Run(context.Background(), Env{Data: m})
	if res.Err != nil {
		t.Fatalf("%s: %v", wantTemplate, res.Err)
	}
	return res
}

func TestBMP(t *testing.T) {
	res := libraryRun(t, makeBMP(), "x.bmp", "bmp.bt")
	if v := value(t, res, "info.bitCount"); v != "24" {
		t.Fatalf("bitCount %s", v)
	}
	if n := res.Root.Find("pixels"); n == nil || n.Size != 16 {
		t.Fatal("pixels")
	}
}
