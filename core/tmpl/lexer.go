// Package tmpl is the language of templates: programs that describe the
// structure of binary data and read it into a tree of fields (ANL-2), do
// calculations over the data and write their results (ANL-12), and serve
// the expression console (ANL-13). The language is described in
// docs/templates.md.
//
// A template is parsed into a syntax tree and compiled into Go closures,
// which the runtime calls; the closures are made once per template.
package tmpl

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

type tokKind int

const (
	tEOF tokKind = iota
	tIdent
	tInt
	tFloat
	tString
	tChar
	tOp // operators and punctuation
)

type token struct {
	kind tokKind
	text string // the identifier, the operator, the decoded string
	ival uint64
	fval float64
	pos  Pos
}

// Pos is a place in the source of a template
type Pos struct {
	Line, Col int
}

func (p Pos) String() string { return fmt.Sprintf("%d:%d", p.Line, p.Col) }

// Error is an error of a template with its place
type Error struct {
	Pos  Pos
	Msg  string
	Path string // the field being read, for errors at run time
}

func (e *Error) Error() string {
	if e.Path != "" {
		return fmt.Sprintf("line %s: %s (%s)", e.Pos, e.Msg, e.Path)
	}
	return fmt.Sprintf("line %s: %s", e.Pos, e.Msg)
}

func errorf(pos Pos, format string, args ...any) *Error {
	return &Error{Pos: pos, Msg: fmt.Sprintf(format, args...)}
}

// operators, the longest first so that the lexer takes the longest match
var operators = []string{
	"<<=", ">>=", "...",
	"<<", ">>", "<=", ">=", "==", "!=", "&&", "||", "++", "--",
	"+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=", "->", "::",
	"+", "-", "*", "/", "%", "&", "|", "^", "~", "!", "<", ">", "=",
	"?", ":", ";", ",", ".", "(", ")", "[", "]", "{", "}", "@",
}

type lexer struct {
	src  string
	i    int
	line int
	col  int
}

func lex(src string) ([]token, error) {
	l := &lexer{src: src, line: 1, col: 1}
	var toks []token
	for {
		t, err := l.next()
		if err != nil {
			return nil, err
		}
		toks = append(toks, t)
		if t.kind == tEOF {
			return toks, nil
		}
	}
}

func (l *lexer) pos() Pos { return Pos{l.line, l.col} }

func (l *lexer) advance(n int) {
	for k := 0; k < n && l.i < len(l.src); k++ {
		if l.src[l.i] == '\n' {
			l.line++
			l.col = 1
		} else {
			l.col++
		}
		l.i++
	}
}

func (l *lexer) skipSpace() error {
	for l.i < len(l.src) {
		c := l.src[l.i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			l.advance(1)
		case strings.HasPrefix(l.src[l.i:], "//"):
			for l.i < len(l.src) && l.src[l.i] != '\n' {
				l.advance(1)
			}
		case strings.HasPrefix(l.src[l.i:], "/*"):
			start := l.pos()
			end := strings.Index(l.src[l.i+2:], "*/")
			if end < 0 {
				return errorf(start, "the comment is not closed")
			}
			l.advance(end + 4)
		case c == '#':
			// Preprocessor lines of 010 templates (#include, #define) are not supported: skipped
			for l.i < len(l.src) && l.src[l.i] != '\n' {
				l.advance(1)
			}
		default:
			return nil
		}
	}
	return nil
}

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func (l *lexer) next() (token, error) {
	if err := l.skipSpace(); err != nil {
		return token{}, err
	}
	p := l.pos()
	if l.i >= len(l.src) {
		return token{kind: tEOF, pos: p}, nil
	}
	c := l.src[l.i]
	switch {
	case isIdentStart(c):
		j := l.i
		for j < len(l.src) && (isIdentStart(l.src[j]) || isDigit(l.src[j])) {
			j++
		}
		t := token{kind: tIdent, text: l.src[l.i:j], pos: p}
		l.advance(j - l.i)
		return t, nil
	case isDigit(c) || (c == '.' && l.i+1 < len(l.src) && isDigit(l.src[l.i+1])):
		return l.number(p)
	case c == '"':
		s, err := l.quoted('"')
		return token{kind: tString, text: s, pos: p}, err
	case c == '\'':
		s, err := l.quoted('\'')
		if err != nil {
			return token{}, err
		}
		switch {
		case len(s) == 1:
			return token{kind: tChar, ival: uint64(s[0]), pos: p}, nil
		case utf8.RuneCountInString(s) == 1:
			r, _ := utf8.DecodeRuneInString(s)
			return token{kind: tChar, ival: uint64(r), pos: p}, nil
		case len(s) >= 2 && len(s) <= 8:
			// Multi-character constants like 'IHDR' are big-endian numbers, as in C
			var v uint64
			for i := 0; i < len(s); i++ {
				v = v<<8 | uint64(s[i])
			}
			return token{kind: tChar, ival: v, pos: p}, nil
		}
		return token{}, errorf(p, "a character constant of one character expected")
	}
	for _, op := range operators {
		if strings.HasPrefix(l.src[l.i:], op) {
			l.advance(len(op))
			return token{kind: tOp, text: op, pos: p}, nil
		}
	}
	return token{}, errorf(p, "unexpected character %q", c)
}

func (l *lexer) number(p Pos) (token, error) {
	j := l.i
	s := l.src
	isFloat := false
	if strings.HasPrefix(s[j:], "0x") || strings.HasPrefix(s[j:], "0X") || strings.HasPrefix(s[j:], "0b") || strings.HasPrefix(s[j:], "0B") {
		j += 2
		for j < len(s) && (isDigit(s[j]) || (s[j] >= 'a' && s[j] <= 'f') || (s[j] >= 'A' && s[j] <= 'F') || s[j] == '_') {
			j++
		}
	} else {
		for j < len(s) && (isDigit(s[j]) || s[j] == '.' || s[j] == '_' ||
			s[j] == 'e' || s[j] == 'E' || ((s[j] == '+' || s[j] == '-') && (s[j-1] == 'e' || s[j-1] == 'E'))) {
			if s[j] == '.' || s[j] == 'e' || s[j] == 'E' {
				isFloat = true
			}
			j++
		}
	}
	text := strings.ReplaceAll(s[l.i:j], "_", "")
	// Suffixes: u, l, f, h (hex like 010: 1Ah is not supported, the C forms are)
	for j < len(s) && strings.ContainsRune("uUlLfF", rune(s[j])) {
		if s[j] == 'f' || s[j] == 'F' {
			isFloat = true
		}
		j++
	}
	raw := s[l.i:j]
	l.advance(j - l.i)
	if isFloat {
		f, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return token{}, errorf(p, "bad number %q", raw)
		}
		return token{kind: tFloat, fval: f, pos: p}, nil
	}
	base := 10
	switch {
	case strings.HasPrefix(text, "0x") || strings.HasPrefix(text, "0X"):
		text, base = text[2:], 16
	case strings.HasPrefix(text, "0b") || strings.HasPrefix(text, "0B"):
		text, base = text[2:], 2
	case len(text) > 1 && text[0] == '0':
		text, base = text[1:], 8
	}
	v, err := strconv.ParseUint(text, base, 64)
	if err != nil {
		return token{}, errorf(p, "bad number %q", raw)
	}
	return token{kind: tInt, ival: v, pos: p}, nil
}

// quoted reads a string or a character constant with C escapes
func (l *lexer) quoted(q byte) (string, error) {
	start := l.pos()
	l.advance(1)
	var sb strings.Builder
	for {
		if l.i >= len(l.src) || l.src[l.i] == '\n' {
			return "", errorf(start, "the string is not closed")
		}
		c := l.src[l.i]
		if c == q {
			l.advance(1)
			return sb.String(), nil
		}
		if c != '\\' {
			sb.WriteByte(c)
			l.advance(1)
			continue
		}
		if l.i+1 >= len(l.src) {
			return "", errorf(start, "the string is not closed")
		}
		e := l.src[l.i+1]
		l.advance(2)
		switch e {
		case 'n':
			sb.WriteByte('\n')
		case 'r':
			sb.WriteByte('\r')
		case 't':
			sb.WriteByte('\t')
		case '0':
			sb.WriteByte(0)
		case 'a':
			sb.WriteByte(7)
		case 'b':
			sb.WriteByte(8)
		case 'f':
			sb.WriteByte(12)
		case 'v':
			sb.WriteByte(11)
		case 'x':
			j := l.i
			for j < len(l.src) && j < l.i+2 && strings.ContainsRune("0123456789abcdefABCDEF", rune(l.src[j])) {
				j++
			}
			if j == l.i {
				return "", errorf(l.pos(), "\\x without hex digits")
			}
			v, _ := strconv.ParseUint(l.src[l.i:j], 16, 8)
			sb.WriteByte(byte(v))
			l.advance(j - l.i)
		default:
			sb.WriteByte(e)
		}
	}
}
