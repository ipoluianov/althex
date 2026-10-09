package search

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// NumKind is the type of a number to find (FIND-3)
type NumKind int

const (
	Int8 NumKind = iota
	Uint8
	Int16
	Uint16
	Int32
	Uint32
	Int64
	Uint64
	Float32
	Float64
)

var kindNames = [...]string{"int8", "uint8", "int16", "uint16", "int32", "uint32", "int64", "uint64", "float32", "float64"}

func (k NumKind) String() string {
	if k < 0 || int(k) >= len(kindNames) {
		return fmt.Sprintf("NumKind(%d)", int(k))
	}
	return kindNames[k]
}

// Size is the number of bytes of the kind
func (k NumKind) Size() int {
	switch k {
	case Int8, Uint8:
		return 1
	case Int16, Uint16:
		return 2
	case Int32, Uint32, Float32:
		return 4
	}
	return 8
}

func (k NumKind) signed() bool { return k == Int8 || k == Int16 || k == Int32 || k == Int64 }

var kindAliases = map[string]NumKind{
	"int8": Int8, "i8": Int8, "s8": Int8, "sbyte": Int8, "char": Int8,
	"uint8": Uint8, "u8": Uint8, "byte": Uint8, "uchar": Uint8,
	"int16": Int16, "i16": Int16, "s16": Int16, "short": Int16,
	"uint16": Uint16, "u16": Uint16, "ushort": Uint16, "word": Uint16,
	"int32": Int32, "i32": Int32, "s32": Int32, "int": Int32, "long": Int32,
	"uint32": Uint32, "u32": Uint32, "uint": Uint32, "ulong": Uint32, "dword": Uint32,
	"int64": Int64, "i64": Int64, "s64": Int64, "longlong": Int64,
	"uint64": Uint64, "u64": Uint64, "ulonglong": Uint64, "qword": Uint64,
	"float32": Float32, "f32": Float32, "float": Float32, "single": Float32,
	"float64": Float64, "f64": Float64, "double": Float64,
}

// ParseNumKind parses the name of a kind: "int32", "u16", "float", "double"...
// "long" is 32 bits as in C on Windows; "int64" says it plainly.
func ParseNumKind(s string) (NumKind, bool) {
	k, ok := kindAliases[strings.ToLower(strings.TrimSpace(s))]
	return k, ok
}

// Number is the pattern of a number of the kind (FIND-3). The value is
// decimal or 0x hex (a float may also be a Go hex float); it must fit the
// kind. A signed kind also takes hex up to its unsigned maximum, so 0xFF as
// int8 is -1: hex is how the bytes are thought of.
func Number(kind NumKind, value string, bigEndian bool) (Pattern, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return Pattern{}, errors.New("no number")
	}
	size := kind.Size()
	var u uint64
	switch {
	case kind == Float32:
		f, err := strconv.ParseFloat(value, 32)
		if err != nil {
			return Pattern{}, fmt.Errorf("not a float32: %w", err)
		}
		u = uint64(math.Float32bits(float32(f)))
	case kind == Float64:
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return Pattern{}, fmt.Errorf("not a float64: %w", err)
		}
		u = math.Float64bits(f)
	case kind.signed():
		v, err := strconv.ParseInt(value, 0, size*8)
		if err != nil {
			uv, uerr := strconv.ParseUint(value, 0, size*8)
			if uerr != nil || !isHex(value) {
				return Pattern{}, fmt.Errorf("not an %v: %w", kind, err)
			}
			v = int64(uv)
		}
		u = uint64(v)
	case kind >= Uint8 && kind <= Uint64:
		v, err := strconv.ParseUint(value, 0, size*8)
		if err != nil {
			return Pattern{}, fmt.Errorf("not a %v: %w", kind, err)
		}
		u = v
	default:
		return Pattern{}, fmt.Errorf("unknown kind %v", kind)
	}
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], u)
	out := b[:size]
	if bigEndian {
		for i, j := 0, size-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	return Exact(out), nil
}

func isHex(s string) bool {
	return len(s) > 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X')
}
