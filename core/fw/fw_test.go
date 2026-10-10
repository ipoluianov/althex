package fw

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func open(t *testing.T, data []byte, name string) *Image {
	t.Helper()
	im, err := Open(bytes.NewReader(data), int64(len(data)), name)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return im
}

func best(t *testing.T, im *Image) Guess {
	t.Helper()
	gs := Detect(im)
	if len(gs) == 0 {
		t.Fatalf("%s: no guess", im.Format)
	}
	return gs[0]
}

func noIssues(t *testing.T, im *Image) {
	t.Helper()
	if v := im.Verify(); len(v) > 0 {
		t.Fatalf("%s: issues %q", im.Format, v)
	}
}

func TestRawCortexSTM32(t *testing.T) {
	im := open(t, cortexImage(0x08000000, 0, 0x20005000, 20, 1024), "fw.bin")
	if im.Format != "Raw binary" || len(im.Segments) != 1 || im.Segments[0].Addr != 0 {
		t.Fatalf("%+v", im)
	}
	g := best(t, im)
	if g.Arch != "arm" || g.Base != 0x08000000 || g.Family != "STM32" || g.Mode == 0 {
		t.Fatalf("%+v", g)
	}
	if g.Entry != 0x08000000+(16+20)*4+0x20 {
		t.Fatalf("entry 0x%X", g.Entry)
	}
	if len(g.Vectors) != 36 || g.Vectors[0].Name != "Initial SP" || g.Vectors[0].Addr != 0x20005000 ||
		g.Vectors[3].Name != "HardFault" || g.Vectors[16].Name != "IRQ0" || g.Vectors[35].Off != 35*4 {
		t.Fatalf("vectors %d %+v", len(g.Vectors), g.Vectors[:4])
	}
	if g.Vectors[1].Addr&1 != 0 {
		t.Fatal("the Thumb bit is kept")
	}
	if !strings.Contains(g.Reason, "Cortex-M") {
		t.Fatal(g.Reason)
	}
}

func TestRawCortexBase0(t *testing.T) {
	g := best(t, open(t, cortexImage(0, 0, 0x20010000, 30, 4096), "app.bin"))
	if g.Arch != "arm" || g.Base != 0 || len(g.Vectors) != 46 {
		t.Fatalf("%+v", g)
	}
	// An application after a bootloader: linked at 0x26000 (an nRF52 with a SoftDevice)
	g = best(t, open(t, cortexImage(0x26000, 0, 0x20010000, 30, 4096), "app.bin"))
	if g.Base != 0x26000 {
		t.Fatalf("base 0x%X: %s", g.Base, g.Reason)
	}
	// SAM3X
	g = best(t, open(t, cortexImage(0x80000, 0, 0x20088000, 45, 8192), "sam.bin"))
	if g.Base != 0x80000 || g.Family != "SAM3X" {
		t.Fatalf("%+v", g)
	}
}

func TestRawRP2040(t *testing.T) {
	im := open(t, rp2040Image(), "pico.bin")
	g := best(t, im)
	if g.Arch != "arm" || g.Base != 0x10000000 || g.Family != "RP2040" || g.Vectors[0].Off != 0x100 {
		t.Fatalf("%+v", g)
	}
}

func TestAVR(t *testing.T) {
	im := open(t, avrImage(), "blink.bin")
	g := best(t, im)
	if g.Arch != "avr" || g.Base != 0 || g.Entry != 0x68 || len(g.Vectors) != 26 {
		t.Fatalf("%+v", g)
	}
	if g.Vectors[0].Name != "RESET" || g.Vectors[1].Name != "INT0" || g.Vectors[25].Name != "SPM_READY" || g.Vectors[25].Addr != 0x7C {
		t.Fatalf("%+v", g.Vectors)
	}
	if !strings.Contains(g.Family, "ATmega328P") {
		t.Fatal(g.Family)
	}
}

func TestX86BootSector(t *testing.T) {
	g := best(t, open(t, x86Boot(), "boot.img"))
	if g.Arch != "x86-16" || g.Base != 0x7C00 {
		t.Fatalf("%+v", g)
	}
}

func TestESP32Image(t *testing.T) {
	data := esp32C3Image()
	im := open(t, data, "app.bin")
	if im.Format != "ESP32-C3 app image" || im.Arch != "riscv32" || im.Family != "ESP32-C3" || !im.HasEntry || im.Entry != 0x40380400 {
		t.Fatalf("%+v", im)
	}
	if len(im.Segments) != 3 {
		t.Fatalf("%d segments", len(im.Segments))
	}
	names := []string{"drom", "dram", "iram"}
	for i, s := range im.Segments {
		if s.Name != names[i] {
			t.Fatalf("segment %d: %s", i, s.Name)
		}
	}
	if s := im.Segments[2]; !s.Exec || s.Addr != 0x40380000 || s.Size != 256 || s.Off < 24 {
		t.Fatalf("%+v", s)
	}
	noIssues(t, im)
	if !hasInfo(im, "SHA-256", "(OK)") || !hasInfo(im, "Checksum", "(OK)") {
		t.Fatalf("%+v", im.Info)
	}
	g := best(t, im)
	if g.Arch != "riscv32" || g.Family != "ESP32-C3" || g.Entry != 0x40380400 {
		t.Fatalf("%+v", g)
	}
	// Bytes through the address space and the file
	b := make([]byte, 9)
	if _, err := im.ReadAddr(b, 0x3FC88000); err != nil || string(b) != "dram data" {
		t.Fatalf("%q %v", b, err)
	}
	off, ok := im.AddrToOff(0x3FC88000)
	if !ok || string(data[off:off+9]) != "dram data" {
		t.Fatal(off, ok)
	}
	if a, ok := im.OffToAddr(off + 2); !ok || a != 0x3FC88002 {
		t.Fatal(a, ok)
	}

	// Corrupted: a byte of a segment changes both the checksum and the hash
	bad := append([]byte(nil), data...)
	bad[off] ^= 0x40
	im = open(t, bad, "app.bin")
	v := im.Verify()
	if len(v) != 2 || !strings.Contains(v[0], "checksum") || !strings.Contains(v[1], "SHA-256") {
		t.Fatalf("%q", v)
	}
	fixed, err := FixESP32(bad)
	if err != nil {
		t.Fatal(err)
	}
	noIssues(t, open(t, fixed, "app.bin"))
	// The hash alone
	bad = append([]byte(nil), data...)
	bad[len(bad)-1] ^= 1
	if v := open(t, bad, "x").Verify(); len(v) != 1 || !strings.Contains(v[0], "SHA-256") {
		t.Fatalf("%q", v)
	}
	// A missing hash is added
	fixed, err = FixESP32(data[:len(data)-32])
	if err != nil || !bytes.Equal(fixed, data) {
		t.Fatal("the hash is not added", err)
	}
}

func TestESP32Xtensa(t *testing.T) {
	data := espImage(0, 0x40080000, false,
		espSeg{0x3F400020, []byte("rodata")},
		espSeg{0x400D0020, bytes.Repeat([]byte{0x36, 0x41, 0}, 20)},
		espSeg{0x40080000, []byte{0x36, 0x41, 0, 0x1d, 0xf0, 0}})
	im := open(t, data, "esp32.bin")
	if im.Format != "ESP32 app image" || im.Arch != "xtensa" || len(im.Segments) != 3 || im.Segments[2].Name != "irom" {
		t.Fatalf("%+v", im)
	}
	noIssues(t, im)
	// A flash dump with the bootloader at 0x1000
	dump := make([]byte, 0x1000+len(data)+100)
	copy(dump[0x1000:], data)
	im = open(t, dump, "flash.bin")
	g := best(t, im)
	if im.Format != "Raw binary" || g.Arch != "xtensa" || g.Family != "ESP32" {
		t.Fatalf("%s %+v", im.Format, g)
	}
}

func TestESP8266(t *testing.T) {
	im := open(t, esp8266Image(), "x.bin")
	if im.Format != "ESP8266 image" || im.Arch != "xtensa" || len(im.Segments) != 2 || im.Entry != 0x40101000 {
		t.Fatalf("%+v", im)
	}
	if im.Segments[0].Name != "dram" || im.Segments[1].Name != "iram" || !im.Segments[1].Exec {
		t.Fatalf("%+v", im.Segments)
	}
	noIssues(t, im)
}

func TestUF2(t *testing.T) {
	data := uf2TwoFamilies()
	im := open(t, data, "x.uf2")
	if im.Format != "UF2 (families RP2040, SAMD21)" || im.Arch != "arm" {
		t.Fatalf("%q %q", im.Format, im.Arch)
	}
	if len(im.Segments) != 2 || im.Segments[0].Addr != 0x2000 || im.Segments[0].Size != 512 ||
		im.Segments[1].Addr != 0x10000000 || im.Segments[1].Size != 768 || im.Segments[1].Name != "RP2040" {
		t.Fatalf("%+v", im.Segments)
	}
	noIssues(t, im)
	// The payload of the second block of RP2040
	off, ok := im.AddrToOff(0x10000104)
	if !ok || off != 512+32+4 {
		t.Fatal(off, ok)
	}
	if a, ok := im.OffToAddr(4*512 + 32 + 1); !ok || a != 0x2101 {
		t.Fatal(a, ok)
	}
	if _, ok := im.OffToAddr(512 + 300); ok {
		t.Fatal("padding of a block has an address")
	}
	// The RP2040 vector table after boot2
	var found bool
	for _, g := range Detect(im) {
		if g.Arch == "arm" && g.Base == 0x10000000 && g.Family == "RP2040" && len(g.Vectors) > 16 {
			found = true
		}
	}
	if !found {
		t.Fatalf("%+v", Detect(im))
	}
	// A lost block
	im = open(t, append(data[:512:512], data[1024:]...), "x.uf2")
	if v := im.Verify(); len(v) != 1 || !strings.Contains(v[0], "2 blocks of 3") {
		t.Fatalf("%q", v)
	}
	// One family
	im = open(t, uf2File(uf2Block{0xd42ba06c, 0x42000000, []byte{1, 2, 3}}), "c3.uf2")
	if im.Format != "UF2 (family ESP32-C3)" || im.Arch != "riscv32" || im.Family != "ESP32-C3" {
		t.Fatalf("%+v", im)
	}
}

func TestDfuSe(t *testing.T) {
	data := dfuseTwoTargets()
	im := open(t, data, "fw.dfu")
	if im.Format != "DfuSe" || len(im.Segments) != 3 {
		t.Fatalf("%+v", im)
	}
	s := im.Segments
	if s[0].Addr != 0x08000000 || s[0].Size != 512 || s[1].Addr != 0x08004000 || s[2].Addr != 0x1FFFF800 ||
		!strings.Contains(s[2].Name, "Option Bytes") || !strings.Contains(s[0].Name, "element 0") {
		t.Fatalf("%+v", s)
	}
	noIssues(t, im)
	if !hasInfo(im, "USB ID", "0483:DF11") {
		t.Fatalf("%+v", im.Info)
	}
	g := best(t, im)
	if g.Arch != "arm" || g.Base != 0x08000000 || g.Family != "STM32" {
		t.Fatalf("%+v", g)
	}
	// Option bytes from the image
	ob := make([]byte, 16)
	im.ReadAddr(ob, 0x1FFFF800)
	fs, err := DecodeConfig("stm32f1-option-bytes", ob)
	if err != nil || fs[0].Meaning != "no read protection (0xA5)" {
		t.Fatal(fs, err)
	}
	// A bad CRC
	bad := append([]byte(nil), data...)
	bad[300] ^= 1
	if v := open(t, bad, "x.dfu").Verify(); len(v) != 1 || !strings.Contains(v[0], "CRC") {
		t.Fatalf("%q", v)
	}
	fixed, err := FixDfuSe(bad)
	if err != nil {
		t.Fatal(err)
	}
	noIssues(t, open(t, fixed, "x.dfu"))
}

func TestELF(t *testing.T) {
	im := open(t, armELF(), "fw.elf")
	if im.Format != "ELF (ARM, 32-bit)" || im.Arch != "arm" || len(im.Segments) != 2 {
		t.Fatalf("%+v", im)
	}
	if s := im.Segments[1]; s.Addr != 0x08000100 || s.Exec || s.Size != 16 {
		t.Fatalf("%+v", s)
	}
	if !hasInfo(im, "Segment 1", "runs at 0x20000000") {
		t.Fatalf("%+v", im.Info)
	}
	if im.Entry&1 != 0 || im.Entry != 0x08000000+16*4+4*4+0x20 {
		t.Fatalf("entry 0x%X", im.Entry)
	}
	b := make([]byte, 16)
	if _, err := im.ReadAddr(b, 0x08000100); err != nil || string(b) != "initialized data" {
		t.Fatalf("%q %v", b, err)
	}
	g := best(t, im)
	if g.Arch != "arm" || g.Base != 0x08000000 || len(g.Vectors) != 20 || g.Score < 0.9 {
		t.Fatalf("%+v", g)
	}
}

func TestIntelHex(t *testing.T) {
	im := open(t, ihexFile(), "fw.hex")
	if im.Format != "Intel HEX" || len(im.Segments) != 2 || !im.HasEntry || im.Entry != 0x08000101 {
		t.Fatalf("%+v", im)
	}
	if s := im.Segments[1]; s.Addr != 0x08010000 || s.Off != -1 {
		t.Fatalf("%+v", s)
	}
	if _, ok := im.AddrToOff(0x08000000); ok {
		t.Fatal("a HEX byte has an offset")
	}
	// A read across the gap
	b := make([]byte, 300)
	n, err := im.ReadAddr(b, 0x08000000)
	if n != 256 || !errors.Is(err, ErrNoData) {
		t.Fatal(n, err)
	}
	if im.Contains(0x08000100) || !im.Contains(0x08010007) || im.Contains(0x08010008) {
		t.Fatal("Contains")
	}
	g := best(t, im)
	if g.Arch != "arm" || g.Base != 0x08000000 {
		t.Fatalf("%+v", g)
	}
	// Found by the content whatever the name
	if open(t, ihexFile(), "noext").Format != "Intel HEX" {
		t.Fatal("by content")
	}
}

func TestSRecord(t *testing.T) {
	im := open(t, srecFile(), "fw.s19")
	if im.Format != "S-record" || len(im.Segments) != 1 || im.Segments[0].Addr != 0x1000 || !im.HasEntry || im.Entry != 0 {
		t.Fatalf("%+v", im)
	}
	if !hasInfo(im, "Header", "hdr") {
		t.Fatalf("%+v", im.Info)
	}
}

func TestBroken(t *testing.T) {
	for _, data := range [][]byte{
		[]byte(":10000000FF"),             // a cut HEX
		[]byte("\x7FELF\x01\x01\x01junk"), // a broken ELF
		[]byte("DfuSe\x01\x00\x00\x00\x00\x01Targ"),
	} {
		if _, err := Open(bytes.NewReader(data), int64(len(data)), "x.hex"); !errors.Is(err, ErrFormat) {
			t.Fatalf("%q: %v", data, err)
		}
	}
	// E9 that is not an ESP image is a raw binary
	data := append([]byte{0xE9, 0x10, 0x20}, make([]byte, 100)...)
	if im := open(t, data, "x.bin"); im.Format != "Raw binary" {
		t.Fatal(im.Format)
	}
	// Empty
	if im := open(t, nil, "x.bin"); len(im.Segments) != 0 || len(Detect(im)) != 0 {
		t.Fatal("empty")
	}
}

func hasInfo(im *Image, key, sub string) bool {
	for _, kv := range im.Info {
		if kv.Key == key && strings.Contains(kv.Value, sub) {
			return true
		}
	}
	return false
}

func TestConfig(t *testing.T) {
	if len(ConfigKinds()) < 5 {
		t.Fatal(ConfigKinds())
	}
	get := func(fs []ConfigField, name string) ConfigField {
		for _, f := range fs {
			if f.Name == name {
				return f
			}
		}
		t.Fatalf("no %s", name)
		return ConfigField{}
	}
	// ATmega328P defaults: 0x62, 0xD9, 0xFF
	fs, err := DecodeConfig("avr-fuses", []byte{0x62, 0xD9, 0xFF})
	if err != nil {
		t.Fatal(err)
	}
	if f := get(fs, "CKSEL"); f.Value != 2 || !strings.Contains(f.Meaning, "8 MHz") || !strings.Contains(f.Meaning, "active-low") {
		t.Fatalf("%+v", f)
	}
	if f := get(fs, "CKDIV8"); f.Value != 0 || !strings.Contains(f.Meaning, "divided by 8") {
		t.Fatalf("%+v", f)
	}
	if f := get(fs, "SPIEN"); f.Value != 0 || !strings.HasPrefix(f.Meaning, "programmed") || f.Off != 1 || f.Bit != 5 {
		t.Fatalf("%+v", f)
	}
	if f := get(fs, "BOOTSZ"); f.Value != 0 || !strings.Contains(f.Meaning, "2048 words") {
		t.Fatalf("%+v", f)
	}
	if f := get(fs, "BOOTRST"); f.Value != 1 || !strings.Contains(f.Meaning, "0x0000") {
		t.Fatalf("%+v", f)
	}
	if f := get(fs, "BODLEVEL"); f.Value != 7 || !strings.Contains(f.Meaning, "disabled") {
		t.Fatalf("%+v", f)
	}
	fs, _ = DecodeConfig("avr-lock", []byte{0xFC})
	if f := get(fs, "LB"); f.Value != 0 || !strings.Contains(f.Meaning, "verification") {
		t.Fatalf("%+v", f)
	}
	// STM32F1: a bad complement, page groups 0 and 9 protected
	fs, _ = DecodeConfig("stm32f1-option-bytes", []byte{0x00, 0xFF, 0x07, 0xF8, 0, 0xFF, 0, 0xFF, 0xFE, 0x01, 0xFD, 0x00, 0xFF, 0, 0xFF, 0})
	if f := get(fs, "RDP"); !strings.Contains(f.Meaning, "protection on") {
		t.Fatalf("%+v", f)
	}
	if f := get(fs, "WRP1"); !strings.Contains(f.Meaning, "page group 9") {
		t.Fatalf("%+v", f)
	}
	if f := get(fs, "nWRP1"); !strings.Contains(f.Meaning, "mismatch") {
		t.Fatalf("%+v", f)
	}
	if f := get(fs, "nWRP0"); f.Meaning != "complement OK" {
		t.Fatalf("%+v", f)
	}
	// STM32F4 defaults: 0xAAEC, nWRP 0x0FFF
	fs, _ = DecodeConfig("stm32f4-option-bytes", []byte{0xEC, 0xAA, 0x55, 0x13, 0, 0, 0, 0, 0xFE, 0x0F, 0, 0, 0, 0, 0, 0})
	if f := get(fs, "RDP"); !strings.Contains(f.Meaning, "level 0") {
		t.Fatalf("%+v", f)
	}
	if f := get(fs, "BOR_LEV"); f.Value != 3 || !strings.Contains(f.Meaning, "off") {
		t.Fatalf("%+v", f)
	}
	if f := get(fs, "nWRP"); !strings.Contains(f.Meaning, "sector 0") {
		t.Fatalf("%+v", f)
	}
	// ESP32 eFuse: MAC 24:0a:c4:01:02:03, flash encryption on, JTAG off
	e := make([]byte, 28)
	put32(e[0:], 1<<20)
	put32(e[4:], 0xc4010203)
	mac := []byte{0x24, 0x0a, 0xc4, 1, 2, 3}
	put32(e[8:], 0x240a|uint32(esp32MacCRC(mac))<<16)
	put32(e[12:], 1<<15)
	put32(e[24:], 1<<6)
	fs, err = DecodeConfig("esp32-efuse", e)
	if err != nil {
		t.Fatal(err)
	}
	if f := get(fs, "MAC"); !strings.Contains(f.Meaning, "24:0a:c4:01:02:03") {
		t.Fatalf("%+v", f)
	}
	if f := get(fs, "MAC_CRC"); !strings.Contains(f.Meaning, "OK") {
		t.Fatalf("%+v", f)
	}
	if f := get(fs, "FLASH_CRYPT_CNT"); !strings.Contains(f.Meaning, "ENABLED") {
		t.Fatalf("%+v", f)
	}
	if f := get(fs, "JTAG_DISABLE"); f.Value != 1 || f.Off != 24 || f.Bit != 6 {
		t.Fatalf("%+v", f)
	}
	if f := get(fs, "CHIP_VER_REV1"); !strings.Contains(f.Meaning, "v1") {
		t.Fatalf("%+v", f)
	}
	if _, err := DecodeConfig("esp32-efuse", e[:8]); err == nil {
		t.Fatal("short data")
	}
	if _, err := DecodeConfig("nope", e); err == nil {
		t.Fatal("unknown kind")
	}
}

func TestParseMap(t *testing.T) {
	const m = `Archive member included to satisfy reference by file (symbol)

Memory Configuration

Linker script and memory map

 .isr_vector    0x08000000      0x10c startup.o
                0x08000000                g_pfnVectors
.text           0x0800010c      0x2a4
 .text          0x0800010c       0x40 main.o
                0x0800010c                main
                0x08000130                SysTick_Handler
 .text.Reset_Handler
                0x0800014c       0x50 startup.o
                0x0800014c                Reset_Handler
                0x08000200                . = ALIGN (0x4)
.data           0x20000000        0x4 load address 0x080003b0
 .data          0x20000000        0x4 main.o
                0x20000000                counter
`
	syms := ParseMap(strings.NewReader(m))
	want := []Symbol{
		{Name: "g_pfnVectors", Addr: 0x08000000, Func: true},
		{Name: "main", Addr: 0x0800010c, Func: true},
		{Name: "SysTick_Handler", Addr: 0x08000130, Func: true},
		{Name: "Reset_Handler", Addr: 0x0800014c, Func: true},
		{Name: "counter", Addr: 0x20000000},
	}
	if !reflect.DeepEqual(syms, want) {
		t.Fatalf("got %+v\nwant %+v", syms, want)
	}
}

// ihex writes Intel HEX records of data at addr (for the tests)
func ihex(parts map[uint32][]byte) []byte {
	var b strings.Builder
	rec := func(typ byte, addr uint16, data []byte) {
		sum := byte(len(data)) + byte(addr>>8) + byte(addr) + typ
		fmt.Fprintf(&b, ":%02X%04X%02X", len(data), addr, typ)
		for _, c := range data {
			fmt.Fprintf(&b, "%02X", c)
			sum += c
		}
		fmt.Fprintf(&b, "%02X\n", byte(-sum))
	}
	for addr, data := range parts {
		for i := 0; i < len(data); i += 16 {
			a := addr + uint32(i)
			rec(4, 0, []byte{byte(a >> 24), byte(a >> 16)})
			rec(0, uint16(a), data[i:min(i+16, len(data))])
		}
	}
	rec(1, 0, nil)
	return []byte(b.String())
}

// avrRjmp is an rjmp at from to the byte address to
func avrRjmp(from, to int) []byte {
	k := (to/2 - from/2 - 1) & 0xFFF
	w := 0xC000 | k
	return []byte{byte(w), byte(w >> 8)}
}

// avrCode is a little AVR code: ldi, out, rjmp to itself
func avrCode(at int) []byte {
	return append([]byte{0x0F, 0xEF, 0x04, 0xB9}, avrRjmp(at+4, at+4)...)
}

func TestDetectAVRReal(t *testing.T) {
	t.Run("rjmp and nop", func(t *testing.T) {
		// LUFA firmwares of the 16U2: jmp vectors relaxed to rjmp + nop
		var code []byte
		for i := 0; i < 29; i++ {
			code = append(code, avrRjmp(i*4, 0x80)...)
			code = append(code, 0, 0)
		}
		for len(code) < 0x80 {
			code = append(code, 0, 0)
		}
		code = append(code, avrCode(0x80)...)
		gs := Detect(open(t, ihex(map[uint32][]byte{0: code}), "fw.hex"))
		if len(gs) == 0 || gs[0].Arch != "avr" || len(gs[0].Vectors) != 29 || gs[0].Vectors[3].Addr != 0x80 {
			t.Fatalf("%+v", gs)
		}
	})
	t.Run("ATtiny to the bootloader", func(t *testing.T) {
		// Gemma: 8 rjmp at 0 to a bootloader at 0x14C0, wrapping around 8 KB
		var vec []byte
		for i := 0; i < 8; i++ {
			k := 0x14C0/2 - i - 1 - 4096
			w := 0xC000 | k&0xFFF
			vec = append(vec, byte(w), byte(w>>8))
		}
		gs := Detect(open(t, ihex(map[uint32][]byte{0: vec, 0x14C0: avrCode(0x14C0)}), "fw.hex"))
		if len(gs) == 0 || gs[0].Arch != "avr" || len(gs[0].Vectors) != 8 || gs[0].Entry != 0x14C0 {
			t.Fatalf("%+v", gs)
		}
	})
	t.Run("bootloader table", func(t *testing.T) {
		// Caterina: the table of the bootloader at 0x7000
		var code []byte
		for i := 0; i < 43; i++ {
			w := 0x7100 / 2
			code = append(code, 0x0C, 0x94, byte(w), byte(w>>8))
		}
		for len(code) < 0x100 {
			code = append(code, 0, 0)
		}
		code = append(code, avrCode(0x7100)...)
		gs := Detect(open(t, ihex(map[uint32][]byte{0x7000: code}), "fw.hex"))
		if len(gs) == 0 || gs[0].Arch != "avr" || gs[0].Base != 0x7000 || len(gs[0].Vectors) != 43 || gs[0].Score < 0.8 {
			t.Fatalf("%+v", gs)
		}
	})
}
