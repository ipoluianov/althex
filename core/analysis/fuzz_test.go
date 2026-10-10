package analysis

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/ipoluianov/althex/core/disasm"
	"github.com/ipoluianov/althex/internal/fuzzguard"
)

// based is a firmware loaded at an address
type based struct {
	base uint64
	b    []byte
}

func (m based) ReadAddr(p []byte, addr uint64) (int, error) {
	if addr < m.base || addr-m.base >= uint64(len(m.b)) {
		return 0, nil
	}
	return copy(p, m.b[addr-m.base:]), nil
}
func (m based) Contains(addr uint64) bool { return addr >= m.base && addr-m.base < uint64(len(m.b)) }

// FuzzAnalyze analyzes any bytes as the code of each architecture, from
// the start and from an entry the input tells, and exports the listing
func FuzzAnalyze(f *testing.F) {
	archs := disasm.Archs()
	thumb := []byte{0x00, 0x50, 0x00, 0x20, 0x09, 0x00, 0x00, 0x08, 0x80, 0xB5, 0x01, 0x48, 0x00, 0xF0, 0x02, 0xF8,
		0x80, 0xBD, 0x00, 0xBF, 0x00, 0x10, 0x02, 0x40, 0x70, 0x47}
	x86 := []byte{0xFA, 0x31, 0xC0, 0xB4, 0x0E, 0xB0, 0x41, 0xCD, 0x10, 0xE8, 0x02, 0x00, 0xEB, 0xFE, 0xC3}
	avr := []byte{0x0C, 0x94, 0x04, 0x00, 0x11, 0x24, 0x0E, 0x94, 0x07, 0x00, 0xFF, 0xCF, 0x08, 0x95}
	rv := []byte{0x37, 0x05, 0x00, 0x40, 0x13, 0x05, 0x45, 0x01, 0x23, 0x20, 0xB5, 0x00, 0xEF, 0x00, 0x80, 0x00, 0x6F, 0x00, 0x00, 0x00, 0x82, 0x80}
	for i := range archs {
		for _, s := range [][]byte{thumb, x86, avr, rv} {
			f.Add(uint8(i), uint16(0), s)
		}
	}
	f.Fuzz(func(t *testing.T, ai uint8, entry uint16, data []byte) {
		a := archs[int(ai)%len(archs)]
		const base = 0x08000000
		mem := based{base, data}
		mode := disasm.ModeDefault
		switch a.ID() {
		case "arm":
			mode = disasm.ModeThumb
		case "arm-a32":
			mode = disasm.ModeARM
		}
		entries := []Entry{{Addr: base, Mode: mode, Name: "start"}, {Addr: base + uint64(entry), Mode: mode, Name: "entry"}}
		// A vector table: the reset handler of Cortex-M
		if len(data) >= 8 {
			entries = append(entries, Entry{Addr: uint64(binary.LittleEndian.Uint32(data[4:])) &^ 1, Mode: mode})
		}
		ranges := []Range{{Name: "flash", Addr: base, Size: uint64(len(data))}}
		stop := fuzzguard.Start(a.ID())
		defer stop()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		res := Analyze(ctx, mem, Options{
			Arch: a, Entries: entries, Ranges: ranges, MaxInsts: 20_000,
			Annotate: func(addr uint64, store bool, v uint64, known bool) string { return fmt.Sprintf("R_%X", addr) },
		})
		if res == nil {
			return
		}
		for _, c := range res.Insts {
			if c.Len < 1 || !mem.Contains(c.Addr) {
				t.Fatalf("%s: instruction %+v", a.ID(), c.Inst)
			}
			res.OperandText(c)
		}
		res.Items(ranges)
		header, footer, comment := AsmSyntax(a.ID())
		err := res.WriteAsm(io.Discard, ranges, AsmOptions{Header: header, Footer: footer, Comment: comment,
			Read: func(p []byte, addr uint64) { mem.ReadAddr(p, addr) }})
		if err != nil {
			t.Fatal(err)
		}
	})
}
