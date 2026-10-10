package disasm

import (
	"fmt"
	"strings"
)

// xtensa decodes the little-endian Xtensa of the ESP chips: the core
// instructions, 16-bit narrow ones (density) and the options of the
// ESP32 (windowed registers, loops, MUL/DIV, booleans, the floating point
// coprocessor, MAC16) and the ESP8266 (LX106). Text is as GNU objdump
// writes it. LITBASE is taken as off: l32r is relative to the PC
type xtensa struct{}

func init() { register(xtensa{}) }

func (xtensa) ID() string   { return "xtensa" }
func (xtensa) Name() string { return "Xtensa (ESP32, ESP8266)" }
func (xtensa) MaxLen() int  { return 3 }

// The constants of beqi, bnei, blti, bgei and of bltui, bgeui
var (
	xtB4const  = [16]int64{-1, 1, 2, 3, 4, 5, 6, 7, 8, 10, 12, 16, 32, 64, 128, 256}
	xtB4constu = [16]int64{32768, 65536, 2, 3, 4, 5, 6, 7, 8, 10, 12, 16, 32, 64, 128, 256}
)

// Special registers by number, for rsr, wsr and xsr
var xtSpecial = map[int]string{
	0: "lbeg", 1: "lend", 2: "lcount", 3: "sar", 4: "br", 5: "litbase", 12: "scompare1",
	16: "acclo", 17: "acchi", 32: "m0", 33: "m1", 34: "m2", 35: "m3",
	72: "windowbase", 73: "windowstart", 96: "ibreakenable", 97: "memctl", 99: "atomctl", 104: "ddr", 128: "ibreaka0", 129: "ibreaka1", 144: "dbreaka0",
	145: "dbreaka1", 160: "dbreakc0", 161: "dbreakc1", 176: "configid0",
	177: "epc1", 178: "epc2", 179: "epc3", 180: "epc4", 181: "epc5", 182: "epc6", 183: "epc7",
	192: "depc", 194: "eps2", 195: "eps3", 196: "eps4", 197: "eps5", 198: "eps6", 199: "eps7",
	208: "configid1", 209: "excsave1", 210: "excsave2", 211: "excsave3", 212: "excsave4",
	213: "excsave5", 214: "excsave6", 215: "excsave7", 224: "cpenable",
	226: "interrupt", 227: "intclear", 228: "intenable", 230: "ps", 231: "vecbase",
	232: "exccause", 233: "debugcause", 234: "ccount", 235: "prid", 236: "icount",
	237: "icountlevel", 238: "excvaddr", 240: "ccompare0", 241: "ccompare1",
	242: "ccompare2", 244: "misc0", 245: "misc1", 246: "misc2", 247: "misc3",
}

// User registers by number, for rur and wur
var xtUser = map[int]string{
	230: "expstate", 231: "threadptr", 232: "fcr", 233: "fsr",
}

// xtInst is an instruction while it is decoded
type xtInst struct {
	mn   string
	ops  []string
	kind Kind
	tgt  uint64
	has  bool
	eff  []Effect
}

func (x *xtInst) set(mn string, ops ...string) { x.mn, x.ops = mn, ops }

func (x *xtInst) jump(k Kind, t uint64) { x.kind, x.tgt, x.has = k, t&0xFFFFFFFF, true }

// clobber notes that a register gets a value that is not known
func (x *xtInst) clobber(r int) {
	x.eff = append(x.eff, Effect{Kind: Clobber, Dst: r, Src: -1, Base: -1})
}

func xa(r int) string { return fmt.Sprintf("a%d", r) }
func xf(r int) string { return fmt.Sprintf("f%d", r) }
func xb(r int) string { return fmt.Sprintf("b%d", r) }

// xi writes an immediate as objdump does: small ones in decimal, others in hex
func xi(v int64) string {
	if v > -256 && v < 256 {
		return fmt.Sprintf("%d", v)
	}
	return fmt.Sprintf("0x%x", uint32(v))
}

func xaddr(v uint64) string { return fmt.Sprintf("0x%x", v&0xFFFFFFFF) }

func xtSext(v uint32, bits uint) int64 {
	s := 32 - bits
	return int64(int32(v<<s) >> s)
}

func (xtensa) Decode(code []byte, addr uint64, mode int) Inst {
	if len(code) == 0 {
		return Inst{}
	}
	var x xtInst
	n := 0
	if code[0]&8 != 0 {
		if code[0]&0xF < 0xE && len(code) >= 2 {
			n = 2
			xtNarrow(&x, uint32(code[0])|uint32(code[1])<<8, addr)
		}
	} else if len(code) >= 3 {
		n = 3
		xtWide(&x, uint32(code[0])|uint32(code[1])<<8|uint32(code[2])<<16, addr)
	}
	if x.mn == "" {
		return Inst{Addr: addr, Len: 1, Mnemonic: ".byte", Operands: fmt.Sprintf("0x%x", code[0]), Kind: Invalid}
	}
	return Inst{Addr: addr, Len: n, Mnemonic: x.mn, Operands: strings.Join(x.ops, ", "),
		Kind: x.kind, Target: x.tgt, HasTarget: x.has, Effects: x.eff}
}

// xtNarrow decodes a 16-bit instruction
func xtNarrow(x *xtInst, w uint32, addr uint64) {
	op0 := int(w & 15)
	t := int(w>>4) & 15
	s := int(w>>8) & 15
	r := int(w>>12) & 15
	switch op0 {
	case 8:
		x.set("l32i.n", xa(t), xa(s), xi(int64(r*4)))
		x.mem(t, s, int64(r*4), 4, false)
	case 9:
		x.set("s32i.n", xa(t), xa(s), xi(int64(r*4)))
		x.mem(t, s, int64(r*4), 4, true)
	case 0xA:
		x.set("add.n", xa(r), xa(s), xa(t))
		x.clobber(r)
	case 0xB:
		imm := int64(t)
		if t == 0 {
			imm = -1
		}
		x.set("addi.n", xa(r), xa(s), xi(imm))
		x.eff = append(x.eff, Effect{Kind: AddImm, Dst: r, Src: s, Base: -1, Imm: imm})
	case 0xC:
		if t&8 == 0 {
			imm := int64(t&7)<<4 | int64(r)
			if imm >= 96 {
				imm -= 128
			}
			x.set("movi.n", xa(s), xi(imm))
			x.eff = append(x.eff, Effect{Kind: SetConst, Dst: s, Src: -1, Base: -1, Imm: imm})
			return
		}
		mn := "beqz.n"
		if t&4 != 0 {
			mn = "bnez.n"
		}
		tg := addr + 4 + uint64(t&3)<<4 + uint64(r)
		x.set(mn, xa(s), xaddr(tg))
		x.jump(Branch, tg)
	case 0xD:
		switch r {
		case 0:
			x.set("mov.n", xa(t), xa(s))
			x.clobber(t)
		case 15:
			switch {
			case t == 0:
				// objdump takes any s here, as for ret
				x.set("ret.n")
				x.kind = Return
			case t == 1 && s == 0:
				x.set("retw.n")
				x.kind = Return
			case t == 2:
				x.set("break.n", xi(int64(s)))
				x.kind = Interrupt
			case t == 3 && s == 0:
				x.set("nop.n")
			case t == 6 && s == 0:
				x.set("ill.n")
			}
		}
	}
}

// mem notes a load or a store of size bytes at as + off; a load also
// clobbers the register it loads
func (x *xtInst) mem(at, as int, off int64, size int, store bool) {
	e := Effect{Kind: MemAccess, Dst: -1, Src: -1, Base: as, Imm: off, Size: size, Store: store}
	if store {
		e.Src = at
	} else {
		e.Dst = at
	}
	x.eff = append(x.eff, e)
	if !store {
		x.clobber(at)
	}
}

// xtWide decodes a 24-bit instruction
func xtWide(x *xtInst, w uint32, addr uint64) {
	op0 := int(w & 15)
	t := int(w>>4) & 15
	s := int(w>>8) & 15
	r := int(w>>12) & 15
	op1 := int(w>>16) & 15
	op2 := int(w>>20) & 15
	imm8 := int64(w>>16) & 0xFF
	switch op0 {
	case 0:
		xtQRST(x, t, s, r, op1, op2)
	case 1:
		// l32r: the literal is below the instruction, word aligned
		off := int64(int32(0xFFFF0000|w>>8)) << 2
		a := uint64(int64((addr+3)&^3)+off) & 0xFFFFFFFF
		x.set("l32r", xa(t), xaddr(a))
		x.eff = append(x.eff, Effect{Kind: LoadLiteral, Dst: t, Src: -1, Base: -1, Addr: a, Size: 4})
	case 2:
		xtLSAI(x, t, s, r, imm8)
	case 3:
		off := imm8 << 2
		switch r {
		case 0:
			x.set("lsi", xf(t), xa(s), xi(off))
		case 4:
			x.set("ssi", xf(t), xa(s), xi(off))
		case 8:
			x.set("lsiu", xf(t), xa(s), xi(off))
			x.clobber(s)
		case 12:
			x.set("ssiu", xf(t), xa(s), xi(off))
			x.clobber(s)
		}
	case 4:
		xtMAC16(x, t, s, r, op1, op2)
	case 5:
		off := xtSext(w>>6, 18)
		tg := uint64(int64(addr&^3) + off<<2 + 4)
		x.set([4]string{"call0", "call4", "call8", "call12"}[t&3], xaddr(tg))
		x.jump(Call, tg)
	case 6:
		xtSI(x, w, t, s, r, imm8, addr)
	case 7:
		tg := uint64(int64(addr) + 4 + xtSext(uint32(imm8), 8))
		switch r {
		case 6, 7, 14, 15:
			mn := "bbci"
			if r >= 14 {
				mn = "bbsi"
			}
			x.set(mn, xa(s), xi(int64(r&1)<<4|int64(t)), xaddr(tg))
		default:
			mn := [16]string{"bnone", "beq", "blt", "bltu", "ball", "bbc", "", "", "bany", "bne", "bge", "bgeu", "bnall", "bbs"}[r]
			x.set(mn, xa(s), xa(t), xaddr(tg))
		}
		x.jump(Branch, tg)
	}
}

// xtQRST decodes the instructions of op0 = 0: register operations
func xtQRST(x *xtInst, t, s, r, op1, op2 int) {
	switch op1 {
	case 0:
		xtRST0(x, t, s, r, op2)
	case 1:
		xtRST1(x, t, s, r, op2)
	case 2:
		switch op2 {
		case 0, 1, 2, 3, 4:
			x.set([5]string{"andb", "andbc", "orb", "orbc", "xorb"}[op2], xb(r), xb(s), xb(t))
		case 8, 10, 11, 12, 13, 14, 15:
			mn := [16]string{8: "mull", 10: "muluh", 11: "mulsh", 12: "quou", 13: "quos", 14: "remu", 15: "rems"}[op2]
			x.set(mn, xa(r), xa(s), xa(t))
			x.clobber(r)
		}
	case 3:
		xtRST3(x, t, s, r, op2)
	case 4, 5:
		x.set("extui", xa(r), xa(t), xi(int64(op1&1)<<4|int64(s)), xi(int64(op2)+1))
		x.clobber(r)
	case 8:
		switch op2 {
		case 0, 1, 4, 5:
			mn := [6]string{"lsx", "lsxu", "", "", "ssx", "ssxu"}[op2]
			x.set(mn, xf(r), xa(s), xa(t))
			if op2&1 != 0 {
				x.clobber(s)
			}
		}
	case 9:
		off := int64(r)*4 - 64
		switch op2 {
		case 0:
			x.set("l32e", xa(t), xa(s), xi(off))
			x.clobber(t)
		case 4:
			x.set("s32e", xa(t), xa(s), xi(off))
		}
	case 10:
		xtFP0(x, t, s, r, op2)
	case 11:
		switch {
		case op2 >= 1 && op2 <= 7:
			mn := [8]string{"", "un.s", "oeq.s", "ueq.s", "olt.s", "ult.s", "ole.s", "ule.s"}[op2]
			x.set(mn, xb(r), xf(s), xf(t))
		case op2 >= 8 && op2 <= 11:
			mn := [4]string{"moveqz.s", "movnez.s", "movltz.s", "movgez.s"}[op2-8]
			x.set(mn, xf(r), xf(s), xa(t))
		case op2 == 12 || op2 == 13:
			mn := [2]string{"movf.s", "movt.s"}[op2-12]
			x.set(mn, xf(r), xf(s), xb(t))
		}
	}
}

func xtRST0(x *xtInst, t, s, r, op2 int) {
	switch op2 {
	case 0:
		xtST0(x, t, s, r)
	case 1, 2, 3:
		x.set([4]string{"", "and", "or", "xor"}[op2], xa(r), xa(s), xa(t))
		x.clobber(r)
	case 4:
		xtST1(x, t, s, r)
	case 5:
		switch r {
		case 3, 5, 6, 7, 11, 13, 14, 15:
			mn := [16]string{3: "ritlb0", 5: "pitlb", 6: "witlb", 7: "ritlb1", 11: "rdtlb0", 13: "pdtlb", 14: "wdtlb", 15: "rdtlb1"}[r]
			x.set(mn, xa(t), xa(s))
			if r != 6 && r != 14 {
				x.clobber(t)
			}
		case 4, 12:
			if t == 0 {
				x.set([2]string{"iitlb", "idtlb"}[r/12], xa(s))
			}
		}
	case 6:
		switch s {
		case 0:
			x.set("neg", xa(r), xa(t))
			x.clobber(r)
		case 1:
			x.set("abs", xa(r), xa(t))
			x.clobber(r)
		}
	case 8, 9, 10, 11, 12, 13, 14, 15:
		mn := [8]string{"add", "addx2", "addx4", "addx8", "sub", "subx2", "subx4", "subx8"}[op2-8]
		x.set(mn, xa(r), xa(s), xa(t))
		x.clobber(r)
	}
}

func xtST0(x *xtInst, t, s, r int) {
	switch r {
	case 0:
		m, n := t>>2, t&3
		switch {
		case m == 0 && n == 0 && s == 0:
			x.set("ill")
		case m == 2 && n == 0:
			x.set("ret")
			x.kind = Return
		case m == 2 && n == 1 && s == 0:
			x.set("retw")
			x.kind = Return
		case m == 2 && n == 2:
			x.set("jx", xa(s))
			x.kind = Jump
			x.eff = append(x.eff, Effect{Kind: JumpReg, Src: s, Dst: -1, Base: -1})
		case m == 3:
			x.set([4]string{"callx0", "callx4", "callx8", "callx12"}[n], xa(s))
			x.kind = Call
			x.eff = append(x.eff, Effect{Kind: JumpReg, Src: s, Dst: -1, Base: -1})
		}
	case 1:
		x.set("movsp", xa(t), xa(s))
		x.clobber(t)
	case 2:
		if s == 0 {
			mn := [16]string{0: "isync", 1: "rsync", 2: "esync", 3: "dsync", 8: "excw", 12: "memw", 13: "extw", 15: "nop"}[t]
			if mn != "" {
				x.set(mn)
			}
		}
	case 3:
		switch {
		case t == 0:
			mn := [16]string{0: "rfe", 1: "rfue", 2: "rfde", 4: "rfwo", 5: "rfwu"}[s]
			if mn != "" {
				x.set(mn)
				x.kind = Return
			}
		case t == 1:
			x.set("rfi", xi(int64(s)))
			x.kind = Return
		case t == 2 && s == 0:
			x.set("rfme")
			x.kind = Return
		}
	case 4:
		x.set("break", xi(int64(s)), xi(int64(t)))
		x.kind = Interrupt
	case 5:
		if t == 0 && s == 0 {
			x.set("syscall")
			x.kind = Interrupt
		} else if t == 0 && s == 1 {
			x.set("simcall")
			x.kind = Interrupt
		}
	case 6:
		x.set("rsil", xa(t), xi(int64(s)))
		x.clobber(t)
	case 7:
		if t == 0 {
			x.set("waiti", xi(int64(s)))
		}
	case 8, 9, 10, 11:
		x.set([4]string{"any4", "all4", "any8", "all8"}[r-8], xb(t), xb(s))
	}
}

func xtST1(x *xtInst, t, s, r int) {
	switch r {
	case 0, 1, 2, 3:
		if t == 0 {
			x.set([4]string{"ssr", "ssl", "ssa8l", "ssa8b"}[r], xa(s))
		}
	case 4:
		if t&14 == 0 {
			x.set("ssai", xi(int64(t&1)<<4|int64(s)))
		}
	case 6:
		x.set("rer", xa(t), xa(s))
		x.clobber(t)
	case 7:
		x.set("wer", xa(t), xa(s))
	case 8:
		if s == 0 {
			x.set("rotw", xi(xtSext(uint32(t), 4)))
		}
	case 14, 15:
		x.set([2]string{"nsa", "nsau"}[r-14], xa(t), xa(s))
		x.clobber(t)
	}
}

func xtRST1(x *xtInst, t, s, r, op2 int) {
	switch op2 {
	case 0, 1:
		x.set("slli", xa(r), xa(s), xi(32-(int64(op2&1)<<4|int64(t))))
	case 2, 3:
		x.set("srai", xa(r), xa(t), xi(int64(op2&1)<<4|int64(s)))
	case 4:
		x.set("srli", xa(r), xa(t), xi(int64(s)))
	case 6:
		if name, ok := xtSpecial[r<<4|s]; ok && name != "intclear" {
			x.set("xsr."+name, xa(t))
			x.clobber(t)
			return
		}
		return
	case 8:
		x.set("src", xa(r), xa(s), xa(t))
	case 9:
		if s == 0 {
			x.set("srl", xa(r), xa(t))
		}
	case 10:
		if t == 0 {
			x.set("sll", xa(r), xa(s))
		}
	case 11:
		if s == 0 {
			x.set("sra", xa(r), xa(t))
		}
	case 12, 13:
		x.set([2]string{"mul16u", "mul16s"}[op2-12], xa(r), xa(s), xa(t))
	}
	if x.mn != "" {
		x.clobber(r)
	}
}

func xtRST3(x *xtInst, t, s, r, op2 int) {
	switch op2 {
	case 0, 1:
		sr := r<<4 | s
		name, ok := xtSpecial[sr]
		if !ok {
			return
		}
		if op2 == 0 {
			if sr == 227 {
				return
			}
			x.set("rsr."+name, xa(t))
			x.clobber(t)
		} else {
			if sr == 226 {
				name = "intset"
			}
			x.set("wsr."+name, xa(t))
		}
		return
	case 2, 3:
		x.set([2]string{"sext", "clamps"}[op2-2], xa(r), xa(s), xi(int64(t)+7))
	case 4, 5, 6, 7, 8, 9, 10, 11:
		mn := [8]string{"min", "max", "minu", "maxu", "moveqz", "movnez", "movltz", "movgez"}[op2-4]
		x.set(mn, xa(r), xa(s), xa(t))
	case 12, 13:
		x.set([2]string{"movf", "movt"}[op2-12], xa(r), xa(s), xb(t))
	case 14:
		ur := s<<4 | t
		if name, ok := xtUser[ur]; ok {
			x.set("rur."+name, xa(r))
		} else {
			x.set("rur", xa(r), xi(int64(ur)))
		}
	case 15:
		ur := r<<4 | s
		if name, ok := xtUser[ur]; ok {
			x.set("wur."+name, xa(t))
		} else {
			x.set("wur", xa(t), xi(int64(ur)))
		}
		return
	}
	if x.mn != "" {
		x.clobber(r)
	}
}

func xtFP0(x *xtInst, t, s, r, op2 int) {
	switch op2 {
	case 0, 1, 2, 4, 5, 6, 7:
		mn := [8]string{"add.s", "sub.s", "mul.s", "", "madd.s", "msub.s", "maddn.s", "divn.s"}[op2]
		x.set(mn, xf(r), xf(s), xf(t))
	case 8, 9, 10, 11, 14:
		mn := [7]string{"round.s", "trunc.s", "floor.s", "ceil.s", "", "", "utrunc.s"}[op2-8]
		x.set(mn, xa(r), xf(s), xi(int64(t)))
		x.clobber(r)
	case 12, 13:
		x.set([2]string{"float.s", "ufloat.s"}[op2-12], xf(r), xa(s), xi(int64(t)))
	case 15:
		switch t {
		case 0, 1, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15:
			mn := [16]string{0: "mov.s", 1: "abs.s", 6: "neg.s", 7: "div0.s", 8: "recip0.s", 9: "sqrt0.s",
				10: "rsqrt0.s", 11: "nexp01.s", 12: "mksadj.s", 13: "mkdadj.s", 14: "addexp.s", 15: "addexpm.s"}[t]
			x.set(mn, xf(r), xf(s))
		case 3:
			x.set("const.s", xf(r), xi(int64(s)))
		case 4:
			x.set("rfr", xa(r), xf(s))
			x.clobber(r)
		case 5:
			x.set("wfr", xf(r), xa(s))
		}
	}
}

// xtLSAI decodes the loads, the stores and the immediates of op0 = 2
func xtLSAI(x *xtInst, t, s, r int, imm8 int64) {
	switch r {
	case 0, 1, 2, 4, 5, 6, 9:
		size := [16]int{1, 2, 4, 0, 1, 2, 4, 0, 0, 2}[r]
		mn := [16]string{"l8ui", "l16ui", "l32i", "", "s8i", "s16i", "s32i", "", "", "l16si"}[r]
		off := imm8 * int64(size)
		x.set(mn, xa(t), xa(s), xi(off))
		x.mem(t, s, off, size, r >= 4 && r <= 6)
	case 10:
		imm := xtSext(uint32(s)<<8|uint32(imm8), 12)
		x.set("movi", xa(t), xi(imm))
		x.eff = append(x.eff, Effect{Kind: SetConst, Dst: t, Src: -1, Base: -1, Imm: imm})
	case 11:
		x.set("l32ai", xa(t), xa(s), xi(imm8<<2))
		x.mem(t, s, imm8<<2, 4, false)
	case 12, 13:
		imm := xtSext(uint32(imm8), 8)
		mn := "addi"
		if r == 13 {
			mn, imm = "addmi", imm<<8
		}
		x.set(mn, xa(t), xa(s), xi(imm))
		x.eff = append(x.eff, Effect{Kind: AddImm, Dst: t, Src: s, Base: -1, Imm: imm})
	case 14:
		x.set("s32c1i", xa(t), xa(s), xi(imm8<<2))
		x.clobber(t)
	case 15:
		x.set("s32ri", xa(t), xa(s), xi(imm8<<2))
		x.mem(t, s, imm8<<2, 4, true)
	}
}

// xtSI decodes the jumps, the branches with an immediate, entry and loops
func xtSI(x *xtInst, w uint32, t, s, r int, imm8 int64, addr uint64) {
	n, m := t&3, t>>2
	b8 := uint64(int64(addr) + 4 + xtSext(uint32(imm8), 8))
	switch n {
	case 0:
		tg := uint64(int64(addr) + 4 + xtSext(w>>6, 18))
		x.set("j", xaddr(tg))
		x.jump(Jump, tg)
	case 1:
		tg := uint64(int64(addr) + 4 + xtSext(w>>12, 12))
		x.set([4]string{"beqz", "bnez", "bltz", "bgez"}[m], xa(s), xaddr(tg))
		x.jump(Branch, tg)
	case 2:
		x.set([4]string{"beqi", "bnei", "blti", "bgei"}[m], xa(s), xi(xtB4const[r]), xaddr(b8))
		x.jump(Branch, b8)
	case 3:
		switch m {
		case 0:
			x.set("entry", xa(s), xi(int64(w>>12)<<3))
			x.clobber(1)
		case 1:
			switch r {
			case 0, 1:
				x.set([2]string{"bf", "bt"}[r], xb(s), xaddr(b8))
				x.jump(Branch, b8)
			case 8, 9, 10:
				end := addr + 4 + uint64(imm8)
				x.set([3]string{"loop", "loopnez", "loopgtz"}[r-8], xa(s), xaddr(end))
				x.jump(Branch, end)
			}
		case 2, 3:
			x.set([2]string{"bltui", "bgeui"}[m-2], xa(s), xi(xtB4constu[r]), xaddr(b8))
			x.jump(Branch, b8)
		}
	}
}

// xtMAC16 decodes the multiply-accumulate instructions (op0 = 4)
func xtMAC16(x *xtInst, t, s, r, op1, op2 int) {
	half := [4]string{"ll", "hl", "lh", "hh"}[op1&3]
	opc := [4]string{"umul", "mul", "mula", "muls"}[op1>>2]
	mr := func(i int) string { return fmt.Sprintf("m%d", i) }
	mx, my, mw := r>>2&1, 2+(t>>2&1), r&3
	xok := r&8 == 0 && r&3 == 0
	yok := t&11 == 0
	switch op2 {
	case 0, 1, 4, 5:
		// mula with a load and an update of as
		if op1>>2 != 2 || r&8 != 0 {
			return
		}
		ld := [6]string{"ldinc", "lddec", "", "", "ldinc", "lddec"}[op2]
		if op2 <= 1 {
			if !yok {
				return
			}
			x.set("mula.dd."+half+"."+ld, mr(mw), xa(s), mr(mx), mr(my))
		} else {
			x.set("mula.da."+half+"."+ld, mr(mw), xa(s), mr(mx), xa(t))
		}
		x.clobber(s)
	case 2:
		if op1>>2 != 0 && xok && yok && s == 0 {
			x.set(opc+".dd."+half, mr(mx), mr(my))
		}
	case 3:
		if op1>>2 != 0 && r == 0 && yok {
			x.set(opc+".ad."+half, xa(s), mr(my))
		}
	case 6:
		if op1>>2 != 0 && xok && s == 0 {
			x.set(opc+".da."+half, mr(mx), xa(t))
		}
	case 7:
		if r == 0 {
			x.set(opc+".aa."+half, xa(s), xa(t))
		}
	case 8, 9:
		if op1 == 0 && t == 0 && r&12 == 0 {
			x.set([2]string{"ldinc", "lddec"}[op2-8], mr(mw), xa(s))
			x.clobber(s)
		}
	}
}
