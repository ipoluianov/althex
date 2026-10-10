package main

import (
	"bytes"
	"compress/flate"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Database format (version 1). All integers are unsigned LEB128 varints
// (interrupt numbers are zig-zag signed varints); every file is raw DEFLATE.
//
// index.bin:
//   "APIX" ver
//   nchunks { fileName }
//   ndevices { name vendor family arch core description source
//              npatterns{pattern} nparts{pattern flash ram} chunk indexInChunk }
//   (strings inline as len+bytes)
//
// <chunk>.bin (one per family):
//   "APCH" ver
//   nstrings { len bytes }                      -- the chunk string table
//   nlayouts { layout }                         -- strings are table indices
//   ndevices { name loadAddr
//              nmem { name start size kind space }
//              nirq { svarint(number) name desc }
//              nperiph { name desc group base size layoutChunk layoutIndex } }
//
// layout: nregs { name desc offset bits reset dim [stride nidx{idx}] nfields
//                 { name desc bit bits mask nvalues { name desc value } } }
//
// Layouts (the register blocks of a peripheral relative to its base) are
// deduplicated by content across all devices and stored in the chunk of the
// first device that uses them; a device may reference layouts of other chunks.

const formatVersion = 1

type wbuf struct{ bytes.Buffer }

func (w *wbuf) u(v uint64) {
	var b [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(b[:], v)
	w.Write(b[:n])
}
func (w *wbuf) i(v int64) {
	var b [binary.MaxVarintLen64]byte
	n := binary.PutVarint(b[:], v)
	w.Write(b[:n])
}
func (w *wbuf) s(v string) { w.u(uint64(len(v))); w.WriteString(v) }

type strtab struct {
	idx  map[string]int
	list []string
}

func (t *strtab) id(s string) uint64 {
	if t.idx == nil {
		t.idx = map[string]int{}
	}
	if i, ok := t.idx[s]; ok {
		return uint64(i)
	}
	t.idx[s] = len(t.list)
	t.list = append(t.list, s)
	return uint64(len(t.list) - 1)
}

func layoutKey(l *layout) string {
	var w wbuf
	w.u(uint64(len(l.Regs)))
	for _, r := range l.Regs {
		w.s(r.Name)
		w.s(r.Desc)
		w.u(r.Offset)
		w.u(uint64(r.Bits))
		w.u(r.Reset)
		w.u(uint64(r.Dim))
		w.u(r.Stride)
		w.u(uint64(len(r.DimIdx)))
		for _, x := range r.DimIdx {
			w.s(x)
		}
		w.u(uint64(len(r.Fields)))
		for _, f := range r.Fields {
			w.s(f.Name)
			w.s(f.Desc)
			w.u(uint64(f.Bit))
			w.u(uint64(f.Bits))
			w.u(f.Mask)
			w.u(uint64(len(f.Values)))
			for _, v := range f.Values {
				w.s(v.Name)
				w.s(v.Desc)
				w.u(v.Value)
			}
		}
	}
	h := sha256.Sum256(w.Bytes())
	return string(h[:])
}

func encodeLayout(w *wbuf, t *strtab, l *layout) {
	w.u(uint64(len(l.Regs)))
	for _, r := range l.Regs {
		w.u(t.id(r.Name))
		w.u(t.id(r.Desc))
		w.u(r.Offset)
		w.u(uint64(r.Bits))
		w.u(r.Reset)
		w.u(uint64(r.Dim))
		if r.Dim > 1 {
			w.u(r.Stride)
			w.u(uint64(len(r.DimIdx)))
			for _, x := range r.DimIdx {
				w.u(t.id(x))
			}
		}
		w.u(uint64(len(r.Fields)))
		for _, f := range r.Fields {
			w.u(t.id(f.Name))
			w.u(t.id(f.Desc))
			w.u(uint64(f.Bit))
			w.u(uint64(f.Bits))
			w.u(f.Mask)
			w.u(uint64(len(f.Values)))
			for _, v := range f.Values {
				w.u(t.id(v.Name))
				w.u(t.id(v.Desc))
				w.u(v.Value)
			}
		}
	}
}

func chunkName(family string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(family) {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			b.WriteRune(c)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String() + ".bin"
}

func deflate(data []byte) []byte {
	var out bytes.Buffer
	fw, _ := flate.NewWriter(&out, flate.BestCompression)
	fw.Write(data)
	fw.Close()
	return out.Bytes()
}

type chunk struct {
	name    string
	devices []*device
	layouts []*layout
}

// writeDB writes index.bin and the chunk files into dir; returns the total size.
func writeDB(dir string, devs []*device) (int64, error) {
	// assign chunks by family, in the order of first appearance
	var chunks []*chunk
	byName := map[string]*chunk{}
	devChunk := map[*device]int{}
	devIdx := map[*device]int{}
	for _, d := range devs {
		n := chunkName(d.Family)
		c := byName[n]
		if c == nil {
			c = &chunk{name: n}
			byName[n] = c
			chunks = append(chunks, c)
		}
		devChunk[d] = indexOf(chunks, c)
		devIdx[d] = len(c.devices)
		c.devices = append(c.devices, d)
	}
	// deduplicate layouts globally
	seen := map[string]*layout{}
	unique, total := 0, 0
	for _, d := range devs {
		ci := devChunk[d]
		for i := range d.Periphs {
			p := &d.Periphs[i]
			total++
			if p.Layout.key == "" {
				p.Layout.key = layoutKey(p.Layout)
			}
			if l, ok := seen[p.Layout.key]; ok {
				p.Layout = l
				continue
			}
			seen[p.Layout.key] = p.Layout
			p.Layout.chunk = ci
			p.Layout.index = len(chunks[ci].layouts)
			chunks[ci].layouts = append(chunks[ci].layouts, p.Layout)
			unique++
		}
	}
	fmt.Fprintf(os.Stderr, "peripheral instances: %d, unique layouts: %d\n", total, unique)

	// remove stale chunk files
	old, _ := filepath.Glob(filepath.Join(dir, "*.bin"))
	for _, f := range old {
		os.Remove(f)
	}
	var size int64
	for _, c := range chunks {
		var t strtab
		var body wbuf
		body.u(uint64(len(c.layouts)))
		for _, l := range c.layouts {
			encodeLayout(&body, &t, l)
		}
		body.u(uint64(len(c.devices)))
		for _, d := range c.devices {
			body.u(t.id(d.Name))
			body.u(d.LoadAddr)
			body.u(uint64(len(d.Memory)))
			for _, m := range d.Memory {
				body.u(t.id(m.Name))
				body.u(m.Start)
				body.u(m.Size)
				body.u(t.id(m.Kind))
				body.u(t.id(m.Space))
			}
			body.u(uint64(len(d.Interrupts)))
			for _, q := range d.Interrupts {
				body.i(int64(q.Number))
				body.u(t.id(q.Name))
				body.u(t.id(q.Desc))
			}
			body.u(uint64(len(d.Periphs)))
			for _, p := range d.Periphs {
				body.u(t.id(p.Name))
				body.u(t.id(p.Desc))
				body.u(t.id(p.Group))
				body.u(p.Base)
				body.u(p.Size)
				body.u(uint64(p.Layout.chunk))
				body.u(uint64(p.Layout.index))
			}
		}
		var w wbuf
		w.WriteString("APCH")
		w.u(formatVersion)
		w.u(uint64(len(t.list)))
		for _, s := range t.list {
			w.s(s)
		}
		w.Write(body.Bytes())
		z := deflate(w.Bytes())
		if err := os.WriteFile(filepath.Join(dir, c.name), z, 0o644); err != nil {
			return 0, err
		}
		fmt.Fprintf(os.Stderr, "  %-16s %3d devices %5d layouts %8d -> %7d bytes\n", c.name, len(c.devices), len(c.layouts), w.Len(), len(z))
		size += int64(len(z))
	}
	var w wbuf
	w.WriteString("APIX")
	w.u(formatVersion)
	w.u(uint64(len(chunks)))
	for _, c := range chunks {
		w.s(c.name)
	}
	w.u(uint64(len(devs)))
	for _, d := range devs {
		w.s(d.Name)
		w.s(d.Vendor)
		w.s(d.Family)
		w.s(d.Arch)
		w.s(d.Core)
		w.s(d.Description)
		w.s(d.Source)
		w.u(uint64(len(d.Patterns)))
		for _, p := range d.Patterns {
			w.s(p)
		}
		w.u(uint64(len(d.Parts)))
		for _, p := range d.Parts {
			w.s(p.Pattern)
			w.u(p.Flash)
			w.u(p.RAM)
		}
		w.u(uint64(devChunk[d]))
		w.u(uint64(devIdx[d]))
	}
	z := deflate(w.Bytes())
	if err := os.WriteFile(filepath.Join(dir, "index.bin"), z, 0o644); err != nil {
		return 0, err
	}
	size += int64(len(z))
	return size, nil
}

func indexOf(cs []*chunk, c *chunk) int {
	for i, x := range cs {
		if x == c {
			return i
		}
	}
	return -1
}

// sortRegions orders memory regions by space and address.
func sortRegions(rs []region) {
	order := func(s string) string {
		switch s {
		case "":
			return "0"
		case "prog":
			return "1"
		case "data":
			return "2"
		}
		return "3" + s
	}
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].Space != rs[j].Space {
			return order(rs[i].Space) < order(rs[j].Space)
		}
		return rs[i].Start < rs[j].Start
	})
}
