package search

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"unicode/utf16"
)

func utf8Enc(s string) ([]byte, error) { return []byte(s), nil }

func utf16LEEnc(s string) ([]byte, error) {
	var b []byte
	for _, u := range utf16.Encode([]rune(s)) {
		b = append(b, byte(u), byte(u>>8))
	}
	return b, nil
}

// latin1Enc fails on characters above U+00FF, like a real 8-bit codec
func latin1Enc(s string) ([]byte, error) {
	var b []byte
	for _, r := range s {
		if r > 0xff {
			return nil, errors.New("not in Latin-1")
		}
		b = append(b, byte(r))
	}
	return b, nil
}

func TestParseHex(t *testing.T) {
	tests := []struct {
		in   string
		want string // the String of the pattern; "" - an error
	}{
		{"4D 5A ?? ?? 50 45", "4D 5A ?? ?? 50 45"},
		{"4d5a", "4D 5A"},
		{"0x4D 0x5A", "4D 5A"},
		{"0X4d,0x5a", "4D 5A"},
		{"4? ?5", "4? ?5"},
		{"4D5A??50", "4D 5A ?? 50"},
		{"  ff\t00\n", "FF 00"},
		{"0x4D5A", "4D 5A"},
		{"", ""},
		{"   ", ""},
		{"4", ""},
		{"4D 5", ""},
		{"4 D", ""},
		{"0x", ""},
		{"GG", ""},
		{"4D-5A", ""},
		{"???", ""},
	}
	for _, tt := range tests {
		p, err := ParseHex(tt.in)
		if tt.want == "" {
			if err == nil {
				t.Errorf("ParseHex(%q) = %v, want an error", tt.in, p)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseHex(%q): %v", tt.in, err)
			continue
		}
		if got := p.String(); got != tt.want {
			t.Errorf("ParseHex(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestParseHexMatch(t *testing.T) {
	p, err := ParseHex("4? ?5 ??")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		b    []byte
		want bool
	}{
		{[]byte{0x40, 0x05, 0x00}, true},
		{[]byte{0x4f, 0xf5, 0xff}, true},
		{[]byte{0x50, 0x05, 0x00}, false},
		{[]byte{0x40, 0x06, 0x00}, false},
		{[]byte{0x40, 0x05}, false},
	} {
		if got := p.Match(tt.b); got != tt.want {
			t.Errorf("Match(% X) = %v, want %v", tt.b, got, tt.want)
		}
	}
}

func FuzzParseHex(f *testing.F) {
	for _, s := range []string{"4D 5A ?? 50", "4d5a", "0x4D 0x5A", "4? ?5", "", "0x", "??", "a,b"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		p, err := ParseHex(s)
		if err != nil {
			if p.Len() != 0 {
				t.Fatalf("an error with a pattern of %d bytes", p.Len())
			}
			return
		}
		if p.Len() == 0 {
			t.Fatal("an empty pattern without an error")
		}
		// What the pattern shows parses to the same pattern, unless it has sets
		str := p.String()
		if strings.Contains(str, "[") {
			t.Fatalf("ParseHex(%q) shows %q", s, str)
		}
		q, err := ParseHex(str)
		if err != nil || q.String() != str {
			t.Fatalf("ParseHex(%q) = %q, again %q, %v", s, str, q.String(), err)
		}
		digits := 0
		for _, c := range s {
			if _, ok := nibble(byte(c)); ok && c < 0x80 {
				digits++
			}
		}
		if p.Len() > digits/2 {
			t.Fatalf("ParseHex(%q) gives %d bytes of %d digits", s, p.Len(), digits)
		}
	})
}

func TestText(t *testing.T) {
	p, err := Text("Ab1", utf8Enc, false)
	if err != nil || !p.Match([]byte("Ab1")) || p.Match([]byte("ab1")) {
		t.Fatalf("case-sensitive: %v %v", p, err)
	}
	p, err = Text("Ab1", utf8Enc, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"Ab1", "ab1", "AB1", "aB1"} {
		if !p.Match([]byte(s)) {
			t.Errorf("%q does not match", s)
		}
	}
	if p.Match([]byte("Ab2")) {
		t.Error("Ab2 matches")
	}

	// Cyrillic in UTF-8: the same length for both cases
	p, err = Text("Жук", utf8Enc, true)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Match([]byte("жУК")) || p.Len() != 6 {
		t.Errorf("cyrillic: %v", p)
	}

	// UTF-16LE
	p, err = Text("Hi", utf16LEEnc, true)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Match([]byte{'h', 0, 'I', 0}) || p.Len() != 4 {
		t.Errorf("utf-16: %v", p)
	}

	// 'k' folds to the Kelvin sign too, 3 bytes in UTF-8: left out
	p, err = Text("k", utf8Enc, true)
	if err != nil || p.Len() != 1 || !p.Match([]byte("K")) {
		t.Errorf("k: %v %v", p, err)
	}
	// Latin-1 has no Kelvin sign: the error of that variant is skipped
	p, err = Text("k", latin1Enc, true)
	if err != nil || !p.Match([]byte("K")) {
		t.Errorf("latin-1 k: %v %v", p, err)
	}
	if _, err := Text("Жук", latin1Enc, true); err == nil {
		t.Error("no error for a character the encoding lacks")
	}

	// An encoding with a BOM cannot be done character by character
	bom := func(s string) ([]byte, error) { return append([]byte{0xef, 0xbb, 0xbf}, s...), nil }
	if _, err := Text("ab", bom, true); !errors.Is(err, ErrNoCaseFold) {
		t.Errorf("bom: %v", err)
	}
	if _, err := Text("", utf8Enc, true); !errors.Is(err, ErrEmptyPattern) {
		t.Errorf("empty: %v", err)
	}
}

func TestParseNumKind(t *testing.T) {
	for s, want := range map[string]NumKind{
		"int32": Int32, "u16": Uint16, "float": Float32, "double": Float64,
		"Int8": Int8, " byte ": Uint8, "i64": Int64, "qword": Uint64, "f64": Float64,
	} {
		if k, ok := ParseNumKind(s); !ok || k != want {
			t.Errorf("ParseNumKind(%q) = %v %v, want %v", s, k, ok, want)
		}
	}
	if _, ok := ParseNumKind("int128"); ok {
		t.Error("int128 parsed")
	}
	for k := Int8; k <= Float64; k++ {
		if k2, ok := ParseNumKind(k.String()); !ok || k2 != k {
			t.Errorf("%v does not parse back", k)
		}
	}
}

func TestNumber(t *testing.T) {
	tests := []struct {
		kind  NumKind
		value string
		be    bool
		want  []byte // nil - an error
	}{
		{Int8, "-1", false, []byte{0xff}},
		{Int8, "127", false, []byte{0x7f}},
		{Int8, "128", false, nil},
		{Int8, "0xFF", false, []byte{0xff}},
		{Int8, "0x1FF", false, nil},
		{Uint8, "255", false, []byte{0xff}},
		{Uint8, "256", false, nil},
		{Uint8, "-1", false, nil},
		{Int16, "0x1234", false, []byte{0x34, 0x12}},
		{Int16, "0x1234", true, []byte{0x12, 0x34}},
		{Int16, "-2", true, []byte{0xff, 0xfe}},
		{Uint16, "65535", false, []byte{0xff, 0xff}},
		{Int32, "305419896", false, []byte{0x78, 0x56, 0x34, 0x12}},
		{Int32, "305419896", true, []byte{0x12, 0x34, 0x56, 0x78}},
		{Int32, "2147483648", false, nil},
		{Uint32, "4294967295", true, []byte{0xff, 0xff, 0xff, 0xff}},
		{Int64, "-9223372036854775808", true, []byte{0x80, 0, 0, 0, 0, 0, 0, 0}},
		{Uint64, "0x0102030405060708", false, []byte{8, 7, 6, 5, 4, 3, 2, 1}},
		{Uint64, "18446744073709551616", false, nil},
		{Float32, "1", false, []byte{0, 0, 0x80, 0x3f}},
		{Float32, "1", true, []byte{0x3f, 0x80, 0, 0}},
		{Float32, "1e39", false, nil},
		{Float64, "1.5", true, []byte{0x3f, 0xf8, 0, 0, 0, 0, 0, 0}},
		{Float64, "-2", false, []byte{0, 0, 0, 0, 0, 0, 0, 0xc0}},
		{Float64, "abc", false, nil},
		{Int32, "", false, nil},
		{Int32, "1.5", false, nil},
	}
	for _, tt := range tests {
		p, err := Number(tt.kind, tt.value, tt.be)
		if tt.want == nil {
			if err == nil {
				t.Errorf("Number(%v, %q) = %v, want an error", tt.kind, tt.value, p)
			}
			continue
		}
		if err != nil {
			t.Errorf("Number(%v, %q): %v", tt.kind, tt.value, err)
			continue
		}
		if p.Len() != len(tt.want) || !p.Match(tt.want) {
			t.Errorf("Number(%v, %q, be %v) = %v, want % X", tt.kind, tt.value, tt.be, p, tt.want)
		}
	}
}

func TestNumberFind(t *testing.T) {
	data := []byte{0, 0x78, 0x56, 0x34, 0x12, 0x12, 0x34, 0x56, 0x78}
	for _, be := range []bool{false, true} {
		p, err := Number(Int32, "0x12345678", be)
		if err != nil {
			t.Fatal(err)
		}
		h, ok, err := FindNext(t.Context(), bytes.NewReader(data), int64(len(data)), p, 0, false)
		want := int64(1)
		if be {
			want = 5
		}
		if err != nil || !ok || h.Off != want {
			t.Errorf("be %v: %v %v %v", be, h, ok, err)
		}
	}
}
