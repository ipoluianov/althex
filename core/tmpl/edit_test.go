package tmpl

import (
	"bytes"
	"testing"
)

// Values typed in the tree become the bytes of the field
func TestEncodeTyped(t *testing.T) {
	data := []byte{0x05, 0xA3, 0x10, 0x00, 0x00, 0x00, 0xEF, 0xBE, 0xAD, 0xDE, 0x34, 0x12, 0, 0, 0, 0}
	res := mustRun(t, `
		enum <uchar> KIND { NONE, ONE, FIVE = 5 };
		KIND kind;
		uchar low : 4;
		uchar high : 4;
		uint32 ptr;
		int16 s;
		float f;
		BigEndian();
		uint16 be @ 10;
	`, data)
	for _, tc := range []struct {
		field, text string
		want        []byte
	}{
		{"kind", "ONE", []byte{1}},
		{"kind", "FIVE (5)", []byte{5}},
		{"kind", "2", []byte{2}},
		{"high", "0xF", []byte{0xF3}}, // the low bits stay
		{"low", "1  // a comment", []byte{0xA1}},
		{"ptr", "0x11223344", []byte{0x44, 0x33, 0x22, 0x11}},
		{"ptr", "4294967295", []byte{0xFF, 0xFF, 0xFF, 0xFF}},
		{"s", "-2", []byte{0xFE, 0xFF}},
		{"s", "0xFFFF", []byte{0xFF, 0xFF}},
		{"f", "1.5", []byte{0, 0, 0xC0, 0x3F}},
		{"be", "0x1234", []byte{0x12, 0x34}},
	} {
		n := res.Root.Find(tc.field)
		if n == nil || !n.Editable() {
			t.Fatalf("%s: not editable", tc.field)
		}
		cur := data[n.Off : n.Off+n.Size]
		got, err := n.Encode(tc.text, cur)
		if err != nil || !bytes.Equal(got, tc.want) {
			t.Errorf("%s = %q: % X, %v; want % X", tc.field, tc.text, got, err, tc.want)
		}
	}
	for _, tc := range []struct{ field, text string }{
		{"kind", "TWO"},
		{"kind", "256"},
		{"high", "16"},
		{"s", "70000"},
		{"s", "-40000"},
		{"ptr", ""},
		{"f", "abc"},
	} {
		n := res.Root.Find(tc.field)
		if _, err := n.Encode(tc.text, data[n.Off:n.Off+n.Size]); err == nil {
			t.Errorf("%s = %q accepted", tc.field, tc.text)
		}
	}
	if res.Root.Editable() {
		t.Error("the root is editable")
	}
}
