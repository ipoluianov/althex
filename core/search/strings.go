package search

import (
	"cmp"
	"context"
	"io"
	"slices"
	"unicode"
	"unicode/utf8"
)

// StringsOptions of Strings (FIND-8)
type StringsOptions struct {
	MinLen   int // in characters; 0 - 4
	ASCII    bool
	UTF8     bool
	UTF16LE  bool
	From, To int64 // To 0 - the end (size)
	Workers  int   // 0 - runtime.NumCPU()
	// ChunkSize 0 - 4 MB
	ChunkSize int
}

// Found is a string: Len bytes at Off in the encoding Enc ("ASCII",
// "UTF-8", "UTF-16LE"); Text is at most MaxStringText characters of it
type Found struct {
	Off  int64
	Len  int
	Enc  string
	Text string
}

// MaxStringText is how many characters of a string Found.Text keeps
const MaxStringText = 1000

const (
	EncASCII   = "ASCII"
	EncUTF8    = "UTF-8"
	EncUTF16LE = "UTF-16LE"
)

// The characters of a string, like the strings tool:
//   - ASCII: 0x20..0x7E and tab;
//   - UTF-8: valid UTF-8 of printable characters and tab; a string of
//     ASCII bytes only is reported as ASCII when ASCII is on too;
//   - UTF-16LE: 2-byte units at either parity of printable characters below
//     U+0800 (Latin, Greek, Cyrillic, Hebrew, Arabic...) and tab. CJK is
//     left out on purpose: most random 16-bit values are CJK, so random
//     data would be full of "strings".

func printableASCII(b byte) bool { return b >= 0x20 && b <= 0x7e || b == '\t' }

func printableRune(r rune) bool { return r == '\t' || unicode.IsPrint(r) }

func printableUTF16(u uint16) bool { return u < 0x800 && printableRune(rune(u)) }

// Strings calls onFound for the strings of printable characters at least
// MinLen long (FIND-8), in increasing order of offset (an 8-bit string goes
// before a UTF-16 one at the same offset), from one goroutine; onFound
// returning false stops. The chunks are scanned in parallel; a string
// belongs to the chunk it starts in, which reads on past its end as far as
// the string goes.
func Strings(ctx context.Context, r io.ReaderAt, size int64, opt StringsOptions, onFound func(Found) bool, progress func(done int64)) error {
	from, to := span(opt.From, opt.To, size)
	if opt.MinLen <= 0 {
		opt.MinLen = 4
	}
	if !opt.ASCII && !opt.UTF8 && !opt.UTF16LE || from >= to {
		return ctx.Err()
	}
	cs := chunkSize(opt.ChunkSize)
	chunks := (to - from + cs - 1) / cs

	work := func(ctx context.Context, c int64) ([]Found, error) {
		s := from + c*cs
		e := min(s+cs, to)
		sc := scanner{r: r, from: from, to: to, s: s, e: e, minLen: opt.MinLen}
		if opt.ASCII || opt.UTF8 {
			if err := sc.scan8(opt.UTF8, opt.ASCII); err != nil {
				return nil, err
			}
		}
		if opt.UTF16LE {
			for parity := int64(0); parity < 2; parity++ {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if err := sc.scan16(parity); err != nil {
					return nil, err
				}
			}
		}
		if opt.UTF16LE {
			// The 8-bit strings are first, so they stay first at the same offset
			slices.SortStableFunc(sc.found, func(a, b Found) int { return cmp.Compare(a.Off, b.Off) })
		}
		return sc.found, nil
	}
	emit := func(c int64, found []Found) bool {
		for _, f := range found {
			if !onFound(f) {
				return false
			}
		}
		if progress != nil {
			progress(min(from+(c+1)*cs, to) - from)
		}
		return true
	}
	return ordered(ctx, opt.Workers, chunks, work, emit)
}

// scanner finds the strings that start in the chunk [s, e) of [from, to)
type scanner struct {
	r        io.ReaderAt
	from, to int64
	s, e     int64
	minLen   int
	found    []Found
	text     []byte
	base     int64 // the offset of buf
	buf      []byte
	bufp     *[]byte
}

// The window: the chunk and a bit more is read at once, then more pieces
// as a string goes on; the bytes before the current place are dropped, so a
// string of any length takes little memory
const (
	scanTail  = 4 << 10
	scanPiece = 64 << 10
)

// load reads the window [off, min(e+scanTail, to))
func (sc *scanner) load(off int64) error {
	n := int(min(sc.e+scanTail, sc.to) - off)
	if sc.bufp == nil {
		sc.bufp = getBuf(n)
	} else if cap(*sc.bufp) < n {
		putBuf(sc.bufp)
		sc.bufp = getBuf(n)
	}
	sc.buf = (*sc.bufp)[:n]
	sc.base = off
	return readFull(sc.r, sc.buf, off)
}

func (sc *scanner) release() {
	if sc.bufp != nil {
		putBuf(sc.bufp)
		sc.bufp = nil
	}
}

// window returns the bytes from off, at least n of them as far as to
// allows; the fast way is inlined, as it is called for every character
func (sc *scanner) window(off int64, n int) ([]byte, error) {
	if k := int(off - sc.base); k+n <= len(sc.buf) {
		return sc.buf[k:], nil
	}
	return sc.avail(off, n)
}

// avail makes the window hold [off, off+n) as far as to allows, dropping
// what is before off; it returns the bytes from off
func (sc *scanner) avail(off int64, n int) ([]byte, error) {
	end := min(off+int64(n), sc.to)
	if end > sc.base+int64(len(sc.buf)) {
		keep := sc.buf[off-sc.base:]
		want := len(keep) + int(min(scanPiece, sc.to-(sc.base+int64(len(sc.buf)))))
		want = max(want, int(end-off))
		nb := getBuf(want)
		copy(*nb, keep)
		if err := readFull(sc.r, (*nb)[len(keep):], off+int64(len(keep))); err != nil {
			putBuf(nb)
			return nil, err
		}
		putBuf(sc.bufp)
		sc.bufp, sc.buf, sc.base = nb, *nb, off
	}
	return sc.buf[off-sc.base : end-sc.base], nil
}

// run is the string being scanned
type run struct {
	on       bool
	start    int64
	chars    int
	allASCII bool
}

func (sc *scanner) finish(ru *run, end int64, enc string) {
	if ru.on && ru.start >= sc.s && ru.chars >= sc.minLen {
		sc.found = append(sc.found, Found{Off: ru.start, Len: int(end - ru.start), Enc: enc, Text: string(sc.text)})
	}
	ru.on = false
}

// begin starts a string; its text is kept only when the string is ours
func (sc *scanner) begin(ru *run, pos int64) {
	*ru = run{on: true, start: pos, allASCII: true}
	sc.text = sc.text[:0]
}

// add adds a character to the text when the string is ours and not too long
func (sc *scanner) add(ru *run, r rune) {
	ru.chars++
	if ru.chars <= MaxStringText && ru.start >= sc.s {
		if r < utf8.RuneSelf {
			sc.text = append(sc.text, byte(r))
		} else {
			sc.text = utf8.AppendRune(sc.text, r)
		}
	}
}

// asciiPrint tells the printable ASCII bytes: 0x20..0x7E and tab
var asciiPrint = func() (t [256]bool) {
	for c := range t {
		t[c] = printableASCII(byte(c))
	}
	return t
}()

// scan8 finds the 8-bit strings: ASCII, or UTF-8 when utf8 is on.
//
// UTF-8 is parsed from the left: a valid character is stepped over whole,
// anything else one byte. Every byte that is not a continuation byte
// (10xxxxxx) is visited by such a parse wherever it starts, and the parse
// catches up with the one from From within 4 bytes. So the scan starts 8
// bytes before the chunk: from s-4 on it agrees with the parse from From,
// and that is enough to know whether a string runs into the chunk from
// before it.
func (sc *scanner) scan8(utf8On, asciiOn bool) error {
	defer sc.release()
	back := int64(1)
	if utf8On {
		back = 8
	}
	pos := max(sc.from, sc.s-back)
	if err := sc.load(pos); err != nil {
		return err
	}
	enc8 := func(ru *run) string {
		if utf8On && !(asciiOn && ru.allASCII) {
			return EncUTF8
		}
		return EncASCII
	}
	var ru run
	buf, base := sc.buf, sc.base
	for pos < sc.to {
		k := int(pos - base)
		// The window is short of a whole character only at its end
		if k+utf8.UTFMax > len(buf) && base+int64(len(buf)) < sc.to {
			if _, err := sc.avail(pos, utf8.UTFMax); err != nil {
				return err
			}
			buf, base = sc.buf, sc.base
			k = int(pos - base)
		}
		if !ru.on {
			if pos >= sc.e {
				break // a string from here is the next chunk's
			}
			// Skip what is not printable at once
			k0 := k
			for k < len(buf) && buf[k] < utf8.RuneSelf && !asciiPrint[buf[k]] {
				k++
			}
			if k != k0 {
				pos = base + int64(k)
				continue
			}
		} else if ru.start < sc.s && pos >= sc.e {
			break // a string from the chunk before over all of this one
		}
		c := buf[k]
		if c < utf8.RuneSelf {
			if asciiPrint[c] {
				if !ru.on {
					sc.begin(&ru, pos)
				}
				sc.add(&ru, rune(c))
			} else if ru.on {
				sc.finish(&ru, pos, enc8(&ru))
			}
			pos++
			continue
		}
		size := 1
		if utf8On {
			var r rune
			r, size = utf8.DecodeRune(buf[k:])
			if size > 1 && printableRune(r) {
				if !ru.on {
					sc.begin(&ru, pos)
				}
				ru.allASCII = false
				sc.add(&ru, r)
				pos += int64(size)
				continue
			}
		}
		if ru.on {
			sc.finish(&ru, pos, enc8(&ru))
		}
		pos += int64(size)
	}
	sc.finish(&ru, pos, enc8(&ru))
	return nil
}

// scan16 finds the UTF-16LE strings of the units at offsets of the parity
func (sc *scanner) scan16(parity int64) error {
	defer sc.release()
	pos := max(sc.from, sc.s-2)
	if pos%2 != parity {
		pos++
	}
	if pos >= sc.to {
		return nil
	}
	if err := sc.load(pos); err != nil {
		return err
	}
	var ru run
	buf, base := sc.buf, sc.base
	for pos+2 <= sc.to {
		k := int(pos - base)
		if k+2 > len(buf) {
			if _, err := sc.avail(pos, 2); err != nil {
				return err
			}
			buf, base = sc.buf, sc.base
			k = int(pos - base)
		}
		if !ru.on {
			if pos >= sc.e {
				break
			}
			// Skip the units with a high byte of 8 or more at once: most
			// of them in data that is not text
			k0 := k
			for k+2 <= len(buf) && buf[k+1] >= 0x08 {
				k += 2
			}
			if k != k0 {
				pos = base + int64(k)
				continue
			}
		} else if ru.start < sc.s && pos >= sc.e {
			break
		}
		u := uint16(buf[k]) | uint16(buf[k+1])<<8
		if printableUTF16(u) {
			if !ru.on {
				sc.begin(&ru, pos)
			}
			sc.add(&ru, rune(u))
		} else if ru.on {
			sc.finish(&ru, pos, EncUTF16LE)
		}
		pos += 2
	}
	sc.finish(&ru, pos, EncUTF16LE)
	return nil
}
