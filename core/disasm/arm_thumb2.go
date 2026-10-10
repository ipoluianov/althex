package disasm

import (
	"fmt"
	"strings"
)

// The 32-bit Thumb-2 instructions of ARMv7-M, ARMv7E-M and ARMv8-M; the
// coprocessor and floating-point ones are in arm_vfp.go

func (d *tdec) thumb32(h1, h2 uint16) {
	switch {
	case h1&0xFE00 == 0xE800:
		if h1&0x40 == 0 {
			d.ldmStm(h1, h2)
		} else {
			d.dualExclusive(h1, h2)
		}
	case h1&0xFE00 == 0xEA00:
		d.dpShifted(h1, h2)
	case h1&0xEC00 == 0xEC00:
		d.coproc(h1, h2)
	case h1&0xF800 == 0xF000:
		switch {
		case h2&0x8000 != 0:
			d.branchMisc(h1, h2)
		case h1&0x200 == 0:
			d.dpModImm(h1, h2)
		default:
			d.dpPlainImm(h1, h2)
		}
	case h1&0xFE00 == 0xF800:
		d.loadStore(h1, h2)
	case h1&0xFF00 == 0xFA00:
		d.dpReg(h1, h2)
	case h1&0xFF80 == 0xFB00:
		d.multiply(h1, h2)
	case h1&0xFF80 == 0xFB80:
		d.longMultiply(h1, h2)
	default:
		d.undef32(h1, h2)
	}
}

// ldmStm is ldm, stm (IA and DB), srs and rfe
func (d *tdec) ldmStm(h1, h2 uint16) {
	rn := int(h1 & 15)
	wb := h1&0x20 != 0
	load := h1&0x10 != 0
	op := (h1 >> 7) & 3
	w := ""
	if wb {
		w = "!"
	}
	if op == 0 || op == 3 {
		// srs and rfe are not in M-profile
		d.undef32(h1, h2)
		return
	}
	list := h2
	n := int64(popCount16(list)) * 4
	mn := "stm"
	if load {
		mn = "ldm"
	}
	if op == 1 {
		mn += "ia" + d.c() + ".w"
	} else {
		mn += "db" + d.c()
		n = -n
	}
	d.set(mn, "%s%s, %s", armRegs[rn], w, regList(list))
	if load {
		d.clobList(list)
		if list&(1<<armPC) != 0 {
			d.in.Kind = Jump
			if rn == armSP {
				d.in.Kind = Return
			}
		}
	}
	if wb {
		d.addImm(rn, rn, n)
	}
}

// dualExclusive is ldrd, strd, ldrex, strex and their kinds, tbb, tbh,
// the load-acquire and store-release ones, sg and tt
func (d *tdec) dualExclusive(h1, h2 uint16) {
	rn := int(h1 & 15)
	rt := int(h2 >> 12)
	rt2 := int(h2>>8) & 15
	rm := int(h2 & 15)
	op1 := (h1 >> 7) & 3
	op2 := (h1 >> 4) & 3
	op3 := (h2 >> 4) & 15
	imm := int64(h2&0xFF) * 4
	switch {
	case op1 == 0 && op2 == 0:
		if rt == 15 && h2&0x3F == 0 {
			mn := [4]string{"tt", "ttt", "tta", "ttat"}[(h2>>6)&3]
			d.set(mn+d.c(), "%s, %s", armRegs[rt2], armRegs[rn])
			d.clob(rt2)
			return
		}
		d.set("strex"+d.c(), "%s, %s, [%s%s]", armRegs[rt2], armRegs[rt], armRegs[rn], d.optImm(imm))
		d.mem(rt, rn, imm, 4, true)
		d.clob(rt2)
	case op1 == 0 && op2 == 1:
		if rt2 != 15 {
			d.undef32(h1, h2)
			return
		}
		d.set("ldrex"+d.c(), "%s, [%s%s]", armRegs[rt], armRegs[rn], d.optImm(imm))
		d.load(rt, rn, imm, 4)
	case op1 == 1 && op2 == 0:
		size := [4]int{1, 2, 8, 4}[op3&3]
		// The fields that must be all ones
		if (op3 != 7 && op3 != 15 && rt2 != 15) || (op3&12 == 8 && rm != 15) {
			d.undef32(h1, h2)
			return
		}
		switch op3 {
		case 4, 5:
			d.set([2]string{"strexb", "strexh"}[op3&1]+d.c(), "%s, %s, [%s]", armRegs[rm], armRegs[rt], armRegs[rn])
		case 7:
			d.set("strexd"+d.c(), "%s, %s, %s, [%s]", armRegs[rm], armRegs[rt], armRegs[rt2], armRegs[rn])
		case 8, 9, 10:
			d.set([3]string{"stlb", "stlh", "stl"}[op3&3]+d.c(), "%s, [%s]", armRegs[rt], armRegs[rn])
			d.mem(rt, rn, 0, size, true)
			return
		case 12, 13, 14:
			d.set([3]string{"stlexb", "stlexh", "stlex"}[op3&3]+d.c(), "%s, %s, [%s]", armRegs[rm], armRegs[rt], armRegs[rn])
		case 15:
			d.set("stlexd"+d.c(), "%s, %s, %s, [%s]", armRegs[rm], armRegs[rt], armRegs[rt2], armRegs[rn])
			if rn == armPC {
				d.note("<UNPREDICTABLE>")
			}
		default:
			d.undef32(h1, h2)
			return
		}
		if size != 8 {
			d.mem(rt, rn, 0, size, true)
		}
		d.clob(rm)
	case op1 == 1 && op2 == 1:
		size := [4]int{1, 2, 8, 4}[op3&3]
		switch {
		case op3 < 2 && h2&0xFF00 != 0xF000,
			op3 >= 2 && rm != 15,
			op3 >= 2 && op3 != 7 && op3 != 15 && rt2 != 15:
			d.undef32(h1, h2)
			return
		}
		switch op3 {
		case 0:
			d.set("tbb"+d.c(), "[%s, %s]", armRegs[rn], armRegs[rm])
			d.in.Kind = Jump
			d.branchInIT()
			return
		case 1:
			d.set("tbh"+d.c(), "[%s, %s, lsl #1]", armRegs[rn], armRegs[rm])
			d.in.Kind = Jump
			d.branchInIT()
			return
		case 4, 5:
			d.set([2]string{"ldrexb", "ldrexh"}[op3&1]+d.c(), "%s, [%s]", armRegs[rt], armRegs[rn])
		case 7:
			d.set("ldrexd"+d.c(), "%s, %s, [%s]", armRegs[rt], armRegs[rt2], armRegs[rn])
		case 8, 9, 10:
			d.set([3]string{"ldab", "ldah", "lda"}[op3&3]+d.c(), "%s, [%s]", armRegs[rt], armRegs[rn])
		case 12, 13, 14:
			d.set([3]string{"ldaexb", "ldaexh", "ldaex"}[op3&3]+d.c(), "%s, [%s]", armRegs[rt], armRegs[rn])
		case 15:
			d.set("ldaexd"+d.c(), "%s, %s, [%s]", armRegs[rt], armRegs[rt2], armRegs[rn])
		default:
			d.undef32(h1, h2)
			return
		}
		if size == 8 {
			d.clob(rt, rt2)
		} else {
			d.load(rt, rn, 0, size)
		}
	default:
		if h1 == 0xE97F && h2 == 0xE97F {
			d.set("sg", "")
			return
		}
		// ldrd, strd
		p := h1&0x100 != 0
		u := h1&0x80 != 0
		wb := h1&0x20 != 0
		load := h1&0x10 != 0
		off := imm
		if !u {
			off = -off
		}
		mn := "strd"
		if load {
			mn = "ldrd"
		}
		// objdump writes the offset in the comment without its sign
		abs := imm
		var addr string
		switch {
		case imm == 0 && u && p:
			// objdump leaves out a zero offset, and the writeback with it
			addr = fmt.Sprintf("[%s]", armRegs[rn])
		case imm == 0 && u:
			addr = fmt.Sprintf("[%s], #0", armRegs[rn])
		case imm == 0 && p:
			addr = fmt.Sprintf("[%s, #-0]", armRegs[rn])
			if wb {
				addr += "!"
			}
		case imm == 0:
			addr = fmt.Sprintf("[%s], #-0", armRegs[rn])
			if rn == armPC {
				d.note("0x%x", d.lit(0))
			}
		case rn == armPC:
			a := d.lit(off)
			d.note("0x%x", a)
			d.imm(abs)
			addr = fmt.Sprintf("[pc, #%d]", off)
			if !p {
				addr = fmt.Sprintf("[pc], #%d", off)
			} else if wb {
				addr += "!"
			}
			if load && p && !wb {
				d.set(mn+d.c(), "%s, %s, %s", armRegs[rt], armRegs[rt2], addr)
				d.loadLiteral(rt, a, 4)
				d.loadLiteral(rt2, a+4, 4)
				return
			}
		case p:
			d.imm(abs)
			addr = fmt.Sprintf("[%s, #%d]", armRegs[rn], off)
			if wb {
				addr += "!"
			}
		default:
			d.imm(abs)
			addr = fmt.Sprintf("[%s], #%d", armRegs[rn], off)
		}
		d.set(mn+d.c(), "%s, %s, %s", armRegs[rt], armRegs[rt2], addr)
		at := off
		if !p {
			at = 0
		}
		d.mem(rt, rn, at, 4, !load)
		d.mem(rt2, rn, at+4, 4, !load)
		if wb {
			d.addImm(rn, rn, off)
		}
		if load {
			d.clob(rt, rt2)
		}
	}
}

// optImm is ", #imm" of an offset that is left out when it is 0
func (d *tdec) optImm(v int64) string {
	if v == 0 {
		return ""
	}
	return ", " + d.imm(v)
}

// The names of the data processing instructions by their opcode
var t2DPNames = [16]string{"and", "bic", "orr", "orn", "eor", "", "pkh", "",
	"add", "", "adc", "sbc", "", "sub", "rsb", ""}

// t2DPWide tells which have a 16-bit form, so objdump writes .w after them
var t2DPWide = [16]bool{0: true, 1: true, 2: true, 4: true, 8: true, 10: true, 11: true, 13: true}

// dpName is the mnemonic of a data processing instruction with an
// immediate or a shifted register: the alias (tst, mov...) and the
// operands it takes; kind: 0 rd, rn, op2; 1 rn, op2 (compare); 2 rd, op2 (mov, mvn)
func (d *tdec) dpName(op uint16, s bool, rn, rd int) (mn string, kind int) {
	base := t2DPNames[op]
	wide := t2DPWide[op]
	switch {
	case rd == 15 && s && (op == 0 || op == 4 || op == 8 || op == 13):
		base = map[uint16]string{0: "tst", 4: "teq", 8: "cmn", 13: "cmp"}[op]
		kind = 1
		s = false
		wide = op != 4
	case rn == 15 && op == 2:
		base, kind, wide = "mov", 2, true
	case rn == 15 && op == 3:
		base, kind, wide = "mvn", 2, true
	}
	if base == "" {
		return "", 0
	}
	mn = base
	if s {
		mn += "s"
	}
	mn += d.c()
	if wide {
		mn += ".w"
	}
	return mn, kind
}

func (d *tdec) dpShifted(h1, h2 uint16) {
	op := (h1 >> 5) & 15
	s := h1&0x10 != 0
	rn := int(h1 & 15)
	rd := int(h2>>8) & 15
	rm := int(h2 & 15)
	typ := uint32(h2>>4) & 3
	imm := uint32(h2>>12)&7<<2 | uint32(h2>>6)&3
	if h2&0x8000 != 0 {
		d.undef32(h1, h2)
		return
	}
	if op == 6 {
		if s || typ&1 != 0 {
			d.undef32(h1, h2)
			return
		}
		if typ == 0 {
			d.set("pkhbt"+d.c(), "%s, %s, %s%s", armRegs[rd], armRegs[rn], armRegs[rm], shiftText(0, imm))
		} else {
			d.set("pkhtb"+d.c(), "%s, %s, %s%s", armRegs[rd], armRegs[rn], armRegs[rm], shiftText(2, imm))
		}
		d.clob(rd)
		return
	}
	mn, kind := d.dpName(op, s, rn, rd)
	if mn == "" {
		d.undef32(h1, h2)
		return
	}
	sh := shiftText(typ, imm)
	switch kind {
	case 0:
		d.set(mn, "%s, %s, %s%s", armRegs[rd], armRegs[rn], armRegs[rm], sh)
		d.clob(rd)
	case 1:
		d.set(mn, "%s, %s%s", armRegs[rn], armRegs[rm], sh)
	case 2:
		d.set(mn, "%s, %s%s", armRegs[rd], armRegs[rm], sh)
		if sh == "" && op == 2 {
			d.addImm(rd, rm, 0)
		} else {
			d.clob(rd)
		}
	}
}

// thumbExpandImm is the value of a modified immediate (i:imm3:imm8)
func thumbExpandImm(imm12 uint32) uint32 {
	imm8 := imm12 & 0xFF
	if imm12>>10 == 0 {
		switch (imm12 >> 8) & 3 {
		case 0:
			return imm8
		case 1:
			return imm8<<16 | imm8
		case 2:
			return imm8<<24 | imm8<<8
		}
		return imm8 * 0x01010101
	}
	v := 0x80 | imm12&0x7F
	rot := imm12 >> 7
	return v>>rot | v<<(32-rot)
}

func (d *tdec) dpModImm(h1, h2 uint16) {
	op := (h1 >> 5) & 15
	s := h1&0x10 != 0
	rn := int(h1 & 15)
	rd := int(h2>>8) & 15
	v := thumbExpandImm(uint32(h1>>10)&1<<11 | uint32(h2>>12)&7<<8 | uint32(h2&0xFF))
	if op == 6 {
		d.undef32(h1, h2)
		return
	}
	mn, kind := d.dpName(op, s, rn, rd)
	if mn == "" {
		d.undef32(h1, h2)
		return
	}
	im := d.imm(int64(v))
	switch kind {
	case 0:
		d.set(mn, "%s, %s, %s", armRegs[rd], armRegs[rn], im)
		switch op {
		case 8:
			d.addImm(rd, rn, int64(v))
		case 13:
			d.addImm(rd, rn, -int64(v))
		default:
			d.clob(rd)
		}
	case 1:
		d.set(mn, "%s, %s", armRegs[rn], im)
	case 2:
		d.set(mn, "%s, %s", armRegs[rd], im)
		if op == 3 {
			v = ^v
		}
		d.setConst(rd, v)
	}
}

func (d *tdec) dpPlainImm(h1, h2 uint16) {
	op := (h1 >> 4) & 31
	rn := int(h1 & 15)
	rd := int(h2>>8) & 15
	imm12 := uint32(h1>>10)&1<<11 | uint32(h2>>12)&7<<8 | uint32(h2&0xFF)
	imm5 := uint32(h2>>12)&7<<2 | uint32(h2>>6)&3 // shift or lsb
	sat := uint32(h2 & 31)
	if op >= 16 && (h1&0x400 != 0 || h2&0x20 != 0) {
		d.undef32(h1, h2)
		return
	}
	switch op {
	case 0, 10:
		mn := "addw"
		v := int64(imm12)
		if op == 10 {
			mn, v = "subw", -v
		}
		d.set(mn+d.c(), "%s, %s, %s", armRegs[rd], armRegs[rn], d.imm(int64(imm12)))
		if rn == armPC {
			d.setConst(rd, d.lit(v))
		} else {
			d.addImm(rd, rn, v)
		}
	case 4, 12:
		v := uint32(h1&15)<<12 | imm12
		if op == 4 {
			d.set("movw"+d.c(), "%s, %s", armRegs[rd], d.imm(int64(v)))
			if rd != armPC {
				d.eff(Effect{Kind: SetLow16, Dst: rd, Src: -1, Base: -1, Imm: int64(v)})
			}
		} else {
			d.set("movt"+d.c(), "%s, %s", armRegs[rd], d.imm(int64(v)))
			if rd != armPC {
				d.eff(Effect{Kind: SetHigh16, Dst: rd, Src: -1, Base: -1, Imm: int64(v)})
			}
		}
	case 16, 18, 24, 26:
		signed := op < 24
		mn := "usat"
		n := sat
		if signed {
			mn, n = "ssat", sat+1
		}
		if op&2 != 0 && imm5 == 0 {
			// objdump takes all 5 bits of the saturation of 16-bit ones
			d.set(mn+"16"+d.c(), "%s, #%d, %s", armRegs[rd], n, armRegs[rn])
		} else {
			d.set(mn+d.c(), "%s, #%d, %s%s", armRegs[rd], n, armRegs[rn], shiftText(uint32(op&2), imm5))
		}
		d.clob(rd)
	case 20, 28:
		mn := "sbfx"
		if op == 28 {
			mn = "ubfx"
		}
		d.set(mn+d.c(), "%s, %s, #%d, #%d", armRegs[rd], armRegs[rn], imm5, sat+1)
		d.clob(rd)
	case 22:
		// objdump writes a width below 1 as unsigned
		w := uint32(int(sat) - int(imm5) + 1)
		if rn == 15 {
			d.set("bfc"+d.c(), "%s, #%d, #%d", armRegs[rd], imm5, w)
		} else {
			d.set("bfi"+d.c(), "%s, %s, #%d, #%d", armRegs[rd], armRegs[rn], imm5, w)
		}
		d.clob(rd)
	default:
		d.undef32(h1, h2)
	}
}

// M-profile special registers by SYSm, as objdump names them
var armSysRegs = map[uint16]string{
	0: "CPSR", 1: "IAPSR", 2: "EAPSR", 3: "PSR", 5: "IPSR", 6: "EPSR", 7: "IEPSR",
	8: "MSP", 9: "PSP", 10: "MSPLIM", 11: "PSPLIM",
	16: "PRIMASK", 17: "BASEPRI", 18: "BASEPRI_MAX", 19: "FAULTMASK", 20: "CONTROL",
	0x88: "MSP_NS", 0x89: "PSP_NS", 0x8A: "MSPLIM_NS", 0x8B: "PSPLIM_NS",
	0x90: "PRIMASK_NS", 0x91: "BASEPRI_NS", 0x93: "FAULTMASK_NS", 0x94: "CONTROL_NS",
	0x98: "SP_NS",
}

// Barrier options
var armBarriers = [16]string{1: "oshld", 2: "oshst", 3: "osh", 5: "nshld", 6: "nshst", 7: "nsh",
	9: "ishld", 10: "ishst", 11: "ish", 13: "ld", 14: "st", 15: "sy"}

// branchMisc is the branches, msr, mrs, the hints and the barriers
func (d *tdec) branchMisc(h1, h2 uint16) {
	op := (h1 >> 4) & 0x7F
	op1 := (h2 >> 12) & 7
	s := uint32(h1>>10) & 1
	j1 := uint32(h2>>13) & 1
	j2 := uint32(h2>>11) & 1
	switch {
	case op1 == 2 && op == 0x7F:
		v := uint32(h1&15)<<12 | uint32(h2&0xFFF)
		d.set("udf"+d.c()+".w", "%s", d.imm(int64(v)))
		d.in.Kind = Interrupt
	case op1&5 == 0 && op&0x38 != 0x38:
		// b<c>.w
		cond := (h1 >> 6) & 15
		off := s<<20 | j2<<19 | j1<<18 | uint32(h1&0x3F)<<12 | uint32(h2&0x7FF)<<1
		t := uint32(int64(d.pc()) + int64(int32(off<<11)>>11))
		d.set("b"+armConds[cond]+".w", "0x%x", t)
		d.branch(Branch, t)
		d.unpredictableInIT()
	case op1 == 0:
		d.control(h1, h2)
	case op1&5 == 0:
		d.undef32(h1, h2)
	default:
		i1 := ^(j1 ^ s) & 1
		i2 := ^(j2 ^ s) & 1
		off := int64(int32((s<<24|i1<<23|i2<<22|uint32(h1&0x3FF)<<12|uint32(h2&0x7FF)<<1)<<7) >> 7)
		t := uint32(int64(d.pc()) + off)
		d.branchInIT()
		switch op1 & 5 {
		case 1:
			d.set("b"+d.c()+".w", "0x%x", t)
			d.branch(Jump, t)
		case 4:
			if h2&1 != 0 {
				d.undef32(h1, h2)
				return
			}
			t = uint32(int64(d.pc()&^3) + off)
			d.set("blx"+d.c(), "0x%x", t)
			d.branch(Call, t)
			d.in.TargetMode = ModeARM
			d.call()
		case 5:
			d.set("bl"+d.c(), "0x%x", t)
			d.branch(Call, t)
			d.call()
		}
	}
}

// control is msr, mrs, the hints, the barriers and cps
func (d *tdec) control(h1, h2 uint16) {
	op := (h1 >> 4) & 0x7F
	switch op {
	case 0x38, 0x39:
		rn := int(h1 & 15)
		sysm := h2 & 0xFF
		name, ok := armSysRegs[sysm]
		if !ok {
			d.undef32(h1, h2)
			return
		}
		if sysm == 0 {
			name = "CPSR_"
			for i, f := range "fsxc" {
				if h2&(0x800>>i) != 0 {
					name += string(f)
				}
			}
		}
		d.set("msr"+d.c(), "%s, %s", name, armRegs[rn])
	case 0x3E, 0x3F:
		rd := int(h2>>8) & 15
		sysm := h2 & 0xFF
		name, ok := armSysRegs[sysm]
		if !ok {
			d.undef32(h1, h2)
			return
		}
		d.set("mrs"+d.c(), "%s, %s", armRegs[rd], name)
		d.clob(rd)
	case 0x3A:
		if h1&15 != 15 {
			d.undef32(h1, h2)
			return
		}
		if h2&0x700 != 0 {
			d.cps(h1, h2)
			return
		}
		hint := h2 & 0xFF
		switch {
		case hint < 6:
			d.set([6]string{"nop", "yield", "wfe", "wfi", "sev", "sevl"}[hint]+d.c()+".w", "")
		case hint >= 0xF0:
			d.set("dbg"+d.c(), "#%d", hint&15)
		default:
			d.set("nop"+d.c()+".w", "{%d}", hint)
		}
	case 0x3B:
		opt := h2 & 15
		if h1&15 != 15 || h2&0xF00 != 0xF00 {
			d.undef32(h1, h2)
			return
		}
		switch (h2 >> 4) & 15 {
		case 2:
			d.set("clrex"+d.c(), "")
		case 4, 5:
			mn := "dsb"
			if h2&0x10 != 0 {
				mn = "dmb"
			}
			if armBarriers[opt] != "" {
				d.set(mn+d.c(), armBarriers[opt])
			} else {
				d.set(mn+d.c(), "#%d", opt)
			}
		case 6:
			if opt == 15 {
				d.set("isb"+d.c(), "sy")
			} else {
				d.set("isb"+d.c(), "#%d", opt)
			}
		default:
			d.undef32(h1, h2)
		}
	default:
		d.undef32(h1, h2)
	}
}

// cps is the 32-bit cps (not of M-profile, where it is 16-bit only)
func (d *tdec) cps(h1, h2 uint16) {
	imod := (h2 >> 9) & 3
	var ops []string
	if imod >= 2 {
		f := ""
		for i, c := range "aif" {
			if h2&(0x80>>i) != 0 {
				f += string(c)
			}
		}
		ops = append(ops, f)
	}
	if h2&0x100 != 0 {
		ops = append(ops, fmt.Sprintf("#%d", h2&31))
	}
	if imod == 1 || len(ops) == 0 || imod >= 2 && h2&0xE0 == 0 || h2&0x100 == 0 && h2&31 != 0 {
		d.undef32(h1, h2)
		return
	}
	d.set([4]string{"cps", "", "cpsie", "cpsid"}[imod], strings.Join(ops, ", "))
	d.unpredictableInIT()
}

// loadStore is ldr, str and their byte, halfword and signed kinds, pld
// and pli
func (d *tdec) loadStore(h1, h2 uint16) {
	signed := h1&0x100 != 0
	size := 1 << ((h1 >> 5) & 3)
	load := h1&0x10 != 0
	rn := int(h1 & 15)
	rt := int(h2 >> 12)
	if size == 8 || (signed && (!load || size == 4)) {
		d.undef32(h1, h2)
		return
	}
	mn := map[int]string{1: "b", 2: "h", 4: ""}[size]
	if signed {
		mn = "s" + mn
	}
	if load {
		mn = "ldr" + mn
	} else {
		mn = "str" + mn
	}
	// pld and pli are the byte loads to the pc
	hint := ""
	if load && rt == 15 && size == 1 {
		hint = "pld"
		if signed {
			hint = "pli"
		}
	}
	if load && rt == 15 && size == 2 && !signed {
		hint = "pldw"
	}
	w := d.c() + ".w"
	switch {
	case rn == armPC:
		// A store relative to the pc is unpredictable, objdump shows it
		off := int64(h2 & 0xFFF)
		if h1&0x80 == 0 {
			off = -off
		}
		a := d.lit(off)
		if h1&0x80 == 0 && h2&0xF00 == 0xE00 && hint == "" {
			// objdump takes the unprivileged form first
			w = "t" + d.c()
		}
		offs := fmt.Sprint(off)
		if h1&0x80 == 0 && off == 0 {
			offs = "-0" // the encoding of the subtraction: GNU as makes the addition
			d.in.Asm = fmt.Sprintf(".inst.w 0x%04x%04x", h1, h2)
		}
		if hint != "" {
			d.set(hint+d.c(), "[pc, #%s]", offs)
		} else {
			d.set(mn+w, "%s, [pc, #%s]", armRegs[rt], offs)
		}
		d.note("0x%x", a)
		switch {
		case hint != "" || !load:
		case rt == armPC:
			d.in.Kind = Jump
			if size == 4 { // ldr pc, =f: the analysis follows it
				d.eff(Effect{Kind: LoadLiteral, Dst: armPC, Src: -1, Base: -1, Addr: uint64(a), Size: 4})
			}
		case signed:
			d.clob(rt)
		default:
			d.loadLiteral(rt, a, size)
		}
		return
	case h1&0x80 != 0:
		// A 12-bit offset
		off := int64(h2 & 0xFFF)
		addr := fmt.Sprintf("[%s, %s]", armRegs[rn], d.imm(off))
		if off == 0 {
			addr = fmt.Sprintf("[%s]", armRegs[rn])
		}
		d.single(mn+w, hint, rt, rn, addr, off, size, load)
	case h2&0x800 != 0:
		// An 8-bit offset, indexed
		off := int64(h2 & 0xFF)
		p := h2&0x400 != 0
		u := h2&0x200 != 0
		wb := h2&0x100 != 0
		unpriv := p && u && !wb // ldrt, strt...
		if !u {
			off = -off
		}
		var addr string
		switch {
		case !p && !wb:
			d.undef32(h1, h2)
			return
		case p && off == 0:
			addr = fmt.Sprintf("[%s]", armRegs[rn])
			if wb {
				addr += "!"
			}
		case p && !wb:
			addr = fmt.Sprintf("[%s, #%d]", armRegs[rn], off)
		case p:
			addr = fmt.Sprintf("[%s, #%d]!", armRegs[rn], off)
		default:
			addr = fmt.Sprintf("[%s], #%d", armRegs[rn], off)
		}
		if unpriv {
			if hint != "" {
				d.set(hint+d.c(), addr)
				return
			}
			d.single(mn+"t"+d.c(), "", rt, rn, addr, off, size, load)
			return
		}
		at := off
		if !p {
			at = 0
		}
		d.single(mn+w, hint, rt, rn, addr, at, size, load)
		if wb {
			d.addImm(rn, rn, off)
		}
	case h2&0xF00 == 0:
		// A register offset (objdump does not check bits 7:6, which are 0)
		rm := int(h2 & 15)
		addr := fmt.Sprintf("[%s, %s]", armRegs[rn], armRegs[rm])
		if sh := (h2 >> 4) & 3; sh != 0 {
			addr = fmt.Sprintf("[%s, %s, lsl #%d]", armRegs[rn], armRegs[rm], sh)
		}
		if hint != "" {
			d.set(hint+d.c(), addr)
			return
		}
		d.set(mn+w, "%s, %s", armRegs[rt], addr)
		if load {
			d.clob(rt)
			if rt == armPC {
				d.in.Kind = Jump
			}
		}
	default:
		d.undef32(h1, h2)
	}
}

// single is a load or a store of one register at base+off
func (d *tdec) single(mn, hint string, rt, rn int, addr string, off int64, size int, load bool) {
	if hint != "" {
		d.set(hint+d.c(), addr)
		return
	}
	d.set(mn, "%s, %s", armRegs[rt], addr)
	if !load {
		d.mem(rt, rn, off, size, true)
		return
	}
	d.load(rt, rn, off, size)
	if rt == armPC {
		d.in.Kind = Jump
		if rn == armSP {
			d.in.Kind = Return
		}
	}
}

// dpReg is the shifts by a register, the extends, the parallel additions
// and subtractions, and qadd, rev, rbit, sel, clz
func (d *tdec) dpReg(h1, h2 uint16) {
	op1 := (h1 >> 4) & 15
	op2 := (h2 >> 4) & 15
	rn := int(h1 & 15)
	rd := int(h2>>8) & 15
	rm := int(h2 & 15)
	if h2&0xF000 != 0xF000 {
		d.undef32(h1, h2)
		return
	}
	switch {
	case op1 < 8 && op2 == 0:
		mn := [4]string{"lsl", "lsr", "asr", "ror"}[op1>>1]
		if op1&1 != 0 {
			mn += "s"
		}
		d.set(mn+d.c()+".w", "%s, %s, %s", armRegs[rd], armRegs[rn], armRegs[rm])
		if op1&1 != 0 && (rd == armPC || rn == armPC || rm == armPC) {
			d.note("<UNPREDICTABLE>")
		}
	case op1 < 6 && op2&8 != 0:
		if op2&4 != 0 {
			d.undef32(h1, h2)
			return
		}
		rot := ""
		if r := (h2 >> 4) & 3; r != 0 {
			rot = fmt.Sprintf(", ror #%d", r*8)
		}
		names := [6][2]string{{"sxtah", "sxth"}, {"uxtah", "uxth"}, {"sxtab16", "sxtb16"},
			{"uxtab16", "uxtb16"}, {"sxtab", "sxtb"}, {"uxtab", "uxtb"}}
		if rn == 15 {
			w := ".w"
			if op1 == 2 || op1 == 3 {
				w = ""
			}
			d.set(names[op1][1]+d.c()+w, "%s, %s%s", armRegs[rd], armRegs[rm], rot)
		} else {
			d.set(names[op1][0]+d.c(), "%s, %s, %s%s", armRegs[rd], armRegs[rn], armRegs[rm], rot)
		}
	case op1 >= 8 && op2 < 8:
		pre := [8]string{"s", "q", "sh", "", "u", "uq", "uh", ""}[op2]
		op := [8]string{"add8", "add16", "asx", "", "sub8", "sub16", "sax", ""}[op1&7]
		if pre == "" || op == "" {
			d.undef32(h1, h2)
			return
		}
		d.set(pre+op+d.c(), "%s, %s, %s", armRegs[rd], armRegs[rn], armRegs[rm])
	case op1 >= 8 && op1 < 12 && op2 >= 8 && op2 < 12:
		switch op1 & 3 {
		case 0:
			mn := [4]string{"qadd", "qdadd", "qsub", "qdsub"}[op2&3]
			d.set(mn+d.c(), "%s, %s, %s", armRegs[rd], armRegs[rm], armRegs[rn])
		case 1:
			// Rm is in both fields; objdump shows the first
			mn := [4]string{"rev.w", "rev16.w", "rbit", "revsh.w"}[op2&3]
			b, w, _ := cutDot(mn)
			d.set(b+d.c()+w, "%s, %s", armRegs[rd], armRegs[rn])
		case 2:
			if op2 != 8 {
				d.undef32(h1, h2)
				return
			}
			d.set("sel"+d.c(), "%s, %s, %s", armRegs[rd], armRegs[rn], armRegs[rm])
		case 3:
			if op2 != 8 {
				d.undef32(h1, h2)
				return
			}
			d.set("clz"+d.c(), "%s, %s", armRegs[rd], armRegs[rn])
		}
	default:
		d.undef32(h1, h2)
		return
	}
	d.clob(rd)
}

// cutDot splits "rev.w" into "rev" and ".w"
func cutDot(s string) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			return s[:i], s[i:], true
		}
	}
	return s, "", false
}

func (d *tdec) multiply(h1, h2 uint16) {
	op1 := (h1 >> 4) & 7
	op2 := (h2 >> 4) & 3
	rn := int(h1 & 15)
	ra := int(h2 >> 12)
	rd := int(h2>>8) & 15
	rm := int(h2 & 15)
	if h2&0xC0 != 0 {
		d.undef32(h1, h2)
		return
	}
	bt := [2]string{"b", "t"}
	var mn, mn3 string // the names with Ra and without (Ra = 15)
	switch op1 {
	case 0:
		switch op2 {
		case 0:
			mn, mn3 = "mla", "mul"
		case 1:
			mn = "mls"
		}
	case 1:
		xy := bt[op2>>1] + bt[op2&1]
		mn, mn3 = "smla"+xy, "smul"+xy
	case 2, 4:
		if op2 < 2 {
			x := [2]string{"", "x"}[op2]
			if op1 == 2 {
				mn, mn3 = "smlad"+x, "smuad"+x
			} else {
				mn, mn3 = "smlsd"+x, "smusd"+x
			}
		}
	case 3:
		if op2 < 2 {
			mn, mn3 = "smlaw"+bt[op2], "smulw"+bt[op2]
		}
	case 5, 6:
		if op2 < 2 {
			r := [2]string{"", "r"}[op2]
			if op1 == 5 {
				mn, mn3 = "smmla"+r, "smmul"+r
			} else {
				mn = "smmls" + r
			}
		}
	case 7:
		if op2 == 0 {
			mn, mn3 = "usada8", "usad8"
		}
	}
	switch {
	case mn == "":
		d.undef32(h1, h2)
		return
	case ra == 15 && mn3 != "":
		w := ""
		if mn3 == "mul" {
			w = ".w"
		}
		d.set(mn3+d.c()+w, "%s, %s, %s", armRegs[rd], armRegs[rn], armRegs[rm])
		ra = 0
		op1 = 0 // objdump does not mark the pc in the forms without ra
	default:
		d.set(mn+d.c(), "%s, %s, %s, %s", armRegs[rd], armRegs[rn], armRegs[rm], armRegs[ra])
	}
	if op1 > 1 && (rd == armPC || rn == armPC || rm == armPC || ra == armPC) {
		d.note("<UNPREDICTABLE>")
	}
	d.clob(rd)
}

func (d *tdec) longMultiply(h1, h2 uint16) {
	op1 := (h1 >> 4) & 7
	op2 := (h2 >> 4) & 15
	rn := int(h1 & 15)
	lo := int(h2 >> 12)
	hi := int(h2>>8) & 15
	rm := int(h2 & 15)
	bt := [2]string{"b", "t"}
	mn := ""
	switch {
	case op1 == 1 && op2 == 15, op1 == 3 && op2 == 15:
		if lo != 15 {
			d.undef32(h1, h2)
			return
		}
		mn = "sdiv"
		if op1 == 3 {
			mn = "udiv"
		}
		d.set(mn+d.c(), "%s, %s, %s", armRegs[hi], armRegs[rn], armRegs[rm])
		d.clob(hi)
		return
	case op2 == 0 && op1 != 1 && op1 != 3 && op1 != 5 && op1 != 7:
		mn = [8]string{"smull", "", "umull", "", "smlal", "", "umlal", ""}[op1]
	case op1 == 4 && op2&12 == 8:
		mn = "smlal" + bt[op2>>1&1] + bt[op2&1]
	case op1 == 4 && op2&14 == 12:
		mn = "smlald" + [2]string{"", "x"}[op2&1]
	case op1 == 5 && op2&14 == 12:
		mn = "smlsld" + [2]string{"", "x"}[op2&1]
	case op1 == 6 && op2 == 6:
		mn = "umaal"
	default:
		d.undef32(h1, h2)
		return
	}
	d.set(mn+d.c(), "%s, %s, %s, %s", armRegs[lo], armRegs[hi], armRegs[rn], armRegs[rm])
	if !(op1 == 4 && op2&12 == 8) && (lo == armPC || hi == armPC || rn == armPC || rm == armPC) {
		d.note("<UNPREDICTABLE>")
	}
	d.clob(lo, hi)
}
