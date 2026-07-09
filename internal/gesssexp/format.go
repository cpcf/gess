package gesssexp

import (
	"bytes"
	"strconv"
	"strings"
)

const indentWidth = 2

// Format parses source and emits canonical .gess layout, preserving `;`
// comments: leading comment lines stay above the expression they precede,
// same-line comments stay on their line, and comments before a closing
// parenthesis or at end of file keep their position.
func Format(name string, source []byte) ([]byte, error) {
	exprs, tail, err := parseAll(name, source)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	for i, expr := range exprs {
		if i > 0 {
			b.WriteByte('\n')
			b.WriteByte('\n')
		}
		for _, comment := range expr.Leading {
			b.WriteString(comment)
			b.WriteByte('\n')
		}
		writeExpr(&b, expr, 0, formatExpanded)
		if expr.Trailing != "" {
			b.WriteByte(' ')
			b.WriteString(expr.Trailing)
		}
	}
	if len(exprs) > 0 {
		b.WriteByte('\n')
	}
	if len(tail) > 0 {
		if len(exprs) > 0 {
			b.WriteByte('\n')
		}
		for _, comment := range tail {
			b.WriteString(comment)
			b.WriteByte('\n')
		}
	}
	return b.Bytes(), nil
}

type formatMode uint8

const (
	formatAuto formatMode = iota
	formatExpanded
	formatInline
)

func writeExpr(b *bytes.Buffer, expr Expr, indent int, mode formatMode) {
	if expr.IsAtom() {
		writeAtom(b, expr)
		return
	}
	if mode != formatExpanded && shouldInline(expr) {
		writeInlineList(b, expr)
		return
	}
	writeExpandedList(b, expr, indent)
}

func writeExpandedList(b *bytes.Buffer, expr Expr, indent int) {
	b.WriteByte('(')
	if len(expr.List) == 0 {
		if len(expr.Dangling) == 0 {
			b.WriteByte(')')
			return
		}
		writeDanglingAndClose(b, expr, indent)
		return
	}
	next := 0
	head := expr.List[0]
	if len(head.Leading) == 0 && head.Trailing == "" {
		writeExpr(b, head, indent+indentWidth, formatInline)
		next = 1
		for next < len(expr.List) && expr.List[next].IsAtom() && exprTriviaFree(expr.List[next]) {
			b.WriteByte(' ')
			writeExpr(b, expr.List[next], indent+indentWidth, formatInline)
			next++
		}
		if canAttachNestedHead(expr, next) && exprTriviaFree(expr.List[next]) && !exprInnerTrivia(expr.List[next]) {
			child := expr.List[next]
			b.WriteByte(' ')
			b.WriteByte('(')
			writeExpr(b, child.List[0], indent+indentWidth, formatInline)
			for i := 1; i < len(child.List); i++ {
				writeChildLine(b, child.List[i], indent+indentWidth)
			}
			b.WriteByte('\n')
			writeIndent(b, indent)
			b.WriteByte(')')
			next++
		}
	}
	for ; next < len(expr.List); next++ {
		if canWriteBindingPattern(expr.List, next) &&
			exprTriviaFree(expr.List[next]) && exprTriviaFree(expr.List[next+1]) &&
			exprTriviaFree(expr.List[next+2]) {
			b.WriteByte('\n')
			writeIndent(b, indent+indentWidth)
			writeBindingPattern(b, expr.List[next], expr.List[next+2], indent+indentWidth)
			next += 2
			continue
		}
		writeChildLine(b, expr.List[next], indent+indentWidth)
	}
	writeDanglingAndClose(b, expr, indent)
}

// writeChildLine emits one child on its own line: its leading comments, the
// child itself, and any same-line trailing comment.
func writeChildLine(b *bytes.Buffer, child Expr, indent int) {
	b.WriteByte('\n')
	for _, comment := range child.Leading {
		writeIndent(b, indent)
		b.WriteString(comment)
		b.WriteByte('\n')
	}
	writeIndent(b, indent)
	writeExpr(b, child, indent, formatAuto)
	if child.Trailing != "" {
		b.WriteByte(' ')
		b.WriteString(child.Trailing)
	}
}

func writeDanglingAndClose(b *bytes.Buffer, expr Expr, indent int) {
	for _, comment := range expr.Dangling {
		b.WriteByte('\n')
		writeIndent(b, indent+indentWidth)
		b.WriteString(comment)
	}
	b.WriteByte('\n')
	writeIndent(b, indent)
	b.WriteByte(')')
}

func exprTriviaFree(expr Expr) bool {
	return len(expr.Leading) == 0 && expr.Trailing == ""
}

// exprInnerTrivia reports whether formatting expr inline would lose comments
// attached inside it.
func exprInnerTrivia(expr Expr) bool {
	if len(expr.Dangling) > 0 {
		return true
	}
	for _, child := range expr.List {
		if !exprTriviaFree(child) || exprInnerTrivia(child) {
			return true
		}
	}
	return false
}

func canWriteBindingPattern(list []Expr, index int) bool {
	return index+2 < len(list) &&
		list[index].IsAtom() &&
		list[index+1].IsAtom() &&
		list[index+1].Atom == "<-" &&
		!list[index+2].IsAtom() &&
		len(list[index+2].List) > 0 &&
		list[index+2].List[0].IsAtom()
}

func writeBindingPattern(b *bytes.Buffer, binding Expr, pattern Expr, indent int) {
	writeExpr(b, binding, indent, formatInline)
	b.WriteString(" <- ")
	if len(pattern.List) > 0 && exprTriviaFree(pattern.List[0]) {
		b.WriteByte('(')
		writeExpr(b, pattern.List[0], indent, formatInline)
		for i := 1; i < len(pattern.List); i++ {
			writeChildLine(b, pattern.List[i], indent+indentWidth)
		}
		writeDanglingAndClose(b, pattern, indent)
		return
	}
	writeExpr(b, pattern, indent, formatExpanded)
}

func canAttachNestedHead(expr Expr, index int) bool {
	if index != 1 || len(expr.List) != 2 || expr.Head() != "assert" && expr.Head() != "assert-logical" {
		return false
	}
	child := expr.List[index]
	return !child.IsAtom() && len(child.List) > 0 && child.List[0].IsAtom()
}

func writeInlineList(b *bytes.Buffer, expr Expr) {
	b.WriteByte('(')
	for i, child := range expr.List {
		if i > 0 {
			b.WriteByte(' ')
		}
		writeExpr(b, child, 0, formatInline)
	}
	b.WriteByte(')')
}

func shouldInline(expr Expr) bool {
	if expr.IsAtom() {
		return true
	}
	if exprInnerTrivia(expr) {
		return false
	}
	if len(expr.List) == 0 {
		return true
	}
	switch expr.Head() {
	case "assert", "assert-logical":
		return false
	case "return":
		// Query returns hold (alias value) lists and never inline; a
		// deffunction return kind such as (return BOOL) inlines normally.
		if len(expr.List) != 2 || !expr.List[1].IsAtom() {
			return false
		}
	}
	if len(expr.List) > 4 {
		return false
	}
	for _, child := range expr.List {
		if !child.IsAtom() && !shouldInline(child) {
			return false
		}
	}
	return inlineLen(expr) <= 88
}

func inlineLen(expr Expr) int {
	if expr.IsAtom() {
		return atomLen(expr)
	}
	n := 2
	for i, child := range expr.List {
		if i > 0 {
			n++
		}
		n += inlineLen(child)
	}
	return n
}

func atomLen(expr Expr) int {
	if !expr.String {
		return len(expr.Atom)
	}
	return len(strconv.Quote(expr.Atom))
}

func writeAtom(b *bytes.Buffer, expr Expr) {
	if expr.String {
		b.WriteString(strconv.Quote(expr.Atom))
		return
	}
	b.WriteString(expr.Atom)
}

func writeIndent(b *bytes.Buffer, indent int) {
	b.WriteString(strings.Repeat(" ", indent))
}
