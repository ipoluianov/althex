package tmpl

// The syntax tree of a template

type stmt interface{ stmtPos() Pos }
type expr interface{ exprPos() Pos }

// typeRef is a type as written: a name, maybe an anonymous struct or enum
type typeRef struct {
	pos  Pos
	name string
}

type attr struct {
	pos   Pos
	name  string
	value expr // nil for a bare name
}

// varDecl is one variable of a declaration: `name[len] : bits @ at = init <attrs>`
type varDecl struct {
	pos     Pos
	name    string
	isArray bool
	length  expr // nil: an array of unknown length (char s[] as a local, a parameter)
	args    []expr
	bits    expr
	at      expr
	init    expr
	attrs   []attr
}

type declStmt struct {
	pos   Pos
	local bool
	typ   typeRef
	vars  []varDecl
}

type typedefStmt struct {
	pos    Pos
	typ    typeRef
	name   string
	length expr // a typedef of an array
	attrs  []attr
}

type param struct {
	typ     typeRef
	name    string
	byRef   bool
	isArray bool
}

type structDef struct {
	pos     Pos
	name    string
	union   bool
	params  []param
	body    []stmt
	attrs   []attr
	forward bool // struct X; - declared, defined later
}

type enumItem struct {
	name  string
	value expr // nil: the previous one + 1
}

type enumDef struct {
	pos   Pos
	name  string
	base  typeRef
	items []enumItem
}

type funcDef struct {
	pos    Pos
	ret    typeRef
	name   string
	params []param
	body   []stmt
	attrs  []attr
}

type ifStmt struct {
	pos       Pos
	cond      expr
	then, els stmt
}

type whileStmt struct {
	pos  Pos
	cond expr
	body stmt
	do   bool
}

type forStmt struct {
	pos  Pos
	init stmt
	cond expr
	post expr
	body stmt
}

type caseClause struct {
	values []expr // nil: default
	body   []stmt
}

type switchStmt struct {
	pos   Pos
	value expr
	cases []caseClause
}

type branchStmt struct {
	pos  Pos
	kind string // break, continue
}

type returnStmt struct {
	pos   Pos
	value expr
}

type blockStmt struct {
	pos   Pos
	stmts []stmt
}

type exprStmt struct {
	pos Pos
	x   expr
}

func (s *declStmt) stmtPos() Pos    { return s.pos }
func (s *typedefStmt) stmtPos() Pos { return s.pos }
func (s *structDef) stmtPos() Pos   { return s.pos }
func (s *enumDef) stmtPos() Pos     { return s.pos }
func (s *funcDef) stmtPos() Pos     { return s.pos }
func (s *ifStmt) stmtPos() Pos      { return s.pos }
func (s *whileStmt) stmtPos() Pos   { return s.pos }
func (s *forStmt) stmtPos() Pos     { return s.pos }
func (s *switchStmt) stmtPos() Pos  { return s.pos }
func (s *branchStmt) stmtPos() Pos  { return s.pos }
func (s *returnStmt) stmtPos() Pos  { return s.pos }
func (s *blockStmt) stmtPos() Pos   { return s.pos }
func (s *exprStmt) stmtPos() Pos    { return s.pos }

type litExpr struct {
	pos Pos
	v   Value
}

type identExpr struct {
	pos  Pos
	name string
}

type unaryExpr struct {
	pos Pos
	op  string
	x   expr
}

type postfixExpr struct {
	pos Pos
	op  string
	x   expr
}

type binaryExpr struct {
	pos  Pos
	op   string
	x, y expr
}

type assignExpr struct {
	pos  Pos
	op   string // =, +=...
	l, r expr
}

type condExpr struct {
	pos           Pos
	cond, yes, no expr
}

type callExpr struct {
	pos  Pos
	fn   string
	args []expr
}

type indexExpr struct {
	pos  Pos
	x, i expr
}

type memberExpr struct {
	pos  Pos
	x    expr
	name string
}

type castExpr struct {
	pos Pos
	typ typeRef
	x   expr
}

// sizeofExpr is sizeof(TYPE) or sizeof(expr)
type sizeofExpr struct {
	pos Pos
	typ *typeRef
	x   expr
}

func (e *litExpr) exprPos() Pos     { return e.pos }
func (e *identExpr) exprPos() Pos   { return e.pos }
func (e *unaryExpr) exprPos() Pos   { return e.pos }
func (e *postfixExpr) exprPos() Pos { return e.pos }
func (e *binaryExpr) exprPos() Pos  { return e.pos }
func (e *assignExpr) exprPos() Pos  { return e.pos }
func (e *condExpr) exprPos() Pos    { return e.pos }
func (e *callExpr) exprPos() Pos    { return e.pos }
func (e *indexExpr) exprPos() Pos   { return e.pos }
func (e *memberExpr) exprPos() Pos  { return e.pos }
func (e *castExpr) exprPos() Pos    { return e.pos }
func (e *sizeofExpr) exprPos() Pos  { return e.pos }
