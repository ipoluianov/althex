package disasm

import (
	"encoding/binary"
	"fmt"
)

// avr decodes the 8-bit AVR instruction set (ATmega, ATtiny, XMEGA, the
// Arduino boards) as GNU avr-objdump writes it. Addresses are byte
// addresses in the flash; instructions are one or two little-endian words.
//
// All the instructions of all the cores are decoded at once, as objdump
// does: the code does not say which core it is for. The one encoding the
// cores do not agree on is 10100kkk dddd kkkk: ldd/std on the classic and
// XMEGA cores, a 16-bit lds/sts on the reduced core (ATtiny4..40, AVRrc).
// "avr" decodes it as ldd/std; "avrtiny" as lds/sts.
type avr struct {
	tiny bool
}

func init() {
	register(avr{})
	register(avr{tiny: true})
}

func (a avr) ID() string {
	if a.tiny {
		return "avrtiny"
	}
	return "avr"
}

func (a avr) Name() string {
	if a.tiny {
		return "AVR (8-bit, reduced core: ATtiny4..40)"
	}
	return "AVR (8-bit)"
}

func (a avr) MaxLen() int { return 4 }

// The pointer registers: the low register of the pair
const (
	avrX = 26
	avrY = 28
	avrZ = 30
)

// avrLong tells whether an instruction word takes a second word
// (lds, sts, jmp, call)
func avrLong(w uint16) bool {
	return w&0xFC0F == 0x9000 || w&0xFE0C == 0x940C
}

// The flags of brbs/brbc and bset/bclr, by bit
var (
	avrBrbs = [8]string{"brcs", "breq", "brmi", "brvs", "brlt", "brhs", "brts", "brie"}
	avrBrbc = [8]string{"brcc", "brne", "brpl", "brvc", "brge", "brhc", "brtc", "brid"}
	avrBset = [8]string{"sec", "sez", "sen", "sev", "ses", "seh", "set", "sei"}
	avrBclr = [8]string{"clc", "clz", "cln", "clv", "cls", "clh", "clt", "cli"}
)

// The two-register ALU instructions, 0000 01..0010 11 and 1001 11 by the
// top 6 bits
var avrALU = map[uint16]string{
	0x01: "cpc", 0x02: "sbc", 0x03: "add", 0x04: "cpse", 0x05: "cp", 0x06: "sub", 0x07: "adc",
	0x08: "and", 0x09: "eor", 0x0A: "or", 0x0B: "mov", 0x27: "mul",
}

// The immediate instructions by the top 4 bits
var avrImm = map[uint16]string{
	0x3: "cpi", 0x4: "sbci", 0x5: "subi", 0x6: "ori", 0x7: "andi", 0xE: "ldi",
}

// The one-register instructions 1001 010d dddd xxxx by the low 4 bits
var avrOne = map[uint16]string{
	0x0: "com", 0x1: "neg", 0x2: "swap", 0x3: "inc", 0x5: "asr", 0x6: "lsr", 0x7: "ror", 0xA: "dec",
}

// The instructions without operands
var avrNoArgs = map[uint16]string{
	0x0000: "nop", 0x9508: "ret", 0x9518: "reti", 0x9588: "sleep", 0x9598: "break", 0x95A8: "wdr",
	0x95C8: "lpm", 0x95D8: "elpm", 0x95E8: "spm", 0x9409: "ijmp", 0x9419: "eijmp", 0x9509: "icall", 0x9519: "eicall",
}

func (a avr) Decode(code []byte, addr uint64, mode int) Inst {
	if len(code) < 2 {
		if len(code) == 0 {
			return Inst{}
		}
		return Inst{Addr: addr, Len: 1, Mnemonic: ".byte", Operands: fmt.Sprintf("0x%02x", code[0]), Kind: Invalid}
	}
	w := binary.LittleEndian.Uint16(code)
	in := Inst{Addr: addr, Len: 2}
	if avrLong(w) {
		if len(code) < 4 {
			return avrWord(in, w)
		}
		in.Len = 4
	}
	if !a.decode(&in, w, code) {
		return avrWord(in, w)
	}
	return in
}

// avrWord is a word that is not an instruction
func avrWord(in Inst, w uint16) Inst {
	in.Len = 2
	in.Mnemonic, in.Operands, in.Comment, in.Kind = ".word", fmt.Sprintf("0x%04x", w), "????", Invalid
	return in
}

// avrSet writes the text of an instruction
func avrSet(in *Inst, mn, format string, args ...any) {
	in.Mnemonic = mn
	in.Operands = fmt.Sprintf(format, args...)
}

// avrEffect adds an effect
func avrEffect(in *Inst, e Effect) { in.Effects = append(in.Effects, e) }

// avrClobber adds Clobber effects for registers
func avrClobber(in *Inst, regs ...int) {
	for _, r := range regs {
		in.Effects = append(in.Effects, Effect{Kind: Clobber, Dst: r, Src: -1, Base: -1})
	}
}

// avrMem is a memory access in the data space
func avrMem(base int, imm int64, addr uint64, store bool, reg int) Effect {
	e := Effect{Kind: MemAccess, Dst: -1, Src: -1, Base: base, Imm: imm, Addr: addr, Size: 1, Store: store}
	if store {
		e.Src = reg
	} else {
		e.Dst = reg
	}
	return e
}

// avrTarget sets the target of a jump, a branch or a call
func avrTarget(in *Inst, k Kind, t int64) {
	if t < 0 {
		// The program counter wraps around the flash
		t &= 0x7FFFFF
	}
	in.Kind, in.Target, in.HasTarget = k, uint64(t), true
}

func (a avr) decode(in *Inst, w uint16, code []byte) bool {
	d := int(w>>4) & 0x1F
	r := int(w&0xF) | int(w>>5)&0x10
	addr := in.Addr

	if mn, ok := avrNoArgs[w]; ok {
		in.Mnemonic = mn
		switch mn {
		case "ret", "reti":
			in.Kind = Return
		case "ijmp", "eijmp":
			in.Kind = Jump
			avrEffect(in, Effect{Kind: JumpReg, Src: 30, Dst: -1, Base: -1})
		case "icall", "eicall":
			in.Kind = Call
			avrEffect(in, Effect{Kind: JumpReg, Src: 30, Dst: -1, Base: -1})
		case "lpm", "elpm":
			avrClobber(in, 0)
		}
		return true
	}

	switch {
	case w>>10 == 0 && w>>8 != 0: // 0000 00xx: movw, muls, mulsu, fmul*
		switch w >> 8 {
		case 0x01:
			dd, rr := int(w>>4&0xF)*2, int(w&0xF)*2
			avrSet(in, "movw", "r%d, r%d", dd, rr)
			avrEffect(in, Effect{Kind: AddImm, Dst: dd, Src: rr, Base: -1})
			avrEffect(in, Effect{Kind: AddImm, Dst: dd + 1, Src: rr + 1, Base: -1})
		case 0x02:
			avrSet(in, "muls", "r%d, r%d", 16+int(w>>4&0xF), 16+int(w&0xF))
			avrClobber(in, 0, 1)
		default:
			names := [4]string{"mulsu", "fmul", "fmuls", "fmulsu"}
			avrSet(in, names[(w>>6&2)|(w>>3&1)], "r%d, r%d", 16+int(w>>4&7), 16+int(w&7))
			avrClobber(in, 0, 1)
		}
		return true

	case w>>12 <= 2 || w>>10 == 0x27: // two registers
		mn := avrALU[w>>10]
		if mn == "" {
			return false
		}
		avrSet(in, mn, "r%d, r%d", d, r)
		switch mn {
		case "cp", "cpc":
		case "cpse":
			a.skip(in, code)
		case "mul":
			avrClobber(in, 0, 1)
		case "mov":
			avrEffect(in, Effect{Kind: AddImm, Dst: d, Src: r, Base: -1})
		case "eor", "sub":
			if d == r { // clr: the register is zero
				avrEffect(in, Effect{Kind: SetConst, Dst: d, Src: -1, Base: -1})
			} else {
				avrClobber(in, d)
			}
		default:
			avrClobber(in, d)
		}
		return true

	case avrImm[w>>12] != "": // a register r16..r31 and a byte
		mn := avrImm[w>>12]
		k := int(w>>4&0xF0) | int(w&0xF)
		d = 16 + int(w>>4&0xF)
		avrSet(in, mn, "r%d, 0x%02X", d, k)
		in.Comment = fmt.Sprint(k)
		switch mn {
		case "ldi":
			avrEffect(in, Effect{Kind: SetConst, Dst: d, Src: -1, Base: -1, Imm: int64(k)})
		case "cpi":
		default:
			avrClobber(in, d)
		}
		return true

	case w>>12 == 0x8 || w>>12 == 0xA: // ldd, std (or the lds/sts of the reduced core)
		if a.tiny && w>>12 == 0xA {
			k := int(w&0xF) | int(w>>5&0x30) | int(w>>2&0x40) | int(^w>>1&0x80)
			d = 16 + int(w>>4&0xF)
			store := w&0x0800 != 0
			if store {
				avrSet(in, "sts", "0x%02x, r%d", k, d)
			} else {
				avrSet(in, "lds", "r%d, 0x%02x", d, k)
			}
			in.Comment = fmt.Sprintf("0x%x", 0x800000+k)
			avrEffect(in, avrMem(-1, 0, uint64(k), store, d))
			if !store {
				avrClobber(in, d)
			}
			return true
		}
		q := int(w&7) | int(w>>7&0x18) | int(w>>8&0x20)
		ptr, base := "Z", avrZ
		if w&8 != 0 {
			ptr, base = "Y", avrY
		}
		store := w&0x0200 != 0
		switch {
		case q == 0 && store:
			avrSet(in, "st", "%s, r%d", ptr, d)
		case q == 0:
			avrSet(in, "ld", "r%d, %s", d, ptr)
		case store:
			avrSet(in, "std", "%s+%d, r%d", ptr, q, d)
			in.Comment = fmt.Sprintf("0x%02x", q)
		default:
			avrSet(in, "ldd", "r%d, %s+%d", d, ptr, q)
			in.Comment = fmt.Sprintf("0x%02x", q)
		}
		avrEffect(in, avrMem(base, int64(q), 0, store, d))
		if !store {
			avrClobber(in, d)
		}
		return true

	case w>>10 == 0x24: // 1001 00xd dddd xxxx: loads, stores, push, pop
		return a.decodeMem(in, w, code, d)

	case w>>9 == 0x4A: // 1001 010d dddd xxxx
		return a.decode94(in, w, code, d)

	case w>>9 == 0x4B: // adiw, sbiw
		dd := 24 + int(w>>3&6)
		k := int(w&0xF) | int(w>>2&0x30)
		mn := "adiw"
		if w&0x100 != 0 {
			mn = "sbiw"
		}
		avrSet(in, mn, "r%d, 0x%02x", dd, k)
		in.Comment = fmt.Sprint(k)
		avrClobber(in, dd, dd+1)
		return true

	case w>>10 == 0x26: // cbi, sbic, sbi, sbis
		io := int(w>>3) & 0x1F
		mn := [4]string{"cbi", "sbic", "sbi", "sbis"}[w>>8&3]
		avrSet(in, mn, "0x%02x, %d", io, w&7)
		in.Comment = fmt.Sprint(io)
		if mn == "cbi" || mn == "sbi" {
			// The bit is not in the effect
			in.Comment += fmt.Sprintf(", bit %d", w&7)
		}
		avrEffect(in, avrMem(-1, 0, uint64(io+0x20), mn == "cbi" || mn == "sbi", -1))
		if mn == "sbic" || mn == "sbis" {
			a.skip(in, code)
		}
		return true

	case w>>12 == 0xB: // in, out
		io := int(w&0xF) | int(w>>5&0x30)
		store := w&0x0800 != 0
		if store {
			avrSet(in, "out", "0x%02x, r%d", io, d)
		} else {
			avrSet(in, "in", "r%d, 0x%02x", d, io)
		}
		in.Comment = fmt.Sprint(io)
		avrEffect(in, avrMem(-1, 0, uint64(io+0x20), store, d))
		if !store {
			avrClobber(in, d)
		}
		return true

	case w>>12 == 0xC || w>>12 == 0xD: // rjmp, rcall
		k := int64(w&0xFFF) << 52 >> 52 * 2
		mn, kind := "rjmp", Jump
		if w>>12 == 0xD {
			mn, kind = "rcall", Call
		}
		avrSet(in, mn, ".%+d", k)
		avrTarget(in, kind, int64(addr)+2+k)
		in.Comment = fmt.Sprintf("0x%x", in.Target)
		return true

	case w>>11 == 0x1E: // brbs, brbc
		k := int64(w>>3&0x7F) << 57 >> 57 * 2
		mn := avrBrbs[w&7]
		if w&0x0400 != 0 {
			mn = avrBrbc[w&7]
		}
		avrSet(in, mn, ".%+d", k)
		avrTarget(in, Branch, int64(addr)+2+k)
		in.Comment = fmt.Sprintf("0x%x", in.Target)
		return true

	case w>>11 == 0x1F: // bld, bst, sbrc, sbrs
		if w&8 != 0 {
			return false
		}
		mn := [4]string{"bld", "bst", "sbrc", "sbrs"}[w>>9&3]
		avrSet(in, mn, "r%d, %d", d, w&7)
		switch mn {
		case "bld":
			avrClobber(in, d)
		case "sbrc", "sbrs":
			a.skip(in, code)
		}
		return true
	}
	return false
}

// decodeMem decodes 1001 00xd dddd xxxx
func (a avr) decodeMem(in *Inst, w uint16, code []byte, d int) bool {
	store := w&0x0200 != 0
	op := w & 0xF
	switch op {
	case 0x0: // lds, sts
		k := uint64(binary.LittleEndian.Uint16(code[2:]))
		if store {
			avrSet(in, "sts", "0x%04X, r%d", k, d)
		} else {
			avrSet(in, "lds", "r%d, 0x%04X", d, k)
		}
		in.Comment = fmt.Sprintf("0x%x", 0x800000+k)
		avrEffect(in, avrMem(-1, 0, k, store, d))
		if !store {
			avrClobber(in, d)
		}
		return true
	case 0xF:
		if store {
			avrSet(in, "push", "r%d", d)
		} else {
			avrSet(in, "pop", "r%d", d)
			avrClobber(in, d)
		}
		return true
	case 0x4, 0x5, 0x6, 0x7:
		if store { // xch, las, lac, lat: Z and a register both ways
			avrSet(in, [4]string{"xch", "las", "lac", "lat"}[op-4], "Z, r%d", d)
			avrEffect(in, avrMem(avrZ, 0, 0, false, d))
			avrEffect(in, avrMem(avrZ, 0, 0, true, d))
			avrClobber(in, d)
			return true
		}
		mn := "lpm"
		if op >= 6 {
			mn = "elpm"
		}
		ptr := "Z"
		if op&1 != 0 {
			ptr = "Z+"
		}
		avrSet(in, mn, "r%d, %s", d, ptr)
		// Program memory: not an access to the data space
		avrClobber(in, d)
		if op&1 != 0 {
			if d >= avrZ {
				in.Comment = "undefined"
			}
			avrClobber(in, avrZ, avrZ+1)
		}
		return true
	}
	var ptr string
	var base, inc int
	switch op {
	case 0x1:
		ptr, base, inc = "Z+", avrZ, 1
	case 0x2:
		ptr, base, inc = "-Z", avrZ, -1
	case 0x9:
		ptr, base, inc = "Y+", avrY, 1
	case 0xA:
		ptr, base, inc = "-Y", avrY, -1
	case 0xC:
		ptr, base = "X", avrX
	case 0xD:
		ptr, base, inc = "X+", avrX, 1
	case 0xE:
		ptr, base, inc = "-X", avrX, -1
	default:
		return false
	}
	if store {
		avrSet(in, "st", "%s, r%d", ptr, d)
	} else {
		avrSet(in, "ld", "r%d, %s", d, ptr)
	}
	// A pre-decrement accesses the pointer less one
	imm := int64(0)
	if inc < 0 {
		imm = -1
	}
	avrEffect(in, avrMem(base, imm, 0, store, d))
	if !store {
		avrClobber(in, d)
	}
	if inc != 0 {
		// The register is also the pointer that changes
		if d == base || d == base+1 {
			in.Comment = "undefined"
		}
		avrClobber(in, base, base+1)
	}
	return true
}

// decode94 decodes 1001 010d dddd xxxx
func (a avr) decode94(in *Inst, w uint16, code []byte, d int) bool {
	op := w & 0xF
	if mn, ok := avrOne[op]; ok {
		avrSet(in, mn, "r%d", d)
		avrClobber(in, d)
		return true
	}
	switch op {
	case 0x8: // bset, bclr (spm and the others are in avrNoArgs)
		if w&0x0100 != 0 {
			if w == 0x95F8 {
				avrSet(in, "spm", "Z+")
				avrClobber(in, avrZ, avrZ+1)
				return true
			}
			return false
		}
		s := w >> 4 & 7
		if w&0x80 != 0 {
			in.Mnemonic = avrBclr[s]
		} else {
			in.Mnemonic = avrBset[s]
		}
		return true
	case 0xB:
		if w&0x0100 != 0 {
			return false
		}
		avrSet(in, "des", "%d", w>>4&0xF)
		return true
	case 0xC, 0xD, 0xE, 0xF: // jmp, call
		k := (uint64(w>>3&0x3E|w&1)<<16 | uint64(binary.LittleEndian.Uint16(code[2:]))) * 2
		mn, kind := "jmp", Jump
		if op >= 0xE {
			mn, kind = "call", Call
		}
		if k == 0 {
			avrSet(in, mn, "0")
		} else {
			avrSet(in, mn, "0x%x", k)
		}
		avrTarget(in, kind, int64(k))
		in.Comment = fmt.Sprintf("0x%x", k)
		return true
	}
	return false
}

// skip makes a skip instruction a branch to after the next instruction
func (a avr) skip(in *Inst, code []byte) {
	n := int64(2)
	if len(code) >= 4 && avrLong(binary.LittleEndian.Uint16(code[2:])) {
		n = 4
	}
	avrTarget(in, Branch, int64(in.Addr)+2+n)
}
