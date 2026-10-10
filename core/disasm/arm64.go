package disasm

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/arch/arm64/arm64asm"
)

// arm64 decodes AArch64 with golang.org/x/arch in GNU syntax (ARMv8.0:
// later extensions such as atomics, pointer authentication and SVE are
// shown as hints or as .inst)
type arm64 struct{}

func init() { register(arm64{}) }

func (arm64) ID() string   { return "arm64" }
func (arm64) Name() string { return "ARM64 (AArch64)" }
func (arm64) MaxLen() int  { return 4 }

func (arm64) Decode(code []byte, addr uint64, mode int) Inst {
	if len(code) < 4 {
		if len(code) == 0 {
			return Inst{}
		}
		return Inst{Addr: addr, Len: 1, Mnemonic: ".byte", Operands: fmt.Sprintf("0x%x", code[0]), Kind: Invalid}
	}
	in, err := arm64asm.Decode(code)
	if err != nil || in.Op == 0 || a64Reserved(in.Enc) {
		w := binary.LittleEndian.Uint32(code)
		return Inst{Addr: addr, Len: 4, Mnemonic: ".inst", Operands: fmt.Sprintf("0x%08x", w), Kind: Invalid}
	}
	out := Inst{Addr: addr, Len: 4}
	text := strings.TrimSpace(arm64asm.GNUSyntax(in))
	// PC-relative operands as addresses
	for _, a := range in.Args {
		rel, ok := a.(arm64asm.PCRel)
		if !ok {
			continue
		}
		t := addr + uint64(rel)
		if in.Op == arm64asm.ADRP {
			t = addr&^0xFFF + uint64(rel)
		}
		out.Target = t
		text = strings.Replace(text, strings.ToLower(rel.String()), fmt.Sprintf("0x%x", t), 1)
	}
	mn, ops, _ := strings.Cut(text, " ")
	if mn == "b.al" && in.Enc&0xF == 0xF {
		mn = "b.nv"
	}
	// "[x1,#16]" as objdump writes it: "[x1, #16]"
	ops = strings.ReplaceAll(strings.ReplaceAll(ops, ",", ", "), ",  ", ", ")
	out.Mnemonic, out.Operands = mn, ops
	target := out.Target
	out.Target = 0
	switch in.Op {
	case arm64asm.B:
		out.Kind = Jump
		if _, ok := in.Args[0].(arm64asm.Cond); ok {
			out.Kind = Branch
		}
		out.Target, out.HasTarget = target, true
	case arm64asm.BL:
		out.Kind = Call
		out.Target, out.HasTarget = target, true
	case arm64asm.CBZ, arm64asm.CBNZ, arm64asm.TBZ, arm64asm.TBNZ:
		out.Kind = Branch
		out.Target, out.HasTarget = target, true
	case arm64asm.BR:
		out.Kind = Jump
	case arm64asm.BLR:
		out.Kind = Call
	case arm64asm.RET, arm64asm.ERET:
		out.Kind = Return
	case arm64asm.SVC, arm64asm.HVC, arm64asm.SMC, arm64asm.BRK, arm64asm.HLT:
		out.Kind = Interrupt
	}
	out.Effects = a64Effects(in, target)
	return out
}

// a64Reg is the number of a general register: x0..x30 (or w0..w30) are 0..30,
// sp is 31; -1 for the zero register and the others
func a64Reg(a arm64asm.Arg) int {
	switch r := a.(type) {
	case arm64asm.Reg:
		switch {
		case r >= arm64asm.X0 && r <= arm64asm.X30:
			return int(r - arm64asm.X0)
		case r >= arm64asm.W0 && r <= arm64asm.W30:
			return int(r - arm64asm.W0)
		}
	case arm64asm.RegSP:
		switch arm64asm.Reg(r) {
		case arm64asm.SP, arm64asm.WSP:
			return 31
		}
		return a64Reg(arm64asm.Reg(r))
	}
	return -1
}

// a64Size is the size in bytes of a register: w0 4, x0 8, q0 16...
func a64Size(a arm64asm.Arg) int {
	r, ok := a.(arm64asm.Reg)
	if !ok {
		return 0
	}
	switch {
	case r >= arm64asm.W0 && r <= arm64asm.WZR:
		return 4
	case r >= arm64asm.X0 && r <= arm64asm.XZR:
		return 8
	case r >= arm64asm.B0 && r <= arm64asm.B31:
		return 1
	case r >= arm64asm.H0 && r <= arm64asm.H31:
		return 2
	case r >= arm64asm.S0 && r <= arm64asm.S31:
		return 4
	case r >= arm64asm.D0 && r <= arm64asm.D31:
		return 8
	case r >= arm64asm.Q0 && r <= arm64asm.Q31:
		return 16
	}
	return 0
}

// a64Imm is the offset of a memory operand: "[x1,#16]" - 16
func a64Imm(m arm64asm.MemImmediate) int64 {
	s := m.String()
	i := strings.IndexByte(s, '#')
	if i < 0 {
		return 0
	}
	j := i + 1
	for j < len(s) && (s[j] == '-' || s[j] >= '0' && s[j] <= '9') {
		j++
	}
	v, _ := strconv.ParseInt(s[i+1:j], 10, 64)
	return v
}

// The instructions whose first operand is read, not written
var a64NoDst = map[arm64asm.Op]bool{
	arm64asm.CMP: true, arm64asm.CMN: true, arm64asm.TST: true, arm64asm.CCMP: true, arm64asm.CCMN: true,
	arm64asm.CBZ: true, arm64asm.CBNZ: true, arm64asm.TBZ: true, arm64asm.TBNZ: true,
	arm64asm.BR: true, arm64asm.BLR: true, arm64asm.RET: true, arm64asm.MSR: true, arm64asm.SYS: true,
	arm64asm.DC: true, arm64asm.IC: true, arm64asm.TLBI: true, arm64asm.AT: true,
	arm64asm.PRFM: true, arm64asm.PRFUM: true, arm64asm.FCMP: true, arm64asm.FCMPE: true,
	arm64asm.FCCMP: true, arm64asm.FCCMPE: true,
}

// The stores: the first operand is the value (the registers before the
// memory operand)
var a64Store = map[arm64asm.Op]bool{
	arm64asm.STR: true, arm64asm.STRB: true, arm64asm.STRH: true, arm64asm.STUR: true, arm64asm.STURB: true,
	arm64asm.STURH: true, arm64asm.STP: true, arm64asm.STNP: true, arm64asm.STTR: true, arm64asm.STTRB: true,
	arm64asm.STTRH: true, arm64asm.STLR: true, arm64asm.STLRB: true, arm64asm.STLRH: true,
	arm64asm.ST1: true, arm64asm.ST2: true, arm64asm.ST3: true, arm64asm.ST4: true,
}

// The exclusive stores: the first operand is the status, written
var a64StoreEx = map[arm64asm.Op]bool{
	arm64asm.STXR: true, arm64asm.STXRB: true, arm64asm.STXRH: true, arm64asm.STXP: true,
	arm64asm.STLXR: true, arm64asm.STLXRB: true, arm64asm.STLXRH: true, arm64asm.STLXP: true,
}

// The sizes of the loads and stores that are not by the register
var a64MemSize = map[arm64asm.Op]int{
	arm64asm.LDRB: 1, arm64asm.LDRSB: 1, arm64asm.STRB: 1, arm64asm.LDURB: 1, arm64asm.LDURSB: 1, arm64asm.STURB: 1,
	arm64asm.LDTRB: 1, arm64asm.LDTRSB: 1, arm64asm.STTRB: 1,
	arm64asm.LDRH: 2, arm64asm.LDRSH: 2, arm64asm.STRH: 2, arm64asm.LDURH: 2, arm64asm.LDURSH: 2, arm64asm.STURH: 2,
	arm64asm.LDTRH: 2, arm64asm.LDTRSH: 2, arm64asm.STTRH: 2,
	arm64asm.LDRSW: 4, arm64asm.LDURSW: 4, arm64asm.LDTRSW: 4, arm64asm.LDPSW: 4,
}

func a64Clobber(r int) Effect { return Effect{Kind: Clobber, Dst: r, Src: -1, Base: -1} }

// a64Effects are the effects of an instruction; target is the address of
// a PC-relative operand
func a64Effects(in arm64asm.Inst, target uint64) []Effect {
	enc := in.Enc
	switch in.Op {
	case arm64asm.ADR, arm64asm.ADRP:
		return []Effect{{Kind: SetConst, Dst: a64Reg(in.Args[0]), Src: -1, Base: -1, Imm: int64(target)}}
	case arm64asm.MOVZ, arm64asm.MOVN, arm64asm.MOVK:
		d := a64Reg(in.Args[0])
		if d < 0 {
			return nil
		}
		sh := (enc >> 21 & 3) * 16
		v := uint64(enc>>5&0xFFFF) << sh
		switch {
		case in.Op == arm64asm.MOVZ:
		case in.Op == arm64asm.MOVN:
			v = ^v
			if enc>>31 == 0 {
				v &= 0xFFFFFFFF
			}
		case sh == 16:
			// movk to bits 16..31: as ARM movt
			return []Effect{{Kind: SetHigh16, Dst: d, Src: -1, Base: -1, Imm: int64(enc >> 5 & 0xFFFF)}}
		default:
			return []Effect{a64Clobber(d)}
		}
		return []Effect{{Kind: SetConst, Dst: d, Src: -1, Base: -1, Imm: int64(v)}}
	case arm64asm.MOV, arm64asm.ORR:
		d := a64Reg(in.Args[0])
		if d < 0 {
			return nil
		}
		// mov x0, #imm; orr x0, xzr, #imm
		var imm arm64asm.Arg = in.Args[1]
		if in.Op == arm64asm.ORR {
			if r, ok := in.Args[1].(arm64asm.Reg); !ok || (r != arm64asm.XZR && r != arm64asm.WZR) {
				break
			}
			imm = in.Args[2]
		}
		switch v := imm.(type) {
		case arm64asm.Imm64:
			return []Effect{{Kind: SetConst, Dst: d, Src: -1, Base: -1, Imm: int64(v.Imm)}}
		case arm64asm.Imm:
			return []Effect{{Kind: SetConst, Dst: d, Src: -1, Base: -1, Imm: int64(v.Imm)}}
		}
		// mov x0, x1; mov x29, sp: a copy of a 64-bit register
		if in.Op == arm64asm.MOV && a64Size(arm64asm.Reg(a64RegArg(in.Args[0]))) == 8 {
			if s := a64Reg(in.Args[1]); s >= 0 && a64Size(arm64asm.Reg(a64RegArg(in.Args[1]))) == 8 {
				return []Effect{{Kind: AddImm, Dst: d, Src: s, Base: -1}}
			}
		}
	case arm64asm.ADD, arm64asm.SUB:
		// add/sub (immediate), without flags
		if enc&0x3F000000 == 0x11000000 {
			d, s := a64Reg(in.Args[0]), a64Reg(in.Args[1])
			imm := int64(enc >> 10 & 0xFFF)
			if enc>>22&1 != 0 {
				imm <<= 12
			}
			if in.Op == arm64asm.SUB {
				imm = -imm
			}
			if d >= 0 && s >= 0 {
				return []Effect{{Kind: AddImm, Dst: d, Src: s, Base: -1, Imm: imm}}
			}
		}
	}
	var eff []Effect
	// the registers before a memory operand, and the memory operand
	mem := -1
	for i, a := range in.Args {
		switch a.(type) {
		case arm64asm.MemImmediate, arm64asm.MemExtend:
			mem = i
		case arm64asm.PCRel:
			if in.Op == arm64asm.LDR || in.Op == arm64asm.LDRSW {
				mem = i
			}
		}
		if mem >= 0 {
			break
		}
	}
	store := a64Store[in.Op]
	first := 0
	if a64StoreEx[in.Op] {
		if r := a64Reg(in.Args[0]); r >= 0 {
			eff = append(eff, a64Clobber(r))
		}
		store, first = true, 1
	}
	if mem > first {
		size := a64MemSize[in.Op]
		if size == 0 {
			size = a64Size(in.Args[first]) * (mem - first)
		}
		vals := in.Args[first:mem]
		switch m := in.Args[mem].(type) {
		case arm64asm.PCRel:
			// ldr x0, =literal
			if d := a64Reg(vals[0]); d >= 0 && len(vals) == 1 {
				eff = append(eff, Effect{Kind: LoadLiteral, Dst: d, Src: -1, Base: -1, Addr: target, Size: size})
				return eff
			}
		case arm64asm.MemImmediate:
			base := a64Reg(m.Base)
			off := a64Imm(m)
			e := Effect{Kind: MemAccess, Dst: -1, Src: -1, Base: base, Imm: off, Size: size, Store: store}
			if m.Mode == arm64asm.AddrPostIndex {
				e.Imm = 0
			}
			if store {
				e.Src = a64Reg(vals[0])
			} else {
				e.Dst = a64Reg(vals[0])
			}
			if m.Mode != arm64asm.AddrPostReg && base >= 0 {
				eff = append(eff, e)
			}
			if !store {
				for _, v := range vals {
					if r := a64Reg(v); r >= 0 {
						eff = append(eff, a64Clobber(r))
					}
				}
			}
			// the base is written back
			if (m.Mode == arm64asm.AddrPreIndex || m.Mode == arm64asm.AddrPostIndex) && base >= 0 {
				eff = append(eff, Effect{Kind: AddImm, Dst: base, Src: base, Base: -1, Imm: off})
			} else if m.Mode == arm64asm.AddrPostReg && base >= 0 {
				eff = append(eff, a64Clobber(base))
			}
			return eff
		}
		if !store {
			for _, v := range vals {
				if r := a64Reg(v); r >= 0 {
					eff = append(eff, a64Clobber(r))
				}
			}
		}
		return eff
	}
	if store {
		return eff
	}
	if in.Op == arm64asm.BL || in.Op == arm64asm.BLR {
		return []Effect{a64Clobber(30)}
	}
	if in.Args[0] != nil && !a64NoDst[in.Op] {
		if r := a64Reg(in.Args[0]); r >= 0 {
			eff = append(eff, a64Clobber(r))
		}
	}
	return eff
}

// a64RegArg is the register of a Reg or RegSP operand
func a64RegArg(a arm64asm.Arg) arm64asm.Reg {
	switch r := a.(type) {
	case arm64asm.Reg:
		return r
	case arm64asm.RegSP:
		return arm64asm.Reg(r)
	}
	return arm64asm.WZR
}

// a64Reserved tells the encodings the decoder takes that are reserved:
// an extended register shifted by more than 4, a bitmask immediate of all
// ones
func a64Reserved(enc uint32) bool {
	if enc&0x1F200000 == 0x0B200000 && enc>>10&7 > 4 {
		return true
	}
	if enc&0x1F800000 == 0x12000000 {
		n := enc >> 22 & 1
		imms := enc >> 10 & 0x3F
		v := n<<6 | ^imms&0x3F
		l := -1
		for i := 6; i >= 0; i-- {
			if v>>i&1 != 0 {
				l = i
				break
			}
		}
		if l < 1 {
			return true
		}
		levels := uint32(1)<<l - 1
		return imms&levels == levels
	}
	return false
}
