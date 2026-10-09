package codec

import (
	"encoding/base64"
	"errors"
	"strings"
)

// FormatBase64 encodes data in standard padded Base64, broken into lines of
// lineLen characters (0 - one line)
func FormatBase64(data []byte, lineLen int) string {
	s := base64.StdEncoding.EncodeToString(data)
	if lineLen <= 0 || len(s) <= lineLen {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s) + len(s)/lineLen)
	for len(s) > lineLen {
		sb.WriteString(s[:lineLen])
		sb.WriteByte('\n')
		s = s[lineLen:]
	}
	sb.WriteString(s)
	return sb.String()
}

// ParseBase64 decodes Base64 in the standard or the URL alphabet, padded or
// not. Whitespace anywhere is ignored, as copied text is often broken into lines.
func ParseBase64(s string) ([]byte, error) {
	s = strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r', '\v', '\f':
			return -1
		}
		return r
	}, s)
	s = strings.TrimRight(s, "=")
	enc := base64.RawStdEncoding
	if strings.ContainsAny(s, "-_") {
		enc = base64.RawURLEncoding
	}
	data, err := enc.DecodeString(s)
	if err != nil {
		return nil, errors.New("not valid Base64: " + err.Error())
	}
	return data, nil
}
