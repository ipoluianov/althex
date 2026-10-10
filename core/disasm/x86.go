package disasm

import (
	"fmt"
	"strings"

	"golang.org/x/arch/x86/x86asm"
)

// x86 decodes with golang.org/x/arch in Intel syntax
type x86 struct {
	mode int
}

func init() {
	register(x86{16})
	register(x86{32})
	register(x86{64})
}

func (a x86) ID() string { return fmt.Sprintf("x86-%d", a.mode) }

func (a x86) Name() string {
	switch a.mode {
	case 16:
		return "x86 16-bit (real mode)"
	case 32:
		return "x86 32-bit"
	}
	return "x86-64"
}

func (a x86) MaxLen() int { return 15 }

func (a x86) Decode(code []byte, addr uint64, mode int) Inst {
	in, err := x86asm.Decode(code, a.mode)
	// A lone prefix is how the decoder shows bytes it cannot decode
	if err != nil || in.Len == 0 || in.Op == 0 {
		// One byte that is not an instruction: shown as data
		if len(code) == 0 {
			return Inst{}
		}
		return Inst{Addr: addr, Len: 1, Mnemonic: "db", Operands: fmt.Sprintf("0x%02X", code[0]), Kind: Invalid}
	}
	text := x86asm.IntelSyntax(in, addr, nil)
	mn, ops, _ := strings.Cut(text, " ")
	out := Inst{Addr: addr, Len: in.Len, Mnemonic: mn, Operands: ops}
	switch in.Op {
	case x86asm.JMP, x86asm.LJMP:
		out.Kind = Jump
	case x86asm.CALL, x86asm.LCALL:
		out.Kind = Call
	case x86asm.RET, x86asm.LRET, x86asm.IRET, x86asm.IRETD, x86asm.IRETQ:
		out.Kind = Return
	case x86asm.INT, x86asm.INTO, x86asm.SYSCALL, x86asm.SYSENTER:
		out.Kind = Interrupt
	case x86asm.JA, x86asm.JAE, x86asm.JB, x86asm.JBE, x86asm.JE, x86asm.JG, x86asm.JGE, x86asm.JL, x86asm.JLE,
		x86asm.JNE, x86asm.JNO, x86asm.JNP, x86asm.JNS, x86asm.JO, x86asm.JP, x86asm.JS,
		x86asm.JCXZ, x86asm.JECXZ, x86asm.JRCXZ, x86asm.LOOP, x86asm.LOOPE, x86asm.LOOPNE:
		out.Kind = Branch
	}
	if rel, ok := in.Args[0].(x86asm.Rel); ok && out.Kind != Other {
		t := int64(addr) + int64(in.Len) + int64(rel)
		if a.mode == 16 {
			t &= 0xFFFF
		}
		out.Target, out.HasTarget = uint64(t), true
	}
	return out
}
