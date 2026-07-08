package rules

import "strings"

// FieldConstraintSpec constrains one field, or nested path, of a matched fact.
type FieldConstraintSpec struct {
	Field    string
	Path     PathSpec
	Operator FieldConstraintOperator
	Value    any
}

// CloneFieldConstraintSpec returns a defensive copy of s.
func CloneFieldConstraintSpec(s FieldConstraintSpec) FieldConstraintSpec {
	out := s
	out.Field = strings.TrimSpace(out.Field)
	out.Path = clonePathSpec(out.Path)
	out.Value = cloneSpecValue(out.Value)
	return out
}

// RuleFieldConstraintSpec is an alias of FieldConstraintSpec, used in
// rule-condition contexts.
type RuleFieldConstraintSpec = FieldConstraintSpec

// FieldConstraint is the compiled, inspectable form of a FieldConstraintSpec.
type FieldConstraint struct {
	Field    string
	Path     PathSpec
	Operator FieldConstraintOperator
	Value    Value
}

// CloneFieldConstraint returns a defensive copy of c.
func CloneFieldConstraint(c FieldConstraint) FieldConstraint {
	c.Path = clonePathSpec(c.Path)
	c.Value = CloneValue(c.Value)
	return c
}

func cloneFieldConstraints(constraints []FieldConstraint) []FieldConstraint {
	out := make([]FieldConstraint, len(constraints))
	for i, constraint := range constraints {
		out[i] = CloneFieldConstraint(constraint)
	}
	return out
}

// RuleFieldConstraint is an alias of FieldConstraint.
type RuleFieldConstraint = FieldConstraint

// FieldRef names a field, or nested path, of an earlier binding.
type FieldRef struct {
	Binding string
	Field   string
	Path    PathSpec
}

// CloneFieldRef returns a defensive copy of r.
func CloneFieldRef(r FieldRef) FieldRef {
	out := r
	out.Binding = strings.TrimSpace(out.Binding)
	out.Field = strings.TrimSpace(out.Field)
	out.Path = clonePathSpec(out.Path)
	return out
}

// JoinConstraintSpec compares a field of the matched fact against a field of an
// earlier binding named by Ref.
type JoinConstraintSpec struct {
	Field    string
	Path     PathSpec
	Operator FieldConstraintOperator
	Ref      FieldRef
}

// CloneJoinConstraintSpec returns a defensive copy of s.
func CloneJoinConstraintSpec(s JoinConstraintSpec) JoinConstraintSpec {
	out := s
	out.Field = strings.TrimSpace(out.Field)
	out.Path = clonePathSpec(out.Path)
	out.Ref = CloneFieldRef(out.Ref)
	return out
}

// JoinConstraint is the compiled, inspectable form of a JoinConstraintSpec.
type JoinConstraint struct {
	Field    string
	Path     PathSpec
	Operator FieldConstraintOperator
	Ref      FieldRef
}

// CloneJoinConstraint returns a defensive copy of c.
func CloneJoinConstraint(c JoinConstraint) JoinConstraint {
	c.Path = clonePathSpec(c.Path)
	c.Ref = CloneFieldRef(c.Ref)
	return c
}

func cloneJoinConstraints(constraints []JoinConstraint) []JoinConstraint {
	out := make([]JoinConstraint, len(constraints))
	for i, constraint := range constraints {
		out[i] = CloneJoinConstraint(constraint)
	}
	return out
}
