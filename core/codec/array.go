package codec

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Lang is a programming language to write a byte array in
type Lang int

const (
	LangC Lang = iota
	LangGo
	LangPython
	LangRust
	LangJava
	LangJS
	LangPascal
)

// Langs returns the languages in menu order
func Langs() []Lang {
	return []Lang{LangC, LangGo, LangPython, LangRust, LangJava, LangJS, LangPascal}
}

func (l Lang) String() string {
	switch l {
	case LangC:
		return "C"
	case LangGo:
		return "Go"
	case LangPython:
		return "Python"
	case LangRust:
		return "Rust"
	case LangJava:
		return "Java"
	case LangJS:
		return "JavaScript"
	case LangPascal:
		return "Pascal"
	}
	return "Lang(" + strconv.Itoa(int(l)) + ")"
}

// FormatArray writes data as a complete declaration of a byte array named
// name ("data" if empty), perLine values a line (0 - 16):
//
//	C:          unsigned char name[3] = { 0x4D, ... };
//	Go:         var name = []byte{ 0x4D, ..., }
//	Python:     name = bytes([ 0x4D, ..., ])
//	Rust:       let name: [u8; 3] = [ 0x4D, ..., ];
//	Java:       byte[] name = { (byte) 0x90, ... };  (bytes are signed there)
//	JavaScript: const name = new Uint8Array([ 0x4D, ..., ]);
//	Pascal:     const name: array[0..2] of Byte = ( $4D, ... );
//
// The values are on lines of their own between the opening and the closing line.
func FormatArray(data []byte, lang Lang, name string, perLine int) string {
	if name == "" {
		name = "data"
	}
	if perLine <= 0 {
		perLine = 16
	}
	n := len(data)
	var head, tail, indent string
	trailing := false // a comma after the last value
	indent = "    "
	switch lang {
	case LangGo:
		head, tail, indent, trailing = "var "+name+" = []byte{", "}", "\t", true
	case LangPython:
		head, tail, trailing = name+" = bytes([", "])", true
	case LangRust:
		head, tail, trailing = fmt.Sprintf("let %s: [u8; %d] = [", name, n), "];", true
	case LangJava:
		head, tail = "byte[] "+name+" = {", "};"
	case LangJS:
		head, tail, trailing = "const "+name+" = new Uint8Array([", "]);", true
	case LangPascal:
		head, tail = fmt.Sprintf("const %s: array[0..%d] of Byte = (", name, n-1), ");"
	default:
		head, tail = fmt.Sprintf("unsigned char %s[%d] = {", name, n), "};"
	}
	var sb strings.Builder
	sb.Grow(len(head) + len(tail) + n*6 + n/perLine*(len(indent)+1) + 8)
	sb.WriteString(head)
	sb.WriteByte('\n')
	for i, b := range data {
		if i%perLine == 0 {
			sb.WriteString(indent)
		} else {
			sb.WriteByte(' ')
		}
		switch {
		case lang == LangPascal:
			sb.WriteByte('$')
		case lang == LangJava && b > 0x7F:
			sb.WriteString("(byte) 0x")
		default:
			sb.WriteString("0x")
		}
		sb.WriteByte(hexDigits[b>>4])
		sb.WriteByte(hexDigits[b&15])
		last := i == n-1
		if !last || trailing {
			sb.WriteByte(',')
		}
		if last || i%perLine == perLine-1 {
			sb.WriteByte('\n')
		}
	}
	sb.WriteString(tail)
	return sb.String()
}

// ParseArray reads the byte values out of a declaration made by FormatArray
// or a like one, or out of a plain list. When there is an '=', only the text
// after the first one is read, so the array size in "name[3] =" is not
// taken for a value. Numbers: decimal, 0x or $ hex, 0b binary, 0o octal,
// with _ between digits and the suffixes u8, i8, u, l; a leading zero does
// not make a number octal, as lists padded with zeros are common. Negative
// values -128..-1 are bytes as in Java. Words (type names, the name of the
// array) and punctuation are skipped, so are // /* */ and # comments.
// Values above 255 are an error, so are string and character literals.
func ParseArray(s string) ([]byte, error) {
	if i := strings.IndexByte(s, '='); i >= 0 {
		s = s[i+1:]
	}
	out := []byte{}
	neg := false // a '-' right before the number
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '/' && i+1 < len(s) && s[i+1] == '/', c == '#':
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				return nil, errors.New("a comment without its end")
			}
			i += 2 + end + 2
		case c == '"' || c == '\'':
			return nil, fmt.Errorf("string and character literals are not supported (at %d)", i)
		case c == '-':
			neg = true
			i++
			continue
		case isIdentStart(c):
			for i < len(s) && isIdentChar(s[i]) {
				i++
			}
		case c >= '0' && c <= '9' || c == '$':
			start := i
			i++
			for i < len(s) && isIdentChar(s[i]) {
				i++
			}
			v, err := parseNumber(s[start:i])
			if err != nil {
				return nil, err
			}
			if neg {
				v = -v
			}
			if v < -128 || v > 255 {
				return nil, fmt.Errorf("-%s is not a byte", s[start:i])
			}
			out = append(out, byte(v))
		default:
			i++
		}
		neg = false
	}
	return out, nil
}

func isIdentStart(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_'
}

func isIdentChar(c byte) bool {
	return isIdentStart(c) || c >= '0' && c <= '9'
}

// parseNumber reads one number token of ParseArray
func parseNumber(tok string) (int64, error) {
	t := strings.ReplaceAll(tok, "_", "")
	base := 10
	switch {
	case strings.HasPrefix(t, "$"):
		base, t = 16, t[1:]
	case len(t) > 1 && t[0] == '0' && (t[1] == 'x' || t[1] == 'X'):
		base, t = 16, t[2:]
	case len(t) > 1 && t[0] == '0' && (t[1] == 'b' || t[1] == 'B'):
		base, t = 2, t[2:]
	case len(t) > 1 && t[0] == '0' && (t[1] == 'o' || t[1] == 'O'):
		base, t = 8, t[2:]
	}
	lower := strings.ToLower(t)
	if strings.HasSuffix(lower, "u8") || strings.HasSuffix(lower, "i8") {
		t = t[:len(t)-2]
	} else {
		// u and l are not hex digits, so they can be cut in any base
		t = strings.TrimRight(t, "uUlL")
	}
	if t == "" {
		return 0, fmt.Errorf("%q is not a number", tok)
	}
	v, err := strconv.ParseUint(t, base, 64)
	if err != nil {
		if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
			return 0, fmt.Errorf("%s is not a byte", tok)
		}
		return 0, fmt.Errorf("%q is not a number", tok)
	}
	if v > 255 {
		return 0, fmt.Errorf("%s is not a byte", tok)
	}
	return int64(v), nil
}
