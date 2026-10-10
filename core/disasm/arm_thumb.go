package disasm

// The 16-bit Thumb instructions (ARMv6-M and ARMv7-M)

func (d *tdec) thumb16(h uint16) {
	r0 := int(h & 7)
	r3 := int(h>>3) & 7
	r6 := int(h>>6) & 7
	r8 := int(h>>8) & 7
	imm8 := uint32(h & 0xFF)
	switch h >> 12 {
	case 0, 1:
		op := (h >> 11) & 3
		if op == 3 {
			// Add and subtract, a register or a 3-bit immediate
			mn := [4]string{"add", "sub", "add", "sub"}[(h>>9)&3] + d.s()
			if h&0x400 == 0 {
				d.set(mn, "%s, %s, %s", armRegs[r0], armRegs[r3], armRegs[r6])
				d.clob(r0)
				return
			}
			d.set(mn, "%s, %s, #%d", armRegs[r0], armRegs[r3], r6)
			v := int64(r6)
			if h&0x200 != 0 {
				v = -v
			}
			d.addImm(r0, r3, v)
			return
		}
		imm := uint32(h>>6) & 31
		if op == 0 && imm == 0 {
			d.set("mov"+d.s(), "%s, %s", armRegs[r0], armRegs[r3])
			d.addImm(r0, r3, 0)
			return
		}
		if op != 0 && imm == 0 {
			imm = 32
		}
		d.set([3]string{"lsl", "lsr", "asr"}[op]+d.s(), "%s, %s, #%d", armRegs[r0], armRegs[r3], imm)
		d.clob(r0)
	case 2, 3:
		// mov, cmp, add, sub with an 8-bit immediate
		switch (h >> 11) & 3 {
		case 0:
			d.set("mov"+d.s(), "%s, %s", armRegs[r8], d.imm(int64(imm8)))
			d.setConst(r8, imm8)
		case 1:
			d.set("cmp"+d.c(), "%s, %s", armRegs[r8], d.imm(int64(imm8)))
		case 2:
			d.set("add"+d.s(), "%s, %s", armRegs[r8], d.imm(int64(imm8)))
			d.addImm(r8, r8, int64(imm8))
		case 3:
			d.set("sub"+d.s(), "%s, %s", armRegs[r8], d.imm(int64(imm8)))
			d.addImm(r8, r8, -int64(imm8))
		}
	case 4:
		switch {
		case h&0x0C00 == 0:
			// Data processing on two low registers
			op := (h >> 6) & 15
			mn := [16]string{"and", "eor", "lsl", "lsr", "asr", "adc", "sbc", "ror",
				"tst", "neg", "cmp", "cmn", "orr", "mul", "bic", "mvn"}[op]
			switch op {
			case 8, 10, 11:
				d.set(mn+d.c(), "%s, %s", armRegs[r0], armRegs[r3])
			default:
				d.set(mn+d.s(), "%s, %s", armRegs[r0], armRegs[r3])
				d.clob(r0)
			}
		case h&0x0C00 == 0x0400:
			// Special data processing and branch and exchange on any register
			rd := int(h&7) | int(h>>4)&8
			rm := int(h>>3) & 15
			switch (h >> 8) & 3 {
			case 0:
				d.set("add"+d.c(), "%s, %s", armRegs[rd], armRegs[rm])
				if rd == armPC {
					d.in.Kind = Jump
				}
				d.clob(rd)
			case 1:
				d.set("cmp"+d.c(), "%s, %s", armRegs[rd], armRegs[rm])
			case 2:
				if h == 0x46C0 {
					d.set("nop"+d.c(), "")
					d.note("mov r8, r8")
					d.in.Asm = "mov" + d.c() + " r8, r8"
					return
				}
				d.set("mov"+d.c(), "%s, %s", armRegs[rd], armRegs[rm])
				if rd == armPC {
					d.in.Kind = Jump
					if rm == armLR {
						d.in.Kind = Return
					}
				}
				d.addImm(rd, rm, 0)
			case 3:
				ns := h&7 == 4
				if h&0x80 != 0 && h&7 != 0 && !ns {
					d.undef16(h)
					return
				}
				c := d.c()
				if ns {
					// bxns and blxns are not conditional
					c = "ns"
				} else {
					d.branchInIT()
				}
				if h&0x80 == 0 {
					d.set("bx"+c, armRegs[rm])
					d.in.Kind = Jump
					if rm == armLR {
						d.in.Kind = Return
					} else {
						d.eff(Effect{Kind: JumpReg, Src: rm, Dst: -1, Base: -1})
					}
				} else {
					d.set("blx"+c, armRegs[rm])
					d.in.Kind = Call
					d.eff(Effect{Kind: JumpReg, Src: rm, Dst: -1, Base: -1})
					d.call()
				}
			}
		default:
			// ldr rt, [pc, #imm]
			off := int64(imm8) * 4
			a := d.lit(off)
			d.set("ldr"+d.c(), "%s, [pc, #%d]", armRegs[r8], off)
			d.note("0x%x", a)
			d.loadLiteral(r8, a, 4)
		}
	case 5:
		// Load and store with a register offset
		op := (h >> 9) & 7
		mn := [8]string{"str", "strh", "strb", "ldrsb", "ldr", "ldrh", "ldrb", "ldrsh"}[op]
		d.set(mn+d.c(), "%s, [%s, %s]", armRegs[r0], armRegs[r3], armRegs[r6])
		if op >= 3 {
			d.clob(r0)
		}
	case 6, 7, 8:
		// Load and store with a 5-bit immediate offset
		size := 4
		mn := "str"
		switch {
		case h>>12 == 7:
			size, mn = 1, "strb"
		case h>>12 == 8:
			size, mn = 2, "strh"
		}
		load := h&0x800 != 0
		if load {
			mn = "ldr" + mn[3:]
		}
		off := int64(h>>6&31) * int64(size)
		d.set(mn+d.c(), "%s, [%s, %s]", armRegs[r0], armRegs[r3], d.imm(off))
		if load {
			d.load(r0, r3, off, size)
		} else {
			d.mem(r0, r3, off, size, true)
		}
	case 9:
		// ldr and str relative to sp
		off := int64(imm8) * 4
		if h&0x800 != 0 {
			d.set("ldr"+d.c(), "%s, [sp, %s]", armRegs[r8], d.imm(off))
			d.load(r8, armSP, off, 4)
		} else {
			d.set("str"+d.c(), "%s, [sp, %s]", armRegs[r8], d.imm(off))
			d.mem(r8, armSP, off, 4, true)
		}
	case 10:
		off := int64(imm8) * 4
		if h&0x800 == 0 {
			// adr
			a := d.lit(off)
			d.set("add"+d.c(), "%s, pc, #%d", armRegs[r8], off)
			d.note("adr %s, 0x%x", armRegs[r8], a)
			d.setConst(r8, a)
		} else {
			d.set("add"+d.c(), "%s, sp, %s", armRegs[r8], d.imm(off))
			d.addImm(r8, armSP, off)
		}
	case 11:
		d.thumb16Misc(h)
	case 12:
		// ldmia, stmia
		list := h & 0xFF
		if h&0x800 == 0 {
			d.set("stmia"+d.c(), "%s!, %s", armRegs[r8], regList(list))
			d.addImm(r8, r8, 4*int64(popCount16(list)))
			return
		}
		wb := ""
		if list&(1<<r8) == 0 {
			wb = "!"
		}
		d.set("ldmia"+d.c(), "%s%s, %s", armRegs[r8], wb, regList(list))
		d.clobList(list)
		if wb != "" {
			d.addImm(r8, r8, 4*int64(popCount16(list)))
		}
	case 13:
		cond := (h >> 8) & 15
		switch cond {
		case 14:
			d.set("udf"+d.c(), "%s", d.imm(int64(imm8)))
			d.in.Kind = Invalid
		case 15:
			d.set("svc"+d.c(), "%d", imm8)
			if imm8 > 32 {
				d.note("0x%x", imm8)
			}
			d.in.Kind = Interrupt
		default:
			t := uint32(int64(d.pc()) + int64(int8(imm8))*2)
			d.set("b"+armConds[cond]+".n", "0x%x", t)
			d.branch(Branch, t)
			d.unpredictableInIT()
		}
	case 14:
		if h&0x800 != 0 {
			d.undef16(h)
			return
		}
		off := int64(int16(h<<5)) >> 4
		t := uint32(int64(d.pc()) + off)
		d.set("b"+d.c()+".n", "0x%x", t)
		d.branch(Jump, t)
		d.branchInIT()
	}
}

// unpredictableInIT notes, as objdump does, an instruction that cannot be
// in an IT block
func (d *tdec) unpredictableInIT() {
	if d.cond >= 0 {
		d.note("unpredictable <IT:%s>", itCond(d.cond))
	}
}

func (d *tdec) thumb16Misc(h uint16) {
	r0 := int(h & 7)
	r3 := int(h>>3) & 7
	switch {
	case h&0x0F00 == 0x0000:
		// add sp, sp, #imm / sub sp, sp, #imm
		off := int64(h&0x7F) * 4
		if h&0x80 == 0 {
			d.set("add"+d.c(), "sp, %s", d.imm(off))
			d.addImm(armSP, armSP, off)
		} else {
			d.set("sub"+d.c(), "sp, %s", d.imm(off))
			d.addImm(armSP, armSP, -off)
		}
	case h&0x0500 == 0x0100:
		// cbz, cbnz
		off := uint32(h>>3)&31<<1 | uint32(h>>9)&1<<6
		t := d.pc() + off
		mn := "cbz"
		if h&0x800 != 0 {
			mn = "cbnz"
		}
		d.set(mn, "%s, 0x%x", armRegs[r0], t)
		d.branch(Branch, t)
		d.unpredictableInIT()
	case h&0x0F00 == 0x0200:
		mn := [4]string{"sxth", "sxtb", "uxth", "uxtb"}[(h>>6)&3]
		d.set(mn+d.c(), "%s, %s", armRegs[r0], armRegs[r3])
		d.clob(r0)
	case h&0x0E00 == 0x0400:
		// push
		list := h & 0xFF
		if h&0x100 != 0 {
			list |= 1 << armLR
		}
		d.set("push"+d.c(), regList(list))
		d.addImm(armSP, armSP, -4*int64(popCount16(list)))
	case h&0x0FF7 == 0x0610:
		d.set("setpan", "#%d", (h>>3)&1)
		d.unpredictableInIT()
	case h&0x0FF7 == 0x0650:
		d.set("setend", map[bool]string{false: "le", true: "be"}[h&8 != 0])
		d.unpredictableInIT()
	case h&0x0FE8 == 0x0660:
		// cps
		mn := "cpsie"
		if h&0x10 != 0 {
			mn = "cpsid"
		}
		f := ""
		for i, c := range "fia" {
			if h&(1<<i) != 0 {
				f = string(c) + f
			}
		}
		d.set(mn, f)
		d.unpredictableInIT()
	case h&0x0F00 == 0x0A00:
		op := (h >> 6) & 3
		if op == 2 {
			d.set("hlt", "0x%04x", h&0x3F)
			return
		}
		mn := [4]string{"rev", "rev16", "", "revsh"}[op]
		d.set(mn+d.c(), "%s, %s", armRegs[r0], armRegs[r3])
		d.clob(r0)
	case h&0x0E00 == 0x0C00:
		// pop
		list := h & 0xFF
		if h&0x100 != 0 {
			list |= 1 << armPC
			d.in.Kind = Return
		}
		d.set("pop"+d.c(), regList(list))
		d.clobList(list)
		d.addImm(armSP, armSP, 4*int64(popCount16(list)))
	case h&0x0F00 == 0x0E00:
		d.set("bkpt", "0x%04x", h&0xFF)
		d.in.Kind = Interrupt
	case h&0x0F00 == 0x0F00:
		d.itHint(h)
	default:
		d.undef16(h)
	}
}

// itHint is IT, or a hint (nop, yield, wfe, wfi, sev) when the mask is 0
func (d *tdec) itHint(h uint16) {
	mask := h & 15
	fc := (h >> 4) & 15
	if mask == 0 {
		op := (h >> 4) & 15
		if op < 6 {
			d.set([6]string{"nop", "yield", "wfe", "wfi", "sev", "sevl"}[op]+d.c(), "")
		} else {
			d.set("nop"+d.c(), "{%d}", op)
		}
		return
	}
	d.unpredictableInIT()
	mn := "it"
	d.itConds[0] = uint8(fc)
	d.itN = 1
	for k := 3; k >= 0 && mask&(1<<k-1) != 0; k-- {
		bit := (mask >> k) & 1
		if bit == fc&1 {
			mn += "t"
		} else {
			mn += "e"
		}
		d.itConds[d.itN] = uint8(fc&^1 | bit)
		d.itN++
	}
	d.set(mn, itCond(int(fc)))
}
