package diskimg

import (
	"encoding/binary"
	"encoding/hex"
	"hash/crc32"
	"io"
	"strings"
)

// VHDX (MS-VHDX): the file identifier, two headers (the one with the
// greater sequence number is in force), two copies of the region table,
// and the regions: the metadata (block size, disk size, sector size, the
// parent) and the BAT. The BAT has an entry per payload block and, after
// each chunk of them, one of a sector bitmap block. Numbers are
// little-endian, checksums are CRC-32C.

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// guid makes the bytes of a GUID as Windows stores it: the first three
// parts little-endian
func guid(s string) [16]byte {
	b, err := hex.DecodeString(strings.ReplaceAll(s, "-", ""))
	if err != nil || len(b) != 16 {
		panic("bad GUID " + s)
	}
	var g [16]byte
	copy(g[:], b)
	g[0], g[1], g[2], g[3] = b[3], b[2], b[1], b[0]
	g[4], g[5] = b[5], b[4]
	g[6], g[7] = b[7], b[6]
	return g
}

var (
	vhdxRegionBAT      = guid("2DC27766-F623-4200-9D64-115E9BFD4A08")
	vhdxRegionMetadata = guid("8B7CA206-4790-4B9A-B8FE-575F050F886E")
	vhdxFileParameters = guid("CAA16737-FA36-4D43-B3B6-33F0AA44E76B")
	vhdxVirtualSize    = guid("2FA54224-CD1B-4876-B211-5DBED83BF4B8")
	vhdxLogicalSector  = guid("8141BF1D-A96F-4709-BA47-F233A8FAAB5F")
	vhdxParentLocator  = guid("A8D35F2D-B30B-454D-ABF7-D3D84834AB0C")
)

const (
	vhdxBlockFullyPresent     = 6
	vhdxBlockPartiallyPresent = 7
)

// vhdxChecksumOK checks the CRC-32C of a structure with its checksum at 4
func vhdxChecksumOK(b []byte) bool {
	want := le32(b[4:])
	c := crc32.Update(0, castagnoli, b[:4])
	c = crc32.Update(c, castagnoli, []byte{0, 0, 0, 0})
	c = crc32.Update(c, castagnoli, b[8:])
	return c == want
}

type vhdxMapper struct {
	r      io.ReaderAt
	bat    *pager
	batOff int64
	block  int64
	chunk  int64 // payload blocks per sector bitmap block
}

func openVHDX(r io.ReaderAt, size int64) (*disk, error) {
	const format = "VHDX"
	// The headers
	var cur []byte
	var seq uint64
	for _, off := range []int64{64 << 10, 128 << 10} {
		h := make([]byte, 4096)
		if readFull(r, h, off) != nil || string(h[:4]) != "head" || !vhdxChecksumOK(h) {
			continue
		}
		if s := le64(h[8:]); cur == nil || s > seq {
			cur, seq = h, s
		}
	}
	if cur == nil {
		return nil, errCorrupt(format, "no valid header")
	}
	if v := le16(cur[66:]); v != 1 {
		return nil, errCorrupt(format, "the version %d", v)
	}
	logged := false
	for _, c := range cur[48:64] {
		if c != 0 {
			// The log may have writes not put in place yet
			logged = true
		}
	}
	// The region table
	var regions []byte
	for _, off := range []int64{192 << 10, 256 << 10} {
		t := make([]byte, 64<<10)
		if readFull(r, t, off) == nil && string(t[:4]) == "regi" && vhdxChecksumOK(t) {
			regions = t
			break
		}
	}
	if regions == nil {
		return nil, errCorrupt(format, "no valid region table")
	}
	count := int(le32(regions[8:]))
	if count > 2047 {
		return nil, errCorrupt(format, "%d regions", count)
	}
	var batOff, batLen, metaOff, metaLen int64
	for i := range count {
		e := regions[16+i*32:]
		g := [16]byte(e[:16])
		off, length := int64(le64(e[16:])), int64(le32(e[24:]))
		switch g {
		case vhdxRegionBAT:
			batOff, batLen = off, length
		case vhdxRegionMetadata:
			metaOff, metaLen = off, length
		default:
			if le32(e[28:])&1 != 0 {
				return nil, errCorrupt(format, "an unknown required region")
			}
		}
	}
	if batOff <= 0 || metaOff <= 0 || batOff > size-batLen || metaOff > size-metaLen || metaLen < 32 || metaLen > 64<<20 {
		return nil, errCorrupt(format, "the BAT or the metadata is outside the file")
	}
	// The metadata
	meta := make([]byte, metaLen)
	if err := readFull(r, meta, metaOff); err != nil {
		return nil, err
	}
	if string(meta[:8]) != "metadata" {
		return nil, errCorrupt(format, "no metadata table")
	}
	n := int(le16(meta[10:]))
	if n > 2047 || 32+n*32 > len(meta) {
		return nil, errCorrupt(format, "%d metadata entries", n)
	}
	item := func(g [16]byte, minLen int) []byte {
		for i := range n {
			e := meta[32+i*32:]
			if [16]byte(e[:16]) != g {
				continue
			}
			off, length := int(le32(e[16:])), int(le32(e[20:]))
			if length < minLen || off < 0 || length < 0 || off > len(meta)-length {
				return nil
			}
			return meta[off : off+length]
		}
		return nil
	}
	params := item(vhdxFileParameters, 8)
	vs := item(vhdxVirtualSize, 8)
	ls := item(vhdxLogicalSector, 4)
	if params == nil || vs == nil || ls == nil {
		return nil, errCorrupt(format, "required metadata is missing")
	}
	m := &vhdxMapper{r: r, batOff: batOff, block: int64(le32(params))}
	if le32(params[4:])&2 != 0 {
		return nil, &ParentError{Format: format, Parent: vhdxParent(item(vhdxParentLocator, 20))}
	}
	vsize := int64(le64(vs))
	sector := int64(le32(ls))
	if !isPow2(m.block) || m.block < 1<<20 || m.block > 256<<20 {
		return nil, errCorrupt(format, "the block size %d", m.block)
	}
	if sector != 512 && sector != 4096 {
		return nil, errCorrupt(format, "the sector size %d", sector)
	}
	if vsize < 0 || vsize > 64<<40 {
		return nil, errCorrupt(format, "the disk size %d", vsize)
	}
	m.chunk = (1 << 23) * sector / m.block
	blocks := ceilDiv(vsize, m.block)
	entries := blocks
	if blocks > 0 {
		entries += (blocks - 1) / m.chunk
	}
	if entries*8 > batLen {
		return nil, errCorrupt(format, "the BAT is too small")
	}
	m.bat = newPager(r, size, binary.LittleEndian)
	name := format
	if logged {
		name = "VHDX (log not replayed)"
	}
	return &disk{m: m, size: vsize, format: name}, nil
}

func (m *vhdxMapper) mapAt(off int64) (run, error) {
	b := off / m.block
	in := off % m.block
	n := m.block - in
	e, err := m.bat.u64(m.batOff + (b+b/m.chunk)*8)
	if err != nil {
		return run{}, err
	}
	switch e & 7 {
	case vhdxBlockFullyPresent, vhdxBlockPartiallyPresent:
		// Partially present is only in differencing disks, which are not
		// opened: its sectors are all taken as present
		fo := int64(e>>20) << 20
		if fo == 0 {
			return run{}, errCorrupt("VHDX", "block %d at offset 0", b)
		}
		return run{n: n, r: m.r, off: fo + in}, nil
	}
	// Not present, undefined, zero, unmapped
	return run{n: n}, nil
}

// vhdxParent finds the path of the parent in the parent locator
func vhdxParent(loc []byte) string {
	if loc == nil {
		return "unknown"
	}
	kv := map[string]string{}
	n := int(le16(loc[18:]))
	for i := range n {
		e := 20 + i*12
		if e+12 > len(loc) {
			break
		}
		ko, vo := int(le32(loc[e:])), int(le32(loc[e+4:]))
		kl, vl := int(le16(loc[e+8:])), int(le16(loc[e+10:]))
		if ko > len(loc)-kl || vo > len(loc)-vl {
			continue
		}
		kv[utf16String(loc[ko:ko+kl], binary.LittleEndian)] = utf16String(loc[vo:vo+vl], binary.LittleEndian)
	}
	for _, k := range []string{"relative_path", "absolute_win32_path", "volume_path"} {
		if v := kv[k]; v != "" {
			return v
		}
	}
	return "unknown"
}
