package bootimg

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"hash/crc64"
	"io"
)

// LZMA, LZMA2 and the xz container, decoding only. The LZMA decoder
// follows the specification of the LZMA SDK (lzma-specification.txt,
// LzmaSpec.cpp); xz is the .xz file format 1.2.1 with the LZMA2 filter
// alone, which is what the initramfs tools and Android write. The branch
// filters (BCJ), used for some kernel images, are not supported.

var errLZMA = errors.New("lzma: corrupt data")

const (
	lzmaStates      = 12
	lzmaPosBitsMax  = 4
	lzmaMatchMinLen = 2
	lzmaMaxWindow   = 1 << 30 // the largest window kept, however large the dictionary claimed
	probInit        = 1 << 10
)

// ---- the range decoder ----

type rangeDec struct {
	r     io.ByteReader
	rng   uint32
	code  uint32
	err   error
	fresh bool
}

func (rc *rangeDec) byte() byte {
	b, err := rc.r.ReadByte()
	if err != nil && rc.err == nil {
		rc.err = errLZMA
	}
	return b
}

func (rc *rangeDec) init(r io.ByteReader) error {
	rc.r, rc.err = r, nil
	rc.rng, rc.code = 0xFFFFFFFF, 0
	if rc.byte() != 0 {
		return errLZMA
	}
	for range 4 {
		rc.code = rc.code<<8 | uint32(rc.byte())
	}
	if rc.code == rc.rng || rc.err != nil {
		return errLZMA
	}
	return nil
}

func (rc *rangeDec) normalize() {
	if rc.rng < 1<<24 {
		rc.rng <<= 8
		rc.code = rc.code<<8 | uint32(rc.byte())
	}
}

func (rc *rangeDec) bit(p *uint16) uint32 {
	v := uint32(*p)
	bound := (rc.rng >> 11) * v
	var b uint32
	if rc.code < bound {
		v += ((1 << 11) - v) >> 5
		rc.rng = bound
	} else {
		v -= v >> 5
		rc.code -= bound
		rc.rng -= bound
		b = 1
	}
	*p = uint16(v)
	rc.normalize()
	return b
}

func (rc *rangeDec) direct(n int) uint32 {
	var res uint32
	for ; n > 0; n-- {
		rc.rng >>= 1
		rc.code -= rc.rng
		t := 0 - (rc.code >> 31)
		rc.code += rc.rng & t
		if rc.code == rc.rng {
			rc.err = errLZMA
		}
		rc.normalize()
		res = res<<1 + t + 1
	}
	return res
}

func (rc *rangeDec) tree(probs []uint16, bits int) uint32 {
	m := uint32(1)
	for range bits {
		m = m<<1 + rc.bit(&probs[m])
	}
	return m - 1<<bits
}

func (rc *rangeDec) reverse(probs []uint16, bits int) uint32 {
	m, sym := uint32(1), uint32(0)
	for i := range bits {
		b := rc.bit(&probs[m])
		m = m<<1 + b
		sym |= b << i
	}
	return sym
}

// ---- the window: the dictionary and the bytes not read yet ----

type window struct {
	buf    []byte
	pos    int // where the next byte goes
	size   int
	full   bool // buf has wrapped
	total  int64
	unread int
}

func newWindow(dict uint32) *window {
	size := int(min(max(int64(dict), 1<<16), lzmaMaxWindow))
	return &window{size: size}
}

func (w *window) reset() { w.buf, w.pos, w.full = w.buf[:0], 0, false }

func (w *window) put(b byte) {
	if !w.full && len(w.buf) < w.size {
		w.buf = append(w.buf, b)
		w.pos++
	} else {
		w.buf[w.pos] = b
		w.pos++
	}
	if w.pos == w.size {
		w.pos, w.full = 0, true
	}
	w.total++
	w.unread++
}

// has tells whether a byte dist back (1 is the last one) is there
func (w *window) has(dist uint32) bool {
	if w.full {
		return int64(dist) <= int64(w.size)
	}
	return int64(dist) <= int64(w.pos)
}

func (w *window) get(dist uint32) byte {
	i := w.pos - int(dist)
	if i < 0 {
		i += w.size
	}
	return w.buf[i]
}

func (w *window) empty() bool { return !w.full && w.pos == 0 }

// read copies out the bytes not read yet
func (w *window) read(p []byte) int {
	n := 0
	for n < len(p) && w.unread > 0 {
		start := w.pos - w.unread
		if start < 0 {
			start += w.size
		}
		end := start + w.unread
		if end > len(w.buf) {
			end = len(w.buf)
		}
		k := copy(p[n:], w.buf[start:end])
		n += k
		w.unread -= k
	}
	return n
}

// room is how many bytes may be decoded before the unread ones are read
func (w *window) room() int { return w.size/2 - w.unread }

// ---- the LZMA decoder ----

type lenDec struct {
	choice, choice2 uint16
	low, mid        [1 << lzmaPosBitsMax][1 << 3]uint16
	high            [1 << 8]uint16
}

func (l *lenDec) reset() {
	l.choice, l.choice2 = probInit, probInit
	for i := range l.low {
		fill(l.low[i][:])
		fill(l.mid[i][:])
	}
	fill(l.high[:])
}

func (l *lenDec) decode(rc *rangeDec, posState uint32) uint32 {
	if rc.bit(&l.choice) == 0 {
		return rc.tree(l.low[posState][:], 3)
	}
	if rc.bit(&l.choice2) == 0 {
		return 8 + rc.tree(l.mid[posState][:], 3)
	}
	return 16 + rc.tree(l.high[:], 8)
}

func fill(p []uint16) {
	for i := range p {
		p[i] = probInit
	}
}

type lzmaDec struct {
	lc, lp, pb uint32
	literal    []uint16
	posSlot    [4][1 << 6]uint16
	posDec     [1 + 128 - 14]uint16
	align      [1 << 4]uint16
	isMatch    [lzmaStates << lzmaPosBitsMax]uint16
	isRep      [lzmaStates]uint16
	isRepG0    [lzmaStates]uint16
	isRepG1    [lzmaStates]uint16
	isRepG2    [lzmaStates]uint16
	isRep0Long [lzmaStates << lzmaPosBitsMax]uint16
	lenDec     lenDec
	repLenDec  lenDec
	state      uint32
	rep        [4]uint32
	dict       uint32
}

// setProps sets lc, lp, pb from the properties byte
func (d *lzmaDec) setProps(b byte) error {
	if b >= 9*5*5 {
		return errLZMA
	}
	d.lc, d.lp, d.pb = uint32(b%9), uint32(b/9%5), uint32(b/45)
	return nil
}

func (d *lzmaDec) reset() {
	n := 0x300 << (d.lc + d.lp)
	if cap(d.literal) < n {
		d.literal = make([]uint16, n)
	}
	d.literal = d.literal[:n]
	fill(d.literal)
	for i := range d.posSlot {
		fill(d.posSlot[i][:])
	}
	fill(d.posDec[:])
	fill(d.align[:])
	fill(d.isMatch[:])
	fill(d.isRep[:])
	fill(d.isRepG0[:])
	fill(d.isRepG1[:])
	fill(d.isRepG2[:])
	fill(d.isRep0Long[:])
	d.lenDec.reset()
	d.repLenDec.reset()
	d.state, d.rep = 0, [4]uint32{}
}

func (d *lzmaDec) literalByte(rc *rangeDec, w *window) {
	prev := uint32(0)
	if !w.empty() {
		prev = uint32(w.get(1))
	}
	ls := (uint32(w.total)&(1<<d.lp-1))<<d.lc + prev>>(8-d.lc)
	probs := d.literal[0x300*ls : 0x300*ls+0x300]
	sym := uint32(1)
	if d.state >= 7 && w.has(d.rep[0]+1) {
		match := uint32(w.get(d.rep[0] + 1))
		for sym < 0x100 {
			mb := (match >> 7) & 1
			match <<= 1
			b := rc.bit(&probs[(1+mb)<<8+sym])
			sym = sym<<1 | b
			if mb != b {
				break
			}
		}
	}
	for sym < 0x100 {
		sym = sym<<1 | rc.bit(&probs[sym])
	}
	w.put(byte(sym))
}

func (d *lzmaDec) distance(rc *rangeDec, l uint32) uint32 {
	ls := min(l, 3)
	slot := rc.tree(d.posSlot[ls][:], 6)
	if slot < 4 {
		return slot
	}
	nd := int(slot>>1) - 1
	dist := (2 | slot&1) << nd
	if slot < 14 {
		dist += rc.reverse(d.posDec[dist-slot:], nd)
	} else {
		dist += rc.direct(nd-4) << 4
		dist += rc.reverse(d.align[:], 4)
	}
	return dist
}

// errEndMarker - the stream ended with its marker
var errEndMarker = errors.New("lzma end marker")

// run decodes into w until remaining bytes are decoded (-1 - unknown),
// the end marker, or the room of the window is used (a match may go past
// it: the room is half the window, a match is at most 273 bytes). It
// returns the remaining count.
func (d *lzmaDec) run(rc *rangeDec, w *window, remaining int64) (int64, error) {
	copyMatch := func(n uint32) {
		for ; n > 0; n-- {
			w.put(w.get(d.rep[0] + 1))
		}
	}
	for w.room() > 0 {
		if rc.err != nil {
			return remaining, rc.err
		}
		if remaining == 0 {
			// The end marker may follow; without one the code is zero
			if rc.code == 0 {
				return 0, nil
			}
		}
		posState := uint32(w.total) & (1<<d.pb - 1)
		s := d.state
		if rc.bit(&d.isMatch[s<<lzmaPosBitsMax+posState]) == 0 {
			if remaining == 0 {
				return 0, errLZMA
			}
			d.literalByte(rc, w)
			switch {
			case s < 4:
				d.state = 0
			case s < 10:
				d.state = s - 3
			default:
				d.state = s - 6
			}
			if remaining > 0 {
				remaining--
			}
			continue
		}
		var l uint32
		if rc.bit(&d.isRep[s]) != 0 {
			if remaining == 0 || w.empty() {
				return remaining, errLZMA
			}
			if rc.bit(&d.isRepG0[s]) == 0 {
				if rc.bit(&d.isRep0Long[s<<lzmaPosBitsMax+posState]) == 0 {
					if s < 7 {
						d.state = 9
					} else {
						d.state = 11
					}
					if !w.has(d.rep[0] + 1) {
						return remaining, errLZMA
					}
					w.put(w.get(d.rep[0] + 1))
					if remaining > 0 {
						remaining--
					}
					continue
				}
			} else {
				var dist uint32
				if rc.bit(&d.isRepG1[s]) == 0 {
					dist = d.rep[1]
				} else {
					if rc.bit(&d.isRepG2[s]) == 0 {
						dist = d.rep[2]
					} else {
						dist = d.rep[3]
						d.rep[3] = d.rep[2]
					}
					d.rep[2] = d.rep[1]
				}
				d.rep[1] = d.rep[0]
				d.rep[0] = dist
			}
			l = d.repLenDec.decode(rc, posState)
			if s < 7 {
				d.state = 8
			} else {
				d.state = 11
			}
		} else {
			d.rep[3], d.rep[2], d.rep[1] = d.rep[2], d.rep[1], d.rep[0]
			l = d.lenDec.decode(rc, posState)
			if s < 7 {
				d.state = 7
			} else {
				d.state = 10
			}
			d.rep[0] = d.distance(rc, l)
			if d.rep[0] == 0xFFFFFFFF {
				if rc.err != nil {
					return remaining, rc.err
				}
				if rc.code != 0 {
					return remaining, errLZMA
				}
				return remaining, errEndMarker
			}
			if remaining == 0 || d.rep[0] >= d.dict {
				return remaining, errLZMA
			}
		}
		if rc.err != nil {
			return remaining, rc.err
		}
		if !w.has(d.rep[0] + 1) {
			return remaining, errLZMA
		}
		l += lzmaMatchMinLen
		if remaining >= 0 && int64(l) > remaining {
			return remaining, errLZMA
		}
		copyMatch(l)
		if remaining > 0 {
			remaining -= int64(l)
		}
	}
	return remaining, rc.err
}

// lzmaReader decodes the .lzma format ("LZMA alone"): the properties, the
// dictionary size, the size (all ones - unknown, the end marker ends it)
type lzmaReader struct {
	rc        rangeDec
	d         lzmaDec
	w         *window
	remaining int64
	err       error
}

func newLZMAReader(r *bufio.Reader) (*lzmaReader, error) {
	var h [13]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return nil, errLZMA
	}
	z := &lzmaReader{}
	if err := z.d.setProps(h[0]); err != nil {
		return nil, err
	}
	z.d.dict = max(binary.LittleEndian.Uint32(h[1:]), 1<<12)
	z.remaining = int64(binary.LittleEndian.Uint64(h[5:]))
	if binary.LittleEndian.Uint64(h[5:]) == ^uint64(0) {
		z.remaining = -1
	} else if z.remaining < 0 {
		return nil, errLZMA
	}
	z.w = newWindow(z.d.dict)
	z.d.reset()
	if err := z.rc.init(r); err != nil {
		return nil, err
	}
	return z, nil
}

func (z *lzmaReader) Read(p []byte) (int, error) {
	for z.w.unread == 0 {
		if z.err != nil {
			return 0, z.err
		}
		if z.remaining == 0 {
			// Done; the marker, if any, is not read
			return 0, io.EOF
		}
		var err error
		z.remaining, err = z.d.run(&z.rc, z.w, z.remaining)
		switch {
		case err == errEndMarker:
			z.err = io.EOF
		case err != nil:
			z.err = err
		case z.remaining == 0:
			z.err = io.EOF
		}
	}
	return z.w.read(p), nil
}

// ---- LZMA2 ----

type lzma2Reader struct {
	r        io.ByteReader
	d        lzmaDec
	w        *window
	rc       rangeDec
	chunk    []byte
	left     int64 // the bytes of the chunk not decoded yet
	raw      bool  // the chunk is not compressed
	rawSrc   *bytes.Reader
	propsSet bool
	err      error
}

// lzma2Dict is the dictionary size of the LZMA2 filter properties byte
func lzma2Dict(b byte) (uint32, error) {
	if b > 40 {
		return 0, errLZMA
	}
	if b == 40 {
		return 0xFFFFFFFF, nil
	}
	return (2 | uint32(b)&1) << (b/2 + 11), nil
}

func newLZMA2Reader(r io.ByteReader, dict uint32) *lzma2Reader {
	z := &lzma2Reader{r: r, w: newWindow(dict)}
	z.d.dict = dict
	return z
}

func (z *lzma2Reader) readN(n int) ([]byte, error) {
	if cap(z.chunk) < n {
		z.chunk = make([]byte, n)
	}
	b := z.chunk[:n]
	for i := range b {
		c, err := z.r.ReadByte()
		if err != nil {
			return nil, errLZMA
		}
		b[i] = c
	}
	return b, nil
}

// next reads the header of the next chunk; io.EOF at the end mark
func (z *lzma2Reader) next() error {
	c, err := z.r.ReadByte()
	if err != nil {
		return errLZMA
	}
	if c == 0 {
		return io.EOF
	}
	if c == 1 || c == 2 {
		h, err := z.readN(2)
		if err != nil {
			return err
		}
		n := int(binary.BigEndian.Uint16(h)) + 1
		if c == 1 {
			z.w.reset()
		}
		data, err := z.readN(n)
		if err != nil {
			return err
		}
		z.raw, z.rawSrc, z.left = true, bytes.NewReader(data), int64(n)
		return nil
	}
	if c < 0x80 {
		return fmt.Errorf("lzma2: control byte 0x%02X", c)
	}
	h, err := z.readN(4)
	if err != nil {
		return err
	}
	unpacked := int64(c&0x1F)<<16 + int64(binary.BigEndian.Uint16(h)) + 1
	packed := int(binary.BigEndian.Uint16(h[2:])) + 1
	mode := (c >> 5) & 3
	if mode == 3 {
		z.w.reset()
	}
	if mode >= 2 {
		p, err := z.r.ReadByte()
		if err != nil {
			return errLZMA
		}
		if err := z.d.setProps(p); err != nil {
			return err
		}
		if z.d.lc+z.d.lp > 4 {
			return errLZMA
		}
		z.propsSet = true
	}
	if !z.propsSet {
		return errLZMA
	}
	if mode >= 1 {
		z.d.reset()
	}
	data, err := z.readN(packed)
	if err != nil {
		return err
	}
	src := append([]byte(nil), data...)
	if err := z.rc.init(bytes.NewReader(src)); err != nil {
		return err
	}
	z.raw, z.left = false, unpacked
	return nil
}

func (z *lzma2Reader) Read(p []byte) (int, error) {
	for z.w.unread == 0 {
		if z.err != nil {
			return 0, z.err
		}
		if z.left == 0 {
			if err := z.next(); err != nil {
				z.err = err
				continue
			}
		}
		if z.raw {
			for z.left > 0 && z.w.room() > 0 {
				c, _ := z.rawSrc.ReadByte()
				z.w.put(c)
				z.left--
			}
			continue
		}
		var err error
		z.left, err = z.d.run(&z.rc, z.w, z.left)
		if err != nil {
			if err == errEndMarker {
				err = errLZMA // LZMA2 has no end marker
			}
			z.err = err
		}
	}
	return z.w.read(p), nil
}

// ---- xz ----

// countReader counts the bytes read, for the padding of the blocks
type countReader struct {
	r *bufio.Reader
	n int64
}

func (c *countReader) ReadByte() (byte, error) {
	b, err := c.r.ReadByte()
	if err == nil {
		c.n++
	}
	return b, err
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

type xzReader struct {
	r     *countReader
	check byte
	block *lzma2Reader
	hash  hash.Hash
	start int64 // where the block started
	err   error
}

var crc64Table = crc64.MakeTable(crc64.ECMA)

func newXZReader(r *bufio.Reader) (*xzReader, error) {
	var h [12]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return nil, errLZMA
	}
	if !bytes.Equal(h[:6], []byte{0xFD, '7', 'z', 'X', 'Z', 0}) || h[6] != 0 || h[7] > 15 {
		return nil, fmt.Errorf("xz: not an xz stream")
	}
	if crc32.ChecksumIEEE(h[6:8]) != binary.LittleEndian.Uint32(h[8:]) {
		return nil, fmt.Errorf("xz: the stream header CRC is wrong")
	}
	return &xzReader{r: &countReader{r: r}, check: h[7]}, nil
}

// checkSize is the size of the check of each block
func (z *xzReader) checkSize() int {
	switch {
	case z.check == 0:
		return 0
	case z.check <= 3:
		return 4
	case z.check <= 6:
		return 8
	case z.check <= 9:
		return 16
	case z.check <= 12:
		return 32
	}
	return 64
}

func (z *xzReader) vli(b []byte) (uint64, []byte, error) {
	var v uint64
	for i := 0; i < 9 && i < len(b); i++ {
		v |= uint64(b[i]&0x7F) << (7 * i)
		if b[i]&0x80 == 0 {
			return v, b[i+1:], nil
		}
	}
	return 0, nil, errLZMA
}

// nextBlock reads the header of a block; io.EOF at the index
func (z *xzReader) nextBlock() error {
	z.start = z.r.n
	sz, err := z.r.ReadByte()
	if err != nil {
		return errLZMA
	}
	if sz == 0 {
		return io.EOF // the index: the blocks are over
	}
	h := make([]byte, int(sz)*4+4)
	h[0] = sz
	if _, err := io.ReadFull(z.r, h[1:]); err != nil {
		return errLZMA
	}
	if crc32.ChecksumIEEE(h[:len(h)-4]) != binary.LittleEndian.Uint32(h[len(h)-4:]) {
		return fmt.Errorf("xz: a block header CRC is wrong")
	}
	flags := h[1]
	b := h[2 : len(h)-4]
	if flags&0x40 != 0 {
		if _, b, err = z.vli(b); err != nil {
			return err
		}
	}
	if flags&0x80 != 0 {
		if _, b, err = z.vli(b); err != nil {
			return err
		}
	}
	filters := int(flags&3) + 1
	var dict uint32
	for i := range filters {
		var id, n uint64
		if id, b, err = z.vli(b); err != nil {
			return err
		}
		if n, b, err = z.vli(b); err != nil {
			return err
		}
		if n > uint64(len(b)) {
			return errLZMA
		}
		props := b[:n]
		b = b[n:]
		if id != 0x21 || i != filters-1 {
			return fmt.Errorf("xz: filter 0x%X is not supported (only LZMA2)", id)
		}
		if len(props) != 1 {
			return errLZMA
		}
		if dict, err = lzma2Dict(props[0]); err != nil {
			return err
		}
	}
	z.block = newLZMA2Reader(z.r, dict)
	switch z.checkSize() {
	case 4:
		if z.check == 1 {
			z.hash = crc32.NewIEEE()
		}
	case 8:
		if z.check == 4 {
			z.hash = crc64.New(crc64Table)
		}
	default:
		z.hash = nil
	}
	return nil
}

// endBlock reads the padding and the check of a block
func (z *xzReader) endBlock() error {
	if pad := (4 - (z.r.n-z.start)%4) % 4; pad > 0 {
		for range pad {
			if c, err := z.r.ReadByte(); err != nil || c != 0 {
				return errLZMA
			}
		}
	}
	sum := make([]byte, z.checkSize())
	if _, err := io.ReadFull(z.r, sum); err != nil {
		return errLZMA
	}
	if z.hash != nil {
		got := z.hash.Sum(nil)
		// The CRCs are stored little-endian
		for i, j := 0, len(got)-1; i < j; i, j = i+1, j-1 {
			got[i], got[j] = got[j], got[i]
		}
		if !bytes.Equal(got, sum) {
			return fmt.Errorf("xz: the check of a block is wrong")
		}
	}
	z.block = nil
	return nil
}

func (z *xzReader) Read(p []byte) (int, error) {
	for {
		if z.err != nil {
			return 0, z.err
		}
		if z.block == nil {
			if err := z.nextBlock(); err != nil {
				z.err = err // io.EOF at the index: the rest is not read
				continue
			}
		}
		n, err := z.block.Read(p)
		if n > 0 {
			if z.hash != nil {
				z.hash.Write(p[:n])
			}
			return n, nil
		}
		if err == io.EOF {
			if err := z.endBlock(); err != nil {
				z.err = err
			}
			continue
		}
		z.err = err
	}
}
