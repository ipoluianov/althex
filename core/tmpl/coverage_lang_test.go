package tmpl

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestCoverageLexer(t *testing.T) {
	got := covOut(t, `
		/* a block
		   comment */ local int a = 0x1F; // a line comment
		local int b = 0b101, c = 017, d = 1_000;
		local uint64 big = 0xFFFFFFFFFFFFFFFF;
		Printf("%d %d %d %d %u", a, b, c, d, big);
		Printf("%g %g %g %g", 1.5f, 2e3, .25, 1E-2);
		Printf("%d %d %d", 10u, 10L, 10UL);
		Printf("%d %d %X %d", 'A', '\n', 'IHDR', 'ж');
		Printf("%d", Strlen("a\tb\0c"));
		Printf("%d %d %d %d %d %d %d", '\r', '\a', '\b', '\f', '\v', '\x41', '\q');
		Printf("%s", "con" "cat");
		Printf("%d", '\\');
	`, nil)
	want := strings.Join([]string{
		"31 5 15 1000 18446744073709551615",
		"1.5 2000 0.25 0.01",
		"10 10 10",
		"65 10 49484452 1078",
		"5",
		"13 7 8 12 11 65 113",
		"concat",
		"92",
	}, "|")
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	for _, c := range []struct{ src, want string }{
		{`/* open`, "the comment is not closed"},
		{`local int a = 'ab cd efgh';`, "a character constant of one character expected"},
		{"local int a = 1; `", "unexpected character '`'"},
		{`local float f = 1.2.3;`, `bad number "1.2.3"`},
		{`local int a = 0x;`, `bad number "0x"`},
		{`local int a = 099;`, `bad number "099"`},
		{`local string s = "open`, "the string is not closed"},
		{"local string s = \"line\nbreak\";", "the string is not closed"},
		{`local string s = "\`, "the string is not closed"},
		{`local string s = "\xZZ";`, `\x without hex digits`},
	} {
		covCompileErr(t, c.src, c.want)
	}
	// An error of the lexer has its place
	_, err := Compile("bad", "\n\n   `")
	if e, ok := err.(*Error); !ok || e.Pos != (Pos{3, 4}) || e.Error() != "line 3:4: unexpected character '`'" {
		t.Fatalf("%#v", err)
	}
	if s := (&Error{Pos: Pos{1, 2}, Msg: "m", Path: "a.b"}).Error(); s != "line 1:2: m (a.b)" {
		t.Fatalf("error with a path %q", s)
	}
}

func TestCoverageTypeWords(t *testing.T) {
	data := bytes.Repeat([]byte{0xFF}, 40)
	res := mustRun(t, `
		unsigned char a;
		signed char b;
		unsigned short c;
		unsigned d;
		unsigned long long e;
		signed long long f;
		const unsigned int g;
		long long h;
		unsigned float i;
	`, data)
	want := map[string][2]string{
		"a": {"uchar", "255"}, "b": {"char", "-1"}, "c": {"uint16", "65535"}, "d": {"uint32", "4294967295"},
		"e": {"uint64", "18446744073709551615"}, "f": {"int64", "-1"}, "g": {"uint32", "4294967295"}, "h": {"int64", "-1"},
	}
	for name, w := range want {
		n := res.Root.Find(name)
		if n == nil || n.TypeName != w[0] || n.Value() != w[1] {
			t.Errorf("%s: %+v %q, want %v", name, n, n.Value(), w)
		}
	}
	if n := res.Root.Find("i"); n == nil || n.TypeName != "float" {
		t.Errorf("unsigned float: %+v", n)
	}
	covCompileErr(t, `typedef uchar B; unsigned B x;`, "unsigned B is not a type")
	covCompileErr(t, `unsigned string x;`, "")
	covCompileErr(t, `typedef 5 X;`, `a type expected, a number found`)
	covCompileErr(t, `local int a = "x" 5;`, `";" expected, a number found`)
	covCompileErr(t, `local int a = 1`, `";" expected, the end found`)
}

func TestCoverageCastsAndSizeof(t *testing.T) {
	data := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	got := covOut(t, `
		typedef uchar ID[4];
		typedef ID ID2;
		enum <uint16> E { X };
		struct S { uchar a; uchar b[a]; };
		typedef float REAL;
		Printf("%d %d %d %d %d %d", sizeof(ID), sizeof(uint32), sizeof(E), sizeof(double), sizeof(ID2), sizeof(REAL));
		S s;
		Printf("%d %d %d %d", sizeof(s), sizeof("abc"), sizeof(1.5), sizeof(7));
		Printf("%d %d %d %u", (uchar)300, (char)200, (int16)0x18000, (uint32)-1);
		Printf("%d %d %d", (uchar)300.7, (char)-1.9, (uint16)-5.0);
		Printf("%g %g %d", (float)0.1 == 0.1, (double)3, (unsigned char)-1);
		Printf("%s|%s", (string)42, (string)"x");
		Printf("%d", (E)0x10001);
		Printf("%g", (hfloat)1.25);
		Printf("%d", (S)5);
	`, data)
	want := strings.Join([]string{
		"4 4 2 8 4 4",
		"2 3 8 8",
		"44 -56 -32768 4294967295",
		"44 -1 0",
		"0 3 255",
		"42 (0x2A)|x",
		"1",
		"1.25",
		"5",
	}, "|")
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	covErr(t, `struct S { uchar a; }; Printf("%d", sizeof(S));`, data, "the size of S is known only when it is read")
	covErr(t, `void f() {} Printf("%d", sizeof(f()));`, data, "sizeof of nothing")
	covErr(t, `Printf("%d", sizeof(nosuch));`, data, `unknown name "nosuch"`)
	covCompileErr(t, `local int x = (nosuch)1;`, "")
}

func TestCoverageOperators(t *testing.T) {
	got := covOut(t, `
		local int a = 7, b = 2;
		local uint32 u = 0xFFFFFFF0;
		local double f = 2.5;
		Printf("%d %d %d %d %d", a + b, a - b, a * b, a / b, a % b);
		Printf("%d %d %d %d %d", a & b, a | b, a ^ b, a << b, a >> 1);
		Printf("%d %d %d", -a, ~a, !a);
		Printf("%d %d %d", -8 >> 1, u >> 4, (uint64)-1 >> 60);
		Printf("%u %u", u / 3, u % 7);
		Printf("%d %d", -7 / 2, -7 % 2);
		Printf("%g %g %g %g %g", f + 1, f - 1, f * 2, f / 2, f % 1);
		Printf("%g %g", -f, +f);
		Printf("%d %d %d %d %d %d", 1.5 < 2, 2 > 1.5, 2.0 == 2, 1.0 != 1, 3 <= 3.0, 3 >= 3.5);
		Printf("%d %d", u > 1, -1 < (uint32)1);
		Printf("%d %d %d", "a" < "b", "b" == "b", "a" >= "b");
		Printf("%d %d %d", a > b ? 10 : 20, 0 ? 1 : 0 ? 2 : 3, (a, b));
		Printf("%s %s %s", "n=" + 5, 1.5 + "x", (uint32)4 + "");
		a += 3; b -= 1; Printf("%d %d", a, b);
		a *= 2; a /= 4; a %= 3; Printf("%d", a);
		a = 6; a &= 3; a |= 8; a ^= 1; a <<= 2; a >>= 1; Printf("%d", a);
		local int i = 5;
		Printf("%d %d %d %d", i++, i, ++i, i--);
		Printf("%d %d", --i, i);
		Printf("%d %d %d %d", 1 && 0, 0 || 2, true, false);
		Printf("%d %d", !"", !1.5);
		Printf("%d", +"x" == "x");
	`, nil)
	want := strings.Join([]string{
		"9 5 14 3 1",
		"2 7 5 28 3",
		"-7 -8 0",
		"-4 268435455 15",
		"1431655760 2",
		"-3 -1",
		"3.5 1.5 5 1.25 0.5",
		"-2.5 2.5",
		"1 1 1 0 1 0",
		"1 0",
		"1 1 0",
		"10 3 2",
		"n=5 1.5x 4",
		"10 1",
		"2",
		"22",
		"5 6 7 7",
		"5 5",
		"0 1 1 0",
		"1 0",
		"1",
	}, "|")
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", strings.ReplaceAll(got, "|", "\n"), strings.ReplaceAll(want, "|", "\n"))
	}
	for _, c := range []struct{ src, want string }{
		{`local int x = 1 / 0;`, "division by zero"},
		{`local int x = 1 % 0;`, "division by zero"},
		{`local uint32 x = 1; x = x / 0;`, "division by zero"},
		{`local double x = 1.5 & 1;`, "& of floats"},
		{`local double x = 1.5 << 1;`, "<< of floats"},
		{`local int x = "a" - 1;`, "a string - a number"},
		{`local int x = "a" < 1;`, "a string < a number"},
		{`local int x = -"a";`, "- of a string"},
		{`local int x = ~1.5;`, "~ of a float"},
		{`struct S { uchar a; } s; local int x = s < 1;`, "a struct or an array < a number"},
		{`struct S { uchar a; } s; local int x = s + 1;`, "a struct or an array + a number"},
		{`void f() {} local int x = f() + 1;`, "nothing + a number"},
		{`void f() {} local int x = f() < 1;`, "nothing < a number"},
	} {
		covErr(t, c.src, []byte{1}, c.want)
	}
	// Two fields compare equal only to themselves
	if got := covOut(t, `struct S { uchar a; }; S x; S y; Printf("%d %d %d", x == x, x == y, x != y);`, []byte{1, 1}); got != "1 0 1" {
		t.Fatalf("node compare %q", got)
	}
}

func TestCoverageStatements(t *testing.T) {
	got := covOut(t, `
		local int i = 0, n = 0;
		do { n += i; i++; } while (i < 4);
		Printf("%d", n);
		do i--; while (0);
		Printf("%d", i);
		for (;;) { if (++i > 6) break; }
		Printf("%d", i);
		for (i = 0; i < 5; i++) { if (i % 2) continue; n++; }
		Printf("%d", n);
		while (i > 0) { i--; if (i == 2) break; }
		Printf("%d", i);
		for (i = 0; i < 3; i++);
		while (0);
		;
		{ local int inner = 1; n += inner; }
		Printf("%d", n);
		switch (n) {
		case 1:
		case 10:
			Printf("ten");
		case 11:
			Printf("eleven");
			break;
		default:
			Printf("default");
		}
		switch (99) { case 1: Printf("one"); }
		switch ("b") { case "a": Printf("a"); break; default: Printf("not a"); case "c": Printf("c"); }
		if (0) Printf("no"); else if (1) Printf("else if"); else Printf("no");
		if (1) ; else Printf("no");
	`, nil)
	want := "6|3|7|9|2|10|ten|eleven|not a|c|else if"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	// return inside loops and switches leaves the function
	got = covOut(t, `
		int F(int k) {
			local int i;
			for (i = 0; ; i++) { if (i == k) return i * 10; }
		}
		int G(int k) { while (1) { do { return k + 1; } while (1); } }
		int H(int k) { switch (k) { case 1: return 100; default: return 200; } }
		void V() { return; }
		Printf("%d %d %d %d", F(3), G(4), H(1), H(2));
		V();
	`, nil)
	if got != "30 5 100 200" {
		t.Fatalf("returns %q", got)
	}
	for _, c := range []struct{ src, want string }{
		{`do i++; until (1);`, `"while" expected`},
		{`do { } while 1;`, `"(" expected`},
		{`do { } while (1)`, `";" expected`},
		{`switch (1) { 5; }`, `"case" expected`},
		{`switch (1) { case 1: `, "the switch is not closed"},
		{`switch (1) { case 1 Printf("x"); }`, `":" expected`},
		{`switch (1) { default Printf("x"); }`, `":" expected`},
		{`if (1 { }`, `")" expected`},
		{`if 1 {}`, `"(" expected`},
		{`for (i = 0 i < 1; i++) {}`, `";" expected`},
		{`for (i = 0; i < 1 i++) {}`, `";" expected`},
		{`for (i = 0; i < 1; i++ {}`, `")" expected`},
		{`for i {}`, `"(" expected`},
		{`while (1) local int;`, "a name expected"},
		{`{ local int a; `, "the block is not closed"},
		{`if (1) {} else {`, "the block is not closed"},
		{`break`, `";" expected`},
		{`int f() { return 1 }`, `";" expected`},
		{`local int a = ;`, "a value expected"},
		{`local int a = (1;`, `")" expected`},
		{`local int a = b[1;`, `"]" expected`},
		{`local int a = b.;`, "a name expected"},
		{`local int a = f(1;`, `")" expected`},
		{`local int a = 1 ? 2;`, `":" expected`},
		{`local int a = sizeof 1;`, `"(" expected`},
		{`local int a = sizeof(1;`, `")" expected`},
	} {
		covCompileErr(t, c.src, c.want)
	}
}

func TestCoverageEnums(t *testing.T) {
	data := []byte{2, 0x10, 0, 7, 0xFF}
	res := mustRun(t, `
		enum COLOR { RED, GREEN, BLUE };
		enum <uint16> FLAGS { F_A = 1 << 4, F_B = F_A | 1, F_C };
		enum <uchar> { ANON1 = 7, ANON2 } anon @ 3;
		typedef enum <char> { NEG = -1 } SIGNED_E;
		typedef uchar U8;
		enum <U8> E8 { E8_A = 2 };
		E8 e8;
		FLAGS flags;
		FSeek(4);
		SIGNED_E s;
		Printf("%d %d %d %d %d %d", RED, BLUE, F_A, F_B, F_C, NEG);
		Printf("%d %d", anon == ANON1, s == NEG);
		local COLOR c = GREEN;
		Printf("%d %s", c, EnumToString(e8));
		local int RED = 100;
		Printf("%d", RED);
	`, data)
	if got := strings.Join(res.Output, "|"); got != "0 2 16 17 18 -1|1 1|1 E8_A|100" {
		t.Fatalf("output %q", got)
	}
	if v := value(t, res, "e8"); v != "E8_A (2)" {
		t.Fatalf("e8 %q", v)
	}
	if v := value(t, res, "flags"); v != "F_A (16)" {
		t.Fatalf("flags %q", v)
	}
	if v := value(t, res, "s"); v != "NEG (-1)" {
		t.Fatalf("s %q", v)
	}
	// An enum value without a name: the number only, no character
	res = mustRun(t, `enum <uchar> E { A }; E x <format=hex>;`, []byte{'z'})
	if v := value(t, res, "x"); v != "0x7A" {
		t.Fatalf("x %q", v)
	}
	// The names of an enum by their values
	// Constant expressions that use the functions that need no data
	p, err := Compile("e", `enum E { K1 = FileSize(), K2 = FEof() + Strlen("abc"), K3 = FindHex("00", 0), K4 = Checksum("crc32", 0, 0) };
		Printf("%d %d %d %d", K1, K2, K3, K4);`)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(p.Run(context.Background(), Env{Data: &mem{}}).Output, "|"); got != "0 4 -1 0" {
		t.Fatalf("constants %q", got)
	}
	if _, err := Compile("e", `enum E { A = Printf("x") };`); err == nil || !strings.Contains(err.Error(), "not a number") {
		t.Fatalf("Printf in an enum: %v", err)
	}
	p, err = Compile("e", `enum <uchar> E { C = 3, A = 1, B = 2 };`)
	if err != nil {
		t.Fatal(err)
	}
	if names := p.types["E"].sortedNames(); strings.Join(names, ",") != "A,B,C" {
		t.Fatalf("sorted names %q", names)
	}

	for _, c := range []struct{ src, want string }{
		{`enum <float> E { A };`, "an enum is of an integer type"},
		{`enum <nosuch> E { A };`, "a type expected"},
		{`enum E e;`, `unknown type "E"`},
		{`enum E { A = "x" };`, "the value of A is not a number"},
		{`enum E { A = 1 / 0 };`, "the value of A: line"},
		// Enum values are computed with no data: what reads it fails, not crashes
		{`enum E { A = ReadInt(0) };`, "the value of A: line 1:14: reading 4 bytes at 0x0 goes past the end"},
		{`enum E { A = ReadString(1) };`, "past the end"},
		{`enum E { A = Checksum("crc32", 0, 1) };`, "out of the data"},
		{`enum E { A = Exit(1) };`, "the value of A is not a number"},
		{`enum E { A = InsertBytes(0, 1) };`, "read-only"},
		{`enum E { A = nosuch };`, "the value of A"},
		{`enum E { A, };  enum F { 5 };`, "a name expected"},
		{`enum E { A B };`, `"}" expected`},
		{`enum <int E { A };`, `">" expected`},
		{`enum <5> E { A };`, "a type expected"},
		{`enum E { A = };`, "a value expected"},
		{`struct S { int a; }; struct S { int b; };`, "S is defined twice"},
		{`enum S { A }; struct S { int b; };`, "S is a type already"},
	} {
		covCompileErr(t, c.src, c.want)
	}
}

func TestCoverageUnionsAndStructs(t *testing.T) {
	data := []byte{0x78, 0x56, 0x34, 0x12, 0xAA, 0xBB}
	res := mustRun(t, `
		union U {
			uint32 dw;
			uint16 w[2];
			uchar b;
			struct { uchar lo; uchar hi; } parts;
		} u;
		struct {
			uchar x;
		} anon;
		struct P(uchar n, uchar &ref, string tag) {
			uchar v[n];
			local string t = tag;
		} <comment="params">;
		FSeek(0);
		P p(2, u.b, "tag") @ 4;
		struct EMPTY { };
		EMPTY e;
		Printf("%d %d %x %x", u.b, u.parts.hi, u.w[1], startof(u.parts));
		Printf("%d %d", sizeof(u), startof(anon));
	`, data)
	if got := strings.Join(res.Output, "|"); got != "120 86 1234 0|4 4" {
		t.Fatalf("output %q", got)
	}
	u := res.Root.Find("u")
	if u.Size != 4 || u.Kind != NodeStruct || res.Root.Find("u.parts").Size != 2 {
		t.Fatalf("union %+v", u)
	}
	if p := res.Root.Find("p"); p.Off != 4 || p.Size != 2 || p.Comment != "params" {
		t.Fatalf("p %+v", p)
	}
	if e := res.Root.Find("e"); e.Size != 0 {
		t.Fatalf("an empty struct %+v", e)
	}

	for _, c := range []struct{ src, want string }{
		{`struct S; S s;`, "the struct S is declared but not defined"},
		{`struct S(int a) { uchar v; }; S s;`, "S gets 1 arguments, 0 given"},
		{`struct S { S inner; }; S s;`, "nested too deep"},
		{`local int a; a.b = 1;`, "a number has no field b"},
		{`struct S { uchar v; } s; s.w = 1;`, "s has no field w"},
		{`struct S { uchar v; } s; local int x = s.w;`, "s has no field w"},
		{`struct S { uchar v; } s; s = 1;`, "only a field of a simple type can be assigned"},
		{`struct S { uchar v; }; local S x;`, "a local variable of a struct type"},
		{`void x;`, "a field of type void"},
		{`local int x = startof(5);`, "startof needs a field"},
	} {
		covErr(t, c.src, data, c.want)
	}
	covCompileErr(t, `local int x = startof(1, 2);`, "startof takes one argument")
	covCompileErr(t, `local int x = exists(1, 2);`, "exists takes one argument")
	covCompileErr(t, `uchar x = 5;`, "only a local variable has a value given")
	covCompileErr(t, `struct S(int) { };`, "a name expected")
	covCompileErr(t, `struct S(int a[) { };`, `"]" expected`)
	covCompileErr(t, `struct S(int a { };`, `")" expected`)
	covCompileErr(t, `struct S { enum E x; };`, `unknown type "E"`)
	covCompileErr(t, `struct S { uchar a; } <format=>;`, "a value expected")
	covCompileErr(t, `struct S { uchar a } s;`, `";" expected`)
}

func TestCoverageLocalArrays(t *testing.T) {
	got := covOut(t, `
		local int arr[4];
		local int i;
		for (i = 0; i < 4; i++) arr[i] = i * i;
		arr[1] += 10;
		arr[2]++;
		Printf("%d %d %d %d", arr[0], arr[1], arr[2], arr[3]);
		local uchar small[2];
		small[0] = 300;
		Printf("%d", small[0]);
		local char s[] = "text";
		Printf("%s %d", s, Strlen(s));
		local char s2[8] = "abc";
		Printf("%s %d", s2, s2[1]);
		local string str = "xyz";
		Printf("%d %d", str[0], str[2]);
		local int none[0];
		local int empty[];
		Printf("%d", exists(none[0]));
		int Sum(int a[], int n) {
			local int k, t = 0;
			for (k = 0; k < n; k++) t += a[k];
			return t;
		}
		Printf("%d", Sum(arr, 4));
	`, nil)
	if got != "0 11 5 9|44|text 4|abc 98|120 122|0|25" {
		t.Fatalf("got %q", got)
	}
	for _, c := range []struct{ src, want string }{
		{`local int a[2]; a[2] = 1;`, "index 2 out of range [0, 2)"},
		{`local int a[2]; a[-1] = 1;`, "index -1 out of range [0, 2)"},
		{`local int a[2]; local int x = a[5];`, "index 5 out of range [0, 2)"},
		{`local int a[-1];`, "a local array of -1 elements"},
		{`local int a[1 << 30];`, "elements"},
		{`local int a[2]; a = 1;`, "cannot assign to the array a"},
		{`local int x; x[0] = 1;`, "not an array"},
		{`local int x; local int y = x[0];`, "a number is not an array"},
		{`local string s = "ab"; local int c = s[2];`, "index 2 out of the string of 2"},
		{`local string s = "ab"; local int c = s[-1];`, "index -1 out of the string of 2"},
		{`local int a[2]; local int c = a["x"];`, "a number expected, a string found"},
		{`nosuch = 1;`, `unknown name "nosuch"`},
		{`nosuch[0] = 1;`, `unknown name "nosuch"`},
		{`nosuch++;`, `unknown name "nosuch"`},
		{`nosuch += 1;`, `unknown name "nosuch"`},
		{`local int x = nosuch;`, `unknown name "nosuch"`},
		{`nosuch();`, "unknown function nosuch"},
	} {
		covErr(t, c.src, []byte{1}, c.want)
	}
	covCompileErr(t, `1 = 2;`, "cannot assign to this")
	covCompileErr(t, `(1 + 2)++;`, "cannot assign to this")
}

func TestCoverageFieldAssignments(t *testing.T) {
	m := &mem{b: []byte{0x21, 1, 2, 3, 4, 5, 0, 0, 'a', 'b', 0}}
	p, err := Compile("assign", `
		uchar lo : 4;
		uchar hi : 4;
		uchar arr[3];
		struct S { uchar a; uchar b; } s;
		uint16 w;
		string str;
		lo = 9;
		hi += 1;
		arr[1] = 0x77;
		arr[2]--;
		s.b = 0x55;
		BigEndian();
		w = 0x0102;
		Printf("%d %d %d %d %d %d", lo, hi, arr[1], arr[2], s.b, w);
	`)
	if err != nil {
		t.Fatal(err)
	}
	res := p.Run(context.Background(), Env{Data: m, Writer: m})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if !bytes.Equal(m.b[:8], []byte{0x39, 1, 0x77, 2, 4, 0x55, 2, 1}) {
		t.Fatalf("% x", m.b)
	}
	// w was read little-endian: it is written as it was read
	if res.Output[0] != "9 3 119 2 85 258" {
		t.Fatalf("output %q", res.Output)
	}
	covErr(t, `string s; s = "x";`, []byte{'a', 0}, "only a field of a simple type can be assigned")
	covErr(t, `uchar a[2]; a = 1;`, []byte{1, 2}, "only a field of a simple type can be assigned")
	covErr(t, `local string s = "ab"; s[0] = 1;`, []byte{1}, "not an array")
	covErr(t, `string s; s[0] = 1;`, []byte{'a', 0}, "cannot assign to this")
	covErr(t, `struct S { uchar v; } s; local int n = 1; n.v = 2;`, []byte{1}, "a number has no field v")
}

func TestCoverageBitfieldOrders(t *testing.T) {
	data := []byte{0xA3, 0xA3, 0xA3, 0x0F, 0x34, 0x12, 0x34, 0x12, 0xFF}
	res := mustRun(t, `
		BitfieldLeftToRight();
		uchar l1 : 4; uchar l2 : 4;
		BitfieldRightToLeft();
		uchar r1 : 4; uchar r2 : 4;
		BitfieldByteOrder();
		BigEndian();
		uchar b1 : 4; uchar b2 : 4;
		LittleEndian();
		char s1 : 4; char s2 : 4;
		uint16 w1 : 4; uint16 w2 : 12;
		uint16 x1 : 8; uint16 x2 : 4;
		uchar one : 3;
		uchar placed : 2 @ 0;
	`, data)
	want := map[string]string{
		"l1": "10", "l2": "3", "r1": "3", "r2": "10", "b1": "10", "b2": "3",
		"s1": "-1", "s2": "0", "w1": "4", "w2": "291", "x1": "52", "x2": "2", "one": "7", "placed": "3",
	}
	for name, w := range want {
		if v := value(t, res, name); v != w {
			t.Errorf("%s = %s, want %s", name, v, w)
		}
	}
	if n := res.Root.Find("w2"); n.TypeName != "uint16 : 12" || n.Off != 4 || n.Size != 2 {
		t.Errorf("w2 %+v", n)
	}
	// x2 does not fit after x1's... it does: 8 + 4 <= 16, both in the uint16 at 6
	if res.Root.Find("x2").Off != 6 || res.Root.Find("one").Off != 8 {
		t.Errorf("units: x2 at %d, one at %d", res.Root.Find("x2").Off, res.Root.Find("one").Off)
	}
	for _, c := range []struct{ src, want string }{
		{`float f : 3;`, "a bit field of a type that is not an integer"},
		{`struct S { uchar a; }; S s : 3;`, "a bit field of a type that is not an integer"},
		{`uchar a : 0;`, "a bit field of 0 bits in uchar"},
		{`uchar a : 9;`, "a bit field of 9 bits in uchar"},
		{`uchar a : "x";`, "a number expected"},
	} {
		covErr(t, c.src, data, c.want)
	}
}

func TestCoverageFunctions(t *testing.T) {
	m := &mem{b: []byte{5, 6, 'h', 'i', 0}}
	p, err := Compile("fn", `
		struct S { uchar a; uchar b; };
		S s;
		string str;
		int Bump(uchar &x) { x += 10; return x; }
		int Get(S &v) { return v.a + v.b; }
		int GetVal(S v) { return v.b; }
		uchar Trunc(int x) { return x; }
		string Name(string n) { return n + "!"; }
		int64 Big(uquad q) { return q; }
		S Same(S &v) { return v; }
		double Half(double d) { return d / 2; }
		int NoRet() { }
		Printf("%d %d %d %d", Bump(s.a), Get(s), GetVal(s), Trunc(0x1FF));
		Printf("%s %s %d", Name(str), Name(5), Big(-1));
		Printf("%d %g %d", Same(s).b, Half(3), NoRet());
		Printf("%d %d %d", function_exists(Get), function_exists("Name"), function_exists(Nope));
		Printf("%d %d", exists(s.a), exists(s.zz));
	`)
	if err != nil {
		t.Fatal(err)
	}
	res := p.Run(context.Background(), Env{Data: m, Writer: m})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if got := strings.Join(res.Output, "|"); got != "15 11 6 255|hi! 5! -1|6 1.5 0|1 1 0|1 0" {
		t.Fatalf("output %q", got)
	}
	for _, c := range []struct{ src, want string }{
		{`int f(int a) { return a; } f();`, "f takes 1 arguments, 0 given"},
		{`void f() { f(); } f();`, "calls nested too deep"},
	} {
		covErr(t, c.src, []byte{1}, c.want)
	}
	covCompileErr(t, `int f() { return 1; } int f() { return 2; }`, "the function f is defined twice")
	covCompileErr(t, `nosuch f() { }`, "")
	covCompileErr(t, `int f(enum E a) { }`, `unknown type "E"`)
	covCompileErr(t, `enum E f() { }`, `unknown type "E"`)
	covCompileErr(t, `int f() { enum E x; }`, `unknown type "E"`)
	covCompileErr(t, `int f() <transform="x" { }`, `">" expected`)
	covCompileErr(t, `int f() { return nosuch(; }`, "a value expected")

	// read= and comment= functions; a failing one leaves its error as the comment
	res = mustRun(t, `
		string R(uchar &x) { return Str("<%d>", x); }
		string C(uchar &x) { return Str("c%d", x * 2); }
		string Bad(uchar &x) { return Str("%d", x / 0); }
		uchar a <read=R, comment=C>;
		uchar b <comment=Bad>;
		uchar c <name="renamed", color=0x123456, bgcolor=cRed, hidden=false, open=true, fgcolor=1, format=dec>;
		uchar d <hidden=true, color=cNone>;
		uchar e <format=oct>;
		uchar f <format=binary>;
		uchar g <format=hex, comment="x" + "y">;
	`, []byte{3, 4, 5, 6, 8, 5, 0xAB})
	if v := value(t, res, "a"); v != "<3>" || res.Root.Find("a").Comment != "c6" {
		t.Fatalf("a %q %q", v, res.Root.Find("a").Comment)
	}
	if c := res.Root.Find("b").Comment; !strings.Contains(c, "division by zero") {
		t.Fatalf("b comment %q", c)
	}
	// name= changes the name shown; the program finds the field by its own.
	// bgcolor after color wins
	c := res.Root.Find("c")
	if c == nil || c.Name != "renamed" || c.Color != 0xFF0000|1<<24 || c.Hidden || !c.Open {
		t.Fatalf("c %+v", c)
	}
	if d := res.Root.Find("d"); !d.Hidden || d.Color != 0 {
		t.Fatalf("d %+v", d)
	}
	for _, ch := range res.Root.Children() {
		if ch.Name == "d" {
			t.Fatal("a hidden field among the children")
		}
	}
	if v := value(t, res, "e"); v != "010" {
		t.Fatalf("oct %q", v)
	}
	if v := value(t, res, "f"); v != "00000101" {
		t.Fatalf("bin %q", v)
	}
	if v := value(t, res, "g"); v != "0xAB" || res.Root.Find("g").Comment != "xy" {
		t.Fatalf("g %q", v)
	}
	covErr(t, `uchar a <format=weird>;`, []byte{1}, "format is hex, dec, oct or bin")
	covErr(t, `uchar a <read=Nope>;`, []byte{1}, "read= needs a function")
	covCompileErr(t, `uchar a <format=hex;`, `">" expected`)
	covCompileErr(t, `uchar a <5>;`, "a name expected")
	covCompileErr(t, `uchar a <color=1|>;`, "a value expected")
}

func TestCoverageFieldErrors(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{`uchar a[];`, "the length of the array a is not given"},
		{`uchar a[-1];`, "the array a has -1 elements"},
		{`typedef uchar A[-2]; A a;`, "the array a has -2 elements"},
		{`struct S { uchar v; }; S a[-1];`, "the array a has -1 elements"},
		{`uchar a[100];`, "goes past the end of the data"},
		{`uchar a["x"];`, "a number expected"},
		{`uchar a @ "x";`, "a number expected"},
		{`uint32 a;`, "goes past the end of the data"},
	} {
		covErr(t, c.src, []byte{1, 2}, c.want)
	}
	// Arrays of a typedef'd array: a list of arrays
	res := mustRun(t, `typedef uchar PAIR[2]; PAIR pairs[3]; typedef char NAME[2]; NAME n;`, []byte{1, 2, 3, 4, 5, 6, 'o', 'k'})
	pairs := res.Root.Find("pairs")
	if pairs.Kind != NodeList || pairs.ChildCount() != 3 || pairs.Child(2).Child(1).Value() != "6" {
		t.Fatalf("pairs %+v", pairs)
	}
	if v := value(t, res, "n"); v != `"ok"` {
		t.Fatalf("typedef char array %q", v)
	}
}

func TestCoverageNesting(t *testing.T) {
	deep := strings.Repeat("(", 2000) + "1" + strings.Repeat(")", 2000)
	covCompileErr(t, "local int x = "+deep+";", "nested too deep")
	chain := "local int x = 1" + strings.Repeat(" + 1", 2000) + ";"
	covCompileErr(t, chain, "nested too deep")
	comma := "local int x = (1" + strings.Repeat(", 1", 2000) + ");"
	covCompileErr(t, comma, "nested too deep")
	blocks := strings.Repeat("{", 2000) + strings.Repeat("}", 2000)
	covCompileErr(t, blocks, "nested too deep")
	idx := "local int x = a" + strings.Repeat("[0]", 2000) + ";"
	covCompileErr(t, idx, "nested too deep")
}

func TestCoverageASTPositions(t *testing.T) {
	p := Pos{4, 2}
	stmts := []stmt{&declStmt{pos: p}, &typedefStmt{pos: p}, &structDef{pos: p}, &enumDef{pos: p}, &funcDef{pos: p},
		&ifStmt{pos: p}, &whileStmt{pos: p}, &forStmt{pos: p}, &switchStmt{pos: p}, &branchStmt{pos: p},
		&returnStmt{pos: p}, &blockStmt{pos: p}, &exprStmt{pos: p}}
	for _, s := range stmts {
		if s.stmtPos() != p {
			t.Errorf("%T: %v", s, s.stmtPos())
		}
	}
	exprs := []expr{&litExpr{pos: p}, &identExpr{pos: p}, &unaryExpr{pos: p}, &postfixExpr{pos: p}, &binaryExpr{pos: p},
		&assignExpr{pos: p}, &condExpr{pos: p}, &callExpr{pos: p}, &indexExpr{pos: p}, &memberExpr{pos: p},
		&castExpr{pos: p}, &sizeofExpr{pos: p}}
	for _, e := range exprs {
		if e.exprPos() != p {
			t.Errorf("%T: %v", e, e.exprPos())
		}
	}
	// A statement or an expression the compiler does not know
	c := newCompiler()
	if _, err := c.stmt(struct{ *exprStmt }{&exprStmt{pos: p}}); err == nil || !strings.Contains(err.Error(), "unsupported statement") {
		t.Errorf("unsupported statement: %v", err)
	}
	if _, err := c.expr(struct{ *litExpr }{&litExpr{pos: p}}); err == nil || !strings.Contains(err.Error(), "unsupported expression") {
		t.Errorf("unsupported expression: %v", err)
	}
	if _, _, err := c.lvalue(&callExpr{pos: p, fn: "f"}); err == nil || !strings.Contains(err.Error(), "line 4:2: cannot assign") {
		t.Errorf("lvalue: %v", err)
	}
}
