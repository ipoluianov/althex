package tmpl

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Editable tells whether a value can be typed for the field in the tree: a
// number, a value of an enum or a bit field
func (n *Node) Editable() bool {
	if n.Kind != NodeValue || n.typ == nil || n.Size <= 0 || n.Size > 8 {
		return false
	}
	t := n.typ.valueType()
	return t != nil && t.kind == tkPrim
}

// Encode turns a value typed for the field into the n.Size bytes written at
// n.Off. It reads what the tree shows ("0x1F", "-3", "1.5", a name of the
// enum, "NAME (2)"); cur is the bytes there now, of which a bit field keeps
// the other bits.
func (n *Node) Encode(text string, cur []byte) ([]byte, error) {
	if !n.Editable() {
		return nil, errors.New("this field cannot be edited")
	}
	v, err := n.parseInput(text)
	if err != nil {
		return nil, err
	}
	vt := n.typ.valueType()
	if n.bitLen > 0 {
		if int64(len(cur)) != n.Size {
			return nil, errors.New("the bytes of the bit field are not read")
		}
		x, _ := v.AsInt()
		if err := checkRange(x, v.unsigned, n.bitLen); err != nil {
			return nil, err
		}
		ut := primTypes[unsignedName(int(n.Size))]
		unit := uint64(decodePrim(ut, cur, n.big).i)
		mask := uint64(1)<<uint(n.bitLen) - 1
		unit = unit&^(mask<<uint(n.bitOff)) | (uint64(x)&mask)<<uint(n.bitOff)
		return encodePrim(ut, Uint(unit), n.big), nil
	}
	if v.k == kInt {
		if err := checkRange(v.i, v.unsigned, vt.size*8); err != nil {
			return nil, err
		}
	}
	return encodePrim(vt, v, n.big), nil
}

// parseInput reads the text typed for the field
func (n *Node) parseInput(text string) (Value, error) {
	s := strings.TrimSpace(text)
	// What the tree adds to the number: a comment, the character of a char
	if i := strings.Index(s, "//"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if i := strings.Index(s, " '"); i > 0 {
		s = s[:i]
	}
	if s == "" {
		return Value{}, errors.New("type a value")
	}
	if t := n.typ.resolved(); t != nil && t.kind == tkEnum {
		// "NAME (2)" as the tree shows it: the name decides
		if i := strings.Index(s, " ("); i > 0 && strings.HasSuffix(s, ")") {
			s = s[:i]
		}
		if v, ok := t.values[s]; ok {
			return Int(v), nil
		}
	}
	switch n.typ.valueType().prim {
	case pHalf, pFloat, pDouble:
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return Value{}, fmt.Errorf("%q is not a number", s)
		}
		return Float(f), nil
	}
	if len(s) == 3 && s[0] == '\'' && s[2] == '\'' {
		return Int(int64(s[1])), nil
	}
	if i, err := strconv.ParseInt(s, 0, 64); err == nil {
		return Int(i), nil
	}
	if u, err := strconv.ParseUint(s, 0, 64); err == nil {
		return Uint(u), nil
	}
	return Value{}, fmt.Errorf("%q is not a number", s)
}

// checkRange tells whether x fits in bits, as a signed or an unsigned number:
// -1 and 0xFF are both a byte
func checkRange(x int64, unsigned bool, bits int) error {
	if bits >= 64 {
		return nil
	}
	lo, hi := -(int64(1) << (bits - 1)), int64(1)<<bits-1
	if unsigned && uint64(x) > uint64(hi) {
		return fmt.Errorf("%d does not fit in %d bits", uint64(x), bits)
	}
	if !unsigned && (x < lo || x > hi) {
		return fmt.Errorf("%d does not fit in %d bits", x, bits)
	}
	return nil
}
