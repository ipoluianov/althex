package interp

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

var errNotInt = errors.New("not an integer")
var errNotNumber = errors.New("not a number")

// intKind is an integer of n bytes
func intKind(id, name string, n int, signed bool) kind {
	dec := func(b []byte, be bool) (string, int) {
		v := getUint(b, n, be)
		if signed {
			return strconv.FormatInt(signExtend(v, n), 10), n
		}
		return strconv.FormatUint(v, 10), n
	}
	enc := func(s string, be bool) ([]byte, error) {
		v, err := parseInt(s, n, signed, name)
		if err != nil {
			return nil, err
		}
		return putUint(v, n, be), nil
	}
	return kind{Type{id, name, n}, dec, enc}
}

// parseInt reads a decimal or a 0x / 0b / 0o integer with a sign and checks
// it fits n bytes; a negative value comes as two's complement
func parseInt(s string, n int, signed bool, name string) (uint64, error) {
	neg, mag, err := parseMag(s)
	if err != nil {
		if errors.Is(err, strconv.ErrRange) {
			return 0, rangeErr(name)
		}
		return 0, err
	}
	bits := uint(8 * n)
	if signed {
		limit := uint64(1) << (bits - 1) // the magnitude of the lowest value
		if neg {
			if mag > limit {
				return 0, rangeErr(name)
			}
			return -mag, nil
		}
		if mag >= limit {
			return 0, rangeErr(name)
		}
		return mag, nil
	}
	if neg && mag != 0 {
		return 0, rangeErr(name)
	}
	if bits < 64 && mag >= uint64(1)<<bits {
		return 0, rangeErr(name)
	}
	return mag, nil
}

// parseMag splits an integer into its sign and magnitude. A leading 0 is
// decimal: users of a hex editor write 0x for hex and do not mean octal.
func parseMag(s string) (neg bool, mag uint64, err error) {
	s = strings.ReplaceAll(strings.TrimSpace(s), "_", "")
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	} else if strings.HasPrefix(s, "+") {
		s = s[1:]
	}
	base := 10
	if len(s) >= 2 && s[0] == '0' {
		switch s[1] {
		case 'x', 'X':
			base = 16
		case 'b', 'B':
			base = 2
		case 'o', 'O':
			base = 8
		}
		if base != 10 {
			s = s[2:]
		}
	}
	if s == "" {
		return false, 0, errNotInt
	}
	mag, err = strconv.ParseUint(s, base, 64)
	if err != nil {
		if errors.Is(err, strconv.ErrRange) {
			return false, 0, strconv.ErrRange
		}
		return false, 0, errNotInt
	}
	return neg, mag, nil
}

// formatFloat shows the shortest text that reads back as the same value
func formatFloat(f float64, bits int) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "+Inf"
	case math.IsInf(f, -1):
		return "-Inf"
	}
	return strconv.FormatFloat(f, 'g', -1, bits)
}

func parseFloat(s string, bits int, name string) (float64, error) {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), bits)
	if err != nil {
		if errors.Is(err, strconv.ErrRange) {
			return 0, rangeErr(name)
		}
		return 0, errNotNumber
	}
	return f, nil
}

func decFloat32(b []byte, be bool) (string, int) {
	f := math.Float32frombits(uint32(getUint(b, 4, be)))
	return formatFloat(float64(f), 32), 4
}

func encFloat32(s string, be bool) ([]byte, error) {
	f, err := parseFloat(s, 32, "Float32")
	if err != nil {
		return nil, err
	}
	return putUint(uint64(math.Float32bits(float32(f))), 4, be), nil
}

func decFloat64(b []byte, be bool) (string, int) {
	return formatFloat(math.Float64frombits(getUint(b, 8, be)), 64), 8
}

func encFloat64(s string, be bool) ([]byte, error) {
	f, err := parseFloat(s, 64, "Float64")
	if err != nil {
		return nil, err
	}
	return putUint(math.Float64bits(f), 8, be), nil
}

func decFloat16(b []byte, be bool) (string, int) {
	h := uint16(getUint(b, 2, be))
	f := halfToFloat(h)
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return formatFloat(f, 64), 2
	}
	if f == math.Trunc(f) {
		return strconv.FormatFloat(f, 'f', -1, 64), 2 // whole halves are exact and short
	}
	// The fewest digits that read back as the same half
	for p := 1; p < 17; p++ {
		g, _ := strconv.ParseFloat(strconv.FormatFloat(f, 'g', p, 64), 64)
		if r, err := floatToHalf(g); err == nil && r == h {
			return strconv.FormatFloat(g, 'g', -1, 64), 2
		}
	}
	return strconv.FormatFloat(f, 'g', -1, 64), 2
}

func encFloat16(s string, be bool) ([]byte, error) {
	f, err := parseFloat(s, 64, "Float16")
	if err != nil {
		return nil, err
	}
	h, err := floatToHalf(f)
	if err != nil {
		return nil, err
	}
	return putUint(uint64(h), 2, be), nil
}

// halfToFloat reads an IEEE 754 half
func halfToFloat(h uint16) float64 {
	sign := 1.0
	if h&0x8000 != 0 {
		sign = -1
	}
	e := int(h>>10) & 0x1f
	m := float64(h & 0x3ff)
	switch e {
	case 0:
		return sign * math.Ldexp(m, -24)
	case 31:
		if m != 0 {
			return math.NaN()
		}
		return math.Inf(int(sign))
	}
	return sign * math.Ldexp(1+m/1024, e-15)
}

// floatToHalf rounds to the nearest half, ties to even. Values that round
// beyond the largest half (65504) are an error rather than infinity.
func floatToHalf(f float64) (uint16, error) {
	var sign uint16
	if math.Signbit(f) {
		sign = 0x8000
	}
	switch {
	case math.IsNaN(f):
		return 0x7e00, nil
	case math.IsInf(f, 0):
		return sign | 0x7c00, nil
	}
	a := math.Abs(f)
	if a >= 65520 { // halfway between 65504 and 65536 rounds up to infinity
		return 0, rangeErr("Float16")
	}
	if a < math.Ldexp(1, -14) {
		// Subnormal; 1024 here is the smallest normal, which has the same bits
		return sign | uint16(math.RoundToEven(math.Ldexp(a, 24))), nil
	}
	_, exp := math.Frexp(a)
	e := exp - 1
	m := math.RoundToEven((math.Ldexp(a, -e) - 1) * 1024)
	if m == 1024 {
		e++
		m = 0
	}
	return sign | uint16(e+15)<<10 | uint16(m), nil
}

func decBinary8(b []byte, be bool) (string, int) {
	return fmt.Sprintf("%08b", b[0]), 1
}

func encBinary8(s string, be bool) ([]byte, error) {
	s = strings.NewReplacer(" ", "", "_", "").Replace(s)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0b"), "0B")
	if len(s) == 0 || len(s) > 8 {
		return nil, errors.New("not 1 to 8 binary digits")
	}
	v, err := strconv.ParseUint(s, 2, 8)
	if err != nil {
		return nil, errors.New("not 1 to 8 binary digits")
	}
	return []byte{byte(v)}, nil
}

func decChar(b []byte, be bool) (string, int) {
	if b[0] < 0x20 || b[0] > 0x7e {
		return "", 0
	}
	return string(rune(b[0])), 1
}

// encChar takes the text as is: a space is a character too
func encChar(s string, be bool) ([]byte, error) {
	if len(s) != 1 || s[0] < 0x20 || s[0] > 0x7e {
		return nil, errors.New("not one printable ASCII character")
	}
	return []byte{s[0]}, nil
}
