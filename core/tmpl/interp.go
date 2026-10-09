package tmpl

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// local is a local variable: a value or an array of them
type local struct {
	v     Value
	arr   []Value
	isArr bool
	typ   *Type
}

// scope is where names are found: the local variables, the fields of the
// struct being read; then the scope it is in. A function's scope is in the
// global one, not in the caller's.
type scope struct {
	vars   map[string]*local
	node   *Node
	parent *scope
	union  *unionState
}

// unionState: the fields of a union all start at its start
type unionState struct {
	start int64
	end   int64
}

// bitState is the value bit fields are taken from
type bitState struct {
	active bool
	size   int
	off    int64
	used   int
	signed bool
}

// The orders of bit fields: by the byte order (from the lowest bit in
// little-endian), from the lowest bit, from the highest
const (
	bitsByOrder = iota
	bitsRightToLeft
	bitsLeftToRight
)

// runError is a panic of the runtime: an error of the template
type runError struct{ err *Error }

// stopRun is a panic of Exit()
type stopRun struct{ code int64 }

type interp struct {
	ctx    context.Context
	prog   *Program
	env    *Env
	rd     *reader
	res    *Result
	pos    int64
	big    bool
	sc     *scope
	global *scope
	ret    Value
	bits   bitState
	// bitOrder is the order bit fields take their bits in
	bitOrder int

	steps    int64
	maxSteps int64
	nodes    int
	maxNodes int
	depth    int
	console  bool // the console: fields are not declared

	// Nodes whose shown value or comment a function makes, after the run
	pending []pendingDisplay
}

type pendingDisplay struct {
	node    *Node
	read    *funcInfo
	comment *funcInfo
}

const (
	defaultMaxSteps = 200_000_000
	defaultMaxNodes = 1_000_000
	maxDepth        = 256
)

func (in *interp) fail(pos Pos, format string, args ...any) {
	panic(runError{&Error{Pos: pos, Msg: fmt.Sprintf(format, args...)}})
}

// step counts the work, so a template that loops forever stops
func (in *interp) step(pos Pos) {
	in.steps++
	if in.steps > in.maxSteps {
		in.fail(pos, "the template runs too long (%d steps)", in.maxSteps)
	}
	if in.steps&0xfff == 0 && in.ctx.Err() != nil {
		in.fail(pos, "canceled")
	}
}

// ---- Names ----

func (in *interp) lookup(name string) (Value, bool) {
	for s := in.sc; s != nil; s = s.parent {
		if l, ok := s.vars[name]; ok {
			if l.isArr {
				return Value{k: kNode, n: in.localArrayNode(name, l)}, true
			}
			return l.v, true
		}
		if s.node != nil {
			if n := s.node.child(name); n != nil {
				return Value{k: kNode, n: n}, true
			}
		}
	}
	if v, ok := in.prog.consts[name]; ok {
		return v, true
	}
	return Value{}, false
}

// localArrayNode wraps a local array so it can be indexed like a field
func (in *interp) localArrayNode(name string, l *local) *Node {
	return &Node{Name: name, Kind: NodeList, local: l}
}

func (in *interp) findLocal(name string) *local {
	for s := in.sc; s != nil; s = s.parent {
		if l, ok := s.vars[name]; ok {
			return l
		}
		if s.node != nil && s.node.child(name) != nil {
			return nil
		}
	}
	return nil
}

func (in *interp) assignName(pos Pos, name string, v Value) Value {
	for s := in.sc; s != nil; s = s.parent {
		if l, ok := s.vars[name]; ok {
			if l.isArr {
				in.fail(pos, "cannot assign to the array %s", name)
			}
			if l.typ != nil && l.typ.kind == tkStruct || v.k == kNode && !v.n.hasValue() {
				l.v = v // a struct passed by reference
			} else {
				l.v = castTo(v, l.typ)
			}
			return l.v
		}
		if s.node != nil {
			if n := s.node.child(name); n != nil {
				return in.writeNode(pos, n, v)
			}
		}
	}
	in.fail(pos, "unknown name %q", name)
	return Value{}
}

func (in *interp) exists(x cexpr) (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			if _, isErr := r.(runError); !isErr {
				panic(r)
			}
			ok = false
		}
	}()
	v := x(in)
	return v.k != kVoid
}

// ---- Locals ----

func (in *interp) declareLocal(v *cvar, t *Type) {
	if in.sc.vars == nil {
		in.sc.vars = make(map[string]*local)
	}
	vt := t.resolved()
	l := &local{typ: vt}
	if v.isArray {
		n := int64(0)
		if v.length != nil {
			n = in.toInt(v.pos, v.length(in))
		}
		if n < 0 || n > 1<<24 {
			in.fail(v.pos, "a local array of %d elements", n)
		}
		if vt.isCharType() && v.init != nil {
			// local char s[] = "text": a string
			l.v = castTo(v.init(in), primTypes["string"])
			l.typ = primTypes["string"]
			in.sc.vars[v.name] = l
			return
		}
		l.isArr = true
		l.arr = make([]Value, n)
		for i := range l.arr {
			l.arr[i] = castTo(Int(0), vt)
		}
		in.sc.vars[v.name] = l
		return
	}
	switch {
	case v.init != nil:
		init := v.init(in)
		if init.k == kNode && !init.n.hasValue() {
			l.v = init
		} else {
			l.v = castTo(init, vt)
		}
	case vt.kind == tkString:
		l.v = String("")
	case vt.kind == tkStruct:
		in.fail(v.pos, "a local variable of a struct type: declare a field, or pass one")
	default:
		l.v = castTo(Int(0), vt)
	}
	in.sc.vars[v.name] = l
}

func (t *Type) isCharType() bool {
	t = t.resolved()
	return t != nil && t.kind == tkPrim && (t.prim == pChar || t.prim == pUchar)
}

func (in *interp) toInt(pos Pos, v Value) int64 {
	n, ok := v.AsInt()
	if !ok {
		in.fail(pos, "a number expected, %s found", kindName(v.deref()))
	}
	return n
}

// ---- Fields ----

// fieldScope returns the scope whose struct gets the fields declared now
func (in *interp) fieldScope(pos Pos) *scope {
	for s := in.sc; s != nil; s = s.parent {
		if s.node != nil {
			return s
		}
	}
	in.fail(pos, "a field is declared outside of a struct")
	return nil
}

// declare reads a field of the document at the current position (or at
// its @ offset) and adds it to the struct being read
func (in *interp) declare(v *cvar, t *Type, typeName string) {
	if in.console {
		in.fail(v.pos, "fields are not declared in the console; use the Read functions")
	}
	sc := in.fieldScope(v.pos)
	placed := v.at != nil
	saved := in.pos
	if placed {
		in.pos = in.toInt(v.pos, v.at(in))
	}
	if sc.union != nil {
		in.pos = sc.union.start
	}
	var n *Node
	if v.bits != nil {
		n = in.readBits(v, t, typeName)
		sc.node.add(n)
	} else {
		in.bits.active = false
		var args []Value
		for _, a := range v.args {
			args = append(args, a(in))
		}
		var count int64 = -1
		if v.isArray {
			if v.length == nil {
				in.fail(v.pos, "the length of the array %s is not given", v.name)
			}
			count = in.toInt(v.pos, v.length(in))
			if count < 0 {
				in.fail(v.pos, "the array %s has %d elements", v.name, count)
			}
		}
		n = in.readField(sc.node, v.pos, t, typeName, v.name, count, args)
	}
	in.applyAttrs(n, v.attrs)
	// A repeated field: its list ends where its last element does
	if l := n.Parent; l != nil && l != sc.node && l.repeat {
		end := max(l.Off+l.Size, n.Off+n.Size)
		l.Off = min(l.Off, n.Off)
		l.Size = end - l.Off
	}
	if sc.union != nil {
		sc.union.end = max(sc.union.end, n.Off+n.Size)
		in.pos = sc.union.start
	}
	if placed {
		in.pos = saved
	}
}

// newNode makes a node at the current position, counting it
func (in *interp) newNode(pos Pos, name, typeName string, kind NodeKind, t *Type) *Node {
	in.nodes++
	if in.nodes > in.maxNodes {
		in.fail(pos, "too many fields (%d)", in.maxNodes)
	}
	return &Node{Name: name, TypeName: typeName, Kind: kind, Off: in.pos, typ: t, big: in.big, res: in.res}
}

// readField reads a field of the type (an array of count elements when
// count >= 0) at the current position, added to parent, and moves the
// position past it
func (in *interp) readField(parent *Node, pos Pos, t *Type, typeName, name string, count int64, args []Value) *Node {
	// A typedef is another name of a type, or an array of it; its
	// attributes go to the field
	var attrs []cattr
	for t.kind == tkTypedef {
		attrs = append(attrs, t.attrs...)
		if t.length != nil {
			if count >= 0 {
				// An array of arrays: a list whose elements are arrays of the typedef
				n := in.readArray(parent, pos, t, typeName, name, count, args)
				in.applyAttrs(n, attrs)
				return n
			}
			count = in.toInt(pos, t.length(in))
			if count < 0 {
				in.fail(pos, "the array %s has %d elements", name, count)
			}
		}
		t = t.elem
	}
	n := in.readValueField(parent, pos, t, typeName, name, count, args)
	in.applyAttrs(n, attrs)
	return n
}

func (in *interp) readValueField(parent *Node, pos Pos, t *Type, typeName, name string, count int64, args []Value) *Node {
	if count >= 0 {
		return in.readArray(parent, pos, t, typeName, name, count, args)
	}
	switch t.kind {
	case tkPrim, tkEnum:
		vt := t.valueType()
		n := in.newNode(pos, name, typeName, NodeValue, t)
		n.Size = int64(vt.size)
		b := in.rd.read(pos, in, n.Off, vt.size)
		n.val = decodePrim(vt, b, in.big)
		parent.add(n)
		in.pos = n.Off + n.Size
		return n
	case tkString:
		n := in.newNode(pos, name, typeName, NodeString, t)
		s, size := in.rd.readZString(n.Off, t.wide, in.big)
		n.val = String(s)
		n.Size = size
		parent.add(n)
		in.pos = n.Off + n.Size
		return n
	case tkStruct:
		return in.readStruct(parent, pos, t, typeName, name, args)
	case tkVoid:
		in.fail(pos, "a field of type void")
	}
	in.fail(pos, "cannot read a field of type %s", t.Name)
	return nil
}

func (in *interp) readArray(parent *Node, pos Pos, t *Type, typeName, name string, count int64, args []Value) *Node {
	if size, ok := t.fixedSize(); ok {
		// Simple elements: one node, read when looked at
		n := in.newNode(pos, name, fmt.Sprintf("%s[%d]", typeName, count), NodeArray, t)
		n.count = count
		n.Size = count * size
		if n.Off+n.Size > in.rd.size() || n.Size < 0 {
			in.fail(pos, "%s[%d] at 0x%X goes past the end of the data (0x%X)", name, count, n.Off, in.rd.size())
		}
		n.isText = typeName == "char" || typeName == "CHAR"
		if td := in.prog.types[typeName]; td != nil && td.kind == tkTypedef && td.isChar {
			n.isText = true
		}
		parent.add(n)
		in.pos = n.Off + n.Size
		return n
	}
	list := in.newNode(pos, name, fmt.Sprintf("%s[%d]", typeName, count), NodeList, t)
	parent.add(list)
	list.fields = make(map[string]*Node)
	for i := int64(0); i < count; i++ {
		in.step(pos)
		el := in.readField(list, pos, t, typeName, fmt.Sprintf("%s[%d]", name, i), -1, args)
		_ = el
		list.Size = in.pos - list.Off
	}
	// The elements are found by index, not by these names
	list.fields = nil
	in.pos = list.Off + list.Size
	return list
}

func (in *interp) readStruct(parent *Node, pos Pos, t *Type, typeName, name string, args []Value) *Node {
	if t.st == nil {
		in.fail(pos, "the struct %s is declared but not defined", t.Name)
	}
	if in.depth >= maxDepth {
		in.fail(pos, "structs nested too deep (%d)", maxDepth)
	}
	n := in.newNode(pos, name, typeName, NodeStruct, t)
	parent.add(n)
	vars := make(map[string]*local)
	for i, p := range t.st.params {
		if i >= len(args) {
			in.fail(pos, "%s gets %d arguments, %d given", t.Name, len(t.st.params), len(args))
		}
		vars[p.name] = in.bindParam(pos, p, args[i])
	}
	sc := &scope{vars: vars, node: n, parent: in.sc}
	if t.st.union {
		sc.union = &unionState{start: n.Off, end: n.Off}
	}
	saved, savedBits := in.sc, in.bits
	in.sc = sc
	in.bits = bitState{}
	in.depth++
	defer func() {
		// Also when the body fails: the fields read so far stay
		in.depth--
		in.sc = saved
		in.bits = savedBits
	}()
	runAll(in, t.st.body)
	end := in.pos
	if sc.union != nil {
		end = sc.union.end
	}
	n.Size = max(end-n.Off, 0)
	in.pos = n.Off + n.Size
	in.applyAttrs(n, t.st.attrs)
	return n
}

// readBits reads a bit field: from the value of its type shared by the bit
// fields before it while it has bits left
func (in *interp) readBits(v *cvar, t *Type, typeName string) *Node {
	vt := t.valueType()
	if vt == nil || vt.kind != tkPrim || vt.prim >= pHalf {
		in.fail(v.pos, "a bit field of a type that is not an integer")
	}
	nbits := int(in.toInt(v.pos, v.bits(in)))
	if nbits < 1 || nbits > vt.size*8 {
		in.fail(v.pos, "a bit field of %d bits in %s", nbits, vt.Name)
	}
	b := &in.bits
	if !b.active || b.size != vt.size || b.used+nbits > b.size*8 || v.at != nil {
		*b = bitState{active: true, size: vt.size, off: in.pos, signed: vt.signed}
		in.pos += int64(vt.size)
	}
	raw := in.rd.read(v.pos, in, b.off, vt.size)
	unit := uint64(decodePrim(primTypes[unsignedName(vt.size)], raw, in.big).i)
	shift := b.used
	if in.bitOrder == bitsLeftToRight || in.bitOrder == bitsByOrder && in.big {
		shift = b.size*8 - b.used - nbits
	}
	val := (unit >> uint(shift)) & (1<<uint(nbits) - 1)
	n := &Node{Name: v.name, TypeName: typeName, Kind: NodeValue, Off: b.off, Size: int64(vt.size),
		typ: t, big: in.big, res: in.res, bitOff: shift, bitLen: nbits}
	in.nodes++
	x := Value{k: kInt, i: int64(val), unsigned: !vt.signed}
	if vt.signed && val&(1<<uint(nbits-1)) != 0 {
		x.i -= int64(1) << uint(nbits)
	}
	n.val = x
	n.TypeName = fmt.Sprintf("%s : %d", typeName, nbits)
	b.used += nbits
	return n
}

func unsignedName(size int) string {
	switch size {
	case 1:
		return "uchar"
	case 2:
		return "uint16"
	case 4:
		return "uint32"
	}
	return "uint64"
}

// applyAttrs applies the attributes of a declaration to its field
func (in *interp) applyAttrs(n *Node, attrs []cattr) {
	for _, a := range attrs {
		switch a.name {
		case "format":
			switch a.ident {
			case "hex", "dec", "oct", "bin", "binary":
				n.format = strings.TrimSuffix(a.ident, "ary")
			default:
				in.fail(a.pos, "format is hex, dec, oct or bin")
			}
		case "comment":
			if f := in.prog.funcs[a.ident]; a.ident != "" && f != nil {
				in.pending = append(in.pending, pendingDisplay{node: n, comment: f})
			} else {
				n.Comment = textOf(a.value(in))
			}
		case "read":
			f := in.prog.funcs[a.ident]
			if f == nil {
				in.fail(a.pos, "read= needs a function")
			}
			in.pending = append(in.pending, pendingDisplay{node: n, read: f})
		case "name":
			n.Name = textOf(a.value(in))
		case "color", "bgcolor":
			if c, ok := a.value(in).AsInt(); ok && c >= 0 {
				n.Color = uint32(c&0xffffff) | 1<<24
			}
		case "hidden":
			n.Hidden = a.value(in).truthy()
		case "open":
			n.Open = a.value(in).truthy()
		}
		// Other attributes of 010 templates (fgcolor, optimize, size...) are ignored
	}
}

// ---- Values of fields ----

func (in *interp) index(pos Pos, x, i Value) Value {
	idx := in.toInt(pos, i)
	if x.k == kString {
		if idx < 0 || idx >= int64(len(x.s)) {
			in.fail(pos, "index %d out of the string of %d", idx, len(x.s))
		}
		return Int(int64(x.s[idx]))
	}
	if x.k != kNode {
		in.fail(pos, "%s is not an array", kindName(x))
	}
	n := x.n
	if n.local != nil {
		if idx < 0 || idx >= int64(len(n.local.arr)) {
			in.fail(pos, "index %d out of range [0, %d)", idx, len(n.local.arr))
		}
		return n.local.arr[idx]
	}
	switch n.Kind {
	case NodeArray:
		if idx < 0 || idx >= n.count {
			in.fail(pos, "index %d out of %s[%d]", idx, n.Name, n.count)
		}
		return Value{k: kNode, n: n.Child(idx)}
	case NodeList:
		if idx < 0 || idx >= int64(len(n.children)) {
			in.fail(pos, "index %d out of %s[%d]", idx, n.Name, len(n.children))
		}
		return Value{k: kNode, n: n.children[idx]}
	case NodeString:
		return in.index(pos, n.val, i)
	}
	// A field declared once in a loop is the element 0 of its list to be
	if idx == 0 {
		return x
	}
	in.fail(pos, "%s is not an array", n.Name)
	return Value{}
}

func (in *interp) member(pos Pos, x Value, name string) Value {
	if x.k != kNode || x.n == nil {
		in.fail(pos, "%s has no field %s", kindName(x), name)
	}
	if c := x.n.child(name); c != nil {
		return Value{k: kNode, n: c}
	}
	in.fail(pos, "%s has no field %s", x.n.Name, name)
	return Value{}
}

// writeNode writes a value into a field of the document: an edit
func (in *interp) writeNode(pos Pos, n *Node, v Value) Value {
	if n.Kind != NodeValue {
		in.fail(pos, "only a field of a simple type can be assigned")
	}
	vt := n.typ.valueType()
	var b []byte
	if n.bitLen > 0 {
		raw := in.rd.read(pos, in, n.Off, int(n.Size))
		ut := primTypes[unsignedName(int(n.Size))]
		unit := uint64(decodePrim(ut, raw, n.big).i)
		mask := uint64(1)<<uint(n.bitLen) - 1
		x, _ := v.AsInt()
		unit = unit&^(mask<<uint(n.bitOff)) | (uint64(x)&mask)<<uint(n.bitOff)
		b = encodePrim(ut, Uint(unit), n.big)
		n.val = castTo(Int(int64(uint64(x)&mask)), vt)
	} else {
		b = encodePrim(vt, v, n.big)
		n.val = decodePrim(vt, b, n.big)
	}
	in.write(pos, n.Off, b)
	return n.val
}

// write changes the document; the reader forgets what it read
func (in *interp) write(pos Pos, off int64, b []byte) {
	if in.env.Writer == nil {
		in.fail(pos, "the template writes, but the document is read-only here")
	}
	if err := in.env.Writer.Overwrite(off, b); err != nil {
		in.fail(pos, "writing at 0x%X: %v", off, err)
	}
	in.res.Wrote = true
	in.rd.invalidate()
}

func (in *interp) typeSize(pos Pos, t *Type) int64 {
	t2 := t
	for t2.kind == tkTypedef && t2.length == nil {
		t2 = t2.elem
	}
	if t2.kind == tkTypedef {
		size, ok := t2.elem.fixedSize()
		if ok {
			return size * in.toInt(pos, t2.length(in))
		}
	}
	if size, ok := t.fixedSize(); ok {
		return size
	}
	in.fail(pos, "the size of %s is known only when it is read", t.Name)
	return 0
}

// ---- Functions ----

func (in *interp) bindParam(pos Pos, p param, arg Value) *local {
	t := primTypes["int64"]
	if pt, ok := in.prog.types[p.typ.name]; ok {
		t = pt
	} else if base, ok := builtinTypes[p.typ.name]; ok {
		t = primTypes[base]
	}
	// A struct, an array or a field passed by reference stays a field
	if p.byRef || p.isArray || arg.k == kNode && !arg.n.hasValue() || t.resolved().kind == tkStruct {
		return &local{v: arg, typ: t}
	}
	if t.resolved().kind == tkString {
		return &local{v: String(textOf(arg)), typ: t}
	}
	return &local{v: castTo(arg, t), typ: t}
}

func (in *interp) callFunc(pos Pos, f *funcInfo, args []Value) Value {
	if len(args) != len(f.params) {
		in.fail(pos, "%s takes %d arguments, %d given", f.name, len(f.params), len(args))
	}
	if in.depth >= maxDepth {
		in.fail(pos, "calls nested too deep (%d)", maxDepth)
	}
	vars := make(map[string]*local, len(args))
	for i, p := range f.params {
		vars[p.name] = in.bindParam(pos, p, args[i])
	}
	saved := in.sc
	in.sc = &scope{vars: vars, parent: in.global}
	in.depth++
	in.ret = Value{}
	defer func() {
		in.depth--
		in.sc = saved
	}()
	in.step(pos)
	runAll(in, f.body)
	ret := in.ret
	in.ret = Value{}
	if f.ret.kind == tkVoid {
		return Value{}
	}
	if ret.k == kNode && !ret.n.hasValue() {
		return ret
	}
	return castTo(ret, f.ret)
}

// ---- Reading the data ----

// reader reads the data through a window, as fields are small and near
type reader struct {
	data   Data
	buf    []byte
	bufOff int64
	bufLen int
	sz     int64
}

const readerWindow = 64 << 10

func newReader(d Data) *reader {
	return &reader{data: d, buf: make([]byte, readerWindow), bufOff: -1, sz: d.Size()}
}

func (r *reader) size() int64 { return r.sz }

func (r *reader) invalidate() {
	r.bufOff = -1
	r.sz = r.data.Size()
}

// read returns n bytes at off; fails past the end
func (r *reader) read(pos Pos, in *interp, off int64, n int) []byte {
	if off < 0 || off+int64(n) > r.sz {
		in.fail(pos, "reading %d bytes at 0x%X goes past the end of the data (0x%X)", n, off, r.sz)
	}
	if n > readerWindow {
		b := make([]byte, n)
		if k, err := r.data.ReadAt(b, off); k < n {
			in.fail(pos, "reading at 0x%X: %v", off, err)
		}
		return b
	}
	if r.bufOff < 0 || off < r.bufOff || off+int64(n) > r.bufOff+int64(r.bufLen) {
		k, err := r.data.ReadAt(r.buf, off)
		if k < n {
			if err == nil {
				err = io.ErrUnexpectedEOF
			}
			in.fail(pos, "reading at 0x%X: %v", off, err)
		}
		r.bufOff, r.bufLen = off, k
	}
	return r.buf[off-r.bufOff : off-r.bufOff+int64(n)]
}

// readZString reads text to its zero (one byte or two); returns it and the
// size with the zero
func (r *reader) readZString(off int64, wide, big bool) (string, int64) {
	return r.readZStringMax(off, wide, big, 1<<20)
}

// readZStringMax is readZString that reads at most maxBytes
func (r *reader) readZStringMax(off int64, wide, big bool, maxBytes int) (string, int64) {
	unit := int64(1)
	if wide {
		unit = 2
	}
	var raw []byte
	chunk := make([]byte, 4096)
	for p := off; p < r.sz; {
		k, _ := r.data.ReadAt(chunk, p)
		if k == 0 {
			break
		}
		c := chunk[:k-k%int(unit)]
		for i := 0; i+int(unit) <= len(c); i += int(unit) {
			if c[i] == 0 && (!wide || c[i+1] == 0) {
				raw = append(raw, c[:i]...)
				s := string(raw)
				if wide {
					s = decodeWide(raw, big)
				}
				return s, int64(len(raw)) + unit
			}
		}
		raw = append(raw, c...)
		p += int64(len(c))
		if len(c) == 0 || len(raw) >= maxBytes {
			raw = raw[:min(len(raw), maxBytes)]
			break
		}
	}
	// No zero before the end: the text to the end
	s := string(raw)
	if wide {
		s = decodeWide(raw, big)
	}
	return s, int64(len(raw))
}

// constEval computes a constant expression at compile time (enum values)
func constEval(x cexpr) (v Value, err error) {
	in := &interp{ctx: context.Background(), prog: &Program{consts: map[string]Value{}, funcs: map[string]*funcInfo{}},
		maxSteps: 1000, maxNodes: 0, env: &Env{}}
	in.global = &scope{}
	in.sc = in.global
	defer func() {
		if r := recover(); r != nil {
			re, ok := r.(runError)
			if !ok {
				panic(r)
			}
			err = re.err
		}
	}()
	return x(in), nil
}
