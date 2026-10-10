package fw

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/ipoluianov/althex/core/disasm"
)

// Guess is a guess of what the firmware is (FW-3)
type Guess struct {
	Arch    string   // disasm ID
	Base    uint64   // the load address of the start of a raw binary (or of the segment)
	Entry   uint64   // the reset handler
	Vectors []Vector // the vector table if found
	Score   float64  // 0..1, how sure
	Reason  string   // "Cortex-M vector table: SP 0x20005000, reset 0x08000135"
	Family  string   // "STM32", "RP2040", "ATmega328P"... when it can be told
	// Mode is the instruction set of the entry (disasm.ModeThumb for
	// Cortex-M), 0 - the default
	Mode int
}

// Vector is an entry of a vector table
type Vector struct {
	Index int
	Name  string
	Addr  uint64 // the handler, the Thumb bit cleared (the value for the initial SP)
	Off   int64  // of the entry in the segment (in the file for a raw binary)
}

// detectMax is how much of a segment the detection reads; statsMax is
// how much of it the instruction statistics decode
const detectMax = 64 << 10

var statsMax = 32 << 10

// Detect guesses the architecture, the load address and the vector table
// of the image: the best guess first. A format that tells the
// architecture (ELF, ESP images, UF2 families) gives a guess too.
func Detect(im *Image) []Guess {
	var gs []Guess
	lo, _ := im.Bounds()
	if im.Arch != "" {
		g := Guess{Arch: im.Arch, Base: lo, Entry: im.Entry, Score: 0.9, Family: im.Family,
			Reason: im.Format + " tells the architecture"}
		if im.kind == kindESP32 || im.kind == kindESP8266 {
			g.Score = 0.95
			g.Reason = fmt.Sprintf("%s: entry 0x%08X", im.Format, im.Entry)
		}
		gs = append(gs, g)
	}
	if im.kind == kindELF && im.Arch == "" {
		return nil // the ELF tells a machine without a disassembler: no guessing
	}
	for i := range im.Segments {
		if i >= 16 {
			break
		}
		s := &im.Segments[i]
		data := readSeg(s, detectMax)
		if len(data) < 8 {
			continue
		}
		gs = append(gs, detectCortexM(im, s, data)...)
		gs = append(gs, detectAVR(im, s, data)...)
		gs = append(gs, detectRISCV(im, s, data)...)
		gs = append(gs, detectX86Boot(im, s, data)...)
		if im.Raw() {
			gs = append(gs, detectESPDump(s)...)
		}
	}
	if s := mainSegment(im); s != nil {
		gs = append(gs, detectStats(im, s, readSeg(s, detectMax))...)
	}
	return mergeGuesses(gs)
}

// mainSegment is the largest executable segment
func mainSegment(im *Image) *Segment {
	var best *Segment
	for i := range im.Segments {
		s := &im.Segments[i]
		if s.Exec && (best == nil || s.Size > best.Size) {
			best = s
		}
	}
	return best
}

// mergeGuesses keeps one guess of an architecture at a base, the best
// first; the one with a vector table gives the details
func mergeGuesses(gs []Guess) []Guess {
	var out []Guess
	for _, g := range gs {
		dup := -1
		for i, o := range out {
			if o.Arch == g.Arch && o.Base == g.Base {
				dup = i
				break
			}
		}
		if dup < 0 {
			out = append(out, g)
			continue
		}
		o := &out[dup]
		score := max(o.Score, g.Score)
		if len(g.Vectors) > len(o.Vectors) || (len(o.Vectors) == 0 && g.Score > o.Score) {
			fam := o.Family
			*o = g
			if o.Family == "" {
				o.Family = fam
			}
		} else if o.Family == "" {
			o.Family = g.Family
		}
		// Two ways to the same answer make it surer
		o.Score = min(1, score+0.05)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

// ---- Cortex-M ----

var cortexNames = []string{
	"Initial SP", "Reset", "NMI", "HardFault", "MemManage", "BusFault", "UsageFault",
	"SecureFault", "Reserved", "Reserved", "Reserved", "SVCall", "DebugMonitor",
	"Reserved", "PendSV", "SysTick",
}

// CortexVectorName is the name of an entry of a Cortex-M vector table
func CortexVectorName(i int) string {
	if i < len(cortexNames) {
		return cortexNames[i]
	}
	return fmt.Sprintf("IRQ%d", i-16)
}

// ramSP tells whether an initial stack pointer is in a RAM of a Cortex-M
func ramSP(sp uint32) bool {
	if sp&3 != 0 {
		return false
	}
	return sp > 0x1FFE0000 && sp <= 0x20800000 || // SRAM (Kinetis SRAM_L below)
		sp > 0x10000000 && sp <= 0x10100000 || // LPC local SRAM
		sp > 0x24000000 && sp <= 0x24100000 || // STM32H7 AXI SRAM
		sp > 0x30000000 && sp <= 0x30100000 // STM32H7 D2 SRAM
}

// cortexBases are load addresses of Cortex-M flash, preferred when they fit
var cortexBases = []uint64{0x08000000, 0x00000000, 0x10000000, 0x00080000, 0x00400000, 0x1A000000, 0x00002000}

// cortexSystem are the entries of the system handlers that must be
// handlers or zero; 7..10 and 13 are reserved (vector 7 is the checksum
// of LPC parts, SecureFault of ARMv8-M)
var cortexSystem = []int{2, 3, 4, 5, 6, 11, 12, 14, 15}

func detectCortexM(im *Image, s *Segment, data []byte) []Guess {
	var out []Guess
	for _, tab := range []int{0, 0x100} {
		if len(data) < tab+16*4 {
			continue
		}
		w := func(i int) uint32 { return le32(data[tab+4*i:]) }
		sp, reset := w(0), w(1)
		if !ramSP(sp) || reset&1 == 0 {
			continue
		}
		hs := []uint64{uint64(reset &^ 1)}
		for _, i := range cortexSystem {
			if v := w(i); v != 0 && v&1 == 1 {
				hs = append(hs, uint64(v&^1))
			}
		}
		minH, maxH := hs[0], hs[0]
		for _, h := range hs {
			minH, maxH = min(minH, h), max(maxH, h)
		}
		size := uint64(s.Size)
		var base uint64
		var inside func(uint64) bool
		if im.Raw() {
			// The handlers are after the table and inside the image
			if maxH-minH >= size {
				// some point outside: take the reset handler alone
				minH, maxH = hs[0], hs[0]
			}
			lo := uint64(0)
			if maxH+1 > size {
				lo = maxH + 1 - size
			}
			minOff := uint64(tab + 16*4)
			if minH < minOff {
				continue
			}
			base = pickBase(lo, minH-minOff)
			if base > minH-minOff || base < lo {
				continue
			}
			inside = func(a uint64) bool { return a >= base && a < base+size }
		} else {
			base = s.Addr
			inside = im.Contains
		}
		if !inside(uint64(reset &^ 1)) {
			continue
		}
		good := 0
		for _, i := range cortexSystem {
			v := w(i)
			if v == 0 || v&1 == 1 && inside(uint64(v&^1)) {
				good++
			}
		}
		frac := float64(good) / float64(len(cortexSystem))
		if frac < 0.65 {
			continue
		}
		score := 0.6 + 0.3*frac
		reservedZero := w(8) == 0 && w(9) == 0 && w(10) == 0
		if reservedZero {
			score += 0.05
		}
		tabAddr := base + uint64(tab)
		// The table: the system entries, then IRQs while they look like
		// handlers, not into the code
		var vs []Vector
		limit := (len(data) - tab) / 4
		if minH > tabAddr && (minH-tabAddr)/4 < uint64(limit) {
			limit = int((minH - tabAddr) / 4)
		}
		limit = min(limit, 16+496)
		for i := 0; i < limit; i++ {
			v := w(i)
			if i >= 16 && !(v == 0 || v&1 == 1 && inside(uint64(v&^1))) {
				break
			}
			a := uint64(v)
			if i > 0 {
				a &^= 1
			}
			vs = append(vs, Vector{Index: i, Name: CortexVectorName(i), Addr: a, Off: int64(tab + 4*i)})
		}
		// Trailing zeros are not IRQs (unless the table goes on)
		for len(vs) > 16 && vs[len(vs)-1].Addr == 0 {
			vs = vs[:len(vs)-1]
		}
		fam := cortexFamily(base, tab, sp, data)
		if fam == "RP2040" {
			score += 0.05
		}
		reason := fmt.Sprintf("Cortex-M vector table at 0x%X: SP 0x%08X, reset 0x%08X, %d of %d system handlers valid, %d vectors",
			tabAddr, sp, reset, good, len(cortexSystem), len(vs))
		if im.Raw() {
			reason += fmt.Sprintf("; loaded at 0x%X", base)
		}
		out = append(out, Guess{Arch: "arm", Base: base, Entry: uint64(reset &^ 1), Vectors: vs,
			Score: min(score, 0.99), Reason: reason, Family: fam, Mode: disasm.ModeThumb})
	}
	return out
}

// pickBase chooses a load address in [lo, hi]: a known one, or the one
// with the most trailing zero bits
func pickBase(lo, hi uint64) uint64 {
	if lo > hi {
		return hi + 1 // none
	}
	for _, b := range cortexBases {
		if b >= lo && b <= hi {
			return b
		}
	}
	if lo == 0 {
		return 0
	}
	for sh := 63; sh >= 2; sh-- {
		a := uint64(1) << sh
		v := (lo + a - 1) &^ (a - 1)
		if v >= lo && v <= hi {
			return v
		}
	}
	return lo
}

// cortexFamily tells the family by the load address
func cortexFamily(base uint64, tab int, sp uint32, data []byte) string {
	switch {
	case base == 0x10000000 && tab == 0x100 && rp2040Boot2OK(data):
		return "RP2040"
	case base == 0x08000000:
		return "STM32"
	case base == 0x00080000:
		return "SAM3X"
	case base == 0x00400000:
		return "SAM4/SAME70"
	case base == 0x1A000000:
		return "LPC43xx"
	case base == 0 && sp > 0x10000000 && sp <= 0x10100000:
		return "LPC"
	}
	return ""
}

// crcMPEG2 is the CRC-32 of the RP2040 boot2: polynomial 0x04C11DB7, not
// reflected, no final inversion
var crcMPEG2Table = func() (t [256]uint32) {
	for i := range t {
		c := uint32(i) << 24
		for k := 0; k < 8; k++ {
			if c&0x80000000 != 0 {
				c = c<<1 ^ 0x04C11DB7
			} else {
				c <<= 1
			}
		}
		t[i] = c
	}
	return
}()

func crcMPEG2(b []byte) uint32 {
	c := uint32(0xFFFFFFFF)
	for _, x := range b {
		c = c<<8 ^ crcMPEG2Table[byte(c>>24)^x]
	}
	return c
}

// rp2040Boot2OK checks the CRC at the end of the 256-byte second stage
// bootloader of the RP2040
func rp2040Boot2OK(data []byte) bool {
	return len(data) >= 256 && crcMPEG2(data[:252]) == le32(data[252:])
}

// ---- AVR ----

var atmega328Names = []string{
	"RESET", "INT0", "INT1", "PCINT0", "PCINT1", "PCINT2", "WDT", "TIMER2_COMPA",
	"TIMER2_COMPB", "TIMER2_OVF", "TIMER1_CAPT", "TIMER1_COMPA", "TIMER1_COMPB",
	"TIMER1_OVF", "TIMER0_COMPA", "TIMER0_COMPB", "TIMER0_OVF", "SPI_STC", "USART_RX",
	"USART_UDRE", "USART_TX", "ADC", "EE_READY", "ANALOG_COMP", "TWI", "SPM_READY",
}

// avrVector decodes a jmp or rjmp at off: its length and the byte address
// of the target; 0 if it is not one
func avrVector(data []byte, off int) (n int, target uint64) {
	if off+2 > len(data) {
		return 0, 0
	}
	w := le16(data[off:])
	switch {
	case w&0xFE0E == 0x940C && off+4 <= len(data): // jmp k
		k := uint64(w&0x1F0)>>3 | uint64(w&1)
		return 4, (k<<16 | uint64(le16(data[off+2:]))) * 2
	case w&0xF000 == 0xC000: // rjmp k
		k := int64(w & 0xFFF)
		if k >= 0x800 {
			k -= 0x1000
		}
		t := int64(off)/2 + 1 + k
		if t < 0 {
			t += 4096 // wraps around the flash of 8 KB parts (ATtiny85: rjmp to the bootloader)
		}
		return 2, uint64(t) * 2
	}
	return 0, 0
}

func detectAVR(im *Image, s *Segment, data []byte) []Guess {
	// The application table is at 0; a bootloader placed by its file (at
	// BOOTRST, 0x7000 for the ATmega32U4) has its own table at its start
	if im.Raw() && s.Addr != 0 {
		return nil
	}
	first, _ := avrVector(data, 0)
	if first == 0 {
		return nil
	}
	stride := first
	if first == 2 && len(data) >= 4 && le16(data[2:]) == 0 {
		stride = 4 // rjmp and nop: jmp vectors relaxed by the linker
	}
	end := s.Addr + uint64(s.Size)
	var vs []Vector
	minT := end // the code starts at the first handler: the table ends there
	for off := 0; off+stride <= len(data) && len(vs) < 128 && s.Addr+uint64(off) < minT; off += stride {
		n, t := avrVector(data, off)
		at := s.Addr + uint64(off)
		if n == 0 {
			if le16(data[off:]) == 0x9518 { // reti
				vs = append(vs, Vector{Index: len(vs), Addr: at, Off: int64(off)})
				continue
			}
			break
		}
		if n == 2 { // rjmp: avrVector counts from the start of the segment
			t += s.Addr
		}
		if t >= end && !im.Contains(t) || t < at && t != 0 {
			break
		}
		if t > at && t < minT {
			minT = t
		}
		vs = append(vs, Vector{Index: len(vs), Addr: t, Off: int64(off)})
	}
	n := len(vs)
	if n < 2 || stride == 2 && n < 4 {
		return nil
	}
	fam := avrFamily(n, stride)
	for i := range vs {
		switch {
		case n == 26 && stride == 4 || n == 26 && fam == "ATmega48/88":
			vs[i].Name = atmega328Names[i]
		case i == 0:
			vs[i].Name = "RESET"
		default:
			vs[i].Name = fmt.Sprintf("vector_%d", i)
		}
	}
	insn := "jmp"
	if stride == 2 {
		insn = "rjmp"
	}
	score := 0.5 + 0.4*min(1, float64(n)/16)
	return []Guess{{Arch: "avr", Base: s.Addr, Entry: vs[0].Addr, Vectors: vs, Score: score, Family: fam,
		Reason: fmt.Sprintf("AVR vector table: %d %s vectors, reset 0x%X", n, insn, vs[0].Addr)}}
}

// avrFamily tells the part by the number of vectors
func avrFamily(n, stride int) string {
	if stride == 4 {
		switch n {
		case 26:
			return "ATmega328P/168/88"
		case 57:
			return "ATmega2560/1280"
		case 35:
			return "ATmega644/1284"
		case 31:
			return "ATmega164/324"
		case 43:
			return "ATmega32U4"
		case 21:
			return "ATmega16/32"
		}
		return "ATmega"
	}
	switch n {
	case 19:
		return "ATmega8"
	case 26:
		return "ATmega48/88"
	case 15:
		return "ATtiny25/45/85"
	case 20:
		return "ATtiny2313"
	}
	return "ATtiny"
}

// ---- RISC-V vector tables of GD32V / CH32V ----

func detectRISCV(im *Image, s *Segment, data []byte) []Guess {
	if len(data) < 64 {
		return nil
	}
	w0 := le32(data)
	var off int64
	switch {
	case w0&0xFFF == 0x06F: // jal x0, imm
		imm := int64(w0>>31)<<20 | int64(w0>>21&0x3FF)<<1 | int64(w0>>20&1)<<11 | int64(w0>>12&0xFF)<<12
		if imm&(1<<20) != 0 {
			imm -= 1 << 21
		}
		off = imm
	case le16(data)&0xE003 == 0xA001: // c.j
		h := uint32(le16(data))
		imm := int64(h>>12&1)<<11 | int64(h>>11&1)<<4 | int64(h>>9&3)<<8 | int64(h>>8&1)<<10 |
			int64(h>>7&1)<<6 | int64(h>>6&1)<<7 | int64(h>>3&7)<<1 | int64(h>>2&1)<<5
		if imm&(1<<11) != 0 {
			imm -= 1 << 12
		}
		off = imm
	default:
		return nil
	}
	if off <= 0 || off >= s.Size {
		return nil
	}
	// The vectors after the jump: addresses of handlers at the base
	hi08, low := 0, 0
	words := 0
	for i := 4; i+4 <= len(data) && i < 4+4*32; i += 4 {
		v := le32(data[i:])
		if v == 0 {
			continue
		}
		words++
		switch {
		case v&0xFFF00000 == 0x08000000 && int64(v-0x08000000) < s.Size:
			hi08++
		case int64(v) < s.Size:
			low++
		}
	}
	base, fam := s.Addr, ""
	if im.Raw() {
		base = 0x08000000
		if low > hi08 {
			base = 0
		}
	}
	switch base {
	case 0x08000000:
		fam = "GD32VF103"
	case 0:
		fam = "CH32V"
	}
	frac := 0.0
	if words > 0 {
		frac = float64(max(hi08, low)) / float64(words)
	}
	if frac < 0.5 && words > 4 {
		return nil
	}
	return []Guess{{Arch: "riscv32", Base: base, Entry: base + uint64(off), Score: 0.5 + 0.35*frac, Family: fam,
		Reason: fmt.Sprintf("RISC-V jump to 0x%X at the start, %d of %d words after it point into the image", base+uint64(off), max(hi08, low), words)}}
}

// ---- x86 boot sector ----

func detectX86Boot(im *Image, s *Segment, data []byte) []Guess {
	if len(data) < 512 || data[510] != 0x55 || data[511] != 0xAA {
		return nil
	}
	if !im.Raw() && s.Addr != 0x7C00 {
		return nil
	}
	switch data[0] {
	case 0xEB, 0xE9, 0xFA, 0x33, 0x31, 0xFC, 0xEA, 0xB8, 0x8C, 0x0E:
	default:
		return nil
	}
	return []Guess{{Arch: "x86-16", Base: 0x7C00, Entry: 0x7C00, Score: 0.8, Family: "PC BIOS",
		Reason: fmt.Sprintf("x86 boot sector: 55 AA at 510, starts with %02X", data[0])}}
}

// ---- a flash dump of an ESP32 ----

func detectESPDump(s *Segment) []Guess {
	var out []Guess
	for _, off := range []int64{0x1000, 0} {
		if s.Size <= off+24 {
			continue
		}
		sub := io.NewSectionReader(s.Data, off, s.Size-off)
		l, err := espParse(sub, s.Size-off, true)
		if err != nil {
			continue
		}
		h := readAt(sub, 4, 4)
		g := Guess{Arch: l.chip.Arch, Base: 0, Entry: uint64(le32(h)), Score: 0.85, Family: l.chip.Name,
			Reason: fmt.Sprintf("%s flash dump: bootloader image at 0x%X", l.chip.Name, off)}
		if sum := espSum(sub, l); readAt(sub, l.csumOff, 1)[0] == sum {
			g.Score = 0.9
		}
		out = append(out, g)
		break
	}
	return out
}

// ---- instruction statistics ----

// commonInsts are frequent in the code of each architecture and rare in
// the decoding of data
var commonInsts = map[string][]string{
	"arm": {"push", "pop", "bl", "bx", "blx", "ldr", "str", "ldrb", "strb", "ldrh", "strh", "mov", "movs",
		"cmp", "b", "beq", "bne", "adds", "subs", "add", "sub", "cbz", "cbnz", "ldm", "stm", "ands", "orrs", "lsls", "lsrs", "it"},
	"arm64": {"ldp", "stp", "ret", "bl", "b", "adrp", "add", "mov", "ldr", "str", "cbz", "cbnz", "cmp", "b.ne",
		"b.eq", "sub", "ldrb", "strb", "tbz", "tbnz", "csel", "movz", "movk"},
	"avr": {"ldi", "rjmp", "rcall", "call", "jmp", "ret", "reti", "push", "pop", "mov", "movw", "lds", "sts",
		"ld", "st", "std", "ldd", "out", "in", "brne", "breq", "cpi", "cpc", "cp", "eor", "adiw", "sbiw", "subi", "sbci", "andi", "ori"},
	"riscv": {"addi", "lw", "sw", "jal", "jalr", "lui", "auipc", "beq", "bne", "li", "mv", "ret", "j",
		"beqz", "bnez", "sd", "ld", "andi", "slli", "srli", "add", "sub", "lbu", "sb",
		"c.addi", "c.lw", "c.sw", "c.li", "c.mv", "c.j", "c.jr", "c.lwsp", "c.swsp", "c.jal", "c.beqz", "c.bnez", "c.addi16sp", "c.addi4spn"},
	"xtensa": {"l32r", "l32i", "s32i", "entry", "retw", "mov", "movi", "call0", "call4", "call8", "callx8",
		"callx0", "ret", "j", "beqz", "bnez", "addi", "add", "l8ui", "s8i", "extui", "bltu", "beq", "bne"},
	"x86": {"mov", "push", "pop", "call", "jmp", "ret", "cmp", "je", "jne", "jz", "jnz", "test", "lea",
		"xor", "add", "sub", "int", "inc", "dec", "and", "or", "movzx", "shl", "shr"},
}

func commonSet(id string) map[string]bool {
	key := id
	switch {
	case strings.HasPrefix(id, "riscv"):
		key = "riscv"
	case strings.HasPrefix(id, "x86"):
		key = "x86"
	case strings.HasPrefix(id, "avr"):
		key = "avr"
	}
	m := map[string]bool{}
	for _, s := range commonInsts[key] {
		m[s] = true
	}
	return m
}

// normMnemonic drops the width suffixes and the condition codes of the
// mnemonics that are common anyway
func normMnemonic(m string) string {
	m = strings.ToLower(m)
	for _, suf := range []string{".w", ".n"} {
		m = strings.TrimSuffix(m, suf)
	}
	return m
}

// statsResult is how much a decoding looks like code
type statsResult struct {
	n, invalid, common, targets, good int
}

func (r statsResult) score() float64 {
	if r.n == 0 {
		return 0
	}
	valid := 1 - float64(r.invalid)/float64(r.n)
	common := min(1, float64(r.common)/float64(r.n)/0.8)
	tg := 0.0
	if r.targets > 0 {
		// real code branches every 20 instructions or so
		tg = float64(r.good) / float64(max(r.targets, 8, r.n/20))
	}
	return 0.3*valid*valid + 0.5*common + 0.2*tg
}

// decodeStats decodes code from addr and counts what looks like code
func decodeStats(a disasm.Arch, mode int, code []byte, addr uint64) statsResult {
	var r statsResult
	starts := map[uint64]bool{}
	var targets []uint64
	set := commonSet(a.ID())
	end := addr + uint64(len(code))
	maxN := 16384
	for off := 0; off < len(code) && r.n < maxN; {
		in := a.Decode(code[off:], addr+uint64(off), mode)
		if in.Len <= 0 {
			break
		}
		r.n++
		starts[addr+uint64(off)] = true
		if in.Kind == disasm.Invalid {
			r.invalid++
		} else {
			m := normMnemonic(in.Mnemonic)
			if set[m] || set[strings.TrimSuffix(m, "s")] {
				r.common++
			}
			if in.HasTarget && (in.Kind == disasm.Call || in.Kind == disasm.Jump || in.Kind == disasm.Branch) {
				r.targets++
				targets = append(targets, in.Target)
			}
		}
		off += in.Len
	}
	for _, t := range targets {
		if t >= addr && t < end && starts[t] {
			r.good++
		}
	}
	return r
}

func detectStats(im *Image, s *Segment, data []byte) []Guess {
	if len(data) < 64 {
		return nil
	}
	if len(data) > statsMax {
		data = data[:statsMax]
	}
	base := s.Addr
	var out []Guess
	for _, a := range disasm.Archs() {
		modes := []int{disasm.ModeDefault}
		if a.ID() == "arm" {
			modes = []int{disasm.ModeThumb, disasm.ModeARM}
		}
		for _, m := range modes {
			r := decodeStats(a, m, data, base)
			sc := r.score()
			if a.ID() == "avrtiny" {
				// the reduced core decodes the code of the others as well:
				// it is rarer
				sc *= 0.98
			}
			if sc < 0.35 {
				continue
			}
			mode := ""
			if a.ID() == "arm" {
				mode = map[int]string{disasm.ModeThumb: " (Thumb)", disasm.ModeARM: " (A32)"}[m]
			}
			score := 0.6 * sc
			if !im.Raw() && r.targets >= 50 && r.good == r.targets && r.invalid*100 <= r.n && r.common*10 >= r.n*8 {
				// The format placed the code and it decodes cleanly with every
				// branch landing on an instruction: sure enough to analyze
				score = 0.85 * sc
			}
			out = append(out, Guess{Arch: a.ID(), Base: base, Entry: base, Score: score, Mode: m,
				Reason: fmt.Sprintf("instruction statistics%s: %d%% valid, %d%% common instructions, %d of %d branch targets on instructions inside",
					mode, 100-100*r.invalid/max(r.n, 1), 100*r.common/max(r.n, 1), r.good, r.targets)})
		}
	}
	return out
}

// rp2040 boot2 check of an image (for Verify)
func checkRP2040(im *Image) []string {
	if !im.Contains(0x10000000) || !im.Contains(0x100000FF) {
		return nil
	}
	b := make([]byte, 256)
	if n, _ := im.ReadAddr(b, 0x10000000); n != 256 {
		return nil
	}
	// Only when the vector table follows: otherwise it is not a boot2
	if !ramSP(le32(b[0:])) && im.Contains(0x10000104) {
		v := make([]byte, 8)
		if n, _ := im.ReadAddr(v, 0x10000100); n == 8 && ramSP(le32(v)) && le32(v[4:])&1 == 1 {
			if want := crcMPEG2(b[:252]); want != le32(b[252:]) {
				return []string{fmt.Sprintf("RP2040 boot2 CRC 0x%08X, should be 0x%08X", le32(b[252:]), want)}
			}
		}
	}
	return nil
}
