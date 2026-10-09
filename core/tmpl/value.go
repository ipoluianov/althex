package tmpl

import (
	"fmt"
	"math"
	"strconv"
)

type kind int

const (
	kVoid kind = iota
	kInt
	kFloat
	kString
	kNode // a field of a struct, an array, a struct
)

// Value is a value of an expression: a 64-bit integer (signed or not), a
// float, a string or a field of the document
type Value struct {
	k        kind
	i        int64
	f        float64
	s        string
	n        *Node
	unsigned bool
}

// Int makes an integer value
func Int(v int64) Value { return Value{k: kInt, i: v} }

// Uint makes an unsigned integer value
func Uint(v uint64) Value { return Value{k: kInt, i: int64(v), unsigned: true} }

// Float makes a floating point value
func Float(v float64) Value { return Value{k: kFloat, f: v} }

// String makes a string value
func String(s string) Value { return Value{k: kString, s: s} }

// IsVoid tells whether there is no value (a function returning nothing)
func (v Value) IsVoid() bool { return v.k == kVoid }

// Text writes the value for the console
func (v Value) Text() string {
	v = v.deref()
	switch v.k {
	case kInt:
		if v.unsigned {
			u := uint64(v.i)
			return fmt.Sprintf("%d (0x%X)", u, u)
		}
		if v.i < 0 {
			return fmt.Sprintf("%d (0x%X)", v.i, uint64(v.i))
		}
		return fmt.Sprintf("%d (0x%X)", v.i, v.i)
	case kFloat:
		return strconv.FormatFloat(v.f, 'g', -1, 64)
	case kString:
		return strconv.Quote(v.s)
	case kNode:
		if v.n != nil {
			return v.n.Value()
		}
	}
	return ""
}

// deref returns the value of a field of a simple type; other values stay
func (v Value) deref() Value {
	if v.k == kNode && v.n != nil && v.n.hasValue() {
		return v.n.val
	}
	return v
}

// num tells whether the value is a number, after deref
func (v Value) num() bool { return v.k == kInt || v.k == kFloat }

// AsInt returns the value as an integer; false for strings and structs
func (v Value) AsInt() (int64, bool) {
	v = v.deref()
	switch v.k {
	case kInt:
		return v.i, true
	case kFloat:
		return int64(v.f), true
	}
	return 0, false
}

func (v Value) asFloat() float64 {
	v = v.deref()
	if v.k == kFloat {
		return v.f
	}
	if v.unsigned {
		return float64(uint64(v.i))
	}
	return float64(v.i)
}

func (v Value) truthy() bool {
	v = v.deref()
	switch v.k {
	case kInt:
		return v.i != 0
	case kFloat:
		return v.f != 0
	case kString:
		return v.s != ""
	case kNode:
		return v.n != nil
	}
	return false
}

// castTo converts a number to a simple type: truncated and sign extended
// as the type says
func castTo(v Value, t *Type) Value {
	v = v.deref()
	if t == nil {
		return v
	}
	for t.kind == tkTypedef && t.elem != nil && t.length == nil {
		t = t.elem
	}
	if t.kind == tkEnum {
		t = t.base
	}
	if t.kind == tkString {
		if v.k == kString {
			return v
		}
		return String(v.Text())
	}
	if t.kind != tkPrim {
		return v
	}
	switch t.prim {
	case pFloat:
		return Float(float64(float32(v.asFloat())))
	case pDouble, pHalf:
		return Float(v.asFloat())
	}
	var x int64
	if v.k == kFloat {
		if t.signed {
			x = int64(v.f)
		} else {
			x = int64(uint64(math.Max(v.f, 0)))
		}
	} else {
		x = v.i
	}
	bits := uint(t.size * 8)
	if bits < 64 {
		mask := int64(1)<<bits - 1
		x &= mask
		if t.signed && x&(int64(1)<<(bits-1)) != 0 {
			x -= int64(1) << bits
		}
	}
	return Value{k: kInt, i: x, unsigned: !t.signed}
}
