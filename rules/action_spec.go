package rules

import "strings"

// ActionSpec names one action implementation registered on a workspace.
type ActionSpec struct {
	Name                 string
	Fn                   ActionFunc
	AssertTemplateValues *AssertTemplateValuesActionSpec
	Effect               *ActionEffectSpec
	Call                 *ActionCallSpec
	BindingReads         *ActionBindingReadSetSpec
	GessSource           string
	// NonEscaping allows the engine to skip freezing unread bindings after a
	// rule fires. Set it only when Fn does not retain ActionContext or any
	// binding-derived data that depends on post-return defensive snapshots.
	NonEscaping bool
}

// CloneActionSpec returns a defensive copy of s.
func CloneActionSpec(s ActionSpec) ActionSpec {
	out := s
	out.Name = strings.TrimSpace(out.Name)
	out.GessSource = strings.TrimSpace(out.GessSource)
	out.AssertTemplateValues = CloneAssertTemplateValuesActionSpec(s.AssertTemplateValues)
	out.Effect = CloneActionEffectSpec(s.Effect)
	out.Call = CloneActionCallSpec(s.Call)
	out.BindingReads = CloneActionBindingReadSetSpec(s.BindingReads)
	return out
}

// ActionCallSpec is a host-function call action.
type ActionCallSpec struct {
	Name string
	Fn   DSLCallFunc
	Args []ExpressionSpec
}

// CloneActionCallSpec returns a defensive copy of s.
func CloneActionCallSpec(s *ActionCallSpec) *ActionCallSpec {
	if s == nil {
		return nil
	}
	out := &ActionCallSpec{
		Name: strings.TrimSpace(s.Name),
		Fn:   s.Fn,
		Args: make([]ExpressionSpec, len(s.Args)),
	}
	for i, arg := range s.Args {
		out.Args[i] = CloneExpressionSpec(arg)
	}
	return out
}

// ActionEffectSpec is a declarative, expression-backed rule action.
type ActionEffectSpec struct {
	Kind ActionEffectKind
	// Target is the fact-binding name for modify/retract, or the local name
	// for bind. Unused by emit.
	Target string
	// TemplateKey/FactName identify the asserted template for assert/-logical.
	TemplateKey TemplateKey
	FactName    string
	// Fields names the slots set by assert/modify, parallel to Values.
	Fields []string
	// Unset names the slots cleared by modify.
	Unset []string
	// Values are the expression-valued operands.
	Values []ExpressionSpec
}

// CloneActionEffectSpec returns a defensive copy of s.
func CloneActionEffectSpec(s *ActionEffectSpec) *ActionEffectSpec {
	if s == nil {
		return nil
	}
	out := &ActionEffectSpec{
		Kind:        s.Kind,
		Target:      strings.TrimSpace(s.Target),
		TemplateKey: s.TemplateKey,
		FactName:    strings.TrimSpace(s.FactName),
		Fields:      append([]string(nil), s.Fields...),
		Unset:       append([]string(nil), s.Unset...),
		Values:      make([]ExpressionSpec, len(s.Values)),
	}
	for i, v := range s.Values {
		out.Values[i] = CloneExpressionSpec(v)
	}
	return out
}

// ActionBindingReadSetSpec declares which bindings an action reads.
type ActionBindingReadSetSpec struct {
	Reads []ActionBindingReadSpec
}

// CloneActionBindingReadSetSpec returns a defensive copy of s.
func CloneActionBindingReadSetSpec(s *ActionBindingReadSetSpec) *ActionBindingReadSetSpec {
	if s == nil {
		return nil
	}
	out := &ActionBindingReadSetSpec{
		Reads: make([]ActionBindingReadSpec, len(s.Reads)),
	}
	for i, read := range s.Reads {
		out.Reads[i] = CloneActionBindingReadSpec(read)
	}
	return out
}

// ActionBindingReadSpec declares one binding an action reads.
type ActionBindingReadSpec struct {
	Binding string
	Field   string
	Path    PathSpec
}

// CloneActionBindingReadSpec returns a defensive copy of s.
func CloneActionBindingReadSpec(s ActionBindingReadSpec) ActionBindingReadSpec {
	out := s
	out.Binding = strings.TrimSpace(out.Binding)
	out.Field = strings.TrimSpace(out.Field)
	out.Path = clonePathSpec(out.Path)
	return out
}

// AssertTemplateValuesActionSpec describes a generated rule action that emits
// values in template field order.
type AssertTemplateValuesActionSpec struct {
	TemplateKey TemplateKey
	Values      []ExpressionSpec
}

// CloneAssertTemplateValuesActionSpec returns a defensive copy of s.
func CloneAssertTemplateValuesActionSpec(s *AssertTemplateValuesActionSpec) *AssertTemplateValuesActionSpec {
	if s == nil {
		return nil
	}
	out := &AssertTemplateValuesActionSpec{
		TemplateKey: s.TemplateKey,
		Values:      make([]ExpressionSpec, len(s.Values)),
	}
	for i, value := range s.Values {
		out.Values[i] = CloneExpressionSpec(value)
	}
	return out
}

// Action is a compiled, inspectable action reference on a rule or query.
type Action struct {
	NameValue                  string
	Order                      int
	GessSourceText             string
	AssertTemplateValuesAction *AssertTemplateValuesActionSpec
}

func (a Action) Name() string {
	return a.NameValue
}

func (a Action) DeclarationOrder() int {
	return a.Order
}

func (a Action) GessSource() string {
	return a.GessSourceText
}

func (a Action) AssertTemplateValues() (*AssertTemplateValuesActionSpec, bool) {
	if a.AssertTemplateValuesAction == nil {
		return nil, false
	}
	return CloneAssertTemplateValuesActionSpec(a.AssertTemplateValuesAction), true
}

// CloneAction returns a defensive copy of a.
func CloneAction(a Action) Action {
	a.AssertTemplateValuesAction = CloneAssertTemplateValuesActionSpec(a.AssertTemplateValuesAction)
	return a
}
