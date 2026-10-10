package disasm

import (
	"encoding/binary"
	"fmt"
	"strings"

	"golang.org/x/arch/arm/armasm"
)

// decodeA32 decodes an A32 instruction with golang.org/x/arch in GNU
// syntax, with the kind, the target and the effects added
func decodeA32(code []byte, addr uint64) Inst {
	if len(code) < 4 {
		if len(code) < 2 {
			return armByte(code, addr)
		}
		return Inst{Addr: addr, Len: 2, Mnemonic: ".short", Operands: fmt.Sprintf("0x%04x", binary.LittleEndian.Uint16(code)), Kind: Invalid}
	}
	w := binary.LittleEndian.Uint32(code)
	in, err := armasm.Decode(code[:4], armasm.ModeARM)
	if err != nil {
		return Inst{Addr: addr, Len: 4, Mnemonic: ".word", Operands: fmt.Sprintf("0x%08x", w), Kind: Invalid, Comment: "undefined"}
	}
	out := Inst{Addr: addr, Len: 4}
	pc := uint32(addr) + 8
	base, cond := in.Op, 14
	if b := in.Op &^ 15; strings.HasSuffix(b.String(), ".EQ") {
		base, cond = b, int(in.Op&15)
	}
	mn, _, _ := strings.Cut(armasm.GNUSyntax(in), " ")
	out.Mnemonic = mn
	args := in.Args[:]
	for len(args) > 0 && args[len(args)-1] == nil {
		args = args[:len(args)-1]
	}
	var ops, notes []string
	for i, arg := range args {
		if arg == nil {
			break
		}
		// The second register of a pair is not written
		if (i == 1 && (base == armasm.LDRD_EQ || base == armasm.LDREXD_EQ || base == armasm.STRD_EQ)) ||
			(i == 2 && base == armasm.STREXD_EQ) {
			continue
		}
		switch a := arg.(type) {
		case armasm.PCRel:
			if base == armasm.LDRD_EQ || base == armasm.LDRH_EQ || base == armasm.LDRSB_EQ || base == armasm.LDRSH_EQ {
				lit := uint32(int64(pc) + int64(a))
				ops = append(ops, fmt.Sprintf("[pc, #%d]", int32(a)))
				notes = append(notes, fmt.Sprintf("0x%x", lit))
				continue
			}
			t := uint32(int64(pc) + int64(a))
			out.Target, out.HasTarget = uint64(t), true
			if base == armasm.BLX_EQ {
				out.TargetMode = ModeThumb
			}
			ops = append(ops, fmt.Sprintf("0x%x", t))
		case armasm.Mem:
			if a.Base == armasm.PC && a.Sign == 0 && a.Mode == armasm.AddrOffset {
				lit := uint32(int64(pc) + int64(a.Offset))
				ops = append(ops, fmt.Sprintf("[pc, #%d]", a.Offset))
				notes = append(notes, fmt.Sprintf("0x%x", lit))
				if r, ok := args[0].(armasm.Reg); ok && base == armasm.LDR_EQ && r < armasm.PC && cond == 14 {
					out.Effects = append(out.Effects, Effect{Kind: LoadLiteral, Dst: int(r), Src: -1, Base: -1, Addr: uint64(lit), Size: 4})
				}
				continue
			}
			ops = append(ops, a32Arg(in, base, a))
		default:
			ops = append(ops, a32Arg(in, base, a))
		}
	}
	out.Operands = strings.Join(ops, ", ")
	out.Comment = strings.Join(notes, ", ")
	a32Kind(&out, in, base, cond, args)
	if cond >= 14 {
		a32Effects(&out, base, args)
	} else if len(args) == 0 {
	} else if r, ok := args[0].(armasm.Reg); ok && r < armasm.PC && !strings.HasPrefix(base.String(), "ST") &&
		!strings.HasPrefix(base.String(), "CM") && !strings.HasPrefix(base.String(), "T") {
		// A conditional instruction: what it writes is not known after it
		out.Effects = append(out.Effects, Effect{Kind: Clobber, Dst: int(r), Src: -1, Base: -1})
	}
	return out
}

// a32Kind sets the kind of a branch, a call, a return
func a32Kind(out *Inst, in armasm.Inst, base armasm.Op, cond int, args []armasm.Arg) {
	var dst armasm.Reg = 255
	if len(args) > 0 {
		if r, ok := args[0].(armasm.Reg); ok {
			dst = r
		}
	}
	switch base {
	case armasm.B_EQ:
		out.Kind = Jump
	case armasm.BL_EQ, armasm.BLX_EQ:
		out.Kind = Call
	case armasm.BX_EQ:
		out.Kind = Jump
		if dst == armasm.LR {
			out.Kind = Return
		}
	case armasm.SVC_EQ, armasm.BKPT_EQ:
		out.Kind = Interrupt
	case armasm.POP_EQ, armasm.LDM_EQ, armasm.LDMIB_EQ, armasm.LDMDA_EQ, armasm.LDMDB_EQ:
		if len(args) == 0 {
			break
		}
		if l, ok := args[len(args)-1].(armasm.RegList); ok && l&(1<<15) != 0 {
			out.Kind = Jump
			if m, ok := args[0].(armasm.Mem); base == armasm.POP_EQ || ok && m.Base == armasm.SP {
				out.Kind = Return
			}
		}
	case armasm.LDR_EQ, armasm.MOV_EQ, armasm.ADD_EQ, armasm.SUB_EQ:
		if dst == armasm.PC {
			out.Kind = Jump
			if len(args) > 1 {
				if m, ok := args[1].(armasm.Mem); ok && m.Base == armasm.SP {
					out.Kind = Return
				}
				if r, ok := args[1].(armasm.Reg); ok && r == armasm.LR && base == armasm.MOV_EQ {
					out.Kind = Return
				}
			}
		}
	}
	if cond < 14 && (out.Kind == Jump || out.Kind == Return) {
		out.Kind = Branch
	}
	if out.Kind == Call {
		for _, r := range []int{0, 1, 2, 3, 12, 14} {
			out.Effects = append(out.Effects, Effect{Kind: Clobber, Dst: r, Src: -1, Base: -1})
		}
	}
}

// a32Effects are the effects of the unconditional A32 instructions the
// analysis follows: movw, movt, mov, and loads and stores with an
// immediate offset
func a32Effects(out *Inst, base armasm.Op, args []armasm.Arg) {
	if len(args) < 2 {
		return
	}
	dst, ok := args[0].(armasm.Reg)
	if !ok || dst >= armasm.PC {
		return
	}
	eff := func(e Effect) { out.Effects = append(out.Effects, e) }
	switch base {
	case armasm.MOVW_EQ, armasm.MOVT_EQ, armasm.MOV_EQ:
		v, ok := args[1].(armasm.Imm)
		if !ok {
			eff(Effect{Kind: Clobber, Dst: int(dst), Src: -1, Base: -1})
			return
		}
		k := map[armasm.Op]EffectKind{armasm.MOVW_EQ: SetLow16, armasm.MOVT_EQ: SetHigh16, armasm.MOV_EQ: SetConst}[base]
		eff(Effect{Kind: k, Dst: int(dst), Src: -1, Base: -1, Imm: int64(uint32(v))})
	case armasm.LDR_EQ, armasm.LDRB_EQ, armasm.LDRH_EQ, armasm.STR_EQ, armasm.STRB_EQ, armasm.STRH_EQ:
		m, ok := args[1].(armasm.Mem)
		store := base == armasm.STR_EQ || base == armasm.STRB_EQ || base == armasm.STRH_EQ
		if ok && m.Base == armasm.PC && base == armasm.LDR_EQ {
			return
		}
		if !ok || m.Base == armasm.PC || m.Sign != 0 {
			if !store {
				eff(Effect{Kind: Clobber, Dst: int(dst), Src: -1, Base: -1})
			}
			return
		}
		size := 2
		switch base {
		case armasm.LDR_EQ, armasm.STR_EQ:
			size = 4
		case armasm.LDRB_EQ, armasm.STRB_EQ:
			size = 1
		}
		off := int64(m.Offset)
		if m.Mode == armasm.AddrPostIndex {
			off = 0
		}
		e := Effect{Kind: MemAccess, Dst: -1, Src: -1, Base: int(m.Base), Imm: off, Size: size, Store: store}
		if store {
			e.Src = int(dst)
		} else {
			e.Dst = int(dst)
		}
		eff(e)
		if m.Mode == armasm.AddrPreIndex || m.Mode == armasm.AddrPostIndex {
			eff(Effect{Kind: AddImm, Dst: int(m.Base), Src: int(m.Base), Base: -1, Imm: int64(m.Offset)})
		}
		if !store {
			eff(Effect{Kind: Clobber, Dst: int(dst), Src: -1, Base: -1})
		}
	default:
		// Any other that writes a register: data processing and loads
		if _, isReg := args[1].(armasm.Reg); isReg || base == armasm.LDRSB_EQ || base == armasm.LDRSH_EQ ||
			base == armasm.MVN_EQ || base == armasm.ADD_EQ || base == armasm.SUB_EQ {
			switch base {
			case armasm.CMP_EQ, armasm.CMN_EQ, armasm.TST_EQ, armasm.TEQ_EQ, armasm.STREX_EQ:
				return
			}
			if _, isMem := args[1].(armasm.Mem); !isMem || !strings.HasPrefix(base.String(), "ST") {
				eff(Effect{Kind: Clobber, Dst: int(dst), Src: -1, Base: -1})
			}
		}
	}
}

// a32Arg is the text of an argument in GNU syntax, with the register names
// of objdump -M reg-names-std (after gnuArg of golang.org/x/arch)
func a32Arg(in armasm.Inst, base armasm.Op, arg armasm.Arg) string {
	switch a := arg.(type) {
	case armasm.Imm:
		switch base {
		case armasm.BKPT_EQ:
			return fmt.Sprintf("0x%04x", uint32(a))
		case armasm.SVC_EQ:
			return fmt.Sprintf("0x%08x", uint32(a))
		}
		return fmt.Sprintf("#%d", int32(a))
	case armasm.ImmAlt:
		return fmt.Sprintf("#%d, %d", a.Val, a.Rot)
	case armasm.Reg:
		if a <= armasm.PC {
			return armRegs[a]
		}
		return strings.ToLower(a.String())
	case armasm.RegX:
		return strings.ToLower(a.String())
	case armasm.Mem:
		r := armRegs[a.Base]
		var x string
		if a.Sign != 0 {
			if a.Sign < 0 {
				x = "-"
			}
			x += armRegs[a.Index]
			switch {
			case a.Shift == armasm.ShiftLeft && a.Count == 0:
			case a.Shift == armasm.RotateRightExt:
				x += ", rrx"
			default:
				x += fmt.Sprintf(", %s #%d", strings.ToLower(a.Shift.String()), a.Count)
			}
		} else {
			x = fmt.Sprintf("#%d", a.Offset)
		}
		switch a.Mode {
		case armasm.AddrOffset:
			if x == "#0" {
				return "[" + r + "]"
			}
			return fmt.Sprintf("[%s, %s]", r, x)
		case armasm.AddrPreIndex:
			return fmt.Sprintf("[%s, %s]!", r, x)
		case armasm.AddrPostIndex:
			return fmt.Sprintf("[%s], %s", r, x)
		case armasm.AddrLDM:
			return r
		case armasm.AddrLDM_WB:
			return r + "!"
		}
		return fmt.Sprintf("[%s %s]", r, x)
	case armasm.RegList:
		return regList(uint16(a))
	case armasm.RegShift:
		r := armRegs[a.Reg]
		switch {
		case a.Shift == armasm.ShiftLeft && a.Count == 0:
			return r
		case a.Shift == armasm.RotateRightExt:
			return r + ", rrx"
		}
		return fmt.Sprintf("%s, %s #%d", r, strings.ToLower(a.Shift.String()), a.Count)
	case armasm.RegShiftReg:
		return fmt.Sprintf("%s, %s %s", armRegs[a.Reg], strings.ToLower(a.Shift.String()), armRegs[a.RegCount])
	}
	return strings.ToLower(arg.String())
}
