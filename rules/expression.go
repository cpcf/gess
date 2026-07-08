package rules

import "strings"

// ExpressionSpec is a deterministic rule predicate expression tree node.
type ExpressionSpec interface {
	expressionSpecNode()
}

// ConstExpr is a literal value in an expression tree.
type ConstExpr struct {
	Value any
}

func (ConstExpr) expressionSpecNode() {}

func (s ConstExpr) clone() ConstExpr {
	return ConstExpr{Value: cloneSpecValue(s.Value)}
}

// CurrentFieldExpr references a field, or nested path, of the fact matched by
// the current condition.
type CurrentFieldExpr struct {
	Field string
	Path  PathSpec
}

func (CurrentFieldExpr) expressionSpecNode() {}

func (s CurrentFieldExpr) clone() CurrentFieldExpr {
	s.Field = strings.TrimSpace(s.Field)
	s.Path = clonePathSpec(s.Path)
	return s
}

// BindingFieldExpr references a field, or nested path, of an earlier
// condition's binding.
type BindingFieldExpr struct {
	Binding string
	Field   string
	Path    PathSpec
}

func (BindingFieldExpr) expressionSpecNode() {}

func (s BindingFieldExpr) clone() BindingFieldExpr {
	s.Binding = strings.TrimSpace(s.Binding)
	s.Field = strings.TrimSpace(s.Field)
	s.Path = clonePathSpec(s.Path)
	return s
}

// HasPathExpr evaluates to true when Path resolves to a present value on the
// current fact.
type HasPathExpr struct {
	Path PathSpec
}

func (HasPathExpr) expressionSpecNode() {}

func (s HasPathExpr) clone() HasPathExpr {
	s.Path = clonePathSpec(s.Path)
	return s
}

// CurrentPath builds a CurrentFieldExpr referencing the current condition's
// fact through path.
func CurrentPath(path PathSpec) CurrentFieldExpr {
	return CurrentFieldExpr{Path: clonePathSpec(path)}
}

// BindingPath builds a BindingFieldExpr referencing an earlier condition's
// binding through path.
func BindingPath(binding string, path PathSpec) BindingFieldExpr {
	return BindingFieldExpr{Binding: binding, Path: clonePathSpec(path)}
}

// HasPath builds a HasPathExpr testing whether path resolves to a present value
// on the current fact.
func HasPath(path PathSpec) HasPathExpr {
	return HasPathExpr{Path: clonePathSpec(path)}
}

// BindingValueExpr references a value binding, such as an aggregate result,
// from an earlier condition.
type BindingValueExpr struct {
	Binding string
}

func (BindingValueExpr) expressionSpecNode() {}

func (s BindingValueExpr) clone() BindingValueExpr {
	s.Binding = strings.TrimSpace(s.Binding)
	return s
}

// ParamExpr references a named query parameter.
type ParamExpr struct {
	Name string
}

func (ParamExpr) expressionSpecNode() {}

func (s ParamExpr) clone() ParamExpr {
	s.Name = strings.TrimSpace(s.Name)
	return s
}

// GlobalExpr references a declared session global.
type GlobalExpr struct {
	Name string
}

func (GlobalExpr) expressionSpecNode() {}

func (s GlobalExpr) clone() GlobalExpr {
	s.Name = strings.TrimSpace(s.Name)
	return s
}

// RHSBindExpr references a right-hand-side local variable created by a bind
// action earlier in the same rule firing.
type RHSBindExpr struct {
	Name string
}

func (RHSBindExpr) expressionSpecNode() {}

func (s RHSBindExpr) clone() RHSBindExpr {
	s.Name = strings.TrimSpace(s.Name)
	return s
}

// CallExpr invokes a registered pure function by name with Args.
type CallExpr struct {
	Name string
	Args []ExpressionSpec
}

func (CallExpr) expressionSpecNode() {}

// Call builds a CallExpr invoking the registered pure function name with args.
func Call(name string, args ...ExpressionSpec) CallExpr {
	out := CallExpr{Name: strings.TrimSpace(name), Args: make([]ExpressionSpec, len(args))}
	for i, arg := range args {
		out.Args[i] = CloneExpressionSpec(arg)
	}
	return out
}

func (s CallExpr) clone() CallExpr {
	s.Name = strings.TrimSpace(s.Name)
	args := s.Args
	s.Args = make([]ExpressionSpec, len(args))
	for i, arg := range args {
		s.Args[i] = CloneExpressionSpec(arg)
	}
	return s
}

// CompareExpr compares Left and Right with Operator.
type CompareExpr struct {
	Operator ExpressionComparisonOperator
	Left     ExpressionSpec
	Right    ExpressionSpec
}

func (CompareExpr) expressionSpecNode() {}

func (s CompareExpr) clone() CompareExpr {
	s.Left = CloneExpressionSpec(s.Left)
	s.Right = CloneExpressionSpec(s.Right)
	return s
}

// BooleanExpr combines Operands with Operator.
type BooleanExpr struct {
	Operator ExpressionBooleanOperator
	Operands []ExpressionSpec
}

func (BooleanExpr) expressionSpecNode() {}

func (s BooleanExpr) clone() BooleanExpr {
	operands := s.Operands
	s.Operands = make([]ExpressionSpec, len(operands))
	for i, operand := range operands {
		s.Operands[i] = CloneExpressionSpec(operand)
	}
	return s
}

// ExpressionPredicate wraps a compiled predicate for inspection.
type ExpressionPredicate struct {
	ExpressionSpec ExpressionSpec
	PlacementValue ExpressionPredicatePlacement
	Order          int
	SourceSpan     SourceSpan
}

func (p ExpressionPredicate) Expression() ExpressionSpec {
	return CloneExpressionSpec(p.ExpressionSpec)
}

func (p ExpressionPredicate) Placement() ExpressionPredicatePlacement {
	return p.PlacementValue
}

func (p ExpressionPredicate) DeclarationOrder() int {
	return p.Order
}

func (p ExpressionPredicate) Source() SourceSpan {
	return p.SourceSpan
}

// CloneExpressionPredicate returns a defensive copy of p.
func CloneExpressionPredicate(p ExpressionPredicate) ExpressionPredicate {
	p.ExpressionSpec = CloneExpressionSpec(p.ExpressionSpec)
	return p
}

// CloneExpressionPredicates returns a defensive copy of predicates.
func CloneExpressionPredicates(predicates []ExpressionPredicate) []ExpressionPredicate {
	if len(predicates) == 0 {
		return nil
	}
	out := make([]ExpressionPredicate, len(predicates))
	for i, predicate := range predicates {
		out[i] = CloneExpressionPredicate(predicate)
	}
	return out
}

// CloneExpressionSpec returns a defensive copy of spec when it is one of the
// built-in expression spec nodes.
func CloneExpressionSpec(spec ExpressionSpec) ExpressionSpec {
	switch expression := spec.(type) {
	case nil:
		return nil
	case ConstExpr:
		return expression.clone()
	case *ConstExpr:
		if expression == nil {
			return nil
		}
		cloned := expression.clone()
		return &cloned
	case CurrentFieldExpr:
		return expression.clone()
	case *CurrentFieldExpr:
		if expression == nil {
			return nil
		}
		cloned := expression.clone()
		return &cloned
	case BindingFieldExpr:
		return expression.clone()
	case *BindingFieldExpr:
		if expression == nil {
			return nil
		}
		cloned := expression.clone()
		return &cloned
	case HasPathExpr:
		return expression.clone()
	case *HasPathExpr:
		if expression == nil {
			return nil
		}
		cloned := expression.clone()
		return &cloned
	case BindingValueExpr:
		return expression.clone()
	case *BindingValueExpr:
		if expression == nil {
			return nil
		}
		cloned := expression.clone()
		return &cloned
	case ParamExpr:
		return expression.clone()
	case *ParamExpr:
		if expression == nil {
			return nil
		}
		cloned := expression.clone()
		return &cloned
	case GlobalExpr:
		return expression.clone()
	case *GlobalExpr:
		if expression == nil {
			return nil
		}
		cloned := expression.clone()
		return &cloned
	case RHSBindExpr:
		return expression.clone()
	case *RHSBindExpr:
		if expression == nil {
			return nil
		}
		cloned := expression.clone()
		return &cloned
	case CallExpr:
		return expression.clone()
	case *CallExpr:
		if expression == nil {
			return nil
		}
		cloned := expression.clone()
		return &cloned
	case CompareExpr:
		return expression.clone()
	case *CompareExpr:
		if expression == nil {
			return nil
		}
		cloned := expression.clone()
		return &cloned
	case BooleanExpr:
		return expression.clone()
	case *BooleanExpr:
		if expression == nil {
			return nil
		}
		cloned := expression.clone()
		return &cloned
	default:
		return spec
	}
}

func cloneSpecValue(value any) any {
	switch typed := value.(type) {
	case Value:
		return CloneValue(typed)
	default:
		return typed
	}
}

func clonePathSpec(p PathSpec) PathSpec {
	if len(p.Segments) == 0 {
		return PathSpec{}
	}
	out := PathSpec{Segments: make([]PathSegment, len(p.Segments))}
	copy(out.Segments, p.Segments)
	return out
}
