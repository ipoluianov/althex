package disasm

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The ARM tests compare with GNU objdump: testdata/arm/*.s are assembled,
// and what objdump shows of them is kept in testdata/arm/*.txt, so the
// tests run without the tools. ALTHEX_ARM_REGEN=1 makes the .txt again;
// ALTHEX_ARM_RANDOM=n compares n random instructions of each length with
// objdump. The tools are found by ALTHEX_ARM_OBJDUMP (the path of
// arm-none-eabi-objdump, with arm-none-eabi-as and -objcopy next to it)
// or in PATH

const armVMA = 0x8000000

// armSources are the test sources and the flags to assemble them
var armSources = []struct{ name, flags string }{
	{"thumb16", "-mcpu=cortex-m7"},
	{"thumb32", "-mcpu=cortex-m7 -mfpu=fpv5-d16 -mfloat-abi=hard"},
	{"dsp", "-mcpu=cortex-m7"},
	{"vfp", "-mcpu=cortex-m7 -mfpu=fpv5-d16 -mfloat-abi=hard"},
	{"v8m", "-mcpu=cortex-m33"},
}

// armTool is the path of a tool of arm-none-eabi ("objdump"); "" if
// there is none
func armTool(name string) string {
	if p := os.Getenv("ALTHEX_ARM_OBJDUMP"); p != "" {
		p = filepath.Join(filepath.Dir(p), "arm-none-eabi-"+name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
		return ""
	}
	p, err := exec.LookPath("arm-none-eabi-" + name)
	if err != nil {
		return ""
	}
	return p
}

// objdumpLine is an instruction as objdump shows it
type objdumpLine struct {
	addr     uint64
	code     []byte
	mn, ops  string
	comment  string
	invalid  bool
	original string
}

var objdumpRe = regexp.MustCompile(`^\s*([0-9a-f]+):\t([0-9a-f ]+?)\s*\t(.*)$`)

// parseObjdump reads the instructions of objdump -D output
func parseObjdump(text string) []objdumpLine {
	var out []objdumpLine
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		m := objdumpRe.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		addr, _ := strconv.ParseUint(m[1], 16, 64)
		l := objdumpLine{addr: addr, original: sc.Text()}
		for _, hw := range strings.Fields(m[2]) {
			v, _ := strconv.ParseUint(hw, 16, 32)
			if len(hw) == 4 {
				l.code = binary.LittleEndian.AppendUint16(l.code, uint16(v))
			} else {
				l.code = binary.LittleEndian.AppendUint32(l.code, uint32(v))
			}
		}
		fields := strings.Split(m[3], "\t")
		var comments []string
		for i, f := range fields {
			switch {
			case strings.HasPrefix(f, "@ "):
				c := strings.TrimPrefix(f, "@ ")
				if strings.HasPrefix(c, "(") && strings.HasSuffix(c, ")") {
					c = c[1 : len(c)-1]
				}
				comments = append(comments, c)
			case i == 0:
				l.mn = f
			case i == 1:
				l.ops = f
			}
		}
		l.comment = strings.Join(comments, ", ")
		if strings.HasPrefix(l.comment, "<UNDEFINED>") || strings.HasPrefix(l.mn, "undefined") || l.mn == "" && l.comment == "" {
			l.invalid = true
		}
		out = append(out, l)
	}
	return out
}

// runObjdump disassembles Thumb code at armVMA
func runObjdump(t *testing.T, code []byte) []objdumpLine {
	return runObjdumpM(t, code, "force-thumb,reg-names-std")
}

func runObjdumpM(t *testing.T, code []byte, opts string) []objdumpLine {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "code.bin")
	if err := os.WriteFile(bin, code, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(armTool("objdump"), "-D", "-b", "binary", "-marm", "-M"+opts,
		fmt.Sprintf("--adjust-vma=0x%x", armVMA), bin).Output()
	if err != nil {
		t.Fatalf("objdump: %v", err)
	}
	return parseObjdump(string(out))
}

// assemble makes the code of a test source
func assemble(t *testing.T, name, flags string) []byte {
	t.Helper()
	dir := t.TempDir()
	obj := filepath.Join(dir, name+".o")
	bin := filepath.Join(dir, name+".bin")
	args := append([]string{"-mthumb"}, strings.Fields(flags)...)
	args = append(args, filepath.Join("testdata", "arm", name+".s"), "-o", obj)
	if out, err := exec.Command(armTool("as"), args...).CombinedOutput(); err != nil {
		t.Fatalf("as %s: %v\n%s", name, err, out)
	}
	if out, err := exec.Command(armTool("objcopy"), "-O", "binary", obj, bin).CombinedOutput(); err != nil {
		t.Fatalf("objcopy: %v\n%s", err, out)
	}
	code, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

// decodeAll decodes code as a listing does, one instruction after another
func decodeAll(code []byte, addr uint64) []Inst {
	return LinearMode(ByID("arm"), bytes.NewReader(code), 0, addr, 1<<30, ModeThumb)
}

// sameAsObjdump tells how an instruction differs from what objdump shows;
// "" if it does not
func sameAsObjdump(in Inst, l objdumpLine) string {
	switch {
	case in.Len != len(l.code):
		return fmt.Sprintf("length %d", in.Len)
	case l.invalid != (in.Kind == Invalid && in.Mnemonic[0] == '.'):
		return "valid/invalid"
	case l.invalid:
		return ""
	case in.Mnemonic != l.mn:
		return "mnemonic"
	case in.Operands != l.ops:
		return "operands"
	case in.Comment != l.comment:
		return "comment"
	}
	return ""
}

func TestARMObjdump(t *testing.T) {
	regen := os.Getenv("ALTHEX_ARM_REGEN") != ""
	if regen && armTool("objdump") == "" {
		t.Fatal("ALTHEX_ARM_REGEN: no arm-none-eabi-objdump")
	}
	total, bad := 0, 0
	for _, src := range armSources {
		txt := filepath.Join("testdata", "arm", src.name+".txt")
		if regen {
			code := assemble(t, src.name, src.flags)
			var sb strings.Builder
			for _, l := range runObjdump(t, code) {
				sb.WriteString(l.original + "\n")
			}
			if err := os.WriteFile(txt, []byte(sb.String()), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		data, err := os.ReadFile(txt)
		if err != nil {
			t.Fatal(err)
		}
		want := parseObjdump(string(data))
		var code []byte
		for _, l := range want {
			code = append(code, l.code...)
		}
		got := decodeAll(code, armVMA)
		i := 0
		for _, l := range want {
			for i < len(got) && got[i].Addr < l.addr {
				i++
			}
			total++
			if i == len(got) || got[i].Addr != l.addr {
				bad++
				t.Errorf("%s: %x: no instruction; objdump: %s", src.name, l.addr, l.original)
				continue
			}
			if why := sameAsObjdump(got[i], l); why != "" {
				bad++
				t.Errorf("%s: %x: %s: %q %q %q; objdump: %q %q %q", src.name, l.addr, why,
					got[i].Mnemonic, got[i].Operands, got[i].Comment, l.mn, l.ops, l.comment)
			}
		}
	}
	t.Logf("%d instructions, %d differ from objdump", total, bad)
}

// armOutside tells an encoding that objdump decodes but that is not of
// the M-profile or not an instruction at all: Advanced SIMD, the
// half-precision and the ARMv8.1-M instructions, the A-profile system
// ones, and what objdump shows as garbage ("ldr??.w", "<undefined>")
func armOutside(l objdumpLine) bool {
	if l.invalid {
		return false
	}
	if len(l.code) == 4 {
		h1 := binary.LittleEndian.Uint16(l.code)
		h2 := binary.LittleEndian.Uint16(l.code[2:])
		cp := (h2 >> 8) & 15
		switch {
		case h1&0xEF00 == 0xEF00, h1&0xFF10 == 0xF900:
			return true // Advanced SIMD
		case h1&0xEC00 == 0xEC00 && (cp == 8 || cp == 9) && strings.HasPrefix(l.mn, "v"):
			return true // ARMv8.x extensions, half precision
		case h1&0xFE00 == 0xFC00 && cp == 9:
			return true // objdump scales the offset of ldc2, stc2 as for half precision
		case h1&0xEC00 == 0xEC00 && cp&14 != 10 && strings.HasPrefix(l.mn, "v"):
			return true // ARMv8.x extensions in the space of the generic coprocessors
		}
	}
	base, _, _ := strings.Cut(l.mn, ".")
	if armOutsideNames[base] {
		return true
	}
	// With the condition of an IT block
	for _, c := range armConds[:15] {
		if strings.HasSuffix(base, c) && armOutsideNames[strings.TrimSuffix(base, c)] {
			return true
		}
	}
	for _, g := range []string{"??", "<undefined>", "<illegal", "<invalid", "(UNDEF", "<unknown>", "_fiq", "_irq", "_svc",
		"_abt", "_und", "_mon", "_hyp", "_usr", "SPSR", "P0"} {
		if strings.Contains(l.mn, g) || strings.Contains(l.ops, g) {
			return true
		}
	}
	return false
}

// armOutsideNames are the instructions of other profiles objdump decodes
var armOutsideNames = map[string]bool{}

func init() {
	for _, n := range strings.Fields(`bf bfl bfx bflx bfcsel le letp wls wlstp dls dlstp lctp
		csel csinc csinv csneg cset csetm cinc cinv cneg hvc smc eret clrm vscclrm vpt vpst vctp
		esb csdb ssbb pssbb tsb dcps1 dcps2 dcps3 hlt pacg autg pac aut pacbti bti bxaut
		crc32b crc32h crc32w crc32cb crc32ch crc32cw vjcvt srsdb srsia rfedb rfeia
		miaBT miaTB miaTT mia miaph mar mra vdup vmovx vins`) {
		armOutsideNames[n] = true
	}
}

// TestARMRandom compares random encodings with objdump: the length and the
// mnemonic must agree, the operands and the comments are counted
func TestARMRandom(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("ALTHEX_ARM_RANDOM"))
	if n <= 0 || armTool("objdump") == "" {
		t.Skip("ALTHEX_ARM_RANDOM and arm-none-eabi-objdump are needed")
	}
	rng := rand.New(rand.NewSource(1))
	// 16-bit ones, 32-bit ones, and 32-bit ones in IT blocks
	for _, kind := range []string{"16-bit", "32-bit", "32-bit in IT"} {
		var code []byte
		for range n {
			switch {
			case kind == "16-bit":
				code = binary.LittleEndian.AppendUint16(code, uint16(rng.Intn(0xE800)))
			case kind == "32-bit in IT" && rng.Intn(3) == 0:
				// it, itt, ittt, itttt with a condition other than al
				it := 0xBF00 | uint16(rng.Intn(14))<<4 | [4]uint16{8, 4, 2, 1}[rng.Intn(4)]
				code = binary.LittleEndian.AppendUint16(code, it)
			default:
				h1 := uint16(0xE800 + rng.Intn(0x1800))
				code = binary.LittleEndian.AppendUint16(code, h1)
				code = binary.LittleEndian.AppendUint16(code, uint16(rng.Intn(0x10000)))
			}
		}
		want := runObjdump(t, code)
		got := decodeAll(code, armVMA)
		byAddr := map[uint64]Inst{}
		for _, in := range got {
			byAddr[in.Addr] = in
		}
		stats := map[string]int{}
		examples := map[string][]string{}
		valid, outside := 0, 0
		for _, l := range want {
			in, ok := byAddr[l.addr]
			why := "missing"
			if ok {
				why = sameAsObjdump(in, l)
			}
			if !l.invalid {
				valid++
			}
			if armOutside(l) {
				outside++
				continue
			}
			if why == "" {
				continue
			}
			key := why + " " + l.mn
			if why == "valid/invalid" && l.invalid {
				key = why + " (objdump undefined) " + in.Mnemonic
			}
			stats[key]++
			if len(examples[key]) < 3 {
				examples[key] = append(examples[key], fmt.Sprintf("%x: %q %q %q; objdump %q %q %q",
					l.code, in.Mnemonic, in.Operands, in.Comment, l.mn, l.ops, l.comment))
			}
		}
		keys := make([]string, 0, len(stats))
		bad := map[string]int{}
		for k, v := range stats {
			keys = append(keys, k)
			kind, _, _ := strings.Cut(k, " ")
			bad[kind] += v
		}
		sort.Slice(keys, func(i, j int) bool { return stats[keys[i]] > stats[keys[j]] })
		for i, k := range keys {
			if i < 400 {
				t.Logf("%6d %s\n\t%s", stats[k], k, strings.Join(examples[k], "\n\t"))
			}
		}
		n := 0
		for _, v := range bad {
			n += v
		}
		compared := len(want) - outside
		t.Logf("%s: %d instructions, %d valid for objdump, %d of them outside M-profile; of the other %d: "+
			"%d differ (%.3f%% agree), %d in the length or the mnemonic: %v", kind, len(want), valid, outside, compared,
			n, 100*float64(compared-n)/float64(compared), bad["missing"]+bad["length"]+bad["valid/invalid"]+bad["mnemonic"], bad)
	}
}

func TestARMResetHandler(t *testing.T) {
	// ldr r0, =0x40021000; ldr r1, [r0, #0x18]; orr.w r1, r1, #4;
	// str r1, [r0, #0x18]; movw r2, #0x1000; movt r2, #0x4001;
	// str.w r3, [r2, #12]; adds r0, #4; adr r4, lit; bl 0; pop {r4, pc};
	// lit: .word 0x40021000
	code := []byte{
		0x07, 0x48, 0x81, 0x69, 0x41, 0xf0, 0x04, 0x01, 0x81, 0x61, 0x41, 0xf2, 0x00, 0x02,
		0xc4, 0xf2, 0x01, 0x02, 0xc2, 0xf8, 0x0c, 0x30, 0x04, 0x30, 0x01, 0xa4, 0xff, 0xf7,
		0xf1, 0xff, 0x10, 0xbd, 0x00, 0x10, 0x02, 0x40,
	}
	insts := decodeAll(code, armVMA)
	want := []struct {
		text string
		eff  []Effect
	}{
		{"ldr r0, [pc, #28]", []Effect{{Kind: LoadLiteral, Dst: 0, Src: -1, Base: -1, Addr: armVMA + 0x20, Size: 4}}},
		{"ldr r1, [r0, #24]", []Effect{{Kind: MemAccess, Dst: 1, Src: -1, Base: 0, Imm: 24, Size: 4}, {Kind: Clobber, Dst: 1, Src: -1, Base: -1}}},
		{"orr.w r1, r1, #4", []Effect{{Kind: Clobber, Dst: 1, Src: -1, Base: -1}}},
		{"str r1, [r0, #24]", []Effect{{Kind: MemAccess, Dst: -1, Src: 1, Base: 0, Imm: 24, Size: 4, Store: true}}},
		{"movw r2, #4096", []Effect{{Kind: SetLow16, Dst: 2, Src: -1, Base: -1, Imm: 0x1000}}},
		{"movt r2, #16385", []Effect{{Kind: SetHigh16, Dst: 2, Src: -1, Base: -1, Imm: 0x4001}}},
		{"str.w r3, [r2, #12]", []Effect{{Kind: MemAccess, Dst: -1, Src: 3, Base: 2, Imm: 12, Size: 4, Store: true}}},
		{"adds r0, #4", []Effect{{Kind: AddImm, Dst: 0, Src: 0, Base: -1, Imm: 4}}},
		{"add r4, pc, #4", []Effect{{Kind: SetConst, Dst: 4, Src: -1, Base: -1, Imm: armVMA + 0x20}}},
		{"bl 0x8000000", nil},
		{"pop {r4, pc}", nil},
	}
	if len(insts) < len(want) {
		t.Fatalf("%d instructions", len(insts))
	}
	for i, w := range want {
		in := insts[i]
		if in.Text() != w.text {
			t.Errorf("%d: %q, want %q", i, in.Text(), w.text)
		}
		if w.eff != nil && fmt.Sprint(in.Effects) != fmt.Sprint(w.eff) {
			t.Errorf("%s: effects %+v, want %+v", in.Text(), in.Effects, w.eff)
		}
	}
	if c := insts[0].Comment; c != "0x8000020" {
		t.Errorf("literal comment %q", c)
	}
	bl := insts[9]
	if bl.Kind != Call || !bl.HasTarget || bl.Target != armVMA {
		t.Errorf("bl: %+v", bl)
	}
	if n := len(bl.Effects); n != 6 {
		t.Errorf("bl clobbers %d registers", n)
	}
	if insts[10].Kind != Return {
		t.Errorf("pop {pc}: %v", insts[10].Kind)
	}
}

func TestARMIT(t *testing.T) {
	// ite eq; addeq r0, r0, r1; movne.w r1, #2; adds r0, r1, r2
	code := []byte{0x0c, 0xbf, 0x40, 0x18, 0x4f, 0xf0, 0x02, 0x01, 0x88, 0x18}
	insts := decodeAll(code, 0)
	var texts []string
	for _, in := range insts {
		texts = append(texts, in.Text())
	}
	want := "ite eq|addeq r0, r0, r1|movne.w r1, #2|adds r0, r1, r2"
	if got := strings.Join(texts, "|"); got != want {
		t.Fatalf("%s, want %s", got, want)
	}
	// A return in an IT block may not be taken: bxne lr
	insts = decodeAll([]byte{0x18, 0xbf, 0x70, 0x47}, 0)
	if insts[1].Text() != "bxne lr" || insts[1].Kind != Branch {
		t.Fatalf("%+v", insts[1])
	}
	// What a conditional instruction sets is not known after it:
	// it eq; moveq r0, #1
	insts = decodeAll([]byte{0x08, 0xbf, 0x01, 0x20}, 0)
	if e := insts[1].Effects; insts[1].Text() != "moveq r0, #1" || len(e) != 1 || e[0].Kind != Clobber || e[0].Dst != 0 {
		t.Fatalf("%q %+v", insts[1].Text(), e)
	}
	// Decoded alone, the instruction after an IT is not in a block
	if in := ByID("arm").Decode([]byte{0x40, 0x18}, 2, ModeThumb); in.Mnemonic != "adds" {
		t.Fatalf("%+v", in)
	}
}

func TestARMBranches(t *testing.T) {
	a := ByID("arm")
	if a.MaxLen() != 4 {
		t.Fatalf("MaxLen %d", a.MaxLen())
	}
	cases := []struct {
		code   []byte
		addr   uint64
		text   string
		kind   Kind
		target uint64
		mode   int
	}{
		// bl, the farthest back and forth (±16 MB)
		{[]byte{0x00, 0xf4, 0x00, 0xd0}, 0x1000000, "bl 0x4", Call, 0x4, 0},
		{[]byte{0xff, 0xf3, 0xff, 0xd7}, 0x1000000, "bl 0x2000002", Call, 0x2000002, 0},
		{[]byte{0xff, 0xf7, 0xfe, 0xff}, 0x8000004, "bl 0x8000004", Call, 0x8000004, 0},
		// blx to A32: the target is aligned to 4
		{[]byte{0x00, 0xf0, 0x02, 0xe8}, 0x8000002, "blx 0x8000008", Call, 0x8000008, ModeARM},
		// cbz, cbnz: forward only, up to 126
		{[]byte{0x00, 0xb1}, 0x100, "cbz r0, 0x104", Branch, 0x104, 0},
		{[]byte{0xff, 0xbb}, 0x100, "cbnz r7, 0x182", Branch, 0x182, 0},
		{[]byte{0xfe, 0xe7}, 0x100, "b.n 0x100", Jump, 0x100, 0},
		{[]byte{0xfe, 0xd0}, 0x100, "beq.n 0x100", Branch, 0x100, 0},
		{[]byte{0x3f, 0xf4, 0xfe, 0xaf}, 0x100, "beq.w 0x100", Branch, 0x100, 0},
		{[]byte{0x70, 0x47}, 0, "bx lr", Return, 0, 0},
		{[]byte{0x98, 0x47}, 0, "blx r3", Call, 0, 0},
		{[]byte{0x5d, 0xf8, 0x04, 0xfb}, 0, "ldr.w pc, [sp], #4", Return, 0, 0},
		{[]byte{0xbd, 0xe8, 0xf0, 0x8f}, 0, "ldmia.w sp!, {r4, r5, r6, r7, r8, r9, r10, r11, pc}", Return, 0, 0},
		{[]byte{0x01, 0xdf}, 0, "svc 1", Interrupt, 0, 0},
		{[]byte{0x00, 0xbe}, 0, "bkpt 0x0000", Interrupt, 0, 0},
		{[]byte{0xd0, 0xe8, 0x01, 0xf0}, 0, "tbb [r0, r1]", Jump, 0, 0},
	}
	for _, c := range cases {
		in := a.Decode(c.code, c.addr, ModeThumb)
		if in.Text() != c.text || in.Kind != c.kind || in.Target != c.target || in.HasTarget != (c.target != 0) ||
			in.TargetMode != c.mode {
			t.Errorf("% x: %q kind %d target %#x %v mode %d", c.code, in.Text(), in.Kind, in.Target, in.HasTarget, in.TargetMode)
		}
	}
	// Undefined encodings
	if in := a.Decode([]byte{0xff, 0xb6}, 0, ModeThumb); in.Kind != Invalid || in.Len != 2 || in.Text() != ".short 0xb6ff" {
		t.Errorf("%+v", in)
	}
	if in := a.Decode([]byte{0xff, 0xff, 0xff, 0xff}, 0, ModeThumb); in.Kind != Invalid || in.Len != 4 || in.Text() != ".word 0xffffffff" {
		t.Errorf("%+v", in)
	}
	// Half of a 32-bit instruction at the end
	if in := a.Decode([]byte{0x00, 0xf0}, 0, ModeThumb); in.Kind != Invalid || in.Len != 2 {
		t.Errorf("%+v", in)
	}
}

func TestARMA32(t *testing.T) {
	a := ByID("arm")
	cases := []struct {
		code   []byte
		addr   uint64
		text   string
		kind   Kind
		target uint64
	}{
		{[]byte{0xfe, 0xff, 0xff, 0xea}, 0x100, "b 0x100", Jump, 0x100},
		{[]byte{0x00, 0x00, 0x00, 0xeb}, 0x100, "bl 0x108", Call, 0x108},
		{[]byte{0x1e, 0xff, 0x2f, 0xe1}, 0, "bx lr", Return, 0},
		{[]byte{0x04, 0x00, 0x9f, 0xe5}, 0x100, "ldr r0, [pc, #4]", Other, 0},
		{[]byte{0xfe, 0xff, 0xff, 0x0a}, 0x100, "beq 0x100", Branch, 0x100},
	}
	for _, c := range cases {
		in := a.Decode(c.code, c.addr, ModeARM)
		if in.Text() != c.text || in.Kind != c.kind || in.Target != c.target {
			t.Errorf("% x: %q kind %d target %#x", c.code, in.Text(), in.Kind, in.Target)
		}
	}
	in := a.Decode([]byte{0x04, 0x00, 0x9f, 0xe5}, 0x100, ModeARM)
	if len(in.Effects) != 1 || in.Effects[0].Kind != LoadLiteral || in.Effects[0].Addr != 0x10c {
		t.Errorf("ldr literal: %+v", in.Effects)
	}
	if b := ByID("arm-a32").Decode([]byte{0xfe, 0xff, 0xff, 0xea}, 0, ModeDefault); b.Text() != "b 0x0" {
		t.Errorf("arm-a32: %q", b.Text())
	}
}

// TestARMA32Random compares the A32 of golang.org/x/arch with objdump: it
// only reports how often they agree
func TestARMA32Random(t *testing.T) {
	n, _ := strconv.Atoi(os.Getenv("ALTHEX_ARM_RANDOM"))
	if n <= 0 || armTool("objdump") == "" {
		t.Skip("ALTHEX_ARM_RANDOM and arm-none-eabi-objdump are needed")
	}
	rng := rand.New(rand.NewSource(2))
	var code []byte
	for range n {
		code = binary.LittleEndian.AppendUint32(code, rng.Uint32())
	}
	want := runObjdumpM(t, code, "reg-names-std")
	a := ByID("arm-a32")
	valid, sameValid, sameMn, sameText := 0, 0, 0, 0
	examples := map[string][]string{}
	for _, l := range want {
		in := a.Decode(code[l.addr-armVMA:], l.addr, ModeARM)
		if l.invalid == (in.Kind == Invalid) {
			sameValid++
		}
		if l.invalid || in.Kind == Invalid {
			continue
		}
		valid++
		if in.Mnemonic == l.mn {
			sameMn++
			if in.Operands == l.ops {
				sameText++
			} else if len(examples[l.mn]) < 1 {
				examples[l.mn] = append(examples[l.mn], fmt.Sprintf("%q; objdump %q", in.Text(), l.mn+" "+l.ops))
			}
		}
	}
	keys := make([]string, 0, len(examples))
	for k := range examples {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		if i < 40 {
			t.Logf("%s", examples[k][0])
		}
	}
	t.Logf("A32: %d words, valid/invalid agree %.2f%%; of %d valid for both: mnemonic %.2f%%, text %.2f%%", len(want),
		100*float64(sameValid)/float64(len(want)), valid, 100*float64(sameMn)/float64(valid), 100*float64(sameText)/float64(valid))
}
