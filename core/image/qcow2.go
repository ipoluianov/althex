package diskimg

import (
	"encoding/binary"
	"fmt"
	"io"
)

// QCOW2 (the QEMU image format, versions 2 and 3): the disk is in clusters;
// the L1 table points to L2 tables, which point to the clusters. A cluster
// may be compressed (raw deflate) or marked as zeros. Refcounts and
// snapshots are not needed to read the active disk. Numbers are big-endian.

const (
	qcowOffsetMask  = 0x00FFFFFFFFFFFE00
	qcowCompressed  = 1 << 62
	qcowZeroCluster = 1

	qcowIncompatDirty      = 1 << 0
	qcowIncompatCorrupt    = 1 << 1
	qcowIncompatDataFile   = 1 << 2
	qcowIncompatCompress   = 1 << 3
	qcowIncompatExtendedL2 = 1 << 4

	qcowExtDataFile = 0x44415441
)

type qcowMapper struct {
	r         io.ReaderAt
	size      int64 // of the file
	bits      uint  // cluster bits
	cluster   int64
	l2Bits    uint
	l1Off     int64
	l1N       int64
	pages     *pager
	clusters  *lru[[]byte] // decoded compressed clusters
	sectorsAt uint         // where the sector count of a compressed cluster starts
}

func openQCOW2(r io.ReaderAt, size int64) (*disk, error) {
	const format = "QCOW2"
	h := make([]byte, min(size, 4096))
	if err := readFull(r, h, 0); err != nil {
		return nil, err
	}
	if len(h) < 72 {
		return nil, errCorrupt(format, "the header is cut")
	}
	version := be32(h[4:])
	bits := be32(h[20:])
	if bits < 9 || bits > 21 {
		return nil, errCorrupt(format, "the cluster bits %d", bits)
	}
	if crypt := be32(h[32:]); crypt != 0 {
		return nil, fmt.Errorf("%s: encrypted images are not supported", format)
	}
	headerLen := 72
	if version >= 3 {
		if len(h) < 104 {
			return nil, errCorrupt(format, "the header is cut")
		}
		headerLen = int(be32(h[100:]))
		if headerLen < 104 || headerLen > len(h) {
			return nil, errCorrupt(format, "the header length %d", headerLen)
		}
		incompat := be64(h[72:])
		known := uint64(qcowIncompatDirty | qcowIncompatCorrupt | qcowIncompatDataFile | qcowIncompatCompress | qcowIncompatExtendedL2)
		switch {
		case incompat&^known != 0:
			return nil, fmt.Errorf("%s: unknown incompatible features %#x", format, incompat&^known)
		case incompat&qcowIncompatDataFile != 0:
			name := qcowExtension(h, headerLen, qcowExtDataFile)
			if name == "" {
				name = "unknown"
			}
			return nil, fmt.Errorf("%s: the data is in the external file %q: not supported", format, name)
		case incompat&qcowIncompatExtendedL2 != 0:
			return nil, fmt.Errorf("%s: extended L2 entries (subclusters) are not supported", format)
		case incompat&qcowIncompatCompress != 0 && headerLen > 104:
			if t := h[104]; t != 0 {
				name := "zstd"
				if t != 1 {
					name = fmt.Sprint(t)
				}
				return nil, fmt.Errorf("%s: the compression %s is not supported", format, name)
			}
		}
	}
	if bo := be64(h[8:]); bo != 0 {
		n := int64(be32(h[16:]))
		name := "unknown"
		if bo < uint64(size) && n > 0 && n <= 1023 {
			b := make([]byte, min(n, size-int64(bo)))
			if readFull(r, b, int64(bo)) == nil {
				name = string(b)
			}
		}
		return nil, &ParentError{Format: format, Parent: name}
	}
	vsize := be64(h[24:])
	if vsize > 1<<56 {
		return nil, errCorrupt(format, "the disk size %d", vsize)
	}
	m := &qcowMapper{
		r:       r,
		size:    size,
		bits:    uint(bits),
		cluster: 1 << bits,
		l2Bits:  uint(bits) - 3,
		l1Off:   int64(be64(h[40:])),
		l1N:     int64(be32(h[36:])),
	}
	m.sectorsAt = 62 - (m.bits - 8)
	if m.l1Off < 0 || m.l1Off > size || m.l1N*8 > size-m.l1Off {
		return nil, errCorrupt(format, "the L1 table is outside the file")
	}
	m.pages = newPager(r, size, binary.BigEndian)
	m.clusters = newLRU[[]byte](max(2, int(8<<20/m.cluster)))
	return &disk{m: m, size: int64(vsize), format: format}, nil
}

// qcowExtension returns the data of a header extension as text
func qcowExtension(h []byte, at int, typ uint32) string {
	for at+8 <= len(h) {
		t, n := be32(h[at:]), int(be32(h[at+4:]))
		if t == 0 || n > len(h)-at-8 {
			break
		}
		if t == typ {
			return cString(h[at+8 : at+8+n])
		}
		at += 8 + (n+7)&^7
	}
	return ""
}

func (m *qcowMapper) mapAt(off int64) (run, error) {
	c := off >> m.bits
	in := off & (m.cluster - 1)
	n := m.cluster - in
	l1 := c >> m.l2Bits
	if l1 >= m.l1N {
		return run{n: n}, nil
	}
	e1, err := m.pages.u64(m.l1Off + l1*8)
	if err != nil {
		return run{}, err
	}
	l2Off := int64(e1 & qcowOffsetMask)
	if l2Off == 0 {
		// No L2 table: all of its clusters are zeros
		return run{n: (l1+1)<<(m.l2Bits+m.bits) - off}, nil
	}
	e, err := m.pages.u64(l2Off + (c&(1<<m.l2Bits-1))*8)
	if err != nil {
		return run{}, err
	}
	if e&qcowCompressed != 0 {
		data, err := m.clusters.get(int64(e&(1<<62-1)), func() ([]byte, error) { return m.decode(e) })
		if err != nil {
			return run{}, err
		}
		return run{n: n, data: data[in:]}, nil
	}
	host := int64(e & qcowOffsetMask)
	if e&qcowZeroCluster != 0 || host == 0 {
		return run{n: n}, nil
	}
	return run{n: n, r: m.r, off: host + in}, nil
}

// decode reads a compressed cluster: its offset and its size in sectors
// are in the L2 entry
func (m *qcowMapper) decode(e uint64) ([]byte, error) {
	host := int64(e & (1<<m.sectorsAt - 1))
	sectors := int64(e>>m.sectorsAt) & (1<<(m.bits-8) - 1)
	n := (sectors+1)*512 - host&511
	if host >= m.size {
		return nil, errCorrupt("QCOW2", "a compressed cluster at %d", host)
	}
	// The last one may end past the end of the file
	src := make([]byte, min(n, m.size-host))
	if err := readFull(m.r, src, host); err != nil {
		return nil, err
	}
	data, err := inflate(src, int(m.cluster), false)
	if err != nil {
		return nil, fmt.Errorf("QCOW2: %w", err)
	}
	return data, nil
}
