package disasm

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// riscvFix leaves out the comments of objdump: it follows the values of
// registers from one instruction to the next and writes the addresses it
// computes as "# 0x..." comments, which a decoder of one instruction
// cannot. The known deviation: on RV32 objdump takes the shifts by 32 and
// more (slli, srli, srai and their compressed forms) for instructions; they
// are reserved, the decoder shows them as not instructions.
func riscvFix(xlen int) arvFixer {
	return func(got, want *arvLine) bool {
		want.comment = ""
		if xlen == 32 && strings.HasPrefix(got.text, ".insn") {
			mn, ops, _ := strings.Cut(want.text, " ")
			sh := ops[strings.LastIndex(ops, ",")+1:]
			switch mn {
			case "slli", "srli", "srai", "c.slli", "c.srli", "c.srai":
				v, err := strconv.ParseUint(strings.TrimPrefix(sh, "0x"), 16, 8)
				return err == nil && v >= 32
			}
		}
		return false
	}
}

func TestRISCVListing(t *testing.T) {
	for _, xlen := range []int{32, 64} {
		t.Run(fmt.Sprint(xlen), func(t *testing.T) {
			name := fmt.Sprintf("rv%d", xlen)
			src := filepath.Join("testdata", "riscv", name+".s")
			exp := filepath.Join("testdata", "riscv", name+".expect")
			if arvRegen() && arvTool("riscv64-unknown-elf-objdump") != "" {
				emu := map[int]string{32: "elf32lriscv", 64: "elf64lriscv"}[xlen]
				bin := arvAssemble(t, "riscv64-unknown-elf-", src, []string{"-march=" + name + "gc", "-mabi=" + map[int]string{32: "ilp32", 64: "lp64"}[xlen]}, []string{"-m", emu})
				arvWriteExpect(t, exp, arvObjdump(t, arvTool("riscv64-unknown-elf-objdump"), bin, "-m", "riscv:"+name))
			}
			lines := arvReadExpect(t, exp)
			agree, known, diffs := arvCheck(ByID("riscv"+fmt.Sprint(xlen)), lines, riscvFix(xlen), 50)
			t.Logf("%d of %d agree, %d known deviations", agree, len(lines), known)
			for _, d := range diffs {
				t.Error(d)
			}
		})
	}
}

// riscvCompressedWords is every 16-bit instruction word, each followed by
// the word 0 (c.unimp), and a nop at the end (objdump leaves out the zeros
// at the end)
func riscvCompressedWords() []byte {
	var b []byte
	for w := 0; w < 0x10000; w++ {
		if w&3 != 3 {
			b = binary.LittleEndian.AppendUint16(b, uint16(w))
			b = binary.LittleEndian.AppendUint16(b, 0)
		}
	}
	return binary.LittleEndian.AppendUint16(b, 1)
}

// riscvRandomWords is n random 32-bit instruction words; with major, the
// major opcode is one of the ones of RV64GC and funct7 is often one that
// is used, so most of the words are instructions
func riscvRandomWords(n int, seed uint64, major bool) []byte {
	majors := []uint32{0x03, 0x07, 0x0F, 0x13, 0x17, 0x1B, 0x23, 0x27, 0x2F, 0x33, 0x37, 0x3B, 0x43, 0x47, 0x4B, 0x4F,
		0x53, 0x63, 0x67, 0x6F, 0x73}
	funct7 := []uint32{0x00, 0x01, 0x20, 0x08, 0x0C, 0x10, 0x14, 0x2C, 0x50, 0x60, 0x68, 0x70, 0x78, 0x21, 0x2D, 0x51,
		0x61, 0x69, 0x71, 0x79, 0x09, 0x18, 0x30, 0x7D}
	x := seed
	next := func() uint32 {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		return uint32(x >> 16)
	}
	var b []byte
	for len(b) < 4*n {
		w := next()
		if major {
			w = w&^0x7F | majors[next()%uint32(len(majors))]
			switch next() % 4 {
			case 0:
				w = w&0x01FFFFFF | funct7[next()%uint32(len(funct7))]<<25
			case 1:
				w &^= 0xF8000 // rs1 = zero
			case 2:
				w &^= 0x1F00000 // rs2 = zero
			}
		} else if w&3 != 3 || w&0x1C == 0x1C {
			continue
		}
		b = binary.LittleEndian.AppendUint32(b, w)
	}
	return b
}

func TestRISCVAllWords(t *testing.T) {
	const tool = "riscv64-unknown-elf-objdump"
	// ALTHEX_SWEEP_SEED compares other random words with objdump (there is
	// no recorded hash for them)
	seed1, seed2 := uint64(88172645463325252), uint64(2463534242)
	hashed := true
	if v := os.Getenv("ALTHEX_SWEEP_SEED"); v != "" {
		if arvTool(tool) == "" {
			t.Skip("no objdump")
		}
		n, _ := strconv.ParseUint(v, 10, 64)
		seed1, seed2 = n*2654435761+1, n*40503+7
		hashed = false
	}
	hash := func(name string) string {
		if !hashed {
			return ""
		}
		return filepath.Join("testdata", "riscv", name)
	}
	for _, xlen := range []int{32, 64} {
		a := ByID(fmt.Sprintf("riscv%d", xlen))
		m := []string{"-m", fmt.Sprintf("riscv:rv%d", xlen)}
		t.Run(fmt.Sprintf("%d/compressed", xlen), func(t *testing.T) {
			arvSweep(t, a, tool, riscvCompressedWords(), m, hash(fmt.Sprintf("words16-rv%d.sha256", xlen)), riscvFix(xlen))
		})
		t.Run(fmt.Sprintf("%d/random", xlen), func(t *testing.T) {
			arvSweep(t, a, tool, riscvRandomWords(100000, seed1, false), m, hash(fmt.Sprintf("random-rv%d.sha256", xlen)), riscvFix(xlen))
		})
		t.Run(fmt.Sprintf("%d/opcodes", xlen), func(t *testing.T) {
			arvSweep(t, a, tool, riscvRandomWords(100000, seed2, true), m, hash(fmt.Sprintf("opcodes-rv%d.sha256", xlen)), riscvFix(xlen))
		})
	}
}

func TestRISCVKinds(t *testing.T) {
	dec := func(xlen int, addr uint64, w uint32) Inst {
		code := binary.LittleEndian.AppendUint32(nil, w)
		return ByID(fmt.Sprintf("riscv%d", xlen)).Decode(code, addr, ModeDefault)
	}
	cases := []struct {
		in     Inst
		text   string
		kind   Kind
		target uint64
		has    bool
	}{
		{dec(32, 0x42000000, 0x120000EF), "jal 0x42000120", Call, 0x42000120, true},
		{dec(32, 0x100, 0x0000006F), "j 0x100", Jump, 0x100, true},
		{dec(32, 0x100, 0x004002EF), "jal t0,0x104", Call, 0x104, true},
		{dec(32, 0, 0x00008067), "ret", Return, 0, false},
		{dec(32, 0, 0x00078067), "jr a5", Jump, 0, false},
		{dec(32, 0, 0x000780E7), "jalr a5", Call, 0, false},
		{dec(32, 0x10, 0x00B50463), "beq a0,a1,0x18", Branch, 0x18, true},
		{dec(32, 0, 0x00000073), "ecall", Interrupt, 0, false},
		{dec(32, 0, 0x00100073), "ebreak", Interrupt, 0, false},
		{dec(32, 0, 0x30200073), "mret", Return, 0, false},
		{dec(32, 0x10, 0x8082), "ret", Return, 0, false},            // c.jr ra
		{dec(32, 0x10, 0x9782), "jalr a5", Call, 0, false},          // c.jalr
		{dec(32, 0x10, 0x2001), "jal 0x10", Call, 0x10, true},       // c.jal
		{dec(32, 0x10, 0xA001), "j 0x10", Jump, 0x10, true},         // c.j
		{dec(32, 0x10, 0xC111), "beqz a0,0x14", Branch, 0x14, true}, // c.beqz
		{dec(32, 0x10, 0x9002), "ebreak", Interrupt, 0, false},      // c.ebreak
		{dec(64, 0x10, 0xFE1FF0EF), "jal 0xfffffffffffffff0", Call, 0xFFFFFFFFFFFFFFF0, true},
		{dec(32, 0x10, 0xFE1FF0EF), "jal 0xfffffff0", Call, 0xFFFFFFF0, true},
		{dec(64, 0x10, 0x2001), ".insn 2, 0x2001", Invalid, 0, false}, // c.addiw with rd 0
		{dec(64, 0, 0xC0001073), "unimp", Invalid, 0, false},
	}
	for _, c := range cases {
		if c.in.Text() != c.text || c.in.Kind != c.kind || c.in.HasTarget != c.has || c.in.Target != c.target {
			t.Errorf("%q kind %d target %x %v, want %q", c.in.Text(), c.in.Kind, c.in.Target, c.in.HasTarget, c.text)
		}
	}
}

func TestRISCVEffects(t *testing.T) {
	dec := func(xlen int, addr uint64, w uint32) Inst {
		code := binary.LittleEndian.AppendUint32(nil, w)
		return ByID(fmt.Sprintf("riscv%d", xlen)).Decode(code, addr, ModeDefault)
	}
	cases := []struct {
		in   Inst
		text string
		want []Effect
	}{
		{dec(32, 0, 0x400217B7), "lui a5,0x40021", []Effect{{Kind: SetConst, Dst: 15, Src: -1, Base: -1, Imm: 0x40021000}}},
		{dec(32, 0, 0x800007B7), "lui a5,0x80000", []Effect{{Kind: SetConst, Dst: 15, Src: -1, Base: -1, Imm: 0x80000000}}},
		{dec(64, 0, 0x800007B7), "lui a5,0x80000", []Effect{{Kind: SetConst, Dst: 15, Src: -1, Base: -1, Imm: -0x80000000}}},
		{dec(32, 0x1000, 0x00001517), "auipc a0,0x1", []Effect{{Kind: SetConst, Dst: 10, Src: -1, Base: -1, Imm: 0x2000}}},
		{dec(32, 0, 0x01850513), "addi a0,a0,24", []Effect{{Kind: AddImm, Dst: 10, Src: 10, Base: -1, Imm: 24}}},
		{dec(32, 0, 0x00500513), "li a0,5", []Effect{{Kind: SetConst, Dst: 10, Src: -1, Base: -1, Imm: 5}}},
		{dec(32, 0, 0x00F72C23), "sw a5,24(a4)", []Effect{{Kind: MemAccess, Dst: -1, Src: 15, Base: 14, Imm: 24, Size: 4, Store: true}}},
		{dec(32, 0, 0x01872783), "lw a5,24(a4)", []Effect{
			{Kind: MemAccess, Dst: 15, Src: -1, Base: 14, Imm: 24, Size: 4},
			{Kind: Clobber, Dst: 15, Src: -1, Base: -1}}},
		{dec(32, 0, 0x00B50533), "add a0,a0,a1", []Effect{{Kind: Clobber, Dst: 10, Src: -1, Base: -1}}},
		{dec(32, 0, 0x4515), "li a0,5", []Effect{{Kind: SetConst, Dst: 10, Src: -1, Base: -1, Imm: 5}}},           // c.li
		{dec(32, 0, 0x6541), "lui a0,0x10", []Effect{{Kind: SetConst, Dst: 10, Src: -1, Base: -1, Imm: 0x10000}}}, // c.lui
		{dec(32, 0, 0x0505), "addi a0,a0,1", []Effect{{Kind: AddImm, Dst: 10, Src: 10, Base: -1, Imm: 1}}},        // c.addi
		{dec(32, 0, 0x7179), "addi sp,sp,-48", []Effect{{Kind: AddImm, Dst: 2, Src: 2, Base: -1, Imm: -48}}},      // c.addi16sp
		{dec(32, 0, 0x0028), "addi a0,sp,8", []Effect{{Kind: AddImm, Dst: 10, Src: 2, Base: -1, Imm: 8}}},         // c.addi4spn
		{dec(32, 0, 0xC11C), "sw a5,0(a0)", []Effect{{Kind: MemAccess, Dst: -1, Src: 15, Base: 10, Size: 4, Store: true}}},
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
