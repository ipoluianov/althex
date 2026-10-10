package bootimg

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// LZ4: the legacy format the Linux kernel writes for its images and the
// initramfs (lz4 -l: magic 0x184C2102, then blocks of up to 8 MB, each
// with its compressed size before it), and the frame format
// (lz4/doc/lz4_Frame_format.md: magic 0x184D2204, a descriptor, blocks,
// an end mark). The checksums of the frame format (xxHash32) are skipped.

const (
	lz4LegacyMagic   = 0x184C2102
	lz4FrameMagic    = 0x184D2204
	lz4LegacyBlock   = 8 << 20 // the uncompressed size of a legacy block
	lz4MaxCompressed = lz4LegacyBlock + lz4LegacyBlock/255 + 16
	lz4History       = 64 << 10 // how far back a match of the frame format may go
)

var errLZ4 = errors.New("lz4: corrupt data")

// lz4Block decodes one LZ4 block from src, appending to dst; the bytes of
// dst before are the history a match may refer to. At most max bytes are
// added.
func lz4Block(dst, src []byte, max int) ([]byte, error) {
	limit := len(dst) + max
	i := 0
	for {
		if i >= len(src) {
			return dst, errLZ4
		}
		token := src[i]
		i++
		// Literals
		n := int(token >> 4)
		if n == 15 {
			for {
				if i >= len(src) {
					return dst, errLZ4
				}
				c := src[i]
				i++
				n += int(c)
				if n > len(src) {
					return dst, errLZ4
				}
				if c != 255 {
					break
				}
			}
		}
		if n > len(src)-i || n > limit-len(dst) {
			return dst, errLZ4
		}
		dst = append(dst, src[i:i+n]...)
		i += n
		if i == len(src) {
			return dst, nil // the last sequence has only literals
		}
		// The match
		if len(src)-i < 2 {
			return dst, errLZ4
		}
		off := int(src[i]) | int(src[i+1])<<8
		i += 2
		if off == 0 || off > len(dst) {
			return dst, errLZ4
		}
		n = int(token & 15)
		if n == 15 {
			for {
				if i >= len(src) {
					return dst, errLZ4
				}
				c := src[i]
				i++
				n += int(c)
				if n > limit {
					return dst, errLZ4
				}
				if c != 255 {
					break
				}
			}
		}
		n += 4
		if n > limit-len(dst) {
			return dst, errLZ4
		}
		start := len(dst) - off
		if off >= n {
			dst = append(dst, dst[start:start+n]...)
		} else {
			for k := 0; k < n; k++ {
				dst = append(dst, dst[start+k])
			}
		}
	}
}

// lz4Reader decodes the legacy and frame formats, one stream after
// another while the magics go on
type lz4Reader struct {
	r   *bufio.Reader
	buf []byte // the history and the bytes decoded
	out []byte // the bytes of buf not read yet
	err error

	frame     bool // in a frame (not legacy)
	started   bool
	indep     bool
	blockSum  bool
	contSum   bool
	blockMax  int
	compBuf   []byte
	frameDone bool
}

func newLZ4Reader(r *bufio.Reader) (*lz4Reader, error) {
	z := &lz4Reader{r: r}
	if err := z.header(true); err != nil {
		return nil, err
	}
	return z, nil
}

func (z *lz4Reader) u32() (uint32, error) {
	var b [4]byte
	if _, err := io.ReadFull(z.r, b[:]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(b[:]), nil
}

// header reads the magic of a stream and, for a frame, its descriptor;
// first: there must be one. io.EOF when the data ends (or what follows is
// not LZ4: padding).
func (z *lz4Reader) header(first bool) error {
	for {
		b, err := z.r.Peek(4)
		if err != nil {
			if first {
				return errLZ4
			}
			return io.EOF
		}
		m := binary.LittleEndian.Uint32(b)
		switch {
		case m == lz4LegacyMagic:
			z.r.Discard(4)
			z.frame, z.started = false, true
			return nil
		case m == lz4FrameMagic:
			z.r.Discard(4)
			return z.descriptor()
		case m&0xFFFFFFF0 == 0x184D2A50 && !first:
			// A skippable frame
			z.r.Discard(4)
			n, err := z.u32()
			if err != nil {
				return io.EOF
			}
			if _, err := z.r.Discard(int(n)); err != nil {
				return io.EOF
			}
		default:
			if first {
				return errLZ4
			}
			return io.EOF
		}
	}
}

func (z *lz4Reader) descriptor() error {
	var d [2]byte
	if _, err := io.ReadFull(z.r, d[:]); err != nil {
		return errLZ4
	}
	flg, bd := d[0], d[1]
	if flg>>6 != 1 {
		return fmt.Errorf("lz4: frame version %d", flg>>6)
	}
	if flg&1 != 0 {
		return fmt.Errorf("lz4: frames with a dictionary are not supported")
	}
	z.indep = flg&0x20 != 0
	z.blockSum = flg&0x10 != 0
	z.contSum = flg&0x04 != 0
	switch (bd >> 4) & 7 {
	case 4:
		z.blockMax = 64 << 10
	case 5:
		z.blockMax = 256 << 10
	case 6:
		z.blockMax = 1 << 20
	case 7:
		z.blockMax = 4 << 20
	default:
		return fmt.Errorf("lz4: block size code %d", (bd>>4)&7)
	}
	skip := 1 // the checksum of the header
	if flg&0x08 != 0 {
		skip += 8 // the content size
	}
	if _, err := z.r.Discard(skip); err != nil {
		return errLZ4
	}
	z.frame, z.started, z.frameDone = true, true, false
	z.buf = z.buf[:0]
	return nil
}

// block decodes the next block into z.out; io.EOF at the end
func (z *lz4Reader) block() error {
	for {
		if z.frame && z.frameDone || !z.started {
			if err := z.header(false); err != nil {
				return err
			}
		}
		n, err := z.u32()
		if err != nil {
			if z.frame {
				return errLZ4
			}
			return io.EOF // the legacy format just ends
		}
		if !z.frame {
			switch {
			case n == lz4LegacyMagic:
				continue // the next stream
			case n == lz4FrameMagic:
				if err := z.descriptor(); err != nil {
					return err
				}
				continue
			case n == 0 || n > lz4MaxCompressed:
				// The size of the kernel appended after it, padding: the end
				return io.EOF
			}
			src, err := z.read(int(n))
			if err != nil {
				return io.EOF // what is left is shorter: not a block
			}
			z.buf, err = lz4Block(z.buf[:0], src, lz4LegacyBlock)
			if err != nil {
				return err
			}
			z.out = z.buf
			return nil
		}
		if n == 0 {
			// The end mark
			if z.contSum {
				if _, err := z.r.Discard(4); err != nil {
					return errLZ4
				}
			}
			z.frameDone = true
			continue
		}
		raw := n&0x80000000 != 0
		n &= 0x7FFFFFFF
		if int(n) > z.blockMax {
			return errLZ4
		}
		src, err := z.read(int(n))
		if err != nil {
			return errLZ4
		}
		if z.blockSum {
			if _, err := z.r.Discard(4); err != nil {
				return errLZ4
			}
		}
		// Keep 64 KB of history for dependent blocks
		hist := 0
		if !z.indep && len(z.buf) > 0 {
			hist = min(len(z.buf), lz4History)
			copy(z.buf, z.buf[len(z.buf)-hist:])
		}
		z.buf = z.buf[:hist]
		if raw {
			z.buf = append(z.buf, src...)
		} else if z.buf, err = lz4Block(z.buf, src, z.blockMax); err != nil {
			return err
		}
		z.out = z.buf[hist:]
		return nil
	}
}

// read reads n bytes of compressed data; the buffer grows with what is
// read, not with what a size claims
func (z *lz4Reader) read(n int) ([]byte, error) {
	b := z.compBuf[:0]
	for len(b) < n {
		k := min(n-len(b), 256<<10)
		if cap(b)-len(b) < k {
			b = append(b, make([]byte, k)...)[:len(b)]
		}
		m, err := io.ReadFull(z.r, b[len(b):len(b)+k])
		b = b[:len(b)+m]
		if err != nil {
			z.compBuf = b
			return nil, err
		}
	}
	z.compBuf = b
	return b, nil
}

func (z *lz4Reader) Read(p []byte) (int, error) {
	for len(z.out) == 0 {
		if z.err != nil {
			return 0, z.err
		}
		z.err = z.block()
	}
	n := copy(p, z.out)
	z.out = z.out[n:]
	return n, nil
}
