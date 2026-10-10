package periph

import "fmt"

// RegionLabel names an address by the memory region it is in, for the
// accesses that reach no register (FW-2): "SRAM+0x1C", "Flash" at the start
// of the region; "" when no region of the device holds it. A region of
// peripherals is named too ("GPIOA+0x400"), the registers being named by
// RegisterAt first.
func (d *Device) RegionLabel(addr uint64) string {
	r, ok := d.RegionAt(addr)
	if !ok {
		return ""
	}
	if addr == r.Start {
		return r.Name
	}
	return fmt.Sprintf("%s+0x%X", r.Name, addr-r.Start)
}

// MapRegions are the regions of the device that are in its address space
// (the unified one, or the AVR program and data spaces): the memory map shown
// to the user, sorted by address within each space
func (d *Device) MapRegions() []Region {
	var out []Region
	for _, r := range d.Memory {
		if r.Size == 0 {
			continue
		}
		out = append(out, r)
	}
	return out
}
