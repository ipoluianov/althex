package tmpl

import (
	"fmt"
	"math"
	"strings"
)

// ctl is how a statement ends: normally or by break, continue, return
type ctl int

const (
	ctlNone ctl = iota
	ctlBreak
	ctlContinue
	ctlReturn
)

type cstmt func(in *interp) ctl
type cexpr func(in *interp) Value

// cattr is an attribute ready to apply
type cattr struct {
	pos   Pos
	name  string
	ident string // the value written as a bare name: a format, a function
	value cexpr
}

// funcInfo is a function of a template
type funcInfo struct {
	name    string
	pos     Pos
	retName string
	ret     *Type
	params  []param
	body    []cstmt
	kind    string // "transform", "checksum" or ""
	title   string
}

// compiler turns the syntax tree into closures; the types, functions and
// enum constants are known at compile time, the names of fields and local
// variables are found at run time
type compiler struct {
	types  map[string]*Type
	funcs  map[string]*funcInfo
	consts map[string]Value
}

func newCompiler() *compiler {
	return &compiler{types: make(map[string]*Type), funcs: make(map[string]*funcInfo), consts: make(map[string]Value)}
}

// typeOf returns the type of a name; a struct used before its body (a
// forward declaration) gets a type filled in later
func (c *compiler) typeOf(r typeRef) (*Type, error) {
	if base, ok := builtinTypes[r.name]; ok {
		return primTypes[base], nil
	}
	if t, ok := c.types[r.name]; ok {
		return t, nil
	}
	if primTypes[r.name] != nil {
		return primTypes[r.name], nil
	}
	return nil, errorf(r.pos, "unknown type %q", r.name)
}

func (c *compiler) stmts(ss []stmt) ([]cstmt, error) {
	var out []cstmt
	for _, s := range ss {
		cs, err := c.stmt(s)
		if err != nil {
			return nil, err
		}
		if cs != nil {
			out = append(out, cs)
		}
	}
	return out, nil
}

// runAll runs statements until one ends otherwise than normally
func runAll(in *interp, ss []cstmt) ctl {
	for _, s := range ss {
		if r := s(in); r != ctlNone {
			return r
		}
	}
	return ctlNone
}

func (c *compiler) stmt(s stmt) (cstmt, error) {
	switch s := s.(type) {
	case *blockStmt:
		body, err := c.stmts(s.stmts)
		if err != nil {
			return nil, err
		}
		return func(in *interp) ctl { return runAll(in, body) }, nil

	case *exprStmt:
		x, err := c.expr(s.x)
		if err != nil {
			return nil, err
		}
		pos := s.pos
		return func(in *interp) ctl {
			in.step(pos)
			x(in)
			return ctlNone
		}, nil

	case *declStmt:
		return c.decl(s)

	case *structDef:
		return nil, c.structDef(s)

	case *enumDef:
		return nil, c.enumDef(s)

	case *typedefStmt:
		return nil, c.typedef(s)

	case *funcDef:
		return nil, c.funcDef(s)

	case *ifStmt:
		cond, err := c.expr(s.cond)
		if err != nil {
			return nil, err
		}
		then, err := c.stmt(s.then)
		if err != nil {
			return nil, err
		}
		var els cstmt
		if s.els != nil {
			if els, err = c.stmt(s.els); err != nil {
				return nil, err
			}
		}
		pos := s.pos
		return func(in *interp) ctl {
			in.step(pos)
			if cond(in).truthy() {
				if then != nil {
					return then(in)
				}
			} else if els != nil {
				return els(in)
			}
			return ctlNone
		}, nil

	case *whileStmt:
		cond, err := c.expr(s.cond)
		if err != nil {
			return nil, err
		}
		body, err := c.stmt(s.body)
		if err != nil {
			return nil, err
		}
		pos, do := s.pos, s.do
		return func(in *interp) ctl {
			first := do
			for first || cond(in).truthy() {
				first = false
				in.step(pos)
				if body == nil {
					continue
				}
				switch body(in) {
				case ctlBreak:
					return ctlNone
				case ctlReturn:
					return ctlReturn
				}
			}
			return ctlNone
		}, nil

	case *forStmt:
		var init, body cstmt
		var cond, post cexpr
		var err error
		if s.init != nil {
			if init, err = c.stmt(s.init); err != nil {
				return nil, err
			}
		}
		if s.cond != nil {
			if cond, err = c.expr(s.cond); err != nil {
				return nil, err
			}
		}
		if s.post != nil {
			if post, err = c.expr(s.post); err != nil {
				return nil, err
			}
		}
		if body, err = c.stmt(s.body); err != nil {
			return nil, err
		}
		pos := s.pos
		return func(in *interp) ctl {
			if init != nil {
				init(in)
			}
			for cond == nil || cond(in).truthy() {
				in.step(pos)
				if body != nil {
					switch body(in) {
					case ctlBreak:
						return ctlNone
					case ctlReturn:
						return ctlReturn
					}
				}
				if post != nil {
					post(in)
				}
			}
			return ctlNone
		}, nil

	case *switchStmt:
		value, err := c.expr(s.value)
		if err != nil {
			return nil, err
		}
		type ccase struct {
			values []cexpr
			body   []cstmt
		}
		cases := make([]ccase, len(s.cases))
		def := -1
		for i, cc := range s.cases {
			if cc.values == nil {
				def = i
			}
			for _, v := range cc.values {
				x, err := c.expr(v)
				if err != nil {
					return nil, err
				}
				cases[i].values = append(cases[i].values, x)
			}
			if cases[i].body, err = c.stmts(cc.body); err != nil {
				return nil, err
			}
		}
		pos := s.pos
		return func(in *interp) ctl {
			in.step(pos)
			v := value(in)
			start := def
		find:
			for i, cc := range cases {
				for _, x := range cc.values {
					if compare(in, pos, "==", v, x(in)).truthy() {
						start = i
						break find
					}
				}
			}
			if start < 0 {
				return ctlNone
			}
			// From the matching case on, as in C, until a break
			for _, cc := range cases[start:] {
				switch r := runAll(in, cc.body); r {
				case ctlBreak:
					return ctlNone
				case ctlNone:
				default:
					return r
				}
			}
			return ctlNone
		}, nil

	case *branchStmt:
		r := ctlBreak
		if s.kind == "continue" {
			r = ctlContinue
		}
		return func(in *interp) ctl { return r }, nil

	case *returnStmt:
		var value cexpr
		if s.value != nil {
			var err error
			if value, err = c.expr(s.value); err != nil {
				return nil, err
			}
		}
		return func(in *interp) ctl {
			in.ret = Value{}
			if value != nil {
				in.ret = value(in)
			}
			return ctlReturn
		}, nil
	}
	return nil, errorf(s.stmtPos(), "unsupported statement")
}

// cvar is a variable of a declaration, compiled
type cvar struct {
	pos     Pos
	name    string
	isArray bool
	length  cexpr
	args    []cexpr
	bits    cexpr
	at      cexpr
	init    cexpr
	attrs   []cattr
}

func (c *compiler) decl(s *declStmt) (cstmt, error) {
	t, err := c.typeOf(s.typ)
	if err != nil {
		return nil, err
	}
	var vars []*cvar
	for _, v := range s.vars {
		cv := &cvar{pos: v.pos, name: v.name, isArray: v.isArray}
		if v.length != nil {
			if cv.length, err = c.expr(v.length); err != nil {
				return nil, err
			}
		}
		for _, a := range v.args {
			x, err := c.expr(a)
			if err != nil {
				return nil, err
			}
			cv.args = append(cv.args, x)
		}
		if v.bits != nil {
			if cv.bits, err = c.expr(v.bits); err != nil {
				return nil, err
			}
		}
		if v.at != nil {
			if cv.at, err = c.expr(v.at); err != nil {
				return nil, err
			}
		}
		if v.init != nil {
			if !s.local {
				return nil, errorf(v.pos, "only a local variable has a value given; a field is read")
			}
			if cv.init, err = c.expr(v.init); err != nil {
				return nil, err
			}
		}
		if cv.attrs, err = c.attrs(v.attrs); err != nil {
			return nil, err
		}
		vars = append(vars, cv)
	}
	typeName := s.typ.name
	if s.local {
		return func(in *interp) ctl {
			for _, v := range vars {
				in.step(v.pos)
				in.declareLocal(v, t)
			}
			return ctlNone
		}, nil
	}
	return func(in *interp) ctl {
		for _, v := range vars {
			in.step(v.pos)
			in.declare(v, t, typeName)
		}
		return ctlNone
	}, nil
}

func (c *compiler) attrs(as []attr) ([]cattr, error) {
	var out []cattr
	for _, a := range as {
		ca := cattr{pos: a.pos, name: a.name}
		if id, ok := a.value.(*identExpr); ok {
			ca.ident = id.name
		}
		if a.value != nil {
			x, err := c.expr(a.value)
			if err != nil {
				return nil, err
			}
			ca.value = x
		} else {
			ca.value = func(in *interp) Value { return Int(1) }
		}
		out = append(out, ca)
	}
	return out, nil
}

func (c *compiler) structDef(s *structDef) error {
	t, ok := c.types[s.name]
	if !ok {
		t = &Type{Name: s.name, kind: tkStruct}
		c.types[s.name] = t
	}
	if s.forward {
		return nil
	}
	if t.st != nil {
		return errorf(s.pos, "%s is defined twice", s.name)
	}
	if t.kind != tkStruct {
		return errorf(s.pos, "%s is a type already", s.name)
	}
	// The type exists before its body is compiled: it may contain itself
	t.st = &structInfo{union: s.union, params: s.params}
	body, err := c.stmts(s.body)
	if err != nil {
		return err
	}
	t.st.body = body
	if t.st.attrs, err = c.attrs(s.attrs); err != nil {
		return err
	}
	return nil
}

func (c *compiler) enumDef(e *enumDef) error {
	base, err := c.typeOf(e.base)
	if err != nil {
		return err
	}
	if vt := base.valueType(); vt == nil || vt.kind != tkPrim || vt.prim >= pHalf {
		return errorf(e.pos, "an enum is of an integer type")
	}
	t := &Type{Name: e.name, kind: tkEnum, base: base, names: make(map[int64]string), values: make(map[string]int64)}
	next := int64(0)
	for _, it := range e.items {
		v := next
		if it.value != nil {
			x, err := c.expr(it.value)
			if err != nil {
				return err
			}
			// Enum values are constant expressions: computed now, with no data
			val, err := constEval(x)
			if err != nil {
				return errorf(e.pos, "the value of %s: %v", it.name, err)
			}
			n, ok := val.AsInt()
			if !ok {
				return errorf(e.pos, "the value of %s is not a number", it.name)
			}
			v = n
		}
		v = castTo(Int(v), base).i
		if _, dup := t.values[it.name]; !dup {
			t.values[it.name] = v
		}
		if _, dup := t.names[v]; !dup {
			t.names[v] = it.name
		}
		c.consts[it.name] = castTo(Int(v), base)
		next = v + 1
	}
	c.types[e.name] = t
	return nil
}

func (c *compiler) typedef(s *typedefStmt) error {
	elem, err := c.typeOf(s.typ)
	if err != nil {
		return err
	}
	t := &Type{Name: s.name, kind: tkTypedef, elem: elem}
	t.isChar = s.typ.name == "char" || s.typ.name == "CHAR" || elem.kind == tkTypedef && elem.isChar
	if s.length != nil {
		if t.length, err = c.expr(s.length); err != nil {
			return err
		}
	}
	if t.attrs, err = c.attrs(s.attrs); err != nil {
		return err
	}
	c.types[s.name] = t
	return nil
}

func (c *compiler) funcDef(f *funcDef) error {
	if _, dup := c.funcs[f.name]; dup {
		return errorf(f.pos, "the function %s is defined twice", f.name)
	}
	fi := &funcInfo{name: f.name, pos: f.pos, retName: f.ret.name, params: f.params}
	var err error
	if fi.ret, err = c.typeOf(f.ret); err != nil {
		return err
	}
	for _, p := range f.params {
		if _, err := c.typeOf(p.typ); err != nil {
			return err
		}
	}
	// Registered before the body is compiled: it may call itself
	c.funcs[f.name] = fi
	if fi.body, err = c.stmts(f.body); err != nil {
		return err
	}
	for _, a := range f.attrs {
		if a.name == "transform" || a.name == "checksum" {
			fi.kind = a.name
			fi.title = f.name
			if lit, ok := a.value.(*litExpr); ok && lit.v.k == kString {
				fi.title = lit.v.s
			}
		}
	}
	return nil
}

// ---- Expressions ----

func (c *compiler) expr(e expr) (cexpr, error) {
	switch e := e.(type) {
	case *litExpr:
		v := e.v
		return func(in *interp) Value { return v }, nil

	case *identExpr:
		name, pos := e.name, e.pos
		if v, ok := c.consts[name]; ok {
			// An enum constant, unless a field or a variable has its name
			return func(in *interp) Value {
				if x, ok := in.lookup(name); ok {
					return x
				}
				return v
			}, nil
		}
		if v, ok := colorConsts[name]; ok {
			return func(in *interp) Value { return v }, nil
		}
		return func(in *interp) Value {
			v, ok := in.lookup(name)
			if !ok {
				in.fail(pos, "unknown name %q", name)
			}
			return v
		}, nil

	case *unaryExpr:
		if e.op == "++" || e.op == "--" {
			return c.incDec(e.x, e.op, e.pos, true)
		}
		x, err := c.expr(e.x)
		if err != nil {
			return nil, err
		}
		op, pos := e.op, e.pos
		return func(in *interp) Value { return unary(in, pos, op, x(in)) }, nil

	case *postfixExpr:
		return c.incDec(e.x, e.op, e.pos, false)

	case *binaryExpr:
		x, err := c.expr(e.x)
		if err != nil {
			return nil, err
		}
		y, err := c.expr(e.y)
		if err != nil {
			return nil, err
		}
		op, pos := e.op, e.pos
		switch op {
		case "&&":
			return func(in *interp) Value { return boolValue(x(in).truthy() && y(in).truthy()) }, nil
		case "||":
			return func(in *interp) Value { return boolValue(x(in).truthy() || y(in).truthy()) }, nil
		case ",":
			return func(in *interp) Value { x(in); return y(in) }, nil
		}
		return func(in *interp) Value { return binop(in, pos, op, x(in), y(in)) }, nil

	case *condExpr:
		cond, err := c.expr(e.cond)
		if err != nil {
			return nil, err
		}
		yes, err := c.expr(e.yes)
		if err != nil {
			return nil, err
		}
		no, err := c.expr(e.no)
		if err != nil {
			return nil, err
		}
		return func(in *interp) Value {
			if cond(in).truthy() {
				return yes(in)
			}
			return no(in)
		}, nil

	case *assignExpr:
		set, get, err := c.lvalue(e.l)
		if err != nil {
			return nil, err
		}
		r, err := c.expr(e.r)
		if err != nil {
			return nil, err
		}
		op, pos := e.op, e.pos
		return func(in *interp) Value {
			v := r(in)
			if op != "=" {
				v = binop(in, pos, strings.TrimSuffix(op, "="), get(in), v)
			}
			return set(in, v)
		}, nil

	case *callExpr:
		return c.call(e)

	case *indexExpr:
		x, err := c.expr(e.x)
		if err != nil {
			return nil, err
		}
		i, err := c.expr(e.i)
		if err != nil {
			return nil, err
		}
		pos := e.pos
		return func(in *interp) Value { return in.index(pos, x(in), i(in)) }, nil

	case *memberExpr:
		x, err := c.expr(e.x)
		if err != nil {
			return nil, err
		}
		name, pos := e.name, e.pos
		return func(in *interp) Value { return in.member(pos, x(in), name) }, nil

	case *castExpr:
		t, err := c.typeOf(e.typ)
		if err != nil {
			return nil, err
		}
		x, err := c.expr(e.x)
		if err != nil {
			return nil, err
		}
		return func(in *interp) Value { return castTo(x(in), t) }, nil

	case *sizeofExpr:
		if e.typ != nil {
			t, err := c.typeOf(*e.typ)
			if err != nil {
				// sizeof(name) where the name is a field, not a type
				return c.expr(&sizeofExpr{pos: e.pos, x: &identExpr{pos: e.typ.pos, name: e.typ.name}})
			}
			pos := e.pos
			return func(in *interp) Value { return Int(in.typeSize(pos, t)) }, nil
		}
		x, err := c.expr(e.x)
		if err != nil {
			return nil, err
		}
		pos := e.pos
		return func(in *interp) Value {
			v := x(in)
			switch v.k {
			case kNode:
				return Int(v.n.Size)
			case kString:
				return Int(int64(len(v.s)))
			case kFloat:
				return Int(8)
			case kInt:
				return Int(8)
			}
			in.fail(pos, "sizeof of nothing")
			return Value{}
		}, nil
	}
	return nil, errorf(e.exprPos(), "unsupported expression")
}

// lvalue compiles what can be assigned: a local variable, an element of a
// local array, a field of the document (written into it)
func (c *compiler) lvalue(e expr) (set func(in *interp, v Value) Value, get cexpr, err error) {
	switch e := e.(type) {
	case *identExpr:
		name, pos := e.name, e.pos
		set = func(in *interp, v Value) Value { return in.assignName(pos, name, v) }
		get = func(in *interp) Value {
			v, ok := in.lookup(name)
			if !ok {
				in.fail(pos, "unknown name %q", name)
			}
			return v
		}
		return set, get, nil
	case *indexExpr:
		x, err := c.expr(e.x)
		if err != nil {
			return nil, nil, err
		}
		var arr func(in *interp) *local
		if id, ok := e.x.(*identExpr); ok {
			name := id.name
			arr = func(in *interp) *local { return in.findLocal(name) }
		}
		i, err := c.expr(e.i)
		if err != nil {
			return nil, nil, err
		}
		pos := e.pos
		set = func(in *interp, v Value) Value {
			idx, _ := i(in).AsInt()
			if arr != nil {
				if l := arr(in); l != nil {
					if !l.isArr {
						in.fail(pos, "not an array")
					}
					if idx < 0 || idx >= int64(len(l.arr)) {
						in.fail(pos, "index %d out of range [0, %d)", idx, len(l.arr))
					}
					l.arr[idx] = castTo(v, l.typ)
					return l.arr[idx]
				}
			}
			el := in.index(pos, x(in), Int(idx))
			if el.k != kNode {
				in.fail(pos, "cannot assign to this")
			}
			return in.writeNode(pos, el.n, v)
		}
		get = func(in *interp) Value { return in.index(pos, x(in), i(in)) }
		return set, get, nil
	case *memberExpr:
		x, err := c.expr(e.x)
		if err != nil {
			return nil, nil, err
		}
		name, pos := e.name, e.pos
		set = func(in *interp, v Value) Value {
			f := in.member(pos, x(in), name)
			if f.k != kNode {
				in.fail(pos, "cannot assign to %s", name)
			}
			return in.writeNode(pos, f.n, v)
		}
		get = func(in *interp) Value { return in.member(pos, x(in), name) }
		return set, get, nil
	}
	return nil, nil, errorf(e.exprPos(), "cannot assign to this")
}

func (c *compiler) incDec(target expr, op string, pos Pos, prefix bool) (cexpr, error) {
	set, get, err := c.lvalue(target)
	if err != nil {
		return nil, err
	}
	bop := "+"
	if op == "--" {
		bop = "-"
	}
	return func(in *interp) Value {
		old := get(in).deref()
		nv := set(in, binop(in, pos, bop, old, Int(1)))
		if prefix {
			return nv
		}
		return old
	}, nil
}

func (c *compiler) call(e *callExpr) (cexpr, error) {
	pos := e.pos
	// Forms that take a field, not its value
	switch e.fn {
	case "exists", "function_exists":
		if len(e.args) != 1 {
			return nil, errorf(pos, "%s takes one argument", e.fn)
		}
		if e.fn == "function_exists" {
			lit, ok := e.args[0].(*litExpr)
			name := ""
			if ok {
				name = lit.v.s
			} else if id, ok := e.args[0].(*identExpr); ok {
				name = id.name
			}
			_, isFunc := c.funcs[name]
			return func(in *interp) Value { return boolValue(isFunc || in.prog.funcs[name] != nil) }, nil
		}
		x, err := c.expr(e.args[0])
		if err != nil {
			return nil, err
		}
		return func(in *interp) Value { return boolValue(in.exists(x)) }, nil
	case "startof":
		if len(e.args) != 1 {
			return nil, errorf(pos, "startof takes one argument")
		}
		x, err := c.expr(e.args[0])
		if err != nil {
			return nil, err
		}
		return func(in *interp) Value {
			v := x(in)
			if v.k != kNode {
				in.fail(pos, "startof needs a field")
			}
			return Int(v.n.Off)
		}, nil
	}
	args := make([]cexpr, len(e.args))
	for i, a := range e.args {
		x, err := c.expr(a)
		if err != nil {
			return nil, err
		}
		args[i] = x
	}
	name := e.fn
	return func(in *interp) Value {
		vals := make([]Value, len(args))
		for i, a := range args {
			vals[i] = a(in)
		}
		if f := in.prog.funcs[name]; f != nil {
			return in.callFunc(pos, f, vals)
		}
		if b := builtins[name]; b != nil {
			return b(in, pos, vals)
		}
		in.fail(pos, "unknown function %s", name)
		return Value{}
	}, nil
}

func boolValue(b bool) Value {
	if b {
		return Int(1)
	}
	return Int(0)
}

func unary(in *interp, pos Pos, op string, x Value) Value {
	x = x.deref()
	switch op {
	case "!":
		return boolValue(!x.truthy())
	case "+":
		return x
	case "-":
		if x.k == kFloat {
			return Float(-x.f)
		}
		if x.k == kInt {
			return Value{k: kInt, i: -x.i, unsigned: x.unsigned}
		}
	case "~":
		if x.k == kInt {
			return Value{k: kInt, i: ^x.i, unsigned: x.unsigned}
		}
	}
	in.fail(pos, "%s of %s", op, kindName(x))
	return Value{}
}

func kindName(v Value) string {
	switch v.k {
	case kInt:
		return "a number"
	case kFloat:
		return "a float"
	case kString:
		return "a string"
	case kNode:
		return "a struct or an array"
	}
	return "nothing"
}

// textValue turns an array of char into its text, for comparisons and joins
func textValue(v Value) Value {
	if v.k == kNode && v.n != nil && v.n.isText {
		return String(v.n.res.readTextMax(v.n.Off, v.n.Size, 16<<20))
	}
	return v
}

func compare(in *interp, pos Pos, op string, x, y Value) Value {
	x, y = textValue(x).deref(), textValue(y).deref()
	var c int
	switch {
	case x.k == kString && y.k == kString:
		c = strings.Compare(x.s, y.s)
	case x.k == kString || y.k == kString:
		// A char array field against a string
		in.fail(pos, "%s %s %s", kindName(x), op, kindName(y))
	case x.k == kFloat || y.k == kFloat:
		a, b := x.asFloat(), y.asFloat()
		switch {
		case a < b:
			c = -1
		case a > b:
			c = 1
		}
	case x.k == kInt && y.k == kInt:
		if x.unsigned || y.unsigned {
			a, b := uint64(x.i), uint64(y.i)
			switch {
			case a < b:
				c = -1
			case a > b:
				c = 1
			}
		} else {
			switch {
			case x.i < y.i:
				c = -1
			case x.i > y.i:
				c = 1
			}
		}
	case x.k == kNode && y.k == kNode:
		if x.n == y.n {
			c = 0
		} else {
			c = 1
		}
	default:
		in.fail(pos, "%s %s %s", kindName(x), op, kindName(y))
	}
	switch op {
	case "==":
		return boolValue(c == 0)
	case "!=":
		return boolValue(c != 0)
	case "<":
		return boolValue(c < 0)
	case "<=":
		return boolValue(c <= 0)
	case ">":
		return boolValue(c > 0)
	}
	return boolValue(c >= 0)
}

func binop(in *interp, pos Pos, op string, x, y Value) Value {
	switch op {
	case "==", "!=", "<", "<=", ">", ">=":
		return compare(in, pos, op, x, y)
	}
	// A char array is its text in expressions
	x, y = textValue(x).deref(), textValue(y).deref()
	if op == "+" && (x.k == kString || y.k == kString) {
		return String(textOf(x) + textOf(y))
	}
	if !x.num() || !y.num() {
		in.fail(pos, "%s %s %s", kindName(x), op, kindName(y))
	}
	if x.k == kFloat || y.k == kFloat {
		a, b := x.asFloat(), y.asFloat()
		switch op {
		case "+":
			return Float(a + b)
		case "-":
			return Float(a - b)
		case "*":
			return Float(a * b)
		case "/":
			return Float(a / b)
		case "%":
			return Float(math.Mod(a, b))
		}
		in.fail(pos, "%s of floats", op)
	}
	uns := x.unsigned || y.unsigned
	a, b := x.i, y.i
	r := Value{k: kInt, unsigned: uns}
	switch op {
	case "+":
		r.i = a + b
	case "-":
		r.i = a - b
	case "*":
		r.i = a * b
	case "/", "%":
		if b == 0 {
			in.fail(pos, "division by zero")
		}
		switch {
		case uns && op == "/":
			r.i = int64(uint64(a) / uint64(b))
		case uns:
			r.i = int64(uint64(a) % uint64(b))
		case op == "/":
			r.i = a / b
		default:
			r.i = a % b
		}
	case "&":
		r.i = a & b
	case "|":
		r.i = a | b
	case "^":
		r.i = a ^ b
	case "<<":
		r.i = a << uint64(b&63)
		r.unsigned = x.unsigned
	case ">>":
		if x.unsigned {
			r.i = int64(uint64(a) >> uint64(b&63))
		} else {
			r.i = a >> uint64(b&63)
		}
		r.unsigned = x.unsigned
	default:
		in.fail(pos, "unknown operator %s", op)
	}
	return r
}

// textOf is a value as text, for joining strings
func textOf(v Value) string {
	v = textValue(v).deref()
	switch v.k {
	case kString:
		return v.s
	case kInt:
		if v.unsigned {
			return fmt.Sprint(uint64(v.i))
		}
		return fmt.Sprint(v.i)
	case kFloat:
		return fmt.Sprint(v.f)
	case kNode:
		return v.n.Value()
	}
	return ""
}

// colorConsts are the color names of 010 templates, as 0xRRGGBB
var colorConsts = map[string]Value{
	"cBlack": Int(0x000000), "cRed": Int(0xff0000), "cDkRed": Int(0x800000), "cLtRed": Int(0xff8080),
	"cGreen": Int(0x00ff00), "cDkGreen": Int(0x008000), "cLtGreen": Int(0x80ff80),
	"cBlue": Int(0x0000ff), "cDkBlue": Int(0x000080), "cLtBlue": Int(0x8080ff),
	"cPurple": Int(0xff00ff), "cDkPurple": Int(0x800080), "cLtPurple": Int(0xffe0ff),
	"cAqua": Int(0x00ffff), "cDkAqua": Int(0x008080), "cLtAqua": Int(0xe0ffff),
	"cYellow": Int(0xffff00), "cDkYellow": Int(0x808000), "cLtYellow": Int(0xffff80),
	"cGray": Int(0x808080), "cDkGray": Int(0x404040), "cLtGray": Int(0xe0e0e0),
	"cSilver": Int(0xc0c0c0), "cWhite": Int(0xffffff), "cNone": Int(-1),
}
