package tmpl

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// NodeKind is what a field is
type NodeKind int

const (
	NodeValue  NodeKind = iota // a number, an enum, a bit field
	NodeString                 // string, wstring
	NodeStruct                 // a struct or a union
	NodeArray                  // an array of a simple type: its elements are read when looked at
	NodeList                   // an array of structs or strings, a repeated field: its elements are nodes
	NodeRoot
)

// Node is a field of the tree a template makes
type Node struct {
	Name     string
	TypeName string
	Kind     NodeKind
	Off      int64
	Size     int64
	Parent   *Node
	Comment  string
	Color    uint32 // 0xRRGGBB + 1<<24 when set
	Hidden   bool
	Open     bool

	children []*Node
	fields   map[string]*Node // the children by name, for the lookups of the program

	typ     *Type
	val     Value
	big     bool
	bitOff  int // bit fields: the first bit and how many
	bitLen  int
	count   int64 // NodeArray: the number of elements
	format  string
	repeat  bool   // NodeList of repeated fields
	display string // made by a read function
	isText  bool   // an array of char shown as text
	res     *Result
	sorted  []*Node // the children by offset, for NodeAt
	local   *local  // a local array seen as a node by the program
}

// hasValue tells whether the node is of a simple type
func (n *Node) hasValue() bool {
	return n.Kind == NodeValue || n.Kind == NodeString
}

// Children returns the fields of a struct or the elements of a list; an
// array of a simple type has its elements made on request (ChildCount, Child)
func (n *Node) Children() []*Node {
	var out []*Node
	for _, c := range n.children {
		if !c.Hidden {
			out = append(out, c)
		}
	}
	return out
}

// ChildCount is the number of elements of an array, or of the children
func (n *Node) ChildCount() int64 {
	if n.Kind == NodeArray {
		return n.count
	}
	return int64(len(n.Children()))
}

// Child returns the i-th element of an array of a simple type, made now;
// for other nodes the i-th child
func (n *Node) Child(i int64) *Node {
	if n.Kind != NodeArray {
		ch := n.Children()
		if i < 0 || i >= int64(len(ch)) {
			return nil
		}
		return ch[i]
	}
	if i < 0 || i >= n.count {
		return nil
	}
	elemType := n.typ
	size, _ := elemType.fixedSize()
	e := &Node{
		Name:     fmt.Sprintf("%s[%d]", n.Name, i),
		TypeName: elemTypeName(n.TypeName),
		Kind:     NodeValue,
		Off:      n.Off + i*size,
		Size:     size,
		Parent:   n,
		typ:      elemType,
		big:      n.big,
		format:   n.format,
		res:      n.res,
	}
	e.val = n.res.readValue(elemType, e.Off, n.big)
	return e
}

func elemTypeName(arr string) string {
	if i := strings.IndexByte(arr, '['); i >= 0 {
		return arr[:i]
	}
	return arr
}

// child returns the field of a struct by name, for the program
func (n *Node) child(name string) *Node {
	if n.fields == nil {
		return nil
	}
	return n.fields[name]
}

// add adds a field; one with the name of another makes them a list (a
// repeated field: while (!FEof()) CHUNK chunk;)
func (n *Node) add(c *Node) {
	c.Parent = n
	if n.fields == nil {
		n.fields = make(map[string]*Node)
	}
	if prev, ok := n.fields[c.Name]; ok {
		if prev.Kind != NodeList || !prev.repeat {
			list := &Node{Name: c.Name, TypeName: c.TypeName + "[]", Kind: NodeList, Off: prev.Off, Size: prev.Size, Parent: n, res: n.res, repeat: true}
			for i, x := range n.children {
				if x == prev {
					n.children[i] = list
				}
			}
			prev.Name = c.Name + "[0]"
			prev.Parent = list
			list.children = []*Node{prev}
			n.fields[c.Name] = list
			prev = list
		}
		c.Name = fmt.Sprintf("%s[%d]", c.Name, len(prev.children))
		c.Parent = prev
		prev.children = append(prev.children, c)
		prev.Size = max(prev.Off+prev.Size, c.Off+c.Size) - prev.Off
		prev.Off = min(prev.Off, c.Off)
		return
	}
	n.children = append(n.children, c)
	n.fields[c.Name] = c
}

// Path returns the names from the top to the node: "hdr.chunk[2].len"
func (n *Node) Path() string {
	var parts []string
	for x := n; x != nil && x.Kind != NodeRoot; x = x.Parent {
		name := x.Name
		// The elements of a list are named with their index already
		if x.Parent != nil && x.Parent.Kind == NodeList {
			parts = append(parts, name[strings.LastIndexByte(name, '['):])
			continue
		}
		parts = append(parts, name)
	}
	var sb strings.Builder
	for i := len(parts) - 1; i >= 0; i-- {
		if sb.Len() > 0 && !strings.HasPrefix(parts[i], "[") {
			sb.WriteByte('.')
		}
		sb.WriteString(parts[i])
	}
	return sb.String()
}

// Value writes the value of the field for the tree
func (n *Node) Value() string {
	if n.display != "" {
		return n.display
	}
	switch n.Kind {
	case NodeValue:
		return n.formatValue()
	case NodeString:
		return strconv.Quote(n.val.s)
	case NodeArray:
		if n.isText {
			return strconv.Quote(n.res.readText(n.Off, n.Size))
		}
		return fmt.Sprintf("[%d]", n.count)
	case NodeList:
		return fmt.Sprintf("[%d]", len(n.children))
	}
	return ""
}

func (n *Node) formatValue() string {
	v := n.val
	if v.k == kFloat {
		return strconv.FormatFloat(v.f, 'g', -1, 64)
	}
	if v.k != kInt {
		return v.Text()
	}
	text := formatInt(v, n.format, n.Size)
	if name := n.typ.enumName(v.i); name != "" {
		return name + " (" + text + ")"
	}
	if n.typ != nil {
		if t := n.typ.resolved(); t != nil && t.kind == tkEnum {
			return text
		}
	}
	// A char shows its character too
	if (n.TypeName == "char" || n.TypeName == "uchar" || n.TypeName == "CHAR") && v.i >= 0x20 && v.i < 0x7f {
		return fmt.Sprintf("%s '%c'", text, rune(v.i))
	}
	return text
}

func formatInt(v Value, format string, size int64) string {
	u := uint64(v.i)
	if size > 0 && size < 8 {
		u &= 1<<(uint(size)*8) - 1
	}
	switch format {
	case "hex":
		return fmt.Sprintf("0x%0*X", int(size)*2, u)
	case "oct":
		return "0" + strconv.FormatUint(u, 8)
	case "bin":
		return fmt.Sprintf("%0*b", int(size)*8, u)
	}
	if v.unsigned {
		return strconv.FormatUint(uint64(v.i), 10)
	}
	return strconv.FormatInt(v.i, 10)
}

// NodeAt returns the deepest visible field that holds the byte at off; nil - none
func (n *Node) NodeAt(off int64) *Node {
	if n.Kind == NodeArray {
		size, _ := n.typ.fixedSize()
		if size > 0 && off >= n.Off && off < n.Off+n.Size {
			return n.Child((off - n.Off) / size)
		}
		return nil
	}
	if n.sorted == nil {
		n.sorted = n.Children()
		sort.SliceStable(n.sorted, func(i, j int) bool { return n.sorted[i].Off < n.sorted[j].Off })
	}
	s := n.sorted
	// The last child that starts at or before off; placed fields may
	// overlap, so a few before it are tried too
	i := sort.Search(len(s), func(i int) bool { return s[i].Off > off }) - 1
	for k := i; k >= 0 && k > i-8; k-- {
		c := s[k]
		if off >= c.Off && off < c.Off+max(c.Size, 1) {
			if deeper := c.NodeAt(off); deeper != nil {
				return deeper
			}
			return c
		}
	}
	return nil
}

// Find returns the field at the path: "hdr.chunk[2].len"
func (n *Node) Find(path string) *Node {
	cur := n
	for _, part := range strings.Split(path, ".") {
		name, idx := part, -1
		if i := strings.IndexByte(part, '['); i >= 0 && strings.HasSuffix(part, "]") {
			name = part[:i]
			idx, _ = strconv.Atoi(part[i+1 : len(part)-1])
		}
		cur = cur.child(name)
		if cur == nil {
			return nil
		}
		if idx == 0 && cur.Kind != NodeArray && cur.Kind != NodeList {
			continue // a field declared once is its own element 0
		}
		if idx >= 0 {
			cur = cur.Child(int64(idx))
			if cur == nil {
				return nil
			}
		}
	}
	return cur
}

// readText reads an array of char as text, up to its first zero
func (r *Result) readText(off, n int64) string {
	return r.readTextMax(off, n, 4096)
}

// readTextMax is readText of at most maxBytes
func (r *Result) readTextMax(off, n, maxBytes int64) string {
	b := make([]byte, max(min(n, maxBytes), 0))
	k, _ := r.data.ReadAt(b, off)
	b = b[:k]
	if i := strings.IndexByte(string(b), 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}

// readValue reads a value of a simple type for an element of an array
func (r *Result) readValue(t *Type, off int64, big bool) Value {
	vt := t.valueType()
	if vt == nil || vt.kind != tkPrim {
		return Value{}
	}
	b := make([]byte, vt.size)
	if n, _ := r.data.ReadAt(b, off); n < len(b) {
		return Value{}
	}
	return decodePrim(vt, b, big)
}

func decodeWide(b []byte, big bool) string {
	u := make([]uint16, len(b)/2)
	for i := range u {
		if big {
			u[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
		} else {
			u[i] = uint16(b[2*i+1])<<8 | uint16(b[2*i])
		}
	}
	return string(utf16.Decode(u))
}
