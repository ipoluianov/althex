// Package analysis finds the code of a firmware (FW-6, FW-7, FW-8): it
// disassembles recursively from the entry points (the reset handler, the
// vectors), following jumps and calls and the changes of the instruction
// set (ARM/Thumb), so code and data are told apart; it finds functions,
// labels and cross references, follows the constants put into registers
// and so knows the addresses of memory accesses, which the caller names
// (peripheral registers).
package analysis

import (
	"context"
	"encoding/binary"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/ipoluianov/althex/core/disasm"
)

// Memory is the address space of the device: the bytes of the firmware at
// their addresses
type Memory interface {
	ReadAddr(p []byte, addr uint64) (int, error)
	Contains(addr uint64) bool
}

// Entry is where the code starts: the reset handler, a vector, a function the user marked
type Entry struct {
	Addr uint64
	Mode int // disasm.ModeThumb for Thumb code
	Name string
	// Code: the code starts here but it is not a function (the jmp of an
	// AVR vector)
	Code bool
}

// Options of an analysis
type Options struct {
	Arch    disasm.Arch
	Entries []Entry
	// Labels name addresses that are not entries: data from symbol tables
	Labels map[uint64]string
	// Ranges are where the image has bytes: they are searched for tables of
	// pointers to functions (ARM: Thumb addresses of code starting with a
	// push of lr); nil - no search
	Ranges []Range
	// Coprocessors are the numbers of the coprocessors of the chip: their
	// instructions are code; the others are taken for data (Cortex-M has
	// none but the FPU; the RP2350 has 0, 4, 7)
	Coprocessors []int
	// Chip is the chip or the core when known ("RP2040", "ESP8266",
	// "ESP32-C3", "cortex-m0+"): the instructions it does not have are
	// taken for data, and so are found less functions in data
	Chip string
	// MaxInsts stops a runaway analysis; 0 - 2 000 000
	MaxInsts int
	// Annotate names a memory access at a known address (e.g. "RCC->APB2ENR"),
	// with the value stored when it is known; "" - nothing to say
	Annotate func(addr uint64, store bool, value uint64, known bool) string
}

// RefKind is what a reference does
type RefKind int

const (
	RefCall RefKind = iota
	RefJump
	RefBranch
	RefRead
	RefWrite
	RefLiteral // a constant of the code read from memory (a literal pool)
	RefVector  // an entry of the vector table
)

func (k RefKind) String() string {
	return [...]string{"call", "jump", "branch", "read", "write", "literal", "vector"}[k]
}

// Ref is a reference from an instruction to an address
type Ref struct {
	From, To uint64
	Kind     RefKind
}

// Code is an instruction found to be code
type Code struct {
	disasm.Inst
	Mode int
	Func uint64 // the function it belongs to
	// Note is what the analysis knows of it: the peripheral register reached,
	// the constant loaded
	Note string
}

// Func is a function: the target of a call or an entry point
type Func struct {
	Addr    uint64
	End     uint64 // past its last instruction (the instructions may have gaps)
	Insts   int
	Calls   []uint64 // the functions it calls
	Callers []uint64 // the functions calling it
	// NoReturn: no path of it returns (abort, a main loop)
	NoReturn bool
}

// Result is what an analysis found
type Result struct {
	Arch   disasm.Arch
	Insts  map[uint64]*Code
	Order  []uint64 // the addresses of the instructions, sorted
	Funcs  map[uint64]*Func
	Refs   map[uint64][]Ref // the references to an address
	Data   map[uint64]int   // literals: their address and size
	Labels map[uint64]string
	// Truncated: the analysis stopped at MaxInsts
	Truncated bool
}

type work struct {
	addr uint64
	mode int
}

// Analyze disassembles the firmware from the entry points
func Analyze(ctx context.Context, mem Memory, opt Options) *Result {
	if opt.MaxInsts <= 0 {
		opt.MaxInsts = 2_000_000
	}
	// The functions that do not return (abort, a longjmp, the main loop)
	// are known only after an analysis; the code after a call to them is
	// not code (often a literal pool): the analysis is made again without it
	// The functions called that other code was decoded over are decoded
	// first in the next round: a call is surer than a flow
	noReturn := map[uint64]bool{}
	first := map[uint64]int{}
	var r *Result
	prev := -1
	for round := 0; round < 4; round++ {
		var blocked map[uint64]int
		r, blocked = analyzeOnce(ctx, mem, opt, noReturn, first)
		// Settled: what another round would change is little
		settled := prev >= 0 && abs(len(r.Insts)-prev)*200 < prev
		prev = len(r.Insts)
		more := false
		for f := range findNoReturn(r, noReturn) {
			if !noReturn[f] {
				noReturn[f], more = true, true
			}
		}
		if settled {
			more = false
		}
		for f, m := range blocked {
			if _, ok := first[f]; !ok {
				first[f], more = m, true
			}
		}
		if !more || ctx.Err() != nil || r.Truncated {
			break
		}
	}
	for f := range noReturn {
		if fn := r.Funcs[f]; fn != nil {
			fn.NoReturn = true
		}
	}
	return r
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func analyzeOnce(ctx context.Context, mem Memory, opt Options, noReturn map[uint64]bool, first map[uint64]int) (*Result, map[uint64]int) {
	if opt.Coprocessors == nil && strings.Contains(strings.ToUpper(opt.Chip), "RP2350") {
		opt.Coprocessors = []int{0, 4, 7} // GPIO, the double FPU, the redundancy one
	}
	r := &Result{
		Arch:   opt.Arch,
		Insts:  make(map[uint64]*Code),
		Funcs:  make(map[uint64]*Func),
		Refs:   make(map[uint64][]Ref),
		Data:   make(map[uint64]int),
		Labels: make(map[uint64]string),
	}
	a := &analyzer{r: r, mem: mem, opt: opt, covered: make(map[uint64]bool), noReturn: noReturn, blocked: make(map[uint64]int)}
	for addr, name := range opt.Labels {
		if name = Ident(name); name != "" && mem.Contains(addr) {
			r.Labels[addr] = name
		}
	}
	for _, e := range opt.Entries {
		if !mem.Contains(e.Addr) {
			continue
		}
		if !e.Code {
			a.addFunc(e.Addr)
		}
		if name := Ident(e.Name); name != "" {
			r.Labels[e.Addr] = name
		}
		a.queue = append(a.queue, work{e.Addr, e.Mode})
	}
	// The queue is a stack: the sure functions go last to be decoded first
	firsts := make([]uint64, 0, len(first))
	for f := range first {
		firsts = append(firsts, f)
	}
	sort.Slice(firsts, func(i, j int) bool { return firsts[i] > firsts[j] })
	for _, f := range firsts {
		a.addFunc(f)
		a.queue = append(a.queue, work{f, first[f]})
	}
	a.walk(ctx)
	a.scanPointers()
	a.walk(ctx)
	// The calls through registers with known values lead to more code,
	// which has more of them
	for round := 0; round < 16 && ctx.Err() == nil && !a.r.Truncated; round++ {
		a.sortOrder()
		a.found = 0
		a.followConstants()
		if a.found == 0 {
			break
		}
		a.walk(ctx)
	}
	a.sortOrder()
	a.assignFuncs()
	a.recording = true
	a.followConstants()
	a.nameLabels()
	return r, a.blocked
}

type analyzer struct {
	r       *Result
	mem     Memory
	opt     Options
	queue   []work
	covered map[uint64]bool // the bytes inside instructions (not their starts)
	buf     [16]byte
	// recording: the constants are followed for the result (references,
	// notes); not recording: only to find more code
	recording bool
	found     int // code found by a discovery pass
	noReturn  map[uint64]bool
	cases     map[uint64]string // caseHelper's answers
	blocked   map[uint64]int    // functions called inside other instructions: their modes
}

func (a *analyzer) addFunc(addr uint64) {
	if _, ok := a.r.Funcs[addr]; !ok {
		a.r.Funcs[addr] = &Func{Addr: addr}
	}
}

func (a *analyzer) addRef(from, to uint64, kind RefKind) {
	a.r.Refs[to] = append(a.r.Refs[to], Ref{From: from, To: to, Kind: kind})
}

// walk decodes from the queued addresses, flow by flow
func (a *analyzer) walk(ctx context.Context) {
	for len(a.queue) > 0 {
		if len(a.r.Insts) >= a.opt.MaxInsts {
			a.r.Truncated = true
			return
		}
		if len(a.r.Insts)&0xfff == 0 && ctx.Err() != nil {
			return
		}
		w := a.queue[len(a.queue)-1]
		a.queue = a.queue[:len(a.queue)-1]
		a.flow(w)
	}
}

// flow decodes one straight line of code until it ends (a jump, a return,
// code seen already, bytes that are not code)
func (a *analyzer) flow(w work) {
	addr, mode := w.addr, w.mode
	bound := -1 // the last "cmp rN, #K" of the flow: the size of a switch
	for {
		if _, seen := a.r.Insts[addr]; seen || a.covered[addr] {
			if a.covered[addr] && addr == w.addr && a.calledDirectly(addr) {
				a.blocked[addr] = mode
			}
			return
		}
		n, _ := a.mem.ReadAddr(a.buf[:a.opt.Arch.MaxLen()], addr)
		if n == 0 {
			return
		}
		in := a.opt.Arch.Decode(a.buf[:n], addr, mode)
		if in.Len > 0 && in.Len <= n {
			in.Bytes = a.buf[:in.Len]
		}
		if in.Len <= 0 || in.Kind == disasm.Invalid || a.unlikely(in) {
			return
		}
		in.Bytes = append([]byte(nil), a.buf[:in.Len]...)
		a.r.Insts[addr] = &Code{Inst: in, Mode: mode}
		if a.isARM() {
			if k, ok := cmpBound(in); ok {
				bound = k
			}
		}
		for i := 1; i < in.Len; i++ {
			a.covered[addr+uint64(i)] = true
		}
		if in.HasTarget {
			tmode := mode
			switch in.TargetMode {
			case disasm.ModeThumb, disasm.ModeARM:
				tmode = in.TargetMode
			}
			target := in.Target
			switch in.Kind {
			case disasm.Call:
				a.addRef(addr, target, RefCall)
				if a.mem.Contains(target) {
					a.addFunc(target)
				}
			case disasm.Jump:
				a.addRef(addr, target, RefJump)
			default:
				a.addRef(addr, target, RefBranch)
			}
			if a.mem.Contains(target) {
				a.queue = append(a.queue, work{target, tmode})
			}
		}
		// Literal pools are data, not code to fall into
		for _, e := range in.Effects {
			if e.Kind == disasm.LoadLiteral && e.Size > 0 {
				a.r.Data[e.Addr] = e.Size
				a.addRef(addr, e.Addr, RefLiteral)
			}
		}
		switch in.Kind {
		case disasm.Jump, disasm.Return:
			if a.isARM() && bound >= 0 && (in.Mnemonic == "tbb" || in.Mnemonic == "tbh") {
				a.switchTable(addr, addr+uint64(in.Len), bound, mode, map[string]string{"tbb": "uqi", "tbh": "uhi"}[in.Mnemonic])
			}
			return
		case disasm.Call:
			// GCC's switch on Thumb-1: "bl __gnu_thumb1_case_uqi" and the
			// table right after the call, which does not come back there
			if a.isARM() && in.HasTarget {
				if kind := a.caseHelper(in.Target); kind != "" {
					if bound >= 0 {
						a.switchTable(addr, addr+uint64(in.Len), bound, mode, kind)
					}
					return // the table follows the call, not code
				}
			}
			if in.HasTarget && a.noReturn[in.Target] {
				return
			}
		}
		addr += uint64(in.Len)
		if _, isData := a.r.Data[addr]; isData {
			return
		}
	}
}

// calledDirectly tells a function some instruction calls by its address
func (a *analyzer) calledDirectly(addr uint64) bool {
	for _, ref := range a.r.Refs[addr] {
		if ref.Kind == RefCall {
			return true
		}
	}
	return false
}

// cmpBound reads "cmp rN, #K": the highest case of a switch
func cmpBound(in disasm.Inst) (int, bool) {
	if in.Mnemonic != "cmp" && in.Mnemonic != "cmp.w" {
		return 0, false
	}
	i := strings.Index(in.Operands, ", #")
	if i < 0 {
		return 0, false
	}
	k, err := strconv.ParseInt(strings.TrimSpace(in.Operands[i+3:]), 0, 32)
	if err != nil || k < 0 || k > 1024 {
		return 0, false
	}
	return int(k), true
}

// caseHelper tells whether a function is one of GCC's helpers of switch
// on Thumb-1 (__gnu_thumb1_case_uqi...): it reads the table at lr by r0
// and jumps: "uqi", "sqi", "uhi", "si"; "" - not one. A veneer to it
// ("ldr r0, =f; mov r12, r0; bx r12") is followed when f is in the image.
func (a *analyzer) caseHelper(f uint64) string {
	if k, ok := a.cases[f]; ok {
		return k
	}
	if a.cases == nil {
		a.cases = make(map[uint64]string)
	}
	a.cases[f] = ""
	kind, readsLR, lit := "", false, uint64(0)
	var buf [16]byte
	addr := f
	for i := 0; i < 14; i++ {
		n, _ := a.mem.ReadAddr(buf[:a.opt.Arch.MaxLen()], addr)
		if n == 0 {
			break
		}
		in := a.opt.Arch.Decode(buf[:n], addr, disasm.ModeThumb)
		if in.Len <= 0 || in.Kind == disasm.Invalid {
			break
		}
		ops := in.Operands
		switch {
		case strings.HasPrefix(in.Mnemonic, "mov") && strings.HasSuffix(ops, ", lr"):
			readsLR = true
		case strings.Contains(ops, "[r1, r0]") || strings.Contains(ops, "[r1, r0, lsl"):
			switch strings.TrimSuffix(in.Mnemonic, ".w") {
			case "ldrb":
				kind = "uqi"
			case "ldrsb":
				kind = "sqi"
			case "ldrh":
				kind = "uhi"
			case "ldr":
				kind = "si"
			}
		}
		for _, e := range in.Effects {
			if e.Kind == disasm.LoadLiteral && e.Size == 4 {
				var w [4]byte
				if got, _ := a.mem.ReadAddr(w[:], e.Addr); got == 4 {
					lit = uint64(binary.LittleEndian.Uint32(w[:]))
				}
			}
		}
		if in.Kind == disasm.Return || in.Kind == disasm.Jump || in.Mnemonic == "bx" {
			if in.Mnemonic == "bx" && in.Operands == "r12" && lit&1 == 1 && i < 6 && lit&^1 != f {
				k := a.caseHelper(lit &^ 1) // a veneer
				a.cases[f] = k
				return k
			}
			break
		}
		addr += uint64(in.Len)
	}
	if !readsLR {
		kind = ""
	}
	a.cases[f] = kind
	return kind
}

// switchTable takes the table of a switch at start with bound+1 entries:
// "uqi", "sqi" (bytes), "uhi" (halfwords): the target is start + 2*entry;
// "si" (words at a 4-aligned start): start + entry. With kind "" the kinds
// are tried in turn and one that gives code at every target is taken. The
// table becomes data, the targets code.
func (a *analyzer) switchTable(from, start uint64, bound, mode int, kind string) bool {
	kinds := []string{kind}
	if kind == "" {
		kinds = []string{"uqi", "uhi", "si", "sqi"}
	}
	for _, k := range kinds {
		base, size := start, 1
		switch k {
		case "uhi":
			size = 2
		case "si":
			base, size = (start+3)&^3, 4
		}
		n := bound + 1
		tab := make([]byte, n*size)
		if got, _ := a.mem.ReadAddr(tab, base); got != len(tab) {
			continue
		}
		end := base + uint64(len(tab))
		targets := make([]uint64, n)
		ok := true
		for i := 0; i < n && ok; i++ {
			var t uint64
			switch k {
			case "uqi":
				t = base + 2*uint64(tab[i])
			case "sqi":
				t = uint64(int64(base) + 2*int64(int8(tab[i])))
			case "uhi":
				t = base + 2*uint64(binary.LittleEndian.Uint16(tab[2*i:]))
			case "si":
				t = uint64(int64(base) + int64(int32(binary.LittleEndian.Uint32(tab[4*i:]))))
			}
			// The cases follow the table (the signed ones may precede it)
			ok = t&1 == 0 && (t >= end || k == "sqi" || k == "si") && t != from && a.mem.Contains(t)
			if ok && kind == "" {
				ok = a.plausible(t, mode, false)
			}
			targets[i] = t
		}
		if !ok {
			continue
		}
		a.r.Data[base] = len(tab)
		for i := 0; i < len(tab); i++ {
			a.covered[base+uint64(i)] = true
		}
		for _, t := range targets {
			a.addRef(from, t, RefBranch)
			a.queue = append(a.queue, work{t, mode})
		}
		return true
	}
	return false
}

func (a *analyzer) sortOrder() {
	a.r.Order = make([]uint64, 0, len(a.r.Insts))
	for addr := range a.r.Insts {
		a.r.Order = append(a.r.Order, addr)
	}
	slices.Sort(a.r.Order)
	// A literal inside code that was decoded as an instruction too: the
	// literal wins only where no flow reached it
	for addr := range a.r.Data {
		if _, isCode := a.r.Insts[addr]; isCode {
			delete(a.r.Data, addr)
		}
	}
}

// assignFuncs gives each instruction its function: the one reaching it by
// the flow inside functions (not by calls)
func (a *analyzer) assignFuncs() {
	entries := make([]uint64, 0, len(a.r.Funcs))
	for f := range a.r.Funcs {
		entries = append(entries, f)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i] < entries[j] })
	assigned := make(map[uint64]bool)
	for _, f := range entries {
		fn := a.r.Funcs[f]
		stack := []uint64{f}
		for len(stack) > 0 {
			addr := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for {
				c, ok := a.r.Insts[addr]
				if !ok || assigned[addr] {
					break
				}
				// Another function's entry is not inside this one
				if addr != f {
					if _, isFunc := a.r.Funcs[addr]; isFunc {
						break
					}
				}
				assigned[addr] = true
				c.Func = f
				fn.Insts++
				fn.End = max(fn.End, addr+uint64(c.Len))
				if c.HasTarget {
					switch c.Kind {
					case disasm.Call:
						fn.Calls = appendUnique(fn.Calls, c.Target)
					case disasm.Jump, disasm.Branch:
						stack = append(stack, c.Target)
					}
				}
				if c.Kind == disasm.Jump || c.Kind == disasm.Return {
					break
				}
				addr += uint64(c.Len)
			}
		}
	}
	for _, f := range a.r.Funcs {
		for _, callee := range f.Calls {
			if g := a.r.Funcs[callee]; g != nil {
				g.Callers = appendUnique(g.Callers, f.Addr)
			}
		}
	}
}

func appendUnique(s []uint64, v uint64) []uint64 {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

// regs are the values of registers known at a point of the code
type regs map[int]uint64

// followConstants goes through the code in order, tracking the constants
// put into registers within each straight run (forgotten where a branch
// lands and after calls), and notes the memory accesses whose address
// becomes known
func (a *analyzer) followConstants() {
	known := regs{}
	prevEnd := uint64(0)
	for _, addr := range a.r.Order {
		c := a.r.Insts[addr]
		// A new run: after a gap, at a branch target, at a function start
		if addr != prevEnd || len(a.r.Refs[addr]) > 0 {
			known = regs{}
		}
		prevEnd = addr + uint64(c.Len)
		for _, e := range c.Effects {
			a.apply(c, e, known)
		}
		if c.Kind == disasm.Call {
			known = regs{}
		}
	}
}

// pairBase gives the 16-bit pointer of an AVR register pair (X, Y, Z)
func (a *analyzer) baseValue(base int, known regs) (uint64, bool) {
	lo, ok := known[base]
	if !ok {
		return 0, false
	}
	if a.opt.Arch.ID() == "avr" && (base == 26 || base == 28 || base == 30) {
		hi, ok := known[base+1]
		if !ok {
			return 0, false
		}
		return lo&0xFF | (hi&0xFF)<<8, true
	}
	return lo, true
}

func (a *analyzer) apply(c *Code, e disasm.Effect, known regs) {
	switch e.Kind {
	case disasm.SetConst:
		known[e.Dst] = uint64(e.Imm)
		if e.Addr != 0 && e.Imm == 0 {
			known[e.Dst] = e.Addr
		}
	case disasm.SetLow16:
		known[e.Dst] = uint64(e.Imm) & 0xFFFF
	case disasm.SetHigh16:
		if v, ok := known[e.Dst]; ok {
			known[e.Dst] = v&0xFFFF | (uint64(e.Imm)&0xFFFF)<<16
			a.note(c, fmt.Sprintf("= 0x%X", known[e.Dst]))
			a.codePointer(known[e.Dst])
		} else {
			delete(known, e.Dst)
		}
	case disasm.AddImm:
		if e.Src < 0 {
			known[e.Dst] = e.Addr
		} else if v, ok := known[e.Src]; ok {
			known[e.Dst] = v + uint64(e.Imm)
			if e.Imm != 0 {
				a.codePointer(known[e.Dst]) // lui and addi (RISC-V)
			}
		} else {
			delete(known, e.Dst)
		}
	case disasm.LoadLiteral:
		var b [8]byte
		size := min(max(e.Size, 1), 8)
		if n, _ := a.mem.ReadAddr(b[:size], e.Addr); n == size {
			v := binary.LittleEndian.Uint64(b[:])
			known[e.Dst] = v
			if e.Dst == 15 && a.isARM() && c.Kind == disasm.Jump {
				// ldr pc, =f: a veneer
				a.apply(c, disasm.Effect{Kind: disasm.JumpReg, Src: 15, Dst: -1, Base: -1}, known)
			}
			a.note(c, fmt.Sprintf("= 0x%X", v))
			// The literal may be an address in the code: a pointer to a
			// function (ARM: odd, a Thumb address)
			if size == 4 {
				// with a prologue: the pointers to strings are odd too
				a.codePointer(v)
			}
		} else {
			delete(known, e.Dst)
		}
	case disasm.MemAccess:
		addr, ok := e.Addr, e.Base < 0
		if !ok {
			var base uint64
			if base, ok = a.baseValue(e.Base, known); ok {
				addr = base + uint64(e.Imm)
			}
		}
		if ok && a.recording {
			kind := RefRead
			if e.Store {
				kind = RefWrite
			}
			a.addRef(c.Addr, addr, kind)
			value, vk := uint64(0), false
			if e.Store && e.Src >= 0 {
				value, vk = known[e.Src]
			}
			if a.opt.Annotate != nil {
				if note := a.opt.Annotate(addr, e.Store, value, vk); note != "" {
					c.Note = note
				}
			}
			if c.Note == "" {
				c.Note = fmt.Sprintf("[0x%X]", addr)
			}
		}
		if !e.Store && e.Dst >= 0 {
			delete(known, e.Dst)
		}
	case disasm.Clobber:
		delete(known, e.Dst)
	case disasm.JumpReg:
		v, ok := a.baseValue(e.Src, known)
		if !ok {
			return
		}
		target, mode := v+uint64(e.Imm), c.Mode
		switch {
		case a.opt.Arch.ID() == "avr":
			target = (v & 0xFFFF) * 2 // Z holds a word address
		case a.isARM():
			mode = disasm.ModeARM
			if target&1 == 1 {
				mode = disasm.ModeThumb
			}
			target &^= 1
		}
		if !a.mem.Contains(target) {
			return
		}
		if !a.recording {
			c.Target, c.HasTarget, c.TargetMode = target, true, mode
			a.maybeFunc(target, mode, true)
			return
		}
		kind := RefJump
		if c.Kind == disasm.Call {
			kind = RefCall
		}
		a.addRef(c.Addr, target, kind)
		a.note(c, fmt.Sprintf("-> 0x%X", target))
	}
}

// unlikely tells the instructions that a firmware does not have, though
// they decode: what the flow meets in data. Cortex-M has no coprocessors
// but the FPU (whose instructions are vmov, vldr...) and no A-profile
// system instructions; objdump marks the unpredictable register uses.
func (a *analyzer) unlikely(in disasm.Inst) bool {
	if a.notOnChip(in) {
		return true
	}
	if a.opt.Arch.ID() == "xtensa" && in.Kind == disasm.Call && in.HasTarget && in.Target&3 != 0 {
		return true // call0..call12 go to words
	}
	if a.opt.Arch.ID() != "arm" {
		return false
	}
	if in.Kind == disasm.Call && in.HasTarget && !a.mem.Contains(in.Target) {
		return true // bl reaches 16 MB: a Cortex-M calls into its image
	}
	if strings.Contains(strings.ToLower(in.Comment), "unpredictable") || strings.Contains(in.Mnemonic, "<") || strings.Contains(in.Operands, "<und>") {
		return true
	}
	mn := strings.TrimSuffix(in.Mnemonic, ".w")
	if strings.HasPrefix(mn, "it") && len(mn) <= 5 && in.Operands == "" {
		return true // an IT without a condition
	}
	usesPC := strings.Contains(in.Operands, "pc")
	if usesPC && (strings.HasPrefix(mn, "stm") || strings.HasPrefix(mn, "push")) {
		return true // pc is never stored
	}
	if usesPC && in.Len == 4 && strings.Contains(in.Operands, "!") && strings.Contains(in.Operands, "[pc") {
		return true // writeback to pc
	}
	if usesPC && in.Len == 4 {
		ok := false
		for _, p := range []string{"ldr", "pld", "pli", "ldm", "pop", "add", "sub", "adr", "tbb", "tbh", "mov", "bx", "blx", "vldr", "lda"} {
			ok = ok || strings.HasPrefix(mn, p)
		}
		if !ok {
			return true // pc as an operand of the others is unpredictable
		}
	}
	// bx and blx with the bits that must be zero set
	if in.Len == 2 && len(in.Bytes) == 2 && in.Bytes[1] == 0x47 && in.Bytes[0]&7 != 0 {
		return true
	}
	// "cmp r0, r1" in the encoding of the high registers
	if in.Len == 2 && mn == "cmp" && len(in.Bytes) == 2 && in.Bytes[1] == 0x45 && in.Bytes[0]&0x80 == 0 && in.Bytes[0]&0x40 == 0 {
		return true
	}
	for _, p := range []string{"cdp", "mcr", "mrc", "ldc", "stc", "setpan", "setend", "bkpt"} {
		if !strings.HasPrefix(in.Mnemonic, p) {
			continue
		}
		switch p {
		case "bkpt":
			return in.Operands != "0x0000" && in.Operands != "0x00ab"
		case "setpan", "setend":
			return true
		}
		// A coprocessor the chip has (RP2350: p0 GPIO, p4 DCP, p7 RCP)
		var cp int
		if _, err := fmt.Sscanf(strings.TrimPrefix(in.Operands, "p"), "%d", &cp); err == nil {
			for _, ok := range a.opt.Coprocessors {
				if cp == ok {
					return false
				}
			}
		}
		return true
	}
	return false
}

// notOnChip tells the instructions the chip of Options.Chip does not have
func (a *analyzer) notOnChip(in disasm.Inst) bool {
	chip := strings.ToLower(a.opt.Chip)
	mn := in.Mnemonic
	switch {
	case chip == "":
		return false
	case strings.Contains(chip, "esp8266"):
		// The LX106 has no register windows (the call0 ABI)
		// nor booleans, nor the FPU, nor MAC16, nor zero-overhead loops
		for _, p := range []string{"entry", "retw", "call4", "call8", "call12", "callx4", "callx8", "callx12", "movsp", "rotw", "l32e", "s32e", "rfwo", "rfwu",
			"andb", "andbc", "orb", "orbc", "xorb", "any4", "all4", "any8", "all8", "bf", "bt",
			"lsi", "lsiu", "ssi", "ssiu", "lsx", "lsxu", "ssx", "ssxu", "wfr", "rfr", "rur", "wur",
			"mul.aa", "mul.ad", "mul.da", "mul.dd", "umul.aa", "mula", "muls", "ldinc", "lddec",
			"loop", "loopnez", "loopgtz"} {
			if mn == p || strings.HasPrefix(mn, p+".") {
				return true
			}
		}
		if strings.HasSuffix(mn, ".s") {
			return true // the single precision FPU
		}
	case strings.HasPrefix(chip, "esp32-c") || strings.HasPrefix(chip, "esp32-h") || strings.Contains(chip, "ch32v0"):
		// RV32IMC without the F and D extensions
		return strings.HasPrefix(mn, "f") && !strings.HasPrefix(mn, "fence") || strings.HasPrefix(mn, "c.f")
	case a.isARM() && (strings.Contains(chip, "rp2040") || strings.Contains(chip, "m0") || strings.Contains(chip, "m23") || strings.Contains(chip, "m1")):
		// ARMv6-M and v8-M Baseline: the 32-bit instructions are few
		if in.Len != 4 {
			return false
		}
		base := strings.TrimSuffix(mn, ".w")
		for _, p := range []string{"bl", "mrs", "msr", "dmb", "dsb", "isb", "udf"} {
			if base == p {
				return false
			}
		}
		if strings.Contains(chip, "m23") {
			for _, p := range []string{"movw", "movt", "sdiv", "udiv", "ldrex", "strex", "lda", "stl", "b", "sg", "tt"} {
				if strings.HasPrefix(base, p) {
					return false
				}
			}
		}
		return true
	}
	return false
}

// findNoReturn finds the functions none of whose paths return: they end in
// loops or in calls of such functions. A path ending in a jump through a
// register or in a tail jump to a function that returns returns.
func findNoReturn(r *Result, known map[uint64]bool) map[uint64]bool {
	returns := make(map[uint64]bool)
	byFunc := make(map[uint64][]*Code)
	for _, c := range r.Insts {
		byFunc[c.Func] = append(byFunc[c.Func], c)
	}
	for changed := true; changed; {
		changed = false
		for f := range r.Funcs {
			if returns[f] {
				continue
			}
			for _, c := range byFunc[f] {
				ret := false
				switch c.Kind {
				case disasm.Return:
					ret = true
				case disasm.Jump:
					// A jump through a register, a tail jump to another
					// function that returns, a jump out of the code
					_, inFunc := r.Funcs[c.Target]
					ret = !c.HasTarget || inFunc && c.Target != f && returns[c.Target] || c.HasTarget && r.Insts[c.Target] == nil
				case disasm.Branch:
					ret = !c.HasTarget // a conditional return (bxne lr, popne {pc})
				}
				// A flow that stops in bytes that are not code: not known
				// to end, unless at a call of a function that does not return
				if !ret && c.Kind != disasm.Jump && c.Kind != disasm.Return && r.Insts[c.Addr+uint64(c.Len)] == nil {
					ret = !(c.Kind == disasm.Call && c.HasTarget && known[c.Target])
				}
				if ret {
					returns[f], changed = true, true
					break
				}
			}
		}
	}
	out := make(map[uint64]bool)
	for f := range r.Funcs {
		if !returns[f] && len(byFunc[f]) > 0 {
			out[f] = true
		}
	}
	return out
}

func (a *analyzer) isARM() bool {
	return strings.HasPrefix(a.opt.Arch.ID(), "arm") && a.opt.Arch.ID() != "arm64"
}

// note sets the comment of an instruction, in the pass for the result
func (a *analyzer) note(c *Code, s string) {
	if a.recording {
		c.Note = s
	}
}

// maybeFunc takes an address found in the data as a function: sure when
// the code goes there (a call through a register), else when the bytes
// there decode as code
func (a *analyzer) maybeFunc(addr uint64, mode int, sure bool) {
	if _, seen := a.r.Insts[addr]; seen || a.covered[addr] || !a.mem.Contains(addr) {
		return
	}
	if _, isData := a.r.Data[addr]; isData {
		return
	}
	if !sure && !a.plausible(addr, mode, false) {
		return
	}
	a.addFunc(addr)
	a.queue = append(a.queue, work{addr, mode})
	a.found++
}

// plausible tells whether code starts at addr: a few valid instructions
// before a jump or a return, starting with a push of lr when prologue
func (a *analyzer) plausible(addr uint64, mode int, prologue bool) bool {
	var buf [16]byte
	for i := 0; i < 6; i++ {
		n, _ := a.mem.ReadAddr(buf[:a.opt.Arch.MaxLen()], addr)
		if n == 0 {
			return false
		}
		in := a.opt.Arch.Decode(buf[:n], addr, mode)
		if in.Len > 0 && in.Len <= n {
			in.Bytes = buf[:in.Len]
		}
		if in.Len <= 0 || in.Kind == disasm.Invalid || in.Kind == disasm.Interrupt && i == 0 || a.unlikely(in) {
			return false
		}
		if i == 0 && prologue && !a.prologue(in) {
			return false
		}
		if in.Kind == disasm.Jump || in.Kind == disasm.Return {
			return true
		}
		addr += uint64(in.Len)
	}
	return true
}

// prologue tells the first instruction of a function: ARM push of lr,
// Xtensa entry (the windowed ABI of the ESP32), RISC-V making its frame
func (a *analyzer) prologue(in disasm.Inst) bool {
	switch id := a.opt.Arch.ID(); {
	case a.isARM():
		return strings.HasPrefix(in.Mnemonic, "push") && strings.Contains(in.Operands, "lr") ||
			strings.HasPrefix(in.Mnemonic, "stmdb") && strings.Contains(in.Operands, "sp!") && strings.Contains(in.Operands, "lr")
	case id == "xtensa":
		if strings.Contains(strings.ToLower(a.opt.Chip), "esp8266") {
			// call0 ABI: the frame is made by addi a1, a1, -n
			ops := strings.ReplaceAll(in.Operands, " ", "")
			return strings.HasPrefix(in.Mnemonic, "addi") && strings.HasPrefix(ops, "a1,a1,-")
		}
		return in.Mnemonic == "entry"
	case strings.HasPrefix(id, "riscv"):
		ops := strings.ReplaceAll(in.Operands, " ", "")
		return (in.Mnemonic == "addi" || in.Mnemonic == "c.addi16sp") && strings.HasPrefix(ops, "sp,sp,-")
	}
	return false
}

// codePointer takes a constant met in the code for a pointer to a
// function when the code there starts as functions do
func (a *analyzer) codePointer(v uint64) {
	if a.recording || !a.mem.Contains(v&^1) {
		return
	}
	mode := disasm.ModeDefault
	switch {
	case a.isARM():
		if v&1 == 0 {
			return
		}
		v, mode = v&^1, disasm.ModeThumb
	case a.opt.Arch.ID() == "xtensa", strings.HasPrefix(a.opt.Arch.ID(), "riscv"):
		if v&1 != 0 {
			return
		}
	default:
		return
	}
	if _, seen := a.r.Insts[v]; seen {
		return
	}
	if a.plausible(v, mode, true) {
		a.maybeFunc(v, mode, true)
	}
}

// scanPointers finds the tables of pointers to functions in the image:
// words pointing to code that starts as functions do (ARM: Thumb
// addresses)
func (a *analyzer) scanPointers() {
	id := a.opt.Arch.ID()
	if len(a.opt.Ranges) == 0 || !a.isARM() && id != "xtensa" && id != "riscv32" {
		return
	}
	buf := make([]byte, 64<<10)
	for _, rg := range a.opt.Ranges {
		for off := uint64(0); off < rg.Size; off += uint64(len(buf)) {
			n, _ := a.mem.ReadAddr(buf[:min(uint64(len(buf)), rg.Size-off)], rg.Addr+off)
			for i := 0; i+4 <= n; i += 4 {
				v := uint64(binary.LittleEndian.Uint32(buf[i:]))
				if _, isFunc := a.r.Funcs[v&^1]; isFunc || !a.mem.Contains(v&^1) {
					continue
				}
				a.codePointer(v)
			}
		}
	}
}

// nameLabels names the functions and the jump targets that have no name
func (a *analyzer) nameLabels() {
	for addr := range a.r.Funcs {
		if _, named := a.r.Labels[addr]; !named {
			a.r.Labels[addr] = fmt.Sprintf("sub_%X", addr)
		}
	}
	for to, refs := range a.r.Refs {
		if _, named := a.r.Labels[to]; named {
			continue
		}
		if _, isCode := a.r.Insts[to]; !isCode {
			if _, isData := a.r.Data[to]; isData {
				a.r.Labels[to] = fmt.Sprintf("lit_%X", to)
			}
			continue
		}
		for _, ref := range refs {
			if ref.Kind == RefJump || ref.Kind == RefBranch {
				a.r.Labels[to] = fmt.Sprintf("loc_%X", to)
				break
			}
		}
	}
}

// InstAt returns the instruction that holds the address (it may start before it)
func (r *Result) InstAt(addr uint64) (*Code, bool) {
	i := sort.Search(len(r.Order), func(i int) bool { return r.Order[i] > addr }) - 1
	if i < 0 {
		return nil, false
	}
	c := r.Insts[r.Order[i]]
	if addr < c.Addr+uint64(c.Len) {
		return c, true
	}
	return nil, false
}

// FuncAt returns the function an address of code belongs to
func (r *Result) FuncAt(addr uint64) (*Func, bool) {
	c, ok := r.InstAt(addr)
	if !ok {
		return nil, false
	}
	f, ok := r.Funcs[c.Func]
	return f, ok
}

// Name returns the label of an address, or "" when it has none
func (r *Result) Name(addr uint64) string { return r.Labels[addr] }

// SortedFuncs returns the functions by address
func (r *Result) SortedFuncs() []*Func {
	fs := make([]*Func, 0, len(r.Funcs))
	for _, f := range r.Funcs {
		fs = append(fs, f)
	}
	sort.Slice(fs, func(i, j int) bool { return fs[i].Addr < fs[j].Addr })
	return fs
}
