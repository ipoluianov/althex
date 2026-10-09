package interp

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

var errNotChar = errors.New("not one character or U+code")

// runeText shows a character and its code: "П (U+041F)"; only the code
// for characters that cannot be shown
func runeText(r rune) string {
	if unicode.IsPrint(r) {
		return fmt.Sprintf("%c (U+%04X)", r, r)
	}
	return fmt.Sprintf("U+%04X", r)
}

// parseRune reads "П", "U+041F" or "П (U+041F)"; the code wins in the last
// form. The text is not trimmed: a space is a character too.
func parseRune(s string) (rune, error) {
	code := ""
	if i := strings.LastIndex(s, " (U+"); i >= 0 && strings.HasSuffix(s, ")") {
		code = s[i+4 : len(s)-1]
	} else if t := strings.TrimSpace(s); len(t) > 2 && (t[:2] == "U+" || t[:2] == "u+") {
		code = t[2:]
	}
	var r rune
	if code != "" {
		v, err := strconv.ParseUint(code, 16, 32)
		if err != nil {
			return 0, errNotChar
		}
		r = rune(v)
	} else {
		if utf8.RuneCountInString(s) != 1 {
			return 0, errNotChar
		}
		var n int
		r, n = utf8.DecodeRuneInString(s)
		if r == utf8.RuneError && n == 1 {
			return 0, errNotChar
		}
	}
	if !utf8.ValidRune(r) {
		return 0, errors.New("not a valid character")
	}
	return r, nil
}

func decUTF8(b []byte, be bool) (string, int) {
	r, n := utf8.DecodeRune(b)
	if r == utf8.RuneError && n <= 1 {
		return "", 0
	}
	return runeText(r), n
}

func encUTF8(s string, be bool) ([]byte, error) {
	r, err := parseRune(s)
	if err != nil {
		return nil, err
	}
	return utf8.AppendRune(nil, r), nil
}

func decUTF16(b []byte, be bool) (string, int) {
	if len(b) < 2 {
		return "", 0
	}
	u := rune(getUint(b, 2, be))
	if !utf16.IsSurrogate(u) {
		return runeText(u), 2
	}
	if u >= 0xdc00 || len(b) < 4 {
		return "", 0 // a low surrogate first, or no room for the pair
	}
	r := utf16.DecodeRune(u, rune(getUint(b[2:], 2, be)))
	if r == unicode.ReplacementChar {
		return "", 0
	}
	return runeText(r), 4
}

func encUTF16(s string, be bool) ([]byte, error) {
	r, err := parseRune(s)
	if err != nil {
		return nil, err
	}
	var b []byte
	for _, u := range utf16.Encode([]rune{r}) {
		b = append(b, putUint(uint64(u), 2, be)...)
	}
	return b, nil
}

// decGUID shows the registry form {6B29FC40-CA47-1067-B31D-00DD010662DA}
func decGUID(b []byte, be bool) (string, int) {
	return fmt.Sprintf("{%08X-%04X-%04X-%X-%X}",
		getUint(b, 4, be), getUint(b[4:], 2, be), getUint(b[6:], 2, be), b[8:10], b[10:16]), 16
}

// encGUID takes the form with or without braces, and also 32 hex digits
func encGUID(s string, be bool) ([]byte, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
		s = s[1 : len(s)-1]
	}
	if len(s) == 36 {
		if s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
			return nil, errors.New("not a GUID")
		}
		s = strings.ReplaceAll(s, "-", "")
	}
	raw, err := hex.DecodeString(s)
	if err != nil || len(raw) != 16 {
		return nil, errors.New("not a GUID")
	}
	b := make([]byte, 0, 16)
	b = append(b, putUint(getUint(raw, 4, true), 4, be)...)
	b = append(b, putUint(getUint(raw[4:], 2, true), 2, be)...)
	b = append(b, putUint(getUint(raw[6:], 2, true), 2, be)...)
	return append(b, raw[8:]...), nil
}

// maxLEB is the longest LEB128 of a 64-bit value
const maxLEB = 10

// decULEB reads an unsigned LEB128; a value beyond 64 bits is invalid
func decULEB(b []byte, be bool) (string, int) {
	var v uint64
	for i := 0; i < len(b) && i < maxLEB; i++ {
		c := b[i]
		if i == maxLEB-1 && c > 1 {
			return "", 0
		}
		v |= uint64(c&0x7f) << (7 * i)
		if c&0x80 == 0 {
			return strconv.FormatUint(v, 10), i + 1
		}
	}
	return "", 0
}

func encULEB(s string, be bool) ([]byte, error) {
	v, err := parseInt(s, 8, false, "ULEB128")
	if err != nil {
		return nil, err
	}
	var b []byte
	for {
		c := byte(v & 0x7f)
		v >>= 7
		if v == 0 {
			return append(b, c), nil
		}
		b = append(b, c|0x80)
	}
}

// decSLEB reads a signed LEB128; in the tenth byte only the sign may be left
func decSLEB(b []byte, be bool) (string, int) {
	var v int64
	for i := 0; i < len(b) && i < maxLEB; i++ {
		c := b[i]
		if i == maxLEB-1 && c != 0 && c != 0x7f {
			return "", 0
		}
		v |= int64(c&0x7f) << (7 * i)
		if c&0x80 == 0 {
			if sh := 7 * (i + 1); sh < 64 && c&0x40 != 0 {
				v |= -1 << sh
			}
			return strconv.FormatInt(v, 10), i + 1
		}
	}
	return "", 0
}

func encSLEB(s string, be bool) ([]byte, error) {
	u, err := parseInt(s, 8, true, "SLEB128")
	if err != nil {
		return nil, err
	}
	v := int64(u)
	var b []byte
	for {
		c := byte(v & 0x7f)
		v >>= 7
		if (v == 0 && c&0x40 == 0) || (v == -1 && c&0x40 != 0) {
			return append(b, c), nil
		}
		b = append(b, c|0x80)
	}
}
