package disasm

import (
	"bytes"
	"testing"

	"github.com/ipoluianov/althex/internal/fuzzguard"
)

// fuzzArchs are the decoders the fuzz targets cover, with their modes and
// the number of registers their effects may name
var fuzzArchs = []struct {
	id    string
	modes []int
	regs  int
}{
	{"arm", []int{ModeDefault, ModeThumb, ModeARM}, 16},
	{"arm-a32", []int{ModeDefault, ModeThumb, ModeARM}, 16},
	{"arm64", []int{ModeDefault}, 32},
	{"avr", []int{ModeDefault}, 32},
	{"avrtiny", []int{ModeDefault}, 32},
	{"riscv32", []int{ModeDefault}, 32},
	{"riscv64", []int{ModeDefault}, 32},
	{"x86-16", []int{ModeDefault}, 32},
	{"x86-32", []int{ModeDefault}, 32},
	{"x86-64", []int{ModeDefault}, 32},
	{"xtensa", []int{ModeDefault}, 16},
}

// Every architecture has a fuzz target
func TestFuzzCoversArchs(t *testing.T) {
	for _, a := range Archs() {
		found := false
		for _, f := range fuzzArchs {
			found = found || f.id == a.ID()
		}
		if !found {
			t.Errorf("no fuzz target for %s", a.ID())
		}
	}
}

// checkInst checks the contract of Decode: nothing decoded from no bytes,
// otherwise at least one byte and no more than there are or MaxLen
func checkInst(t *testing.T, a Arch, code []byte, addr uint64, mode, regs int, in Inst) {
	t.Helper()
	if len(code) == 0 {
		if in.Len != 0 {
			t.Fatalf("%s: %d bytes decoded from none", a.ID(), in.Len)
		}
		return
	}
	if in.Len < 1 || in.Len > len(code) || in.Len > a.MaxLen() {
		t.Fatalf("%s mode %d: length %d of % x (%s)", a.ID(), mode, in.Len, code[:min(len(code), a.MaxLen())], in.Text())
	}
	if in.Addr != addr {
		t.Fatalf("%s: address 0x%x, want 0x%x", a.ID(), in.Addr, addr)
	}
	if in.Kind < Other || in.Kind > Invalid {
		t.Fatalf("%s: kind %d", a.ID(), in.Kind)
	}
	if in.Mnemonic == "" {
		t.Fatalf("%s: no mnemonic for % x", a.ID(), code[:in.Len])
	}
	if in.TargetMode != 0 && in.TargetMode != ModeThumb && in.TargetMode != ModeARM {
		t.Fatalf("%s: target mode %d", a.ID(), in.TargetMode)
	}
	reg := func(r int) bool { return r >= -1 && r < regs }
	for _, e := range in.Effects {
		if e.Kind < SetConst || e.Kind > JumpReg || !reg(e.Dst) || !reg(e.Src) || !reg(e.Base) || e.Size < 0 || e.Size > 64 {
			t.Fatalf("%s: effect %+v of %s", a.ID(), e, in.Text())
		}
	}
	_ = in.Text()
}

func fuzzArch(f *testing.F, id string) {
	a := ByID(id)
	if a == nil {
		f.Fatalf("no architecture %s", id)
	}
	var modes []int
	regs := 0
	for _, fa := range fuzzArchs {
		if fa.id == id {
			modes, regs = fa.modes, fa.regs
		}
	}
	// Seeds: the code of the tests of the package
	f.Add([]byte{0xFA, 0x31, 0xC0, 0xB4, 0x0E, 0xB0, 0x41, 0xCD, 0x10, 0xEB, 0xFE}, uint64(0x7C00), uint8(0))
	f.Add([]byte{0x0F, 0xFF, 0x90}, uint64(0), uint8(1))
	f.Add([]byte{0x00, 0xBF, 0x08, 0xBF, 0x01, 0x30, 0xDF, 0xF8, 0x04, 0x00, 0x70, 0x47}, uint64(0x08000000), uint8(1))
	f.Add([]byte{0x04, 0xE0, 0x2D, 0xE5, 0x1E, 0xFF, 0x2F, 0xE1, 0x00, 0x00, 0x9F, 0xE5}, uint64(0x8000), uint8(2))
	f.Add([]byte{0xFD, 0x7B, 0xBF, 0xA9, 0xC0, 0x03, 0x5F, 0xD6, 0x00, 0x00, 0x00, 0x90}, uint64(0x400000), uint8(0))
	f.Add([]byte{0x0C, 0x94, 0x34, 0x00, 0x11, 0x24, 0x1F, 0xBE, 0xCF, 0xEF, 0xD8, 0xE0}, uint64(0), uint8(0))
	f.Add([]byte{0x13, 0x05, 0x10, 0x00, 0x82, 0x80, 0x37, 0x05, 0x00, 0x40, 0x01, 0x45}, uint64(0x42000000), uint8(0))
	f.Add([]byte{0x36, 0x41, 0x00, 0x0C, 0x02, 0x1D, 0xF0, 0x21, 0xFF, 0xFF, 0xE5, 0x00}, uint64(0x40080000), uint8(0))
	f.Fuzz(func(t *testing.T, code []byte, addr uint64, m uint8) {
		defer fuzzguard.Start(t.Name())()
		mode := modes[int(m)%len(modes)]
		checkInst(t, a, nil, addr, mode, regs, a.Decode(nil, addr, mode))
		// Every start, so a truncated instruction at the end is decoded too
		for start := range min(len(code), 32) {
			b := code[start:]
			checkInst(t, a, b, addr+uint64(start), mode, regs, a.Decode(b, addr+uint64(start), mode))
		}
		// One after another, as the listing does (the IT blocks of Thumb)
		off, at := 0, addr
		for off < len(code) {
			in := a.Decode(code[off:], at, mode)
			checkInst(t, a, code[off:], at, mode, regs, in)
			off += in.Len
			at += uint64(in.Len)
		}
		LinearMode(a, bytes.NewReader(code), 0, addr, 64, mode)
	})
}

func FuzzDecodeARM(f *testing.F)     { fuzzArch(f, "arm") }
func FuzzDecodeARMA32(f *testing.F)  { fuzzArch(f, "arm-a32") }
func FuzzDecodeARM64(f *testing.F)   { fuzzArch(f, "arm64") }
func FuzzDecodeAVR(f *testing.F)     { fuzzArch(f, "avr") }
func FuzzDecodeAVRTiny(f *testing.F) { fuzzArch(f, "avrtiny") }
func FuzzDecodeRISCV32(f *testing.F) { fuzzArch(f, "riscv32") }
func FuzzDecodeRISCV64(f *testing.F) { fuzzArch(f, "riscv64") }
func FuzzDecodeX86_16(f *testing.F)  { fuzzArch(f, "x86-16") }
func FuzzDecodeX86_32(f *testing.F)  { fuzzArch(f, "x86-32") }
func FuzzDecodeX86_64(f *testing.F)  { fuzzArch(f, "x86-64") }
func FuzzDecodeXtensa(f *testing.F)  { fuzzArch(f, "xtensa") }
