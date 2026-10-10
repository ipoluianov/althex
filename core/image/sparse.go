package diskimg

import (
	"io"
	"sort"
)

// Android sparse image (libsparse, sparse_format.h): a header and chunks,
// each a number of blocks of the image: raw (the blocks follow), fill (a
// 4-byte value repeated), don't care (zeros here) and CRC-32 (no blocks).
// Little-endian.

const (
	sparseMagic    = 0xED26FF3A
	sparseRaw      = 0xCAC1
	sparseFill     = 0xCAC2
	sparseDontCare = 0xCAC3
	sparseCRC32    = 0xCAC4
)

type sparseChunk struct {
	start, end int64  // in the image
	off        int64  // the raw data in the file
	fill       []byte // the value twice
	raw        bool
}

type sparseMapper struct {
	r      io.ReaderAt
	chunks []sparseChunk // only raw and fill ones
}

func openSparse(r io.ReaderAt, size int64) (*disk, error) {
	const format = "Android sparse"
	h := make([]byte, 28)
	if err := readFull(r, h, 0); err != nil {
		return nil, err
	}
	if major := le16(h[4:]); major != 1 {
		return nil, errCorrupt(format, "the version %d", major)
	}
	hdrLen, chunkLen := int64(le16(h[8:])), int64(le16(h[10:]))
	blk := int64(le32(h[12:]))
	total, count := int64(le32(h[16:])), int64(le32(h[20:]))
	if hdrLen < 28 || chunkLen < 12 || blk < 4 || blk%4 != 0 || blk > 1<<28 {
		return nil, errCorrupt(format, "the header")
	}
	m := &sparseMapper{r: r}
	vsize := total * blk
	pos := hdrLen
	var out int64
	var ch [12]byte
	for i := int64(0); i < count; i++ {
		if err := readFull(r, ch[:], pos); err != nil {
			return nil, err
		}
		typ := le16(ch[:])
		blocks := int64(le32(ch[4:]))
		n := int64(le32(ch[8:]))
		if n < chunkLen || n > size-pos {
			return nil, errCorrupt(format, "chunk %d: its size %d", i, n)
		}
		data := pos + chunkLen
		c := sparseChunk{start: out, end: out + blocks*blk, off: data}
		switch typ {
		case sparseRaw:
			if n-chunkLen != blocks*blk {
				return nil, errCorrupt(format, "chunk %d: raw data of %d bytes", i, n-chunkLen)
			}
			c.raw = true
		case sparseFill:
			if n-chunkLen < 4 {
				return nil, errCorrupt(format, "chunk %d: no fill value", i)
			}
			// Twice, so that the pattern from any of its bytes is a slice
			c.fill = make([]byte, 8)
			if err := readFull(r, c.fill[:4], data); err != nil {
				return nil, err
			}
			copy(c.fill[4:], c.fill[:4])
		case sparseDontCare:
		case sparseCRC32:
			if blocks != 0 {
				return nil, errCorrupt(format, "chunk %d: a CRC with blocks", i)
			}
		default:
			return nil, errCorrupt(format, "chunk %d: the type %#x", i, typ)
		}
		if c.end > vsize {
			return nil, errCorrupt(format, "chunk %d is past the end of the image", i)
		}
		if blocks > 0 && (c.raw || c.fill != nil) {
			m.chunks = append(m.chunks, c)
		}
		out = c.end
		pos += n
	}
	return &disk{m: m, size: vsize, format: format}, nil
}

func (m *sparseMapper) mapAt(off int64) (run, error) {
	i := sort.Search(len(m.chunks), func(i int) bool { return m.chunks[i].end > off })
	if i == len(m.chunks) {
		return run{n: 1 << 62}, nil
	}
	c := &m.chunks[i]
	if off < c.start {
		return run{n: c.start - off}, nil
	}
	n := c.end - off
	in := off - c.start
	if c.raw {
		return run{n: n, r: m.r, off: c.off + in}, nil
	}
	// The pattern from where the offset is in it
	k := in % 4
	return run{n: n, fill: c.fill[k : k+4]}, nil
}
