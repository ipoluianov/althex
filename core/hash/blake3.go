package checksum

import (
	"encoding/binary"
	gohash "hash"
	"math/bits"
)

// BLAKE3 in plain Go, the hash mode only (no key, no key derivation),
// after the reference implementation in the BLAKE3 paper. The input is cut
// into chunks of 1 KB; each chunk is hashed alone and the chunks are joined
// in a binary tree. The tree is kept as a stack of the chaining values of
// complete subtrees, so memory does not grow with the input.

const (
	b3OutLen   = 32
	b3BlockLen = 64
	b3ChunkLen = 1024

	b3ChunkStart = 1 << 0
	b3ChunkEnd   = 1 << 1
	b3Parent     = 1 << 2
	b3Root       = 1 << 3
)

var b3IV = [8]uint32{
	0x6A09E667, 0xBB67AE85, 0x3C6EF372, 0xA54FF53A,
	0x510E527F, 0x9B05688C, 0x1F83D9AB, 0x5BE0CD19,
}

// b3Schedule is the order the message words are used in by each round:
// the message permutation applied 0..6 times
var b3Schedule = func() (s [7][16]uint8) {
	perm := [16]uint8{2, 6, 3, 10, 7, 0, 4, 13, 1, 11, 12, 5, 9, 14, 15, 8}
	for i := range 16 {
		s[0][i] = uint8(i)
	}
	for r := 1; r < 7; r++ {
		for i := range 16 {
			s[r][i] = s[r-1][perm[i]]
		}
	}
	return
}()

func b3g(a, b, c, d, mx, my uint32) (uint32, uint32, uint32, uint32) {
	a += b + mx
	d = bits.RotateLeft32(d^a, -16)
	c += d
	b = bits.RotateLeft32(b^c, -12)
	a += b + my
	d = bits.RotateLeft32(d^a, -8)
	c += d
	b = bits.RotateLeft32(b^c, -7)
	return a, b, c, d
}

// b3Compress is the compression function; it returns the whole state, the
// first 8 words of which are the new chaining value
func b3Compress(cv *[8]uint32, m *[16]uint32, counter uint64, blockLen, flags uint32) [16]uint32 {
	s0, s1, s2, s3 := cv[0], cv[1], cv[2], cv[3]
	s4, s5, s6, s7 := cv[4], cv[5], cv[6], cv[7]
	s8, s9, s10, s11 := b3IV[0], b3IV[1], b3IV[2], b3IV[3]
	s12, s13, s14, s15 := uint32(counter), uint32(counter>>32), blockLen, flags
	for r := range 7 {
		k := &b3Schedule[r]
		s0, s4, s8, s12 = b3g(s0, s4, s8, s12, m[k[0]], m[k[1]])
		s1, s5, s9, s13 = b3g(s1, s5, s9, s13, m[k[2]], m[k[3]])
		s2, s6, s10, s14 = b3g(s2, s6, s10, s14, m[k[4]], m[k[5]])
		s3, s7, s11, s15 = b3g(s3, s7, s11, s15, m[k[6]], m[k[7]])
		s0, s5, s10, s15 = b3g(s0, s5, s10, s15, m[k[8]], m[k[9]])
		s1, s6, s11, s12 = b3g(s1, s6, s11, s12, m[k[10]], m[k[11]])
		s2, s7, s8, s13 = b3g(s2, s7, s8, s13, m[k[12]], m[k[13]])
		s3, s4, s9, s14 = b3g(s3, s4, s9, s14, m[k[14]], m[k[15]])
	}
	return [16]uint32{
		s0 ^ s8, s1 ^ s9, s2 ^ s10, s3 ^ s11, s4 ^ s12, s5 ^ s13, s6 ^ s14, s7 ^ s15,
		s8 ^ cv[0], s9 ^ cv[1], s10 ^ cv[2], s11 ^ cv[3], s12 ^ cv[4], s13 ^ cv[5], s14 ^ cv[6], s15 ^ cv[7],
	}
}

func b3Words(m *[16]uint32, b []byte) {
	_ = b[63]
	for i := range m {
		m[i] = binary.LittleEndian.Uint32(b[4*i:])
	}
}

// b3Output is what is needed to compress the last block of a node: its
// chaining value or, for the root, any number of output bytes
type b3Output struct {
	cv       [8]uint32
	block    [16]uint32
	counter  uint64
	blockLen uint32
	flags    uint32
}

func (o *b3Output) chainingValue() [8]uint32 {
	s := b3Compress(&o.cv, &o.block, o.counter, o.blockLen, o.flags)
	return [8]uint32(s[:8])
}

// rootBytes fills out with the output stream of the root
func (o *b3Output) rootBytes(out []byte) {
	var buf [64]byte
	for i := uint64(0); len(out) > 0; i++ {
		s := b3Compress(&o.cv, &o.block, i, o.blockLen, o.flags|b3Root)
		for j, w := range s {
			binary.LittleEndian.PutUint32(buf[4*j:], w)
		}
		out = out[copy(out, buf[:]):]
	}
}

type b3Chunk struct {
	cv        [8]uint32
	counter   uint64
	block     [64]byte
	blockLen  int
	blocks    int // compressed already
	baseFlags uint32
}

func (c *b3Chunk) len() int { return b3BlockLen*c.blocks + c.blockLen }

func (c *b3Chunk) startFlag() uint32 {
	if c.blocks == 0 {
		return b3ChunkStart
	}
	return 0
}

func (c *b3Chunk) compress(b []byte) {
	var m [16]uint32
	b3Words(&m, b)
	s := b3Compress(&c.cv, &m, c.counter, b3BlockLen, c.baseFlags|c.startFlag())
	c.cv = [8]uint32(s[:8])
	c.blocks++
}

// update adds bytes, no more than the chunk has room for. The last block
// is kept uncompressed: it may be the end of the chunk.
func (c *b3Chunk) update(b []byte) {
	for len(b) > 0 {
		if c.blockLen == b3BlockLen {
			c.compress(c.block[:])
			c.blockLen = 0
		}
		// Whole blocks straight from the input, without a copy
		for c.blockLen == 0 && len(b) > b3BlockLen {
			c.compress(b[:b3BlockLen])
			b = b[b3BlockLen:]
		}
		n := copy(c.block[c.blockLen:], b)
		c.blockLen += n
		b = b[n:]
	}
}

func (c *b3Chunk) output() b3Output {
	o := b3Output{cv: c.cv, counter: c.counter, blockLen: uint32(c.blockLen),
		flags: c.baseFlags | c.startFlag() | b3ChunkEnd}
	var last [64]byte
	copy(last[:], c.block[:c.blockLen])
	b3Words(&o.block, last[:])
	return o
}

func b3ParentOutput(left, right *[8]uint32, key *[8]uint32, flags uint32) b3Output {
	o := b3Output{cv: *key, blockLen: b3BlockLen, flags: flags | b3Parent}
	copy(o.block[:8], left[:])
	copy(o.block[8:], right[:])
	return o
}

// Blake3 is a BLAKE3 hasher; Sum gives the 32 byte digest, XOF any length
type Blake3 struct {
	key   [8]uint32
	flags uint32
	chunk b3Chunk
	stack [54][8]uint32 // 2^54 chunks is more than 2^64 bytes
	n     int
}

// NewBlake3 makes a BLAKE3 hasher of the default hash mode
func NewBlake3() *Blake3 {
	h := &Blake3{key: b3IV}
	h.Reset()
	return h
}

var _ gohash.Hash = (*Blake3)(nil)

func (h *Blake3) Reset() {
	h.chunk = b3Chunk{cv: h.key, baseFlags: h.flags}
	h.n = 0
}

func (h *Blake3) Size() int      { return b3OutLen }
func (h *Blake3) BlockSize() int { return b3BlockLen }

// addChunk pushes the chaining value of a complete chunk; total is the
// number of chunks so far. Every trailing zero bit of total is a subtree
// that is complete now and is merged into its parent.
func (h *Blake3) addChunk(cv [8]uint32, total uint64) {
	for total&1 == 0 {
		h.n--
		o := b3ParentOutput(&h.stack[h.n], &cv, &h.key, h.flags)
		cv = o.chainingValue()
		total >>= 1
	}
	h.stack[h.n] = cv
	h.n++
}

func (h *Blake3) Write(b []byte) (int, error) {
	n := len(b)
	for len(b) > 0 {
		// A full chunk is finished only when more input comes: the last
		// chunk is compressed differently if it is the root
		if h.chunk.len() == b3ChunkLen {
			o := h.chunk.output()
			total := h.chunk.counter + 1
			h.addChunk(o.chainingValue(), total)
			h.chunk = b3Chunk{cv: h.key, counter: total, baseFlags: h.flags}
		}
		k := min(b3ChunkLen-h.chunk.len(), len(b))
		h.chunk.update(b[:k])
		b = b[k:]
	}
	return n, nil
}

func (h *Blake3) rootOutput() b3Output {
	o := h.chunk.output()
	for i := h.n - 1; i >= 0; i-- {
		cv := o.chainingValue()
		o = b3ParentOutput(&h.stack[i], &cv, &h.key, h.flags)
	}
	return o
}

// Sum appends the 32 byte digest; the state does not change
func (h *Blake3) Sum(b []byte) []byte {
	var out [b3OutLen]byte
	h.XOF(out[:])
	return append(b, out[:]...)
}

// XOF fills out with the output of any length; its first 32 bytes are Sum
func (h *Blake3) XOF(out []byte) {
	o := h.rootOutput()
	o.rootBytes(out)
}
