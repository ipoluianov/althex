package codec

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// FormatEscaped writes the bytes as the body of a C or Go string literal:
// printable ASCII as is, \\ \" \n \r \t, other bytes \xHH. In C a \x escape
// takes all the hex digits after it, so a hex digit right after a \xHH is
// written as \xHH too; the result reads the same in C and in Go.
func FormatEscaped(data []byte) string {
	var sb strings.Builder
	sb.Grow(len(data))
	hexBefore := false // the last thing written is a \xHH
	for _, b := range data {
		esc := false
		switch b {
		case '\\':
			sb.WriteString(`\\`)
		case '"':
			sb.WriteString(`\"`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		default:
			_, isHex := hexVal(b)
			if b < 0x20 || b > 0x7E || (hexBefore && isHex) {
				sb.WriteString(`\x`)
				sb.WriteByte(hexDigits[b>>4])
				sb.WriteByte(hexDigits[b&15])
				esc = true
			} else {
				sb.WriteByte(b)
			}
		}
		hexBefore = esc
	}
	return sb.String()
}

// ParseEscaped reads the body of a C, Go or Python string literal; the
// quotes around it, if there are both, are removed. Escapes: \\ \" \' \?
// \a \b \f \n \r \t \v, \x with one or two hex digits (as in Go: more
// digits are text, not a larger value), octal \o..\ooo up to \377, and
// \uXXXX, \UXXXXXXXX written as UTF-8. Other characters are taken as their
// UTF-8 bytes.
func ParseEscaped(s string) ([]byte, error) {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		// The closing quote must not be escaped: count the backslashes before it
		k := 0
		for j := len(s) - 2; j >= 1 && s[j] == '\\'; j-- {
			k++
		}
		if k%2 == 0 {
			s = s[1 : len(s)-1]
		}
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		c := s[i]
		if c != '\\' {
			out = append(out, c)
			i++
			continue
		}
		if i+1 >= len(s) {
			return nil, fmt.Errorf("a backslash at the end")
		}
		e := s[i+1]
		i += 2
		switch e {
		case '\\', '"', '\'', '?':
			out = append(out, e)
		case 'a':
			out = append(out, 7)
		case 'b':
			out = append(out, 8)
		case 'f':
			out = append(out, 12)
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'v':
			out = append(out, 11)
		case 'x':
			v, n := 0, 0
			for n < 2 && i < len(s) {
				d, ok := hexVal(s[i])
				if !ok {
					break
				}
				v = v<<4 | int(d)
				i++
				n++
			}
			if n == 0 {
				return nil, fmt.Errorf("no hex digits after \\x at %d", i-2)
			}
			out = append(out, byte(v))
		case '0', '1', '2', '3', '4', '5', '6', '7':
			v := int(e - '0')
			for n := 1; n < 3 && i < len(s) && s[i] >= '0' && s[i] <= '7'; n++ {
				v = v<<3 | int(s[i]-'0')
				i++
			}
			if v > 0xFF {
				return nil, fmt.Errorf("octal escape above \\377 at %d", i)
			}
			out = append(out, byte(v))
		case 'u', 'U':
			n := 4
			if e == 'U' {
				n = 8
			}
			if i+n > len(s) {
				return nil, fmt.Errorf("short \\%c escape at %d", e, i-2)
			}
			v := 0
			for _, d := range []byte(s[i : i+n]) {
				h, ok := hexVal(d)
				if !ok {
					return nil, fmt.Errorf("not a hex digit in \\%c escape at %d", e, i-2)
				}
				v = v<<4 | int(h)
			}
			if v > utf8.MaxRune || !utf8.ValidRune(rune(v)) {
				return nil, fmt.Errorf("\\%c escape at %d is not a character", e, i-2)
			}
			out = utf8.AppendRune(out, rune(v))
			i += n
		default:
			return nil, fmt.Errorf("unknown escape \\%c at %d", e, i-2)
		}
	}
	return out, nil
}
