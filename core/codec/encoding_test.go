package codec

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"
)

func TestEncodingList(t *testing.T) {
	list := Encodings()
	if list[0].ID() != "ascii" {
		t.Fatalf("first encoding is %s, want ascii", list[0].ID())
	}
	need := []string{"ascii", "latin1", "utf8", "utf16le", "utf16be", "cp1251", "cp866",
		"koi8r", "cp1252", "cp437", "ebcdic037", "macroman", "iso8859-5"}
	for _, id := range need {
		if ByID(id) == nil {
			t.Errorf("no encoding %q", id)
		}
	}
	seen := map[string]bool{}
	for _, e := range list {
		if seen[e.ID()] {
			t.Errorf("duplicate id %q", e.ID())
		}
		seen[e.ID()] = true
		if e.Name() == "" {
			t.Errorf("%s has no name", e.ID())
		}
		if ByID(e.ID()) != e {
			t.Errorf("ByID(%q) is another encoding", e.ID())
		}
	}
	if ByID("nope") != nil {
		t.Error("ByID of an unknown id is not nil")
	}
}

func TestDecodeKnown(t *testing.T) {
	tests := []struct {
		id   string
		data []byte
		r    rune
		size int
		ok   bool
	}{
		{"ascii", []byte{0x41}, 'A', 1, true},
		{"ascii", []byte{0x20}, ' ', 1, true},
		{"ascii", []byte{0x7E}, '~', 1, true},
		{"ascii", []byte{0x7F}, 0, 1, false},
		{"ascii", []byte{0x0A}, 0, 1, false},
		{"ascii", []byte{0x80}, 0, 1, false},
		{"ascii", []byte{0xE9}, 0, 1, false},
		{"latin1", []byte{0xE9}, 'é', 1, true},
		{"latin1", []byte{0x85}, 0, 1, false}, // C1 control
		{"cp1252", []byte{0x80}, '€', 1, true},
		{"cp1252", []byte{0x81}, 0, 1, false}, // not defined
		{"cp1251", []byte{0xC0}, 'А', 1, true},
		{"cp1251", []byte{0xFF}, 'я', 1, true},
		{"cp1251", []byte{0xA8}, 'Ё', 1, true},
		{"cp1251", []byte{0x98}, 0, 1, false}, // not defined
		{"cp1251", []byte{0x09}, 0, 1, false},
		{"koi8r", []byte{0xE1}, 'А', 1, true},
		{"koi8r", []byte{0xC1}, 'а', 1, true},
		{"koi8r", []byte{0xB3}, 'Ё', 1, true},
		{"cp866", []byte{0x80}, 'А', 1, true},
		{"cp866", []byte{0xA0}, 'а', 1, true},
		{"cp866", []byte{0xF0}, 'Ё', 1, true},
		{"cp866", []byte{0xDB}, '█', 1, true},
		{"cp437", []byte{0xDB}, '█', 1, true},
		{"cp437", []byte{0x82}, 'é', 1, true},
		{"iso8859-5", []byte{0xB0}, 'А', 1, true},
		{"macroman", []byte{0x8A}, 'ä', 1, true},
		{"ebcdic037", []byte{0xC1}, 'A', 1, true},
		{"ebcdic037", []byte{0x81}, 'a', 1, true},
		{"ebcdic037", []byte{0xF0}, '0', 1, true},
		{"ebcdic037", []byte{0x40}, ' ', 1, true},
		{"ebcdic037", []byte{0x25}, 0, 1, false}, // LF
		{"ebcdic1047", []byte{0xC1}, 'A', 1, true},

		{"utf8", []byte("A"), 'A', 1, true},
		{"utf8", []byte{0x00}, 0, 1, false},
		{"utf8", []byte("Ж"), 'Ж', 2, true},
		{"utf8", []byte("€"), '€', 3, true},
		{"utf8", []byte("😀"), '😀', 4, true},
		{"utf8", []byte{0xE2, 0x82}, 0, 1, false},       // cut
		{"utf8", []byte{0xF0, 0x9F, 0x98}, 0, 1, false}, // cut
		{"utf8", []byte{0x82}, 0, 1, false},             // continuation
		{"utf8", []byte{0xC0, 0x80}, 0, 1, false},       // overlong
		{"utf8", []byte{0xED, 0xA0, 0x80}, 0, 1, false}, // a surrogate
		{"utf8", []byte{0xFF}, 0, 1, false},
		{"utf8", []byte{0xC2, 0x85}, 0, 2, false},       // U+0085, a control
		{"utf8", []byte{0xEF, 0xBB, 0xBF}, 0, 3, false}, // BOM, a format char

		{"utf16le", []byte{0x41, 0x00}, 'A', 2, true},
		{"utf16be", []byte{0x00, 0x41}, 'A', 2, true},
		{"utf16le", []byte{0x16, 0x04}, 'Ж', 2, true},
		{"utf16be", []byte{0x04, 0x16}, 'Ж', 2, true},
		{"utf16le", []byte{0x0A, 0x00}, 0, 2, false},
		{"utf16le", []byte{0x41}, 0, 1, false}, // cut
		{"utf16le", []byte{0x3D, 0xD8, 0x00, 0xDE}, '😀', 4, true},
		{"utf16be", []byte{0xD8, 0x3D, 0xDE, 0x00}, '😀', 4, true},
		{"utf16le", []byte{0x3D, 0xD8}, 0, 2, false},             // high surrogate, cut
		{"utf16le", []byte{0x3D, 0xD8, 0x00}, 0, 2, false},       // pair cut
		{"utf16le", []byte{0x3D, 0xD8, 0x41, 0x00}, 0, 2, false}, // high, no low
		{"utf16le", []byte{0x00, 0xDE, 0x41, 0x00}, 0, 2, false}, // low alone
		{"utf16be", []byte{0xD8, 0x3D, 0xD8, 0x3D}, 0, 2, false}, // two highs
	}
	for _, tt := range tests {
		e := ByID(tt.id)
		r, size, ok := e.Decode(tt.data, 0)
		if size != tt.size || ok != tt.ok || (tt.ok && r != tt.r) {
			t.Errorf("%s % X: got %q %d %v, want %q %d %v", tt.id, tt.data, r, size, ok, tt.r, tt.size, tt.ok)
		}
	}
}

func TestDecodeOffset(t *testing.T) {
	data := []byte("aЖb")
	e := ByID("utf8")
	var got []rune
	for i := 0; i < len(data); {
		r, n, _ := e.Decode(data, i)
		got = append(got, r)
		i += n
	}
	if string(got) != "aЖb" {
		t.Errorf("got %q", string(got))
	}
	// Starting at the continuation byte
	if _, n, ok := e.Decode(data, 2); ok || n != 1 {
		t.Errorf("continuation byte: %d %v", n, ok)
	}
}

// Decode must never panic, whatever the data and the offset, and must
// always move forward
func TestDecodeNoPanic(t *testing.T) {
	rnd := rand.New(rand.NewSource(1))
	for _, e := range Encodings() {
		for k := 0; k < 200; k++ {
			data := make([]byte, rnd.Intn(9)+1)
			rnd.Read(data)
			for i := range data {
				_, n, _ := e.Decode(data, i)
				if n < 1 || i+n > len(data) {
					t.Fatalf("%s % X at %d: size %d", e.ID(), data, i, n)
				}
			}
		}
	}
}

// Every printable byte of a single-byte encoding encodes back to itself
func TestSingleByteRoundTrip(t *testing.T) {
	for _, e := range Encodings() {
		if e.Unit() != 1 || e.ID() == "utf8" {
			continue
		}
		printable := 0
		for b := 0; b < 256; b++ {
			r, _, ok := e.Decode([]byte{byte(b)}, 0)
			if !ok {
				continue
			}
			printable++
			got, err := e.Encode(string(r))
			if err != nil || !bytes.Equal(got, []byte{byte(b)}) {
				t.Errorf("%s: %02X -> %q -> % X %v", e.ID(), b, r, got, err)
			}
		}
		if printable < 95 {
			t.Errorf("%s: only %d printable bytes", e.ID(), printable)
		}
	}
}

func TestEncode(t *testing.T) {
	tests := []struct {
		id   string
		s    string
		want []byte
		err  bool
	}{
		{"ascii", "MZ", []byte("MZ"), false},
		{"ascii", "é", nil, true},
		{"latin1", "é", []byte{0xE9}, false},
		{"latin1", "Ж", nil, true},
		{"cp1251", "Привет", []byte{0xCF, 0xF0, 0xE8, 0xE2, 0xE5, 0xF2}, false},
		{"koi8r", "Привет", []byte{0xF0, 0xD2, 0xC9, 0xD7, 0xC5, 0xD4}, false},
		{"cp866", "Привет", []byte{0x8F, 0xE0, 0xA8, 0xA2, 0xA5, 0xE2}, false},
		{"cp1251", "€", []byte{0x88}, false},
		{"cp866", "€", nil, true},
		{"ebcdic037", "AZ09", []byte{0xC1, 0xE9, 0xF0, 0xF9}, false},
		{"utf8", "Ж€", []byte{0xD0, 0x96, 0xE2, 0x82, 0xAC}, false},
		{"utf8", "\xff", nil, true},
		{"utf16le", "AЖ😀", []byte{0x41, 0, 0x16, 0x04, 0x3D, 0xD8, 0x00, 0xDE}, false},
		{"utf16be", "AЖ😀", []byte{0, 0x41, 0x04, 0x16, 0xD8, 0x3D, 0xDE, 0x00}, false},
		{"cp1251", "a\xffb", nil, true},
	}
	for _, tt := range tests {
		got, err := ByID(tt.id).Encode(tt.s)
		if (err != nil) != tt.err || !bytes.Equal(got, tt.want) {
			t.Errorf("%s %q: got % X %v", tt.id, tt.s, got, err)
		}
	}
	var ce *CharError
	if _, err := ByID("ascii").Encode("é"); !errors.As(err, &ce) || ce.Char != 'é' {
		t.Errorf("want a CharError, got %v", err)
	}
}

func BenchmarkDecodeCP1251(b *testing.B) {
	data := make([]byte, 4096)
	for i := range data {
		data[i] = byte(i)
	}
	e := ByID("cp1251")
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		for i := range data {
			e.Decode(data, i)
		}
	}
}
