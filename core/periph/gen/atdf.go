package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"math/bits"
	"sort"
	"strings"
)

// Microchip ATDF (AVR) parsing. Register addresses are data-space addresses
// (for classic AVR: I/O address + 0x20), the way the disassembler reports
// in/out accesses.

type atdfFile struct {
	Devices []atdfDevice `xml:"devices>device"`
	Modules []atdfModule `xml:"modules>module"`
}

type atdfDevice struct {
	Name          string `xml:"name,attr"`
	Architecture  string `xml:"architecture,attr"`
	Family        string `xml:"family,attr"`
	AddressSpaces []struct {
		ID       string `xml:"id,attr"`
		Name     string `xml:"name,attr"`
		Segments []struct {
			Name  string `xml:"name,attr"`
			Start string `xml:"start,attr"`
			Size  string `xml:"size,attr"`
			Type  string `xml:"type,attr"`
		} `xml:"memory-segment"`
	} `xml:"address-spaces>address-space"`
	Modules []struct {
		Name      string `xml:"name,attr"`
		Instances []struct {
			Name    string `xml:"name,attr"`
			Caption string `xml:"caption,attr"`
			Groups  []struct {
				Name         string `xml:"name,attr"`
				NameInModule string `xml:"name-in-module,attr"`
				Offset       string `xml:"offset,attr"`
				AddressSpace string `xml:"address-space,attr"`
				Caption      string `xml:"caption,attr"`
			} `xml:"register-group"`
		} `xml:"instance"`
	} `xml:"peripherals>module"`
	Interrupts []struct {
		Index          string `xml:"index,attr"`
		Name           string `xml:"name,attr"`
		Caption        string `xml:"caption,attr"`
		ModuleInstance string `xml:"module-instance,attr"`
	} `xml:"interrupts>interrupt"`
}

type atdfModule struct {
	Name        string           `xml:"name,attr"`
	Caption     string           `xml:"caption,attr"`
	Groups      []atdfGroup      `xml:"register-group"`
	ValueGroups []atdfValueGroup `xml:"value-group"`
}

type atdfGroup struct {
	Name         string `xml:"name,attr"`
	NameInModule string `xml:"name-in-module,attr"`
	Caption      string `xml:"caption,attr"`
	Offset       string `xml:"offset,attr"`
	Size         string `xml:"size,attr"`
	Count        string `xml:"count,attr"`
	Registers    []struct {
		Name     string `xml:"name,attr"`
		Caption  string `xml:"caption,attr"`
		Offset   string `xml:"offset,attr"`
		Size     string `xml:"size,attr"`
		Initval  string `xml:"initval,attr"`
		Modes    string `xml:"modes,attr"`
		Count    string `xml:"count,attr"`
		Bitfield []struct {
			Name    string `xml:"name,attr"`
			Caption string `xml:"caption,attr"`
			Mask    string `xml:"mask,attr"`
			Values  string `xml:"values,attr"`
		} `xml:"bitfield"`
	} `xml:"register"`
	Groups []atdfGroup `xml:"register-group"`
}

type atdfValueGroup struct {
	Name   string `xml:"name,attr"`
	Values []struct {
		Name    string `xml:"name,attr"`
		Caption string `xml:"caption,attr"`
		Value   string `xml:"value,attr"`
	} `xml:"value"`
}

func maskField(mask uint64) (bit, n int, m uint64) {
	if mask == 0 {
		return 0, 0, 0
	}
	bit = bits.TrailingZeros64(mask)
	top := 63 - bits.LeadingZeros64(mask)
	n = top - bit + 1
	if bits.OnesCount64(mask) != n {
		m = mask // non-contiguous
	}
	return
}

func atdfGroupRegs(mod *atdfModule, g *atdfGroup, base uint64, prefix string, out *[]reg, depth int) {
	if depth > 4 {
		return
	}
	vgs := map[string]*atdfValueGroup{}
	for i := range mod.ValueGroups {
		vgs[mod.ValueGroups[i].Name] = &mod.ValueGroups[i]
	}
	for _, r := range g.Registers {
		size := int(num(r.Size, 1))
		if size < 1 || size > 8 {
			size = 1
		}
		name := r.Name
		if r.Modes != "" && !strings.Contains(r.Modes, " ") {
			name = r.Modes + "." + name
		}
		rg := reg{
			Name:   prefix + name,
			Desc:   clean(r.Caption),
			Offset: base + num(r.Offset, 0),
			Bits:   size * 8,
			Reset:  num(r.Initval, 0),
			Dim:    1,
		}
		if c := int(num(r.Count, 1)); c > 1 && c <= 1024 {
			rg.Dim, rg.Stride, rg.Name = c, uint64(size), rg.Name+"[%s]"
		}
		for _, bf := range r.Bitfield {
			m, ok := parseNum(bf.Mask)
			if !ok || m == 0 {
				continue
			}
			bit, n, mk := maskField(m)
			f := field{Name: bf.Name, Desc: clean(bf.Caption), Bit: bit, Bits: n, Mask: mk}
			if vg := vgs[bf.Values]; vg != nil {
				for _, v := range vg.Values {
					if x, ok := parseNum(v.Value); ok {
						f.Values = append(f.Values, enumValue{Name: v.Name, Desc: clean(v.Caption), Value: x})
					}
				}
			}
			rg.Fields = append(rg.Fields, f)
		}
		if len(rg.Fields) == 0 && size == 1 && (mod.Name == "PORT" || mod.Name == "VPORT") && !strings.Contains(r.Name, "CTRL") {
			rg.Fields = portPins(r.Name)
		}
		sort.SliceStable(rg.Fields, func(i, j int) bool { return rg.Fields[i].Bit < rg.Fields[j].Bit })
		*out = append(*out, rg)
	}
	// nested register groups referencing other groups of the module
	for _, sub := range g.Groups {
		ref := findGroup(mod, sub.NameInModule)
		if ref == nil || ref == g {
			continue
		}
		cnt := int(num(sub.Count, 1))
		if cnt < 1 || cnt > 64 {
			cnt = 1
		}
		size := num(sub.Size, num(ref.Size, 0))
		for k := 0; k < cnt; k++ {
			p := prefix + sub.Name
			if cnt > 1 {
				p += fmt.Sprintf("[%d]", k)
			}
			atdfGroupRegs(mod, ref, base+num(sub.Offset, 0)+uint64(k)*size, p+".", out, depth+1)
		}
	}
}

func findGroup(m *atdfModule, name string) *atdfGroup {
	for i := range m.Groups {
		if m.Groups[i].Name == name {
			return &m.Groups[i]
		}
	}
	return nil
}

func segKind(t, name string) string {
	switch t {
	case "flash", "ram", "eeprom", "fuses", "lockbits", "signatures", "user_signatures", "osccal", "regs":
		return t
	case "io":
		return "periph"
	case "other":
		if strings.Contains(name, "PROGMEM") {
			return "flash"
		}
	}
	return "other"
}

func parseATDF(data []byte, d *device) error {
	var af atdfFile
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	if err := dec.Decode(&af); err != nil {
		return fmt.Errorf("atdf: %v", err)
	}
	if len(af.Devices) == 0 {
		return fmt.Errorf("atdf: no device")
	}
	ad := &af.Devices[0]
	if d.Name == "" {
		d.Name = ad.Name
	}
	if d.Core == "" {
		d.Core = ad.Architecture
	}
	if d.Description == "" {
		d.Description = ad.Family
	}
	for _, as := range ad.AddressSpaces {
		if as.ID == "io" {
			continue
		}
		for _, s := range as.Segments {
			if strings.HasPrefix(s.Name, "BOOT_SECTION") {
				continue
			}
			d.Memory = append(d.Memory, region{
				Name: s.Name, Start: num(s.Start, 0), Size: num(s.Size, 0),
				Kind: segKind(s.Type, s.Name), Space: as.ID,
			})
		}
	}
	mods := map[string]*atdfModule{}
	for i := range af.Modules {
		mods[af.Modules[i].Name] = &af.Modules[i]
	}
	for _, m := range ad.Modules {
		mod := mods[m.Name]
		if mod == nil {
			continue
		}
		for _, in := range m.Instances {
			for _, g := range in.Groups {
				if g.AddressSpace != "" && g.AddressSpace != "data" {
					continue // fuses/lockbits of classic AVR live in their own spaces
				}
				gn := g.NameInModule
				if gn == "" {
					gn = g.Name
				}
				grp := findGroup(mod, gn)
				if grp == nil {
					continue
				}
				l := &layout{}
				atdfGroupRegs(mod, grp, 0, "", &l.Regs, 0)
				if len(l.Regs) == 0 {
					continue
				}
				sort.SliceStable(l.Regs, func(i, j int) bool { return l.Regs[i].Offset < l.Regs[j].Offset })
				name := g.Name
				if name == "" {
					name = in.Name
				}
				desc := in.Caption
				if desc == "" {
					desc = grp.Caption
				}
				if desc == "" {
					desc = mod.Caption
				}
				base := num(g.Offset, 0)
				lo, hi := ^uint64(0), uint64(0)
				for _, r := range l.Regs {
					n := uint64(r.Dim)
					lo = min(lo, r.Offset)
					hi = max(hi, r.Offset+(n-1)*r.Stride+uint64(r.Bits/8))
				}
				d.Periphs = append(d.Periphs, periphInst{
					Name: name, Desc: clean(desc), Group: m.Name,
					Base: base, Size: hi, Layout: l,
				})
				if base != 0 {
					d.Memory = append(d.Memory, region{Name: name, Start: base + lo, Size: hi - lo, Kind: "periph", Space: "data"})
				}
			}
		}
	}
	seen := map[int]bool{}
	for _, q := range ad.Interrupts {
		v, ok := parseNum(q.Index)
		if !ok || seen[int(v)] {
			continue
		}
		seen[int(v)] = true
		name := q.Name
		if q.ModuleInstance != "" && !strings.HasPrefix(name, q.ModuleInstance) {
			name = q.ModuleInstance + "_" + name
		}
		d.Interrupts = append(d.Interrupts, interrupt{Number: int(v), Name: name, Desc: clean(q.Caption)})
	}
	sort.Slice(d.Interrupts, func(i, j int) bool { return d.Interrupts[i].Number < d.Interrupts[j].Number })
	return nil
}

// portPins synthesises the per-pin bit fields of an I/O port register (the
// ATDF files describe port registers without fields): PORTB0.., DDB0..,
// PINB0.. (avr-libc names) for classic AVR, PIN0.. for AVR8X.
func portPins(reg string) []field {
	prefix := "PIN"
	for _, p := range []string{"PORT", "DDR", "PIN"} {
		if len(reg) == len(p)+1 && strings.HasPrefix(reg, p) {
			port := reg[len(p):]
			switch p {
			case "DDR":
				prefix = "DD" + port
			default:
				prefix = p + port
			}
		}
	}
	out := make([]field, 8)
	for i := range out {
		out[i] = field{Name: fmt.Sprintf("%s%d", prefix, i), Desc: fmt.Sprintf("Pin %d", i), Bit: i, Bits: 1}
	}
	return out
}
