package gess

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

type SourceSpan struct {
	Name        string
	StartLine   int
	StartColumn int
	EndLine     int
	EndColumn   int
}

// String returns the source location in name:line:column form.
func (s SourceSpan) String() string {
	if s.Name == "" {
		return fmt.Sprintf("%d:%d", s.StartLine, s.StartColumn)
	}
	return fmt.Sprintf("%s:%d:%d", s.Name, s.StartLine, s.StartColumn)
}

// GessFileError reports a Gess file parser or lowering error with source location.
type GessFileError struct {
	Span   SourceSpan
	Reason string
	Err    error
}

func (e *GessFileError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Span.String(), e.Reason, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Span.String(), e.Reason)
}

func (e *GessFileError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

type gessSExpr struct {
	atom   string
	list   []gessSExpr
	string bool
	span   SourceSpan
}

func (e gessSExpr) isAtom() bool {
	return e.list == nil
}

func (e gessSExpr) text() string {
	return e.atom
}

func (e gessSExpr) head() string {
	if len(e.list) == 0 || !e.list[0].isAtom() {
		return ""
	}
	return e.list[0].atom
}

type gessTokenKind uint8

const (
	gessTokenEOF gessTokenKind = iota
	gessTokenLParen
	gessTokenRParen
	gessTokenAtom
	gessTokenString
)

type gessToken struct {
	kind gessTokenKind
	text string
	span SourceSpan
}

type gessLexer struct {
	name   string
	input  []rune
	offset int
	line   int
	col    int
}

func newGessLexer(name string, source []byte) *gessLexer {
	return &gessLexer{name: name, input: []rune(string(source)), line: 1, col: 1}
}

func (l *gessLexer) next() (gessToken, error) {
	for {
		if l.offset >= len(l.input) {
			return gessToken{kind: gessTokenEOF, span: l.span()}, nil
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
		return gessToken{kind: gessTokenLParen, text: "(", span: start.withEnd(l.span())}, nil
	case ')':
		l.advance(r)
		return gessToken{kind: gessTokenRParen, text: ")", span: start.withEnd(l.span())}, nil
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
		return gessToken{kind: gessTokenAtom, text: b.String(), span: start.withEnd(l.span())}, nil
	}
}

func (l *gessLexer) stringToken() (gessToken, error) {
	start := l.span()
	l.advance('"')
	var b strings.Builder
	for l.offset < len(l.input) {
		r := l.input[l.offset]
		if r == '"' {
			l.advance(r)
			return gessToken{kind: gessTokenString, text: b.String(), span: start.withEnd(l.span())}, nil
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
	return gessToken{}, &GessFileError{Span: start, Reason: "unterminated string literal"}
}

func (l *gessLexer) advance(r rune) {
	l.offset++
	if r == '\n' {
		l.line++
		l.col = 1
		return
	}
	l.col++
}

func (l *gessLexer) span() SourceSpan {
	return SourceSpan{Name: l.name, StartLine: l.line, StartColumn: l.col, EndLine: l.line, EndColumn: l.col}
}

func (s SourceSpan) withEnd(end SourceSpan) SourceSpan {
	s.EndLine = end.StartLine
	s.EndColumn = end.StartColumn
	return s
}

type gessParser struct {
	lexer *gessLexer
	peek  gessToken
	has   bool
}

func parseGessSExprs(name string, source []byte) ([]gessSExpr, error) {
	p := &gessParser{lexer: newGessLexer(name, source)}
	var out []gessSExpr
	for {
		tok, err := p.next()
		if err != nil {
			return nil, err
		}
		if tok.kind == gessTokenEOF {
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

func (p *gessParser) expr() (gessSExpr, error) {
	tok, err := p.next()
	if err != nil {
		return gessSExpr{}, err
	}
	switch tok.kind {
	case gessTokenAtom:
		return gessSExpr{atom: tok.text, span: tok.span}, nil
	case gessTokenString:
		return gessSExpr{atom: tok.text, string: true, span: tok.span}, nil
	case gessTokenLParen:
		start := tok.span
		var list []gessSExpr
		for {
			next, err := p.next()
			if err != nil {
				return gessSExpr{}, err
			}
			switch next.kind {
			case gessTokenEOF:
				return gessSExpr{}, &GessFileError{Span: start, Reason: "unterminated list"}
			case gessTokenRParen:
				return gessSExpr{list: list, span: start.withEnd(next.span)}, nil
			default:
				p.unread(next)
				child, err := p.expr()
				if err != nil {
					return gessSExpr{}, err
				}
				list = append(list, child)
			}
		}
	case gessTokenRParen:
		return gessSExpr{}, &GessFileError{Span: tok.span, Reason: "unexpected ')'"}
	case gessTokenEOF:
		return gessSExpr{}, &GessFileError{Span: tok.span, Reason: "unexpected end of input"}
	default:
		return gessSExpr{}, &GessFileError{Span: tok.span, Reason: "invalid token"}
	}
}

func (p *gessParser) next() (gessToken, error) {
	if p.has {
		p.has = false
		return p.peek, nil
	}
	return p.lexer.next()
}

func (p *gessParser) unread(tok gessToken) {
	p.peek = tok
	p.has = true
}

func gessAtomValue(expr gessSExpr) (any, error) {
	if !expr.isAtom() {
		return nil, &GessFileError{Span: expr.span, Reason: "expected scalar value"}
	}
	if expr.string {
		return expr.atom, nil
	}
	switch strings.ToUpper(expr.atom) {
	case "TRUE":
		return true, nil
	case "FALSE":
		return false, nil
	case "NULL", "NIL":
		return nil, nil
	}
	if i, err := strconv.ParseInt(expr.atom, 10, 64); err == nil {
		return i, nil
	}
	if f, err := strconv.ParseFloat(expr.atom, 64); err == nil && strings.ContainsAny(expr.atom, ".eE") {
		return f, nil
	}
	return expr.atom, nil
}
