// Package interp is the data interpreter (ANL-1): the bytes at the cursor
// shown as values of the common types, and an edited value turned back into
// bytes.
//
// Every type follows the byte order given by the caller, also the DOS dates
// and times. A GUID is read in the Windows order (the first three fields
// little-endian) and in the RFC 4122 order (all big-endian) when the byte
// order is big-endian.
package interp

import (
	"fmt"
)

// MaxBytes is how many bytes at the cursor the interpreter reads: the
// longest fixed type (GUID) is 16 bytes
const MaxBytes = 16

// Type is a type of value
type Type struct {
	ID   string // stable, for settings: "int16", "filetime"
	Name string // shown to the user: "Int16", "FILETIME"
	Size int    // bytes; 0 - variable (LEB128, UTF-8, UTF-16)
}

// Value is the bytes at the cursor read as one type
type Value struct {
	Type Type
	Text string // the value; "" when there are not enough bytes or the value is invalid (a bad date)
	Size int    // bytes the value takes; 0 when Text is ""
}

// kind is a type with the way to read and to write it. dec gets at least
// Size bytes for a fixed type and returns "" for an invalid value.
type kind struct {
	Type
	dec func(b []byte, be bool) (text string, size int)
	enc func(s string, be bool) ([]byte, error)
}

var kinds = []kind{
	intKind("int8", "Int8", 1, true),
	intKind("uint8", "UInt8", 1, false),
	intKind("int16", "Int16", 2, true),
	intKind("uint16", "UInt16", 2, false),
	intKind("int24", "Int24", 3, true),
	intKind("uint24", "UInt24", 3, false),
	intKind("int32", "Int32", 4, true),
	intKind("uint32", "UInt32", 4, false),
	intKind("int64", "Int64", 8, true),
	intKind("uint64", "UInt64", 8, false),
	{Type{"float16", "Float16", 2}, decFloat16, encFloat16},
	{Type{"float32", "Float32", 4}, decFloat32, encFloat32},
	{Type{"float64", "Float64", 8}, decFloat64, encFloat64},
	{Type{"binary8", "Binary", 1}, decBinary8, encBinary8},
	{Type{"char", "Char", 1}, decChar, encChar},
	{Type{"utf8", "UTF-8", 0}, decUTF8, encUTF8},
	{Type{"utf16", "UTF-16", 0}, decUTF16, encUTF16},
	{Type{"unix32", "Unix time (32)", 4}, decUnix32, encUnix32},
	{Type{"unix64", "Unix time (64)", 8}, decUnix64, encUnix64},
	{Type{"filetime", "FILETIME", 8}, decFiletime, encFiletime},
	{Type{"dosdatetime", "DOS date & time", 4}, decDOSDateTime, encDOSDateTime},
	{Type{"dosdate", "DOS date", 2}, decDOSDate, encDOSDate},
	{Type{"dostime", "DOS time", 2}, decDOSTime, encDOSTime},
	{Type{"oletime", "OLE date", 8}, decOLE, encOLE},
	{Type{"guid", "GUID", 16}, decGUID, encGUID},
	{Type{"uleb128", "ULEB128", 0}, decULEB, encULEB},
	{Type{"sleb128", "SLEB128", 0}, decSLEB, encSLEB},
}

// Types lists the types in the order they are shown
func Types() []Type {
	ts := make([]Type, len(kinds))
	for i, k := range kinds {
		ts[i] = k.Type
	}
	return ts
}

// Interpret reads the bytes at the cursor in every type, in the order of
// Types. Only the first MaxBytes bytes are used; fewer at the end of the
// document leave the longer types empty.
func Interpret(data []byte, bigEndian bool) []Value {
	if len(data) > MaxBytes {
		data = data[:MaxBytes]
	}
	vals := make([]Value, len(kinds))
	for i, k := range kinds {
		v := Value{Type: k.Type}
		if len(data) > 0 && len(data) >= k.Size {
			v.Text, v.Size = k.dec(data, bigEndian)
		}
		if v.Text == "" {
			v.Size = 0
		}
		vals[i] = v
	}
	return vals
}

// Encode parses the text of a value of the type and returns its bytes, to
// write the value back. It takes everything Interpret shows, and integers
// also in hex ("0x1F", "-0x10").
func Encode(typeID string, text string, bigEndian bool) ([]byte, error) {
	for _, k := range kinds {
		if k.ID == typeID {
			return k.enc(text, bigEndian)
		}
	}
	return nil, fmt.Errorf("unknown type %q", typeID)
}

// getUint reads an unsigned integer of n bytes
func getUint(b []byte, n int, be bool) uint64 {
	var v uint64
	for i := range n {
		j := n - 1 - i
		if be {
			j = i
		}
		v = v<<8 | uint64(b[j])
	}
	return v
}

// putUint writes the low n bytes of v
func putUint(v uint64, n int, be bool) []byte {
	b := make([]byte, n)
	for i := range n {
		c := byte(v >> (8 * i))
		if be {
			b[n-1-i] = c
		} else {
			b[i] = c
		}
	}
	return b
}

// signExtend makes a signed value of the low n bytes of v
func signExtend(v uint64, n int) int64 {
	sh := 64 - 8*n
	return int64(v<<sh) >> sh
}

func rangeErr(name string) error {
	return fmt.Errorf("out of range for %s", name)
}
