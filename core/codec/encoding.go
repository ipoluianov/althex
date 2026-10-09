// Package codec converts bytes to and from text: the encodings of the text
// column, the file formats of firmware images (Intel HEX, Motorola
// S-record), and the forms bytes are copied and pasted in (hex strings,
// code arrays, Base64, escaped strings).
package codec

import (
	"errors"
	"fmt"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// ErrInvalidText is returned by Encode for a string that is not valid UTF-8
var ErrInvalidText = errors.New("text is not valid UTF-8")

// Encoding of the text column and of text search
type Encoding interface {
	ID() string   // stable, for settings: "ascii", "utf8", "cp1251", ...
	Name() string // shown in menus: "ASCII", "UTF-8", "Windows-1251", ...
	// Unit is the size of a code unit: 1, or 2 for UTF-16; characters start at
	// offsets that are multiples of it (counted from the start of the document)
	Unit() int
	// Decode returns the character at data[i] and how many bytes it takes.
	// ok is false when no printable character starts there (control bytes,
	// invalid or cut sequences, UTF-8 continuation bytes): the view shows '.'.
	// Must never panic for any i in [0, len(data)).
	Decode(data []byte, i int) (r rune, size int, ok bool)
	// Encode returns the bytes of the text; an error for characters the encoding lacks
	Encode(s string) ([]byte, error)
}

// CharError is returned by Encode for a character the encoding lacks
type CharError struct {
	Encoding string // the name of the encoding
	Char     rune
}

func (e *CharError) Error() string {
	return fmt.Sprintf("%s has no character %q (U+%04X)", e.Encoding, e.Char, e.Char)
}

var encodings = []Encoding{
	newTable("ascii", "ASCII", nil),
	utf8Enc{},
	utf16Enc{be: false},
	utf16Enc{be: true},
	newTable("latin1", "Latin-1 (ISO 8859-1)", charmap.ISO8859_1),
	newTable("cp1252", "Windows-1252", charmap.Windows1252),
	newTable("iso8859-15", "ISO 8859-15", charmap.ISO8859_15),
	newTable("cp1250", "Windows-1250", charmap.Windows1250),
	newTable("iso8859-2", "ISO 8859-2", charmap.ISO8859_2),
	newTable("cp1251", "Windows-1251", charmap.Windows1251),
	newTable("cp866", "CP866 (DOS Cyrillic)", charmap.CodePage866),
	newTable("koi8r", "KOI8-R", charmap.KOI8R),
	newTable("koi8u", "KOI8-U", charmap.KOI8U),
	newTable("iso8859-5", "ISO 8859-5", charmap.ISO8859_5),
	newTable("maccyrillic", "Mac Cyrillic", charmap.MacintoshCyrillic),
	newTable("cp1253", "Windows-1253 (Greek)", charmap.Windows1253),
	newTable("cp1254", "Windows-1254 (Turkish)", charmap.Windows1254),
	newTable("cp1257", "Windows-1257 (Baltic)", charmap.Windows1257),
	newTable("cp437", "CP437 (DOS)", charmap.CodePage437),
	newTable("cp850", "CP850 (DOS Latin-1)", charmap.CodePage850),
	newTable("macroman", "Mac Roman", charmap.Macintosh),
	newTable("ebcdic037", "EBCDIC 037", charmap.CodePage037),
	newTable("ebcdic1047", "EBCDIC 1047", charmap.CodePage1047),
}

// Encodings returns the encodings in menu order, ASCII first
func Encodings() []Encoding {
	return append([]Encoding(nil), encodings...)
}

// ByID returns the encoding with the id; nil if unknown
func ByID(id string) Encoding {
	for _, e := range encodings {
		if e.ID() == id {
			return e
		}
	}
	return nil
}

func printable(r rune) bool {
	return r == ' ' || (unicode.IsGraphic(r) && !unicode.IsControl(r))
}

// tableEnc is a single-byte encoding. Decode runs for every visible byte
// each frame, so it is a lookup in tables made once.
type tableEnc struct {
	id, name string
	cm       *charmap.Charmap // nil - ASCII
	runes    [256]rune
	ok       [256]bool
}

func newTable(id, name string, cm *charmap.Charmap) *tableEnc {
	e := &tableEnc{id: id, name: name, cm: cm}
	for b := 0; b < 256; b++ {
		r := rune(b)
		if cm != nil {
			r = cm.DecodeByte(byte(b))
		} else if b >= 0x80 {
			r = utf8.RuneError
		}
		e.runes[b] = r
		// The charmaps decode the bytes they lack as U+FFFD
		e.ok[b] = r != utf8.RuneError && printable(r)
	}
	return e
}

func (e *tableEnc) ID() string   { return e.id }
func (e *tableEnc) Name() string { return e.name }
func (e *tableEnc) Unit() int    { return 1 }

func (e *tableEnc) Decode(data []byte, i int) (rune, int, bool) {
	b := data[i]
	return e.runes[b], 1, e.ok[b]
}

func (e *tableEnc) Encode(s string) ([]byte, error) {
	out := make([]byte, 0, len(s))
	for i, r := range s {
		if r == utf8.RuneError {
			if _, n := utf8.DecodeRuneInString(s[i:]); n == 1 {
				return nil, ErrInvalidText
			}
		}
		if e.cm == nil {
			if r >= 0x80 {
				return nil, &CharError{e.name, r}
			}
			out = append(out, byte(r))
			continue
		}
		b, ok := e.cm.EncodeRune(r)
		if !ok {
			return nil, &CharError{e.name, r}
		}
		out = append(out, b)
	}
	return out, nil
}

type utf8Enc struct{}

func (utf8Enc) ID() string   { return "utf8" }
func (utf8Enc) Name() string { return "UTF-8" }
func (utf8Enc) Unit() int    { return 1 }

func (utf8Enc) Decode(data []byte, i int) (rune, int, bool) {
	if b := data[i]; b < utf8.RuneSelf {
		return rune(b), 1, b >= 0x20 && b < 0x7F
	}
	r, n := utf8.DecodeRune(data[i:])
	if r == utf8.RuneError && n == 1 {
		return r, 1, false
	}
	// A character that is not printable still takes its bytes, so the
	// view does not show its continuation bytes as separate dots
	return r, n, printable(r)
}

func (utf8Enc) Encode(s string) ([]byte, error) {
	if !utf8.ValidString(s) {
		return nil, ErrInvalidText
	}
	return []byte(s), nil
}

type utf16Enc struct{ be bool }

func (e utf16Enc) ID() string {
	if e.be {
		return "utf16be"
	}
	return "utf16le"
}

func (e utf16Enc) Name() string {
	if e.be {
		return "UTF-16 BE"
	}
	return "UTF-16 LE"
}

func (utf16Enc) Unit() int { return 2 }

func (e utf16Enc) unit(data []byte, i int) uint16 {
	if e.be {
		return uint16(data[i])<<8 | uint16(data[i+1])
	}
	return uint16(data[i+1])<<8 | uint16(data[i])
}

func (e utf16Enc) Decode(data []byte, i int) (rune, int, bool) {
	if len(data)-i < 2 {
		return utf8.RuneError, len(data) - i, false
	}
	u := rune(e.unit(data, i))
	switch {
	case u < 0xD800 || u > 0xDFFF:
		return u, 2, printable(u)
	case u >= 0xDC00:
		// A low surrogate without a high one before it
		return utf8.RuneError, 2, false
	}
	if len(data)-i < 4 {
		return utf8.RuneError, 2, false
	}
	u2 := rune(e.unit(data, i+2))
	if u2 < 0xDC00 || u2 > 0xDFFF {
		return utf8.RuneError, 2, false
	}
	r := utf16.DecodeRune(u, u2)
	return r, 4, printable(r)
}

func (e utf16Enc) Encode(s string) ([]byte, error) {
	if !utf8.ValidString(s) {
		return nil, ErrInvalidText
	}
	units := utf16.Encode([]rune(s))
	out := make([]byte, 0, 2*len(units))
	for _, u := range units {
		if e.be {
			out = append(out, byte(u>>8), byte(u))
		} else {
			out = append(out, byte(u), byte(u>>8))
		}
	}
	return out, nil
}
