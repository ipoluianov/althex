package diskimg

import (
	"encoding/binary"
	"fmt"
	"io"
)

// VDI (VirtualBox Disk Image): a text line, the signature, the header and
// the block map: for each block of the disk the index of the block in the
// data area, or 0xFFFFFFFF (free) / 0xFFFFFFFE (zeros). Little-endian.

const (
	vdiSignature  = 0xBEDA107F
	vdiTypeNormal = 1
	vdiTypeFixed  = 2
	vdiTypeUndo   = 3
	vdiTypeDiff   = 4
)

type vdiMapper struct {
	r       io.ReaderAt
	pages   *pager
	mapOff  int64
	dataOff int64
	block   int64
	extra   int64 // bytes before the data of each block
	blocks  int64
}

func openVDI(r io.ReaderAt, size int64) (*disk, error) {
	const format = "VDI"
	h := make([]byte, 0x200)
	if err := readFull(r, h, 0); err != nil {
		return nil, err
	}
	if v := le32(h[0x44:]); v>>16 != 1 {
		return nil, fmt.Errorf("%s: the version %d.%d is not supported", format, v>>16, v&0xFFFF)
	}
	switch typ := le32(h[0x4C:]); typ {
	case vdiTypeNormal, vdiTypeFixed:
	case vdiTypeUndo, vdiTypeDiff:
		// A VDI names its parent only by its UUID
		return nil, &ParentError{Format: format, Parent: "UUID " + uuidString(h[0x1A8:0x1B8])}
	default:
		return nil, errCorrupt(format, "the image type %d", typ)
	}
	m := &vdiMapper{
		r:       r,
		mapOff:  int64(le32(h[0x154:])),
		dataOff: int64(le32(h[0x158:])),
		block:   int64(le32(h[0x178:])),
		extra:   int64(le32(h[0x17C:])),
		blocks:  int64(le32(h[0x180:])),
	}
	vsize := int64(le64(h[0x170:]))
	if m.block < 512 || m.block > 1<<30 || m.extra > 1<<20 {
		return nil, errCorrupt(format, "the block size %d", m.block)
	}
	if vsize < 0 || vsize > 1<<50 {
		return nil, errCorrupt(format, "the disk size %d", vsize)
	}
	if m.mapOff > size || m.blocks*4 > size-m.mapOff {
		return nil, errCorrupt(format, "the block map is outside the file")
	}
	m.pages = newPager(r, size, binary.LittleEndian)
	return &disk{m: m, size: vsize, format: format}, nil
}

func (m *vdiMapper) mapAt(off int64) (run, error) {
	b := off / m.block
	in := off % m.block
	n := m.block - in
	if b >= m.blocks {
		return run{n: n}, nil
	}
	e, err := m.pages.u32(m.mapOff + b*4)
	if err != nil {
		return run{}, err
	}
	if e >= 0xFFFFFFFE {
		return run{n: n}, nil
	}
	return run{n: n, r: m.r, off: m.dataOff + int64(e)*(m.block+m.extra) + m.extra + in}, nil
}

// uuidString shows a UUID stored little-endian in its first three parts
func uuidString(b []byte) string {
	return fmt.Sprintf("%08x-%04x-%04x-%x-%x", le32(b), le16(b[4:]), le16(b[6:]), b[8:10], b[10:16])
}
