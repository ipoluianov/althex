package disasm

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
)

// armArch is ARM: Thumb and Thumb-2 of the Cortex-M cores (ARMv6-M,
// ARMv7-M, ARMv7E-M, ARMv8-M, with the DSP and the FP extensions) by its
// own decoder, and A32 by golang.org/x/arch; the text is that of GNU
// objdump (arm-none-eabi-objdump -M reg-names-std)
type armArch struct {
	id, name string
	a32      bool // ModeDefault is A32, not Thumb

	mu sync.Mutex
	it armIT
}

// armIT is what an IT instruction left for the instructions after it:
// Decode is called on one instruction at a time, so the conditions of the
// block are kept for the instruction at next
type armIT struct {
	next  uint64
	conds [4]uint8
	n     int
}

func init() {
	register(&armArch{id: "arm", name: "ARM (Thumb, Cortex-M)"})
	register(&armArch{id: "arm-a32", name: "ARM (A32)", a32: true})
}

func (a *armArch) ID() string   { return a.id }
func (a *armArch) Name() string { return a.name }
func (a *armArch) MaxLen() int  { return 4 }

func (a *armArch) Decode(code []byte, addr uint64, mode int) Inst {
	if mode == ModeDefault {
		mode = ModeThumb
		if a.a32 {
			mode = ModeARM
		}
	}
	if mode == ModeARM {
		return decodeA32(code, addr)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	cond := -1
	it := a.it
	a.it = armIT{}
	if it.n > 0 && it.next == addr {
		cond = int(it.conds[0])
	}
	in, d := decodeThumbIT(code, addr, cond, it.n == 1)
	if in.Len == 0 {
		return in
	}
	switch {
	case d.itN > 0:
		// A new IT block, also when inside one (objdump does the same)
		a.it = armIT{next: addr + uint64(in.Len), conds: d.itConds, n: d.itN}
	case cond >= 0 && it.n > 1:
		a.it = armIT{next: addr + uint64(in.Len), n: it.n - 1}
		copy(a.it.conds[:], it.conds[1:it.n])
	}
	return in
}

// Register names: objdump -M reg-names-std
var armRegs = [16]string{"r0", "r1", "r2", "r3", "r4", "r5", "r6", "r7",
	"r8", "r9", "r10", "r11", "r12", "sp", "lr", "pc"}

var armConds = [16]string{"eq", "ne", "cs", "cc", "mi", "pl", "vs", "vc",
	"hi", "ls", "ge", "lt", "gt", "le", "al", ""}

// itCond is the name of a condition of an IT block; objdump writes the
// condition 0b1111, which is not one, as "<und>"
func itCond(c int) string {
	if c == 15 {
		return "<und>"
	}
	return armConds[c]
}

// Registers of the effects
const (
	armSP = 13
	armLR = 14
	armPC = 15
)

// tdec is the state of decoding one Thumb instruction
type tdec struct {
	addr     uint64
	cond     int  // the IT condition, -1 outside an IT block
	itLast   bool // the instruction is the last of its IT block
	in       Inst
	comments []string
	itConds  [4]uint8 // the conditions an IT instruction sets
	itN      int
}

// armByte is a byte at the end of the code, too short for an instruction;
// nothing for no bytes
func armByte(code []byte, addr uint64) Inst {
	if len(code) == 0 {
		return Inst{}
	}
	return Inst{Addr: addr, Len: 1, Mnemonic: ".byte", Operands: fmt.Sprintf("0x%02x", code[0]), Kind: Invalid}
}

func decodeThumbIT(code []byte, addr uint64, cond int, last bool) (Inst, *tdec) {
	d := &tdec{addr: addr, cond: cond, itLast: last}
	d.in.Addr = addr
	if len(code) < 2 {
		return armByte(code, addr), d
	}
	h := binary.LittleEndian.Uint16(code)
	if h>>11 >= 0x1D {
		if len(code) < 4 {
			d.in.Len = 2
			d.undef16(h)
		} else {
			d.in.Len = 4
			d.thumb32(h, binary.LittleEndian.Uint16(code[2:]))
		}
	} else {
		d.in.Len = 2
		d.thumb16(h)
	}
	if cond >= 0 && cond != 14 {
		// Inside an IT block a jump is taken or not: a branch
		if d.in.Kind == Jump || d.in.Kind == Return {
			d.in.Kind = Branch
		}
		conditional(d.in.Effects)
	}
	d.in.Comment = strings.Join(d.comments, ", ")
	return d.in, d
}

// conditional makes the effects of an instruction that may not execute
// ones the analysis cannot be wrong with: a register it sets is not known
// after it (the accesses to memory stay, they may be made)
func conditional(effs []Effect) {
	for i, e := range effs {
		switch e.Kind {
		case SetConst, SetLow16, SetHigh16, AddImm, LoadLiteral:
			effs[i] = Effect{Kind: Clobber, Dst: e.Dst, Src: -1, Base: -1}
		}
	}
}

// c is the condition of the IT block for the mnemonic
func (d *tdec) c() string {
	if d.cond < 0 {
		return ""
	}
	return itCond(d.cond)
}

// s is the "s" of a 16-bit instruction that sets the flags outside an IT
// block and does not inside it, where the condition is shown instead
func (d *tdec) s() string {
	if d.cond < 0 {
		return "s"
	}
	return itCond(d.cond)
}

func (d *tdec) set(mn, ops string, args ...any) {
	d.in.Mnemonic = mn
	if len(args) > 0 {
		ops = fmt.Sprintf(ops, args...)
	}
	d.in.Operands = ops
}

func (d *tdec) note(format string, args ...any) {
	d.comments = append(d.comments, fmt.Sprintf(format, args...))
}

// imm is an immediate as objdump shows it: decimal, and in hex in the
// comment when it is not small
func (d *tdec) imm(v int64) string {
	if v > 32 || v < -16 {
		d.note("0x%x", uint32(v))
	}
	return fmt.Sprintf("#%d", v)
}

// reset forgets what was decoded, to decode the instruction another way
func (d *tdec) reset() {
	d.in = Inst{Addr: d.addr, Len: d.in.Len}
	d.comments = nil
}

func (d *tdec) undef16(h uint16) {
	d.in = Inst{Addr: d.addr, Len: 2, Mnemonic: ".short", Operands: fmt.Sprintf("0x%04x", h), Kind: Invalid}
	d.comments = []string{"undefined"}
}

func (d *tdec) undef32(h1, h2 uint16) {
	d.in = Inst{Addr: d.addr, Len: 4, Mnemonic: ".word", Operands: fmt.Sprintf("0x%04x%04x", h1, h2), Kind: Invalid}
	d.comments = []string{"undefined"}
}

// pc is the value of the PC an instruction reads: its address + 4
func (d *tdec) pc() uint32 { return uint32(d.addr) + 4 }

// lit is the address of a PC-relative literal: Align(PC, 4) + off
func (d *tdec) lit(off int64) uint32 { return uint32(int64(d.pc()&^3) + off) }

// branchInIT notes, as objdump does, a branch that is in an IT block but
// not the last of it
func (d *tdec) branchInIT() {
	if d.cond >= 0 && !d.itLast {
		d.note("unpredictable branch in IT block")
	}
}

func (d *tdec) branch(kind Kind, target uint32) {
	d.in.Kind = kind
	d.in.Target, d.in.HasTarget = uint64(target), true
}

// Effects

func (d *tdec) eff(e Effect) { d.in.Effects = append(d.in.Effects, e) }

func (d *tdec) clob(regs ...int) {
	for _, r := range regs {
		if r != armPC {
			d.eff(Effect{Kind: Clobber, Dst: r, Src: -1, Base: -1})
		}
	}
}

func (d *tdec) clobList(list uint16) {
	for r := 0; r < 15; r++ {
		if list&(1<<r) != 0 {
			d.clob(r)
		}
	}
}

func (d *tdec) setConst(r int, v uint32) {
	if r != armPC {
		d.eff(Effect{Kind: SetConst, Dst: r, Src: -1, Base: -1, Imm: int64(v)})
	}
}

func (d *tdec) addImm(dst, src int, v int64) {
	if dst != armPC {
		d.eff(Effect{Kind: AddImm, Dst: dst, Src: src, Base: -1, Imm: v})
	}
}

// mem is a load or a store of size bytes at base+off; rt is the register
// loaded or stored
func (d *tdec) mem(rt, base int, off int64, size int, store bool) {
	e := Effect{Kind: MemAccess, Dst: -1, Src: -1, Base: base, Imm: off, Size: size, Store: store}
	if store {
		e.Src = rt
	} else {
		e.Dst = rt
	}
	if base == armPC {
		e.Base, e.Addr = -1, uint64(d.lit(off))
	}
	d.eff(e)
}

// load is mem of a load, with the clobber of the register loaded
func (d *tdec) load(rt, base int, off int64, size int) {
	d.mem(rt, base, off, size, false)
	d.clob(rt)
}

func (d *tdec) loadLiteral(rt int, addr uint32, size int) {
	if rt != armPC {
		d.eff(Effect{Kind: LoadLiteral, Dst: rt, Src: -1, Base: -1, Addr: uint64(addr), Size: size})
	}
}

// call clobbers what a call does by the AAPCS: r0-r3, r12 and lr
func (d *tdec) call() { d.clob(0, 1, 2, 3, 12, armLR) }

// regList is "{r4, r5, lr}"
func regList(list uint16) string {
	var sb strings.Builder
	sb.WriteByte('{')
	for r := 0; r < 16; r++ {
		if list&(1<<r) != 0 {
			if sb.Len() > 1 {
				sb.WriteString(", ")
			}
			sb.WriteString(armRegs[r])
		}
	}
	sb.WriteByte('}')
	return sb.String()
}

func popCount16(v uint16) int {
	n := 0
	for ; v != 0; v &= v - 1 {
		n++
	}
	return n
}

// shiftText is ", lsl #3" of a shift by an immediate as encoded (type,
// imm5); "" for none
func shiftText(typ, imm uint32) string {
	switch typ {
	case 0:
		if imm == 0 {
			return ""
		}
		return fmt.Sprintf(", lsl #%d", imm)
	case 1, 2:
		if imm == 0 {
			imm = 32
		}
		return fmt.Sprintf(", %s #%d", [4]string{"", "lsr", "asr"}[typ], imm)
	}
	if imm == 0 {
		return ", rrx"
	}
	return fmt.Sprintf(", ror #%d", imm)
}

// vfpImm is the value of an 8-bit floating-point immediate (VFPExpandImm)
func vfpImm(imm8 uint32) float64 {
	sign := 1.0
	if imm8&0x80 != 0 {
		sign = -1
	}
	exp := int(imm8>>4) & 7 // b:cd: the exponent is (NOT b):cd - 3, biased
	e := exp&3 + 1
	if exp&4 != 0 {
		e = exp&3 - 3
	}
	return sign * (16 + float64(imm8&15)) / 16 * math.Pow(2, float64(e))
}

// floatText is the value of a floating-point immediate as objdump writes
// it in a comment: the shortest of 1, 3 or 7 decimals that is exact
func floatText(v float64) string {
	s := ""
	for _, p := range []int{1, 3, 7} {
		s = strconv.FormatFloat(v, 'f', p, 64)
		if f, _ := strconv.ParseFloat(s, 64); f == v {
			break
		}
	}
	if v >= 0 {
		s = " " + s
	}
	return s
}
