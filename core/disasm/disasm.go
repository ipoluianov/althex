// Package disasm turns machine code into instructions (ANL-9): a decoder
// per architecture behind one interface, a linear listing of a range, and
// notes on the instructions (the BIOS calls of boot code, BOOT-4).
package disasm

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// Kind is what an instruction does to the flow of the program
type Kind int

const (
	Other  Kind = iota
	Jump        // an unconditional jump
	Branch      // a conditional jump
	Call
	Return
	Interrupt // int, syscall, svc
	Invalid   // bytes that are not an instruction
)

// Inst is a decoded instruction
type Inst struct {
	Addr     uint64
	Len      int
	Bytes    []byte
	Mnemonic string
	Operands string
	Kind     Kind
	// Target is where a jump, a branch or a call goes, when it is known
	// from the instruction alone; HasTarget tells whether it is
	Target    uint64
	HasTarget bool
	// TargetMode is the instruction set at the target when the jump or call
	// changes it (ARM BLX, BX to an odd address); 0 - the same
	TargetMode int
	// Effects are what the analysis follows: constants and memory accesses
	Effects []Effect
	Comment string
	// Asm is the instruction as the assembler takes it back to the same
	// bytes when the text shown would give others: "vmov.f32 s0, #1.0" for
	// objdump's "vmov.f32 s0, #112", "mov r8, r8" for its "nop",
	// ".inst.w 0xf85ff000" for an encoding GNU as does not make; "" - the
	// text shown
	Asm string
}

// Text is the instruction as written: "mov ax, 0x7c0"
func (i Inst) Text() string {
	if i.Operands == "" {
		return i.Mnemonic
	}
	return i.Mnemonic + " " + i.Operands
}

// Arch is an instruction set
type Arch interface {
	// ID is stable, for settings: "x86-16", "x86-32", "x86-64"...
	ID() string
	// Name is shown: "x86 16-bit (real mode)"
	Name() string
	// Decode decodes the instruction at the start of code, at the address,
	// in a mode (ModeDefault, or ModeThumb/ModeARM for ARM). Len is 0 only
	// for no code; otherwise it is 1..min(len(code), MaxLen()), and bytes
	// that are not an instruction (or too few for one) are Kind Invalid
	Decode(code []byte, addr uint64, mode int) Inst
	// MaxLen is the longest instruction, in bytes
	MaxLen() int
}

var archs []Arch

func register(a Arch) {
	archs = append(archs, a)
	sort.SliceStable(archs, func(i, j int) bool { return archs[i].Name() < archs[j].Name() })
}

// Archs returns the architectures there are decoders for
func Archs() []Arch { return archs }

// ByID returns the architecture of an ID; nil if there is none
func ByID(id string) Arch {
	for _, a := range archs {
		if a.ID() == id {
			return a
		}
	}
	return nil
}

// Linear decodes count instructions one after another from off in r; addr
// is the address of the byte at off (where the code is loaded)
func Linear(a Arch, r io.ReaderAt, off int64, addr uint64, count int) []Inst {
	return LinearMode(a, r, off, addr, count, ModeDefault)
}

// LinearMode is Linear in a mode of the architecture (ARM or Thumb)
func LinearMode(a Arch, r io.ReaderAt, off int64, addr uint64, count int, mode int) []Inst {
	buf := make([]byte, 64<<10)
	var out []Inst
	bufOff, bufLen := int64(-1), 0
	for len(out) < count {
		if bufOff < 0 || off+int64(a.MaxLen()) > bufOff+int64(bufLen) {
			n, _ := r.ReadAt(buf, off)
			if n == 0 {
				break
			}
			bufOff, bufLen = off, n
		}
		code := buf[off-bufOff : bufLen]
		in := a.Decode(code, addr, mode)
		if in.Len <= 0 {
			break
		}
		in.Bytes = append([]byte(nil), code[:in.Len]...)
		out = append(out, in)
		off += int64(in.Len)
		addr += uint64(in.Len)
	}
	// The notes of BIOS calls are of x86 boot code
	if strings.HasPrefix(a.ID(), "x86") {
		annotate(out)
	}
	return out
}

// FormatBytes writes the bytes of an instruction in hex
func FormatBytes(b []byte) string {
	var sb strings.Builder
	for i, c := range b {
		if i > 0 {
			sb.WriteByte(' ')
		}
		fmt.Fprintf(&sb, "%02X", c)
	}
	return sb.String()
}
