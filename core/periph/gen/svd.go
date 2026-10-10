package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// CMSIS-SVD parsing (the subset needed for register names, fields and
// enumerated values, interrupts and peripheral address blocks).

type svdDevice struct {
	Name        string `xml:"name"`
	Vendor      string `xml:"vendor"`
	Description string `xml:"description"`
	CPU         struct {
		Name string `xml:"name"`
	} `xml:"cpu"`
	Size        string          `xml:"size"`
	ResetValue  string          `xml:"resetValue"`
	Peripherals []svdPeripheral `xml:"peripherals>peripheral"`
}

type svdAddressBlock struct {
	Offset string `xml:"offset"`
	Size   string `xml:"size"`
}

type svdInterrupt struct {
	Name        string `xml:"name"`
	Description string `xml:"description"`
	Value       string `xml:"value"`
}

type svdPeripheral struct {
	DerivedFrom   string            `xml:"derivedFrom,attr"`
	Name          string            `xml:"name"`
	Description   string            `xml:"description"`
	GroupName     string            `xml:"groupName"`
	BaseAddress   string            `xml:"baseAddress"`
	Size          string            `xml:"size"`
	ResetValue    string            `xml:"resetValue"`
	Dim           string            `xml:"dim"`
	DimIncrement  string            `xml:"dimIncrement"`
	DimIndex      string            `xml:"dimIndex"`
	AddressBlocks []svdAddressBlock `xml:"addressBlock"`
	Interrupts    []svdInterrupt    `xml:"interrupt"`
	Registers     []svdRegister     `xml:"registers>register"`
	Clusters      []svdCluster      `xml:"registers>cluster"`
}

type svdCluster struct {
	DerivedFrom   string        `xml:"derivedFrom,attr"`
	Name          string        `xml:"name"`
	Description   string        `xml:"description"`
	AddressOffset string        `xml:"addressOffset"`
	Size          string        `xml:"size"`
	ResetValue    string        `xml:"resetValue"`
	Dim           string        `xml:"dim"`
	DimIncrement  string        `xml:"dimIncrement"`
	DimIndex      string        `xml:"dimIndex"`
	Registers     []svdRegister `xml:"register"`
	Clusters      []svdCluster  `xml:"cluster"`
}

type svdRegister struct {
	DerivedFrom   string     `xml:"derivedFrom,attr"`
	Name          string     `xml:"name"`
	Description   string     `xml:"description"`
	AddressOffset string     `xml:"addressOffset"`
	Size          string     `xml:"size"`
	ResetValue    string     `xml:"resetValue"`
	Dim           string     `xml:"dim"`
	DimIncrement  string     `xml:"dimIncrement"`
	DimIndex      string     `xml:"dimIndex"`
	Fields        []svdField `xml:"fields>field"`
}

type svdField struct {
	DerivedFrom      string          `xml:"derivedFrom,attr"`
	Name             string          `xml:"name"`
	Description      string          `xml:"description"`
	BitOffset        string          `xml:"bitOffset"`
	BitWidth         string          `xml:"bitWidth"`
	Lsb              string          `xml:"lsb"`
	Msb              string          `xml:"msb"`
	BitRange         string          `xml:"bitRange"`
	EnumeratedValues []svdEnumValues `xml:"enumeratedValues"`
}

type svdEnumValues struct {
	DerivedFrom string `xml:"derivedFrom,attr"`
	Name        string `xml:"name"`
	Values      []struct {
		Name        string `xml:"name"`
		Description string `xml:"description"`
		Value       string `xml:"value"`
		IsDefault   string `xml:"isDefault"`
	} `xml:"enumeratedValue"`
}

// parseNum parses an SVD/ATDF scaledNonNegativeInteger: decimal, 0x hex,
// #binary or 0b binary ('x' don't-care digits read as 0).
func parseNum(s string) (uint64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	base := 10
	switch {
	case strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X"):
		s, base = s[2:], 16
	case strings.HasPrefix(s, "#"):
		s, base = s[1:], 2
	case strings.HasPrefix(s, "0b") || strings.HasPrefix(s, "0B"):
		s, base = s[2:], 2
	}
	if base == 2 {
		s = strings.Map(func(r rune) rune {
			if r == 'x' || r == 'X' {
				return '0'
			}
			return r
		}, s)
	}
	mul := uint64(1)
	if base == 10 && len(s) > 1 {
		switch s[len(s)-1] {
		case 'k', 'K':
			mul, s = 1<<10, s[:len(s)-1]
		case 'm', 'M':
			mul, s = 1<<20, s[:len(s)-1]
		}
	}
	v, err := strconv.ParseUint(s, base, 64)
	if err != nil {
		return 0, false
	}
	return v * mul, true
}

func num(s string, def uint64) uint64 {
	if v, ok := parseNum(s); ok {
		return v
	}
	return def
}

// dimIndices expands an SVD dimIndex ("0-7", "A-D", "A,B,C"); nil means 0..dim-1.
func dimIndices(spec string, dim int) []string {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil
	}
	if a, b, ok := strings.Cut(spec, "-"); ok && !strings.Contains(spec, ",") {
		if x, err1 := strconv.Atoi(a); err1 == nil {
			if y, err2 := strconv.Atoi(b); err2 == nil && y >= x && y-x+1 == dim {
				if x == 0 {
					return nil
				}
				var out []string
				for i := x; i <= y; i++ {
					out = append(out, strconv.Itoa(i))
				}
				return out
			}
		}
		if len(a) == 1 && len(b) == 1 && b[0] >= a[0] && int(b[0]-a[0])+1 == dim {
			var out []string
			for c := a[0]; c <= b[0]; c++ {
				out = append(out, string(c))
			}
			return out
		}
	}
	parts := strings.Split(spec, ",")
	if len(parts) != dim {
		return nil
	}
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	// plain 0..n-1 needs no list
	plain := true
	for i, p := range parts {
		if p != strconv.Itoa(i) {
			plain = false
			break
		}
	}
	if plain {
		return nil
	}
	return parts
}

func idxName(idx []string, k int) string {
	if idx == nil {
		return strconv.Itoa(k)
	}
	return idx[k]
}

type svdProps struct {
	size  int
	reset uint64
}

func (p svdProps) with(size, reset string) svdProps {
	if v, ok := parseNum(size); ok && v > 0 && v <= 64 {
		p.size = int(v)
	}
	if v, ok := parseNum(reset); ok {
		p.reset = v
	}
	return p
}

type svdCtx struct {
	enums map[string][]enumValue // enumeratedValues by name (for derivedFrom)
	regs  map[string]*svdRegister
}

func lastPath(s string) string {
	if i := strings.LastIndexByte(s, '.'); i >= 0 {
		return s[i+1:]
	}
	return s
}

func (c *svdCtx) fields(r *svdRegister, width int) []field {
	var out []field
	byName := map[string]*svdField{}
	for i := range r.Fields {
		byName[r.Fields[i].Name] = &r.Fields[i]
	}
	for _, f := range r.Fields {
		if f.DerivedFrom != "" {
			if b := byName[lastPath(f.DerivedFrom)]; b != nil && b != &f {
				if f.Description == "" {
					f.Description = b.Description
				}
				if len(f.EnumeratedValues) == 0 {
					f.EnumeratedValues = b.EnumeratedValues
				}
				if f.BitOffset == "" && f.Lsb == "" && f.BitRange == "" {
					f.BitOffset, f.BitWidth, f.Lsb, f.Msb, f.BitRange = b.BitOffset, b.BitWidth, b.Lsb, b.Msb, b.BitRange
				}
			}
		}
		bit, bits := 0, 1
		switch {
		case f.BitRange != "":
			s := strings.Trim(strings.TrimSpace(f.BitRange), "[]")
			a, b, _ := strings.Cut(s, ":")
			msb, _ := strconv.Atoi(strings.TrimSpace(a))
			lsb, _ := strconv.Atoi(strings.TrimSpace(b))
			bit, bits = lsb, msb-lsb+1
		case f.Lsb != "":
			lsb, msb := int(num(f.Lsb, 0)), int(num(f.Msb, 0))
			bit, bits = lsb, msb-lsb+1
		default:
			bit, bits = int(num(f.BitOffset, 0)), int(num(f.BitWidth, 1))
		}
		if bits < 1 || bit < 0 || bit+bits > 64 {
			continue
		}
		fl := field{Name: f.Name, Desc: clean(f.Description), Bit: bit, Bits: bits}
		seen := map[string]bool{}
		for _, ev := range f.EnumeratedValues {
			vals := c.enumValues(ev)
			if ev.Name != "" && ev.DerivedFrom == "" {
				c.enums[ev.Name] = vals
			}
			for _, v := range vals {
				k := v.Name + "\x00" + strconv.FormatUint(v.Value, 10)
				if !seen[k] {
					seen[k] = true
					fl.Values = append(fl.Values, v)
				}
			}
		}
		out = append(out, fl)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Bit < out[j].Bit })
	return out
}

func (c *svdCtx) enumValues(ev svdEnumValues) []enumValue {
	if ev.DerivedFrom != "" && len(ev.Values) == 0 {
		return c.enums[lastPath(ev.DerivedFrom)]
	}
	var out []enumValue
	for _, v := range ev.Values {
		if v.Value == "" || strings.EqualFold(v.IsDefault, "true") {
			continue
		}
		x, ok := parseNum(v.Value)
		if !ok {
			continue
		}
		out = append(out, enumValue{Name: v.Name, Desc: clean(v.Description), Value: x})
	}
	return out
}

func (c *svdCtx) register(r svdRegister, base uint64, prefix string, props svdProps, out *[]reg) {
	if r.DerivedFrom != "" {
		if b := c.regs[lastPath(r.DerivedFrom)]; b != nil {
			if r.Description == "" {
				r.Description = b.Description
			}
			if len(r.Fields) == 0 {
				r.Fields = b.Fields
			}
			if r.Size == "" {
				r.Size = b.Size
			}
			if r.ResetValue == "" {
				r.ResetValue = b.ResetValue
			}
		}
	}
	p := props.with(r.Size, r.ResetValue)
	rg := reg{
		Name:   prefix + r.Name,
		Desc:   clean(r.Description),
		Offset: base + num(r.AddressOffset, 0),
		Bits:   p.size,
		Reset:  p.reset,
		Dim:    1,
		Fields: c.fields(&r, p.size),
	}
	if d := int(num(r.Dim, 1)); d > 1 {
		rg.Dim = d
		rg.Stride = num(r.DimIncrement, uint64(p.size/8))
		rg.DimIdx = dimIndices(r.DimIndex, d)
		if !strings.Contains(rg.Name, "%s") {
			rg.Name += "%s"
		}
	} else {
		rg.Name = strings.ReplaceAll(rg.Name, "[%s]", "")
		rg.Name = strings.ReplaceAll(rg.Name, "%s", "")
	}
	*out = append(*out, rg)
}

func (c *svdCtx) cluster(cl svdCluster, base uint64, prefix string, props svdProps, out *[]reg, clusters map[string]*svdCluster, depth int) {
	if depth > 8 {
		return
	}
	if cl.DerivedFrom != "" {
		if b := clusters[lastPath(cl.DerivedFrom)]; b != nil && len(cl.Registers) == 0 && len(cl.Clusters) == 0 {
			cl.Registers, cl.Clusters = b.Registers, b.Clusters
			if cl.Description == "" {
				cl.Description = b.Description
			}
		}
	}
	p := props.with(cl.Size, cl.ResetValue)
	off := base + num(cl.AddressOffset, 0)
	n := int(num(cl.Dim, 1))
	if n < 1 {
		n = 1
	}
	if n > 1024 {
		n = 1024
	}
	inc := num(cl.DimIncrement, 0)
	idx := dimIndices(cl.DimIndex, n)
	for k := 0; k < n; k++ {
		name := cl.Name
		if n > 1 || strings.Contains(name, "%s") {
			name = strings.ReplaceAll(name, "%s", idxName(idx, k))
		}
		pre := prefix + name + "."
		for _, r := range cl.Registers {
			c.register(r, off+uint64(k)*inc, pre, p, out)
		}
		for _, sc := range cl.Clusters {
			c.cluster(sc, off+uint64(k)*inc, pre, p, out, clusters, depth+1)
		}
	}
}

func collectNames(regs []svdRegister, cls []svdCluster, rm map[string]*svdRegister, cm map[string]*svdCluster) {
	for i := range regs {
		rm[regs[i].Name] = &regs[i]
	}
	for i := range cls {
		cm[cls[i].Name] = &cls[i]
		collectNames(cls[i].Registers, cls[i].Clusters, rm, cm)
	}
}

// parseSVD converts an SVD file into a device (without the hand-written data).
func parseSVD(data []byte, d *device) error {
	var sd svdDevice
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	if err := dec.Decode(&sd); err != nil {
		return fmt.Errorf("svd: %v", err)
	}
	if d.Name == "" {
		d.Name = sd.Name
	}
	if d.Description == "" {
		d.Description = clean(sd.Description)
	}
	if d.Core == "" {
		d.Core = cpuName(sd.CPU.Name)
	}
	devProps := svdProps{size: 32}.with(sd.Size, sd.ResetValue)
	byName := map[string]*svdPeripheral{}
	for i := range sd.Peripherals {
		byName[sd.Peripherals[i].Name] = &sd.Peripherals[i]
	}
	// resolve derivedFrom chains
	var resolve func(p *svdPeripheral, depth int) *svdPeripheral
	resolve = func(p *svdPeripheral, depth int) *svdPeripheral {
		if p.DerivedFrom == "" || depth > 8 {
			return p
		}
		b := byName[lastPath(p.DerivedFrom)]
		if b == nil || b == p {
			return p
		}
		return resolve(b, depth+1)
	}
	layouts := map[*svdPeripheral]*layout{}
	irqs := map[int]interrupt{}
	for i := range sd.Peripherals {
		p := &sd.Peripherals[i]
		for _, q := range p.Interrupts {
			v, ok := parseNum(q.Value)
			if !ok {
				continue
			}
			if _, dup := irqs[int(v)]; !dup {
				irqs[int(v)] = interrupt{Number: int(v), Name: q.Name, Desc: clean(q.Description)}
			}
		}
		src := p
		if len(p.Registers) == 0 && len(p.Clusters) == 0 {
			src = resolve(p, 0)
		}
		desc := p.Description
		if desc == "" {
			desc = resolve(p, 0).Description
		}
		group := p.GroupName
		if group == "" {
			group = resolve(p, 0).GroupName
		}
		blocks := p.AddressBlocks
		if len(blocks) == 0 {
			blocks = resolve(p, 0).AddressBlocks
		}
		l := layouts[src]
		if l == nil {
			ctx := &svdCtx{enums: map[string][]enumValue{}, regs: map[string]*svdRegister{}}
			cm := map[string]*svdCluster{}
			collectNames(src.Registers, src.Clusters, ctx.regs, cm)
			pp := devProps.with(src.Size, src.ResetValue)
			l = &layout{}
			for _, r := range src.Registers {
				ctx.register(r, 0, "", pp, &l.Regs)
			}
			for _, cl := range src.Clusters {
				ctx.cluster(cl, 0, "", pp, &l.Regs, cm, 0)
			}
			sort.SliceStable(l.Regs, func(i, j int) bool { return l.Regs[i].Offset < l.Regs[j].Offset })
			layouts[src] = l
		}
		base := num(p.BaseAddress, 0)
		size := uint64(0)
		for _, b := range blocks {
			if e := num(b.Offset, 0) + num(b.Size, 0); e > size {
				size = e
			}
		}
		if size == 0 {
			for _, r := range l.Regs {
				n := uint64(r.Dim)
				if n < 1 {
					n = 1
				}
				if e := r.Offset + (n-1)*r.Stride + uint64(r.Bits/8); e > size {
					size = e
				}
			}
		}
		n := int(num(p.Dim, 1))
		if n < 1 || n > 256 {
			n = 1
		}
		inc := num(p.DimIncrement, size)
		idx := dimIndices(p.DimIndex, n)
		for k := 0; k < n; k++ {
			name := p.Name
			if n > 1 {
				name = strings.ReplaceAll(strings.ReplaceAll(name, "[%s]", "%s"), "%s", idxName(idx, k))
			}
			d.Periphs = append(d.Periphs, periphInst{
				Name: name, Desc: clean(desc), Group: group,
				Base: base + uint64(k)*inc, Size: size, Layout: l,
			})
		}
	}
	for _, q := range irqs {
		d.Interrupts = append(d.Interrupts, q)
	}
	sort.Slice(d.Interrupts, func(i, j int) bool { return d.Interrupts[i].Number < d.Interrupts[j].Number })
	sort.SliceStable(d.Periphs, func(i, j int) bool { return d.Periphs[i].Base < d.Periphs[j].Base })
	for _, p := range d.Periphs {
		if p.Size > 0 {
			d.Memory = append(d.Memory, region{Name: p.Name, Start: p.Base, Size: p.Size, Kind: "periph"})
		}
	}
	return nil
}

func cpuName(s string) string {
	u := strings.ToUpper(strings.TrimSpace(s))
	switch u {
	case "CM0":
		return "Cortex-M0"
	case "CM0PLUS", "CM0+":
		return "Cortex-M0+"
	case "CM1":
		return "Cortex-M1"
	case "CM3":
		return "Cortex-M3"
	case "CM4":
		return "Cortex-M4"
	case "CM7":
		return "Cortex-M7"
	case "CM23":
		return "Cortex-M23"
	case "CM33":
		return "Cortex-M33"
	case "CM55":
		return "Cortex-M55"
	}
	return strings.TrimSpace(s)
}
