package tmpl

import (
	"bytes"
	"context"
	"errors"
	"math"
	"strings"
	"testing"
)

// covOut runs a template that must not fail and returns its output, one
// line per Printf, joined with "|"
func covOut(t *testing.T, src string, data []byte) string {
	t.Helper()
	return strings.Join(mustRun(t, src, data).Output, "|")
}

// covErr runs a template that must fail at run time with the text
func covErr(t *testing.T, src string, data []byte, want string) *Result {
	t.Helper()
	res := run(t, src, data)
	if res.Err == nil || !strings.Contains(res.Err.Error(), want) {
		t.Fatalf("%q: error %v, want %q", src, res.Err, want)
	}
	return res
}

// covCompileErr compiles a template that must fail with the text
func covCompileErr(t *testing.T, src, want string) {
	t.Helper()
	_, err := Compile("bad", src)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("%q: compile error %v, want %q", src, err, want)
	}
}

func TestCoverageStringBuiltins(t *testing.T) {
	got := covOut(t, `
		Printf("%s %s", ToUpper("abC"), ToLower("AbC"));
		Printf("%d %d", Strlen("hello"), Strlen(""));
		Printf("[%s][%s][%s][%s][%s]", SubStr("hello", 1, 3), SubStr("hello", 3), SubStr("hello", -2, 2),
			SubStr("hello", 10), SubStr("hello", 1, -1));
		Printf("%d %d %d", Strstr("hello", "ll"), Strstr("hello", "z"), Strstr("hello", ""));
		Printf("%d %d %d", Atoi(" 42 "), Atoi("-7"), Atoi("x"));
		char name[4];
		Printf("%d %s", Strlen(name), ToLower(name));
	`, []byte("ABCD"))
	want := "ABC abc|5 0|[ell][lo][he][][ello]|2 -1 0|42 -7 0|4 abcd"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	covErr(t, `Strlen(5);`, nil, "a string expected, a number found")
	covErr(t, `Strlen();`, nil, "too few arguments")
	covErr(t, `ToUpper(1.5);`, nil, "a string expected, a float found")
}

func TestCoverageMathBuiltins(t *testing.T) {
	got := covOut(t, `
		Printf("%d %g %d", Abs(-5), Abs(-2.5), Abs(3));
		Printf("%d %d %g %s", Min(3, 7), Max(3, 7), Min(2.5, 1), Max("a", "b"));
		Printf("%g %g %g %g", Pow(2, 10), Floor(2.7), Ceil(2.1), Sqrt(16));
		Printf("%g %g", Floor(-2.5), Pow(2.0, -1));
	`, nil)
	want := "5 2.5 3|3 7 1 b|1024 2 3 4|-3 0.5"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	covErr(t, `Floor("x");`, nil, "a number expected, a string found")
	covErr(t, `Pow(2);`, nil, "too few arguments")
	covErr(t, `Abs("x");`, nil, "a number expected, a string found")
}

func TestCoveragePrintfFormats(t *testing.T) {
	got := covOut(t, `
		Printf("%5d|%-5d|%05d|%+d|%i", 42, 42, 42, 42, 7);
		Printf("%u", -1);
		Printf("%x %X %o %b %#x", 255, 255, 255, 5, 255);
		Printf("%c%c", 65, 'b');
		Printf("%s %s %s", 12, 1.5, "str");
		Printf("%.2f %e %G %g", 3.14159, 1500.0, 0.00001, 2);
		Printf("%lld %hd %Lf %lu", 1, 2, 0.5, 3);
		Printf("100%% done");
		Printf("trailing %");
		Printf("%d", 3.9);
		Printf("%q-%d", 1, 2);
	`, nil)
	want := strings.Join([]string{
		"   42|42   |00042|+42|7",
		"18446744073709551615",
		"ff FF 377 101 0xff",
		"Ab",
		"12 1.5 str",
		"3.14 1.500000e+03 1E-05 2",
		"1 2 0.500000 3",
		"100% done",
		"trailing %",
		"3",
		// An unknown verb is written as it is; it uses its argument
		"%q-2",
	}, "|")
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	covErr(t, `Printf("%d %d", 1);`, nil, "too few arguments for the format")
	covErr(t, `Printf(1);`, nil, "a string expected")

	// SPrintf and Str make strings; Warning goes to the warnings and the output
	res := mustRun(t, `
		local string s = SPrintf("%03d", 7) + Str("-%s", "x");
		Printf("%s", s);
		Warning("careful %d", 1);
	`, nil)
	if strings.Join(res.Output, "|") != "007-x|Warning: careful 1" {
		t.Fatalf("output %q", res.Output)
	}
	if len(res.Warnings) != 1 || res.Warnings[0] != "careful 1" {
		t.Fatalf("warnings %q", res.Warnings)
	}
}

func TestCoverageAssertExit(t *testing.T) {
	if got := covOut(t, `Assert(1); Assert(1 == 1, "never"); Printf("ok");`, nil); got != "ok" {
		t.Fatalf("got %q", got)
	}
	covErr(t, `Assert(0);`, nil, "assertion failed")
	covErr(t, `Assert(2 > 3, "bad %d", 5);`, nil, "bad 5")
	covErr(t, `Assert("");`, nil, "assertion failed")

	// Exit stops the run without an error; what was read before stays
	res := mustRun(t, `uchar a; Printf("before"); Exit(3); uchar b; Printf("after");`, []byte{1, 2})
	if strings.Join(res.Output, "|") != "before" || res.Root.Find("a") == nil || res.Root.Find("b") != nil {
		t.Fatalf("exit: %q", res.Output)
	}
	res = mustRun(t, `if (1) Exit(); Printf("after");`, nil)
	if len(res.Output) != 0 {
		t.Fatalf("exit without a code: %q", res.Output)
	}
}

func TestCoverageEnvBuiltins(t *testing.T) {
	p, err := Compile("env", `
		Printf("%d %d %d", GetSelStart(), GetSelSize(), GetCursorPos());
		Printf("%d %d", IsLittleEndian(), IsBigEndian());
		BigEndian();
		Printf("%d %d", IsLittleEndian(), IsBigEndian());
		LittleEndian();
		Printf("%d %d", FTell(), FileSize());
		uchar first;
		Printf("%d", FTell());
	`)
	if err != nil {
		t.Fatal(err)
	}
	res := p.Run(context.Background(), Env{Data: &mem{b: make([]byte, 10)}, SelStart: 3, SelSize: 4, Cursor: 5, Start: 2})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if got := strings.Join(res.Output, "|"); got != "3 4 5|1 0|0 1|2 10|3" {
		t.Fatalf("got %q", got)
	}
	if res.Root.Find("first").Off != 2 {
		t.Fatal("Env.Start is not where reading starts")
	}
}

func TestCoverageSeekSkip(t *testing.T) {
	got := covOut(t, `
		Printf("%d %d", FSkip(2), FTell());
		Printf("%d %d", FSkip(-1), FTell());
		Printf("%d %d", FSeek(-1), FTell());
		Printf("%d %d", FSeek(FileSize()), FEof());
		Printf("%d %d", FSeek(FileSize() + 1), FTell());
		Printf("%d %d", FSkip(-100), FTell());
	`, make([]byte, 6))
	want := "0 2|0 1|-1 1|0 1|-1 6|-1 6"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestCoverageReadBuiltins(t *testing.T) {
	data := []byte{0xFF, 0xFE, 0x01, 0x80, 0x00, 0x3C, 0, 0, 0, 0, 0, 0, 0xF0, 0x3F}
	got := covOut(t, `
		Printf("%d %d %d %d", ReadByte(0), ReadUByte(0), ReadShort(0), ReadUShort(0));
		Printf("%d %u", ReadInt(0), ReadUInt(0));
		Printf("%d %d", ReadInt64(0) == ReadQuad(0), ReadUInt64(0) == ReadUQuad(0));
		Printf("%g %g", ReadHFloat(4), ReadDouble(6));
		Printf("%d", ReadUByte());
		FSeek(2);
		Printf("%d", ReadUShort());
		BigEndian();
		Printf("%d %x", ReadShort(0), ReadUInt(0));
		Printf("%g", ReadHFloat(5));
	`, data)
	want := "-1 255 -257 65279|-2147352833 2147614463|1 1|1 1|255|32769|-2 fffe0180|1"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	// ReadFloat of 1.0f
	if got := covOut(t, `Printf("%g %g", ReadFloat(0), ReadFloat(4));`, []byte{0, 0, 0x80, 0x3F, 0, 0, 0xC0, 0xBF}); got != "1 -1.5" {
		t.Fatalf("floats %q", got)
	}
	covErr(t, `ReadInt(FileSize() - 2);`, []byte{1, 2, 3}, "goes past the end")
	covErr(t, `ReadUByte(-1);`, []byte{1}, "goes past the end")
}

func TestCoverageReadStringBuiltins(t *testing.T) {
	data := append([]byte("hello\x00world"), 'H', 0, 'i', 0, 0x3A, 0x04, 0, 0)
	got := covOut(t, `
		Printf("[%s][%s][%s]", ReadString(0), ReadString(6), ReadString(0, 3));
		Printf("[%s][%s]", ReadString(0, 0), ReadString(0, -1));
		Printf("[%s][%s]", ReadWString(11), ReadWString(11, 1));
		FSeek(6);
		Printf("[%s]", ReadString());
		Printf("[%s]", ReadString(FileSize()));
	`, data)
	// "world" runs on into the UTF-16 text: it has no zero of its own
	want := "[hello][worldH][hel]|[][hello]|[Hiк][H]|[worldH]|[]"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	covErr(t, `ReadString(100);`, data, "past the end")
	covErr(t, `ReadWString(-1);`, data, "past the end")
	// Big-endian UTF-16
	if got := covOut(t, `BigEndian(); Printf("%s", ReadWString(0));`, []byte{0, 'O', 0, 'K', 0, 0}); got != "OK" {
		t.Fatalf("big-endian wide %q", got)
	}
}

func TestCoverageWriteBuiltins(t *testing.T) {
	m := &mem{b: make([]byte, 32)}
	p, err := Compile("w", `
		WriteByte(0, -1);
		WriteUByte(1, 0x12);
		WriteShort(2, -2);
		WriteUShort(4, 0x3456);
		WriteInt(6, -3);
		WriteUInt(10, 0x789ABCDE);
		WriteInt64(14, -4);
		WriteHFloat(22, 1.5);
		WriteFloat(24, 2.0);
		BigEndian();
		WriteUShort(28, 0xA1B2);
		WriteString(30, "ok");
		Printf("%g %g %x", ReadHFloat(22) , 0, ReadUShort(28));
	`)
	if err != nil {
		t.Fatal(err)
	}
	res := p.Run(context.Background(), Env{Data: m, Writer: m})
	if res.Err != nil || !res.Wrote {
		t.Fatalf("%v %v", res.Err, res.Wrote)
	}
	want := []byte{
		0xFF, 0x12, 0xFE, 0xFF, 0x56, 0x34, 0xFD, 0xFF, 0xFF, 0xFF,
		0xDE, 0xBC, 0x9A, 0x78, 0xFC, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF,
		0x00, 0x3E, 0x00, 0x00, 0x00, 0x40, 0xA1, 0xB2, 'o', 'k',
	}
	if !bytes.Equal(m.b, want) {
		t.Fatalf("got  % x\nwant % x", m.b, want)
	}
	// ReadHFloat at 22 in big-endian: 0x003E is a subnormal, 62 * 2^-24
	if res.Output[0] != "3.6954879760742188e-06 0 a1b2" {
		t.Fatalf("output %q", res.Output)
	}

	m = &mem{b: make([]byte, 16)}
	p, _ = Compile("w2", `WriteDouble(0, -0.5); WriteQuad(8, 1); BigEndian(); WriteUQuad(8, 0x0102030405060708); WriteDouble(0, 2);`)
	if res := p.Run(context.Background(), Env{Data: m, Writer: m}); res.Err != nil {
		t.Fatal(res.Err)
	}
	if !bytes.Equal(m.b, []byte{0x40, 0, 0, 0, 0, 0, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8}) {
		t.Fatalf("% x", m.b)
	}

	covErr(t, `WriteInt(FileSize() - 2, 1);`, make([]byte, 4), "past the end")
	covErr(t, `WriteUByte(-1, 1);`, make([]byte, 4), "past the end")
	covErr(t, `WriteString(2, "abc");`, make([]byte, 4), "past the end")
	covErr(t, `WriteString(-1, "");`, make([]byte, 4), "past the end")
	covErr(t, `WriteString(0, 5);`, make([]byte, 4), "a string expected")
}

// failWriter is a writer that fails every change
type failWriter struct{}

var errCovWrite = errors.New("disk full")

func (failWriter) Overwrite(off int64, b []byte) error { return errCovWrite }
func (failWriter) Insert(off int64, b []byte) error    { return errCovWrite }
func (failWriter) Delete(off, n int64) error           { return errCovWrite }

func TestCoverageInsertDelete(t *testing.T) {
	m := &mem{b: []byte{1, 2, 3, 4}}
	p, err := Compile("ins", `
		InsertBytes(1, 2);
		InsertBytes(FileSize(), 1, 0xEE);
		DeleteBytes(0, 1);
		Printf("%d", FileSize());
		uchar all[FileSize()];
	`)
	if err != nil {
		t.Fatal(err)
	}
	res := p.Run(context.Background(), Env{Data: m, Writer: m})
	if res.Err != nil || !res.Wrote {
		t.Fatalf("%v %v", res.Err, res.Wrote)
	}
	if !bytes.Equal(m.b, []byte{0, 0, 2, 3, 4, 0xEE}) || res.Output[0] != "6" {
		t.Fatalf("% x %q", m.b, res.Output)
	}
	if res.Root.Find("all").ChildCount() != 6 {
		t.Fatal("the reader does not see the new size")
	}

	for _, c := range []struct{ src, want string }{
		{`InsertBytes(0, -1);`, "inserting -1 bytes"},
		{`InsertBytes(5, 1);`, "inserting 1 bytes at 0x5"},
		{`InsertBytes(-1, 1);`, "inserting 1 bytes"},
		{`DeleteBytes(0, 5);`, "deleting 5 bytes"},
		{`DeleteBytes(-1, 1);`, "deleting 1 bytes"},
		{`DeleteBytes(0, -1);`, "deleting -1 bytes"},
	} {
		covErr(t, c.src, []byte{1, 2, 3, 4}, c.want)
	}

	// Without a writer, and with a writer that fails
	for _, c := range []struct{ src, ro, fail string }{
		{`InsertBytes(0, 1);`, "read-only", "inserting at 0x0: disk full"},
		{`DeleteBytes(0, 1);`, "read-only", "deleting at 0x0: disk full"},
		{`WriteUByte(0, 1);`, "read-only", "writing at 0x0: disk full"},
	} {
		p, err := Compile("w", c.src)
		if err != nil {
			t.Fatal(err)
		}
		d := &mem{b: []byte{1, 2, 3, 4}}
		if res := p.Run(context.Background(), Env{Data: d}); res.Err == nil || !strings.Contains(res.Err.Error(), c.ro) {
			t.Errorf("%s read-only: %v", c.src, res.Err)
		}
		if res := p.Run(context.Background(), Env{Data: d, Writer: failWriter{}}); res.Err == nil || !strings.Contains(res.Err.Error(), c.fail) || res.Wrote {
			t.Errorf("%s failing: %v", c.src, res.Err)
		}
	}
}

func TestCoverageChecksum(t *testing.T) {
	data := []byte("123456789")
	got := covOut(t, `
		Printf("%X", Checksum("crc32", 0, FileSize()));
		Printf("%X", Checksum("CRC32", 0, 9));
		Printf("%X", Checksum("CRC-16/KERMIT", 0, 9));
		Printf("%X", Checksum("adler32", 0, 9));
		Printf("%X", Checksum("sum8", 0, 9));
		Printf("%X", Checksum("xor8", 0, 9));
		Printf("%s", Checksum("md5", 0, 9));
		Printf("%s", Checksum("sha1", 0, 0));
		Printf("%X", Checksum("crc32", 0, 0));
	`, data)
	want := strings.Join([]string{
		"CBF43926", "CBF43926", "2189", "91E01DE", "DD", "31",
		"25f9e794323b453885f5181f1b624d0b",
		"da39a3ee5e6b4b0d3255bfef95601890afd80709",
		"0",
	}, "|")
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	covErr(t, `Checksum("nope", 0, 1);`, data, `unknown checksum "nope"`)
	covErr(t, `Checksum("crc32", 5, 5);`, data, "out of the data")
	covErr(t, `Checksum("crc32", -1, 1);`, data, "out of the data")
	covErr(t, `Checksum("crc32", 0, -1);`, data, "out of the data")

	// A canceled context stops the checksum
	p, _ := Compile("c", `Checksum("sha256", 0, FileSize());`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if res := p.Run(ctx, Env{Data: &mem{b: make([]byte, 1<<20)}}); res.Err == nil {
		t.Fatal("a canceled checksum gives no error")
	}
}

func TestCoverageFindHex(t *testing.T) {
	data := []byte{0x00, 0xFF, 0xD8, 0x01, 0xFF, 0xD8, 0x02, 0xAA}
	got := covOut(t, `
		Printf("%d %d %d", FindHex("FF D8", 0), FindHex("FF D8", 2), FindHex("FF D8 ??", 4));
		Printf("%d %d", FindHex("FF D8", 2, 5), FindHex("FF D8", 2, 6));
		Printf("%d %d %d", FindHex("AA", 8), FindHex("AA", -1), FindHex("AA", 3, 3));
		Printf("%d %d", FindHex("FFD8", 0, 100), FindHex("BB", 0));
	`, data)
	want := "1 4 4|-1 4|-1 -1 -1|1 -1"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	covErr(t, `FindHex("ZZ", 0);`, data, "FindHex")
	covErr(t, `FindHex("", 0);`, data, "FindHex")

	p, _ := Compile("c", `FindHex("01 02 03", 0);`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if res := p.Run(ctx, Env{Data: &mem{b: make([]byte, 4<<20)}}); res.Err == nil {
		t.Fatal("a canceled search gives no error")
	}
}

func TestCoverageEnumToString(t *testing.T) {
	got := covOut(t, `
		enum <uchar> COLOR { RED, GREEN = 5, BLUE };
		COLOR a;
		COLOR b;
		uchar c;
		Printf("[%s][%s][%s][%s][%s]", EnumToString(a), EnumToString(b), EnumToString(c), EnumToString(5), EnumToString(BLUE));
	`, []byte{6, 9, 0})
	if got != "[BLUE][][][][]" {
		t.Fatalf("got %q", got)
	}
}

func TestCoverageHalfFloats(t *testing.T) {
	cases := []struct {
		in   float64
		bits uint16
	}{
		{0, 0x0000},
		{1, 0x3C00},
		{-2, 0xC000},
		{1.5, 0x3E00},
		{65504, 0x7BFF},
		{1e6, 0x7C00},              // too big: infinity
		{-1e6, 0xFC00},             // minus infinity
		{math.Inf(1), 0x7C00},      // infinity
		{math.Pow(2, -24), 0x0001}, // the smallest subnormal
		{math.Pow(2, -15), 0x0200}, // a subnormal
		{1e-10, 0x0000},            // too small: zero
		{-1e-10, 0x8000},           // minus zero
		{0.333251953125, 0x3555},   // exact
		{math.Pow(2, -14), 0x0400}, // the smallest normal
	}
	for _, c := range cases {
		if got := floatToHalf(c.in); got != c.bits {
			t.Errorf("floatToHalf(%g) = %#04x, want %#04x", c.in, got, c.bits)
		}
		if c.in == 1e6 || c.in == -1e6 || c.in == 1e-10 || c.in == -1e-10 {
			continue
		}
		if back := halfToFloat(c.bits); back != c.in {
			t.Errorf("halfToFloat(%#04x) = %g, want %g", c.bits, back, c.in)
		}
	}
	if got := floatToHalf(math.NaN()); got != 0x7E00 {
		t.Errorf("NaN -> %#04x", got)
	}
	if !math.IsNaN(halfToFloat(0x7E00)) || !math.IsInf(halfToFloat(0xFC00), -1) {
		t.Error("NaN and -Inf do not come back")
	}
	if halfToFloat(0x8000) != 0 || !math.Signbit(halfToFloat(0x8000)) {
		t.Error("minus zero")
	}

	// Fields of type hfloat and the writes of them
	m := &mem{b: []byte{0x00, 0x3C, 0x00, 0xC0, 0, 0}}
	p, _ := Compile("h", `hfloat a; hfloat b; hfloat c; c = a - b * 2; Printf("%g %g %g", a, b, c);`)
	res := p.Run(context.Background(), Env{Data: m, Writer: m})
	if res.Err != nil || res.Output[0] != "1 -2 5" || !bytes.Equal(m.b[4:], []byte{0x00, 0x45}) {
		t.Fatalf("%v %q % x", res.Err, res.Output, m.b)
	}
	if v := res.Root.Find("b").Value(); v != "-2" {
		t.Fatalf("hfloat value %q", v)
	}
}
