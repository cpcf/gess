package gesssexp

import (
	"bytes"
	"strconv"
	"strings"
)

const indentWidth = 2

// Format parses source and emits canonical .gess layout.
func Format(name string, source []byte) ([]byte, error) {
	exprs, err := Parse(name, source)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	for i, expr := range exprs {
		if i > 0 {
			b.WriteByte('\n')
			b.WriteByte('\n')
		}
		writeExpr(&b, expr, 0, formatExpanded)
	}
	if len(exprs) > 0 {
		b.WriteByte('\n')
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
		b.WriteByte(')')
		return
	}
	writeExpr(b, expr.List[0], indent+indentWidth, formatInline)
	next := 1
	for next < len(expr.List) && expr.List[next].IsAtom() {
		b.WriteByte(' ')
		writeExpr(b, expr.List[next], indent+indentWidth, formatInline)
		next++
	}
	if canAttachNestedHead(expr, next) {
		child := expr.List[next]
		b.WriteByte(' ')
		b.WriteByte('(')
		writeExpr(b, child.List[0], indent+indentWidth, formatInline)
		for i := 1; i < len(child.List); i++ {
			b.WriteByte('\n')
			writeIndent(b, indent+indentWidth)
			writeExpr(b, child.List[i], indent+indentWidth, formatAuto)
		}
		b.WriteByte('\n')
		writeIndent(b, indent)
		b.WriteByte(')')
		next++
	}
	for ; next < len(expr.List); next++ {
		if canWriteBindingPattern(expr.List, next) {
			b.WriteByte('\n')
			writeIndent(b, indent+indentWidth)
			writeBindingPattern(b, expr.List[next], expr.List[next+2], indent+indentWidth)
			next += 2
			continue
		}
		b.WriteByte('\n')
		writeIndent(b, indent+indentWidth)
		writeExpr(b, expr.List[next], indent+indentWidth, formatAuto)
	}
	b.WriteByte('\n')
	writeIndent(b, indent)
	b.WriteByte(')')
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
	b.WriteByte('(')
	writeExpr(b, pattern.List[0], indent, formatInline)
	for i := 1; i < len(pattern.List); i++ {
		b.WriteByte('\n')
		writeIndent(b, indent+indentWidth)
		writeExpr(b, pattern.List[i], indent+indentWidth, formatAuto)
	}
	b.WriteByte('\n')
	writeIndent(b, indent)
	b.WriteByte(')')
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
	if len(expr.List) == 0 {
		return true
	}
	switch expr.Head() {
	case "assert", "assert-logical", "return":
		return false
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
