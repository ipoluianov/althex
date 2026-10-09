package tmpl

import (
	"bytes"
	"context"
	"encoding/binary"
	"strings"
	"testing"
	"time"
)

// mem is data in memory that can be written, for the tests
type mem struct{ b []byte }

func (m *mem) Size() int64 { return int64(len(m.b)) }
func (m *mem) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(m.b)) {
		return 0, nil
	}
	return copy(p, m.b[off:]), nil
}
func (m *mem) Overwrite(off int64, b []byte) error { copy(m.b[off:], b); return nil }
func (m *mem) Insert(off int64, b []byte) error {
	m.b = append(m.b[:off:off], append(b, m.b[off:]...)...)
	return nil
}
func (m *mem) Delete(off, n int64) error { m.b = append(m.b[:off:off], m.b[off+n:]...); return nil }

func run(t *testing.T, src string, data []byte) *Result {
	t.Helper()
	p, err := Compile("test", src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	m := &mem{b: data}
	res := p.Run(context.Background(), Env{Data: m, Writer: m})
	return res
}

func mustRun(t *testing.T, src string, data []byte) *Result {
	t.Helper()
	res := run(t, src, data)
	if res.Err != nil {
		t.Fatalf("run: %v", res.Err)
	}
	return res
}

func value(t *testing.T, res *Result, path string) string {
	t.Helper()
	n := res.Root.Find(path)
	if n == nil {
		t.Fatalf("no field %s", path)
	}
	return n.Value()
}

func le32(v uint32) []byte { b := make([]byte, 4); binary.LittleEndian.PutUint32(b, v); return b }

func TestStructsAndArrays(t *testing.T) {
	data := append([]byte("BM"), le32(1234)...)
	data = append(data, 1, 2, 3, 4)
	res := mustRun(t, `
		struct HDR {
			char magic[2];
			uint32 size <format=hex>;
		};
		HDR hdr;
		uchar rest[FileSize() - FTell()];
	`, data)
	if v := value(t, res, "hdr.magic"); v != `"BM"` {
		t.Fatalf("magic %s", v)
	}
	if v := value(t, res, "hdr.size"); v != "0x000004D2" {
		t.Fatalf("size %s", v)
	}
	rest := res.Root.Find("rest")
	if rest.ChildCount() != 4 || rest.Child(2).Value() != "3" {
		t.Fatalf("rest %v %v", rest.ChildCount(), rest.Child(2).Value())
	}
	if n := res.Root.NodeAt(4); n == nil || n.Name != "size" {
		t.Fatalf("node at 4: %+v", n)
	}
	if n := res.Root.NodeAt(8); n == nil || n.Name != "rest[2]" {
		t.Fatalf("node at 8: %+v", n)
	}
}

func TestRepeatedFieldsAndConditions(t *testing.T) {
	// Chunks: a length byte, a type byte, data
	data := []byte{2, 'A', 1, 2, 0, 'B', 3, 'C', 7, 8, 9}
	res := mustRun(t, `
		struct CHUNK {
			uchar len;
			char type;
			if (len > 0)
				uchar data[len];
		};
		while (!FEof())
			CHUNK chunk;
		local int n = 0;
		local int i;
		for (i = 0; i < 3; i++) {
			if (exists(chunk[i].data)) n++;
		}
		Printf("%d chunks with data", n);
	`, data)
	if v := value(t, res, "chunk[2].type"); v != "67 'C'" {
		t.Fatalf("type %s", v)
	}
	if res.Output[0] != "2 chunks with data" {
		t.Fatalf("output %q", res.Output)
	}
	if res.Root.Find("chunk").ChildCount() != 3 {
		t.Fatal("not 3 chunks")
	}
}

func TestEnumsBitsUnionsPlacement(t *testing.T) {
	data := []byte{0x05, 0xA3, 0x10, 0x00, 0x00, 0x00, 0xEF, 0xBE, 0xAD, 0xDE, 0x34, 0x12}
	res := mustRun(t, `
		enum <uchar> KIND { NONE, ONE, FIVE = 5 };
		KIND kind;
		uchar low : 4;
		uchar high : 4;
		uint32 ptr;
		union U { uint32 whole; uint16 halves[2]; } u @ ptr - 10;
		uint16 last;
		BigEndian();
		uint16 be @ 10;
	`, data)
	if v := value(t, res, "kind"); v != "FIVE (5)" {
		t.Fatalf("enum %s", v)
	}
	if v := value(t, res, "low"); v != "3" {
		t.Fatalf("low bits %s", v)
	}
	if v := value(t, res, "high"); v != "10" {
		t.Fatalf("high bits %s", v)
	}
	if v := value(t, res, "u.whole"); v != "3735928559" {
		t.Fatalf("union whole %s", v)
	}
	u := res.Root.Find("u")
	if u.Off != 6 || u.Size != 4 {
		t.Fatalf("union at %d size %d", u.Off, u.Size)
	}
	// The union is placed at 6, where the position is anyway; it stays there
	if v := value(t, res, "last"); v != "48879" {
		t.Fatalf("after a placed field the position stays: %s", v)
	}
	if v := value(t, res, "be"); v != "13330" {
		t.Fatalf("big-endian %s", v)
	}
}

func TestFunctionsAndAttributes(t *testing.T) {
	data := []byte{3, 'a', 'b', 'c', 0, 'x', 'y', 0}
	res := mustRun(t, `
		string Show(uchar &n) { return Str("len=%d", n); }
		int Twice(int x) { return x * 2; }
		int Fact(int n) { if (n <= 1) return 1; return n * Fact(n - 1); }
		uchar n <read=Show, comment="the length">;
		char text[n];
		uchar zero;
		string s;
		Printf("%d %d", Twice(21), Fact(5));
		local string t = "ab" + "cd";
		Printf("%s %d %s", t, Strlen(t), SubStr(t, 1, 2));
	`, data)
	if v := value(t, res, "n"); v != "len=3" {
		t.Fatalf("read: %s", v)
	}
	if c := res.Root.Find("n").Comment; c != "the length" {
		t.Fatalf("comment %q", c)
	}
	if v := value(t, res, "s"); v != `"xy"` {
		t.Fatalf("string %s", v)
	}
	if res.Output[0] != "42 120" || res.Output[1] != "abcd 4 bc" {
		t.Fatalf("output %q", res.Output)
	}
}

func TestStructParamsSwitchTypedef(t *testing.T) {
	data := []byte{1, 0xAA, 0xBB, 2, 0x01, 0x02, 0x03, 0x04}
	res := mustRun(t, `
		typedef uchar BYTES4[4];
		typedef uint16 WORD16 <format=hex>;
		struct REC(int kind) {
			switch (kind) {
			case 1:
				WORD16 w;
				break;
			case 2:
				BYTES4 b;
				break;
			default:
				Warning("unknown");
			}
		};
		uchar k1;
		REC r1(k1);
		uchar k2;
		REC r2(k2);
	`, data)
	if v := value(t, res, "r1.w"); v != "0xBBAA" {
		t.Fatalf("w %s", v)
	}
	if n := res.Root.Find("r2.b"); n == nil || n.ChildCount() != 4 {
		t.Fatal("typedef array")
	}
}

func TestWrites(t *testing.T) {
	data := []byte{1, 2, 3, 4, 0, 0, 0, 0}
	p, err := Compile("w", `
		uint32 a;
		uint32 sum;
		sum = a + 1;
		WriteUByte(0, 0xFF);
	`)
	if err != nil {
		t.Fatal(err)
	}
	m := &mem{b: data}
	res := p.Run(context.Background(), Env{Data: m, Writer: m})
	if res.Err != nil || !res.Wrote {
		t.Fatalf("%v %v", res.Err, res.Wrote)
	}
	if !bytes.Equal(m.b, []byte{0xFF, 2, 3, 4, 2, 2, 3, 4}) {
		t.Fatalf("% x", m.b)
	}
	// Without a writer a write is an error
	res = p.Run(context.Background(), Env{Data: &mem{b: data}})
	if res.Err == nil || !strings.Contains(res.Err.Error(), "read-only") {
		t.Fatalf("a write without a writer: %v", res.Err)
	}
}

func TestErrorsKeepFields(t *testing.T) {
	res := run(t, `
		uint16 a;
		uint32 b;
	`, []byte{1, 0, 2})
	if res.Err == nil || !strings.Contains(res.Err.Error(), "line 3") {
		t.Fatalf("error %v", res.Err)
	}
	if res.Root.Find("a") == nil {
		t.Fatal("the field read before the error is lost")
	}
	for _, src := range []string{
		`uint32 x`,                // no ;
		`struct { int a; } b; b.`, // bad member
		`int f(int a) { return a; } f(1, 2);`,
		`unknowntype x;`,
	} {
		p, err := Compile("bad", src)
		if err == nil {
			if res := p.Run(context.Background(), Env{Data: &mem{b: make([]byte, 8)}}); res.Err == nil {
				t.Errorf("no error for %q", src)
			}
		}
	}
}

func TestLimits(t *testing.T) {
	p, err := Compile("loop", `while (1) {}`)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	res := p.Run(context.Background(), Env{Data: &mem{}, MaxSteps: 100000})
	if res.Err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("an endless loop: %v", res.Err)
	}
	p, _ = Compile("deep", `int f(int n) { return f(n + 1); } f(0);`)
	if res := p.Run(context.Background(), Env{Data: &mem{}}); res.Err == nil {
		t.Fatal("endless recursion")
	}
	p, _ = Compile("many", `while (!FEof()) uchar b;`)
	if res := p.Run(context.Background(), Env{Data: &mem{b: make([]byte, 5000)}, MaxNodes: 1000}); res.Err == nil {
		t.Fatal("too many fields")
	}
	// A huge simple array is one field
	p, _ = Compile("big", `uchar all[FileSize()];`)
	if res := p.Run(context.Background(), Env{Data: &mem{b: make([]byte, 1<<20)}, MaxNodes: 10}); res.Err != nil {
		t.Fatal(res.Err)
	}
}

func TestTransformsAndConsole(t *testing.T) {
	p, err := Compile("fn", `
		uint32 head;
		void Xor(int64 start, int64 size) <transform="XOR 0x5A"> {
			local int64 i;
			for (i = 0; i < size; i++) WriteUByte(start + i, ReadUByte(start + i) ^ 0x5A);
		}
		uint32 Sum(int64 start, int64 size) <checksum="Sum"> {
			local uint32 s = 0;
			local int64 i;
			for (i = 0; i < size; i++) s += ReadUByte(start + i);
			return s;
		}
	`)
	if err != nil {
		t.Fatal(err)
	}
	fs := p.Functions()
	if len(fs) != 2 || fs[0].Title != "Sum" || fs[1].Kind != "transform" {
		t.Fatalf("functions %+v", fs)
	}
	m := &mem{b: []byte{1, 2, 3, 4}}
	env := Env{Data: m, Writer: m}
	last := p.Run(context.Background(), env)
	v, _, err := p.Call(context.Background(), env, last, "Sum", Int(0), Int(4))
	if n, _ := v.AsInt(); err != nil || n != 10 {
		t.Fatalf("sum %v %v", v.Text(), err)
	}
	if _, _, err := p.Call(context.Background(), env, last, "Xor", Int(0), Int(2)); err != nil || m.b[0] != 0x5B {
		t.Fatalf("xor %v % x", err, m.b)
	}

	cn := NewConsole()
	ans, _, err := cn.Eval(context.Background(), env, p, last, "head + 1")
	if err != nil || !strings.HasPrefix(ans, "67305986") {
		t.Fatalf("console: %q %v", ans, err)
	}
	if _, _, err := cn.Eval(context.Background(), env, p, last, "local int x = 5;"); err != nil {
		t.Fatal(err)
	}
	if ans, _, _ := cn.Eval(context.Background(), env, p, last, "x * 2"); !strings.HasPrefix(ans, "10") {
		t.Fatalf("a variable kept between lines: %q", ans)
	}
	if ans, _, err := cn.Eval(context.Background(), env, p, last, `Checksum("crc32", 0, 4)`); err != nil || ans == "" {
		t.Fatalf("checksum %q %v", ans, err)
	}
}

func TestMeta(t *testing.T) {
	p, err := Compile("png.bt", `//! name: PNG image
//! extensions: png, apng
//! magic: 0 "89 50 4E 47 ?? 0A"
//! magic: -4 "AE 42 60 82"
uchar sig[8];`)
	if err != nil {
		t.Fatal(err)
	}
	m := p.Meta()
	if m.Name != "PNG image" || len(m.Extensions) != 2 || len(m.Magic) != 2 {
		t.Fatalf("%+v", m)
	}
	if !m.Matches(&mem{b: []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0, 0}}) {
		t.Fatal("the magic does not match")
	}
	if !m.Matches(&mem{b: []byte{0, 0, 0xAE, 0x42, 0x60, 0x82}}) {
		t.Fatal("the magic at the end does not match")
	}
	if m.Matches(&mem{b: []byte("GIF89a")}) {
		t.Fatal("a GIF matches")
	}
}

func FuzzCompile(f *testing.F) {
	f.Add(`struct A { uint32 x; if (x) uchar d[x]; } a; while(!FEof()) uchar b;`)
	f.Add(`enum <uchar> E { A, B = 3 } e; local int i; for (i=0;i<3;i++) Printf("%d", i);`)
	f.Fuzz(func(t *testing.T, src string) {
		p, err := Compile("fuzz", src)
		if err != nil {
			return
		}
		p.Run(context.Background(), Env{Data: &mem{b: []byte{1, 2, 3, 4, 5, 6, 7, 8}}, MaxSteps: 10000, MaxNodes: 1000})
	})
}

func TestFixesFromTemplates(t *testing.T) {
	data := []byte{0x21, 'I', 'H', 'D', 'R', 0, 'x', 0xFF, 0xD8, 0xFF}
	res := mustRun(t, `
		uchar lo : 4 <comment="low">;
		uchar hi : 4 <format=hex>;
		char type[4];
		if (type == "IHDR") Printf("ok %s", type);
		string s;
		Printf("%s|%s", Str("%s", type), s);
		Printf("%d", FindHex("FF D8 ??", 0));
		Printf("%d", FindHex("AA", 0));
		uchar tail @ 2 + 1 <format=hex>;
	`, data)
	if res.Root.Find("lo").Comment != "low" {
		t.Fatal("an attribute after a bit field")
	}
	want := []string{"ok IHDR", "IHDR|", "7", "-1"}
	for i, w := range want {
		if i >= len(res.Output) || res.Output[i] != w {
			t.Fatalf("output %q, want %q", res.Output, want)
		}
	}
	if v := value(t, res, "tail"); v != "0x44 'D'" {
		t.Fatalf("tail %s", v)
	}
}

func TestFixesFromTemplates2(t *testing.T) {
	data := []byte{1, 2, 3, 4, 5, 6, 7, 8, 'a', 'b', 'c', 'd', 0, 0xB4}
	res := mustRun(t, `
		struct S { uchar a[4]; };
		struct T { uchar b; };
		S s; S s;
		T t;
		Printf("%d %d %s", sizeof(s), t[0].b, ReadString(8, 2));
		FSeek(13);
		BitfieldLeftToRight();
		uchar hi : 4;
		uchar lo : 4;
		ULONGLONG dummy @ 0;
	`, data)
	if res.Output[0] != "8 97 ab" {
		t.Fatalf("output %q", res.Output)
	}
	if res.Root.Find("t[0].b") == nil {
		t.Fatal("t[0] of a single field")
	}
	if value(t, res, "hi") != "11" || value(t, res, "lo") != "4" {
		t.Fatalf("bits %s %s", value(t, res, "hi"), value(t, res, "lo"))
	}
}

func TestFixesFromTemplates3(t *testing.T) {
	data := []byte{'a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'x', 0xFF, 0xFE, 0}
	res := mustRun(t, `
		struct S;
		string Show(S &s) { return Str("%d", s.v); }
		struct S(int k) { uchar v; } <read=Show>;
		typedef char FOURCC[4];
		FOURCC names[2];
		S s(1) <comment="with arguments">;
		Printf("%d %s", Strlen(ReadString(9, 2)), names[1]);
	`, data)
	if res.Root.Find("s").Comment != "with arguments" || value(t, res, "s") != "120" {
		t.Fatalf("s: %q %q", res.Root.Find("s").Comment, value(t, res, "s"))
	}
	if res.Output[0] != "2 efgh" {
		t.Fatalf("output %q", res.Output)
	}
}
