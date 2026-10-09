package codec

import (
	"fmt"
	"strings"
)

// FormatHex writes the bytes as upper case hex digits with sep between
// them: "4D 5A 90" with sep " "
func FormatHex(data []byte, sep string) string {
	if len(data) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.Grow(len(data) * (2 + len(sep)))
	for i, b := range data {
		if i > 0 {
			sb.WriteString(sep)
		}
		sb.WriteByte(hexDigits[b>>4])
		sb.WriteByte(hexDigits[b&15])
	}
	return sb.String()
}

// ParseHex reads hex digits: "4D5A90", "4D 5A 90", "4d:5a:90",
// "0x4D, 0x5A". Groups of digits are separated by whitespace, commas,
// colons, semicolons or dashes and may have a 0x prefix; a group with an
// odd number of digits is an error, as which nibble is missing is unknown.
func ParseHex(s string) ([]byte, error) {
	out := make([]byte, 0, len(s)/2)
	i := 0
	for i < len(s) {
		c := s[i]
		if isHexSep(c) {
			i++
			continue
		}
		start := i
		if c == '0' && i+1 < len(s) && (s[i+1] == 'x' || s[i+1] == 'X') {
			i += 2
		}
		digits := i
		for i < len(s) && !isHexSep(s[i]) {
			if _, ok := hexVal(s[i]); !ok {
				return nil, fmt.Errorf("not a hex digit at %d: %q", i, s[i])
			}
			i++
		}
		if (i-digits)%2 != 0 {
			return nil, fmt.Errorf("odd number of hex digits in %q", s[start:i])
		}
		if i == digits {
			return nil, fmt.Errorf("no digits after 0x at %d", start)
		}
		for j := digits; j < i; j += 2 {
			hi, _ := hexVal(s[j])
			lo, _ := hexVal(s[j+1])
			out = append(out, hi<<4|lo)
		}
	}
	return out, nil
}

func isHexSep(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', ',', ':', ';', '-':
		return true
	}
	return false
}
