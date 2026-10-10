package interp

import (
	"bytes"
	"math"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/ipoluianov/althex/internal/fuzzguard"
)

// valueOf returns the value of the type among all values
func valueOf(t *testing.T, vals []Value, id string) Value {
	t.Helper()
	for _, v := range vals {
		if v.Type.ID == id {
			return v
		}
	}
	t.Fatalf("no type %q", id)
	return Value{}
}

func rep(b byte, n int) []byte {
	return bytes.Repeat([]byte{b}, n)
}

func cat(parts ...[]byte) []byte {
	return bytes.Join(parts, nil)
}

func TestTypes(t *testing.T) {
	ids := "int8 uint8 int16 uint16 int24 uint24 int32 uint32 int64 uint64 float16 float32 float64 binary8 char utf8 utf16 unix32 unix64 filetime dosdatetime dosdate dostime oletime guid uleb128 sleb128"
	var got []string
	for _, ty := range Types() {
		got = append(got, ty.ID)
	}
	if strings.Join(got, " ") != ids {
		t.Fatalf("types %v", got)
	}
}

func TestInterpret(t *testing.T) {
	tests := []struct {
		id   string
		data []byte
		be   bool
		want string
		size int
	}{
		{"int8", []byte{0xff}, false, "-1", 1},
		{"uint8", []byte{0xff}, true, "255", 1},
		{"int16", []byte{0x34, 0x12}, false, "4660", 2},
		{"int16", []byte{0x12, 0x34}, true, "4660", 2},
		{"int16", []byte{0x00, 0x80}, false, "-32768", 2},
		{"uint16", []byte{0x00, 0x80}, false, "32768", 2},
		{"int16", []byte{0x01}, false, "", 0},
		{"int24", []byte{0x56, 0x34, 0x12}, false, "1193046", 3},
		{"int24", []byte{0x12, 0x34, 0x56}, true, "1193046", 3},
		{"int24", []byte{0xff, 0xff, 0xff}, false, "-1", 3},
		{"uint24", []byte{0xff, 0xff, 0xff}, true, "16777215", 3},
		{"int32", []byte{0x78, 0x56, 0x34, 0x12}, false, "305419896", 4},
		{"int32", []byte{0x80, 0, 0, 0}, true, "-2147483648", 4},
		{"uint32", rep(0xff, 4), false, "4294967295", 4},
		{"int64", []byte{0x80, 0, 0, 0, 0, 0, 0, 0}, true, "-9223372036854775808", 8},
		{"int64", []byte{0, 0, 0, 0, 0, 0, 0, 0x80}, false, "-9223372036854775808", 8},
		{"uint64", rep(0xff, 8), false, "18446744073709551615", 8},
		{"uint64", rep(0xff, 7), false, "", 0},
		{"float16", []byte{0x00, 0x3c}, false, "1", 2},
		{"float16", []byte{0xc0, 0x00}, true, "-2", 2},
		{"float16", []byte{0xff, 0x7b}, false, "65504", 2},
		{"float16", []byte{0x01, 0x00}, false, "6e-08", 2},
		{"float16", []byte{0x35, 0x55}, true, "0.3333", 2},
		{"float16", []byte{0x7c, 0x00}, true, "+Inf", 2},
		{"float16", []byte{0x7e, 0x00}, true, "NaN", 2},
		{"float32", []byte{0x3f, 0xc0, 0, 0}, true, "1.5", 4},
		{"float32", []byte{0, 0, 0xc0, 0x3f}, false, "1.5", 4},
		{"float32", []byte{0xcd, 0xcc, 0xcc, 0x3d}, false, "0.1", 4},
		{"float64", []byte{0x3f, 0xf0, 0, 0, 0, 0, 0, 0}, true, "1", 8},
		{"float64", []byte{0, 0, 0, 0, 0, 0, 0x04, 0xc0}, false, "-2.5", 8},
		{"float64", []byte{0xff, 0xf0, 0, 0, 0, 0, 0, 0}, true, "-Inf", 8},
		{"binary8", []byte{0x4d}, false, "01001101", 1},
		{"char", []byte{0x41}, false, "A", 1},
		{"char", []byte{0x20}, false, " ", 1},
		{"char", []byte{0x7f}, false, "", 0},
		{"char", []byte{0xc0}, false, "", 0},
		{"utf8", []byte{0xd0, 0x9f, 0x00}, false, "П (U+041F)", 2},
		{"utf8", []byte{0xf0, 0x9f, 0x98, 0x80}, false, "😀 (U+1F600)", 4},
		{"utf8", []byte{0x00}, false, "U+0000", 1},
		{"utf8", []byte{0xff}, false, "", 0},
		{"utf8", []byte{0xd0}, false, "", 0},
		{"utf8", []byte{0xef, 0xbf, 0xbd}, false, "� (U+FFFD)", 3},
		{"utf16", []byte{0x1f, 0x04}, false, "П (U+041F)", 2},
		{"utf16", []byte{0x04, 0x1f}, true, "П (U+041F)", 2},
		{"utf16", []byte{0x3d, 0xd8, 0x00, 0xde}, false, "😀 (U+1F600)", 4},
		{"utf16", []byte{0xd8, 0x3d, 0xde, 0x00}, true, "😀 (U+1F600)", 4},
		{"utf16", []byte{0x00, 0xdc, 0x00, 0xd8}, false, "", 0},
		{"utf16", []byte{0x3d, 0xd8, 0x41, 0x00}, false, "", 0},
		{"utf16", []byte{0x3d, 0xd8}, false, "", 0},
		{"utf16", []byte{0x41}, false, "", 0},
		{"unix32", []byte{0, 0, 0, 0}, false, "1970-01-01 00:00:00", 4},
		{"unix32", []byte{0x00, 0x10, 0x5e, 0x5f}, false, "2020-09-13 12:26:40", 4},
		{"unix32", []byte{0x5f, 0x5e, 0x10, 0x00}, true, "2020-09-13 12:26:40", 4},
		{"unix32", rep(0xff, 4), false, "1969-12-31 23:59:59", 4},
		{"unix64", []byte{0, 0, 0, 0, 0x5f, 0x5e, 0x10, 0x00}, true, "2020-09-13 12:26:40", 8},
		{"unix64", []byte{0x7f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, true, "", 0},
		{"unix64", []byte{0x80, 0, 0, 0, 0, 0, 0, 0}, true, "", 0},
		{"filetime", []byte{0x00, 0x80, 0x3e, 0xd5, 0xde, 0xb1, 0x9d, 0x01}, false, "1970-01-01 00:00:00", 8},
		{"filetime", []byte{0x01, 0x9d, 0xb1, 0xde, 0xd5, 0x3e, 0x80, 0x00}, true, "1970-01-01 00:00:00", 8},
		{"filetime", []byte{0x87, 0x56, 0x51, 0xd5, 0xde, 0xb1, 0x9d, 0x01}, false, "1970-01-01 00:00:00.1234567", 8},
		{"filetime", rep(0, 8), false, "1601-01-01 00:00:00", 8},
		{"filetime", rep(0xff, 8), false, "", 0},
		{"dosdate", []byte{0x2d, 0x51}, false, "2020-09-13", 2},
		{"dosdate", []byte{0x51, 0x2d}, true, "2020-09-13", 2},
		{"dosdate", []byte{0, 0}, false, "", 0},
		{"dosdate", []byte{0x5e, 0x50}, false, "", 0}, // 2020-02-30
		{"dosdate", []byte{0x5d, 0x50}, false, "2020-02-29", 2},
		{"dostime", []byte{0x54, 0x63}, false, "12:26:40", 2},
		{"dostime", []byte{0x63, 0x54}, true, "12:26:40", 2},
		{"dostime", []byte{0, 0}, false, "00:00:00", 2},
		{"dostime", []byte{0x00, 0xc0}, false, "", 0}, // hour 24
		{"dostime", []byte{0x1e, 0x00}, false, "", 0}, // second 60
		{"dosdatetime", []byte{0x54, 0x63, 0x2d, 0x51}, false, "2020-09-13 12:26:40", 4},
		{"dosdatetime", []byte{0x51, 0x2d, 0x63, 0x54}, true, "2020-09-13 12:26:40", 4},
		{"dosdatetime", []byte{0x54, 0x63, 0, 0}, false, "", 0},
		{"oletime", rep(0, 8), false, "1899-12-30 00:00:00", 8},
		{"oletime", []byte{0x40, 0x04, 0, 0, 0, 0, 0, 0}, true, "1900-01-01 12:00:00", 8},
		{"oletime", []byte{0, 0, 0, 0, 0, 0, 0xf4, 0xbf}, false, "1899-12-29 06:00:00", 8}, // -1.25
		{"oletime", []byte{0x7f, 0xf8, 0, 0, 0, 0, 0, 1}, true, "", 0},
		{"oletime", []byte{0x41, 0x70, 0, 0, 0, 0, 0, 0}, true, "", 0}, // 2^24 days
		{"guid", []byte{0x40, 0xfc, 0x29, 0x6b, 0x47, 0xca, 0x67, 0x10, 0xb3, 0x1d, 0x00, 0xdd, 0x01, 0x06, 0x62, 0xda}, false, "{6B29FC40-CA47-1067-B31D-00DD010662DA}", 16},
		{"guid", []byte{0x6b, 0x29, 0xfc, 0x40, 0xca, 0x47, 0x10, 0x67, 0xb3, 0x1d, 0x00, 0xdd, 0x01, 0x06, 0x62, 0xda}, true, "{6B29FC40-CA47-1067-B31D-00DD010662DA}", 16},
		{"guid", rep(0, 15), false, "", 0},
		{"uleb128", []byte{0xe5, 0x8e, 0x26, 0xff}, false, "624485", 3},
		{"uleb128", []byte{0x00}, true, "0", 1},
		{"uleb128", []byte{0x80}, false, "", 0},
		{"uleb128", cat(rep(0xff, 9), []byte{0x01}), false, "18446744073709551615", 10},
		{"uleb128", cat(rep(0xff, 9), []byte{0x02}), false, "", 0},
		{"uleb128", rep(0x80, 16), false, "", 0},
		{"sleb128", []byte{0xc0, 0xbb, 0x78}, false, "-123456", 3},
		{"sleb128", []byte{0x7f}, false, "-1", 1},
		{"sleb128", []byte{0x3f}, false, "63", 1},
		{"sleb128", cat(rep(0x80, 9), []byte{0x7f}), false, "-9223372036854775808", 10},
		{"sleb128", cat(rep(0xff, 9), []byte{0x00}), false, "9223372036854775807", 10},
		{"sleb128", cat(rep(0xff, 9), []byte{0x01}), false, "", 0},
	}
	for _, tt := range tests {
		v := valueOf(t, Interpret(tt.data, tt.be), tt.id)
		if v.Text != tt.want || v.Size != tt.size {
			t.Errorf("%s % x be=%v: got %q size %d, want %q size %d", tt.id, tt.data, tt.be, v.Text, v.Size, tt.want, tt.size)
		}
	}
}

func TestInterpretEmpty(t *testing.T) {
	for _, v := range Interpret(nil, false) {
		if v.Text != "" || v.Size != 0 {
			t.Errorf("%s: %q", v.Type.ID, v.Text)
		}
	}
	if n := len(Interpret(rep(0, 100), false)); n != len(Types()) {
		t.Fatalf("%d values", n)
	}
}

func TestEncode(t *testing.T) {
	tests := []struct {
		id   string
		text string
		be   bool
		want []byte
	}{
		{"int8", "-1", false, []byte{0xff}},
		{"int8", "0x1F", false, []byte{0x1f}},
		{"int8", "-0x10", false, []byte{0xf0}},
		{"int8", "-128", false, []byte{0x80}},
		{"uint8", " 255 ", false, []byte{0xff}},
		{"uint8", "0b101", false, []byte{5}},
		{"uint8", "010", false, []byte{10}},
		{"uint8", "0o10", false, []byte{8}},
		{"int16", "4660", false, []byte{0x34, 0x12}},
		{"int16", "4660", true, []byte{0x12, 0x34}},
		{"uint16", "+1_000", true, []byte{0x03, 0xe8}},
		{"int24", "-1", true, []byte{0xff, 0xff, 0xff}},
		{"uint24", "0x123456", false, []byte{0x56, 0x34, 0x12}},
		{"int32", "-2147483648", true, []byte{0x80, 0, 0, 0}},
		{"int64", "-9223372036854775808", false, []byte{0, 0, 0, 0, 0, 0, 0, 0x80}},
		{"uint64", "0xFFFFFFFFFFFFFFFF", false, rep(0xff, 8)},
		{"float16", "1", false, []byte{0x00, 0x3c}},
		{"float16", "65504", true, []byte{0x7b, 0xff}},
		{"float16", "-Inf", true, []byte{0xfc, 0x00}},
		{"float16", "1e-9", true, []byte{0x00, 0x00}},
		{"float16", "-0", true, []byte{0x80, 0x00}},
		{"float32", "1.5", true, []byte{0x3f, 0xc0, 0, 0}},
		{"float64", "-2.5", false, []byte{0, 0, 0, 0, 0, 0, 0x04, 0xc0}},
		{"binary8", "01001101", false, []byte{0x4d}},
		{"binary8", "0b1", false, []byte{1}},
		{"binary8", "0100 1101", false, []byte{0x4d}},
		{"char", " ", false, []byte{0x20}},
		{"utf8", "П", false, []byte{0xd0, 0x9f}},
		{"utf8", "П (U+041F)", false, []byte{0xd0, 0x9f}},
		{"utf8", "U+41f", false, []byte{0xd0, 0x9f}},
		{"utf8", "  (U+0020)", false, []byte{0x20}},
		{"utf16", "😀", true, []byte{0xd8, 0x3d, 0xde, 0x00}},
		{"utf16", "U+041F", false, []byte{0x1f, 0x04}},
		{"unix32", "2020-09-13 12:26:40", false, []byte{0x00, 0x10, 0x5e, 0x5f}},
		{"unix32", "1970-01-01", true, []byte{0, 0, 0, 0}},
		{"unix64", "2020-09-13T12:26:40", true, []byte{0, 0, 0, 0, 0x5f, 0x5e, 0x10, 0x00}},
		{"filetime", "1970-01-01 00:00:00.1234567", false, []byte{0x87, 0x56, 0x51, 0xd5, 0xde, 0xb1, 0x9d, 0x01}},
		{"dosdate", "2020-09-13", false, []byte{0x2d, 0x51}},
		{"dostime", "12:26:40", true, []byte{0x63, 0x54}},
		{"dosdatetime", "2020-09-13 12:26:40", false, []byte{0x54, 0x63, 0x2d, 0x51}},
		{"dosdatetime", "2020-09-13 12:26:40", true, []byte{0x51, 0x2d, 0x63, 0x54}},
		{"oletime", "1899-12-29 06:00:00", false, []byte{0, 0, 0, 0, 0, 0, 0xf4, 0xbf}},
		{"oletime", "1900-01-01 12:00:00", true, []byte{0x40, 0x04, 0, 0, 0, 0, 0, 0}},
		{"guid", "{6B29FC40-CA47-1067-B31D-00DD010662DA}", false, []byte{0x40, 0xfc, 0x29, 0x6b, 0x47, 0xca, 0x67, 0x10, 0xb3, 0x1d, 0x00, 0xdd, 0x01, 0x06, 0x62, 0xda}},
		{"guid", "6b29fc40-ca47-1067-b31d-00dd010662da", true, []byte{0x6b, 0x29, 0xfc, 0x40, 0xca, 0x47, 0x10, 0x67, 0xb3, 0x1d, 0x00, 0xdd, 0x01, 0x06, 0x62, 0xda}},
		{"guid", "6B29FC40CA471067B31D00DD010662DA", false, []byte{0x40, 0xfc, 0x29, 0x6b, 0x47, 0xca, 0x67, 0x10, 0xb3, 0x1d, 0x00, 0xdd, 0x01, 0x06, 0x62, 0xda}},
		{"uleb128", "624485", false, []byte{0xe5, 0x8e, 0x26}},
		{"uleb128", "0", false, []byte{0}},
		{"uleb128", "18446744073709551615", false, cat(rep(0xff, 9), []byte{0x01})},
		{"sleb128", "-123456", false, []byte{0xc0, 0xbb, 0x78}},
		{"sleb128", "63", false, []byte{0x3f}},
		{"sleb128", "64", false, []byte{0xc0, 0x00}},
		{"sleb128", "-64", false, []byte{0x40}},
		{"sleb128", "-9223372036854775808", false, cat(rep(0x80, 9), []byte{0x7f})},
	}
	for _, tt := range tests {
		got, err := Encode(tt.id, tt.text, tt.be)
		if err != nil || !bytes.Equal(got, tt.want) {
			t.Errorf("%s %q be=%v: got % x, %v; want % x", tt.id, tt.text, tt.be, got, err, tt.want)
		}
	}
}

func TestEncodeErrors(t *testing.T) {
	tests := []struct {
		id, text, want string
	}{
		{"int16", "40000", "out of range for Int16"},
		{"int16", "-32769", "out of range for Int16"},
		{"int8", "0xFF", "out of range for Int8"},
		{"uint8", "-1", "out of range for UInt8"},
		{"uint8", "256", "out of range for UInt8"},
		{"uint64", "18446744073709551616", "out of range for UInt64"},
		{"int64", "9223372036854775808", "out of range for Int64"},
		{"int32", "abc", "not an integer"},
		{"int32", "", "not an integer"},
		{"int32", "0x", "not an integer"},
		{"int32", "--1", "not an integer"},
		{"int32", "1.5", "not an integer"},
		{"float16", "65520", "out of range for Float16"},
		{"float16", "-1e6", "out of range for Float16"},
		{"float32", "1e39", "out of range for Float32"},
		{"float64", "1e309", "out of range for Float64"},
		{"float64", "x", "not a number"},
		{"binary8", "012", "not 1 to 8 binary digits"},
		{"binary8", "111111111", "not 1 to 8 binary digits"},
		{"char", "AB", "not one printable ASCII character"},
		{"char", "П", "not one printable ASCII character"},
		{"utf8", "ab", "not one character or U+code"},
		{"utf8", "U+D800", "not a valid character"},
		{"utf16", "U+110000", "not a valid character"},
		{"utf16", "U+xyz", "not one character or U+code"},
		{"unix32", "2040-01-01", "out of range for Unix time (32)"},
		{"unix32", "2020-01-01 00:00:00.5", "Unix time keeps whole seconds"},
		{"unix64", "13.09.2020", "not a date and time (YYYY-MM-DD hh:mm:ss)"},
		{"filetime", "1600-12-31 23:59:59", "out of range for FILETIME"},
		{"filetime", "2000-01-01 00:00:00.00000001", "FILETIME keeps 100 ns"},
		{"dosdate", "1979-12-31", "out of range for DOS date"},
		{"dosdate", "2108-01-01", "out of range for DOS date"},
		{"dosdate", "2020-02-30", "not a date (YYYY-MM-DD)"},
		{"dostime", "12:00:01", "DOS time keeps even seconds"},
		{"dostime", "25:00:00", "not a time (hh:mm:ss)"},
		{"dosdatetime", "2020-01-01 00:00:03", "DOS time keeps even seconds"},
		{"oletime", "0099-12-31 00:00:00", "out of range for OLE date"},
		{"guid", "{6B29FC40-CA47-1067-B31D-00DD010662D}", "not a GUID"},
		{"guid", "6B29FC40+CA47-1067-B31D-00DD010662DA", "not a GUID"},
		{"guid", "{6B29FC40-CA47-1067-B31D-00DD010662DA", "not a GUID"},
		{"uleb128", "-1", "out of range for ULEB128"},
		{"sleb128", "9223372036854775808", "out of range for SLEB128"},
		{"nope", "1", `unknown type "nope"`},
	}
	for _, tt := range tests {
		b, err := Encode(tt.id, tt.text, false)
		if err == nil || err.Error() != tt.want {
			t.Errorf("%s %q: got % x, %v; want %q", tt.id, tt.text, b, err, tt.want)
		}
	}
}

// checkRoundTrip encodes every value shown and checks the bytes show the same
func checkRoundTrip(t *testing.T, data []byte, be bool) {
	t.Helper()
	for _, v := range Interpret(data, be) {
		if v.Text == "" {
			continue
		}
		b, err := Encode(v.Type.ID, v.Text, be)
		if err != nil {
			t.Fatalf("%s % x be=%v: encode %q: %v", v.Type.ID, data, be, v.Text, err)
		}
		if v.Type.Size != 0 && len(b) != v.Type.Size {
			t.Fatalf("%s: encode %q: %d bytes", v.Type.ID, v.Text, len(b))
		}
		w := valueOf(t, Interpret(b, be), v.Type.ID)
		if w.Text != v.Text || w.Size != len(b) {
			t.Fatalf("%s % x be=%v: %q -> % x -> %q", v.Type.ID, data, be, v.Text, b, w.Text)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	ole := func(f float64) []byte { return putUint(math.Float64bits(f), 8, false) }
	samples := [][]byte{
		ole(0), ole(-1.25), ole(-0.5), ole(2958465.99999), ole(-657434.5), ole(45000.123456789),
		{0xd0, 0x9f}, {0x20, 0x00}, {0x29, 0x00}, {0x3d, 0xd8, 0x00, 0xde},
		cat(rep(0x80, 9), []byte{0x7f}), cat(rep(0xff, 9), []byte{0x01}),
	}
	for range 20000 {
		b := make([]byte, r.IntN(MaxBytes+1))
		for i := range b {
			b[i] = byte(r.UintN(256))
		}
		samples = append(samples, b)
	}
	for _, b := range samples {
		checkRoundTrip(t, b, false)
		checkRoundTrip(t, b, true)
	}
}

// Every half reads back as the same bits, except NaN payloads
func TestFloat16All(t *testing.T) {
	for h := range 1 << 16 {
		f := halfToFloat(uint16(h))
		if math.IsNaN(f) {
			continue
		}
		text, _ := decFloat16(putUint(uint64(h), 2, false), false)
		b, err := Encode("float16", text, false)
		if err != nil || getUint(b, 2, false) != uint64(h) {
			t.Fatalf("%04x: %q -> % x, %v", h, text, b, err)
		}
	}
}

func FuzzInterpret(f *testing.F) {
	f.Add([]byte{}, false)
	f.Add([]byte{0xd0, 0x9f, 0x80, 0x80}, true)
	f.Add(rep(0xff, 16), false)
	f.Add(rep(0x80, 20), true)
	f.Fuzz(func(t *testing.T, data []byte, be bool) {
		defer fuzzguard.Start(t.Name())()
		checkRoundTrip(t, data, be)
	})
}

func FuzzEncode(f *testing.F) {
	f.Add(0, "0x1F", false)
	f.Add(20, "1970-01-01 00:00:00.1234567", true)
	f.Add(24, "{6B29FC40-CA47-1067-B31D-00DD010662DA}", false)
	f.Add(15, "П (U+041F)", true)
	f.Fuzz(func(t *testing.T, i int, text string, be bool) {
		defer fuzzguard.Start(t.Name())()
		ts := Types()
		id := ts[uint(i)%uint(len(ts))].ID
		Encode(id, text, be)
	})
}
