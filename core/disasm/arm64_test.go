package disasm

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

func a64Dec(w uint32, addr uint64) Inst {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, w)
	return ByID("arm64").Decode(b, addr, ModeDefault)
}

func TestARM64Text(t *testing.T) {
	a := ByID("arm64")
	if a == nil || a.MaxLen() != 4 {
		t.Fatal("no arm64")
	}
	for _, c := range []struct {
		w      uint32
		addr   uint64
		text   string
		kind   Kind
		target uint64
	}{
		{0xd2800020, 0, "mov x0, #0x1", Other, 0},
		{0xf9400820, 0, "ldr x0, [x1, #16]", Other, 0},
		{0xf8408420, 0, "ldr x0, [x1], #8", Other, 0},
		{0xa9bf7bfd, 0, "stp x29, x30, [sp, #-16]!", Other, 0},
		{0x58000040, 0x1000, "ldr x0, 0x1008", Other, 0},
		{0x90000000, 0x400123, "adrp x0, 0x400000", Other, 0},
		{0x14000004, 0x1000, "b 0x1010", Jump, 0x1010},
		{0x17ffffff, 0x1000, "b 0xffc", Jump, 0xffc},
		{0x54000040, 0x1000, "b.eq 0x1008", Branch, 0x1008},
		{0xb4000040, 0x1000, "cbz x0, 0x1008", Branch, 0x1008},
		{0x37080040, 0x1000, "tbnz w0, #1, 0x1008", Branch, 0x1008},
		{0x94000010, 0x1000, "bl 0x1040", Call, 0x1040},
		{0xd63f0020, 0, "blr x1", Call, 0},
		{0xd61f0020, 0, "br x1", Jump, 0},
		{0xd65f03c0, 0, "ret", Return, 0},
		{0xd4000001, 0, "svc #0x0", Interrupt, 0},
		{0xd4200000, 0, "brk #0x0", Interrupt, 0},
		{0xd503201f, 0, "nop", Other, 0},
		{0x00000000, 0, ".inst 0x00000000", Invalid, 0},
	} {
		in := a64Dec(c.w, c.addr)
		if in.Text() != c.text || in.Kind != c.kind || in.Len != 4 {
			t.Errorf("%08x: %q kind %d, want %q kind %d", c.w, in.Text(), in.Kind, c.text, c.kind)
		}
		if in.HasTarget != (c.target != 0) || in.Target != c.target {
			t.Errorf("%08x: target %#x %v, want %#x", c.w, in.Target, in.HasTarget, c.target)
		}
	}
}

func TestARM64Effects(t *testing.T) {
	has := func(in Inst, e Effect) bool {
		for _, x := range in.Effects {
			if x == e {
				return true
			}
		}
		return false
	}
	for _, c := range []struct {
		w    uint32
		addr uint64
		e    Effect
	}{
		{0xd2800020, 0, Effect{Kind: SetConst, Dst: 0, Src: -1, Base: -1, Imm: 1}},               // mov x0, #1
		{0xd2a80040, 0, Effect{Kind: SetConst, Dst: 0, Src: -1, Base: -1, Imm: 0x40020000}},      // mov x0, #0x40020000
		{0x12800000, 0, Effect{Kind: SetConst, Dst: 0, Src: -1, Base: -1, Imm: 0xffffffff}},      // mov w0, #-1
		{0xf2a80041, 0, Effect{Kind: SetHigh16, Dst: 1, Src: -1, Base: -1, Imm: 0x4002}},         // movk x1, #0x4002, lsl #16
		{0xf2800041, 0, Effect{Kind: Clobber, Dst: 1, Src: -1, Base: -1}},                        // movk x1, #2
		{0x90000000, 0x400123, Effect{Kind: SetConst, Dst: 0, Src: -1, Base: -1, Imm: 0x400000}}, // adrp
		{0x10000040, 0x1000, Effect{Kind: SetConst, Dst: 0, Src: -1, Base: -1, Imm: 0x1008}},     // adr
		{0x91004020, 0, Effect{Kind: AddImm, Dst: 0, Src: 1, Base: -1, Imm: 16}},                 // add x0, x1, #16
		{0xd10043ff, 0, Effect{Kind: AddImm, Dst: 31, Src: 31, Base: -1, Imm: -16}},              // sub sp, sp, #16
		{0x910003fd, 0, Effect{Kind: AddImm, Dst: 29, Src: 31, Base: -1}},                        // mov x29, sp
		{0xf9400820, 0, Effect{Kind: MemAccess, Dst: 0, Src: -1, Base: 1, Imm: 16, Size: 8}},     // ldr x0, [x1, #16]
		{0xf9400820, 0, Effect{Kind: Clobber, Dst: 0, Src: -1, Base: -1}},
		{0xb9000801, 0, Effect{Kind: MemAccess, Dst: -1, Src: 1, Base: 0, Imm: 8, Size: 4, Store: true}}, // str w1, [x0, #8]
		{0x39400020, 0, Effect{Kind: MemAccess, Dst: 0, Src: -1, Base: 1, Size: 1}},                      // ldrb w0, [x1]
		{0xa9bf7bfd, 0, Effect{Kind: MemAccess, Dst: -1, Src: 29, Base: 31, Imm: -16, Size: 16, Store: true}},
		{0xa9bf7bfd, 0, Effect{Kind: AddImm, Dst: 31, Src: 31, Base: -1, Imm: -16}},
		{0x58000040, 0x1000, Effect{Kind: LoadLiteral, Dst: 0, Src: -1, Base: -1, Addr: 0x1008, Size: 8}}, // ldr x0, lit
		{0x18000040, 0x1000, Effect{Kind: LoadLiteral, Dst: 0, Src: -1, Base: -1, Addr: 0x1008, Size: 4}}, // ldr w0, lit
		{0x8b020020, 0, Effect{Kind: Clobber, Dst: 0, Src: -1, Base: -1}},                                 // add x0, x1, x2
		{0x94000010, 0, Effect{Kind: Clobber, Dst: 30, Src: -1, Base: -1}},                                // bl
	} {
		in := a64Dec(c.w, c.addr)
		if !has(in, c.e) {
			t.Errorf("%08x %s: %+v, want %+v", c.w, in.Text(), in.Effects, c.e)
		}
	}
	// cmp and cbz write nothing
	for _, w := range []uint32{0xf100041f, 0xb4000040, 0xf9000020} {
		for _, e := range a64Dec(w, 0).Effects {
			if e.Kind == Clobber {
				t.Errorf("%08x: %+v", w, e)
			}
		}
	}
}

// a64CsMnemonic is a mnemonic as the comparison takes it: aliases the
// decoders choose differently are one name
func a64CsMnemonic(mn string) string {
	switch mn {
	case "b.hs":
		return "b.cs"
	case "b.lo":
		return "b.cc"
	}
	return mn
}

// TestARM64Capstone compares random words with Capstone: whether they are
// instructions, and the mnemonic
func TestARM64Capstone(t *testing.T) {
	name := "testdata/arm64/capstone.txt"
	ours := func(w uint32) string {
		in := a64Dec(w, 0x400000)
		if in.Kind == Invalid {
			return ""
		}
		return a64CsMnemonic(in.Mnemonic)
	}
	if xtRegen() {
		r := rand.New(rand.NewSource(2))
		words := make([][]byte, 100000)
		for i := range words {
			words[i] = binary.LittleEndian.AppendUint32(nil, r.Uint32())
		}
		cs := csDecode(t, "CS_ARCH_AARCH64", "CS_MODE_ARM", 0x400000, words)
		var lines []string
		bothValid, csValid, ourValid, same, sameValid := 0, 0, 0, 0, 0
		for i, b := range words {
			w := binary.LittleEndian.Uint32(b)
			mn := ours(w)
			cmn := ""
			if cs[i][1] != "0" {
				cmn = a64CsMnemonic(cs[i][2])
				csValid++
			}
			if mn != "" {
				ourValid++
			}
			if mn != "" && cmn != "" {
				bothValid++
			}
			if (mn == "") == (cmn == "") {
				sameValid++
			}
			mark := "?"
			if mn == cmn {
				same++
				mark = ""
			}
			if i < 2000 {
				lines = append(lines, fmt.Sprintf("%s%08x\t%s\t%s\t%s", mark, w, cmn, cs[i][3], a64Dec(w, 0x400000).Text()))
			}
		}
		t.Logf("capstone AArch64: %d words, valid for capstone %d, for us %d, both %d; same validity %d (%.2f%%); same mnemonic (or both invalid) %d (%.2f%%), of both valid %.2f%%",
			len(words), csValid, ourValid, bothValid, sameValid, 100*float64(sameValid)/float64(len(words)),
			same, 100*float64(same)/float64(len(words)), 100*float64(same-(len(words)-csValid-ourValid+bothValid))/float64(bothValid))
		xtWriteLines(t, name, fmt.Sprintf("# random words, Capstone AArch64 at 0x400000: word, mnemonic, operands, ours\n"+
			"# %d words: valid for capstone %d, for us %d, both %d; same mnemonic %d\n", len(words), csValid, ourValid, bothValid, same), lines)
	}
	n := 0
	for _, p := range xtReadLines(t, name) {
		if strings.HasPrefix(p[0], "?") {
			continue
		}
		w, _ := strconv.ParseUint(p[0], 16, 32)
		if mn := ours(uint32(w)); mn != p[1] {
			t.Errorf("%s: %q, want %q", p[0], mn, p[1])
		}
		if text := a64Dec(uint32(w), 0x400000).Text(); text != p[3] {
			t.Errorf("%s: %q, was %q", p[0], text, p[3])
		}
		n++
	}
	if n < 1000 {
		t.Fatalf("%d lines", n)
	}
}

func TestARM64Short(t *testing.T) {
	in := ByID("arm64").Decode([]byte{1, 2}, 0, ModeDefault)
	if in.Kind != Invalid || in.Len != 1 {
		t.Fatalf("%+v", in)
	}
}
