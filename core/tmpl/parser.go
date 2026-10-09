package tmpl

import (
	"fmt"
	"strings"
)

// builtinTypes are the names of the simple types and the words for them
var builtinTypes = map[string]string{
	"char": "char", "int8": "char", "byte": "char", "CHAR": "char", "BOOL": "char", "bool": "char",
	"uchar": "uchar", "uint8": "uchar", "ubyte": "uchar", "UCHAR": "uchar", "BYTE": "uchar", "UBYTE": "uchar",
	"int16": "int16", "short": "int16", "SHORT": "int16", "INT16": "int16",
	"uint16": "uint16", "ushort": "uint16", "USHORT": "uint16", "WORD": "uint16", "UINT16": "uint16",
	"int32": "int32", "int": "int32", "long": "int32", "INT": "int32", "LONG": "int32", "INT32": "int32",
	"uint32": "uint32", "uint": "uint32", "ulong": "uint32", "UINT": "uint32", "ULONG": "uint32", "DWORD": "uint32", "UINT32": "uint32",
	"int64": "int64", "quad": "int64", "__int64": "int64", "QUAD": "int64", "INT64": "int64",
	"uint64": "uint64", "uquad": "uint64", "__uint64": "uint64", "UQUAD": "uint64", "QWORD": "uint64", "UINT64": "uint64",
	"float": "float", "FLOAT": "float", "double": "double", "DOUBLE": "double", "hfloat": "hfloat", "HFLOAT": "hfloat",
	"string": "string", "wstring": "wstring", "wchar_t": "uint16", "void": "void",
	"LONGLONG": "int64", "ULONGLONG": "uint64", "int8_t": "char", "uint8_t": "uchar", "int16_t": "int16", "uint16_t": "uint16",
	"int32_t": "int32", "uint32_t": "uint32", "int64_t": "int64", "uint64_t": "uint64", "DWORD64": "uint64", "BOOLEAN": "uchar",
}

type parser struct {
	toks  []token
	i     int
	types map[string]bool // the names that are types, as declared so far
	anon  int
}

func parse(src string) ([]stmt, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks, types: make(map[string]bool)}
	var prog []stmt
	for !p.at(tEOF, "") {
		s, err := p.stmt(true)
		if err != nil {
			return nil, err
		}
		prog = append(prog, s...)
	}
	return prog, nil
}

func (p *parser) tok() token { return p.toks[p.i] }
func (p *parser) peek(n int) token {
	if p.i+n < len(p.toks) {
		return p.toks[p.i+n]
	}
	return p.toks[len(p.toks)-1]
}

// at tells whether the token is of the kind (and has the text, if given)
func (p *parser) at(kind tokKind, text string) bool {
	t := p.tok()
	return t.kind == kind && (text == "" || t.text == text)
}

func (p *parser) isOp(text string) bool    { return p.at(tOp, text) }
func (p *parser) isIdent(text string) bool { return p.at(tIdent, text) }

func (p *parser) next() token {
	t := p.tok()
	if p.i < len(p.toks)-1 {
		p.i++
	}
	return t
}

func (p *parser) expectOp(op string) (token, error) {
	if !p.isOp(op) {
		return p.tok(), p.unexpected(fmt.Sprintf("%q", op))
	}
	return p.next(), nil
}

func (p *parser) expectIdent() (token, error) {
	if !p.at(tIdent, "") {
		return p.tok(), p.unexpected("a name")
	}
	return p.next(), nil
}

func (p *parser) unexpected(want string) error {
	t := p.tok()
	got := t.text
	switch t.kind {
	case tEOF:
		got = "the end"
	case tInt, tFloat, tChar:
		got = "a number"
	case tString:
		got = "a string"
	}
	return errorf(t.pos, "%s expected, %s found", want, quoteTok(got, t.kind))
}

func quoteTok(s string, k tokKind) string {
	if k == tIdent || k == tOp {
		return "\"" + s + "\""
	}
	return s
}

// isTypeName tells whether the name is a type
func (p *parser) isTypeName(name string) bool {
	_, ok := builtinTypes[name]
	return ok || p.types[name]
}

// atType tells whether a type starts at the current token
func (p *parser) atType() bool {
	t := p.tok()
	if t.kind != tIdent {
		return false
	}
	switch t.text {
	case "struct", "union", "enum", "unsigned", "signed", "const":
		return true
	}
	return p.isTypeName(t.text)
}

// stmt parses a statement; a struct with variables after it gives two
func (p *parser) stmt(top bool) ([]stmt, error) {
	t := p.tok()
	if t.kind == tOp {
		switch t.text {
		case ";":
			p.next()
			return nil, nil
		case "{":
			b, err := p.block()
			return []stmt{b}, err
		}
	}
	if t.kind == tIdent {
		switch t.text {
		case "if":
			s, err := p.ifStmt()
			return []stmt{s}, err
		case "while":
			s, err := p.whileStmt()
			return []stmt{s}, err
		case "do":
			s, err := p.doStmt()
			return []stmt{s}, err
		case "for":
			s, err := p.forStmt()
			return []stmt{s}, err
		case "switch":
			s, err := p.switchStmt()
			return []stmt{s}, err
		case "break", "continue":
			p.next()
			if _, err := p.expectOp(";"); err != nil {
				return nil, err
			}
			return []stmt{&branchStmt{pos: t.pos, kind: t.text}}, nil
		case "return":
			p.next()
			var v expr
			if !p.isOp(";") {
				var err error
				if v, err = p.expr(); err != nil {
					return nil, err
				}
			}
			if _, err := p.expectOp(";"); err != nil {
				return nil, err
			}
			return []stmt{&returnStmt{pos: t.pos, value: v}}, nil
		case "typedef":
			return p.typedef()
		case "local":
			p.next()
			typ, defs, err := p.typeRef()
			if err != nil {
				return nil, err
			}
			d, err := p.declRest(typ, true, t.pos)
			return append(defs, d), err
		}
		if p.atType() {
			typ, defs, err := p.typeRef()
			if err != nil {
				return nil, err
			}
			// struct X {...}; with no variables
			if p.isOp(";") {
				p.next()
				return defs, nil
			}
			name := p.tok()
			if top && name.kind == tIdent && p.peek(1).kind == tOp && p.peek(1).text == "(" && p.isFuncDef() {
				f, err := p.funcDef(typ)
				return append(defs, f), err
			}
			d, err := p.declRest(typ, false, t.pos)
			return append(defs, d), err
		}
	}
	x, err := p.expr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expectOp(";"); err != nil {
		return nil, err
	}
	return []stmt{&exprStmt{pos: t.pos, x: x}}, nil
}

// isFuncDef tells whether NAME ( ... ) is followed by a body: a function,
// not a struct read with arguments
func (p *parser) isFuncDef() bool {
	depth := 0
	for k := p.i + 1; k < len(p.toks); k++ {
		t := p.toks[k]
		if t.kind == tEOF {
			return false
		}
		if t.kind != tOp {
			continue
		}
		switch t.text {
		case "(":
			depth++
		case ")":
			depth--
			if depth == 0 {
				k++
				// Attributes may stand between the parameters and the body
				if t := p.toks[min(k, len(p.toks)-1)]; t.kind == tOp && t.text == "<" {
					for k < len(p.toks) && !(p.toks[k].kind == tOp && p.toks[k].text == ">") {
						k++
					}
					k++
				}
				next := p.toks[min(k, len(p.toks)-1)]
				return next.kind == tOp && next.text == "{"
			}
		}
	}
	return false
}

func (p *parser) block() (*blockStmt, error) {
	open, err := p.expectOp("{")
	if err != nil {
		return nil, err
	}
	b := &blockStmt{pos: open.pos}
	for !p.isOp("}") {
		if p.at(tEOF, "") {
			return nil, errorf(open.pos, "the block is not closed")
		}
		s, err := p.stmt(false)
		if err != nil {
			return nil, err
		}
		b.stmts = append(b.stmts, s...)
	}
	p.next()
	return b, nil
}

// body parses a statement as the body of if, while, for
func (p *parser) body() (stmt, error) {
	ss, err := p.stmt(false)
	if err != nil {
		return nil, err
	}
	if len(ss) == 1 {
		return ss[0], nil
	}
	return &blockStmt{pos: p.tok().pos, stmts: ss}, nil
}

func (p *parser) parenExpr() (expr, error) {
	if _, err := p.expectOp("("); err != nil {
		return nil, err
	}
	x, err := p.expr()
	if err != nil {
		return nil, err
	}
	_, err = p.expectOp(")")
	return x, err
}

func (p *parser) ifStmt() (stmt, error) {
	t := p.next()
	cond, err := p.parenExpr()
	if err != nil {
		return nil, err
	}
	s := &ifStmt{pos: t.pos, cond: cond}
	if s.then, err = p.body(); err != nil {
		return nil, err
	}
	if p.isIdent("else") {
		p.next()
		if s.els, err = p.body(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (p *parser) whileStmt() (stmt, error) {
	t := p.next()
	cond, err := p.parenExpr()
	if err != nil {
		return nil, err
	}
	b, err := p.body()
	return &whileStmt{pos: t.pos, cond: cond, body: b}, err
}

func (p *parser) doStmt() (stmt, error) {
	t := p.next()
	b, err := p.body()
	if err != nil {
		return nil, err
	}
	if !p.isIdent("while") {
		return nil, p.unexpected("\"while\"")
	}
	p.next()
	cond, err := p.parenExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expectOp(";"); err != nil {
		return nil, err
	}
	return &whileStmt{pos: t.pos, cond: cond, body: b, do: true}, nil
}

func (p *parser) forStmt() (stmt, error) {
	t := p.next()
	if _, err := p.expectOp("("); err != nil {
		return nil, err
	}
	s := &forStmt{pos: t.pos}
	if !p.isOp(";") {
		init, err := p.stmt(false) // takes the ';'
		if err != nil {
			return nil, err
		}
		if len(init) == 1 {
			s.init = init[0]
		}
	} else {
		p.next()
	}
	var err error
	if !p.isOp(";") {
		if s.cond, err = p.expr(); err != nil {
			return nil, err
		}
	}
	if _, err := p.expectOp(";"); err != nil {
		return nil, err
	}
	if !p.isOp(")") {
		if s.post, err = p.expr(); err != nil {
			return nil, err
		}
	}
	if _, err := p.expectOp(")"); err != nil {
		return nil, err
	}
	s.body, err = p.body()
	return s, err
}

func (p *parser) switchStmt() (stmt, error) {
	t := p.next()
	v, err := p.parenExpr()
	if err != nil {
		return nil, err
	}
	s := &switchStmt{pos: t.pos, value: v}
	if _, err := p.expectOp("{"); err != nil {
		return nil, err
	}
	for !p.isOp("}") {
		var c caseClause
		switch {
		case p.isIdent("case"):
			for p.isIdent("case") {
				p.next()
				x, err := p.condExpr()
				if err != nil {
					return nil, err
				}
				c.values = append(c.values, x)
				if _, err := p.expectOp(":"); err != nil {
					return nil, err
				}
			}
		case p.isIdent("default"):
			p.next()
			if _, err := p.expectOp(":"); err != nil {
				return nil, err
			}
		default:
			return nil, p.unexpected("\"case\"")
		}
		for !p.isIdent("case") && !p.isIdent("default") && !p.isOp("}") {
			if p.at(tEOF, "") {
				return nil, errorf(t.pos, "the switch is not closed")
			}
			ss, err := p.stmt(false)
			if err != nil {
				return nil, err
			}
			c.body = append(c.body, ss...)
		}
		s.cases = append(s.cases, c)
	}
	p.next()
	return s, nil
}

// typeRef parses a type; a struct or an enum defined in place comes as a
// statement to run before
func (p *parser) typeRef() (typeRef, []stmt, error) {
	for p.isIdent("const") {
		p.next()
	}
	t := p.tok()
	switch t.text {
	case "struct", "union":
		s, err := p.structDef()
		if err != nil {
			return typeRef{}, nil, err
		}
		// A forward declaration too: the type exists before its body
		defs := []stmt{s}
		p.types[s.name] = true
		return typeRef{pos: t.pos, name: s.name}, defs, nil
	case "enum":
		e, err := p.enumDef()
		if err != nil {
			return typeRef{}, nil, err
		}
		var defs []stmt
		if e.items != nil {
			defs = []stmt{e}
		}
		p.types[e.name] = true
		return typeRef{pos: t.pos, name: e.name}, defs, nil
	case "unsigned", "signed":
		p.next()
		unsigned := t.text == "unsigned"
		name := "int"
		if p.at(tIdent, "") && p.isTypeName(p.tok().text) {
			name = p.next().text
			if name == "long" && p.isIdent("long") {
				p.next()
				name = "int64"
			}
		}
		base := builtinTypes[name]
		if base == "" {
			return typeRef{}, nil, errorf(t.pos, "%s %s is not a type", t.text, name)
		}
		if unsigned && !strings.HasPrefix(base, "u") && base != "float" && base != "double" {
			if base == "char" {
				base = "uchar"
			} else {
				base = "u" + base
			}
		}
		return typeRef{pos: t.pos, name: base}, nil, nil
	}
	if t.kind != tIdent || !p.isTypeName(t.text) {
		return typeRef{}, nil, p.unexpected("a type")
	}
	p.next()
	name := t.text
	if name == "long" && p.isIdent("long") {
		p.next()
		name = "int64"
	}
	return typeRef{pos: t.pos, name: name}, nil, nil
}

func (p *parser) structDef() (*structDef, error) {
	t := p.next()
	s := &structDef{pos: t.pos, union: t.text == "union"}
	if p.at(tIdent, "") {
		s.name = p.next().text
		p.types[s.name] = true // the struct can refer to itself
	} else {
		p.anon++
		s.name = fmt.Sprintf("<struct %d>", p.anon)
	}
	if p.isOp("(") {
		ps, err := p.params()
		if err != nil {
			return nil, err
		}
		s.params = ps
	}
	if !p.isOp("{") {
		s.forward = true
		return s, nil
	}
	b, err := p.block()
	if err != nil {
		return nil, err
	}
	s.body = b.stmts
	if p.isOp("<") {
		if s.attrs, err = p.attrs(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (p *parser) params() ([]param, error) {
	if _, err := p.expectOp("("); err != nil {
		return nil, err
	}
	var ps []param
	for !p.isOp(")") {
		if p.isIdent("void") && p.peek(1).kind == tOp && p.peek(1).text == ")" {
			p.next()
			break
		}
		typ, _, err := p.typeRef()
		if err != nil {
			return nil, err
		}
		pr := param{typ: typ}
		if p.isOp("&") {
			p.next()
			pr.byRef = true
		}
		name, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		pr.name = name.text
		if p.isOp("[") {
			p.next()
			if _, err := p.expectOp("]"); err != nil {
				return nil, err
			}
			pr.isArray = true
		}
		ps = append(ps, pr)
		if !p.isOp(",") {
			break
		}
		p.next()
	}
	_, err := p.expectOp(")")
	return ps, err
}

func (p *parser) enumDef() (*enumDef, error) {
	t := p.next()
	e := &enumDef{pos: t.pos, base: typeRef{pos: t.pos, name: "int"}}
	if p.isOp("<") {
		p.next()
		base, _, err := p.typeRef()
		if err != nil {
			return nil, err
		}
		e.base = base
		if _, err := p.expectOp(">"); err != nil {
			return nil, err
		}
	}
	if p.at(tIdent, "") {
		e.name = p.next().text
	} else {
		p.anon++
		e.name = fmt.Sprintf("<enum %d>", p.anon)
	}
	if !p.isOp("{") {
		return e, nil
	}
	p.next()
	e.items = []enumItem{}
	for !p.isOp("}") {
		name, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		it := enumItem{name: name.text}
		if p.isOp("=") {
			p.next()
			if it.value, err = p.condExpr(); err != nil {
				return nil, err
			}
		}
		e.items = append(e.items, it)
		if !p.isOp(",") {
			break
		}
		p.next()
	}
	if _, err := p.expectOp("}"); err != nil {
		return nil, err
	}
	return e, nil
}

func (p *parser) typedef() ([]stmt, error) {
	t := p.next()
	typ, defs, err := p.typeRef()
	if err != nil {
		return nil, err
	}
	name, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	td := &typedefStmt{pos: t.pos, typ: typ, name: name.text}
	if p.isOp("[") {
		p.next()
		if td.length, err = p.expr(); err != nil {
			return nil, err
		}
		if _, err := p.expectOp("]"); err != nil {
			return nil, err
		}
	}
	if p.isOp("<") {
		if td.attrs, err = p.attrs(); err != nil {
			return nil, err
		}
	}
	if _, err := p.expectOp(";"); err != nil {
		return nil, err
	}
	p.types[td.name] = true
	return append(defs, td), nil
}

func (p *parser) funcDef(ret typeRef) (stmt, error) {
	name := p.next()
	ps, err := p.params()
	if err != nil {
		return nil, err
	}
	f := &funcDef{pos: name.pos, ret: ret, name: name.text, params: ps}
	if p.isOp("<") {
		if f.attrs, err = p.attrs(); err != nil {
			return nil, err
		}
	}
	b, err := p.block()
	if err != nil {
		return nil, err
	}
	f.body = b.stmts
	return f, nil
}

// declRest parses the variables of a declaration after its type
func (p *parser) declRest(typ typeRef, local bool, pos Pos) (stmt, error) {
	d := &declStmt{pos: pos, local: local, typ: typ}
	for {
		name, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		v := varDecl{pos: name.pos, name: name.text}
		if p.isOp("[") {
			p.next()
			v.isArray = true
			if !p.isOp("]") {
				if v.length, err = p.expr(); err != nil {
					return nil, err
				}
			}
			if _, err := p.expectOp("]"); err != nil {
				return nil, err
			}
		}
		if p.isOp("(") {
			p.next()
			for !p.isOp(")") {
				a, err := p.assignExpr()
				if err != nil {
					return nil, err
				}
				v.args = append(v.args, a)
				if !p.isOp(",") {
					break
				}
				p.next()
			}
			if _, err := p.expectOp(")"); err != nil {
				return nil, err
			}
		}
		// The width of a bit field and the offset after @ stop before a
		// comparison: a '<' after them starts the attributes
		if p.isOp(":") {
			p.next()
			if v.bits, err = p.binary(shiftLevel); err != nil {
				return nil, err
			}
		}
		if p.isOp("@") {
			p.next()
			if v.at, err = p.binary(shiftLevel); err != nil {
				return nil, err
			}
		}
		if p.isOp("=") {
			p.next()
			if v.init, err = p.assignExpr(); err != nil {
				return nil, err
			}
		}
		if p.isOp("<") {
			if v.attrs, err = p.attrs(); err != nil {
				return nil, err
			}
		}
		d.vars = append(d.vars, v)
		if !p.isOp(",") {
			break
		}
		p.next()
	}
	if _, err := p.expectOp(";"); err != nil {
		return nil, err
	}
	return d, nil
}

// attrs parses <name=value, ...>; a value stops before '>', so it is a
// unary expression (a number, a name, a string, -1)
func (p *parser) attrs() ([]attr, error) {
	p.next()
	var as []attr
	for !p.isOp(">") {
		name, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		a := attr{pos: name.pos, name: name.text}
		if p.isOp("=") {
			p.next()
			if a.value, err = p.unary(); err != nil {
				return nil, err
			}
			// Allow a|b and a+b in values written without brackets
			for p.isOp("|") || p.isOp("+") {
				op := p.next()
				y, err := p.unary()
				if err != nil {
					return nil, err
				}
				a.value = &binaryExpr{pos: op.pos, op: op.text, x: a.value, y: y}
			}
		}
		as = append(as, a)
		if !p.isOp(",") {
			break
		}
		p.next()
	}
	_, err := p.expectOp(">")
	return as, err
}

// ---- Expressions ----

func (p *parser) expr() (expr, error) {
	x, err := p.assignExpr()
	if err != nil {
		return nil, err
	}
	// The comma operator: the value of the last
	for p.isOp(",") {
		op := p.next()
		y, err := p.assignExpr()
		if err != nil {
			return nil, err
		}
		x = &binaryExpr{pos: op.pos, op: ",", x: x, y: y}
	}
	return x, nil
}

var assignOps = map[string]bool{"=": true, "+=": true, "-=": true, "*=": true, "/=": true, "%=": true,
	"&=": true, "|=": true, "^=": true, "<<=": true, ">>=": true}

func (p *parser) assignExpr() (expr, error) {
	x, err := p.condExpr()
	if err != nil {
		return nil, err
	}
	if t := p.tok(); t.kind == tOp && assignOps[t.text] {
		p.next()
		r, err := p.assignExpr()
		if err != nil {
			return nil, err
		}
		return &assignExpr{pos: t.pos, op: t.text, l: x, r: r}, nil
	}
	return x, nil
}

func (p *parser) condExpr() (expr, error) {
	c, err := p.binary(0)
	if err != nil {
		return nil, err
	}
	if !p.isOp("?") {
		return c, nil
	}
	t := p.next()
	yes, err := p.assignExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expectOp(":"); err != nil {
		return nil, err
	}
	no, err := p.condExpr()
	if err != nil {
		return nil, err
	}
	return &condExpr{pos: t.pos, cond: c, yes: yes, no: no}, nil
}

// precedence of the binary operators, as in C
// shiftLevel is the level of << and >> in precedence: the expressions above
// comparisons
const shiftLevel = 7

var precedence = [][]string{
	{"||"}, {"&&"}, {"|"}, {"^"}, {"&"}, {"==", "!="}, {"<", "<=", ">", ">="}, {"<<", ">>"}, {"+", "-"}, {"*", "/", "%"},
}

func (p *parser) binary(level int) (expr, error) {
	if level == len(precedence) {
		return p.unary()
	}
	x, err := p.binary(level + 1)
	if err != nil {
		return nil, err
	}
	for {
		t := p.tok()
		found := false
		if t.kind == tOp {
			for _, op := range precedence[level] {
				if t.text == op {
					found = true
				}
			}
		}
		if !found {
			return x, nil
		}
		p.next()
		y, err := p.binary(level + 1)
		if err != nil {
			return nil, err
		}
		x = &binaryExpr{pos: t.pos, op: t.text, x: x, y: y}
	}
}

func (p *parser) unary() (expr, error) {
	t := p.tok()
	if t.kind == tOp {
		switch t.text {
		case "-", "+", "!", "~", "++", "--":
			p.next()
			x, err := p.unary()
			if err != nil {
				return nil, err
			}
			return &unaryExpr{pos: t.pos, op: t.text, x: x}, nil
		case "&":
			// &field passes the field itself: it is so anyway
			p.next()
			return p.unary()
		case "(":
			// A cast: (TYPE) expr
			if n := p.peek(1); n.kind == tIdent && (p.isTypeName(n.text) || n.text == "unsigned" || n.text == "signed") {
				save := p.i
				p.next()
				typ, _, err := p.typeRef()
				if err == nil && p.isOp(")") {
					p.next()
					x, err := p.unary()
					if err != nil {
						return nil, err
					}
					return &castExpr{pos: t.pos, typ: typ, x: x}, nil
				}
				p.i = save
			}
		}
	}
	if t.kind == tIdent && t.text == "sizeof" {
		p.next()
		if _, err := p.expectOp("("); err != nil {
			return nil, err
		}
		s := &sizeofExpr{pos: t.pos}
		if p.atType() && !(p.peek(1).kind == tOp && (p.peek(1).text == "." || p.peek(1).text == "[")) {
			typ, _, err := p.typeRef()
			if err != nil {
				return nil, err
			}
			s.typ = &typ
		} else {
			x, err := p.expr()
			if err != nil {
				return nil, err
			}
			s.x = x
		}
		_, err := p.expectOp(")")
		return s, err
	}
	return p.postfix()
}

func (p *parser) postfix() (expr, error) {
	x, err := p.primary()
	if err != nil {
		return nil, err
	}
	for {
		t := p.tok()
		if t.kind != tOp {
			return x, nil
		}
		switch t.text {
		case "[":
			p.next()
			i, err := p.expr()
			if err != nil {
				return nil, err
			}
			if _, err := p.expectOp("]"); err != nil {
				return nil, err
			}
			x = &indexExpr{pos: t.pos, x: x, i: i}
		case ".", "->":
			p.next()
			name, err := p.expectIdent()
			if err != nil {
				return nil, err
			}
			x = &memberExpr{pos: t.pos, x: x, name: name.text}
		case "++", "--":
			p.next()
			x = &postfixExpr{pos: t.pos, op: t.text, x: x}
		default:
			return x, nil
		}
	}
}

func (p *parser) primary() (expr, error) {
	t := p.tok()
	switch t.kind {
	case tInt, tChar:
		p.next()
		v := Value{k: kInt, i: int64(t.ival)}
		if t.ival > 1<<63-1 {
			v.unsigned = true
		}
		return &litExpr{pos: t.pos, v: v}, nil
	case tFloat:
		p.next()
		return &litExpr{pos: t.pos, v: Value{k: kFloat, f: t.fval}}, nil
	case tString:
		p.next()
		s := t.text
		// Adjacent strings are joined, as in C
		for p.at(tString, "") {
			s += p.next().text
		}
		return &litExpr{pos: t.pos, v: Value{k: kString, s: s}}, nil
	case tIdent:
		p.next()
		switch t.text {
		case "true":
			return &litExpr{pos: t.pos, v: Value{k: kInt, i: 1}}, nil
		case "false":
			return &litExpr{pos: t.pos, v: Value{k: kInt, i: 0}}, nil
		}
		if p.isOp("(") {
			p.next()
			c := &callExpr{pos: t.pos, fn: t.text}
			for !p.isOp(")") {
				a, err := p.assignExpr()
				if err != nil {
					return nil, err
				}
				c.args = append(c.args, a)
				if !p.isOp(",") {
					break
				}
				p.next()
			}
			_, err := p.expectOp(")")
			return c, err
		}
		return &identExpr{pos: t.pos, name: t.text}, nil
	case tOp:
		if t.text == "(" {
			p.next()
			x, err := p.expr()
			if err != nil {
				return nil, err
			}
			_, err = p.expectOp(")")
			return x, err
		}
	}
	return nil, p.unexpected("a value")
}
