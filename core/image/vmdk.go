package diskimg

import (
	"bufio"
	"bytes"
	"compress/flate"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// VMDK (VMware Virtual Disk Format 5.0): a sparse extent starts with a
// header ("KDMV"); the grain directory points to grain tables, which point
// to grains of data. In a stream-optimized extent the grains are
// compressed (each with a marker: the sector and the size) and the header
// in force is the footer near the end. A descriptor is text naming the
// extents: sparse or flat files next to it, or zeros. Numbers are
// little-endian, offsets are in sectors.

const (
	vmdkMagic          = 0x564D444B // "KDMV"
	vmdkFlagCompressed = 1 << 16
	vmdkGDAtEnd        = 0xFFFFFFFFFFFFFFFF
)

type vmdkMapper struct {
	r          io.ReaderAt
	size       int64 // of the file
	capacity   int64 // bytes
	grain      int64 // bytes
	gtes       int64 // entries per grain table
	gdOff      int64
	gdN        int64
	compressed bool
	pages      *pager
	grains     *lru[[]byte] // decoded compressed grains
}

// vmdkSparse reads the header of a sparse extent
func vmdkSparse(r io.ReaderAt, size int64) (*vmdkMapper, []byte, error) {
	const format = "VMDK"
	h := make([]byte, 512)
	if err := readFull(r, h, 0); err != nil {
		return nil, nil, err
	}
	if le32(h) != vmdkMagic {
		return nil, nil, errCorrupt(format, "no sparse header")
	}
	if le64(h[56:]) == vmdkGDAtEnd {
		// Stream-optimized: the grain directory was written last, and the
		// footer before the end-of-stream marker says where
		if size < 1536 {
			return nil, nil, errCorrupt(format, "no footer")
		}
		if err := readFull(r, h, size-1024); err != nil {
			return nil, nil, err
		}
		if le32(h) != vmdkMagic || le64(h[56:]) == vmdkGDAtEnd {
			return nil, nil, errCorrupt(format, "no footer")
		}
	}
	if v := le32(h[4:]); v < 1 || v > 3 {
		return nil, nil, errCorrupt(format, "the version %d", v)
	}
	flags := le32(h[8:])
	capacity, grain := le64(h[12:]), le64(h[20:])
	gtes := int64(le32(h[44:]))
	m := &vmdkMapper{r: r, size: size, gtes: gtes, compressed: flags&vmdkFlagCompressed != 0}
	maxGrain := uint64(1 << 16)
	if m.compressed {
		// A decoded grain is kept in memory
		maxGrain = 2048
		if alg := le16(h[77:]); alg != 1 {
			return nil, nil, fmt.Errorf("%s: the compression %d is not supported", format, alg)
		}
	}
	if !isPow2(int64(grain)) || grain > maxGrain {
		return nil, nil, errCorrupt(format, "the grain size %d", grain)
	}
	if capacity > 1<<41 {
		return nil, nil, errCorrupt(format, "the capacity %d", capacity)
	}
	if gtes < 1 || gtes > 1<<16 {
		return nil, nil, errCorrupt(format, "%d entries per grain table", gtes)
	}
	m.capacity, m.grain = int64(capacity)*512, int64(grain)*512
	m.gdN = ceilDiv(ceilDiv(int64(capacity), int64(grain)), gtes)
	gd := le64(h[56:])
	if gd > uint64(size)/512 || int64(gd)*512+m.gdN*4 > size {
		return nil, nil, errCorrupt(format, "the grain directory is outside the file")
	}
	m.gdOff = int64(gd) * 512
	m.pages = newPager(r, size, binary.LittleEndian)
	m.grains = newLRU[[]byte](max(4, int(8<<20/m.grain)))
	// The embedded descriptor
	var desc []byte
	if do, ds := le64(h[28:]), le64(h[36:]); do > 0 && ds > 0 && ds <= 2048 && do <= uint64(size)/512 {
		desc = make([]byte, min(int64(ds)*512, size-int64(do)*512))
		if err := readFull(r, desc, int64(do)*512); err != nil {
			return nil, nil, err
		}
	}
	return m, desc, nil
}

func openVMDKSparse(r io.ReaderAt, size int64) (*disk, error) {
	m, desc, err := vmdkSparse(r, size)
	if err != nil {
		return nil, err
	}
	if desc != nil {
		if d, err := parseVMDKDescriptor(desc); err == nil && d.parent != "" {
			return nil, &ParentError{Format: "VMDK", Parent: d.parent}
		}
	}
	format := "VMDK (sparse)"
	if m.compressed {
		format = "VMDK (stream-optimized)"
	}
	return &disk{m: m, size: m.capacity, format: format}, nil
}

func (m *vmdkMapper) mapAt(off int64) (run, error) {
	g := off / m.grain
	in := off % m.grain
	n := m.grain - in
	if off >= m.capacity {
		return run{n: 1 << 62}, nil
	}
	gt, err := m.pages.u32(m.gdOff + g/m.gtes*4)
	if err != nil {
		return run{}, err
	}
	if gt == 0 {
		// No grain table: the grains of all of it are zeros
		return run{n: (g/m.gtes+1)*m.gtes*m.grain - off}, nil
	}
	gte, err := m.pages.u32(int64(gt)*512 + g%m.gtes*4)
	if err != nil {
		return run{}, err
	}
	if gte <= 1 {
		// 0 - not allocated, 1 - a zeroed grain
		return run{n: n}, nil
	}
	at := int64(gte) * 512
	if !m.compressed {
		return run{n: n, r: m.r, off: at + in}, nil
	}
	data, err := m.grains.get(at, func() ([]byte, error) { return m.decode(at) })
	if err != nil {
		return run{}, err
	}
	return run{n: n, data: data[in:]}, nil
}

// decode reads a compressed grain: its marker and the zlib data
func (m *vmdkMapper) decode(at int64) ([]byte, error) {
	var mk [12]byte
	if err := readFull(m.r, mk[:], at); err != nil {
		return nil, err
	}
	n := int64(le32(mk[8:]))
	if n == 0 || n > 2*m.grain+4096 || n > m.size-at-12 {
		return nil, errCorrupt("VMDK", "a compressed grain of %d bytes at %d", n, at)
	}
	src := make([]byte, n)
	if err := readFull(m.r, src, at+12); err != nil {
		return nil, err
	}
	return inflate(src, int(m.grain), true)
}

// inflate decodes deflate data into size bytes; what the data does not
// fill is zeros. withHeader - a zlib stream (a raw deflate one is taken too).
func inflate(src []byte, size int, withHeader bool) ([]byte, error) {
	var zr io.Reader
	if withHeader {
		if z, err := zlib.NewReader(bytes.NewReader(src)); err == nil {
			zr = z
		}
	}
	if zr == nil {
		zr = flate.NewReader(bytes.NewReader(src))
	}
	out := make([]byte, size)
	done := 0
	for done < size {
		k, err := zr.Read(out[done:])
		done += k
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("compressed data: %w", err)
		}
	}
	return out, nil
}

// vmdkDescriptor is what the text descriptor says
type vmdkDescriptor struct {
	createType string
	parent     string
	extents    []vmdkExtent
}

type vmdkExtent struct {
	access  string // RW, RDONLY, NOACCESS
	sectors int64
	kind    string // FLAT, SPARSE, ZERO, VMFS...
	file    string
	offset  int64 // sectors, for FLAT
}

func isVMDKDescriptor(h []byte) bool {
	s := string(h)
	return strings.HasPrefix(s, "# Disk DescriptorFile") ||
		(strings.Contains(s, "createType=") && strings.Contains(s, "version=") && !bytes.ContainsRune(h, 0))
}

func parseVMDKDescriptor(text []byte) (*vmdkDescriptor, error) {
	if i := bytes.IndexByte(text, 0); i >= 0 {
		text = text[:i]
	}
	d := &vmdkDescriptor{}
	parentCID := ""
	sc := bufio.NewScanner(bytes.NewReader(text))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok && !strings.ContainsAny(k, " \t\"") {
			v = strings.Trim(strings.TrimSpace(v), "\"")
			switch strings.TrimSpace(k) {
			case "createType":
				d.createType = v
			case "parentFileNameHint":
				d.parent = v
			case "parentCID":
				parentCID = strings.ToLower(v)
			}
			continue
		}
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		switch f[0] {
		case "RW", "RDONLY", "NOACCESS":
		default:
			continue
		}
		e, err := parseVMDKExtent(line)
		if err != nil {
			return nil, err
		}
		d.extents = append(d.extents, e)
	}
	if parentCID == "ffffffff" {
		d.parent = ""
	} else if parentCID != "" && d.parent == "" {
		d.parent = "unknown (parentCID " + parentCID + ")"
	}
	return d, nil
}

// parseVMDKExtent reads `RW 2048 FLAT "name.vmdk" 0`
func parseVMDKExtent(line string) (vmdkExtent, error) {
	var e vmdkExtent
	bad := fmt.Errorf("VMDK: damaged: the extent %q", line)
	f := strings.Fields(line)
	if len(f) < 3 {
		return e, bad
	}
	e.access, e.kind = f[0], f[2]
	n, err := strconv.ParseInt(f[1], 10, 64)
	if err != nil || n < 0 || n > 1<<41 {
		return e, bad
	}
	e.sectors = n
	if e.kind == "ZERO" {
		return e, nil
	}
	q1 := strings.IndexByte(line, '"')
	q2 := strings.LastIndexByte(line, '"')
	if q1 < 0 || q2 <= q1 {
		return e, bad
	}
	e.file = line[q1+1 : q2]
	if rest := strings.Fields(line[q2+1:]); len(rest) > 0 {
		e.offset, err = strconv.ParseInt(rest[0], 10, 64)
		if err != nil || e.offset < 0 || e.offset > 1<<41 {
			return e, bad
		}
	}
	return e, nil
}

func openVMDKDescriptor(r io.ReaderAt, size int64, o *opener) (*disk, error) {
	const format = "VMDK"
	if size > 1<<20 {
		return nil, errCorrupt(format, "a descriptor of %d bytes", size)
	}
	text := make([]byte, size)
	if err := readFull(r, text, 0); err != nil {
		return nil, err
	}
	d, err := parseVMDKDescriptor(text)
	if err != nil {
		return nil, err
	}
	if d.parent != "" {
		return nil, &ParentError{Format: format, Parent: d.parent}
	}
	if len(d.extents) == 0 {
		return nil, errCorrupt(format, "no extents")
	}
	c := &concatMapper{}
	var total int64
	for _, e := range d.extents {
		var m mapper
		switch e.kind {
		case "ZERO":
			m = zeroMapper{}
		case "FLAT", "VMFS":
			if e.access == "NOACCESS" {
				m = zeroMapper{}
				break
			}
			f, _, err := o.open(e.file)
			if err != nil {
				return nil, err
			}
			m = flatMapper{r: f, base: e.offset * 512}
		case "SPARSE":
			if e.access == "NOACCESS" {
				m = zeroMapper{}
				break
			}
			f, fsize, err := o.open(e.file)
			if err != nil {
				return nil, err
			}
			sm, _, err := vmdkSparse(f, fsize)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", e.file, err)
			}
			m = sm
		default:
			return nil, fmt.Errorf("%s: the extent type %s is not supported", format, e.kind)
		}
		if e.sectors == 0 {
			continue
		}
		c.starts = append(c.starts, total)
		total += e.sectors * 512
		c.ends = append(c.ends, total)
		c.parts = append(c.parts, m)
	}
	if total > 1<<50 {
		return nil, errCorrupt(format, "the size %d", total)
	}
	name := "VMDK (descriptor)"
	if d.createType != "" {
		name = "VMDK (" + d.createType + ")"
	}
	return &disk{m: c, size: total, format: name}, nil
}
