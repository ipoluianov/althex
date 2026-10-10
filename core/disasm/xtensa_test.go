package disasm

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The differential tests compare the decoders with GNU objdump and
// Capstone. What the tools said is kept in testdata, so the tests run
// without them; to make it again:
//
//	ALTHEX_DISASM_REGEN=1 XTENSA_TOOLS=<dir of xtensa-lx106-elf-as, -objdump>
//	CAPSTONE_DIR=<dir with the capstone Python package> go test -run 'Xtensa|ARM64'
//
// A line of the expected files that starts with "?" is where the decoder
// and the tool disagree (known, not checked)

const xtBase = 0x40080000

func xtRegen() bool { return os.Getenv("ALTHEX_DISASM_REGEN") != "" }

func TestXtensaKinds(t *testing.T) {
	a := ByID("xtensa")
	if a == nil || a.MaxLen() != 3 {
		t.Fatal("no xtensa")
	}
	type tc struct {
		hex    string
		addr   uint64
		text   string
		kind   Kind
		target uint64
	}
	for _, c := range []tc{
		{"21ffff", 0x40080004, "l32r a2, 0x40080000", Other, 0},
		{"05feff", 0x40080021, "call0 0x40080004", Call, 0x40080004},
		{"a50000", 0x40080000, "call8 0x4008000c", Call, 0x4008000c},
		{"c00300", 0, "callx0 a3", Call, 0},
		{"46b8ff", 0x4008011f, "j 0x40080004", Jump, 0x40080004},
		{"a00200", 0, "jx a2", Jump, 0},
		{"0df0", 0, "ret.n", Return, 0},
		{"1df0", 0, "retw.n", Return, 0},
		{"900000", 0, "retw", Return, 0},
		{"2632ca", 0x40080036, "beqi a2, 3, 0x40080004", Branch, 0x40080004},
		{"8c c2", 0x4008011b, "beqz.n a2, 0x4008012b", Branch, 0x4008012b},
		{"768210", 0x1000, "loop a2, 0x1014", Branch, 0x1014},
		{"005000", 0, "syscall", Interrupt, 0},
		{"364100", 0, "entry a1, 32", Other, 0},
		{"00100a", 0x1000, "add.s f1, f0, f0", Other, 0},
		{"240374", 0, "mul.aa.ll a3, a2", Other, 0},
	} {
		code, _ := hex.DecodeString(strings.ReplaceAll(c.hex, " ", ""))
		in := a.Decode(code, c.addr, ModeDefault)
		if in.Text() != c.text || in.Kind != c.kind || in.Len != len(code) {
			t.Errorf("%s: %q kind %d len %d, want %q kind %d", c.hex, in.Text(), in.Kind, in.Len, c.text, c.kind)
		}
		if c.target != 0 && (!in.HasTarget || in.Target != c.target) {
			t.Errorf("%s: target %#x %v, want %#x", c.hex, in.Target, in.HasTarget, c.target)
		}
		if c.target == 0 && in.HasTarget {
			t.Errorf("%s: a target %#x", c.hex, in.Target)
		}
	}
}

func TestXtensaEffects(t *testing.T) {
	a := ByID("xtensa")
	dec := func(h string, addr uint64) Inst {
		code, _ := hex.DecodeString(h)
		return a.Decode(code, addr, ModeDefault)
	}
	has := func(in Inst, e Effect) bool {
		for _, x := range in.Effects {
			if x == e {
				return true
			}
		}
		return false
	}
	cases := []struct {
		hex  string
		addr uint64
		e    Effect
	}{
		{"32a3ff", 0, Effect{Kind: SetConst, Dst: 3, Src: -1, Base: -1, Imm: 0x3ff}},
		{"0c07", 0, Effect{Kind: SetConst, Dst: 7, Src: -1, Base: -1, Imm: 0}},
		{"21ffff", 0x40080004, Effect{Kind: LoadLiteral, Dst: 2, Src: -1, Base: -1, Addr: 0x40080000, Size: 4}},
		{"12c1f0", 0, Effect{Kind: AddImm, Dst: 1, Src: 1, Base: -1, Imm: -16}},
		{"22d380", 0, Effect{Kind: AddImm, Dst: 2, Src: 3, Base: -1, Imm: -32768}},
		{"0b23", 0, Effect{Kind: AddImm, Dst: 2, Src: 3, Base: -1, Imm: -1}},
		{"2221ff", 0, Effect{Kind: MemAccess, Dst: 2, Src: -1, Base: 1, Imm: 0x3fc, Size: 4}},
		{"2221ff", 0, Effect{Kind: Clobber, Dst: 2, Src: -1, Base: -1}},
		{"326204", 0, Effect{Kind: MemAccess, Dst: -1, Src: 3, Base: 2, Imm: 16, Size: 4, Store: true}},
		{"2213ff", 0, Effect{Kind: MemAccess, Dst: 2, Src: -1, Base: 3, Imm: 0x1fe, Size: 2}},
		{"229301", 0, Effect{Kind: MemAccess, Dst: 2, Src: -1, Base: 3, Imm: 2, Size: 2}},
		{"224300", 0, Effect{Kind: MemAccess, Dst: -1, Src: 2, Base: 3, Size: 1, Store: true}},
		{"28f1", 0, Effect{Kind: MemAccess, Dst: 2, Src: -1, Base: 1, Imm: 60, Size: 4}},
		{"2901", 0, Effect{Kind: MemAccess, Dst: -1, Src: 2, Base: 1, Size: 4, Store: true}},
		{"402380", 0, Effect{Kind: Clobber, Dst: 2, Src: -1, Base: -1}},
	}
	for _, c := range cases {
		in := dec(c.hex, c.addr)
		if !has(in, c.e) {
			t.Errorf("%s %s: %+v, want %+v", c.hex, in.Text(), in.Effects, c.e)
		}
	}
}

// xtObjdump dumps a binary with the lx106 objdump: address, length, text
type xtLine struct {
	addr uint64
	n    int
	text string
}

func xtObjdump(t *testing.T, bin []byte) []xtLine {
	dir := t.TempDir()
	f := filepath.Join(dir, "a.bin")
	os.WriteFile(f, bin, 0o644)
	tools := os.Getenv("XTENSA_TOOLS")
	out, err := exec.Command(filepath.Join(tools, "xtensa-lx106-elf-objdump"), "-D", "-z", "-b", "binary",
		"-m", "xtensa", "-EL", fmt.Sprintf("--adjust-vma=%#x", xtBase), f).Output()
	if err != nil {
		t.Fatal(err)
	}
	var res []xtLine
	for _, l := range strings.Split(string(out), "\n") {
		p := strings.Split(l, "\t")
		if len(p) < 3 || !strings.HasSuffix(p[0], ":") {
			continue
		}
		a, err := strconv.ParseUint(strings.TrimSpace(strings.TrimSuffix(p[0], ":")), 16, 64)
		if err != nil {
			continue
		}
		text := strings.Join(p[2:], " ")
		// l32r shows the literal: not known here
		if i := strings.Index(text, " ("); i > 0 && strings.HasPrefix(text, "l32r") {
			text = text[:i]
		}
		res = append(res, xtLine{a, len(strings.TrimSpace(p[1])) / 2, strings.TrimSpace(text)})
	}
	return res
}

// xtWriteLines writes the lines of a testdata file
func xtWriteLines(t *testing.T, name string, head string, lines []string) {
	var b strings.Builder
	b.WriteString(head)
	for _, l := range lines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(name, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// xtReadLines reads the lines of a testdata file, without comments
func xtReadLines(t *testing.T, name string) [][]string {
	f, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var res [][]string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := sc.Text()
		if l == "" || l[0] == '#' {
			continue
		}
		res = append(res, strings.Split(l, "\t"))
	}
	return res
}

// TestXtensaCore checks the instructions of core.s as objdump shows them
func TestXtensaCore(t *testing.T) {
	name := "testdata/xtensa/core.txt"
	if xtRegen() {
		tools := os.Getenv("XTENSA_TOOLS")
		dir := t.TempDir()
		o, bin := filepath.Join(dir, "a.o"), filepath.Join(dir, "a.bin")
		for _, c := range [][]string{
			{filepath.Join(tools, "xtensa-lx106-elf-as"), "--no-transform", "testdata/xtensa/core.s", "-o", o},
			{filepath.Join(tools, "xtensa-lx106-elf-objcopy"), "-O", "binary", "-j", ".text", o, bin},
		} {
			if out, err := exec.Command(c[0], c[1:]...).CombinedOutput(); err != nil {
				t.Fatalf("%v: %s", err, out)
			}
		}
		code, _ := os.ReadFile(bin)
		var lines []string
		for _, d := range xtObjdump(t, code) {
			off := d.addr - xtBase
			lines = append(lines, fmt.Sprintf("%x\t%x\t%s", d.addr, code[off:off+uint64(d.n)], d.text))
		}
		xtWriteLines(t, name, "# core.s as xtensa-lx106-elf-objdump shows it: address, bytes, text\n", lines)
	}
	a := ByID("xtensa")
	n := 0
	for _, p := range xtReadLines(t, name) {
		addr, _ := strconv.ParseUint(p[0], 16, 64)
		code, _ := hex.DecodeString(p[1])
		in := a.Decode(code, addr, ModeDefault)
		if in.Text() != p[2] || in.Len != len(code) {
			t.Errorf("%x %s: %q len %d, want %q", addr, p[1], in.Text(), in.Len, p[2])
		}
		n++
	}
	if n < 140 {
		t.Fatalf("%d lines", n)
	}
}

// xtRandom returns random instructions: 24-bit words and 16-bit ones
func xtRandom(n int) [][]byte {
	r := rand.New(rand.NewSource(1))
	res := make([][]byte, n)
	for i := range res {
		w := r.Uint32()
		if i%4 == 3 {
			// a narrow one
			w = w&0xFFF0 | uint32(8+r.Intn(6))
			res[i] = []byte{byte(w), byte(w >> 8)}
		} else {
			res[i] = []byte{byte(w), byte(w >> 8), byte(w >> 16)}
		}
	}
	return res
}

// TestXtensaObjdump compares random instructions with objdump (lx106)
func TestXtensaObjdump(t *testing.T) {
	name := "testdata/xtensa/objdump.txt"
	if xtRegen() {
		const slot = 8
		words := xtRandom(100000)
		bin := make([]byte, len(words)*slot)
		for i := range bin {
			bin[i] = 0xFF
		}
		for i, w := range words {
			copy(bin[i*slot:], w)
		}
		at := map[uint64]xtLine{}
		for _, d := range xtObjdump(t, bin) {
			at[d.addr] = d
		}
		a := ByID("xtensa")
		var lines []string
		same, sameMn, valid, other := 0, 0, 0, 0
		for i, w := range words {
			addr := uint64(xtBase + i*slot)
			d := at[addr]
			in := a.Decode(bin[i*slot:i*slot+slot], addr, ModeDefault)
			mark := ""
			switch {
			case in.Len == d.n && in.Text() == d.text:
				same++
			case d.text == "excw" && in.Text() != "excw":
				// the lx106 objdump shows what it does not have as excw
				other++
				mark = "?"
			default:
				mark = "?"
				if valid-same < 30 {
					t.Logf("%x %x: objdump %q, ours %q", addr, w, d.text, in.Text())
				}
			}
			if in.Len == d.n && in.Mnemonic == strings.Fields(d.text + " x")[0] {
				sameMn++
			}
			if mark != "?" || d.text != "excw" || in.Text() == "excw" {
				valid++
			}
			if i < 2000 {
				lines = append(lines, fmt.Sprintf("%s%x\t%x\t%d\t%s\t%s", mark, addr, w, d.n, d.text, in.Text()))
			}
		}
		t.Logf("objdump lx106: %d words, %d shown as excw for an option lx106 lacks; of the other %d same text %d (%.2f%%), same length and mnemonic %d",
			len(words), other, valid, same, 100*float64(same)/float64(valid), sameMn)
		xtWriteLines(t, name, fmt.Sprintf("# random words, xtensa-lx106-elf-objdump: address, bytes, length, text, ours\n"+
			"# %d words: %d excw for options lx106 lacks; of the other %d same text %d\n", len(words), other, valid, same), lines)
	}
	xtCheckDump(t, name)
}

func xtCheckDump(t *testing.T, name string) {
	a := ByID("xtensa")
	n := 0
	for _, p := range xtReadLines(t, name) {
		if strings.HasPrefix(p[0], "?") {
			continue
		}
		addr, _ := strconv.ParseUint(p[0], 16, 64)
		code, _ := hex.DecodeString(p[1])
		code = append(code, 0xFF, 0xFF, 0xFF)
		l, _ := strconv.Atoi(p[2])
		in := a.Decode(code, addr, ModeDefault)
		if in.Text() != p[3] || in.Len != l {
			t.Errorf("%x %s: %q len %d, want %q len %d", addr, p[1], in.Text(), in.Len, p[3], l)
		}
		n++
	}
	if n < 1000 {
		t.Fatalf("%d lines", n)
	}
}

// capstone decodes instructions with Capstone (the script in testdata)
func csDecode(t *testing.T, arch, mode string, addr uint64, insts [][]byte) [][]string {
	dir := t.TempDir()
	in, out := filepath.Join(dir, "in"), filepath.Join(dir, "out")
	var b bytes.Buffer
	for _, c := range insts {
		fmt.Fprintf(&b, "%x\n", c)
	}
	os.WriteFile(in, b.Bytes(), 0o644)
	script, _ := filepath.Abs("testdata/xtensa/capstone.py")
	cmd := exec.Command("python3", "-I", script, os.Getenv("CAPSTONE_DIR"), arch, mode, in, out, fmt.Sprint(addr))
	cmd.Dir = dir
	if o, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, o)
	}
	data, _ := os.ReadFile(out)
	var res [][]string
	for _, l := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		res = append(res, strings.Split(l, "\t"))
	}
	if len(res) != len(insts) {
		t.Fatalf("capstone: %d of %d", len(res), len(insts))
	}
	return res
}

// xtCsMnemonic is the mnemonic of Capstone written as objdump does
func xtCsMnemonic(mn string) string {
	switch mn {
	case "lsip", "ssip", "lsxp", "ssxp":
		return mn[:len(mn)-1] + "u"
	}
	if i := strings.IndexByte(mn, '.'); i > 0 {
		switch mn[:i] {
		case "rsr", "wsr", "xsr", "rur", "wur":
			return mn[:i]
		}
	}
	return mn
}

// TestXtensaCapstone compares random instructions with Capstone (ESP32):
// the length and the mnemonic
func TestXtensaCapstone(t *testing.T) {
	name := "testdata/xtensa/capstone.txt"
	a := ByID("xtensa")
	ours := func(code []byte) (int, string) {
		in := a.Decode(append(append([]byte(nil), code...), 0xFF, 0xFF), xtBase, ModeDefault)
		if in.Kind == Invalid {
			return 0, ""
		}
		return in.Len, xtCsMnemonic(in.Mnemonic)
	}
	if xtRegen() {
		words := xtRandom(100000)
		cs := csDecode(t, "CS_ARCH_XTENSA", "CS_MODE_XTENSA_ESP32", xtBase, words)
		var lines []string
		same, valid := 0, 0
		for i, w := range words {
			l, mn := ours(w)
			cl, _ := strconv.Atoi(cs[i][1])
			cmn := xtCsMnemonic(cs[i][2])
			if cl > 0 {
				valid++
			}
			mark := "?"
			if l == cl && mn == cmn {
				same++
				mark = ""
			}
			if i < 2000 || mark != "" && len(lines) < 4000 {
				lines = append(lines, fmt.Sprintf("%s%x\t%d\t%s\t%s", mark, w, cl, cmn, cs[i][3]))
			}
		}
		t.Logf("capstone ESP32: %d words, %d valid for capstone; same length and mnemonic %d (%.2f%%)",
			len(words), valid, same, 100*float64(same)/float64(len(words)))
		xtWriteLines(t, name, fmt.Sprintf("# random words, Capstone ESP32: bytes, length, mnemonic, operands\n"+
			"# %d words: same length and mnemonic %d\n", len(words), same), lines)
	}
	n := 0
	for _, p := range xtReadLines(t, name) {
		if strings.HasPrefix(p[0], "?") {
			continue
		}
		code, _ := hex.DecodeString(p[0])
		cl, _ := strconv.Atoi(p[1])
		if l, mn := ours(code); l != cl || mn != p[2] {
			t.Errorf("%s: %s len %d, want %s len %d", p[0], mn, l, p[2], cl)
		}
		n++
	}
	if n < 1000 {
		t.Fatalf("%d lines", n)
	}
}

func TestXtensaLinear(t *testing.T) {
	// entry a1, 32; movi.n a2, 1; retw.n
	code := []byte{0x36, 0x41, 0x00, 0x0C, 0x12, 0x1D, 0xF0}
	insts := Linear(ByID("xtensa"), bytes.NewReader(code), 0, 0x400D0000, 10)
	if len(insts) != 3 || insts[1].Text() != "movi.n a2, 1" || insts[2].Kind != Return {
		t.Fatalf("%+v", insts)
	}
}
