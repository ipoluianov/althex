package disasm

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The differential tests of the AVR and RISC-V decoders against GNU
// objdump. The listings of objdump are recorded in testdata, so the tests
// run without it; with ALTHEX_REGEN=1 and the tools present (in
// ALTHEX_BINUTILS, or on the PATH) they are made again; ALTHEX_NO_BINUTILS=1
// runs them as without the tools. The sweeps over all
// the instruction words run objdump when it is there and otherwise check a
// hash of the listing objdump made.

// arvToolDirs are where the GNU tools are looked for, after
// ALTHEX_BINUTILS and the PATH
var arvToolDirs = []string{}

// arvTool returns the path of a GNU tool; "" if it is not there
func arvTool(name string) string {
	if os.Getenv("ALTHEX_NO_BINUTILS") != "" {
		return ""
	}
	dirs := append([]string{os.Getenv("ALTHEX_BINUTILS")}, arvToolDirs...)
	for _, d := range dirs {
		if d == "" {
			continue
		}
		p := filepath.Join(d, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return ""
}

func arvRegen() bool { return os.Getenv("ALTHEX_REGEN") != "" }

// arvLine is a line of a listing: the address, the bytes in memory order,
// the text and the comment
type arvLine struct {
	addr    uint64
	code    []byte
	text    string
	comment string
}

func (l arvLine) String() string {
	return fmt.Sprintf("%x\t%x\t%s\t%s", l.addr, l.code, l.text, l.comment)
}

// arvParseObjdump reads the lines of "objdump -D" of a raw binary; the
// bytes are taken from bin
func arvParseObjdump(out string, bin []byte) []arvLine {
	var lines []arvLine
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		f := strings.Split(sc.Text(), "\t")
		if len(f) < 3 || !strings.HasSuffix(f[0], ":") {
			continue
		}
		addr, err := strconv.ParseUint(strings.TrimSpace(strings.TrimSuffix(f[0], ":")), 16, 64)
		if err != nil {
			continue
		}
		n := len(strings.ReplaceAll(strings.TrimSpace(f[1]), " ", "")) / 2
		text := strings.TrimSpace(f[2])
		comment := ""
		if len(f) > 3 {
			ops := strings.Join(f[3:], "\t")
			if i := strings.Index(ops, ";"); i >= 0 {
				comment = strings.TrimSpace(ops[i+1:])
				ops = ops[:i]
			}
			// RISC-V writes the target of a jump as "# 0x..."
			if i := strings.Index(ops, " # "); i >= 0 {
				comment = strings.TrimSpace(ops[i+3:])
				ops = ops[:i]
			}
			if ops = strings.TrimSpace(ops); ops != "" {
				text += " " + ops
			}
		}
		if int(addr)+n > len(bin) {
			continue
		}
		lines = append(lines, arvLine{addr, bin[addr : addr+uint64(n)], text, comment})
	}
	return lines
}

// arvObjdump disassembles a raw binary with objdump
func arvObjdump(t *testing.T, tool string, bin []byte, args ...string) []arvLine {
	t.Helper()
	f := filepath.Join(t.TempDir(), "code.bin")
	if err := os.WriteFile(f, bin, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(tool, append(append([]string{"-D", "-b", "binary"}, args...), f)...).Output()
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	return arvParseObjdump(string(out), bin)
}

// arvDecode is how the decoder writes a line, for the comparison
func arvDecode(a Arch, code []byte, addr uint64) arvLine {
	in := a.Decode(code, addr, ModeDefault)
	if in.Len <= 0 || in.Len > len(code) {
		return arvLine{addr: addr, text: "<no instruction>"}
	}
	return arvLine{addr, code[:in.Len], in.Text(), strings.TrimSpace(in.Comment)}
}

// arvFixer makes what is not compared equal (the comments objdump has
// and the decoder cannot) and tells whether a line is a known deviation
// from objdump
type arvFixer func(got, want *arvLine) (known bool)

// arvCheck decodes the code of each line of a listing and compares; it
// returns the number of the lines that agree, of the known deviations
// and up to max differences
func arvCheck(a Arch, lines []arvLine, fix arvFixer, max int) (agree, known int, diffs []string) {
	for _, l := range lines {
		got := arvDecode(a, l.code, l.addr)
		want := l
		if fix != nil && fix(&got, &want) {
			known++
			continue
		}
		if bytes.Equal(got.code, want.code) && got.text == want.text && got.comment == want.comment {
			agree++
		} else if len(diffs) < max {
			diffs = append(diffs, fmt.Sprintf("%x: % x: got %q ; %q, objdump %q ; %q", l.addr, l.code, got.text, got.comment, l.text, l.comment))
		}
	}
	return agree, known, diffs
}

// arvReadExpect reads a recorded listing
func arvReadExpect(t *testing.T, path string) []arvLine {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var lines []arvLine
	for _, s := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		f := strings.Split(s, "\t")
		if len(f) != 4 {
			t.Fatalf("%s: %q", path, s)
		}
		addr, _ := strconv.ParseUint(f[0], 16, 64)
		code, err := hex.DecodeString(f[1])
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, arvLine{addr, code, f[2], f[3]})
	}
	return lines
}

// arvWriteExpect records a listing
func arvWriteExpect(t *testing.T, path string, lines []arvLine) {
	t.Helper()
	var sb strings.Builder
	for _, l := range lines {
		sb.WriteString(l.String() + "\n")
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// arvAssemble assembles a source with GNU as and ld and returns the raw
// code at address 0
func arvAssemble(t *testing.T, prefix, src string, asArgs, ldArgs []string) []byte {
	t.Helper()
	dir := t.TempDir()
	obj, elf, bin := filepath.Join(dir, "a.o"), filepath.Join(dir, "a.elf"), filepath.Join(dir, "a.bin")
	run := func(tool string, args ...string) {
		out, err := exec.Command(arvTool(prefix+tool), args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", tool, err, out)
		}
	}
	run("as", append(append([]string{}, asArgs...), "-o", obj, src)...)
	run("ld", append(append([]string{}, ldArgs...), "-Ttext=0", "-e", "0", "-o", elf, obj)...)
	run("objcopy", "-O", "binary", "-j", ".text", elf, bin)
	data, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// arvHash is the hash of a listing, to check a sweep without objdump
func arvHash(lines []arvLine) string {
	h := sha256.New()
	for _, l := range lines {
		fmt.Fprintln(h, l.String())
	}
	return hex.EncodeToString(h.Sum(nil))
}

// arvSweep compares the decoder with objdump over a binary of many
// instruction words: with objdump when it is there, and otherwise with the
// hash of the listing of the decoder recorded when it agreed with objdump
func arvSweep(t *testing.T, a Arch, tool string, bin []byte, args []string, hashFile string, fix arvFixer) {
	t.Helper()
	path := arvTool(tool)
	if path == "" {
		data, err := os.ReadFile(hashFile)
		if err != nil {
			t.Fatal(err)
		}
		var lines []arvLine
		for off := 0; off < len(bin); {
			l := arvDecode(a, bin[off:], uint64(off))
			if len(l.code) == 0 {
				t.Fatalf("%x: no instruction", off)
			}
			lines = append(lines, l)
			off += len(l.code)
		}
		if got := arvHash(lines); got != strings.TrimSpace(string(data)) {
			t.Fatalf("the listing differs from objdump's (hash %s); run with %s to see where", got, tool)
		}
		return
	}
	want := arvObjdump(t, path, bin, args...)
	agree, known, diffs := arvCheck(a, want, fix, 40)
	t.Logf("%s %v: %d of %d agree (%.3f%%), %d known deviations", a.ID(), args, agree, len(want),
		100*float64(agree)/float64(len(want)), known)
	for _, d := range diffs {
		t.Error(d)
	}
	// Record the hash of the listing of the decoder once it agrees with
	// objdump: the tests without objdump check that it stays the same
	if arvRegen() && len(diffs) == 0 && hashFile != "" {
		lines := make([]arvLine, len(want))
		for i, l := range want {
			lines[i] = arvDecode(a, l.code, l.addr)
		}
		if err := os.WriteFile(hashFile, []byte(arvHash(lines)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// avrCores are the sources in testdata/avr: the -mmcu of as, the -m of ld
// and objdump, and the decoder
var avrCores = []struct{ mcu, ld, arch, id string }{
	{"avr5", "avr5", "avr:5", "avr"},
	{"avr6", "avr6", "avr:6", "avr"},
	{"avrxmega7", "avrxmega7", "avr:107", "avr"},
	{"avrtiny", "avrtiny", "avr:100", "avrtiny"},
}

// avrFix makes the known differences from objdump equal: a relative jump
// before address 0 wraps around the flash, objdump writes a 32-bit address;
// the decoder adds the bit of sbi and cbi to the comment
func avrFix(got, want *arvLine) bool {
	if strings.HasPrefix(got.text, "sbi ") || strings.HasPrefix(got.text, "cbi ") {
		got.comment, _, _ = strings.Cut(got.comment, ", bit ")
	}
	if v, err := strconv.ParseUint(strings.TrimPrefix(want.comment, "0x"), 16, 64); err == nil && v >= 0xFFFF0000 {
		return got.text == want.text && got.comment == fmt.Sprintf("0x%x", v&0x7FFFFF)
	}
	return false
}

func TestAVRListing(t *testing.T) {
	for _, c := range avrCores {
		t.Run(c.mcu, func(t *testing.T) {
			src := filepath.Join("testdata", "avr", c.mcu+".s")
			exp := filepath.Join("testdata", "avr", c.mcu+".expect")
			if arvRegen() && arvTool("avr-objdump") != "" {
				asArgs := []string{"-mmcu=" + c.mcu, "-mno-link-relax"}
				if strings.HasPrefix(c.mcu, "avrxmega") {
					asArgs = append(asArgs, "-mrmw")
				}
				bin := arvAssemble(t, "avr-", src, asArgs, []string{"-m", c.ld})
				arvWriteExpect(t, exp, arvObjdump(t, arvTool("avr-objdump"), bin, "-m", c.arch))
			}
			lines := arvReadExpect(t, exp)
			agree, known, diffs := arvCheck(ByID(c.id), lines, avrFix, 50)
			t.Logf("%d of %d agree, %d known deviations", agree, len(lines), known)
			for _, d := range diffs {
				t.Error(d)
			}
		})
	}
}

// avrWords is every 16-bit word followed by the word 0 (a nop, or the
// address of a long instruction), and then the long instructions with
// other second words
func avrWords() []byte {
	var b []byte
	for w := 0; w < 0x10000; w++ {
		b = binary.LittleEndian.AppendUint16(b, uint16(w))
		b = binary.LittleEndian.AppendUint16(b, 0)
	}
	x := uint32(12345)
	for w := 0; w < 0x10000; w++ {
		if avrLong(uint16(w)) {
			for i := 0; i < 4; i++ {
				x = x*1664525 + 1013904223
				b = binary.LittleEndian.AppendUint16(b, uint16(w))
				b = binary.LittleEndian.AppendUint16(b, uint16(x>>16))
			}
		}
	}
	return b
}

func TestAVRAllWords(t *testing.T) {
	bin := avrWords()
	t.Run("avr6", func(t *testing.T) {
		arvSweep(t, ByID("avr"), "avr-objdump", bin, []string{"-m", "avr:6"}, "testdata/avr/words-avr6.sha256", avrFix)
	})
	t.Run("avrxmega7", func(t *testing.T) {
		arvSweep(t, ByID("avr"), "avr-objdump", bin, []string{"-m", "avr:107"}, "testdata/avr/words-avr6.sha256", avrFix)
	})
	t.Run("avrtiny", func(t *testing.T) {
		arvSweep(t, ByID("avrtiny"), "avr-objdump", bin, []string{"-m", "avr:100"}, "testdata/avr/words-avrtiny.sha256", avrFix)
	})
}

func TestAVRKinds(t *testing.T) {
	a := ByID("avr")
	dec := func(addr uint64, words ...uint16) Inst {
		var b []byte
		for _, w := range words {
			b = binary.LittleEndian.AppendUint16(b, w)
		}
		return a.Decode(b, addr, ModeDefault)
	}
	cases := []struct {
		in     Inst
		kind   Kind
		target uint64
		has    bool
	}{
		{dec(0x100, 0xC001), Jump, 0x104, true},            // rjmp .+2
		{dec(0x100, 0xDFFF), Call, 0x100, true},            // rcall .-2
		{dec(0, 0x940C, 0x0034), Jump, 0x68, true},         // jmp 0x68
		{dec(0, 0x940E, 0x0034), Call, 0x68, true},         // call 0x68
		{dec(0, 0x9508), Return, 0, false},                 // ret
		{dec(0, 0x9518), Return, 0, false},                 // reti
		{dec(0x10, 0xF409), Branch, 0x14, true},            // brne .+2
		{dec(0x10, 0x9B00, 0x0000), Branch, 0x14, true},    // sbis 0, 0; nop
		{dec(0x10, 0x9B00, 0x940C, 0), Branch, 0x16, true}, // sbis 0, 0; jmp
		{dec(0, 0x9409), Jump, 0, false},                   // ijmp
		{dec(0, 0x9519), Call, 0, false},                   // eicall
		{dec(0, 0x9598), Other, 0, false},                  // break
		{dec(0, 0xFFFF), Invalid, 0, false},
	}
	for _, c := range cases {
		if c.in.Kind != c.kind || c.in.HasTarget != c.has || c.in.Target != c.target {
			t.Errorf("%s: kind %d target %x %v", c.in.Text(), c.in.Kind, c.in.Target, c.in.HasTarget)
		}
	}
}

func TestAVREffects(t *testing.T) {
	a := ByID("avr")
	dec := func(words ...uint16) Inst {
		var b []byte
		for _, w := range words {
			b = binary.LittleEndian.AppendUint16(b, w)
		}
		return a.Decode(b, 0, ModeDefault)
	}
	cases := []struct {
		in   Inst
		text string
		want []Effect
	}{
		{dec(0xEF8F), "ldi r24, 0xFF", []Effect{{Kind: SetConst, Dst: 24, Src: -1, Base: -1, Imm: 0xFF}}},
		{dec(0x9180, 0x0100), "lds r24, 0x0100", []Effect{
			{Kind: MemAccess, Dst: 24, Src: -1, Base: -1, Addr: 0x100, Size: 1},
			{Kind: Clobber, Dst: 24, Src: -1, Base: -1}}},
		{dec(0x9380, 0x0100), "sts 0x0100, r24", []Effect{{Kind: MemAccess, Dst: -1, Src: 24, Base: -1, Addr: 0x100, Size: 1, Store: true}}},
		{dec(0xB78F), "in r24, 0x3f", []Effect{
			{Kind: MemAccess, Dst: 24, Src: -1, Base: -1, Addr: 0x5F, Size: 1},
			{Kind: Clobber, Dst: 24, Src: -1, Base: -1}}},
		{dec(0xBB88), "out 0x18, r24", []Effect{{Kind: MemAccess, Dst: -1, Src: 24, Base: -1, Addr: 0x38, Size: 1, Store: true}}},
		{dec(0x9AC3), "sbi 0x18, 3", []Effect{{Kind: MemAccess, Dst: -1, Src: -1, Base: -1, Addr: 0x38, Size: 1, Store: true}}},
		{dec(0x9BB3), "sbis 0x16, 3", []Effect{{Kind: MemAccess, Dst: -1, Src: -1, Base: -1, Addr: 0x36, Size: 1}}},
		{dec(0x838D), "std Y+5, r24", []Effect{{Kind: MemAccess, Dst: -1, Src: 24, Base: 28, Imm: 5, Size: 1, Store: true}}},
		{dec(0x9181), "ld r24, Z+", []Effect{
			{Kind: MemAccess, Dst: 24, Src: -1, Base: 30, Size: 1},
			{Kind: Clobber, Dst: 24, Src: -1, Base: -1},
			{Kind: Clobber, Dst: 30, Src: -1, Base: -1},
			{Kind: Clobber, Dst: 31, Src: -1, Base: -1}}},
		{dec(0x938E), "st -X, r24", []Effect{
			{Kind: MemAccess, Dst: -1, Src: 24, Base: 26, Imm: -1, Size: 1, Store: true},
			{Kind: Clobber, Dst: 26, Src: -1, Base: -1},
			{Kind: Clobber, Dst: 27, Src: -1, Base: -1}}},
		{dec(0x0F88), "add r24, r24", []Effect{{Kind: Clobber, Dst: 24, Src: -1, Base: -1}}},
	}
	for _, c := range cases {
		if c.in.Text() != c.text {
			t.Errorf("%q, want %q", c.in.Text(), c.text)
		}
		if fmt.Sprint(c.in.Effects) != fmt.Sprint(c.want) {
			t.Errorf("%s: %+v, want %+v", c.text, c.in.Effects, c.want)
		}
	}
}

func TestAVRClearIsConst(t *testing.T) {
	a := ByID("avr")
	for _, code := range [][]byte{{0x11, 0x24}, {0x88, 0x1B}} { // eor r1, r1; sub r24, r24
		in := a.Decode(code, 0, ModeDefault)
		if len(in.Effects) != 1 || in.Effects[0].Kind != SetConst || in.Effects[0].Imm != 0 {
			t.Errorf("%s %s: %+v", in.Mnemonic, in.Operands, in.Effects)
		}
	}
	if in := a.Decode([]byte{0x12, 0x24}, 0, ModeDefault); len(in.Effects) != 1 || in.Effects[0].Kind != Clobber {
		t.Errorf("eor r1, r2: %+v", in.Effects)
	}
}
