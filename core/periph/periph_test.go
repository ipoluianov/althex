package periph

import (
	"io/fs"
	"math/rand"
	"strings"
	"testing"
	"time"
)

func mustLoad(t testing.TB, name string) *Device {
	t.Helper()
	d, err := Load(name)
	if err != nil {
		t.Fatalf("Load(%q): %v", name, err)
	}
	return d
}

func regAt(t *testing.T, d *Device, addr uint64, label string) *Register {
	t.Helper()
	r, ok := d.RegisterAt(addr)
	if !ok {
		t.Fatalf("%s: no register at %#x (want %s)", d.Name, addr, label)
	}
	if r.Label() != label {
		t.Fatalf("%s: register at %#x is %s @%#x, want %s", d.Name, addr, r.Label(), r.Addr, label)
	}
	return r
}

func field(t *testing.T, r *Register, name string) *Field {
	t.Helper()
	for i := range r.Fields {
		if r.Fields[i].Name == name {
			return &r.Fields[i]
		}
	}
	t.Fatalf("%s: no field %s", r.Label(), name)
	return nil
}

func TestDatabaseSize(t *testing.T) {
	var total int64
	files := 0
	fs.WalkDir(dataFS, "data", func(p string, e fs.DirEntry, err error) error {
		if err == nil && !e.IsDir() {
			fi, _ := e.Info()
			total += fi.Size()
			files++
		}
		return nil
	})
	t.Logf("embedded database: %d files, %d bytes (%.2f MB), %d devices", files, total, float64(total)/(1<<20), len(Devices()))
	if total > 12<<20 {
		t.Errorf("embedded database is %d bytes, over the 12 MB budget", total)
	}
}

func TestAllDevicesLoad(t *testing.T) {
	devs := Devices()
	if len(devs) < 100 {
		t.Fatalf("only %d devices", len(devs))
	}
	names := map[string]bool{}
	nregs := 0
	for _, info := range devs {
		if names[info.Name] {
			t.Errorf("duplicate device name %s", info.Name)
		}
		names[info.Name] = true
		switch info.Arch {
		case "arm", "avr", "xtensa", "riscv32":
		default:
			t.Errorf("%s: unexpected arch %q", info.Name, info.Arch)
		}
		d, err := Load(info.Name)
		if err != nil {
			t.Errorf("Load(%s): %v", info.Name, err)
			continue
		}
		if d.Part != "" {
			t.Errorf("%s: Load by exact name set Part %q", info.Name, d.Part)
		}
		regs := d.Registers()
		nregs += len(regs)
		if len(regs) == 0 || len(d.Peripherals) == 0 || len(d.Interrupts) == 0 {
			t.Errorf("%s: %d registers, %d peripherals, %d interrupts", d.Name, len(regs), len(d.Peripherals), len(d.Interrupts))
		}
		if _, ok := d.Region("flash"); !ok {
			t.Errorf("%s: no flash region", d.Name)
		}
		if _, ok := d.Region("ram"); !ok {
			t.Errorf("%s: no ram region", d.Name)
		}
		for i, r := range regs {
			if i > 0 && regs[i-1].Addr > r.Addr {
				t.Fatalf("%s: registers not sorted", d.Name)
			}
			if r.Name == "" || r.Size == 0 {
				t.Errorf("%s: bad register %+v", d.Name, r)
			}
			got, ok := d.RegisterAt(r.Addr)
			if !ok || got.Addr > r.Addr || got.End() <= r.Addr {
				t.Fatalf("%s: RegisterAt(%#x) = %v, %v", d.Name, r.Addr, got, ok)
			}
			for _, f := range r.Fields {
				if f.Bits < 1 || f.Bit+f.Bits > 64 {
					t.Errorf("%s: %s.%s bad bit range %d/%d", d.Name, r.Label(), f.Name, f.Bit, f.Bits)
				}
			}
		}
	}
	t.Logf("%d devices, %d registers", len(devs), nregs)
}

func TestSTM32F103(t *testing.T) {
	d := mustLoad(t, "STM32F103xx")
	if d.Arch != "arm" || d.Core != "Cortex-M3" || d.LoadAddr != 0x08000000 || d.Family != "STM32F1" {
		t.Fatalf("device: %+v", d.Name)
	}
	r := regAt(t, d, 0x40021018, "RCC->APB2ENR")
	f := field(t, r, "IOPAEN")
	if f.Bit != 2 || f.Bits != 1 {
		t.Errorf("IOPAEN at %d/%d", f.Bit, f.Bits)
	}
	regAt(t, d, 0x4002101A, "RCC->APB2ENR") // inside the register
	regAt(t, d, 0x4001080C, "GPIOA->ODR")
	regAt(t, d, 0x40010C0C, "GPIOB->ODR")
	if s := r.DecodeString(1<<2|1<<14, true); s != "IOPAEN=1, USART1EN=1" {
		t.Errorf("DecodeString = %q", s)
	}
	var found bool
	for _, fv := range r.Decode(1 << 2) {
		if fv.Field.Name == "IOPAEN" {
			found = fv.Value == 1 && fv.Text == "IOPAEN=1"
		}
	}
	if !found {
		t.Errorf("Decode: IOPAEN not decoded")
	}
	if p, ok := d.PeripheralAt(0x40021018); !ok || p.Name != "RCC" || p.Base != 0x40021000 {
		t.Errorf("PeripheralAt = %+v", p)
	}
	if q, ok := d.Vector(16); !ok || q.Name != "WWDG" {
		t.Errorf("vector 16 = %+v", q)
	}
	if q, ok := d.Vector(15); !ok || q.Name != "SysTick" {
		t.Errorf("vector 15 = %+v", q)
	}
	if q, ok := d.Interrupt(37); !ok || q.Name != "USART1" {
		t.Errorf("IRQ 37 = %+v", q)
	}
	if r, ok := d.RegisterByName("rcc->apb2enr"); !ok || r.Addr != 0x40021018 {
		t.Errorf("RegisterByName")
	}
	// part-number resolution with the per-part memory sizes
	p := mustLoad(t, "STM32F103C8T6")
	if p.Name != "STM32F103xx" || p.Part != "STM32F103C8T6" {
		t.Fatalf("part load: %s / %s", p.Name, p.Part)
	}
	fl, _ := p.Region("flash")
	ram, _ := p.Region("ram")
	if fl.Start != 0x08000000 || fl.Size != 64<<10 || ram.Start != 0x20000000 || ram.Size != 20<<10 {
		t.Errorf("STM32F103C8 memory: flash %+v ram %+v", fl, ram)
	}
	p = mustLoad(t, "stm32f103rbt6")
	if fl, _ := p.Region("flash"); fl.Size != 128<<10 {
		t.Errorf("STM32F103RB flash %d", fl.Size)
	}
}

func TestATmega328P(t *testing.T) {
	d := mustLoad(t, "ATmega328P")
	if d.Arch != "avr" || d.Family != "AVR" || d.Core != "AVR8" {
		t.Fatalf("device: %s %s %s", d.Arch, d.Family, d.Core)
	}
	r := regAt(t, d, 0x25, "PORTB") // I/O 0x05
	if len(r.Fields) != 8 || field(t, r, "PORTB2").Bit != 2 {
		t.Errorf("PORTB fields: %+v", r.Fields)
	}
	regAt(t, d, 0x24, "DDRB")
	regAt(t, d, 0x5F, "SREG")
	regAt(t, d, 0x85, "TCNT1") // high byte of a 16-bit register
	if f := field(t, regAt(t, d, 0x5F, "SREG"), "I"); f.Bit != 7 {
		t.Errorf("SREG.I bit %d", f.Bit)
	}
	// WDTCSR.WDP is the non-contiguous mask 0x27 (WDP3 is bit 5)
	w := regAt(t, d, 0x60, "WDTCSR")
	wdp := field(t, w, "WDP")
	if wdp.Mask != 0x27 || wdp.Extract(0x21) != 9 {
		t.Errorf("WDP mask %#x extract %d", wdp.Mask, wdp.Extract(0x21))
	}
	if s := w.DecodeString(0x21, true); !strings.Contains(s, "WDP=9") {
		t.Errorf("WDTCSR decode %q", s)
	}
	for n, name := range map[int]string{0: "RESET", 1: "INT0", 2: "INT1", 16: "TIMER0_OVF", 18: "USART_RX"} {
		if q, ok := d.Vector(n); !ok || q.Name != name {
			t.Errorf("vector %d = %+v, want %s", n, q, name)
		}
	}
	want := map[string]Region{
		"FLASH":  {Name: "FLASH", Start: 0, Size: 0x8000, Kind: "flash", Space: "prog"},
		"IRAM":   {Name: "IRAM", Start: 0x100, Size: 0x800, Kind: "ram", Space: "data"},
		"EEPROM": {Name: "EEPROM", Start: 0, Size: 0x400, Kind: "eeprom", Space: "eeprom"},
		"FUSES":  {Name: "FUSES", Start: 0, Size: 3, Kind: "fuses", Space: "fuses"},
	}
	for _, m := range d.Memory {
		if w, ok := want[m.Name]; ok {
			if m != w {
				t.Errorf("region %+v, want %+v", m, w)
			}
			delete(want, m.Name)
		}
	}
	if len(want) != 0 {
		t.Errorf("missing regions %v", want)
	}
	if fl, _ := d.Region("flash"); fl.Space != "prog" {
		t.Errorf("first flash region %+v", fl)
	}
}

func TestAVR8X(t *testing.T) {
	d := mustLoad(t, "ATmega4809")
	regAt(t, d, 0x404, "PORTA.OUT")
	regAt(t, d, 0x01, "VPORTA.OUT")
	r := regAt(t, d, 0xA00, "TCA0.SINGLE.CTRLA")
	if s := r.DecodeString(0x0F, true); s != "ENABLE=1, CLKSEL=7 (System Clock / 1024)" {
		t.Errorf("decode %q", s)
	}
	if q, ok := d.Vector(3); !ok || q.Name != "RTC_CNT" {
		t.Errorf("vector 3 = %+v", q)
	}
}

func TestESP32(t *testing.T) {
	d := mustLoad(t, "ESP32")
	if d.Arch != "xtensa" {
		t.Errorf("arch %s", d.Arch)
	}
	regAt(t, d, 0x3FF44004, "GPIO->OUT")
	if r, ok := d.RegionAt(0x3FFB0000); !ok || r.Kind != "ram" {
		t.Errorf("RegionAt DRAM = %+v", r)
	}
	c3 := mustLoad(t, "esp32-c3")
	if c3.Name != "ESP32-C3" || c3.Arch != "riscv32" {
		t.Errorf("esp32-c3 -> %s %s", c3.Name, c3.Arch)
	}
}

func TestRP2040(t *testing.T) {
	d := mustLoad(t, "RP2040")
	p, ok := d.PeripheralAt(0xD0000000)
	if !ok || p.Name != "SIO" || p.Base != 0xD0000000 {
		t.Fatalf("SIO: %+v", p)
	}
	regAt(t, d, 0xD0000010, "SIO->GPIO_OUT")
	if d.LoadAddr != 0x10000000 || d.Core != "Cortex-M0+" {
		t.Errorf("load %#x core %s", d.LoadAddr, d.Core)
	}
	// ARMv6-M has no MemManage/BusFault/UsageFault
	if _, ok := d.Interrupt(-12); ok {
		t.Errorf("Cortex-M0+ has MemoryManagement")
	}
}

func TestSearch(t *testing.T) {
	for q, want := range map[string]string{
		"stm32f103c8":   "STM32F103xx",
		"STM32F407VGT6": "STM32F407",
		"stm32h743zi":   "STM32H742/743",
		"atmega328p-au": "ATmega328P",
		"ATmega328":     "ATmega328",
		"attiny45":      "ATtiny85",
		"esp32s3":       "ESP32-S3",
		"ESP32":         "ESP32",
		"nrf52840":      "nRF52840",
		"rp2040":        "RP2040",
		"ATSAMD21G18A":  "ATSAMD21",
		"LPC1768":       "LPC176x/5x",
		"gd32vf103cbt6": "GD32VF103",
		"ch32v307":      "CH32V30x",
	} {
		res := Search(q)
		if len(res) == 0 || res[0].Name != want {
			names := []string{}
			for i, r := range res {
				if i < 5 {
					names = append(names, r.Name)
				}
			}
			t.Errorf("Search(%q) = %v, want %s first", q, names, want)
		}
	}
	f103 := map[string]bool{}
	for _, r := range Search("f103") {
		f103[r.Name] = true
	}
	if !f103["STM32F103xx"] || !f103["GD32VF103"] {
		t.Errorf("Search(f103) = %v", f103)
	}
	if len(Search("stm32")) < 50 {
		t.Errorf("Search(stm32) found %d", len(Search("stm32")))
	}
	if len(Search("nonexistent-part-xyz")) != 0 {
		t.Errorf("unexpected match")
	}
	if _, err := Load("nonexistent-part-xyz"); err == nil {
		t.Errorf("Load of an unknown part succeeded")
	}
}

func TestRegisterAtPerformance(t *testing.T) {
	d := mustLoad(t, "STM32H742/743")
	regs := d.Registers()
	rnd := rand.New(rand.NewSource(1))
	addrs := make([]uint64, 4096)
	for i := range addrs {
		if i%2 == 0 {
			addrs[i] = regs[rnd.Intn(len(regs))].Addr + uint64(rnd.Intn(4))
		} else {
			addrs[i] = 0x40000000 + uint64(rnd.Intn(0x20000000))
		}
	}
	const n = 1_000_000
	start := time.Now()
	hits := 0
	for i := 0; i < n; i++ {
		if _, ok := d.RegisterAt(addrs[i&4095]); ok {
			hits++
		}
	}
	el := time.Since(start)
	t.Logf("%d RegisterAt lookups over %d registers: %v (%.0f ns/op), %d hits", n, len(regs), el, float64(el.Nanoseconds())/n, hits)
	if el > 2*time.Second {
		t.Errorf("RegisterAt too slow: %v", el)
	}
}

func BenchmarkRegisterAt(b *testing.B) {
	d := mustLoad(b, "STM32H742/743")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		d.RegisterAt(0x58024400 + uint64(i&0xFF))
	}
}

func BenchmarkLoad(b *testing.B) {
	for i := 0; i < b.N; i++ {
		mustLoad(b, "STM32F407")
	}
}
