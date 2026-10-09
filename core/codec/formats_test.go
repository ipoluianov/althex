package codec

import (
	"bytes"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// The example from the Intel HEX article: 64 bytes at 0x0100
const ihexSample = `:10010000214601360121470136007EFE09D2190140
:100110002146017E17C20001FF5F16002148011928
:10012000194E79234623965778239EDA3F01B2CAA7
:100130003F0156702B5E712B722B732146013421C7
:00000001FF
`

func TestParseIntelHexSample(t *testing.T) {
	segs, err := ParseIntelHex(strings.NewReader(ihexSample))
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 1 || segs[0].Addr != 0x100 || len(segs[0].Data) != 64 {
		t.Fatalf("got %+v", segs)
	}
	d := segs[0].Data
	if !bytes.Equal(d[:4], []byte{0x21, 0x46, 0x01, 0x36}) || !bytes.Equal(d[60:], []byte{0x46, 0x01, 0x34, 0x21}) {
		t.Errorf("data % X", d)
	}
}

func TestParseIntelHexRecords(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []Segment
		err  string // a part of the error
	}{
		{"extended linear 0x0800_0000 (STM32 flash)",
			":020000040800F2\n:0400000001020304F2\n:00000001FF\n",
			[]Segment{{0x08000000, []byte{1, 2, 3, 4}}}, ""},
		{"extended linear above 16M",
			":020000040100F9\n:02FFFE00AABB9C\n:00000001FF\n",
			[]Segment{{0x0100FFFE, []byte{0xAA, 0xBB}}}, ""},
		{"extended segment 0x1200 -> 0x12000",
			":020000021200EA\n:0100100055G9\n:00000001FF\n",
			nil, "not a hex digit"},
		{"extended segment 0x1200 -> 0x12000 ok",
			":020000021200EA\n:01001000559A\n:00000001FF\n",
			[]Segment{{0x12010, []byte{0x55}}}, ""},
		{"start addresses ignored, lower case, CRLF and blank lines",
			":0400000508000130be\r\n\r\n:0400000300001234B3\r\n:0100000041be\r\n:00000001ff\r\n",
			[]Segment{{0, []byte{0x41}}}, ""},
		{"records out of order are sorted and merged",
			":0100020043BA\n:0100000041BE\n:0100010042BC\n:01001000509F\n:00000001FF\n",
			[]Segment{{0, []byte{0x41, 0x42, 0x43}}, {0x10, []byte{0x50}}}, ""},
		{"after EOF ignored", ":0100000041BE\n:00000001FF\ngarbage\n",
			[]Segment{{0, []byte{0x41}}}, ""},
		{"bad checksum", ":0100000041BF\n:00000001FF\n", nil, "line 1: checksum is BF, should be BE"},
		{"no colon", "0100000041BE\n", nil, "line 1: a record must start"},
		{"short", ":0100000041\n", nil, "wrong record length"},
		{"length mismatch", ":0200000041BD\n", nil, "wrong record length"},
		{"odd digits", ":0100000041BE0\n", nil, "odd number"},
		{"unknown type", ":00000006FA\n", nil, "unknown record type 06"},
		{"bad 04 length", ":0100000401FA\n", nil, "must have 2 bytes"},
		{"no EOF", ":0100000041BE\n", nil, "no end-of-file"},
		{"empty", "", nil, "no end-of-file"},
		{"overlap", ":0100000041BE\n:0100000042BD\n:00000001FF\n", nil, "line 2: data at 0x0 overlaps"},
		{"beyond 4G", ":02000004FFFFFC\n:02FFFF00AABB9B\n:00000001FF\n", nil, "beyond the 4 GiB"},
		{"bad checksum on line 3", ":0100000041BE\n\n:0100010042BD\n", nil, "line 3:"},
	}
	for _, tt := range tests {
		got, err := ParseIntelHex(strings.NewReader(tt.in))
		if tt.err != "" {
			if err == nil || !strings.Contains(err.Error(), tt.err) {
				t.Errorf("%s: error %v, want %q", tt.name, err, tt.err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: got %+v, want %+v", tt.name, got, tt.want)
		}
	}
}

func TestWriteIntelHex(t *testing.T) {
	var buf bytes.Buffer
	segs := []Segment{{0x100, []byte{0x21, 0x46, 0x01, 0x36, 0x01, 0x21, 0x47, 0x01, 0x36, 0x00, 0x7E, 0xFE, 0x09, 0xD2, 0x19, 0x01}}}
	if err := WriteIntelHex(&buf, segs, 0); err != nil {
		t.Fatal(err)
	}
	want := ":10010000214601360121470136007EFE09D2190140\n:00000001FF\n"
	if buf.String() != want {
		t.Errorf("got\n%s", buf.String())
	}

	// A segment across the 64K boundary above 16M: records split at the
	// boundary, an 04 record before each 64K page
	buf.Reset()
	segs = []Segment{{0x0100FFFE, []byte{0xAA, 0xBB, 0xCC, 0xDD}}}
	if err := WriteIntelHex(&buf, segs, 0); err != nil {
		t.Fatal(err)
	}
	want = ":020000040100F9\n:02FFFE00AABB9C\n:020000040101F8\n:02000000CCDD55\n:00000001FF\n"
	if buf.String() != want {
		t.Errorf("got\n%s", buf.String())
	}

	if err := WriteIntelHex(&buf, segs, 256); err == nil {
		t.Error("record length 256 accepted")
	}
	if err := WriteIntelHex(&buf, []Segment{{0xFFFFFFFF, []byte{1, 2}}}, 0); err == nil {
		t.Error("data beyond 4G accepted")
	}
}

func randSegs(rnd *rand.Rand, maxAddr uint64) []Segment {
	var segs []Segment
	addr := uint64(rnd.Int63n(int64(maxAddr / 2)))
	for k := rnd.Intn(5); k >= 0; k-- {
		data := make([]byte, rnd.Intn(300)+1)
		rnd.Read(data)
		if addr+uint64(len(data)) > maxAddr {
			break
		}
		segs = append(segs, Segment{addr, data})
		addr += uint64(len(data)) + uint64(rnd.Intn(0x30000)) + 1
	}
	return segs
}

func TestIntelHexRoundTrip(t *testing.T) {
	rnd := rand.New(rand.NewSource(2))
	for k := 0; k < 300; k++ {
		segs := randSegs(rnd, []uint64{0x10000, 0x1000000, 1 << 32}[k%3])
		var buf bytes.Buffer
		if err := WriteIntelHex(&buf, segs, rnd.Intn(256)); err != nil {
			t.Fatal(err)
		}
		got, err := ParseIntelHex(&buf)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, segs) {
			t.Fatalf("round trip of %d segments differs", len(segs))
		}
	}
}

// The example from the S-record article: "Hello world." code at 0
const srecSample = `S00F000068656C6C6F202020202000003C
S11F00007C0802A6900100049421FFF07C6C1B787C8C23783C6000003863000026
S11F001C4BFFFFE5398000007D83637880010014382100107C0803A64E800020E9
S111003848656C6C6F20776F726C642E0A0042
S5030003F9
S9030000FC
`

func TestParseSRecordSample(t *testing.T) {
	segs, err := ParseSRecord(strings.NewReader(srecSample))
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 1 || segs[0].Addr != 0 || len(segs[0].Data) != 0x46 {
		t.Fatalf("got %+v", segs)
	}
	if !bytes.HasSuffix(segs[0].Data, []byte("Hello world.\n\x00")) {
		t.Errorf("data % X", segs[0].Data)
	}
}

func TestParseSRecordRecords(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []Segment
		err  string
	}{
		{"S2 above 64K", "S206010000414275\nS804000000FB\n",
			[]Segment{{0x10000, []byte{0x41, 0x42}}}, ""},
		{"S3 above 16M", "S3070800000041426D\nS70500000000FA\n",
			[]Segment{{0x08000000, []byte{0x41, 0x42}}}, ""},
		{"S6 count", "S104000041BA\nS604000001FA\nS9030000FC\n",
			[]Segment{{0, []byte{0x41}}}, ""},
		{"lower case, CRLF", "s104000041ba\r\ns9030000fc\r\n",
			[]Segment{{0, []byte{0x41}}}, ""},
		{"bad checksum", "S104000041BB\nS9030000FC\n", nil, "line 1: checksum is BB, should be BA"},
		{"wrong count", "S104000041BA\nS5030002FA\nS9030000FC\n", nil, "line 2: the count record says 2"},
		{"S4", "S4030000FC\n", nil, "S4 is not defined"},
		{"not S", "X1040000417A\n", nil, "must start with S0..S9"},
		{"length mismatch", "S1050000417A\n", nil, "wrong record length"},
		{"too short for the address", "S3030000FC\n", nil, "wrong record length"},
		{"no termination", "S104000041BA\n", nil, "no termination"},
		{"overlap", "S104000041BA\nS104000041BA\nS9030000FC\n", nil, "line 2: data at 0x0 overlaps"},
		{"S5 with data", "S504000141B9\nS9030000FC\n", nil, "count record with data"},
	}
	for _, tt := range tests {
		got, err := ParseSRecord(strings.NewReader(tt.in))
		if tt.err != "" {
			if err == nil || !strings.Contains(err.Error(), tt.err) {
				t.Errorf("%s: error %v, want %q", tt.name, err, tt.err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: got %+v, want %+v", tt.name, got, tt.want)
		}
	}
}

func TestWriteSRecord(t *testing.T) {
	var buf bytes.Buffer
	data := []byte("Hello world.\n\x00")
	if err := WriteSRecord(&buf, []Segment{{0x38, data}}, 0x20, "hello     \x00\x00"); err != nil {
		t.Fatal(err)
	}
	want := "S00F000068656C6C6F202020202000003C\nS111003848656C6C6F20776F726C642E0A0042\nS5030001FB\nS9030000FC\n"
	if buf.String() != want {
		t.Errorf("got\n%s", buf.String())
	}
	tests := []struct {
		addr uint64
		want string
	}{
		{0xFFFF, "S104000041BA"}, // the type is by the highest address
		{0x10000, "S206010000414275"},
		{0xFFFFFE, "S2"}, // 0xFFFFFF is the last byte
		{0xFFFFFF, "S3"},
		{0x08000000, "S3070800000041426D"},
	}
	for _, tt := range tests {
		buf.Reset()
		d := []byte{0x41, 0x42}
		if tt.addr == 0xFFFF {
			d = d[:1]
			tt.addr = 0
		}
		if err := WriteSRecord(&buf, []Segment{{tt.addr, d}}, 0, ""); err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(buf.String(), "\n")
		if !strings.HasPrefix(lines[0], tt.want) {
			t.Errorf("0x%X: got %q", tt.addr, lines[0])
		}
		term := map[byte]string{'1': "S9", '2': "S8", '3': "S7"}[lines[0][1]]
		if !strings.HasPrefix(lines[2], term) {
			t.Errorf("0x%X: termination %q", tt.addr, lines[2])
		}
	}
	if err := WriteSRecord(&buf, []Segment{{0, data}}, 253, ""); err == nil {
		t.Error("record length 253 accepted for S1")
	}
	if err := WriteSRecord(&buf, []Segment{{0xFFFFFFFF, []byte{1, 2}}}, 0, ""); err == nil {
		t.Error("data beyond 4G accepted")
	}
}

func TestSRecordRoundTrip(t *testing.T) {
	rnd := rand.New(rand.NewSource(3))
	for k := 0; k < 300; k++ {
		segs := randSegs(rnd, []uint64{0x10000, 0x1000000, 1 << 32}[k%3])
		var buf bytes.Buffer
		if err := WriteSRecord(&buf, segs, rnd.Intn(251), "test"); err != nil {
			t.Fatal(err)
		}
		got, err := ParseSRecord(&buf)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, segs) {
			t.Fatalf("round trip of %d segments differs", len(segs))
		}
	}
}

func TestFlatten(t *testing.T) {
	base, data := Flatten([]Segment{{0x12, []byte{3}}, {0x10, []byte{1, 2}}, {0x15, nil}}, 0xFF)
	if base != 0x10 || !bytes.Equal(data, []byte{1, 2, 3}) {
		t.Errorf("got 0x%X % X", base, data)
	}
	base, data = Flatten([]Segment{{0x10, []byte{1}}, {0x14, []byte{5, 6}}}, 0xFF)
	if base != 0x10 || !bytes.Equal(data, []byte{1, 0xFF, 0xFF, 0xFF, 5, 6}) {
		t.Errorf("got 0x%X % X", base, data)
	}
	if base, data = Flatten(nil, 0); base != 0 || data != nil {
		t.Errorf("empty: 0x%X % X", base, data)
	}
	if lo, hi := Bounds([]Segment{{0x20, []byte{1}}, {0x8, []byte{1, 2}}}); lo != 8 || hi != 0x21 {
		t.Errorf("bounds 0x%X 0x%X", lo, hi)
	}
}

func TestBase64(t *testing.T) {
	data := []byte("Many hands make light work.")
	s := FormatBase64(data, 0)
	if s != "TWFueSBoYW5kcyBtYWtlIGxpZ2h0IHdvcmsu" {
		t.Errorf("got %s", s)
	}
	if s := FormatBase64(data, 10); s != "TWFueSBoYW\n5kcyBtYWtl\nIGxpZ2h0IH\ndvcmsu" {
		t.Errorf("lines: %q", s)
	}
	tests := []struct {
		in   string
		want []byte
		err  bool
	}{
		{"TWE=", []byte("Ma"), false},
		{"TWE", []byte("Ma"), false},
		{" T W\nE =\r\n", []byte("Ma"), false},
		{"+/8=", []byte{0xFB, 0xFF}, false},
		{"-_8", []byte{0xFB, 0xFF}, false},
		{"-_8=", []byte{0xFB, 0xFF}, false},
		{"", []byte{}, false},
		{"+_8", nil, true},
		{"TW=E", nil, true},
		{"T", nil, true},
		{"TWE*", nil, true},
	}
	for _, tt := range tests {
		got, err := ParseBase64(tt.in)
		if (err != nil) != tt.err || (!tt.err && !bytes.Equal(got, tt.want)) {
			t.Errorf("%q: got % X %v", tt.in, got, err)
		}
	}
	rnd := rand.New(rand.NewSource(4))
	for k := 0; k < 200; k++ {
		d := make([]byte, rnd.Intn(100))
		rnd.Read(d)
		got, err := ParseBase64(FormatBase64(d, rnd.Intn(80)))
		if err != nil || !bytes.Equal(got, d) {
			t.Fatalf("round trip of % X: % X %v", d, got, err)
		}
	}
}

func TestFormatArray(t *testing.T) {
	data := []byte{0x4D, 0x5A, 0x90}
	tests := []struct {
		lang Lang
		want string
	}{
		{LangC, "unsigned char name[3] = {\n    0x4D, 0x5A, 0x90\n};"},
		{LangGo, "var name = []byte{\n\t0x4D, 0x5A, 0x90,\n}"},
		{LangPython, "name = bytes([\n    0x4D, 0x5A, 0x90,\n])"},
		{LangRust, "let name: [u8; 3] = [\n    0x4D, 0x5A, 0x90,\n];"},
		{LangJava, "byte[] name = {\n    0x4D, 0x5A, (byte) 0x90\n};"},
		{LangJS, "const name = new Uint8Array([\n    0x4D, 0x5A, 0x90,\n]);"},
		{LangPascal, "const name: array[0..2] of Byte = (\n    $4D, $5A, $90\n);"},
	}
	for _, tt := range tests {
		if got := FormatArray(data, tt.lang, "name", 0); got != tt.want {
			t.Errorf("%v: got\n%s", tt.lang, got)
		}
	}
	if got := FormatArray(data, LangC, "", 2); got != "unsigned char data[3] = {\n    0x4D, 0x5A,\n    0x90\n};" {
		t.Errorf("per line: got\n%s", got)
	}
	if len(Langs()) != 7 || LangRust.String() != "Rust" || Lang(99).String() != "Lang(99)" {
		t.Error("Langs or String")
	}
}

func TestParseArray(t *testing.T) {
	tests := []struct {
		in   string
		want []byte
		err  bool
	}{
		{"0x4D, 77, 0b1010, 0o17", []byte{0x4D, 77, 10, 15}, false},
		{"unsigned char name[3] = {0x4D, 0x5A, 0x90};", []byte{0x4D, 0x5A, 0x90}, false},
		{"static const uint8_t img_2[] = { 0xffu, 0x01U }; // 2 bytes", []byte{0xFF, 1}, false},
		{"let x: [u8; 2] = [0x4D_u8, 1u8];", []byte{0x4D, 1}, false},
		{"const a = new Uint8Array([1, 2]);", []byte{1, 2}, false},
		{"byte[] b = {(byte) 0x90, -112, -1, 127};", []byte{0x90, 0x90, 0xFF, 0x7F}, false},
		{"const a: array[0..1] of Byte = ($4D, $5a);", []byte{0x4D, 0x5A}, false},
		{"x = bytes([1, 2,  # one, two\n 3])", []byte{1, 2, 3}, false},
		{"{ /* 10 */ 1, 2 }", []byte{1, 2}, false},
		{"0010, 255", []byte{10, 255}, false}, // not octal
		{"1_0", []byte{10}, false},
		{"", []byte{}, false},
		{"unsigned char e[0] = {};", []byte{}, false},
		{"256", nil, true},
		{"0x100", nil, true},
		{"-129", nil, true},
		{"99999999999999999999999", nil, true},
		{"0x", nil, true},
		{"4D", nil, true},
		{"0b2", nil, true},
		{"b\"MZ\"", nil, true},
		{"/* 1, 2", nil, true},
	}
	for _, tt := range tests {
		got, err := ParseArray(tt.in)
		if (err != nil) != tt.err || (!tt.err && !bytes.Equal(got, tt.want)) {
			t.Errorf("%q: got % X %v", tt.in, got, err)
		}
	}
}

func TestArrayRoundTrip(t *testing.T) {
	rnd := rand.New(rand.NewSource(5))
	for k := 0; k < 100; k++ {
		d := make([]byte, rnd.Intn(70))
		rnd.Read(d)
		for _, l := range Langs() {
			s := FormatArray(d, l, "name1", rnd.Intn(20))
			got, err := ParseArray(s)
			if err != nil || !bytes.Equal(got, d) {
				t.Fatalf("%v: round trip of % X:\n%s\n% X %v", l, d, s, got, err)
			}
		}
	}
}

func TestHex(t *testing.T) {
	if s := FormatHex([]byte{0x4D, 0x5A, 0x90}, " "); s != "4D 5A 90" {
		t.Errorf("got %q", s)
	}
	if s := FormatHex([]byte{0x4D, 0x5A}, ""); s != "4D5A" {
		t.Errorf("got %q", s)
	}
	if s := FormatHex(nil, " "); s != "" {
		t.Errorf("got %q", s)
	}
	tests := []struct {
		in   string
		want []byte
		err  bool
	}{
		{"4D 5A 90", []byte{0x4D, 0x5A, 0x90}, false},
		{"4d5a90", []byte{0x4D, 0x5A, 0x90}, false},
		{"4d:5a:90", []byte{0x4D, 0x5A, 0x90}, false},
		{"4D-5A-90", []byte{0x4D, 0x5A, 0x90}, false},
		{"0x4D, 0x5A,0X90\n", []byte{0x4D, 0x5A, 0x90}, false},
		{"0x4D5A", []byte{0x4D, 0x5A}, false},
		{"  ", []byte{}, false},
		{"4D5", nil, true},
		{"4 D", nil, true},
		{"0x", nil, true},
		{"4G", nil, true},
		{"0x0x4D", nil, true},
	}
	for _, tt := range tests {
		got, err := ParseHex(tt.in)
		if (err != nil) != tt.err || (!tt.err && !bytes.Equal(got, tt.want)) {
			t.Errorf("%q: got % X %v", tt.in, got, err)
		}
	}
	d := make([]byte, 256)
	for i := range d {
		d[i] = byte(i)
	}
	for _, sep := range []string{"", " ", ", ", ":", "-"} {
		got, err := ParseHex(FormatHex(d, sep))
		if err != nil || !bytes.Equal(got, d) {
			t.Errorf("round trip with %q: %v", sep, err)
		}
	}
}

func TestEscaped(t *testing.T) {
	tests := []struct {
		in   []byte
		want string
	}{
		{[]byte("MZ"), "MZ"},
		{[]byte("a\\b\"c\n\r\t"), `a\\b\"c\n\r\t`},
		{[]byte{0x00, 0x7F, 0xFF}, `\x00\x7F\xFF`},
		// A hex digit after \xHH is escaped too, so C does not read \x00A
		{[]byte{0x00, 'A', 'g', 0x01, '1'}, `\x00\x41g\x01\x31`},
		{[]byte{0x00, 'g', 'A'}, `\x00gA`},
		{[]byte{0x00, '\n', 'A'}, `\x00\nA`},
	}
	for _, tt := range tests {
		if got := FormatEscaped(tt.in); got != tt.want {
			t.Errorf("% X: got %s, want %s", tt.in, got, tt.want)
		}
	}
	parse := []struct {
		in   string
		want []byte
		err  bool
	}{
		{`MZ\x90\x00`, []byte{'M', 'Z', 0x90, 0}, false},
		{`\x4d\x5A`, []byte{0x4D, 0x5A}, false},
		{`\x4142`, []byte{0x41, '4', '2'}, false},
		{`\xf`, []byte{0x0F}, false},
		{`\0\101\377\08`, []byte{0, 'A', 0xFF, 0, '8'}, false},
		{`\a\b\f\v\'\?`, []byte{7, 8, 12, 11, '\'', '?'}, false},
		{`\u0416\U0001F600`, []byte("Ж😀"), false},
		{`"MZ\x90"`, []byte{'M', 'Z', 0x90}, false},
		{"Ж", []byte("Ж"), false},
		{`\`, nil, true},
		{`\x`, nil, true},
		{`\xg`, nil, true},
		{`\400`, nil, true},
		{`\u12`, nil, true},
		{`\uD800`, nil, true},
		{`\UFFFFFFFF`, nil, true},
		{`\q`, nil, true},
	}
	for _, tt := range parse {
		got, err := ParseEscaped(tt.in)
		if (err != nil) != tt.err || (!tt.err && !bytes.Equal(got, tt.want)) {
			t.Errorf("%s: got % X %v", tt.in, got, err)
		}
	}
	// The last quote is escaped, so the quotes are not removed
	if got, err := ParseEscaped(`"a\"`); err != nil || string(got) != `"a"` {
		t.Errorf(`"a\": got %q %v`, got, err)
	}
	d := make([]byte, 256)
	for i := range d {
		d[i] = byte(i)
	}
	got, err := ParseEscaped(FormatEscaped(d))
	if err != nil || !bytes.Equal(got, d) {
		t.Errorf("round trip: %v", err)
	}
}
