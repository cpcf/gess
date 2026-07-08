package gesssexp

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	gessrules "github.com/cpcf/gess/rules"
)

type SourceSpan = gessrules.SourceSpan

type FileError struct {
	Span   SourceSpan
	Reason string
	Err    error
}

func (e *FileError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Span.String(), e.Reason, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Span.String(), e.Reason)
}

func (e *FileError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type Expr struct {
	Atom   string
	List   []Expr
	String bool
	Span   SourceSpan
}

func (e Expr) IsAtom() bool {
	return e.List == nil
}

func (e Expr) Text() string {
	return e.Atom
}

func (e Expr) Head() string {
	if len(e.List) == 0 || !e.List[0].IsAtom() {
		return ""
	}
	return e.List[0].Atom
}

type tokenKind uint8

const (
	tokenEOF tokenKind = iota
	tokenLParen
	tokenRParen
	tokenAtom
	tokenString
)

type token struct {
	kind tokenKind
	text string
	span SourceSpan
}

type lexer struct {
	name   string
	input  []rune
	offset int
	line   int
	col    int
}

func newLexer(name string, source []byte) *lexer {
	return &lexer{name: name, input: []rune(string(source)), line: 1, col: 1}
}

func (l *lexer) next() (token, error) {
	for {
		if l.offset >= len(l.input) {
			return token{kind: tokenEOF, span: l.span()}, nil
		}
		r := l.input[l.offset]
		if unicode.IsSpace(r) {
			l.advance(r)
			continue
		}
		if r == ';' {
			for l.offset < len(l.input) && l.input[l.offset] != '\n' {
				l.advance(l.input[l.offset])
			}
			continue
		}
		break
	}

	start := l.span()
	r := l.input[l.offset]
	switch r {
	case '(':
		l.advance(r)
		return token{kind: tokenLParen, text: "(", span: spanWithEnd(start, l.span())}, nil
	case ')':
		l.advance(r)
		return token{kind: tokenRParen, text: ")", span: spanWithEnd(start, l.span())}, nil
	case '"':
		return l.stringToken()
	default:
		var b strings.Builder
		for l.offset < len(l.input) {
			r = l.input[l.offset]
			if unicode.IsSpace(r) || r == '(' || r == ')' || r == ';' {
				break
			}
			b.WriteRune(r)
			l.advance(r)
		}
		return token{kind: tokenAtom, text: b.String(), span: spanWithEnd(start, l.span())}, nil
	}
}

func (l *lexer) stringToken() (token, error) {
	start := l.span()
	l.advance('"')
	var b strings.Builder
	for l.offset < len(l.input) {
		r := l.input[l.offset]
		if r == '"' {
			l.advance(r)
			return token{kind: tokenString, text: b.String(), span: spanWithEnd(start, l.span())}, nil
		}
		if r == '\\' {
			l.advance(r)
			if l.offset >= len(l.input) {
				break
			}
			esc := l.input[l.offset]
			switch esc {
			case 'n':
				b.WriteRune('\n')
			case 'r':
				b.WriteRune('\r')
			case 't':
				b.WriteRune('\t')
			case '"', '\\':
				b.WriteRune(esc)
			default:
				b.WriteRune(esc)
			}
			l.advance(esc)
			continue
		}
		b.WriteRune(r)
		l.advance(r)
	}
	return token{}, &FileError{Span: start, Reason: "unterminated string literal"}
}

func (l *lexer) advance(r rune) {
	l.offset++
	if r == '\n' {
		l.line++
		l.col = 1
		return
	}
	l.col++
}

func (l *lexer) span() SourceSpan {
	return SourceSpan{Name: l.name, StartLine: l.line, StartColumn: l.col, EndLine: l.line, EndColumn: l.col}
}

func spanWithEnd(start, end SourceSpan) SourceSpan {
	start.EndLine = end.StartLine
	start.EndColumn = end.StartColumn
	return start
}

type parser struct {
	lexer *lexer
	peek  token
	has   bool
}

func Parse(name string, source []byte) ([]Expr, error) {
	p := &parser{lexer: newLexer(name, source)}
	var out []Expr
	for {
		tok, err := p.next()
		if err != nil {
			return nil, err
		}
		if tok.kind == tokenEOF {
			return out, nil
		}
		p.unread(tok)
		expr, err := p.expr()
		if err != nil {
			return nil, err
		}
		out = append(out, expr)
	}
}

func (p *parser) expr() (Expr, error) {
	tok, err := p.next()
	if err != nil {
		return Expr{}, err
	}
	switch tok.kind {
	case tokenAtom:
		return Expr{Atom: tok.text, Span: tok.span}, nil
	case tokenString:
		return Expr{Atom: tok.text, String: true, Span: tok.span}, nil
	case tokenLParen:
		start := tok.span
		var list []Expr
		for {
			next, err := p.next()
			if err != nil {
				return Expr{}, err
			}
			switch next.kind {
			case tokenEOF:
				return Expr{}, &FileError{Span: start, Reason: "unterminated list"}
			case tokenRParen:
				return Expr{List: list, Span: spanWithEnd(start, next.span)}, nil
			default:
				p.unread(next)
				child, err := p.expr()
				if err != nil {
					return Expr{}, err
				}
				list = append(list, child)
			}
		}
	case tokenRParen:
		return Expr{}, &FileError{Span: tok.span, Reason: "unexpected ')'"}
	case tokenEOF:
		return Expr{}, &FileError{Span: tok.span, Reason: "unexpected end of input"}
	default:
		return Expr{}, &FileError{Span: tok.span, Reason: "invalid token"}
	}
}

func (p *parser) next() (token, error) {
	if p.has {
		p.has = false
		return p.peek, nil
	}
	return p.lexer.next()
}

func (p *parser) unread(tok token) {
	p.peek = tok
	p.has = true
}

func AtomValue(expr Expr) (any, error) {
	if !expr.IsAtom() {
		return nil, &FileError{Span: expr.Span, Reason: "expected scalar value"}
	}
	if expr.String {
		return expr.Atom, nil
	}
	switch strings.ToUpper(expr.Atom) {
	case "TRUE":
		return true, nil
	case "FALSE":
		return false, nil
	case "NULL", "NIL":
		return nil, nil
	}
	if i, err := strconv.ParseInt(expr.Atom, 10, 64); err == nil {
		return i, nil
	}
	if f, err := strconv.ParseFloat(expr.Atom, 64); err == nil && strings.ContainsAny(expr.Atom, ".eE") {
		return f, nil
	}
	return expr.Atom, nil
}
