package rules

import "strings"

// ListPatternElementSpec is one element of a ListPatternSpec.
type ListPatternElementSpec struct {
	Kind       ListPatternElementKind
	Expression ExpressionSpec
	Binding    string
}

// CloneListPatternElementSpec returns a defensive copy of s.
func CloneListPatternElementSpec(s ListPatternElementSpec) ListPatternElementSpec {
	s.Binding = strings.TrimSpace(s.Binding)
	s.Expression = CloneExpressionSpec(s.Expression)
	return s
}

// ListPatternSpec is a structural pattern over a LIST-typed field.
type ListPatternSpec struct {
	Path     PathSpec
	Elements []ListPatternElementSpec
}

// CloneListPatternSpec returns a defensive copy of s.
func CloneListPatternSpec(s ListPatternSpec) ListPatternSpec {
	out := s
	out.Path = clonePathSpec(s.Path)
	out.Elements = make([]ListPatternElementSpec, len(s.Elements))
	for i, element := range s.Elements {
		out.Elements[i] = CloneListPatternElementSpec(element)
	}
	return out
}

// ListPattern builds a ListPatternSpec matching elements positionally against
// the LIST-typed field at path.
func ListPattern(path PathSpec, elements ...ListPatternElementSpec) ListPatternSpec {
	out := ListPatternSpec{
		Path:     clonePathSpec(path),
		Elements: make([]ListPatternElementSpec, len(elements)),
	}
	for i, element := range elements {
		out.Elements[i] = CloneListPatternElementSpec(element)
	}
	return out
}

// ListElem builds a fixed-position list pattern element.
func ListElem(expression ExpressionSpec) ListPatternElementSpec {
	return ListPatternElementSpec{Kind: ListPatternElementValue, Expression: CloneExpressionSpec(expression)}
}

// ListWildcard builds a list pattern element matching any one element.
func ListWildcard() ListPatternElementSpec {
	return ListPatternElementSpec{Kind: ListPatternElementWildcard}
}

// ListSegment builds a list pattern element matching a variable-length run.
func ListSegment(binding string) ListPatternElementSpec {
	return ListPatternElementSpec{Kind: ListPatternElementSegment, Binding: strings.TrimSpace(binding)}
}

// ListRestWildcard builds a list pattern element matching a variable-length
// run without capturing it.
func ListRestWildcard() ListPatternElementSpec {
	return ListPatternElementSpec{Kind: ListPatternElementRestWildcard}
}

// ListPatternElement is the compiled, inspectable form of a
// ListPatternElementSpec.
type ListPatternElement struct {
	KindValue      ListPatternElementKind
	ExpressionSpec ExpressionSpec
	BindingName    string
	Order          int
}

func (e ListPatternElement) Kind() ListPatternElementKind {
	return e.KindValue
}

func (e ListPatternElement) Expression() ExpressionSpec {
	return CloneExpressionSpec(e.ExpressionSpec)
}

func (e ListPatternElement) Binding() string {
	return e.BindingName
}

func (e ListPatternElement) DeclarationOrder() int {
	return e.Order
}

// CloneListPatternElement returns a defensive copy of e.
func CloneListPatternElement(e ListPatternElement) ListPatternElement {
	e.ExpressionSpec = CloneExpressionSpec(e.ExpressionSpec)
	return e
}

// RuleListPattern is the compiled, inspectable list pattern attached to a rule
// condition.
type RuleListPattern struct {
	PathSpec      PathSpec
	ElementsValue []ListPatternElement
	Order         int
}

func (p RuleListPattern) Path() PathSpec {
	return clonePathSpec(p.PathSpec)
}

func (p RuleListPattern) Elements() []ListPatternElement {
	out := make([]ListPatternElement, len(p.ElementsValue))
	for i, element := range p.ElementsValue {
		out[i] = CloneListPatternElement(element)
	}
	return out
}

func (p RuleListPattern) DeclarationOrder() int {
	return p.Order
}

// CloneRuleListPattern returns a defensive copy of p.
func CloneRuleListPattern(p RuleListPattern) RuleListPattern {
	out := p
	out.PathSpec = clonePathSpec(p.PathSpec)
	out.ElementsValue = make([]ListPatternElement, len(p.ElementsValue))
	for i, element := range p.ElementsValue {
		out.ElementsValue[i] = CloneListPatternElement(element)
	}
	return out
}

// CloneRuleListPatterns returns a defensive copy of patterns.
func CloneRuleListPatterns(patterns []RuleListPattern) []RuleListPattern {
	out := make([]RuleListPattern, len(patterns))
	for i, pattern := range patterns {
		out[i] = CloneRuleListPattern(pattern)
	}
	return out
}
