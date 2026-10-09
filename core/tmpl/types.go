package tmpl

import (
	"encoding/binary"
	"math"
	"sort"
)

type typeKind int

const (
	tkPrim typeKind = iota
	tkString
	tkStruct
	tkEnum
	tkTypedef // another name of a type, maybe an array of it
	tkVoid
)

type primKind int

const (
	pChar primKind = iota
	pUchar
	pInt16
	pUint16
	pInt32
	pUint32
	pInt64
	pUint64
	pHalf
	pFloat
	pDouble
)

// Type is a type of a template
type Type struct {
	Name string
	kind typeKind

	// Simple types
	prim   primKind
	size   int
	signed bool
	wide   bool // wstring

	// Structs and unions
	st *structInfo

	// Enums: the type of the value and the names of the values
	base   *Type
	names  map[int64]string
	values map[string]int64

	// Typedefs: elem is the type named; length makes an array of it
	elem   *Type
	length cexpr
	attrs  []cattr
	// isChar: the typedef names char as written (not byte or int8): its
	// arrays are text
	isChar bool
}

type structInfo struct {
	union  bool
	params []param
	body   []cstmt
	attrs  []cattr
}

var primTypes = map[string]*Type{
	"char":    {Name: "char", kind: tkPrim, prim: pChar, size: 1, signed: true},
	"uchar":   {Name: "uchar", kind: tkPrim, prim: pUchar, size: 1},
	"int16":   {Name: "int16", kind: tkPrim, prim: pInt16, size: 2, signed: true},
	"uint16":  {Name: "uint16", kind: tkPrim, prim: pUint16, size: 2},
	"int32":   {Name: "int32", kind: tkPrim, prim: pInt32, size: 4, signed: true},
	"uint32":  {Name: "uint32", kind: tkPrim, prim: pUint32, size: 4},
	"int64":   {Name: "int64", kind: tkPrim, prim: pInt64, size: 8, signed: true},
	"uint64":  {Name: "uint64", kind: tkPrim, prim: pUint64, size: 8},
	"hfloat":  {Name: "hfloat", kind: tkPrim, prim: pHalf, size: 2, signed: true},
	"float":   {Name: "float", kind: tkPrim, prim: pFloat, size: 4, signed: true},
	"double":  {Name: "double", kind: tkPrim, prim: pDouble, size: 8, signed: true},
	"string":  {Name: "string", kind: tkString},
	"wstring": {Name: "wstring", kind: tkString, wide: true},
	"void":    {Name: "void", kind: tkVoid},
}

// resolved returns the type a typedef without a length names, through all of them
func (t *Type) resolved() *Type {
	for t != nil && t.kind == tkTypedef && t.length == nil {
		t = t.elem
	}
	return t
}

// valueType returns the simple type of the value of a type: the base of an enum
func (t *Type) valueType() *Type {
	t = t.resolved()
	if t != nil && t.kind == tkEnum {
		return t.base.resolved()
	}
	return t
}

// fixedSize returns the size of a value of the type when it does not
// depend on the data; ok is false for structs and strings
func (t *Type) fixedSize() (int64, bool) {
	t = t.resolved()
	switch t.kind {
	case tkPrim:
		return int64(t.size), true
	case tkEnum:
		return t.base.fixedSize()
	}
	return 0, false
}

// decode reads a value of a simple type from b
func decodePrim(t *Type, b []byte, big bool) Value {
	var u uint64
	switch t.size {
	case 1:
		u = uint64(b[0])
	case 2:
		if big {
			u = uint64(binary.BigEndian.Uint16(b))
		} else {
			u = uint64(binary.LittleEndian.Uint16(b))
		}
	case 4:
		if big {
			u = uint64(binary.BigEndian.Uint32(b))
		} else {
			u = uint64(binary.LittleEndian.Uint32(b))
		}
	case 8:
		if big {
			u = binary.BigEndian.Uint64(b)
		} else {
			u = binary.LittleEndian.Uint64(b)
		}
	}
	switch t.prim {
	case pHalf:
		return Float(halfToFloat(uint16(u)))
	case pFloat:
		return Float(float64(math.Float32frombits(uint32(u))))
	case pDouble:
		return Float(math.Float64frombits(u))
	}
	v := Value{k: kInt, i: int64(u), unsigned: !t.signed}
	if t.signed && t.size < 8 {
		bits := uint(t.size * 8)
		if u&(1<<(bits-1)) != 0 {
			v.i -= int64(1) << bits
		}
	}
	return v
}

// encodePrim writes a value of a simple type
func encodePrim(t *Type, v Value, big bool) []byte {
	v = castTo(v, t)
	var u uint64
	switch t.prim {
	case pHalf:
		u = uint64(floatToHalf(v.f))
	case pFloat:
		u = uint64(math.Float32bits(float32(v.f)))
	case pDouble:
		u = math.Float64bits(v.f)
	default:
		u = uint64(v.i)
	}
	b := make([]byte, t.size)
	switch t.size {
	case 1:
		b[0] = byte(u)
	case 2:
		if big {
			binary.BigEndian.PutUint16(b, uint16(u))
		} else {
			binary.LittleEndian.PutUint16(b, uint16(u))
		}
	case 4:
		if big {
			binary.BigEndian.PutUint32(b, uint32(u))
		} else {
			binary.LittleEndian.PutUint32(b, uint32(u))
		}
	case 8:
		if big {
			binary.BigEndian.PutUint64(b, u)
		} else {
			binary.LittleEndian.PutUint64(b, u)
		}
	}
	return b
}

func halfToFloat(h uint16) float64 {
	sign := 1.0
	if h&0x8000 != 0 {
		sign = -1
	}
	exp := int(h>>10) & 0x1f
	frac := float64(h & 0x3ff)
	switch exp {
	case 0:
		return sign * frac / 1024 * math.Pow(2, -14)
	case 31:
		if frac == 0 {
			return math.Inf(int(sign))
		}
		return math.NaN()
	}
	return sign * (1 + frac/1024) * math.Pow(2, float64(exp-15))
}

func floatToHalf(f float64) uint16 {
	bits := math.Float32bits(float32(f))
	sign := uint16(bits>>16) & 0x8000
	exp := int(bits>>23&0xff) - 127 + 15
	frac := bits & 0x7fffff
	switch {
	case exp >= 31:
		if math.IsNaN(f) {
			return sign | 0x7e00
		}
		return sign | 0x7c00
	case exp <= 0:
		if exp < -10 {
			return sign
		}
		frac |= 0x800000
		return sign | uint16(frac>>uint(14-exp))
	}
	return sign | uint16(exp)<<10 | uint16(frac>>13)
}

// enumName returns the name of an enum value; "" if it has none
func (t *Type) enumName(v int64) string {
	t = t.resolved()
	if t == nil || t.kind != tkEnum {
		return ""
	}
	return t.names[v]
}

// sortedNames returns the names of an enum in the order of their values
func (t *Type) sortedNames() []string {
	var names []string
	for n := range t.values {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool { return t.values[names[i]] < t.values[names[j]] })
	return names
}
