package codec

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func FuzzParseIntelHex(f *testing.F) {
	f.Add(ihexSample)
	f.Add(":020000040800F2\n:0400000001020304F2\n:00000001FF\n")
	f.Add(":020000021200EA\n:01001000559A\n:00000001FF\n")
	f.Add(":0400000508000130BE\n:0100020043BA\n:0100000041BE\n:00000001FF\n")
	f.Fuzz(func(t *testing.T, s string) {
		segs, err := ParseIntelHex(strings.NewReader(s))
		if err != nil {
			return
		}
		var buf bytes.Buffer
		if err := WriteIntelHex(&buf, segs, 0); err != nil {
			t.Fatalf("write: %v", err)
		}
		again, err := ParseIntelHex(&buf)
		if err != nil {
			t.Fatalf("parse of the written file: %v", err)
		}
		if !reflect.DeepEqual(again, segs) {
			t.Fatalf("round trip differs")
		}
	})
}

func FuzzParseSRecord(f *testing.F) {
	f.Add(srecSample)
	f.Add("S2060100004142B5\nS804000000FB\n")
	f.Add("S3070800000041426D\nS70500000000FA\n")
	f.Add("S104000041BA\nS604000001FA\nS9030000FC\n")
	f.Fuzz(func(t *testing.T, s string) {
		segs, err := ParseSRecord(strings.NewReader(s))
		if err != nil {
			return
		}
		var buf bytes.Buffer
		if err := WriteSRecord(&buf, segs, 0, "fuzz"); err != nil {
			t.Fatalf("write: %v", err)
		}
		again, err := ParseSRecord(&buf)
		if err != nil {
			t.Fatalf("parse of the written file: %v", err)
		}
		if !reflect.DeepEqual(again, segs) {
			t.Fatalf("round trip differs")
		}
	})
}

func FuzzParseArray(f *testing.F) {
	f.Add("unsigned char name[3] = {0x4D, 0x5A, 0x90};")
	f.Add("byte[] b = {(byte) 0x90, -112, -1};")
	f.Add("const a: array[0..1] of Byte = ($4D, $5a); // x")
	f.Add("let x: [u8; 2] = [0x4D_u8, 0b1u8, 0o17]; /* y */")
	f.Fuzz(func(t *testing.T, s string) {
		data, err := ParseArray(s)
		if err != nil {
			return
		}
		for _, l := range Langs() {
			again, err := ParseArray(FormatArray(data, l, "n", 7))
			if err != nil || !bytes.Equal(again, data) {
				t.Fatalf("%v: round trip of % X: % X %v", l, data, again, err)
			}
		}
	})
}

func FuzzParseEscaped(f *testing.F) {
	f.Add(`MZ\x90\x00\n\"\\`)
	f.Add(`"Ж\U0001F600\101\0"`)
	f.Add("\x00A\xff1\"")
	f.Fuzz(func(t *testing.T, s string) {
		// Any bytes are an escaped string and back
		b := []byte(s)
		got, err := ParseEscaped(FormatEscaped(b))
		if err != nil || !bytes.Equal(got, b) {
			t.Fatalf("round trip of % X: % X %v", b, got, err)
		}
		data, err := ParseEscaped(s)
		if err != nil {
			return
		}
		again, err := ParseEscaped(FormatEscaped(data))
		if err != nil || !bytes.Equal(again, data) {
			t.Fatalf("round trip of % X: % X %v", data, again, err)
		}
	})
}
