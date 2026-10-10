package diskimg

import (
	"bytes"
	"encoding/binary"
	"io"
	"math/bits"
	"unicode/utf16"
)

// VHD (Virtual Hard Disk Image Format Specification, Microsoft): a 512-byte
// footer at the end of the file. A fixed disk is the file before the
// footer. A dynamic disk has a copy of the footer at the start, then a
// dynamic disk header, the block allocation table (BAT) and blocks: each
// is a bitmap of its sectors (a set bit - the sector is stored) and the
// data. All numbers are big-endian.

const (
	vhdTypeFixed        = 2
	vhdTypeDynamic      = 3
	vhdTypeDifferencing = 4
)

// vhdFooterOK tells whether the footer has its cookie and checksum
func vhdFooterOK(f []byte) bool {
	if len(f) < 512 || string(f[:8]) != "conectix" {
		return false
	}
	return vhdChecksum(f, 64) == be32(f[64:])
}

// vhdChecksum is the ones' complement of the sum of the bytes, without the
// checksum field at at
func vhdChecksum(b []byte, at int) uint32 {
	var sum uint32
	for i, c := range b {
		if i < at || i >= at+4 {
			sum += uint32(c)
		}
	}
	return ^sum
}

// vhdFixedAtEnd tells whether the file ends with the footer of a fixed disk
func vhdFixedAtEnd(r io.ReaderAt, size int64) bool {
	var f [512]byte
	if readFull(r, f[:], size-512) != nil {
		return false
	}
	return vhdFooterOK(f[:]) && be32(f[60:]) == vhdTypeFixed
}

type vhdMapper struct {
	r         io.ReaderAt
	size      int64 // of the file
	bat       *pager
	batOff    int64
	batN      int64
	block     int64 // the size of a block of data
	bitmapLen int64 // the size of the bitmap before each block, whole sectors
	bitmaps   *lru[[]byte]
}

func openVHD(r io.ReaderAt, size int64) (*disk, error) {
	const format = "VHD"
	// The footer at the end is the one in force; the copy at the start is
	// for when the end is lost
	foot := make([]byte, 512)
	if size < 512 || readFull(r, foot, size-512) != nil || !vhdFooterOK(foot) {
		if err := readFull(r, foot, 0); err != nil {
			return nil, err
		}
		if !vhdFooterOK(foot) {
			return nil, errCorrupt(format, "the footer checksum")
		}
	}
	typ := be32(foot[60:])
	if typ != vhdTypeDynamic && typ != vhdTypeDifferencing {
		return nil, errCorrupt(format, "the disk type %d", typ)
	}
	vsize := int64(be64(foot[48:]))
	hdrOff := be64(foot[16:])
	if hdrOff > uint64(size) {
		return nil, errCorrupt(format, "the dynamic header offset")
	}
	h := make([]byte, 1024)
	if err := readFull(r, h, int64(hdrOff)); err != nil {
		return nil, err
	}
	if string(h[:8]) != "cxsparse" {
		return nil, errCorrupt(format, "no dynamic header")
	}
	if typ == vhdTypeDifferencing {
		return nil, &ParentError{Format: "VHD", Parent: utf16String(h[64:576], binary.BigEndian)}
	}
	m := &vhdMapper{
		r:      r,
		size:   size,
		batOff: int64(be64(h[16:])),
		batN:   int64(be32(h[28:])),
		block:  int64(be32(h[32:])),
	}
	if !isPow2(m.block) || m.block < 512 || m.block > 1<<28 {
		return nil, errCorrupt(format, "the block size %d", m.block)
	}
	if vsize < 0 || vsize > 1<<50 {
		return nil, errCorrupt(format, "the disk size %d", vsize)
	}
	if m.batOff < 0 || m.batOff > size || m.batN*4 > size-m.batOff {
		return nil, errCorrupt(format, "the BAT is outside the file")
	}
	m.bitmapLen = ceilDiv(m.block/512/8, 512) * 512
	m.bat = newPager(r, size, binary.BigEndian)
	m.bitmaps = newLRU[[]byte](64)
	return &disk{m: m, size: vsize, format: "VHD (dynamic)"}, nil
}

func (m *vhdMapper) mapAt(off int64) (run, error) {
	b := off / m.block
	in := off % m.block
	n := m.block - in
	if b >= m.batN {
		return run{n: n}, nil
	}
	e, err := m.bat.u32(m.batOff + b*4)
	if err != nil {
		return run{}, err
	}
	if e == 0xFFFFFFFF {
		return run{n: n}, nil
	}
	start := int64(e) * 512
	bm, err := m.bitmaps.get(b, func() ([]byte, error) {
		bm := make([]byte, m.block/512/8)
		if err := readFull(m.r, bm, start); err != nil {
			return nil, err
		}
		return bm, nil
	})
	if err != nil {
		return run{}, err
	}
	// The sectors from this one that are all stored, or all not
	s := in / 512
	end := m.block / 512
	set := bm[s/8]&(0x80>>(s%8)) != 0
	e2 := sameBitsRun(bm, s, end, set)
	n = e2*512 - in
	if !set {
		return run{n: n}, nil
	}
	return run{n: n, r: m.r, off: start + m.bitmapLen + in}, nil
}

// sameBitsRun returns the first bit from s (counting from the highest bit
// of each byte) that is not set, up to end
func sameBitsRun(bm []byte, s, end int64, set bool) int64 {
	var all byte
	if set {
		all = 0xFF
	}
	for s < end {
		if s%8 == 0 && s+8 <= end {
			// Whole bytes at once
			c := bm[s/8] ^ all
			if c == 0 {
				s += 8
				continue
			}
			return s + int64(bits.LeadingZeros8(c))
		}
		if (bm[s/8]&(0x80>>(s%8)) != 0) != set {
			return s
		}
		s++
	}
	return end
}

// utf16String decodes UTF-16 text up to its first zero char
func utf16String(b []byte, order binary.ByteOrder) string {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		c := order.Uint16(b[i:])
		if c == 0 {
			break
		}
		u = append(u, c)
	}
	return string(utf16.Decode(u))
}

// cString is the text up to the first zero byte
func cString(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}
