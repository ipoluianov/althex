// Package expr computes the integer expressions of the "go to" box (NAV-1):
// "0x400 + 3*512", "cursor + 0x10", and offsets relative to the cursor:
// "+0x10", "-20".
//
// The operators and their precedence are those of C, from the lowest:
// | then ^ then & then << >> then + - then * / % then the unary - + ~.
// Numbers are decimal, 0x hex, 0o octal, 0b binary, hex with an h suffix
// (1A2Bh, it must start with a digit) or a $ prefix ($1A2B); '_' may
// separate digits. A leading 0 is decimal: octal is only 0o.
package expr

import (
	"fmt"
	"math"
	"math/bits"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Error is an error at a place in the expression
type Error struct {
	Pos int // byte offset in the expression
	Msg string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s at position %d", e.Msg, e.Pos+1)
}

// Eval computes the expression. Names are looked up in vars, ignoring case.
// Overflow of int64 and division by zero are errors.
func Eval(s string, vars map[string]int64) (int64, error) {
	p := &parser{s: s, vars: vars}
	p.next()
	v, err := p.or()
	if err != nil {
		return 0, err
	}
	if p.tok != tokEnd {
		return 0, p.unexpected()
	}
	return v, nil
}

// Offset reads the "go to" box: an expression, or with a leading '+' or
// '-' an offset relative to base ("+0x10" is base+16). The sign stays a part
// of the expression, so "-0x10+4" is base-12. Offsets below 0 are an error.
func Offset(s string, base int64, vars map[string]int64) (int64, error) {
	t := strings.TrimSpace(s)
	v, err := Eval(s, vars)
	if err != nil {
		return 0, err
	}
	if strings.HasPrefix(t, "+") || strings.HasPrefix(t, "-") {
		r, ok := add(base, v)
		if !ok {
			return 0, &Error{0, "overflow"}
		}
		v = r
	}
	if v < 0 {
		return 0, fmt.Errorf("offset %d is below 0", v)
	}
	return v, nil
}

type tokKind int

const (
	tokEnd tokKind = iota
	tokNum
	tokName
	tokOp
)

type parser struct {
	s    string
	vars map[string]int64

	pos  int // after the token
	tok  tokKind
	at   int    // where the token starts
	text string // the token as written
	num  uint64 // a number; may be 1<<63, which only a unary minus makes fit
	err  *Error // a bad token

	depth int // of the parentheses and the unary operators
}

// maxDepth limits the recursion of the parser: ((((((... would overflow the stack
const maxDepth = 1000

// next reads the next token
func (p *parser) next() {
	for p.pos < len(p.s) && isSpace(p.s[p.pos]) {
		p.pos++
	}
	p.at = p.pos
	if p.pos >= len(p.s) {
		p.tok, p.text = tokEnd, ""
		return
	}
	c := p.s[p.pos]
	switch {
	case isDigit(c) || c == '$':
		p.pos++
		for p.pos < len(p.s) && isWord(p.s[p.pos]) {
			p.pos++
		}
		p.tok, p.text = tokNum, p.s[p.at:p.pos]
		p.num, p.err = parseNum(p.text, p.at)
	case isLetter(c):
		for p.pos < len(p.s) && isWord(p.s[p.pos]) {
			p.pos++
		}
		p.tok, p.text = tokName, p.s[p.at:p.pos]
	default:
		_, n := utf8.DecodeRuneInString(p.s[p.pos:])
		if op := p.s[p.pos:min(p.pos+2, len(p.s))]; op == "<<" || op == ">>" {
			n = 2
		}
		p.pos += n
		p.tok, p.text = tokOp, p.s[p.at:p.pos]
	}
}

func (p *parser) unexpected() *Error {
	if p.tok == tokEnd {
		return &Error{p.at, "unexpected end"}
	}
	return &Error{p.at, fmt.Sprintf("unexpected %q", p.text)}
}

func (p *parser) isOp(ops ...string) bool {
	if p.tok != tokOp {
		return false
	}
	for _, op := range ops {
		if p.text == op {
			return true
		}
	}
	return false
}

// binary parses a level of left-associative operators
func (p *parser) binary(sub func() (int64, error), ops ...string) (int64, error) {
	a, err := sub()
	if err != nil {
		return 0, err
	}
	for p.isOp(ops...) {
		op, at := p.text, p.at
		p.next()
		b, err := sub()
		if err != nil {
			return 0, err
		}
		if a, err = apply(op, a, b, at); err != nil {
			return 0, err
		}
	}
	return a, nil
}

func (p *parser) or() (int64, error)    { return p.binary(p.xor, "|") }
func (p *parser) xor() (int64, error)   { return p.binary(p.and, "^") }
func (p *parser) and() (int64, error)   { return p.binary(p.shift, "&") }
func (p *parser) shift() (int64, error) { return p.binary(p.sum, "<<", ">>") }
func (p *parser) sum() (int64, error)   { return p.binary(p.prod, "+", "-") }
func (p *parser) prod() (int64, error)  { return p.binary(p.unary, "*", "/", "%") }

func (p *parser) unary() (int64, error) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxDepth {
		return 0, &Error{p.at, "nested too deep"}
	}
	if !p.isOp("-", "+", "~") {
		return p.primary()
	}
	op, at := p.text, p.at
	p.next()
	// -9223372036854775808 is the one number that fits only negated
	if op == "-" && p.tok == tokNum && p.err == nil && p.num == 1<<63 {
		p.next()
		return math.MinInt64, nil
	}
	v, err := p.unary()
	if err != nil {
		return 0, err
	}
	switch op {
	case "-":
		if v == math.MinInt64 {
			return 0, &Error{at, "overflow"}
		}
		return -v, nil
	case "~":
		return ^v, nil
	}
	return v, nil
}

func (p *parser) primary() (int64, error) {
	switch p.tok {
	case tokNum:
		if p.err != nil {
			return 0, p.err
		}
		if p.num > math.MaxInt64 {
			return 0, &Error{p.at, "number too big"}
		}
		v := int64(p.num)
		p.next()
		return v, nil
	case tokName:
		for k, v := range p.vars {
			if strings.EqualFold(k, p.text) {
				p.next()
				return v, nil
			}
		}
		return 0, &Error{p.at, fmt.Sprintf("unknown name %q", p.text)}
	case tokOp:
		if p.text == "(" {
			at := p.at
			p.next()
			v, err := p.or()
			if err != nil {
				return 0, err
			}
			if !p.isOp(")") {
				if p.tok == tokEnd {
					return 0, &Error{at, "unclosed '('"}
				}
				return 0, p.unexpected()
			}
			p.next()
			return v, nil
		}
	}
	return 0, p.unexpected()
}

// parseNum reads a number token; at is where it starts, for errors
func parseNum(t string, at int) (uint64, *Error) {
	bad := &Error{at, fmt.Sprintf("bad number %q", t)}
	s := strings.ReplaceAll(t, "_", "")
	base := 10
	switch {
	case s[0] == '$':
		s, base = s[1:], 16
	case len(s) > 1 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X'):
		s, base = s[2:], 16
	case strings.HasSuffix(s, "h") || strings.HasSuffix(s, "H"):
		s, base = s[:len(s)-1], 16
	case len(s) > 1 && s[0] == '0' && (s[1] == 'b' || s[1] == 'B'):
		s, base = s[2:], 2
	case len(s) > 1 && s[0] == '0' && (s[1] == 'o' || s[1] == 'O'):
		s, base = s[2:], 8
	}
	if s == "" {
		return 0, bad
	}
	v, err := strconv.ParseUint(s, base, 64)
	if err != nil {
		if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
			return 0, &Error{at, "number too big"}
		}
		return 0, bad
	}
	return v, nil
}

// apply computes a binary operator, checking for overflow
func apply(op string, a, b int64, at int) (int64, error) {
	overflow := &Error{at, "overflow"}
	switch op {
	case "|":
		return a | b, nil
	case "^":
		return a ^ b, nil
	case "&":
		return a & b, nil
	case "+":
		if r, ok := add(a, b); ok {
			return r, nil
		}
		return 0, overflow
	case "-":
		r := a - b
		if (b < 0 && r < a) || (b > 0 && r > a) {
			return 0, overflow
		}
		return r, nil
	case "*":
		if a == 0 || b == 0 {
			return 0, nil
		}
		r := a * b
		if r/b != a || (a == -1 && b == math.MinInt64) || (b == -1 && a == math.MinInt64) {
			return 0, overflow
		}
		return r, nil
	case "/", "%":
		if b == 0 {
			return 0, &Error{at, "division by zero"}
		}
		if op == "%" {
			return a % b, nil
		}
		if a == math.MinInt64 && b == -1 {
			return 0, overflow
		}
		return a / b, nil
	case "<<", ">>":
		if b < 0 {
			return 0, &Error{at, "negative shift"}
		}
		if op == ">>" {
			return a >> min(b, 63), nil
		}
		if a == 0 {
			return 0, nil
		}
		// The bits shifted out and the sign must all be copies of the sign
		lead := bits.LeadingZeros64(uint64(a))
		if a < 0 {
			lead = bits.LeadingZeros64(^uint64(a))
		}
		if b >= int64(lead) {
			return 0, overflow
		}
		return a << b, nil
	}
	return 0, &Error{at, fmt.Sprintf("unknown operator %q", op)}
}

func add(a, b int64) (int64, bool) {
	r := a + b
	if (b > 0 && r < a) || (b < 0 && r > a) {
		return 0, false
	}
	return r, true
}

func isSpace(c byte) bool  { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }
func isDigit(c byte) bool  { return c >= '0' && c <= '9' }
func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' }
func isWord(c byte) bool   { return isDigit(c) || isLetter(c) }
