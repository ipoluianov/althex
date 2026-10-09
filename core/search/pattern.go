// Package search finds byte patterns, text, numbers and strings in big data
// (FIND-1..FIND-8). The data is an io.ReaderAt safe for concurrent use, so
// the chunks of it are searched in parallel on all cores; the results come
// in the order of offsets all the same.
package search

import (
	"errors"
	"fmt"
	"math/bits"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	ErrEmptyPattern = errors.New("the pattern is empty")
	ErrNoCaseFold   = errors.New("the encoding does not allow a case-insensitive search")
)

// byteSet is a set of byte values, one bit per value
type byteSet [4]uint64

func (s *byteSet) add(b byte)      { s[b>>6] |= 1 << (b & 63) }
func (s *byteSet) has(b byte) bool { return s[b>>6]&(1<<(b&63)) != 0 }
func (s *byteSet) count() int {
	return bits.OnesCount64(s[0]) + bits.OnesCount64(s[1]) + bits.OnesCount64(s[2]) + bits.OnesCount64(s[3])
}
func (s *byteSet) single() (byte, bool) {
	if s.count() != 1 {
		return 0, false
	}
	for i, w := range s {
		if w != 0 {
			return byte(i*64 + bits.TrailingZeros64(w)), true
		}
	}
	return 0, false
}

// values lists the members in increasing order
func (s *byteSet) values() []byte {
	var v []byte
	for i := 0; i < 256; i++ {
		if s.has(byte(i)) {
			v = append(v, byte(i))
		}
	}
	return v
}

// Pattern is what to find: Len bytes, each with a set of allowed values.
// The zero Pattern is empty and finds nothing.
type Pattern struct {
	sets []byteSet
}

func (p Pattern) Len() int { return len(p.sets) }

// Match tells if b starts with a match of the pattern
func (p Pattern) Match(b []byte) bool {
	if len(b) < len(p.sets) {
		return false
	}
	for i := range p.sets {
		if !p.sets[i].has(b[i]) {
			return false
		}
	}
	return true
}

// String shows the pattern as hex: "4D 5A ?? 4?"; a position that is not a
// byte with wildcard nibbles (a case-insensitive letter) is shown as [..]
func (p Pattern) String() string {
	var sb strings.Builder
	for i := range p.sets {
		if i > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(p.sets[i].hex())
	}
	return sb.String()
}

func (s *byteSet) hex() string {
	const digits = "0123456789ABCDEF"
	if b, ok := s.single(); ok {
		return string([]byte{digits[b>>4], digits[b&15]})
	}
	// A byte with wildcard nibbles: try both kinds of masks
	if s.count() == 256 {
		return "??"
	}
	v := s.values()
	if len(v) == 16 {
		if v[0]&15 == 0 && v[15]-v[0] == 15 {
			return string([]byte{digits[v[0]>>4], '?'})
		}
		lo := v[0] & 15
		low := true
		for i, b := range v {
			if b&15 != lo || b>>4 != byte(i) {
				low = false
				break
			}
		}
		if low {
			return string([]byte{'?', digits[lo]})
		}
	}
	parts := make([]string, len(v))
	for i, b := range v {
		parts[i] = string([]byte{digits[b>>4], digits[b&15]})
	}
	return "[" + strings.Join(parts, "|") + "]"
}

// Exact is the pattern of exactly these bytes
func Exact(b []byte) Pattern {
	p := Pattern{sets: make([]byteSet, len(b))}
	for i, c := range b {
		p.sets[i].add(c)
	}
	return p
}

// ParseHex parses a hex sequence with a mask (FIND-1): "4D 5A ?? 50",
// "4D5A", "4? ?5", "0x4D 0x5A". '?' is a wildcard nibble, "??" a wildcard
// byte. Spaces (and commas) are optional between whole bytes, but a byte
// cannot be split by them.
func ParseHex(s string) (Pattern, error) {
	var p Pattern
	fields := strings.FieldsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || r == ',' })
	for _, f := range fields {
		if len(f) >= 2 && f[0] == '0' && (f[1] == 'x' || f[1] == 'X') {
			f = f[2:]
			if f == "" {
				return Pattern{}, fmt.Errorf("no digits after 0x in %q", s)
			}
		}
		if len(f)%2 != 0 {
			return Pattern{}, fmt.Errorf("an odd number of hex digits in %q", f)
		}
		for i := 0; i < len(f); i += 2 {
			hi, ok1 := nibble(f[i])
			lo, ok2 := nibble(f[i+1])
			if !ok1 || !ok2 {
				return Pattern{}, fmt.Errorf("not a hex byte: %q", f[i:i+2])
			}
			var set byteSet
			for v := 0; v < 256; v++ {
				if (hi < 0 || v>>4 == hi) && (lo < 0 || v&15 == lo) {
					set.add(byte(v))
				}
			}
			p.sets = append(p.sets, set)
		}
	}
	if len(p.sets) == 0 {
		return Pattern{}, ErrEmptyPattern
	}
	return p, nil
}

// nibble is the value of a hex digit, -1 for '?'
func nibble(c byte) (int, bool) {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0'), true
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10, true
	case c == '?':
		return -1, true
	}
	return 0, false
}

// Text is the pattern of the text in an encoding (FIND-2). When
// caseInsensitive, each position allows the bytes of all the case variants
// of its character whose encoding has the same length as the character's
// own; a character without such variants must match exactly. The sets are
// per byte, so with multibyte characters a mix of the variants' bytes
// matches too; that is rare in real data and keeps the search fast.
func Text(s string, encode func(string) ([]byte, error), caseInsensitive bool) (Pattern, error) {
	if s == "" {
		return Pattern{}, ErrEmptyPattern
	}
	whole, err := encode(s)
	if err != nil {
		return Pattern{}, err
	}
	if len(whole) == 0 {
		return Pattern{}, ErrEmptyPattern
	}
	if !caseInsensitive {
		return Exact(whole), nil
	}
	var p Pattern
	var joined []byte
	for _, r := range s {
		own, err := encode(string(r))
		if err != nil {
			return Pattern{}, err
		}
		joined = append(joined, own...)
		sets := make([]byteSet, len(own))
		for i, b := range own {
			sets[i].add(b)
		}
		if r != utf8.RuneError {
			for v := unicode.SimpleFold(r); v != r; v = unicode.SimpleFold(v) {
				enc, err := encode(string(v))
				if err != nil || len(enc) != len(own) {
					continue // the encoding has no such character, or it does not line up
				}
				for i, b := range enc {
					sets[i].add(b)
				}
			}
		}
		p.sets = append(p.sets, sets...)
	}
	// An encoding with state (a BOM, shifts) gives other bytes for the
	// characters one by one than for the whole text
	if string(joined) != string(whole) {
		return Pattern{}, ErrNoCaseFold
	}
	return p, nil
}
