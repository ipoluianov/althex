package disasm

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// The coprocessor instructions: the floating-point extension (FPv4, FPv5,
// single and double precision) and the generic mcr, mrc, ldc, stc, cdp

func (d *tdec) coproc(h1, h2 uint16) {
	cp := (h2 >> 8) & 15
	t := h1&0x1000 != 0
	op1 := (h1 >> 4) & 0x3F
	switch {
	case h1&0xEF00 == 0xEF00:
		// Advanced SIMD (not in M-profile)
		d.undef32(h1, h2)
	case cp&14 == 10 && t:
		d.vfpV8(h1, h2)
		if d.in.Kind == Invalid {
			d.reset()
			d.coprocUnpredictable(h1, h2)
		}
	case cp == 9 && t:
		d.coprocUnpredictable(h1, h2)
	case cp == 9 && op1&0x31 == 0x21 && h2&0xF010 == 0xF010:
		// mrc to APSR_nzcv: objdump has them
		d.coprocGeneric(h1, h2)
		if d.cond >= 0 {
			d.note("<UNPREDICTABLE>")
		}
	case cp == 9 && op1&0x3E != 0x04:
		// Half precision (ARMv8.1-M) is not decoded; objdump has the other
		// encodings of coprocessor 9 as undefined
		d.undef32(h1, h2)
	case cp&14 == 10 && op1&0x3E == 0x04:
		d.vmov64(h1, h2)
		if d.in.Kind == Invalid {
			// objdump shows the others as mcrr, mrrc
			d.reset()
			d.coprocGeneric(h1, h2)
		}
	case cp&14 == 10:
		d.vfp(h1, h2, op1)
		if d.in.Kind == Invalid && op1&0x31 == 0x21 && h2&0xF010 == 0xF010 {
			// objdump shows the other transfers to the pc as mrc
			d.reset()
			d.coprocGeneric(h1, h2)
		}
	default:
		d.coprocGeneric(h1, h2)
	}
}

// coprocUnpredictable is the coprocessor instructions with a "2" of the
// coprocessors of the floating-point: objdump shows them as generic, but
// unpredictable
func (d *tdec) coprocUnpredictable(h1, h2 uint16) {
	op1 := (h1 >> 4) & 0x3F
	if op1&0x30 == 0x20 && h2&0x10 == 0 {
		d.undef32(h1, h2) // cdp2
		return
	}
	d.coprocGeneric(h1, h2)
	if op1&0x3E == 0x04 || d.in.Kind == Invalid {
		return // mcrr2, mrrc2 are only for the pc
	}
	if n := len(d.comments); n == 0 || d.comments[n-1] != "<UNPREDICTABLE>" {
		d.note("<UNPREDICTABLE>")
	}
}

// vfp is the floating-point instructions of coprocessors 10 and 11
func (d *tdec) vfp(h1, h2, op1 uint16) {
	switch {
	case op1&0x20 == 0 && op1&0x3A != 0:
		d.vldst(h1, h2)
	case op1&0x30 == 0x20 && h2&0x10 == 0:
		d.vfpData(h1, h2)
	case op1&0x30 == 0x20:
		d.vmovCore(h1, h2)
	default:
		d.undef32(h1, h2)
	}
}

// sReg and dReg are the names of the registers of an encoding: the four
// bits and the one more (Vd and D)
func sReg(v, x uint16) string { return fmt.Sprintf("s%d", v<<1|x) }
func dReg(v, x uint16) string { return fmt.Sprintf("d%d", x<<4|v) }

// fReg is sReg or dReg by the size
func fReg(dbl bool, v, x uint16) string {
	if dbl {
		return dReg(v, x)
	}
	return sReg(v, x)
}

// vldst is vldr, vstr, vldm, vstm, vpush, vpop
func (d *tdec) vldst(h1, h2 uint16) {
	p := h1&0x100 != 0
	u := h1&0x80 != 0
	dx := (h1 >> 6) & 1
	wb := h1&0x20 != 0
	load := h1&0x10 != 0
	rn := int(h1 & 15)
	vd := h2 >> 12
	dbl := h2&0x100 != 0
	imm8 := h2 & 0xFF
	size := 4
	if dbl {
		size = 8
	}
	if p && !wb {
		// vldr, vstr
		off := int64(imm8) * 4
		if !u {
			off = -off
		}
		mn := "vstr"
		if load {
			mn = "vldr"
		}
		reg := fReg(dbl, vd, dx)
		switch {
		case rn == armPC && !u && off == 0:
			d.set(mn+d.c(), "%s, [pc, #-0]", reg)
			d.in.Asm = fmt.Sprintf(".inst.w 0x%04x%04x", h1, h2)
			d.note("0x%x", d.lit(off))
		case rn == armPC:
			d.set(mn+d.c(), "%s, [pc, #%d]", reg, off)
			d.note("0x%x", d.lit(off))
		case off == 0 && u:
			d.set(mn+d.c(), "%s, [%s]", reg, armRegs[rn])
		case off == 0:
			d.set(mn+d.c(), "%s, [%s, #-0]", reg, armRegs[rn])
		default:
			d.set(mn+d.c(), "%s, [%s, %s]", reg, armRegs[rn], d.imm(off))
		}
		d.mem(-1, rn, off, size, !load)
		return
	}
	if p == u || (!p && !u) {
		d.undef32(h1, h2)
		return
	}
	// vldm, vstm, vpush, vpop
	var list string
	n := int64(imm8) * 4
	w := ""
	if wb {
		w = "!"
	}
	if dbl && imm8&1 != 0 {
		// fldmx, fstmx: deprecated
		mn := map[bool]string{false: "fstm", true: "fldm"}[load] + map[bool]string{false: "db", true: "ia"}[u] + "x"
		d.set(mn+d.c(), "%s%s, %s", armRegs[rn], w, vfpList("d", dx<<4|vd, int(imm8>>1)))
		d.note("Deprecated")
	} else if dbl {
		// As objdump: 6 bits of the count, a mark past d31
		first := int(dx<<4 | vd)
		cnt := int(imm8>>1) & 0x3F
		list = vfpList("d", dx<<4|vd, cnt)
		if last := first + cnt - 1; last > 31 {
			list = fmt.Sprintf("{d%d-<overflow reg d%d>}", first, last)
		}
	} else {
		list = vfpList("s", vd<<1|dx, int(imm8))
	}
	switch {
	case dbl && imm8&1 != 0:
	case load && wb && u && rn == armSP:
		d.set("vpop"+d.c(), list)
	case !load && wb && !u && rn == armSP:
		d.set("vpush"+d.c(), list)
	default:
		mn := "vstm"
		if load {
			mn = "vldm"
		}
		if u {
			mn += "ia"
		} else {
			mn += "db"
		}
		d.set(mn+d.c(), "%s%s, %s", armRegs[rn], w, list)
	}
	if wb {
		if !u {
			n = -n
		}
		d.addImm(rn, rn, n)
	}
}

// vfpList is "{s16-s31}", "{d8}"
func vfpList(k string, first uint16, n int) string {
	if n == 1 {
		return fmt.Sprintf("{%s%d}", k, first)
	}
	return fmt.Sprintf("{%s%d-%s%d}", k, first, k, int(first)+n-1)
}

// vmov64 is vmov between two core registers and a double or two singles
func (d *tdec) vmov64(h1, h2 uint16) {
	rt2 := int(h1 & 15)
	rt := int(h2 >> 12)
	toCore := h1&0x10 != 0
	if h2&0xD0 != 0x10 {
		d.undef32(h1, h2)
		return
	}
	vm := h2 & 15
	m := (h2 >> 5) & 1
	var regs string
	if h2&0x100 != 0 {
		regs = dReg(vm, m)
	} else {
		n := vm<<1 | m
		regs = fmt.Sprintf("s%d, s%d", n, n+1)
	}
	if toCore {
		d.set("vmov"+d.c(), "%s, %s, %s", armRegs[rt], armRegs[rt2], regs)
		d.clob(rt, rt2)
	} else {
		d.set("vmov"+d.c(), "%s, %s, %s", regs, armRegs[rt], armRegs[rt2])
	}
}

// The floating-point system registers by the number of vmrs and vmsr
var vfpSysRegs = map[uint16]string{0: "fpsid", 1: "fpscr", 5: "mvfr2", 6: "mvfr1", 7: "mvfr0", 8: "fpexc", 9: "fpinst", 10: "fpinst2"}

// vmovCore is vmov between a core register and a single or a scalar, vmrs
// and vmsr
func (d *tdec) vmovCore(h1, h2 uint16) {
	a := (h1 >> 5) & 7
	toCore := h1&0x10 != 0
	vn := h1 & 15
	n := (h2 >> 7) & 1
	rt := int(h2 >> 12)
	c := h2&0x100 != 0
	if h2&0xF != 0 && !c {
		d.undef32(h1, h2)
		return
	}
	switch {
	case !c && a == 0:
		if h2&0x60 != 0 {
			d.undef32(h1, h2)
			return
		}
		if toCore {
			d.set("vmov"+d.c(), "%s, %s", armRegs[rt], sReg(vn, n))
			d.clob(rt)
		} else {
			d.set("vmov"+d.c(), "%s, %s", sReg(vn, n), armRegs[rt])
		}
	case !c && a == 7:
		if h2&0xE0 != 0 {
			d.undef32(h1, h2)
			return
		}
		reg, ok := vfpSysRegs[vn]
		if !ok {
			d.undef32(h1, h2)
			return
		}
		if toCore {
			if rt == 15 && vn == 1 {
				d.set("vmrs"+d.c(), "APSR_nzcv, %s", reg)
				return
			}
			d.set("vmrs"+d.c(), "%s, %s", armRegs[rt], reg)
			d.clob(rt)
		} else {
			d.set("vmsr"+d.c(), "%s, %s", reg, armRegs[rt])
		}
	case c && (toCore || a&4 == 0):
		// vmov.32 d0[1], r0; vmov.u8 r0, d0[7]
		opc := (h1>>5)&3<<2 | (h2>>5)&3
		var size string
		var idx uint16
		switch {
		case opc&8 != 0:
			size, idx = "8", opc&7
		case opc&1 != 0:
			size, idx = "16", opc>>1&3
		case opc&3 == 0:
			size, idx = "32", opc>>2&1
		default:
			d.undef32(h1, h2)
			return
		}
		scalar := fmt.Sprintf("%s[%d]", dReg(vn, n), idx)
		if toCore {
			if size != "32" {
				size = [2]string{"s", "u"}[(h1>>7)&1] + size
			}
			d.set("vmov"+d.c()+"."+size, "%s, %s", armRegs[rt], scalar)
			d.clob(rt)
		} else {
			d.set("vmov"+d.c()+"."+size, "%s, %s", scalar, armRegs[rt])
		}
	default:
		d.undef32(h1, h2)
	}
}

// vfpData is the floating-point data processing instructions
func (d *tdec) vfpData(h1, h2 uint16) {
	dbl := h2&0x100 != 0
	ty := ".f32"
	if dbl {
		ty = ".f64"
	}
	dx := (h1 >> 6) & 1
	vn := h1 & 15
	n := (h2 >> 7) & 1
	vd := h2 >> 12
	m := (h2 >> 5) & 1
	vm := h2 & 15
	op := (h2 >> 6) & 1
	rd := fReg(dbl, vd, dx)
	rn := fReg(dbl, vn, n)
	rm := fReg(dbl, vm, m)
	three := func(mn string) {
		d.set(mn+d.c()+ty, "%s, %s, %s", rd, rn, rm)
	}
	opc := (h1>>7)&1<<2 | (h1>>4)&3
	switch opc {
	case 0:
		three([2]string{"vmla", "vmls"}[op])
	case 1:
		three([2]string{"vnmls", "vnmla"}[op])
	case 2:
		three([2]string{"vmul", "vnmul"}[op])
	case 3:
		three([2]string{"vadd", "vsub"}[op])
	case 4:
		if op != 0 {
			d.undef32(h1, h2)
			return
		}
		three("vdiv")
	case 5:
		three([2]string{"vfnms", "vfnma"}[op])
	case 6:
		three([2]string{"vfma", "vfms"}[op])
	default:
		d.vfpOther(h1, h2, dbl, ty)
	}
}

// vfpOther is the floating-point instructions of one or two operands:
// vmov, vabs, vneg, vsqrt, vcmp, vcvt, vrint
func (d *tdec) vfpOther(h1, h2 uint16, dbl bool, ty string) {
	dx := (h1 >> 6) & 1
	opc2 := h1 & 15
	vd := h2 >> 12
	m := (h2 >> 5) & 1
	vm := h2 & 15
	b7 := (h2 >> 7) & 1
	rd := fReg(dbl, vd, dx)
	rm := fReg(dbl, vm, m)
	two := func(mn string) {
		d.set(mn+d.c()+ty, "%s, %s", rd, rm)
	}
	if h2&0x40 == 0 {
		// vmov with an immediate
		if h2&0xA0 != 0 {
			d.undef32(h1, h2)
			return
		}
		imm8 := uint32(opc2)<<4 | uint32(vm)
		v := vfpImm(imm8)
		d.set("vmov"+d.c()+ty, "%s, #%d", rd, imm8)
		d.note("0x%08x %s", math.Float32bits(float32(v)), floatText(v))
		f := strconv.FormatFloat(v, 'f', -1, 64)
		if !strings.Contains(f, ".") {
			f += ".0"
		}
		d.in.Asm = fmt.Sprintf("%s %s, #%s", d.in.Mnemonic, rd, f)
		return
	}
	switch opc2 {
	case 0:
		two([2]string{"vmov", "vabs"}[b7])
	case 1:
		two([2]string{"vneg", "vsqrt"}[b7])
	case 2, 3:
		// vcvtb, vcvtt between half and single or double
		mn := [2]string{"vcvtb", "vcvtt"}[b7]
		if opc2 == 2 {
			d.set(mn+d.c()+ty+".f16", "%s, %s", rd, sReg(vm, m))
		} else {
			d.set(mn+d.c()+".f16"+ty, "%s, %s", sReg(vd, dx), rm)
		}
	case 4:
		two([2]string{"vcmp", "vcmpe"}[b7])
	case 5:
		d.set([2]string{"vcmp", "vcmpe"}[b7]+d.c()+ty, "%s, #0.0", rd)
	case 6:
		two([2]string{"vrintr", "vrintz"}[b7])
	case 7:
		if b7 == 0 {
			two("vrintx")
			return
		}
		// vcvt between single and double
		if dbl {
			d.set("vcvt"+d.c()+".f32.f64", "%s, %s", sReg(vd, dx), rm)
		} else {
			d.set("vcvt"+d.c()+".f64.f32", "%s, %s", dReg(vd, dx), rm)
		}
	case 8:
		// vcvt from an integer
		d.set("vcvt"+d.c()+ty+[2]string{".u32", ".s32"}[b7], "%s, %s", rd, sReg(vm, m))
	case 10, 11, 14, 15:
		// vcvt between floating-point and fixed-point
		u := opc2&1 != 0
		sx := h2&0x80 != 0
		size := uint16(16)
		if sx {
			size = 32
		}
		fbits := int(size) - int(vm<<1|m)
		fx := fmt.Sprintf(".%s%d", map[bool]string{false: "s", true: "u"}[u], size)
		if opc2&4 != 0 {
			d.set("vcvt"+d.c()+fx+ty, "%s, %s, #%d", rd, rd, fbits)
		} else {
			d.set("vcvt"+d.c()+ty+fx, "%s, %s, #%d", rd, rd, fbits)
		}
	case 12, 13:
		// vcvt to an integer
		mn := "vcvtr"
		if b7 != 0 {
			mn = "vcvt"
		}
		d.set(mn+d.c()+[2]string{".u32", ".s32"}[opc2&1]+ty, "%s, %s", sReg(vd, dx), rm)
	default:
		d.undef32(h1, h2)
	}
}

// vfpV8 is the FPv5 instructions that are not conditional: vsel, vmaxnm,
// vminnm, vrinta/n/p/m, vcvta/n/p/m
func (d *tdec) vfpV8(h1, h2 uint16) {
	dbl := h2&0x100 != 0
	ty := ".f32"
	if dbl {
		ty = ".f64"
	}
	dx := (h1 >> 6) & 1
	vn := h1 & 15
	n := (h2 >> 7) & 1
	vd := h2 >> 12
	m := (h2 >> 5) & 1
	vm := h2 & 15
	rd := fReg(dbl, vd, dx)
	rn := fReg(dbl, vn, n)
	rm := fReg(dbl, vm, m)
	if h2&0x10 != 0 {
		d.undef32(h1, h2)
		return
	}
	switch {
	case h1&0xFF80 == 0xFE00 && h2&0x40 == 0:
		cc := [4]string{"eq", "vs", "ge", "gt"}[(h1>>4)&3]
		d.set("vsel"+cc+d.c()+ty, "%s, %s, %s", rd, rn, rm)
	case h1&0xFFB0 == 0xFE80:
		d.set([2]string{"vmaxnm", "vminnm"}[(h2>>6)&1]+d.c()+ty, "%s, %s, %s", rd, rn, rm)
	case h1&0xFFBC == 0xFEB8 && h2&0xC0 == 0x40:
		d.set("vrint"+[4]string{"a", "n", "p", "m"}[h1&3]+d.c()+ty, "%s, %s", rd, rm)
	case h1&0xFFBC == 0xFEBC && h2&0x40 == 0x40:
		d.set("vcvt"+[4]string{"a", "n", "p", "m"}[h1&3]+d.c()+[2]string{".u32", ".s32"}[(h2>>7)&1]+ty, "%s, %s", sReg(vd, dx), rm)
	default:
		d.undef32(h1, h2)
		return
	}
	// They cannot be conditional
	if d.cond >= 0 {
		d.note("<UNPREDICTABLE>")
	}
}

// coprocGeneric is mcr, mrc, mcrr, mrrc, ldc, stc, cdp and their "2" kinds
func (d *tdec) coprocGeneric(h1, h2 uint16) {
	two := ""
	c := d.c()
	if h1&0x1000 != 0 {
		two = "2"
	}
	op1 := (h1 >> 4) & 0x3F
	cp := (h2 >> 8) & 15
	rn := int(h1 & 15)
	rt := int(h2 >> 12)
	crm := h2 & 15
	switch {
	case op1&0x3E == 0x04:
		mn := "mcrr"
		if op1&1 != 0 {
			mn = "mrrc"
		}
		d.set(mn+two+c, "%d, %d, %s, %s, cr%d", cp, (h2>>4)&15, armRegs[rt], armRegs[rn], crm)
		if rt == armPC || (op1&1 != 0 || two != "") && rn == armPC || op1&1 != 0 && rt == rn ||
			cp == 9 && two == "" && d.cond >= 0 {
			d.note("<UNPREDICTABLE>")
		}
		if op1&1 != 0 {
			d.clob(rt, rn)
		}
	case op1&0x20 == 0:
		// ldc, stc
		p := h1&0x100 != 0
		u := h1&0x80 != 0
		wb := h1&0x20 != 0
		mn := "stc"
		if h1&0x10 != 0 {
			mn = "ldc"
		}
		l := ""
		if h1&0x40 != 0 {
			l = "l"
		}
		off := int64(h2&0xFF) * 4
		if !u {
			off = -off
		}
		var addr string
		switch {
		case off == 0 && u && (p || wb):
			addr = fmt.Sprintf("[%s]", armRegs[rn])
		case off == 0 && p:
			addr = fmt.Sprintf("[%s, #-0]", armRegs[rn])
		case off == 0 && wb:
			addr = fmt.Sprintf("[%s], #-0", armRegs[rn])
		case off == 0 && !u:
			addr = fmt.Sprintf("[%s], {-0}", armRegs[rn])
		case p && !wb:
			addr = fmt.Sprintf("[%s, #%d]", armRegs[rn], off)
		case p:
			addr = fmt.Sprintf("[%s, #%d]!", armRegs[rn], off)
		case wb:
			addr = fmt.Sprintf("[%s], #%d", armRegs[rn], off)
		default:
			addr = fmt.Sprintf("[%s], {%d}", armRegs[rn], h2&0xFF)
		}
		d.set(mn+two+l+c, "%d, cr%d, %s", cp, rt, addr)
		switch {
		case !p && !wb:
			d.imm(int64(h2 & 0xFF))
		case rn == armPC:
			d.note("0x%x", d.lit(off))
		default:
			d.imm(off)
		}
		if wb {
			d.addImm(rn, rn, off)
		}
	case h2&0x10 == 0:
		d.set("cdp"+two+c, "%d, %d, cr%d, cr%d, cr%d, {%d}", cp, (h1>>4)&15, rt, rn, crm, (h2>>5)&7)
	default:
		mn := "mcr"
		reg := armRegs[rt]
		if h1&0x10 != 0 {
			mn = "mrc"
			if rt == armPC && two == "" {
				reg = "APSR_nzcv"
			}
			d.clob(rt)
		} else if rt == armPC {
			d.note("<UNPREDICTABLE>")
		}
		d.set(mn+two+c, "%d, %d, %s, cr%d, cr%d, {%d}", cp, (h1>>5)&7, reg, rn, crm, (h2>>5)&7)
	}
}
