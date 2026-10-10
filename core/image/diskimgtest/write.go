// Package diskimgtest makes disk image containers of raw disk content, for
// the tests of core/image and of the templates. The writers are simple:
// they store what is not zeros and use the ways each format has to leave
// the rest out, so a reader is tested on all of them.
package diskimgtest

import (
	"bytes"
	"compress/flate"
	"compress/zlib"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"strings"
	"unicode/utf16"
)

var (
	be = binary.BigEndian
	le = binary.LittleEndian
)

// Raw makes disk content of size bytes (a multiple of 4096) with
// recognizable sectors, a zero area from 1 MB to 2.5 MB, zeros within a
// sector range at 512 KB + 1536, and a repeated 4-byte value at 3 MB
func Raw(size int) []byte {
	b := make([]byte, size)
	for s := 0; s*512 < size; s++ {
		sec := b[s*512 : (s+1)*512]
		for i := range sec {
			sec[i] = byte(i*31 + s*7 + 1)
		}
		be.PutUint64(sec, uint64(s))
	}
	zero := func(from, to int) {
		clear(b[min(from, size):min(to, size)])
	}
	zero(1<<20, 5<<19)
	zero(512<<10+1536, 512<<10+8192)
	for i := 3 << 20; i+4 <= min(size, 3<<20+64<<10); i += 4 {
		be.PutUint32(b[i:], 0xDEADBEEF)
	}
	return b
}

func isZero(b []byte) bool {
	for _, c := range b {
		if c != 0 {
			return false
		}
	}
	return true
}

// zeroRange tells whether raw[off:off+n] is zeros (or past its end)
func zeroRange(raw []byte, off, n int) bool {
	if off >= len(raw) {
		return true
	}
	return isZero(raw[off:min(len(raw), off+n)])
}

// part is raw[off:off+n] padded with zeros to n bytes
func part(raw []byte, off, n int) []byte {
	b := make([]byte, n)
	if off < len(raw) {
		copy(b, raw[off:])
	}
	return b
}

func align(v, a int) int { return (v + a - 1) / a * a }

// grow makes b at least n bytes long
func grow(b []byte, n int) []byte {
	if len(b) < n {
		b = append(b, make([]byte, n-len(b))...)
	}
	return b
}

// --- VHD ---

func vhdFooter(typ uint32, dataOff uint64, size uint64) []byte {
	f := make([]byte, 512)
	copy(f, "conectix")
	be.PutUint32(f[8:], 2)
	be.PutUint32(f[12:], 0x00010000)
	be.PutUint64(f[16:], dataOff)
	be.PutUint32(f[24:], 0x2E000000)
	copy(f[28:], "ahex")
	be.PutUint32(f[32:], 0x00010000)
	be.PutUint32(f[36:], 0x5769326B) // Wi2k
	be.PutUint64(f[40:], size)
	be.PutUint64(f[48:], size)
	be.PutUint16(f[56:], uint16(min(size/512/16/63, 65535)))
	f[58], f[59] = 16, 63
	be.PutUint32(f[60:], typ)
	for i := range 16 {
		f[68+i] = byte(i*17 + 3)
	}
	VHDChecksum(f)
	return f
}

// VHDChecksum sets the checksum of a VHD footer
func VHDChecksum(f []byte) {
	be.PutUint32(f[64:], 0)
	var sum uint32
	for _, c := range f[:512] {
		sum += uint32(c)
	}
	be.PutUint32(f[64:], ^sum)
}

// VHDFixed is the raw disk and a footer
func VHDFixed(raw []byte) []byte {
	return append(append([]byte(nil), raw...), vhdFooter(2, ^uint64(0), uint64(len(raw)))...)
}

// VHDDynamic stores the blocks that are not zeros; in them the sectors of
// zeros are left out by the bitmap and filled with 0xAA, which a reader
// must not show
func VHDDynamic(raw []byte, block int) []byte {
	size := align(len(raw), 512)
	blocks := (size + block - 1) / block
	batOff := 512 + 1024
	bitmapLen := align(block/512/8, 512)
	out := make([]byte, align(batOff+blocks*4, 512))
	copy(out, vhdFooter(3, 512, uint64(size)))
	h := out[512:]
	copy(h, "cxsparse")
	be.PutUint64(h[8:], ^uint64(0))
	be.PutUint64(h[16:], uint64(batOff))
	be.PutUint32(h[24:], 0x00010000)
	be.PutUint32(h[28:], uint32(blocks))
	be.PutUint32(h[32:], uint32(block))
	be.PutUint32(h[36:], vhdHeaderChecksum(h[:1024]))
	for i := range blocks {
		data := part(raw, i*block, block)
		if isZero(data) {
			be.PutUint32(out[batOff+i*4:], 0xFFFFFFFF)
			continue
		}
		be.PutUint32(out[batOff+i*4:], uint32(len(out)/512))
		bm := make([]byte, bitmapLen)
		for s := 0; s < block/512; s++ {
			sec := data[s*512 : (s+1)*512]
			if isZero(sec) {
				for k := range sec {
					sec[k] = 0xAA
				}
			} else {
				bm[s/8] |= 0x80 >> (s % 8)
			}
		}
		out = append(append(out, bm...), data...)
	}
	return append(out, vhdFooter(3, 512, uint64(size))...)
}

func vhdHeaderChecksum(h []byte) uint32 {
	var sum uint32
	for i, c := range h {
		if i < 36 || i >= 40 {
			sum += uint32(c)
		}
	}
	return ^sum
}

// --- VHDX ---

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// VHDXGUID makes the bytes of a GUID as Windows stores it
func VHDXGUID(s string) []byte {
	b, err := hex.DecodeString(strings.ReplaceAll(s, "-", ""))
	if err != nil || len(b) != 16 {
		panic("bad GUID " + s)
	}
	b[0], b[1], b[2], b[3] = b[3], b[2], b[1], b[0]
	b[4], b[5] = b[5], b[4]
	b[6], b[7] = b[7], b[6]
	return b
}

func utf16le(s string) []byte {
	var b []byte
	for _, c := range utf16.Encode([]rune(s)) {
		b = le.AppendUint16(b, c)
	}
	return b
}

// VHDXOptions changes what VHDX makes
type VHDXOptions struct {
	Log bool // a log GUID in the header: the log is not empty
	// Compact puts the regions before 1 MB, against the specification, to
	// make small files for fuzzing
	Compact bool
}

// VHDX stores the blocks that are not zeros; the zero ones alternate
// between the states ZERO and NOT_PRESENT
func VHDX(raw []byte, block int, opt VHDXOptions) []byte {
	const mb = 1 << 20
	size := align(len(raw), 512)
	blocks := (size + block - 1) / block
	chunk := (1 << 23) * 512 / block
	metaOff, batOff, dataOff := 2*mb, 3*mb, 4*mb
	logOff := mb
	if opt.Compact {
		metaOff, batOff, dataOff = 320<<10, 384<<10, mb
		logOff = 0
	}
	out := make([]byte, dataOff)
	copy(out, "vhdxfile")
	copy(out[8:], utf16le("althex test"))
	for i, off := range []int{64 << 10, 128 << 10} {
		h := out[off : off+4096]
		copy(h, "head")
		le.PutUint64(h[8:], uint64(i+1))
		copy(h[16:], VHDXGUID("11111111-2222-3333-4444-555555555555"))
		copy(h[32:], VHDXGUID("66666666-7777-8888-9999-AAAAAAAAAAAA"))
		if opt.Log {
			copy(h[48:], VHDXGUID("BBBBBBBB-CCCC-DDDD-EEEE-FFFFFFFFFFFF"))
		}
		le.PutUint16(h[66:], 1)
		if logOff > 0 {
			le.PutUint32(h[68:], mb)
			le.PutUint64(h[72:], uint64(logOff))
		}
		le.PutUint32(h[4:], crc32.Checksum(h, castagnoli))
	}
	batEntries := blocks
	if blocks > 0 {
		batEntries += (blocks - 1) / chunk
	}
	batLen := align(batEntries*8, mb)
	if opt.Compact {
		batLen = align(batEntries*8, 64<<10)
	}
	for _, off := range []int{192 << 10, 256 << 10} {
		t := out[off : off+64<<10]
		copy(t, "regi")
		le.PutUint32(t[8:], 2)
		copy(t[16:], VHDXGUID("2DC27766-F623-4200-9D64-115E9BFD4A08"))
		le.PutUint64(t[32:], uint64(batOff))
		le.PutUint32(t[40:], uint32(batLen))
		le.PutUint32(t[44:], 1)
		copy(t[48:], VHDXGUID("8B7CA206-4790-4B9A-B8FE-575F050F886E"))
		le.PutUint64(t[64:], uint64(metaOff))
		le.PutUint32(t[72:], 64<<10)
		le.PutUint32(t[76:], 1)
		le.PutUint32(t[4:], crc32.Checksum(t, castagnoli))
	}
	m := out[metaOff : metaOff+64<<10]
	copy(m, "metadata")
	items := []struct {
		guid  string
		data  []byte
		flags uint32
	}{
		{"CAA16737-FA36-4D43-B3B6-33F0AA44E76B", le.AppendUint32(le.AppendUint32(nil, uint32(block)), 0), 4},
		{"2FA54224-CD1B-4876-B211-5DBED83BF4B8", le.AppendUint64(nil, uint64(size)), 6},
		{"8141BF1D-A96F-4709-BA47-F233A8FAAB5F", le.AppendUint32(nil, 512), 6},
		{"CDA348C7-445D-4471-9CC9-E9885251C556", le.AppendUint32(nil, 4096), 6},
		{"BECA12AB-B2E6-4523-93EF-C309E000C746", VHDXGUID("01020304-0506-0708-090A-0B0C0D0E0F10"), 6},
	}
	le.PutUint16(m[10:], uint16(len(items)))
	at := 4096
	for i, it := range items {
		e := m[32+i*32:]
		copy(e, VHDXGUID(it.guid))
		le.PutUint32(e[16:], uint32(at))
		le.PutUint32(e[20:], uint32(len(it.data)))
		le.PutUint32(e[24:], it.flags)
		copy(m[at:], it.data)
		at += len(it.data)
	}
	out = grow(out, dataOff)
	for i := range blocks {
		idx := i + i/chunk
		var e uint64
		data := part(raw, i*block, block)
		switch {
		case !isZero(data):
			e = uint64(len(out)) | 6
			out = append(out, data...)
		case i%2 == 0:
			e = 2 // ZERO
		}
		le.PutUint64(out[batOff+idx*8:], e)
	}
	return out
}

// --- VMDK ---

func vmdkHeader(capacity, grain, descOff, descSize, gtes, gdOff, overhead uint64, flags uint32, version uint32, compress uint16) []byte {
	h := make([]byte, 512)
	le.PutUint32(h, 0x564D444B)
	le.PutUint32(h[4:], version)
	le.PutUint32(h[8:], flags)
	le.PutUint64(h[12:], capacity)
	le.PutUint64(h[20:], grain)
	le.PutUint64(h[28:], descOff)
	le.PutUint64(h[36:], descSize)
	le.PutUint32(h[44:], uint32(gtes))
	le.PutUint64(h[56:], gdOff)
	le.PutUint64(h[64:], overhead)
	copy(h[73:], "\n \r\n")
	le.PutUint16(h[77:], compress)
	return h
}

// VMDKDescriptor is the text of a descriptor with the extent lines
func VMDKDescriptor(createType string, extents ...string) string {
	s := "# Disk DescriptorFile\nversion=1\nCID=fffffffe\nparentCID=ffffffff\ncreateType=\"" + createType + "\"\n\n# Extent description\n"
	for _, e := range extents {
		s += e + "\n"
	}
	return s + "\n# The Disk Data Base\n#DDB\n\nddb.virtualHWVersion = \"4\"\nddb.adapterType = \"ide\"\n"
}

// VMDKSparse makes a monolithic sparse extent: grains of grainSectors,
// gtes entries per grain table. The grain tables of zeros are left out,
// the zero grains alternate between "not allocated" and "zeroed".
func VMDKSparse(raw []byte, grainSectors, gtes int) []byte {
	sectors := align(len(raw), 512) / 512
	grain := grainSectors * 512
	grains := (sectors + grainSectors - 1) / grainSectors
	gdN := (grains + gtes - 1) / gtes
	desc := VMDKDescriptor("monolithicSparse", fmt.Sprintf("RW %d SPARSE \"test.vmdk\"", sectors))
	descSectors := 20
	gdOff := 1 + descSectors
	gtOff := gdOff + align(gdN*4, 512)/512
	gtSectors := align(gtes*4, 512) / 512
	// The grain tables of zeros get no space
	var used []int
	for t := range gdN {
		if !zeroRange(raw, t*gtes*grain, gtes*grain) {
			used = append(used, t)
		}
	}
	overhead := align(gtOff+len(used)*gtSectors, grainSectors)
	out := make([]byte, overhead*512)
	copy(out, vmdkHeader(uint64(sectors), uint64(grainSectors), 1, uint64(descSectors), uint64(gtes), uint64(gdOff), uint64(overhead), 1|4, 1, 0))
	copy(out[512:], desc)
	for k, t := range used {
		gt := gtOff + k*gtSectors
		le.PutUint32(out[gdOff*512+t*4:], uint32(gt))
		for j := range gtes {
			g := t*gtes + j
			if g >= grains {
				break
			}
			data := part(raw, g*grain, grain)
			var e uint32
			switch {
			case !isZero(data):
				e = uint32(len(out) / 512)
				out = append(out, data...)
			case g%2 == 0:
				e = 1
			}
			le.PutUint32(out[gt*512+j*4:], e)
		}
	}
	return out
}

func zlibBytes(b []byte) []byte {
	var buf bytes.Buffer
	w, _ := zlib.NewWriterLevel(&buf, zlib.BestCompression)
	w.Write(b)
	w.Close()
	return buf.Bytes()
}

func vmdkMarker(sectors uint64, typ uint32) []byte {
	m := make([]byte, 512)
	le.PutUint64(m, sectors)
	le.PutUint32(m[12:], typ)
	return m
}

// VMDKStream makes a stream-optimized extent: compressed grains of 64 KB,
// then the grain tables, the directory and the footer
func VMDKStream(raw []byte) []byte {
	const grainSectors, gtes = 128, 512
	sectors := align(len(raw), 512) / 512
	grain := grainSectors * 512
	grains := (sectors + grainSectors - 1) / grainSectors
	gdN := (grains + gtes - 1) / gtes
	desc := VMDKDescriptor("streamOptimized", fmt.Sprintf("RW %d SPARSE \"stream.vmdk\"", sectors))
	const flags = 1 | 1<<16 | 1<<17
	out := make([]byte, 128*512)
	copy(out, vmdkHeader(uint64(sectors), grainSectors, 1, 20, gtes, ^uint64(0), 128, flags, 3, 1))
	copy(out[512:], desc)
	gts := make([]uint32, gdN*gtes)
	for g := range grains {
		data := part(raw, g*grain, grain)
		if isZero(data) {
			continue
		}
		gts[g] = uint32(len(out) / 512)
		z := zlibBytes(data)
		mk := make([]byte, 12)
		le.PutUint64(mk, uint64(g*grainSectors))
		le.PutUint32(mk[8:], uint32(len(z)))
		out = append(append(out, mk...), z...)
		out = grow(out, align(len(out), 512))
	}
	gd := make([]uint32, gdN)
	gtSectors := align(gtes*4, 512) / 512
	for t := range gdN {
		out = append(out, vmdkMarker(uint64(gtSectors), 1)...)
		gd[t] = uint32(len(out) / 512)
		for j := range gtes {
			out = le.AppendUint32(out, gts[t*gtes+j])
		}
		out = grow(out, align(len(out), 512))
	}
	out = append(out, vmdkMarker(uint64(align(gdN*4, 512)/512), 2)...)
	gdOff := len(out) / 512
	for _, e := range gd {
		out = le.AppendUint32(out, e)
	}
	out = grow(out, align(len(out), 512))
	out = append(out, vmdkMarker(1, 3)...)
	out = append(out, vmdkHeader(uint64(sectors), grainSectors, 1, 20, gtes, uint64(gdOff), 128, flags, 3, 1)...)
	return append(out, make([]byte, 512)...)
}

// --- QCOW2 ---

// QCOW2Options changes what QCOW2 makes
type QCOW2Options struct {
	Version     int  // 2 or 3
	ClusterBits uint // 9..21
	Compress    bool // some clusters are compressed
	Backing     string
}

// QCOW2 stores the clusters that are not zeros, some compressed; the zero
// ones alternate between "not allocated" and the zero flag (version 3)
func QCOW2(raw []byte, o QCOW2Options) []byte {
	cluster := 1 << o.ClusterBits
	l2n := cluster / 8
	clusters := (len(raw) + cluster - 1) / cluster
	l1n := (clusters + l2n - 1) / l2n
	l1Clusters := (l1n*8 + cluster - 1) / cluster
	// Clusters: header, refcount table, refcount block, L1, then the L2 tables
	l1Off := 3 * cluster
	l2 := make([][]uint64, l1n)
	for i := range l2 {
		if !zeroRange(raw, i*l2n*cluster, l2n*cluster) {
			l2[i] = make([]uint64, l2n)
		}
	}
	next := l1Off + l1Clusters*cluster
	l1 := make([]uint64, l1n)
	for i := range l2 {
		if l2[i] != nil {
			l1[i] = uint64(next) | 1<<63
			next += cluster
		}
	}
	out := make([]byte, next)
	var compressed []byte // after the data, packed by bytes
	type comp struct {
		c   int
		off int // in compressed
		n   int
	}
	var comps []comp
	for c := range clusters {
		t := l2[c/l2n]
		if t == nil {
			continue
		}
		data := part(raw, c*cluster, cluster)
		switch {
		case isZero(data):
			if o.Version >= 3 && c%2 == 0 {
				t[c%l2n] = 1
			}
		case o.Compress && c%3 == 1:
			var buf bytes.Buffer
			w, _ := flate.NewWriter(&buf, flate.BestCompression)
			w.Write(data)
			w.Close()
			comps = append(comps, comp{c, len(compressed), buf.Len()})
			compressed = append(compressed, buf.Bytes()...)
		default:
			t[c%l2n] = uint64(len(out)) | 1<<63
			out = append(out, data...)
		}
	}
	base := len(out)
	x := 62 - (o.ClusterBits - 8)
	for _, cp := range comps {
		host := base + cp.off
		sectors := (host+cp.n-1)>>9 - host>>9
		l2[cp.c/l2n][cp.c%l2n] = 1<<62 | uint64(sectors)<<x | uint64(host)
	}
	out = append(out, compressed...)
	out = grow(out, align(len(out), cluster))
	// The header
	h := out
	copy(h, "QFI\xfb")
	be.PutUint32(h[4:], uint32(o.Version))
	be.PutUint32(h[20:], uint32(o.ClusterBits))
	be.PutUint64(h[24:], uint64(len(raw)))
	be.PutUint32(h[36:], uint32(l1n))
	be.PutUint64(h[40:], uint64(l1Off))
	be.PutUint64(h[48:], uint64(cluster))
	be.PutUint32(h[56:], 1)
	end := 72
	if o.Version >= 3 {
		be.PutUint32(h[96:], 4)
		be.PutUint32(h[100:], 112)
		end = 112 + 8 // the end of the extensions
	}
	if o.Backing != "" {
		be.PutUint64(h[8:], uint64(end))
		be.PutUint32(h[16:], uint32(len(o.Backing)))
		copy(h[end:], o.Backing)
	}
	// Refcounts of 16 bits: 1 for every cluster of the file
	be.PutUint64(out[cluster:], uint64(2*cluster))
	for i := 0; i < len(out)/cluster && i < cluster/2; i++ {
		be.PutUint16(out[2*cluster+i*2:], 1)
	}
	for i, e := range l1 {
		be.PutUint64(out[l1Off+i*8:], e)
		if t := l2[i]; t != nil {
			at := int(e &^ (1 << 63))
			for j, v := range t {
				be.PutUint64(out[at+j*8:], v)
			}
		}
	}
	return out
}

// --- VDI ---

// VDI stores the blocks that are not zeros, in the reverse order; the zero
// ones alternate between free and zero entries
func VDI(raw []byte, block int) []byte {
	blocks := (len(raw) + block - 1) / block
	mapOff := 0x200
	dataOff := align(mapOff+blocks*4, 512)
	h := make([]byte, dataOff)
	copy(h, "<<< Oracle VM VirtualBox Disk Image >>>\n")
	le.PutUint32(h[0x40:], 0xBEDA107F)
	le.PutUint32(h[0x44:], 0x00010001)
	le.PutUint32(h[0x48:], 0x190)
	le.PutUint32(h[0x4C:], 1)
	copy(h[0x54:], "made by the test")
	le.PutUint32(h[0x154:], uint32(mapOff))
	le.PutUint32(h[0x158:], uint32(dataOff))
	le.PutUint32(h[0x168:], 512)
	le.PutUint64(h[0x170:], uint64(len(raw)))
	le.PutUint32(h[0x178:], uint32(block))
	le.PutUint32(h[0x180:], uint32(blocks))
	for i := range 16 {
		h[0x188+i] = byte(i + 1)
	}
	var nonZero []int
	for i := range blocks {
		if zeroRange(raw, i*block, block) {
			le.PutUint32(h[mapOff+i*4:], 0xFFFFFFFF-uint32(i%2))
		} else {
			nonZero = append(nonZero, i)
		}
	}
	le.PutUint32(h[0x184:], uint32(len(nonZero)))
	out := h
	for k := len(nonZero) - 1; k >= 0; k-- {
		i := nonZero[k]
		le.PutUint32(out[mapOff+i*4:], uint32(len(nonZero)-1-k))
		out = append(out, part(raw, i*block, block)...)
	}
	return out
}

// --- Android sparse ---

// AndroidSparse makes raw, fill and don't-care chunks of blocks of blk
// bytes and a CRC-32 chunk at the end
func AndroidSparse(raw []byte, blk int) []byte {
	blocks := (len(raw) + blk - 1) / blk
	out := make([]byte, 28)
	chunks := 0
	add := func(typ uint16, n int, data []byte) {
		out = le.AppendUint16(out, typ)
		out = le.AppendUint16(out, 0)
		out = le.AppendUint32(out, uint32(n))
		out = le.AppendUint32(out, uint32(12+len(data)))
		out = append(out, data...)
		chunks++
	}
	kind := func(b []byte) (int, uint32) {
		if isZero(b) {
			return 0, 0
		}
		v := le.Uint32(b)
		for i := 4; i < len(b); i += 4 {
			if le.Uint32(b[i:]) != v {
				return 2, 0
			}
		}
		return 1, v
	}
	for i := 0; i < blocks; {
		k, v := kind(part(raw, i*blk, blk))
		j := i + 1
		for j < blocks {
			k2, v2 := kind(part(raw, j*blk, blk))
			if k2 != k || v2 != v {
				break
			}
			j++
		}
		switch k {
		case 0:
			add(0xCAC3, j-i, nil)
		case 1:
			add(0xCAC2, j-i, le.AppendUint32(nil, v))
		default:
			add(0xCAC1, j-i, part(raw, i*blk, (j-i)*blk))
		}
		i = j
	}
	add(0xCAC4, 0, le.AppendUint32(nil, crc32.ChecksumIEEE(part(raw, 0, blocks*blk))))
	le.PutUint32(out, 0xED26FF3A)
	le.PutUint16(out[4:], 1)
	le.PutUint16(out[8:], 28)
	le.PutUint16(out[10:], 12)
	le.PutUint32(out[12:], uint32(blk))
	le.PutUint32(out[16:], uint32(blocks))
	le.PutUint32(out[20:], uint32(chunks))
	return out
}
