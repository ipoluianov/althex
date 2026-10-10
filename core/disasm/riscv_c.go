package disasm

import "fmt"

// compressed decodes a 16-bit instruction (the C extension, with F and D on
// RV32 and D on RV64); false if it is not one. objdump writes the forms
// they stand for ("addi a0,a0,1") and the hints by the names of C
// ("c.nop 1", "c.slli64 a0").
func (r riscvInst) compressed(h uint16) bool {
	w := uint32(h)
	f3 := w >> 13
	rd := w >> 7 & 31                         // rd/rs1 of the full register set
	rs2 := w >> 2 & 31                        // rs2 of the full register set
	rdp := 8 + w>>2&7                         // rd' / rs2' of the 3-bit forms
	rs1p := 8 + w>>7&7                        // rs1' of the 3-bit forms
	imm6 := riscvSext(w>>7&0x20|w>>2&0x1F, 6) // the CI immediate
	uimm6 := w>>7&0x20 | w>>2&0x1F
	rv64 := r.xlen == 64
	// The offsets of the loads and stores, scaled by 4 and by 8
	offW := int64(w>>7&0x38 | w<<1&0x40 | w>>4&4)
	offD := int64(w>>7&0x38 | w<<1&0xC0)
	// The offsets from sp: CI loads and CSS stores
	spW := int64(w>>7&0x20 | w>>2&0x1C | w<<4&0xC0)
	spD := int64(w>>7&0x20 | w>>2&0x18 | w<<4&0x1C0)
	ssW := int64(w>>7&0x3C | w>>1&0xC0)
	ssD := int64(w>>7&0x38 | w>>1&0x1C0)

	switch w&3<<3 | f3 {
	// Quadrant 0
	case 0<<3 | 0: // c.addi4spn
		imm := int64(w>>7&0x30 | w>>1&0x3C0 | w>>4&4 | w>>2&8)
		if w == 0 {
			r.set("unimp")
			r.Kind = Invalid
			return true
		}
		if imm == 0 {
			return false
		}
		r.set("addi", r.x(rdp), "sp", fmt.Sprint(imm))
		r.addImm(rdp, riscvSP, imm)
	case 0<<3 | 1: // c.fld
		r.fmem("fld", 8, rdp, rs1p, offD, false)
	case 0<<3 | 2: // c.lw
		r.load("lw", 4, rdp, rs1p, offW)
	case 0<<3 | 3: // c.flw, c.ld
		if rv64 {
			r.load("ld", 8, rdp, rs1p, offD)
		} else {
			r.fmem("flw", 4, rdp, rs1p, offW, false)
		}
	case 0<<3 | 5: // c.fsd
		r.fmem("fsd", 8, rdp, rs1p, offD, true)
	case 0<<3 | 6: // c.sw
		r.store("sw", 4, rdp, rs1p, offW)
	case 0<<3 | 7: // c.fsw, c.sd
		if rv64 {
			r.store("sd", 8, rdp, rs1p, offD)
		} else {
			r.fmem("fsw", 4, rdp, rs1p, offW, true)
		}

	// Quadrant 1
	case 1<<3 | 0: // c.addi, c.nop
		switch {
		case rd == 0 && imm6 == 0:
			r.set("nop")
		case rd == 0:
			r.set("c.nop", fmt.Sprint(imm6))
		default:
			r.set("addi", r.x(rd), r.x(rd), fmt.Sprint(imm6))
			r.addImm(rd, rd, imm6)
		}
	case 1<<3 | 1: // c.jal, c.addiw
		if !rv64 {
			r.set("jal", r.target(Call, r.cjOffset(w)))
			r.clobber(riscvRA)
			break
		}
		if rd == 0 {
			return false
		}
		if imm6 == 0 {
			r.set("sext.w", r.x(rd), r.x(rd))
		} else {
			r.set("addiw", r.x(rd), r.x(rd), fmt.Sprint(imm6))
		}
		r.clobber(rd)
	case 1<<3 | 2: // c.li
		if rd == 0 {
			r.set("c.li", r.x(rd), fmt.Sprint(imm6))
			break
		}
		r.set("li", r.x(rd), fmt.Sprint(imm6))
		r.setConst(rd, imm6)
	case 1<<3 | 3: // c.addi16sp, c.lui
		if rd == riscvSP {
			imm := riscvSext(w>>3&0x200|w>>2&0x10|w<<1&0x40|w<<4&0x180|w<<3&0x20, 10)
			// The immediate 0 is reserved; objdump shows it as an addi
			r.set("addi", "sp", "sp", fmt.Sprint(imm))
			r.addImm(riscvSP, riscvSP, imm)
			break
		}
		if imm6 == 0 {
			return false
		}
		mn := "lui"
		if rd == 0 {
			mn = "c.lui"
		}
		r.set(mn, r.x(rd), fmt.Sprintf("0x%x", uint32(imm6)&0xFFFFF))
		r.setConst(rd, imm6<<12)
	case 1<<3 | 4: // c.srli, c.srai, c.andi, c.sub...
		return r.cALU(w, rs1p, rdp, imm6, uimm6)
	case 1<<3 | 5: // c.j
		r.set("j", r.target(Jump, r.cjOffset(w)))
	case 1<<3 | 6, 1<<3 | 7: // c.beqz, c.bnez
		off := riscvSext(w>>4&0x100|w<<1&0xC0|w<<3&0x20|w>>7&0x18|w>>2&6, 9)
		r.set([2]string{"beqz", "bnez"}[f3-6], r.x(rs1p), r.target(Branch, off))

	// Quadrant 2
	case 2<<3 | 0: // c.slli
		if !rv64 && uimm6&0x20 != 0 {
			return false
		}
		if uimm6 == 0 {
			r.set("c.slli64", r.x(rd))
			break
		}
		if rd == 0 {
			r.set("c.slli", r.x(rd), fmt.Sprintf("0x%x", uimm6))
			break
		}
		r.set("slli", r.x(rd), r.x(rd), fmt.Sprintf("0x%x", uimm6))
		r.clobber(rd)
	case 2<<3 | 1: // c.fldsp
		r.fmem("fld", 8, rd, riscvSP, spD, false)
	case 2<<3 | 2: // c.lwsp
		if rd == 0 {
			return false
		}
		r.load("lw", 4, rd, riscvSP, spW)
	case 2<<3 | 3: // c.flwsp, c.ldsp
		if !rv64 {
			r.fmem("flw", 4, rd, riscvSP, spW, false)
			break
		}
		if rd == 0 {
			return false
		}
		r.load("ld", 8, rd, riscvSP, spD)
	case 2<<3 | 4: // c.jr, c.mv, c.ebreak, c.jalr, c.add
		bit12 := w>>12&1 != 0
		switch {
		case !bit12 && rs2 == 0:
			if rd == 0 {
				return false
			}
			r.jalr(0, rd, 0)
		case !bit12 && rd == 0:
			r.set("c.mv", r.x(rd), r.x(rs2))
		case !bit12:
			r.set("mv", r.x(rd), r.x(rs2))
			r.addImm(rd, rs2, 0)
		case rd == 0 && rs2 == 0:
			r.set("ebreak")
			r.Kind = Interrupt
		case rs2 == 0:
			r.jalr(riscvRA, rd, 0)
		case rd == 0:
			r.set("c.add", r.x(rd), r.x(rs2))
		default:
			r.set("add", r.x(rd), r.x(rd), r.x(rs2))
			r.clobber(rd)
		}
	case 2<<3 | 5: // c.fsdsp
		r.fmem("fsd", 8, rs2, riscvSP, ssD, true)
	case 2<<3 | 6: // c.swsp
		r.store("sw", 4, rs2, riscvSP, ssW)
	case 2<<3 | 7: // c.fswsp, c.sdsp
		if rv64 {
			r.store("sd", 8, rs2, riscvSP, ssD)
		} else {
			r.fmem("fsw", 4, rs2, riscvSP, ssW, true)
		}
	default:
		return false
	}
	return true
}

// cjOffset is the offset of c.j and c.jal
func (r riscvInst) cjOffset(w uint32) int64 {
	return riscvSext(w>>1&0x800|w>>7&0x10|w>>1&0x300|w<<2&0x400|w>>1&0x40|w<<1&0x80|w>>2&0xE|w<<3&0x20, 12)
}

// cALU decodes c.srli, c.srai, c.andi, c.sub, c.xor, c.or, c.and, c.subw,
// c.addw
func (r riscvInst) cALU(w, rd, rs2 uint32, imm6 int64, uimm6 uint32) bool {
	switch w >> 10 & 3 {
	case 0, 1: // c.srli, c.srai
		if r.xlen == 32 && uimm6&0x20 != 0 {
			return false
		}
		mn := [2]string{"srli", "srai"}[w>>10&1]
		if uimm6 == 0 {
			r.set("c."+mn+"64", r.x(rd))
		} else {
			r.set(mn, r.x(rd), r.x(rd), fmt.Sprintf("0x%x", uimm6))
		}
	case 2:
		r.set("andi", r.x(rd), r.x(rd), fmt.Sprint(imm6))
	default:
		mn := [8]string{"sub", "xor", "or", "and", "subw", "addw"}[w>>10&4|w>>5&3]
		if mn == "" || mn[len(mn)-1] == 'w' && r.xlen == 32 {
			return false
		}
		r.set(mn, r.x(rd), r.x(rd), r.x(rs2))
	}
	r.clobber(rd)
	return true
}
