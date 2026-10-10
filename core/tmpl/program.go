package tmpl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Data is what a template reads: a document
type Data interface {
	io.ReaderAt
	Size() int64
}

// Writer changes the document for a template that writes (ANL-12)
type Writer interface {
	Overwrite(off int64, b []byte) error
	Insert(off int64, b []byte) error
	Delete(off, n int64) error
}

// Env is what a run of a template works with
type Env struct {
	Data Data
	// Writer is nil when the template may not write
	Writer Writer
	// The selection and the caret, for GetSelStart() and the like
	SelStart, SelSize, Cursor int64
	// Start is where the template starts reading (0 - the start)
	Start int64
	// Limits; 0 - the defaults
	MaxSteps int64
	MaxNodes int
}

// Program is a compiled template
type Program struct {
	Name   string
	meta   Meta
	body   []cstmt
	types  map[string]*Type
	funcs  map[string]*funcInfo
	consts map[string]Value
}

// Meta is what the //! lines of a template tell
type Meta struct {
	Name       string
	Extensions []string
	Magic      []Magic
	Priority   int
}

// Magic is bytes at an offset that tell the format; Off < 0 counts from the end
type Magic struct {
	Off     int64
	Pattern []byte
	Mask    []byte // 0xFF - the byte must match, 0 - any
}

// Func is a function of a template offered in the menu: a transform or a checksum
type Func struct {
	Name  string
	Title string
	Kind  string // "transform" or "checksum"
}

// Result is what a run gives
type Result struct {
	Root     *Node
	Output   []string // what Printf printed
	Warnings []string // what Warning said (in Output too)
	Err      error    // what stopped the run; the fields read before it stay
	Wrote    bool     // the template changed the document
	Duration time.Duration
	Nodes    int

	data Data
}

// Compile parses and compiles a template
func Compile(name, src string) (*Program, error) {
	ast, err := parse(src)
	if err != nil {
		return nil, err
	}
	c := newCompiler()
	body, err := c.stmts(ast)
	if err != nil {
		return nil, err
	}
	p := &Program{Name: name, body: body, types: c.types, funcs: c.funcs, consts: c.consts}
	p.meta = parseMeta(src)
	if p.meta.Name == "" {
		p.meta.Name = name
	}
	return p, nil
}

// Meta returns what the template tells about itself
func (p *Program) Meta() Meta { return p.meta }

// Functions returns the transforms and checksums of the template
func (p *Program) Functions() []Func {
	var out []Func
	for _, f := range p.funcs {
		if f.kind != "" {
			out = append(out, Func{Name: f.name, Title: f.title, Kind: f.kind})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Title < out[j].Title })
	return out
}

func (p *Program) newInterp(ctx context.Context, env *Env, res *Result) *interp {
	in := &interp{ctx: ctx, prog: p, env: env, res: res, maxSteps: env.MaxSteps, maxNodes: env.MaxNodes}
	if in.maxSteps <= 0 {
		in.maxSteps = defaultMaxSteps
	}
	if in.maxNodes <= 0 {
		in.maxNodes = defaultMaxNodes
	}
	in.rd = newReader(env.Data)
	in.pos = env.Start
	return in
}

// run runs f, turning the panics of the runtime into errors
func (in *interp) run(f func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			switch e := r.(type) {
			case runError:
				err = e.err
			case stopRun:
				err = nil
			default:
				panic(r)
			}
		}
	}()
	f()
	return nil
}

// Run runs the template over the data
func (p *Program) Run(ctx context.Context, env Env) *Result {
	start := time.Now()
	res := &Result{data: env.Data}
	root := &Node{Name: p.meta.Name, Kind: NodeRoot, Off: 0, Size: env.Data.Size(), res: res}
	res.Root = root
	in := p.newInterp(ctx, &env, res)
	in.global = &scope{node: root}
	in.sc = in.global
	res.Err = in.run(func() { runAll(in, p.body) })
	in.finishDisplay()
	res.Nodes = in.nodes
	res.Duration = time.Since(start)
	return res
}

// finishDisplay runs the read= and comment= functions of the fields, now
// that all of them are read
func (in *interp) finishDisplay() {
	for _, pd := range in.pending {
		pd := pd
		in.sc = in.global
		// The byte order the field was read in, for the Read functions
		in.big = pd.node.big
		err := in.run(func() {
			arg := []Value{{k: kNode, n: pd.node}}
			if pd.read != nil {
				pd.node.display = textOf(in.callFunc(pd.read.pos, pd.read, arg))
			}
			if pd.comment != nil {
				pd.node.Comment = textOf(in.callFunc(pd.comment.pos, pd.comment, arg))
			}
		})
		if err != nil {
			pd.node.Comment = err.Error()
		}
	}
	in.pending = nil
}

// Call runs a function of the template (a transform or a checksum) with the
// arguments; last is the result of a run whose fields the function sees
func (p *Program) Call(ctx context.Context, env Env, last *Result, name string, args ...Value) (Value, []string, error) {
	f := p.funcs[name]
	if f == nil {
		return Value{}, nil, fmt.Errorf("no function %s", name)
	}
	res := &Result{data: env.Data}
	in := p.newInterp(ctx, &env, res)
	root := &Node{Kind: NodeRoot, res: res}
	if last != nil {
		root = last.Root
	}
	in.global = &scope{node: root}
	in.sc = in.global
	in.console = true
	var v Value
	err := in.run(func() { v = in.callFunc(f.pos, f, args) })
	return v, res.Output, err
}

// Console keeps the local variables of the expression console between its lines
type Console struct {
	vars map[string]*local
}

func NewConsole() *Console { return &Console{vars: make(map[string]*local)} }

// Eval runs a line of the console (ANL-13): an expression, whose value is
// returned as text, or statements. prog gives the types and functions (nil -
// none), last the fields (nil - none).
func (cn *Console) Eval(ctx context.Context, env Env, prog *Program, last *Result, src string) (string, []string, error) {
	ast, err := parseConsole(src, prog)
	if err != nil {
		return "", nil, err
	}
	c := newCompiler()
	p := &Program{Name: "console", types: c.types, funcs: c.funcs, consts: c.consts}
	if prog != nil {
		for k, v := range prog.types {
			c.types[k] = v
		}
		for k, v := range prog.funcs {
			c.funcs[k] = v
		}
		for k, v := range prog.consts {
			c.consts[k] = v
		}
	}
	// A single expression: its value is the answer
	var value cexpr
	if len(ast) == 1 {
		if es, ok := ast[0].(*exprStmt); ok {
			if value, err = c.expr(es.x); err != nil {
				return "", nil, err
			}
		}
	}
	var body []cstmt
	if value == nil {
		if body, err = c.stmts(ast); err != nil {
			return "", nil, err
		}
	}
	res := &Result{data: env.Data}
	if env.MaxSteps == 0 {
		env.MaxSteps = 50_000_000
	}
	in := p.newInterp(ctx, &env, res)
	root := &Node{Kind: NodeRoot, res: res}
	if last != nil {
		root = last.Root
	}
	in.global = &scope{node: root, vars: cn.vars}
	in.sc = in.global
	in.console = true
	var answer string
	err = in.run(func() {
		if value != nil {
			v := value(in)
			if !v.IsVoid() {
				answer = v.Text()
			}
			return
		}
		runAll(in, body)
	})
	return answer, res.Output, err
}

// parseConsole parses a line of the console; the names of the types of the
// template are types there too
func parseConsole(src string, prog *Program) ([]stmt, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	pr := &parser{toks: toks, types: make(map[string]bool)}
	if prog != nil {
		for name := range prog.types {
			pr.types[name] = true
		}
	}
	var out []stmt
	for !pr.at(tEOF, "") {
		// An expression without the closing ';' is allowed
		save := pr.i
		ss, err := pr.stmt(false)
		if err != nil {
			pr.i = save
			x, err2 := pr.expr()
			if err2 != nil || !pr.at(tEOF, "") {
				return nil, err
			}
			out = append(out, &exprStmt{pos: x.exprPos(), x: x})
			break
		}
		out = append(out, ss...)
	}
	return out, nil
}

// parseMeta reads the //! lines at the top of a template
func parseMeta(src string) Meta {
	var m Meta
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "//!") {
			continue
		}
		key, value, ok := strings.Cut(strings.TrimSpace(line[3:]), ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(strings.ToLower(key)) {
		case "name":
			m.Name = value
		case "extensions", "extension":
			for _, e := range strings.Split(value, ",") {
				if e = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(e), ".")); e != "" {
					m.Extensions = append(m.Extensions, e)
				}
			}
		case "priority":
			m.Priority, _ = strconv.Atoi(value)
		case "magic":
			if mg, err := parseMagic(value); err == nil {
				m.Magic = append(m.Magic, mg)
			}
		}
	}
	return m
}

// parseMagic reads `OFFSET "HEX"`
func parseMagic(s string) (Magic, error) {
	offText, rest, ok := strings.Cut(s, " ")
	if !ok {
		return Magic{}, errors.New("OFFSET \"HEX\" expected")
	}
	off, err := strconv.ParseInt(strings.TrimSpace(offText), 0, 64)
	if err != nil {
		return Magic{}, err
	}
	hexText := strings.Trim(strings.TrimSpace(rest), "\"")
	m := Magic{Off: off}
	for _, f := range strings.Fields(hexText) {
		if f == "??" {
			m.Pattern = append(m.Pattern, 0)
			m.Mask = append(m.Mask, 0)
			continue
		}
		v, err := strconv.ParseUint(f, 16, 8)
		if err != nil {
			return Magic{}, err
		}
		m.Pattern = append(m.Pattern, byte(v))
		m.Mask = append(m.Mask, 0xFF)
	}
	if len(m.Pattern) == 0 {
		return Magic{}, errors.New("no bytes")
	}
	return m, nil
}

// Matches tells whether the data has the magic of the template
func (m Meta) Matches(d Data) bool {
	size := d.Size()
	for _, mg := range m.Magic {
		off := mg.Off
		if off < 0 {
			off += size
		}
		if off < 0 || off+int64(len(mg.Pattern)) > size {
			continue
		}
		b := make([]byte, len(mg.Pattern))
		if n, _ := d.ReadAt(b, off); n < len(b) {
			continue
		}
		ok := true
		for i := range b {
			if b[i]&mg.Mask[i] != mg.Pattern[i]&mg.Mask[i] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}
