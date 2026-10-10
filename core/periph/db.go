package periph

import (
	"bytes"
	"compress/flate"
	"embed"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// The embedded database; see gen/encode.go for the format.
//
//go:embed data/*.bin
var dataFS embed.FS

const formatVersion = 1

var errCorrupt = errors.New("periph: corrupt database")

type reader struct {
	b   []byte
	err error
}

func (r *reader) u() uint64 {
	if r.err != nil {
		return 0
	}
	v, n := binary.Uvarint(r.b)
	if n <= 0 {
		r.err = errCorrupt
		return 0
	}
	r.b = r.b[n:]
	return v
}

func (r *reader) i() int64 {
	if r.err != nil {
		return 0
	}
	v, n := binary.Varint(r.b)
	if n <= 0 {
		r.err = errCorrupt
		return 0
	}
	r.b = r.b[n:]
	return v
}

// n reads a count, bounded by the remaining input to reject corrupt data.
func (r *reader) n() int {
	v := r.u()
	if v > uint64(len(r.b))+1 {
		r.err = errCorrupt
		return 0
	}
	return int(v)
}

func (r *reader) s() string {
	n := r.u()
	if r.err != nil {
		return ""
	}
	if n > uint64(len(r.b)) {
		r.err = errCorrupt
		return ""
	}
	s := string(r.b[:n])
	r.b = r.b[n:]
	return s
}

func (r *reader) magic(m string) {
	if len(r.b) < len(m) || string(r.b[:len(m)]) != m {
		r.err = errCorrupt
		return
	}
	r.b = r.b[len(m):]
	if v := r.u(); r.err == nil && v != formatVersion {
		r.err = fmt.Errorf("periph: unsupported database version %d", v)
	}
}

func inflate(name string) ([]byte, error) {
	z, err := dataFS.ReadFile("data/" + name)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(flate.NewReader(bytes.NewReader(z)))
}

type part struct {
	pattern    string // normalised
	flash, ram uint64
}

type indexEntry struct {
	info  DeviceInfo
	parts []part
	chunk int
	index int
	norm  string   // normalised name
	npats []string // normalised patterns
}

var (
	indexOnce sync.Once
	indexErr  error
	chunks    []string
	entries   []*indexEntry
)

func loadIndex() error {
	indexOnce.Do(func() {
		b, err := inflate("index.bin")
		if err != nil {
			indexErr = err
			return
		}
		r := &reader{b: b}
		r.magic("APIX")
		nc := r.n()
		for i := 0; i < nc && r.err == nil; i++ {
			chunks = append(chunks, r.s())
		}
		nd := r.n()
		for i := 0; i < nd && r.err == nil; i++ {
			e := &indexEntry{}
			e.info.Name = r.s()
			e.info.Vendor = r.s()
			e.info.Family = r.s()
			e.info.Arch = r.s()
			e.info.Core = r.s()
			e.info.Description = r.s()
			e.info.Source = r.s()
			np := r.n()
			for j := 0; j < np && r.err == nil; j++ {
				p := r.s()
				e.info.Patterns = append(e.info.Patterns, p)
				e.npats = append(e.npats, normalize(p))
			}
			nparts := r.n()
			for j := 0; j < nparts && r.err == nil; j++ {
				e.parts = append(e.parts, part{pattern: normalize(r.s()), flash: r.u(), ram: r.u()})
			}
			e.chunk = int(r.u())
			e.index = int(r.u())
			e.norm = normalize(e.info.Name)
			if e.chunk >= len(chunks) {
				r.err = errCorrupt
			}
			entries = append(entries, e)
		}
		indexErr = r.err
	})
	return indexErr
}

// Devices lists the embedded devices (cheap: no register data is loaded).
func Devices() []DeviceInfo {
	if loadIndex() != nil {
		return nil
	}
	out := make([]DeviceInfo, len(entries))
	for i, e := range entries {
		out[i] = e.info
	}
	return out
}

// layoutReg is a register of a peripheral layout, relative to the base.
type layoutReg struct {
	name, desc string
	offset     uint64
	bits       int
	reset      uint64
	dim        int
	stride     uint64
	idx        []string
	fields     []Field
}

type chunkData struct {
	layouts []([]layoutReg)
	devs    []byte // undecoded device records (positioned reader)
	strs    []string
	ndev    int
}

func decodeChunk(ci int) (*chunkData, error) {
	if ci < 0 || ci >= len(chunks) {
		return nil, errCorrupt
	}
	b, err := inflate(chunks[ci])
	if err != nil {
		return nil, err
	}
	r := &reader{b: b}
	r.magic("APCH")
	c := &chunkData{}
	ns := r.n()
	c.strs = make([]string, 0, ns)
	for i := 0; i < ns && r.err == nil; i++ {
		c.strs = append(c.strs, r.s())
	}
	str := func() string {
		v := r.u()
		if v >= uint64(len(c.strs)) {
			r.err = errCorrupt
			return ""
		}
		return c.strs[v]
	}
	nl := r.n()
	for i := 0; i < nl && r.err == nil; i++ {
		nr := r.n()
		regs := make([]layoutReg, 0, nr)
		for j := 0; j < nr && r.err == nil; j++ {
			lr := layoutReg{name: str(), desc: str(), offset: r.u(), bits: int(r.u()), reset: r.u(), dim: int(r.u())}
			if lr.dim > 1<<16 {
				r.err = errCorrupt
			}
			if lr.dim > 1 {
				lr.stride = r.u()
				ni := r.n()
				for k := 0; k < ni && r.err == nil; k++ {
					lr.idx = append(lr.idx, str())
				}
				if ni != 0 && ni != lr.dim {
					r.err = errCorrupt
				}
			}
			nf := r.n()
			if nf > 0 {
				lr.fields = make([]Field, 0, nf)
			}
			for k := 0; k < nf && r.err == nil; k++ {
				f := Field{Name: str(), Description: str(), Bit: int(r.u()), Bits: int(r.u()), Mask: r.u()}
				nv := r.n()
				for m := 0; m < nv && r.err == nil; m++ {
					f.Values = append(f.Values, EnumValue{Name: str(), Description: str(), Value: r.u()})
				}
				lr.fields = append(lr.fields, f)
			}
			regs = append(regs, lr)
		}
		c.layouts = append(c.layouts, regs)
	}
	c.ndev = r.n()
	c.devs = r.b
	return c, r.err
}

// Load loads a device by its name ("STM32F103xx", "ATmega328P") or by a part
// number matched against the part-number patterns ("STM32F103C8T6",
// "esp32-s3", "atmega328p-au"); for a part number the Flash/RAM sizes of the
// part are applied where known.
func Load(name string) (*Device, error) {
	if err := loadIndex(); err != nil {
		return nil, err
	}
	e, partName := resolve(name)
	if e == nil {
		return nil, fmt.Errorf("periph: unknown device %q", name)
	}
	d, err := loadEntry(e)
	if err != nil {
		return nil, err
	}
	if partName != "" {
		d.Part = partName
		applyPart(d, e, normalize(partName))
	}
	return d, nil
}

func resolve(name string) (*indexEntry, string) {
	for _, e := range entries {
		if strings.EqualFold(e.info.Name, name) {
			return e, ""
		}
	}
	q := normalize(name)
	if q == "" {
		return nil, ""
	}
	for _, e := range entries {
		if e.norm == q {
			return e, ""
		}
	}
	best, bestScore := (*indexEntry)(nil), 0
	for _, e := range entries {
		if s := patternScore(e, q); s > bestScore {
			best, bestScore = e, s
		}
	}
	if best == nil {
		return nil, ""
	}
	return best, strings.ToUpper(strings.TrimSpace(name))
}

func applyPart(d *Device, e *indexEntry, q string) {
	var best *part
	for i := range e.parts {
		p := &e.parts[i]
		if globPrefix(p.pattern, q) && (best == nil || len(p.pattern) > len(best.pattern)) {
			best = p
		}
	}
	if best == nil {
		return
	}
	if r, ok := d.Region("flash"); ok && best.flash > 0 {
		r.Size = best.flash
	}
	if r, ok := d.Region("ram"); ok && best.ram > 0 {
		r.Size = best.ram
	}
}

func loadEntry(e *indexEntry) (*Device, error) {
	cache := map[int]*chunkData{}
	get := func(ci int) (*chunkData, error) {
		if c, ok := cache[ci]; ok {
			return c, nil
		}
		c, err := decodeChunk(ci)
		if err != nil {
			return nil, err
		}
		cache[ci] = c
		return c, nil
	}
	c, err := get(e.chunk)
	if err != nil {
		return nil, err
	}
	if e.index >= c.ndev {
		return nil, errCorrupt
	}
	r := &reader{b: c.devs}
	str := func() string {
		v := r.u()
		if v >= uint64(len(c.strs)) {
			r.err = errCorrupt
			return ""
		}
		return c.strs[v]
	}
	d := &Device{
		Name: e.info.Name, Vendor: e.info.Vendor, Family: e.info.Family,
		Arch: e.info.Arch, Core: e.info.Core, Description: e.info.Description,
	}
	type pref struct{ chunk, index int }
	var prefs []pref
	for k := 0; k <= e.index && r.err == nil; k++ {
		d.Memory, d.Interrupts, d.Peripherals, prefs = d.Memory[:0], d.Interrupts[:0], d.Peripherals[:0], prefs[:0]
		_ = str() // name
		d.LoadAddr = r.u()
		nm := r.n()
		for i := 0; i < nm && r.err == nil; i++ {
			d.Memory = append(d.Memory, Region{Name: str(), Start: r.u(), Size: r.u(), Kind: str(), Space: str()})
		}
		ni := r.n()
		for i := 0; i < ni && r.err == nil; i++ {
			d.Interrupts = append(d.Interrupts, Interrupt{Number: int(r.i()), Name: str(), Description: str()})
		}
		np := r.n()
		for i := 0; i < np && r.err == nil; i++ {
			d.Peripherals = append(d.Peripherals, Peripheral{Name: str(), Description: str(), Group: str(), Base: r.u(), Size: r.u()})
			prefs = append(prefs, pref{int(r.u()), int(r.u())})
		}
	}
	if r.err != nil {
		return nil, r.err
	}
	// copy the slices so that the reused backing arrays are not shared
	d.Memory = append([]Region(nil), d.Memory...)
	d.Interrupts = append([]Interrupt(nil), d.Interrupts...)
	d.Peripherals = append([]Peripheral(nil), d.Peripherals...)
	if d.Arch == "arm" && strings.HasPrefix(d.Core, "Cortex-M") {
		d.Interrupts = append(cortexExceptions(d.Core), d.Interrupts...)
	}
	sort.SliceStable(d.Interrupts, func(i, j int) bool { return d.Interrupts[i].Number < d.Interrupts[j].Number })

	// expand the layouts at the peripheral bases
	for i, p := range d.Peripherals {
		lc, err := get(prefs[i].chunk)
		if err != nil {
			return nil, err
		}
		if prefs[i].index >= len(lc.layouts) {
			return nil, errCorrupt
		}
		style := byte(0)
		if d.Arch == "avr" {
			// classic AVR: plain avr-libc names ("PORTB"); AVR8X: "PORTA.OUT"
			style = 2
			if d.Core != "AVR8X" {
				style = 1
			}
		}
		for _, lr := range lc.layouts[prefs[i].index] {
			n := lr.dim
			if n < 1 {
				n = 1
			}
			for k := 0; k < n; k++ {
				name := lr.name
				if lr.dim > 1 {
					ix := strconv.Itoa(k)
					if lr.idx != nil {
						ix = lr.idx[k]
					}
					name = strings.ReplaceAll(name, "%s", ix)
				}
				d.regs = append(d.regs, &Register{
					Peripheral: p.Name, Name: name, Description: lr.desc,
					Addr: p.Base + lr.offset + uint64(k)*lr.stride, Size: lr.bits,
					Reset: lr.reset, Fields: lr.fields, style: style,
				})
			}
		}
	}
	sort.SliceStable(d.regs, func(i, j int) bool { return d.regs[i].Addr < d.regs[j].Addr })
	d.maxEnd = make([]uint64, len(d.regs))
	var m uint64
	for i, rg := range d.regs {
		m = max(m, rg.End())
		d.maxEnd[i] = m
	}
	return d, nil
}
