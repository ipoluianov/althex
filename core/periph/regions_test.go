package periph

import "testing"

func TestRegionLabel(t *testing.T) {
	d := &Device{Memory: []Region{
		{Name: "Flash", Start: 0x08000000, Size: 0x10000, Kind: "flash"},
		{Name: "SRAM", Start: 0x20000000, Size: 0x5000, Kind: "ram"},
		{Name: "PERIPH", Start: 0x40000000, Size: 0x20000000, Kind: "periph"},
		{Name: "GPIOA", Start: 0x40010800, Size: 0x400, Kind: "periph"},
		{Name: "EEPROM", Start: 0, Size: 0x400, Kind: "eeprom", Space: "eeprom"},
	}}
	for _, c := range []struct {
		addr uint64
		want string
	}{
		{0x20000000, "SRAM"},
		{0x2000001C, "SRAM+0x1C"},
		{0x08000100, "Flash+0x100"},
		{0x40010804, "GPIOA+0x4"}, // the smallest region holding it
		{0x40000010, "PERIPH+0x10"},
		{0x20005000, ""}, // past the end of SRAM
		{0x10, ""},       // only in another address space
	} {
		if got := d.RegionLabel(c.addr); got != c.want {
			t.Errorf("RegionLabel(%#x) = %q, want %q", c.addr, got, c.want)
		}
	}
}

func TestRegionLabelReal(t *testing.T) {
	d := mustLoad(t, "STM32F103xx")
	if got := d.RegionLabel(0x2000001C); got != "SRAM+0x1C" {
		t.Errorf("STM32F103 SRAM: %q", got)
	}
	if len(d.MapRegions()) == 0 {
		t.Error("no memory map")
	}
}
