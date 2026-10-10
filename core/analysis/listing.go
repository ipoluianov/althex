package analysis

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Range is a part of the address space shown in the listing: a segment of
// the firmware
type Range struct {
	Name string
	Addr uint64
	Size uint64
}

// Item is a line of the listing: an instruction, or bytes of data
type Item struct {
	Addr uint64
	Len  int
	Code *Code // nil for data
	// Literal: a word the code reads as a constant
	Literal bool
}

// dataLine is how many bytes of data a line shows
const dataLine = 16

// Items lists the address space in order: the instructions found, the
// literals, and the other bytes as data lines
func (r *Result) Items(ranges []Range) []Item {
	var items []Item
	for _, rg := range ranges {
		end := rg.Addr + rg.Size
		// The first instruction at or after the start
		i := sort.Search(len(r.Order), func(i int) bool { return r.Order[i] >= rg.Addr })
		for addr := rg.Addr; addr < end; {
			if i < len(r.Order) && r.Order[i] == addr {
				c := r.Insts[addr]
				items = append(items, Item{Addr: addr, Len: c.Len, Code: c})
				addr += uint64(c.Len)
				i++
				continue
			}
			if size, ok := r.Data[addr]; ok {
				items = append(items, Item{Addr: addr, Len: size, Literal: true})
				addr += uint64(size)
				continue
			}
			// Data up to the next instruction, literal, label or line boundary
			next := min(end, (addr/dataLine+1)*dataLine)
			if i < len(r.Order) && r.Order[i] < next {
				next = r.Order[i]
			}
			for k := addr + 1; k < next; k++ {
				if _, ok := r.Data[k]; ok {
					next = k
					break
				}
				if _, ok := r.Labels[k]; ok {
					next = k
					break
				}
			}
			if next <= addr { // an instruction overlapping: skip its bytes
				next = addr + 1
			}
			items = append(items, Item{Addr: addr, Len: int(next - addr)})
			addr = next
			for i < len(r.Order) && r.Order[i] < addr {
				i++
			}
		}
	}
	return items
}

// OperandText is the operands of an instruction with the addresses of
// jumps and calls replaced by their labels
func (r *Result) OperandText(c *Code) string {
	ops := c.Operands
	if !c.HasTarget {
		return ops
	}
	label := r.Labels[c.Target]
	if label == "" {
		return ops
	}
	for _, form := range []string{fmt.Sprintf("0x%x", c.Target), fmt.Sprintf("0x%X", c.Target), fmt.Sprintf("0x%08x", c.Target)} {
		if strings.Contains(ops, form) {
			return strings.Replace(ops, form, label, 1)
		}
	}
	// A relative form: ".+6", ".-12" (AVR)
	for _, f := range strings.FieldsFunc(ops, func(r rune) bool { return r == ',' || r == ' ' }) {
		if len(f) > 2 && f[0] == '.' && (f[1] == '+' || f[1] == '-') {
			return strings.Replace(ops, f, label, 1)
		}
	}
	return ops
}

// AsmOptions tell how to write an assembler source
type AsmOptions struct {
	Header   string                      // directives at the top (".syntax unified\n.thumb")
	Footer   string                      // directives at the end
	Comment  string                      // the comment mark of the assembler: "@" for ARM, ";" for AVR, "#" for RISC-V and Xtensa
	Comments map[uint64]string           // the user's comments
	Read     func(p []byte, addr uint64) // the bytes of data lines
}

// WriteAsm writes the listing as an assembler source (FW-9): sections per
// range, labels, instructions, data as .byte and literals as .word, with
// the comments
func (r *Result) WriteAsm(w io.Writer, ranges []Range, opt AsmOptions) error {
	bw := bufio.NewWriter(w)
	if opt.Comment == "" {
		opt.Comment = "@"
	}
	if opt.Header != "" {
		fmt.Fprintln(bw, opt.Header)
	}
	// RISC-V: GNU as compresses what it can; the instructions keep their
	// size with .option rvc before the compressed ones and norvc before
	// the others
	riscv := r.Arch != nil && strings.HasPrefix(r.Arch.ID(), "riscv")
	for _, rg := range ranges {
		fmt.Fprintf(bw, "\n\t.section %s, \"ax\"\n\t%s 0x%X, %d bytes\n", sectionName(rg), opt.Comment, rg.Addr, rg.Size)
		rvc := ""
		for _, it := range r.Items([]Range{rg}) {
			if riscv && it.Code != nil {
				want := "norvc"
				if it.Len == 2 {
					want = "rvc"
				}
				if want != rvc {
					fmt.Fprintf(bw, "\t.option %s\n", want)
					rvc = want
				}
			}
			if label := r.Labels[it.Addr]; label != "" {
				if _, isFunc := r.Funcs[it.Addr]; isFunc {
					fmt.Fprintln(bw)
				}
				fmt.Fprintf(bw, "%s:\n", label)
			}
			comment := opt.Comments[it.Addr]
			var line string
			switch {
			case it.Code != nil:
				line = "\t" + it.Code.Mnemonic
				if ops := r.OperandText(it.Code); ops != "" {
					line += "\t" + ops
				}
				if it.Code.Asm != "" {
					comment = joinComment(it.Code.Text(), comment)
					line = "\t" + it.Code.Asm
				}
				if riscv && it.Len == 2 {
					line = rvcBranch(it.Code, r.OperandText(it.Code), line)
				}
				if it.Code.Note != "" {
					comment = joinComment(it.Code.Note, comment)
				}
			default:
				b := make([]byte, it.Len)
				if opt.Read != nil {
					opt.Read(b, it.Addr)
				}
				line = dataDirective(b, it.Literal)
			}
			if comment != "" {
				line += "\t" + opt.Comment + " " + comment
			}
			fmt.Fprintf(bw, "%s\n", line)
		}
	}
	if opt.Footer != "" {
		fmt.Fprintln(bw, opt.Footer)
	}
	return bw.Flush()
}

// AsmSyntax gives what an assembler source for the GNU assembler of the
// architecture needs to give back the same bytes (FW-10): the directives
// at its top and end, and the comment mark
func AsmSyntax(arch string) (header, footer, comment string) {
	header, comment = "/* Disassembled by AltHex */", "#"
	switch arch {
	case "arm":
		// The widest M profile: v8-M Mainline with DSP and the double FPU
		// takes the code of every Cortex-M
		header += "\n\t.syntax unified\n\t.arch armv8-m.main\n\t.arch_extension dsp\n\t.fpu fpv5-d16\n\t.thumb"
		comment = "@"
	case "arm-a32":
		header += "\n\t.syntax unified\n\t.arm"
		comment = "@"
	case "arm64":
		comment = "//"
	case "riscv32", "riscv64":
		// The sizes are kept by the .option rvc / norvc of the listing
		header += "\n\t.option norelax"
	case "xtensa":
		// Not to widen, narrow or reorder the instructions
		header += "\n\t.begin no-transform"
		footer = "\t.end no-transform"
	case "avr":
		header, comment = "; Disassembled by AltHex", ";"
	}
	return header, footer, comment
}

// Ident makes a name a label the assemblers take: letters, digits, '_',
// '.' and '$', not starting with a digit ("vector 3" -> "vector_3")
func Ident(name string) string {
	name = strings.TrimSpace(name)
	b := []byte(name)
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '.' || c == '$') {
			b[i] = '_'
		}
	}
	if len(b) > 0 && b[0] >= '0' && b[0] <= '9' {
		return "_" + string(b)
	}
	return string(b)
}

// rvcBranch writes a compressed RISC-V branch or jump as one: GNU as
// sizes the plain ones by its own layout. Near the edge of their reach it
// makes them long even when told c.beqz: there the bytes are written.
func rvcBranch(c *Code, ops, line string) string {
	reach := int64(0)
	switch c.Mnemonic {
	case "beqz", "bnez":
		reach = 256
	case "j", "jal":
		reach = 2048
	default:
		return line
	}
	if d := int64(c.Target - c.Addr); c.HasTarget && len(c.Bytes) == 2 && (d > reach-56 || d < -reach+56) {
		return fmt.Sprintf("\t.2byte\t0x%02X%02X\t# c.%s %s", c.Bytes[1], c.Bytes[0], c.Mnemonic, ops)
	}
	if c.Mnemonic == "jal" {
		return "\tc.jal\t" + strings.TrimPrefix(strings.TrimPrefix(ops, "ra,"), "ra, ")
	}
	return "\tc." + c.Mnemonic + "\t" + ops
}

func joinComment(a, b string) string {
	if b == "" {
		return a
	}
	return a + "; " + b
}

func sectionName(rg Range) string {
	name := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, rg.Name)
	if name == "" {
		name = fmt.Sprintf("seg_%X", rg.Addr)
	}
	if !strings.HasPrefix(name, ".") {
		name = "." + name
	}
	return name
}

// dataDirective writes bytes as .word (a literal of 4) or .byte
func dataDirective(b []byte, literal bool) string {
	if literal && len(b) == 4 {
		v := uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
		return fmt.Sprintf("\t.word\t0x%08X", v)
	}
	parts := make([]string, len(b))
	for i, c := range b {
		parts[i] = fmt.Sprintf("0x%02X", c)
	}
	return "\t.byte\t" + strings.Join(parts, ", ")
}
