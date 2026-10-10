package analysis

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/ipoluianov/althex/core/disasm"
)

// toy is a tiny instruction set for the tests:
//
//	01        nop
//	02 tt     call tt
//	03 tt     jmp tt
//	04        ret
//	05 r v    r = v
//	06 r b o  store r at [b] + o
//	07 r tt   r = the byte at tt (a literal)
//	08 tt     branch to tt
type toy struct{}

func (toy) ID() string   { return "toy" }
func (toy) Name() string { return "toy" }
func (toy) MaxLen() int  { return 4 }
func (toy) Decode(code []byte, addr uint64, mode int) disasm.Inst {
	in := disasm.Inst{Addr: addr, Len: 1, Mnemonic: "db"}
	switch code[0] {
	case 1:
		in.Mnemonic = "nop"
	case 2, 3, 8:
		in.Len = 2
		in.Target, in.HasTarget = uint64(code[1]), true
		in.Mnemonic = map[byte]string{2: "call", 3: "jmp", 8: "br"}[code[0]]
		in.Kind = map[byte]disasm.Kind{2: disasm.Call, 3: disasm.Jump, 8: disasm.Branch}[code[0]]
		in.Operands = fmt.Sprintf("0x%x", code[1])
	case 4:
		in.Mnemonic, in.Kind = "ret", disasm.Return
	case 5:
		in.Len, in.Mnemonic = 3, "set"
		in.Effects = []disasm.Effect{{Kind: disasm.SetConst, Dst: int(code[1]), Imm: int64(code[2])}}
	case 6:
		in.Len, in.Mnemonic = 4, "store"
		in.Effects = []disasm.Effect{{Kind: disasm.MemAccess, Src: int(code[1]), Base: int(code[2]), Imm: int64(code[3]), Size: 1, Store: true}}
	case 7:
		in.Len, in.Mnemonic = 3, "lit"
		in.Effects = []disasm.Effect{{Kind: disasm.LoadLiteral, Dst: int(code[1]), Addr: uint64(code[2]), Size: 1}}
	default:
		in.Kind = disasm.Invalid
	}
	return in
}

type mem []byte

func (m mem) ReadAddr(p []byte, addr uint64) (int, error) {
	if addr >= uint64(len(m)) {
		return 0, nil
	}
	return copy(p, m[addr:]), nil
}
func (m mem) Contains(addr uint64) bool { return addr < uint64(len(m)) }

func TestAnalyze(t *testing.T) {
	m := make(mem, 64)
	copy(m[0:], []byte{
		0x05, 1, 0x30, // r1 = 0x30
		0x05, 2, 0x07, // r2 = 7
		0x06, 2, 1, 0x02, // store r2 at [r1+2] = 0x32
		0x02, 0x20, // call 0x20
		0x08, 0x10, // br 0x10
		0x04,          // ret
		0xFF,          // data
		0x07, 3, 0x18, // 0x10: lit r3 = [0x18]
		0x04, // ret
	})
	m[0x18] = 0xAB
	copy(m[0x20:], []byte{0x01, 0x03, 0x00}) // nop; jmp 0 (a loop back)
	var notes []string
	res := Analyze(context.Background(), m, Options{
		Arch:    toy{},
		Entries: []Entry{{Addr: 0, Name: "Reset"}},
		Annotate: func(addr uint64, store bool, v uint64, known bool) string {
			s := fmt.Sprintf("REG_%X", addr)
			if store && known {
				s += fmt.Sprintf(" = %d", v)
			}
			notes = append(notes, s)
			return s
		},
	})
	if res.Insts[6].Note != "REG_32 = 7" {
		t.Fatalf("store note %q (%v)", res.Insts[6].Note, notes)
	}
	if _, ok := res.Funcs[0x20]; !ok || res.Labels[0x20] != "sub_20" || res.Labels[0] != "Reset" {
		t.Fatalf("functions %v labels %v", res.Funcs, res.Labels)
	}
	if res.Insts[0x10] == nil || res.Insts[0x10].Note != "= 0xAB" || res.Data[0x18] != 1 {
		t.Fatal("the branch target or the literal")
	}
	if _, ok := res.Insts[15]; ok {
		t.Fatal("the byte after ret is code")
	}
	if f := res.Funcs[0]; len(f.Calls) != 1 || f.Calls[0] != 0x20 || len(res.Funcs[0x20].Callers) != 1 {
		t.Fatal("the call graph")
	}
	if c, ok := res.InstAt(7); !ok || c.Addr != 6 {
		t.Fatal("InstAt inside an instruction")
	}
	items := res.Items([]Range{{Name: "flash", Addr: 0, Size: 0x24}})
	var sb bytes.Buffer
	res.WriteAsm(&sb, []Range{{Name: "flash", Addr: 0, Size: 0x24}}, AsmOptions{Read: func(p []byte, a uint64) { copy(p, m[a:]) }})
	out := sb.String()
	for _, want := range []string{"Reset:", "\tcall\tsub_20", "loc_10:", "\t.byte\t0xFF", "@ REG_32 = 7", "lit_18:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("no %q in\n%s", want, out)
		}
	}
	if len(items) == 0 || items[0].Code == nil {
		t.Fatal("items")
	}
}

func TestNotes(t *testing.T) {
	path := NotesPath(t.TempDir(), "/x/fw.bin")
	n := LoadNotes(path)
	n.SetLabel(0x8000134, "Reset_Handler")
	n.SetComment(0x8000140, "clock setup")
	n.Device = "STM32F103xx"
	if err := n.Save(path); err != nil {
		t.Fatal(err)
	}
	m := LoadNotes(path)
	if l, _ := m.Label(0x8000134); l != "Reset_Handler" || m.Comment(0x8000140) != "clock setup" || m.Device != "STM32F103xx" {
		t.Fatalf("%+v", m)
	}
}

func TestIdent(t *testing.T) {
	for in, want := range map[string]string{
		"vector 3": "vector_3", "Reset": "Reset", "3x": "_3x", " USB Gen-eral ": "USB_Gen_eral",
		"_ZN3Foo3barEv": "_ZN3Foo3barEv", "привет": "____________", "": "",
	} {
		if got := Ident(in); got != want {
			t.Errorf("Ident(%q) = %q, want %q", in, got, want)
		}
	}
}
