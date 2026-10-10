package disasm

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// riscv decodes RISC-V as GNU objdump writes it, with its aliases (li, mv,
// j, ret, nop, beqz...): RV32IMAC for the microcontrollers (ESP32-C3/C6,
// GD32V, CH32V) and RV64GC (IMAFD, Zicsr, Zifencei, C) for the rest. RV32
// also decodes F and D, as objdump does. Instructions are 4 bytes, or 2 when
// compressed.
type riscv struct {
	xlen int
}

func init() {
	register(riscv{32})
	register(riscv{64})
}

func (a riscv) ID() string { return fmt.Sprintf("riscv%d", a.xlen) }

func (a riscv) Name() string {
	if a.xlen == 32 {
		return "RISC-V 32-bit (RV32IMAC)"
	}
	return "RISC-V 64-bit (RV64GC)"
}

func (a riscv) MaxLen() int { return 4 }

// The registers by their ABI names
var (
	riscvX = [32]string{"zero", "ra", "sp", "gp", "tp", "t0", "t1", "t2", "s0", "s1", "a0", "a1", "a2", "a3", "a4", "a5",
		"a6", "a7", "s2", "s3", "s4", "s5", "s6", "s7", "s8", "s9", "s10", "s11", "t3", "t4", "t5", "t6"}
	riscvF = [32]string{"ft0", "ft1", "ft2", "ft3", "ft4", "ft5", "ft6", "ft7", "fs0", "fs1", "fa0", "fa1", "fa2", "fa3", "fa4", "fa5",
		"fa6", "fa7", "fs2", "fs3", "fs4", "fs5", "fs6", "fs7", "fs8", "fs9", "fs10", "fs11", "ft8", "ft9", "ft10", "ft11"}
	riscvRM = [8]string{"rne", "rtz", "rdn", "rup", "rmm", "unknown", "unknown", "dyn"}
)

const riscvRA, riscvSP = 1, 2

// riscvInst is an instruction being decoded
type riscvInst struct {
	*Inst
	xlen int
}

func (a riscv) Decode(code []byte, addr uint64, mode int) Inst {
	if len(code) < 2 {
		if len(code) == 0 {
			return Inst{}
		}
		return Inst{Addr: addr, Len: 1, Mnemonic: ".byte", Operands: fmt.Sprintf("0x%02x", code[0]), Kind: Invalid}
	}
	in := Inst{Addr: addr}
	r := riscvInst{&in, a.xlen}
	h := binary.LittleEndian.Uint16(code)
	switch {
	case h&3 != 3:
		in.Len = 2
		if !r.compressed(h) {
			in = Inst{Addr: addr, Len: 2, Mnemonic: ".insn", Operands: fmt.Sprintf("2, 0x%04x", h), Kind: Invalid}
		}
	case h&0x1C != 0x1C && len(code) >= 4:
		w := binary.LittleEndian.Uint32(code)
		in.Len = 4
		if !r.decode32(w) {
			// objdump leaves out a high half that is 0
			ops := fmt.Sprintf("4, 0x%08x", w)
			if w>>16 == 0 {
				ops = fmt.Sprintf("4, 0x%04x", w)
			}
			in = Inst{Addr: addr, Len: 4, Mnemonic: ".insn", Operands: ops, Kind: Invalid}
		}
	default:
		// The longer instructions (48 bits and more) or a cut one: shown
		// one parcel at a time
		in.Len = 2
		in.Mnemonic, in.Operands, in.Kind = ".2byte", fmt.Sprintf("0x%04x", h), Invalid
	}
	return in
}

// set writes the text of an instruction
func (r riscvInst) set(mn string, ops ...string) {
	r.Mnemonic = mn
	r.Operands = strings.Join(ops, ",")
}

// x is the name of an integer register
func (r riscvInst) x(n uint32) string { return riscvX[n&31] }

// mask cuts a value to the width of the registers
func (r riscvInst) mask(v int64) uint64 {
	if r.xlen == 32 {
		return uint64(uint32(v))
	}
	return uint64(v)
}

// target sets where a jump, a branch or a call goes and writes it
func (r riscvInst) target(k Kind, off int64) string {
	r.Kind, r.Target, r.HasTarget = k, r.mask(int64(r.Addr)+off), true
	return fmt.Sprintf("0x%x", r.Target)
}

// clobber notes that a register gets a value that is not known
func (r riscvInst) clobber(rd uint32) {
	if rd != 0 {
		r.Effects = append(r.Effects, Effect{Kind: Clobber, Dst: int(rd), Src: -1, Base: -1})
	}
}

// setConst notes that a register gets a constant
func (r riscvInst) setConst(rd uint32, v int64) {
	if rd != 0 {
		r.Effects = append(r.Effects, Effect{Kind: SetConst, Dst: int(rd), Src: -1, Base: -1, Imm: int64(r.mask(v))})
	}
}

// addImm notes rd = rs + imm
func (r riscvInst) addImm(rd, rs uint32, imm int64) {
	if rd == 0 {
		return
	}
	if rs == 0 {
		r.setConst(rd, imm)
		return
	}
	r.Effects = append(r.Effects, Effect{Kind: AddImm, Dst: int(rd), Src: int(rs), Base: -1, Imm: imm})
}

// load writes a load of an integer register and notes it
func (r riscvInst) load(mn string, size int, rd, rs uint32, imm int64) {
	r.set(mn, r.x(rd), fmt.Sprintf("%d(%s)", imm, r.x(rs)))
	dst := int(rd)
	if rd == 0 {
		dst = -1
	}
	r.Effects = append(r.Effects, Effect{Kind: MemAccess, Dst: dst, Src: -1, Base: int(rs), Imm: imm, Size: size})
	r.clobber(rd)
}

// store writes a store of an integer register and notes it
func (r riscvInst) store(mn string, size int, rs2, rs uint32, imm int64) {
	r.set(mn, r.x(rs2), fmt.Sprintf("%d(%s)", imm, r.x(rs)))
	r.Effects = append(r.Effects, Effect{Kind: MemAccess, Dst: -1, Src: int(rs2), Base: int(rs), Imm: imm, Size: size, Store: true})
}

// fmem writes a load or a store of a floating-point register and notes the
// access
func (r riscvInst) fmem(mn string, size int, fr, rs uint32, imm int64, st bool) {
	r.set(mn, riscvF[fr&31], fmt.Sprintf("%d(%s)", imm, r.x(rs)))
	r.Effects = append(r.Effects, Effect{Kind: MemAccess, Dst: -1, Src: -1, Base: int(rs), Imm: imm, Size: size, Store: st})
}

// riscvSext sign-extends the low bits of a value
func riscvSext(v uint32, bits uint) int64 {
	return int64(int32(v<<(32-bits)) >> (32 - bits))
}

// decode32 decodes a 32-bit instruction; false if it is not one
func (r riscvInst) decode32(w uint32) bool {
	rd, rs1, rs2 := w>>7&31, w>>15&31, w>>20&31
	f3, f7 := w>>12&7, w>>25
	immI := riscvSext(w>>20, 12)
	immS := riscvSext(w>>25<<5|w>>7&31, 12)
	rv64 := r.xlen == 64
	switch w & 0x7F {
	case 0x37: // lui
		r.set("lui", r.x(rd), fmt.Sprintf("0x%x", w>>12))
		r.setConst(rd, int64(int32(w&0xFFFFF000)))
	case 0x17: // auipc
		r.set("auipc", r.x(rd), fmt.Sprintf("0x%x", w>>12))
		r.setConst(rd, int64(r.Addr)+int64(int32(w&0xFFFFF000)))
	case 0x6F: // jal
		off := riscvSext(w>>31<<20|w>>12&0xFF<<12|w>>20&1<<11|w>>21&0x3FF<<1, 21)
		switch rd {
		case 0:
			r.set("j", r.target(Jump, off))
		case riscvRA:
			r.set("jal", r.target(Call, off))
		default:
			r.set("jal", r.x(rd), r.target(Call, off))
		}
		r.clobber(rd)
	case 0x67: // jalr
		if f3 != 0 {
			return false
		}
		r.jalr(rd, rs1, immI)
	case 0x63: // branches
		mn := [8]string{"beq", "bne", "", "", "blt", "bge", "bltu", "bgeu"}[f3]
		if mn == "" {
			return false
		}
		t := r.target(Branch, riscvSext(w>>31<<12|w>>7&1<<11|w>>25&0x3F<<5|w>>8&0xF<<1, 13))
		switch {
		case rs2 == 0 && f3 <= 1:
			r.set(mn+"z", r.x(rs1), t)
		case rs1 == 0 && f3 == 5:
			r.set("blez", r.x(rs2), t)
		case rs2 == 0 && f3 == 5:
			r.set("bgez", r.x(rs1), t)
		case rs2 == 0 && f3 == 4:
			r.set("bltz", r.x(rs1), t)
		case rs1 == 0 && f3 == 4:
			r.set("bgtz", r.x(rs2), t)
		default:
			r.set(mn, r.x(rs1), r.x(rs2), t)
		}
	case 0x03: // loads
		mn := [8]string{"lb", "lh", "lw", "ld", "lbu", "lhu", "lwu", ""}[f3]
		if mn == "" || !rv64 && (f3 == 3 || f3 == 6) {
			return false
		}
		r.load(mn, 1<<(f3&3), rd, rs1, immI)
	case 0x23: // stores
		if f3 > 3 || !rv64 && f3 == 3 {
			return false
		}
		r.store([4]string{"sb", "sh", "sw", "sd"}[f3], 1<<f3, rs2, rs1, immS)
	case 0x13:
		return r.opImm(w, rd, rs1, f3, immI)
	case 0x1B:
		return rv64 && r.opImm32(w, rd, rs1, f3, immI)
	case 0x33:
		return r.op(rd, rs1, rs2, f3, f7, false)
	case 0x3B:
		return rv64 && r.op(rd, rs1, rs2, f3, f7, true)
	case 0x0F:
		return r.miscMem(w, rd, rs1, f3)
	case 0x73:
		return r.system(w, rd, rs1, rs2, f3, f7)
	case 0x2F:
		return r.amo(rd, rs1, rs2, f3, f7)
	case 0x07, 0x27: // flw, fld, fsw, fsd
		if f3 != 2 && f3 != 3 {
			return false
		}
		st := w&0x7F == 0x27
		mn := map[bool]string{false: "fl", true: "fs"}[st] + map[uint32]string{2: "w", 3: "d"}[f3]
		if st {
			r.fmem(mn, 1<<f3, rs2, rs1, immS, true)
		} else {
			r.fmem(mn, 1<<f3, rd, rs1, immI, false)
		}
	case 0x43, 0x47, 0x4B, 0x4F: // fmadd, fmsub, fnmsub, fnmadd
		ft := w >> 25 & 3
		if ft > 1 {
			return false
		}
		mn := [4]string{"fmadd", "fmsub", "fnmsub", "fnmadd"}[w>>2&3] + [2]string{".s", ".d"}[ft]
		r.set(mn, riscvF[rd], riscvF[rs1], riscvF[rs2], riscvF[w>>27])
		r.rm(f3)
	case 0x53:
		return r.opFP(w, rd, rs1, rs2, f3, f7)
	default:
		return false
	}
	return true
}

// jalr writes a jalr and its kind
func (r riscvInst) jalr(rd, rs1 uint32, imm int64) {
	mem := fmt.Sprintf("%d(%s)", imm, r.x(rs1))
	if imm == 0 {
		mem = r.x(rs1)
	}
	switch {
	case rd == 0 && rs1 == riscvRA && imm == 0:
		r.set("ret")
		r.Kind = Return
	case rd == 0:
		r.set("jr", mem)
		r.Kind = Jump
	case rd == riscvRA:
		r.set("jalr", mem)
		r.Kind = Call
	default:
		r.set("jalr", r.x(rd), mem)
		r.Kind = Call
	}
	if r.Kind != Return {
		r.Effects = append(r.Effects, Effect{Kind: JumpReg, Src: int(rs1), Dst: -1, Base: -1, Imm: imm})
	}
	r.clobber(rd)
}

// rm writes the rounding mode, when it is not the dynamic one
func (r riscvInst) rm(f3 uint32) {
	if f3 != 7 {
		r.Operands += "," + riscvRM[f3]
	}
}

// opImm decodes addi..srai
func (r riscvInst) opImm(w, rd, rs1, f3 uint32, imm int64) bool {
	switch f3 {
	case 0:
		switch {
		case rd == 0 && rs1 == 0 && imm == 0:
			r.set("nop")
		case rs1 == 0:
			r.set("li", r.x(rd), fmt.Sprint(imm))
		case imm == 0:
			r.set("mv", r.x(rd), r.x(rs1))
		default:
			r.set("addi", r.x(rd), r.x(rs1), fmt.Sprint(imm))
		}
		r.addImm(rd, rs1, imm)
		return true
	case 1, 5:
		top := w >> 26
		if r.xlen == 32 {
			top = w >> 25
		}
		shamt := w >> 20 & uint32(r.xlen-1)
		mn := ""
		switch {
		case f3 == 1 && top == 0:
			mn = "slli"
		case f3 == 5 && top == 0:
			mn = "srli"
		case f3 == 5 && (r.xlen == 64 && top == 0x10 || r.xlen == 32 && top == 0x20):
			mn = "srai"
		default:
			return false
		}
		r.set(mn, r.x(rd), r.x(rs1), fmt.Sprintf("0x%x", shamt))
	case 3:
		if imm == 1 {
			r.set("seqz", r.x(rd), r.x(rs1))
		} else {
			r.set("sltiu", r.x(rd), r.x(rs1), fmt.Sprint(imm))
		}
	case 4:
		if imm == -1 {
			r.set("not", r.x(rd), r.x(rs1))
		} else {
			r.set("xori", r.x(rd), r.x(rs1), fmt.Sprint(imm))
		}
	case 7:
		if imm == 0xFF {
			r.set("zext.b", r.x(rd), r.x(rs1))
		} else {
			r.set("andi", r.x(rd), r.x(rs1), fmt.Sprint(imm))
		}
	default:
		r.set([8]string{2: "slti", 6: "ori"}[f3], r.x(rd), r.x(rs1), fmt.Sprint(imm))
	}
	r.clobber(rd)
	return true
}

// opImm32 decodes addiw..sraiw
func (r riscvInst) opImm32(w, rd, rs1, f3 uint32, imm int64) bool {
	f7 := w >> 25
	switch {
	case f3 == 0 && imm == 0:
		r.set("sext.w", r.x(rd), r.x(rs1))
	case f3 == 0:
		r.set("addiw", r.x(rd), r.x(rs1), fmt.Sprint(imm))
	case f3 == 1 && f7 == 0:
		r.set("slliw", r.x(rd), r.x(rs1), fmt.Sprintf("0x%x", w>>20&31))
	case f3 == 5 && f7 == 0:
		r.set("srliw", r.x(rd), r.x(rs1), fmt.Sprintf("0x%x", w>>20&31))
	case f3 == 5 && f7 == 0x20:
		r.set("sraiw", r.x(rd), r.x(rs1), fmt.Sprintf("0x%x", w>>20&31))
	default:
		return false
	}
	r.clobber(rd)
	return true
}

// op decodes the register-register instructions, w: the 32-bit ones of RV64
func (r riscvInst) op(rd, rs1, rs2, f3, f7 uint32, w bool) bool {
	var mn string
	switch f7 {
	case 0:
		mn = [8]string{"add", "sll", "slt", "sltu", "xor", "srl", "or", "and"}[f3]
	case 0x20:
		mn = [8]string{0: "sub", 5: "sra"}[f3]
	case 1:
		mn = [8]string{"mul", "mulh", "mulhsu", "mulhu", "div", "divu", "rem", "remu"}[f3]
	}
	if w {
		switch mn {
		case "add", "sub", "sll", "srl", "sra", "mul", "div", "divu", "rem", "remu":
			mn += "w"
		default:
			return false
		}
	}
	switch {
	case mn == "":
		return false
	case (mn == "sub" || mn == "subw") && rs1 == 0:
		r.set("neg"+mn[3:], r.x(rd), r.x(rs2))
	case mn == "sltu" && rs1 == 0:
		r.set("snez", r.x(rd), r.x(rs2))
	case mn == "slt" && rs2 == 0:
		r.set("sltz", r.x(rd), r.x(rs1))
	case mn == "slt" && rs1 == 0:
		r.set("sgtz", r.x(rd), r.x(rs2))
	default:
		r.set(mn, r.x(rd), r.x(rs1), r.x(rs2))
	}
	r.clobber(rd)
	return true
}

// riscvFence writes the sets of a fence
func riscvFence(v uint32) string {
	if v == 0 {
		return "unknown"
	}
	var s string
	for i, c := range "iorw" {
		if v&(8>>i) != 0 {
			s += string(c)
		}
	}
	return s
}

// miscMem decodes fence, fence.tso, fence.i
func (r riscvInst) miscMem(w, rd, rs1, f3 uint32) bool {
	switch {
	case f3 == 0 && rd == 0 && rs1 == 0 && w>>28 == 0:
		pred, succ := w>>24&15, w>>20&15
		if pred == 15 && succ == 15 {
			r.set("fence")
		} else {
			r.set("fence", riscvFence(pred), riscvFence(succ))
		}
	case f3 == 0 && rd == 0 && rs1 == 0 && w>>20 == 0x833:
		r.set("fence.tso")
	case f3 == 1 && w>>7 == 0x20:
		r.set("fence.i")
	default:
		return false
	}
	return true
}

// riscvCSRRead are the aliases of objdump for reading a CSR: csrrs rd,
// csr, zero
var riscvCSRRead = map[uint32]string{0x001: "frflags", 0x002: "frrm", 0x003: "frcsr",
	0xC00: "rdcycle", 0xC01: "rdtime", 0xC02: "rdinstret", 0xC80: "rdcycleh", 0xC81: "rdtimeh", 0xC82: "rdinstreth"}

// riscvCSR is the name of a CSR, or its number
func riscvCSR(c uint32) string {
	switch c {
	case 1:
		return "fflags"
	case 2:
		return "frm"
	case 3:
		return "fcsr"
	}
	if n, ok := riscvCSRs[uint16(c)]; ok {
		return n
	}
	return fmt.Sprintf("0x%x", c)
}

// system decodes ecall, ebreak, the privileged instructions and the CSR ones
func (r riscvInst) system(w, rd, rs1, rs2, f3, f7 uint32) bool {
	if f3 == 0 {
		switch {
		case w == 0x00000073:
			r.set("ecall")
			r.Kind = Interrupt
		case w == 0x00100073:
			r.set("ebreak")
			r.Kind = Interrupt
		case w == 0x10200073:
			r.set("sret")
			r.Kind = Return
		case w == 0x30200073:
			r.set("mret")
			r.Kind = Return
		case w == 0x7B200073:
			r.set("dret")
			r.Kind = Return
		case w == 0x10500073:
			r.set("wfi")
		case f7 == 0x09 && rd == 0:
			switch {
			case rs1 == 0 && rs2 == 0:
				r.set("sfence.vma")
			case rs2 == 0:
				r.set("sfence.vma", r.x(rs1))
			default:
				r.set("sfence.vma", r.x(rs1), r.x(rs2))
			}
		default:
			return false
		}
		return true
	}
	if f3 == 4 {
		return false
	}
	csr := w >> 20
	name := riscvCSR(csr)
	imm := f3 >= 4
	src := r.x(rs1)
	if imm {
		src = fmt.Sprint(rs1)
	}
	base := [4]string{"", "csrrw", "csrrs", "csrrc"}[f3&3]
	if imm {
		base += "i"
	}
	switch {
	case w == 0xC0001073:
		r.set("unimp")
		r.Kind = Invalid
		return true
	case f3 == 2 && rs1 == 0 && riscvCSRRead[csr] != "" && (csr < 0xC80 || r.xlen == 32):
		r.set(riscvCSRRead[csr], r.x(rd))
	case f3 == 2 && rs1 == 0:
		r.set("csrr", r.x(rd), name)
	case f3&3 == 1 && csr >= 1 && csr <= 3:
		// fsflags, fsrm, fscsr and their immediate forms
		mn := [4]string{"", "fsflags", "fsrm", "fscsr"}[csr]
		if imm && csr == 3 {
			r.set(base, r.x(rd), name, src)
			if rd == 0 {
				r.set("csrwi", name, src)
			}
			break
		}
		if imm {
			// The immediate forms keep rd
			r.set(mn+"i", r.x(rd), src)
		} else if rd == 0 {
			r.set(mn, src)
		} else {
			r.set(mn, r.x(rd), src)
		}
	case rd == 0:
		r.set("csr"+[4]string{"", "w", "s", "c"}[f3&3]+base[5:], name, src)
	default:
		r.set(base, r.x(rd), name, src)
	}
	r.clobber(rd)
	return true
}

// amo decodes lr, sc and the atomic memory operations
func (r riscvInst) amo(rd, rs1, rs2, f3, f7 uint32) bool {
	if f3 != 2 && (f3 != 3 || r.xlen != 64) {
		return false
	}
	size := 4
	sfx := ".w"
	if f3 == 3 {
		size, sfx = 8, ".d"
	}
	op := f7 >> 2
	mn := map[uint32]string{0x00: "amoadd", 0x01: "amoswap", 0x02: "lr", 0x03: "sc", 0x04: "amoxor", 0x08: "amoor",
		0x0C: "amoand", 0x10: "amomin", 0x14: "amomax", 0x18: "amominu", 0x1C: "amomaxu"}[op]
	if mn == "" || mn == "lr" && rs2 != 0 {
		return false
	}
	mn += sfx + [4]string{"", ".rl", ".aq", ".aqrl"}[f7&3]
	mem := "(" + r.x(rs1) + ")"
	dst := int(rd)
	if rd == 0 {
		dst = -1
	}
	switch {
	case op == 2:
		r.set(mn, r.x(rd), mem)
		r.Effects = append(r.Effects, Effect{Kind: MemAccess, Dst: dst, Src: -1, Base: int(rs1), Size: size})
	case op == 3:
		r.set(mn, r.x(rd), r.x(rs2), mem)
		r.Effects = append(r.Effects, Effect{Kind: MemAccess, Dst: -1, Src: int(rs2), Base: int(rs1), Size: size, Store: true})
	default:
		r.set(mn, r.x(rd), r.x(rs2), mem)
		r.Effects = append(r.Effects,
			Effect{Kind: MemAccess, Dst: dst, Src: -1, Base: int(rs1), Size: size},
			Effect{Kind: MemAccess, Dst: -1, Src: int(rs2), Base: int(rs1), Size: size, Store: true})
	}
	r.clobber(rd)
	return true
}

// opFP decodes the floating-point operations of F and D
func (r riscvInst) opFP(w, rd, rs1, rs2, f3, f7 uint32) bool {
	if f7&3 > 1 {
		return false
	}
	t := [2]string{"s", "d"}[f7&1]
	fd, fs1, fs2 := riscvF[rd], riscvF[rs1], riscvF[rs2]
	rv64 := r.xlen == 64
	switch f7 >> 2 {
	case 0x00, 0x01, 0x02, 0x03: // fadd, fsub, fmul, fdiv
		r.set([4]string{"fadd.", "fsub.", "fmul.", "fdiv."}[f7>>2]+t, fd, fs1, fs2)
		r.rm(f3)
	case 0x0B: // fsqrt
		if rs2 != 0 {
			return false
		}
		r.set("fsqrt."+t, fd, fs1)
		r.rm(f3)
	case 0x04: // fsgnj, fsgnjn, fsgnjx
		if f3 > 2 {
			return false
		}
		if rs1 == rs2 {
			r.set([3]string{"fmv.", "fneg.", "fabs."}[f3]+t, fd, fs1)
		} else {
			r.set([3]string{"fsgnj.", "fsgnjn.", "fsgnjx."}[f3]+t, fd, fs1, fs2)
		}
	case 0x05: // fmin, fmax
		if f3 > 1 {
			return false
		}
		r.set([2]string{"fmin.", "fmax."}[f3]+t, fd, fs1, fs2)
	case 0x08: // fcvt.s.d, fcvt.d.s
		if rs2 != 1-f7&1 {
			return false
		}
		if f7&1 == 1 {
			// fcvt.d.s is exact: the rounding mode is 0
			if f3 != 0 {
				return false
			}
			r.set("fcvt.d.s", fd, fs1)
		} else {
			r.set("fcvt.s.d", fd, fs1)
			r.rm(f3)
		}
	case 0x14: // feq, flt, fle
		if f3 > 2 {
			return false
		}
		r.set([3]string{"fle.", "flt.", "feq."}[f3]+t, r.x(rd), fs1, fs2)
		r.clobber(rd)
	case 0x18, 0x1A: // fcvt to and from the integer registers
		if rs2 > 3 || rs2 > 1 && !rv64 {
			return false
		}
		it := [4]string{"w", "wu", "l", "lu"}[rs2]
		if f7>>2 == 0x18 {
			r.set("fcvt."+it+"."+t, r.x(rd), fs1)
			r.rm(f3)
			r.clobber(rd)
		} else {
			r.set("fcvt."+t+"."+it, fd, r.x(rs1))
			if t == "d" && rs2 < 2 {
				// fcvt.d.w and fcvt.d.wu are exact: the rounding mode is 0
				if f3 != 0 {
					return false
				}
			} else {
				r.rm(f3)
			}
		}
	case 0x1C: // fmv.x.w, fmv.x.d, fclass
		if rs2 != 0 || f3 > 1 || t == "d" && !rv64 && f3 == 0 {
			return false
		}
		if f3 == 0 {
			r.set("fmv.x."+map[string]string{"s": "w", "d": "d"}[t], r.x(rd), fs1)
		} else {
			r.set("fclass."+t, r.x(rd), fs1)
		}
		r.clobber(rd)
	case 0x1E: // fmv.w.x, fmv.d.x
		if rs2 != 0 || f3 != 0 || t == "d" && !rv64 {
			return false
		}
		r.set("fmv."+map[string]string{"s": "w", "d": "d"}[t]+".x", fd, r.x(rs1))
	default:
		return false
	}
	return true
}
