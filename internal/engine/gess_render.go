package engine

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/cpcf/gess/internal/gesssexp"
)

func RenderGessRuleset(r *Ruleset) ([]byte, error) {
	if r == nil {
		return nil, ErrInvalidRuleset
	}
	renderer := gessRenderer{ruleset: r}
	var b strings.Builder
	for _, module := range r.Modules() {
		if module.Name() == MainModule {
			if _, ok := module.AutoFocusDefault(); !ok && module.Description() == "" {
				continue
			}
		}
		renderer.writeModule(&b, module)
	}
	for _, global := range r.Globals() {
		renderer.writeGlobal(&b, global)
	}
	for _, fn := range r.Functions() {
		if err := renderer.writeFunction(&b, fn); err != nil {
			return nil, err
		}
	}
	for _, template := range r.Templates() {
		renderer.writeTemplate(&b, template)
	}
	for _, rule := range r.Rules() {
		if err := renderer.writeRule(&b, rule); err != nil {
			return nil, err
		}
	}
	for _, query := range r.Queries() {
		if err := renderer.writeQuery(&b, query); err != nil {
			return nil, err
		}
	}
	return formatRenderedGess(b.String())
}

func RenderGessModule(r *Ruleset, name ModuleName) ([]byte, error) {
	if r == nil {
		return nil, ErrInvalidRuleset
	}
	module, ok := r.Module(name)
	if !ok {
		return nil, fmt.Errorf("%w: module %q not found", ErrInvalidRuleset, name)
	}
	var b strings.Builder
	gessRenderer{ruleset: r}.writeModule(&b, module)
	return formatRenderedGess(b.String())
}

func RenderGessTemplate(r *Ruleset, name string) ([]byte, error) {
	if r == nil {
		return nil, ErrInvalidRuleset
	}
	template, ok := r.Template(name)
	if !ok {
		return nil, fmt.Errorf("%w: template %q not found", ErrInvalidRuleset, name)
	}
	var b strings.Builder
	gessRenderer{ruleset: r}.writeTemplate(&b, template)
	return formatRenderedGess(b.String())
}

func RenderGessRule(r *Ruleset, name string) ([]byte, error) {
	if r == nil {
		return nil, ErrInvalidRuleset
	}
	rule, ok := r.Rule(name)
	if !ok {
		return nil, fmt.Errorf("%w: rule %q not found", ErrInvalidRuleset, name)
	}
	var b strings.Builder
	if err := (gessRenderer{ruleset: r}).writeRule(&b, rule); err != nil {
		return nil, err
	}
	return formatRenderedGess(b.String())
}

func RenderGessQuery(r *Ruleset, name string) ([]byte, error) {
	if r == nil {
		return nil, ErrInvalidRuleset
	}
	query, ok := r.Query(name)
	if !ok {
		return nil, fmt.Errorf("%w: query %q not found", ErrInvalidRuleset, name)
	}
	var b strings.Builder
	if err := (gessRenderer{ruleset: r}).writeQuery(&b, query); err != nil {
		return nil, err
	}
	return formatRenderedGess(b.String())
}

func RenderGessFunction(r *Ruleset, name string) ([]byte, error) {
	if r == nil {
		return nil, ErrInvalidRuleset
	}
	fn, ok := r.Function(name)
	if !ok {
		return nil, fmt.Errorf("%w: function %q not found", ErrInvalidRuleset, name)
	}
	var b strings.Builder
	if err := (gessRenderer{ruleset: r}).writeFunction(&b, fn); err != nil {
		return nil, err
	}
	if !fn.ExpressionBacked() {
		return []byte(b.String()), nil
	}
	return formatRenderedGess(b.String())
}

type gessRenderer struct {
	ruleset *Ruleset
}

func formatRenderedGess(source string) ([]byte, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil, nil
	}
	return gesssexp.Format("<rendered>", []byte(source+"\n"))
}

func (r gessRenderer) writeSep(b *strings.Builder) {
	if b.Len() > 0 {
		b.WriteString("\n\n")
	}
}

func (r gessRenderer) writeModule(b *strings.Builder, module Module) {
	r.writeSep(b)
	fmt.Fprintf(b, "(defmodule %s", module.Name())
	if value, ok := module.AutoFocusDefault(); ok {
		fmt.Fprintf(b, " (declare (auto-focus %s))", renderBool(value))
	}
	b.WriteByte(')')
}

func (r gessRenderer) writeGlobal(b *strings.Builder, global Global) {
	r.writeSep(b)
	fmt.Fprintf(b, "(defglobal *%s* (type %s)", global.Name(), renderKind(global.Kind()))
	if value, ok := global.Default(); ok {
		fmt.Fprintf(b, " (default %s)", renderGessValue(value))
	}
	if global.Description() != "" {
		fmt.Fprintf(b, " (description %s)", strconv.Quote(global.Description()))
	}
	b.WriteByte(')')
}

func (r gessRenderer) writeFunction(b *strings.Builder, fn PureFunctionDefinition) error {
	expr, ok := fn.Expression()
	if !ok {
		r.writeSep(b)
		fmt.Fprintf(b, "; go-function: %s\n", fn.Name())
		return nil
	}
	r.writeSep(b)
	fmt.Fprintf(b, "(deffunction %s", fn.Name())
	params := fn.ParamNames()
	kinds := fn.Args()
	for i, kind := range kinds {
		name := fmt.Sprintf("arg%d", i)
		if i < len(params) && params[i] != "" {
			name = params[i]
		}
		fmt.Fprintf(b, " (param ?%s %s)", name, renderKind(kind))
	}
	fmt.Fprintf(b, " (return %s)", renderKind(fn.Return()))
	if fn.Description() != "" {
		fmt.Fprintf(b, " (description %s)", strconv.Quote(fn.Description()))
	}
	rendered, err := r.renderExpr(expr, "")
	if err != nil {
		return err
	}
	fmt.Fprintf(b, " %s)", rendered)
	return nil
}

func (r gessRenderer) writeTemplate(b *strings.Builder, template Template) {
	r.writeSep(b)
	if source := strings.TrimSpace(template.GessSource()); source != "" {
		b.WriteString(source)
		return
	}
	fmt.Fprintf(b, "(deftemplate %s", renderQualifiedName(template.Module(), template.Name()))
	decls := make([]string, 0, 3)
	if template.BackchainReactive() {
		decls = append(decls, "(backchain-reactive TRUE)")
	}
	switch template.DuplicatePolicy() {
	case DuplicateStructural:
		decls = append(decls, "(duplicate-policy structural)")
	case DuplicateUniqueKey:
		decls = append(decls, "(duplicate-policy unique-key)")
	case DuplicateAllow:
	}
	if keys := template.DuplicateKeys(); len(keys) > 0 {
		decls = append(decls, "(duplicate-key "+strings.Join(keys, " ")+")")
	}
	if len(decls) > 0 {
		b.WriteString(" (declare ")
		b.WriteString(strings.Join(decls, " "))
		b.WriteByte(')')
	}
	for _, field := range template.Fields() {
		fmt.Fprintf(b, " (slot %s (type %s)", field.Name, renderKind(field.Kind))
		if field.Required {
			b.WriteString(" (required TRUE)")
		}
		if field.HasDefault {
			value, err := NewValue(field.Default)
			if err == nil {
				fmt.Fprintf(b, " (default %s)", renderGessValue(value))
			}
		}
		if len(field.AllowedValues) > 0 {
			rendered := make([]string, 0, len(field.AllowedValues))
			for _, allowed := range field.AllowedValues {
				value, err := NewValue(allowed)
				if err != nil {
					continue
				}
				rendered = append(rendered, renderGessValue(value))
			}
			if len(rendered) > 0 {
				fmt.Fprintf(b, " (allowed-values %s)", strings.Join(rendered, " "))
			}
		}
		b.WriteByte(')')
	}
	b.WriteByte(')')
}

func (r gessRenderer) writeRule(b *strings.Builder, rule Rule) error {
	r.writeSep(b)
	if source := strings.TrimSpace(rule.GessSource()); source != "" {
		b.WriteString(source)
		return nil
	}
	fmt.Fprintf(b, "(defrule %s", renderQualifiedName(rule.Module(), rule.Name()))
	if rule.Description() != "" {
		fmt.Fprintf(b, " (description %s)", strconv.Quote(rule.Description()))
	}
	decls := make([]string, 0, 2)
	if rule.Salience() != 0 {
		decls = append(decls, fmt.Sprintf("(salience %d)", rule.Salience()))
	}
	if value, ok := rule.AutoFocus(); ok {
		decls = append(decls, fmt.Sprintf("(auto-focus %s)", renderBool(value)))
	}
	if len(decls) > 0 {
		b.WriteString(" (declare ")
		b.WriteString(strings.Join(decls, " "))
		b.WriteByte(')')
	}
	if err := r.writeConditionTreeForms(b, rule.ConditionTree()); err != nil {
		return err
	}
	b.WriteString(" =>")
	for _, action := range rule.Actions() {
		rendered, err := r.renderRuleAction(action)
		if err != nil {
			return err
		}
		b.WriteByte(' ')
		b.WriteString(rendered)
	}
	b.WriteByte(')')
	return nil
}

func (r gessRenderer) writeQuery(b *strings.Builder, query Query) error {
	r.writeSep(b)
	if source := strings.TrimSpace(query.GessSource()); source != "" {
		b.WriteString(source)
		return nil
	}
	fmt.Fprintf(b, "(defquery %s", renderQualifiedName(query.Module(), query.Name()))
	if query.Description() != "" {
		fmt.Fprintf(b, " (description %s)", strconv.Quote(query.Description()))
	}
	if params := query.Parameters(); len(params) > 0 {
		b.WriteString(" (declare (variables")
		for _, param := range params {
			fmt.Fprintf(b, " ?%s", param.Name())
		}
		b.WriteString("))")
	}
	if err := r.writeConditionTreeForms(b, query.ConditionTree()); err != nil {
		return err
	}
	returns := query.Returns()
	if len(returns) > 0 {
		b.WriteString(" (return")
		for _, ret := range returns {
			if ret.Fact() {
				fmt.Fprintf(b, " (%s ?%s)", ret.Alias(), ret.Binding())
				continue
			}
			rendered, err := r.renderExpr(ret.Expression(), "")
			if err != nil {
				return err
			}
			fmt.Fprintf(b, " (%s %s)", ret.Alias(), rendered)
		}
		b.WriteByte(')')
	}
	b.WriteByte(')')
	return nil
}

func (r gessRenderer) writeConditionTreeForms(b *strings.Builder, tree RuleConditionTree) error {
	forms, err := r.renderConditionTree(tree)
	if err != nil {
		return err
	}
	for _, form := range forms {
		b.WriteByte(' ')
		b.WriteString(form)
	}
	return nil
}

func (r gessRenderer) renderConditionTree(tree RuleConditionTree) ([]string, error) {
	switch tree.Kind() {
	case ConditionTreeKindUnknown:
		return nil, nil
	case ConditionTreeKindAnd:
		var forms []string
		for _, child := range tree.Children() {
			rendered, err := r.renderConditionTree(child)
			if err != nil {
				return nil, err
			}
			forms = append(forms, rendered...)
		}
		return forms, nil
	case ConditionTreeKindMatch:
		match, ok := tree.Match()
		if !ok {
			return nil, nil
		}
		return r.renderMatchForms(match)
	case ConditionTreeKindTest:
		expr, ok := tree.Test()
		if !ok {
			return nil, nil
		}
		rendered, err := r.renderExpr(expr, "")
		if err != nil {
			return nil, err
		}
		return []string{"(test " + rendered + ")"}, nil
	case ConditionTreeKindNot, ConditionTreeKindExists:
		children := tree.Children()
		if len(children) != 1 {
			return nil, fmt.Errorf("%w: %s expects one child", ErrInvalidRuleset, tree.Kind())
		}
		child, err := r.renderConditionAsSingle(children[0])
		if err != nil {
			return nil, err
		}
		return []string{fmt.Sprintf("(%s %s)", tree.Kind(), child)}, nil
	case ConditionTreeKindOr:
		parts := make([]string, 0, len(tree.Children()))
		for _, child := range tree.Children() {
			rendered, err := r.renderConditionAsSingle(child)
			if err != nil {
				return nil, err
			}
			parts = append(parts, rendered)
		}
		return []string{"(or " + strings.Join(parts, " ") + ")"}, nil
	case ConditionTreeKindForall:
		children := tree.Children()
		if len(children) != 2 {
			return nil, fmt.Errorf("%w: forall expects two children", ErrInvalidRuleset)
		}
		domain, err := r.renderConditionAsSingle(children[0])
		if err != nil {
			return nil, err
		}
		requirement, err := r.renderConditionAsSingle(children[1])
		if err != nil {
			return nil, err
		}
		return []string{"(forall " + domain + " " + requirement + ")"}, nil
	case ConditionTreeKindAccumulate:
		aggregate, ok := tree.Aggregate()
		if !ok {
			return nil, fmt.Errorf("%w: missing accumulate payload", ErrInvalidRuleset)
		}
		rendered, err := r.renderAccumulate(aggregate)
		if err != nil {
			return nil, err
		}
		return []string{rendered}, nil
	default:
		return nil, fmt.Errorf("%w: unsupported condition tree kind %q", ErrInvalidRuleset, tree.Kind())
	}
}

func (r gessRenderer) renderConditionAsSingle(tree RuleConditionTree) (string, error) {
	forms, err := r.renderConditionTree(tree)
	if err != nil {
		return "", err
	}
	if len(forms) == 1 {
		return forms[0], nil
	}
	return "(and " + strings.Join(forms, " ") + ")", nil
}

func (r gessRenderer) renderMatchForms(condition RuleCondition) ([]string, error) {
	binding := condition.Binding()
	needsBinding := binding != ""
	tests := make([]string, 0)
	slots := make([]string, 0)
	for _, constraint := range condition.FieldConstraints() {
		field, ok := renderTopLevelField(constraint.Field, constraint.Path)
		if !ok {
			return nil, fmt.Errorf("%w: nested field constraints cannot be rendered to .gess yet", ErrInvalidPath)
		}
		if constraint.Operator == FieldConstraintEqual {
			slots = append(slots, fmt.Sprintf("(%s %s)", field, renderGessValue(constraint.Value)))
			continue
		}
		needsBinding = true
		expr := renderFieldCompareOp(constraint.Operator)
		if expr == "" {
			return nil, fmt.Errorf("%w: unsupported field constraint operator %q", ErrInvalidRuleset, constraint.Operator)
		}
		tests = append(tests, fmt.Sprintf("(test (%s ?%s:%s %s))", expr, binding, field, renderGessValue(constraint.Value)))
	}
	for _, join := range condition.JoinConstraints() {
		field, ok := renderTopLevelField(join.Field, join.Path)
		if !ok {
			return nil, fmt.Errorf("%w: nested join paths cannot be rendered to .gess yet", ErrInvalidPath)
		}
		refField, ok := renderTopLevelField(join.Ref.Field, join.Ref.Path)
		if !ok {
			return nil, fmt.Errorf("%w: nested join paths cannot be rendered to .gess yet", ErrInvalidPath)
		}
		ref := fmt.Sprintf("?%s:%s", join.Ref.Binding, refField)
		if join.Operator == FieldConstraintEqual {
			slots = append(slots, fmt.Sprintf("(%s %s)", field, ref))
			continue
		}
		needsBinding = true
		op := renderFieldCompareOp(join.Operator)
		if op == "" {
			return nil, fmt.Errorf("%w: unsupported join operator %q", ErrInvalidRuleset, join.Operator)
		}
		tests = append(tests, fmt.Sprintf("(test (%s ?%s:%s %s))", op, binding, field, ref))
	}
	for _, predicate := range condition.Predicates() {
		if slot, ok, err := r.renderPredicateSlot(predicate.Expression(), binding); err != nil {
			return nil, err
		} else if ok {
			slots = append(slots, slot)
			continue
		}
		rendered, err := r.renderExpr(predicate.Expression(), binding)
		if err != nil {
			return nil, err
		}
		tests = append(tests, "(test "+rendered+")")
	}
	for _, pattern := range condition.ListPatterns() {
		_ = pattern
		return nil, fmt.Errorf("%w: list patterns cannot be rendered to .gess yet", ErrInvalidListPattern)
	}
	target, err := r.renderConditionTarget(condition)
	if err != nil {
		return nil, err
	}
	sort.Strings(slots)
	pattern := "(" + target
	if len(slots) > 0 {
		pattern += " " + strings.Join(slots, " ")
	}
	pattern += ")"
	if needsBinding && binding != "" {
		pattern = "?" + binding + " <- " + pattern
	}
	return append([]string{pattern}, tests...), nil
}

func (r gessRenderer) renderPredicateSlot(spec ExpressionSpec, currentBinding string) (string, bool, error) {
	var expr CompareExpr
	switch typed := spec.(type) {
	case CompareExpr:
		expr = typed
	case *CompareExpr:
		if typed == nil {
			return "", false, nil
		}
		expr = *typed
	default:
		return "", false, nil
	}
	if expr.Operator != ExpressionCompareEqual {
		return "", false, nil
	}
	field, ok := currentFieldName(expr.Left)
	value := expr.Right
	if !ok {
		field, ok = currentFieldName(expr.Right)
		value = expr.Left
	}
	if !ok {
		return "", false, nil
	}
	rendered, err := r.renderExpr(value, currentBinding)
	if err != nil {
		return "", false, err
	}
	return fmt.Sprintf("(%s %s)", field, rendered), true, nil
}

func currentFieldName(spec ExpressionSpec) (string, bool) {
	switch expr := spec.(type) {
	case CurrentFieldExpr:
		if !pathIsZero(expr.Path) && !pathTopLevel(expr.Path) {
			return "", false
		}
		field, ok := renderTopLevelField(expr.Field, expr.Path)
		return field, ok
	case *CurrentFieldExpr:
		if expr == nil {
			return "", false
		}
		return currentFieldName(CurrentFieldExpr(*expr))
	default:
		return "", false
	}
}

func (r gessRenderer) renderConditionTarget(condition RuleCondition) (string, error) {
	if key := condition.TemplateKey(); key != "" {
		template, ok := r.ruleset.TemplateByKey(key)
		if !ok {
			return "", fmt.Errorf("%w: template key %q not found", ErrInvalidRuleset, key)
		}
		return renderQualifiedName(template.Module(), template.Name()), nil
	}
	if name := condition.Name(); name != "" {
		return name, nil
	}
	return "", fmt.Errorf("%w: condition target is missing", ErrInvalidRuleset)
}

func (r gessRenderer) renderAccumulate(condition AccumulateCondition) (string, error) {
	inputTree, err := conditionSpecToTree(condition.Input)
	if err != nil {
		return "", err
	}
	input, err := r.renderConditionAsSingle(inputTree)
	if err != nil {
		return "", err
	}
	parts := []string{"(accumulate", input}
	for _, spec := range condition.Specs {
		call := "(" + string(spec.Kind())
		if spec.Kind() != AggregateCount {
			expr, err := r.renderExpr(spec.Expression(), "")
			if err != nil {
				return "", err
			}
			call += " " + expr
		}
		call += ")"
		parts = append(parts, fmt.Sprintf("(bind ?%s %s)", spec.Binding(), call))
	}
	return strings.Join(parts, " ") + ")", nil
}

func conditionSpecToTree(spec ConditionSpec) (RuleConditionTree, error) {
	switch condition := spec.(type) {
	case And:
		children := make([]RuleConditionTree, 0, len(condition.Conditions))
		for _, child := range condition.Conditions {
			tree, err := conditionSpecToTree(child)
			if err != nil {
				return RuleConditionTree{}, err
			}
			children = append(children, tree)
		}
		return RuleConditionTree{KindValue: ConditionTreeKindAnd, ChildrenValue: children}, nil
	case *And:
		if condition == nil {
			return RuleConditionTree{}, fmt.Errorf("%w: nil aggregate input", ErrInvalidRuleset)
		}
		return conditionSpecToTree(And(*condition))
	case Match:
		return matchSpecToTree(RuleConditionSpec(condition))
	case *Match:
		if condition == nil {
			return RuleConditionTree{}, fmt.Errorf("%w: nil aggregate input", ErrInvalidRuleset)
		}
		return matchSpecToTree(RuleConditionSpec(*condition))
	default:
		return RuleConditionTree{}, fmt.Errorf("%w: unsupported aggregate input condition %T", ErrInvalidRuleset, spec)
	}
}

func matchSpecToTree(spec RuleConditionSpec) (RuleConditionTree, error) {
	compiled := RuleCondition{
		BindingName: spec.Binding,
		SourceSpan:  spec.Source,
	}
	target := spec.Target.Normalized()
	switch target.Kind() {
	case FactTargetDynamic:
		compiled.NameValue = target.Ref().Name
	case FactTargetTemplate:
		compiled.NameValue = target.Ref().Name
	case FactTargetTemplateKey:
		compiled.TemplateKeyValue = target.TemplateKey()
	}
	for _, constraint := range spec.FieldConstraints {
		value, err := NewValue(constraint.Value)
		if err != nil {
			return RuleConditionTree{}, err
		}
		compiled.FieldConstraintValues = append(compiled.FieldConstraintValues, FieldConstraint{
			Field:    constraint.Field,
			Path:     clonePathSpec(constraint.Path),
			Operator: constraint.Operator,
			Value:    value,
		})
	}
	for _, join := range spec.JoinConstraints {
		compiled.JoinConstraintValues = append(compiled.JoinConstraintValues, JoinConstraint{
			Field:    join.Field,
			Path:     clonePathSpec(join.Path),
			Operator: join.Operator,
			Ref:      cloneFieldRef(join.Ref),
		})
	}
	for i, predicate := range spec.Predicates {
		compiled.PredicateValues = append(compiled.PredicateValues, ExpressionPredicate{
			ExpressionSpec: cloneExpressionSpec(predicate),
			Order:          i,
			SourceSpan:     spec.Source,
		})
	}
	return RuleConditionTree{KindValue: ConditionTreeKindMatch, MatchCondition: compiled, HasMatch: true}, nil
}

func (r gessRenderer) renderRuleAction(action RuleAction) (string, error) {
	def, ok := r.ruleset.Action(action.Name())
	if !ok {
		return "", fmt.Errorf("%w: action %q not found", ErrInvalidRuleset, action.Name())
	}
	if native, ok := def.AssertTemplateValues(); ok {
		return r.renderAssertTemplateValues(native)
	}
	if source := def.GessSource(); source != "" {
		return source, nil
	}
	return "(call " + def.Name() + ")", nil
}

func (r gessRenderer) renderAssertTemplateValues(spec *AssertTemplateValuesActionSpec) (string, error) {
	template, ok := r.ruleset.TemplateByKey(spec.TemplateKey)
	if !ok {
		return "", fmt.Errorf("%w: template key %q not found", ErrInvalidRuleset, spec.TemplateKey)
	}
	fields := template.Fields()
	sort.Slice(fields, func(i, j int) bool { return fields[i].Name < fields[j].Name })
	if len(fields) != len(spec.Values) {
		return "", fmt.Errorf("%w: native assert action arity mismatch", ErrInvalidRuleset)
	}
	parts := []string{"(assert (" + renderQualifiedName(template.Module(), template.Name())}
	for i, field := range fields {
		value, err := r.renderExpr(spec.Values[i], "")
		if err != nil {
			return "", err
		}
		parts = append(parts, fmt.Sprintf("(%s %s)", field.Name, value))
	}
	return strings.Join(parts, " ") + "))", nil
}

func (r gessRenderer) renderExpr(spec ExpressionSpec, currentBinding string) (string, error) {
	switch expr := spec.(type) {
	case nil:
		return "", fmt.Errorf("%w: expression is nil", ErrInvalidRuleset)
	case ConstExpr:
		value, err := NewValue(expr.Value)
		if err != nil {
			return "", err
		}
		return renderGessValue(value), nil
	case *ConstExpr:
		if expr == nil {
			return "", fmt.Errorf("%w: expression is nil", ErrInvalidRuleset)
		}
		return r.renderExpr(ConstExpr(*expr), currentBinding)
	case CurrentFieldExpr:
		if currentBinding == "" {
			return "", fmt.Errorf("%w: current-field expression requires a condition binding", ErrInvalidRuleset)
		}
		return renderProjection(currentBinding, expr.Field, expr.Path)
	case *CurrentFieldExpr:
		if expr == nil {
			return "", fmt.Errorf("%w: expression is nil", ErrInvalidRuleset)
		}
		return r.renderExpr(CurrentFieldExpr(*expr), currentBinding)
	case BindingFieldExpr:
		return renderProjection(expr.Binding, expr.Field, expr.Path)
	case *BindingFieldExpr:
		if expr == nil {
			return "", fmt.Errorf("%w: expression is nil", ErrInvalidRuleset)
		}
		return r.renderExpr(BindingFieldExpr(*expr), currentBinding)
	case BindingValueExpr:
		return "?" + expr.Binding, nil
	case *BindingValueExpr:
		if expr == nil {
			return "", fmt.Errorf("%w: expression is nil", ErrInvalidRuleset)
		}
		return r.renderExpr(BindingValueExpr(*expr), currentBinding)
	case ParamExpr:
		return "?" + expr.Name, nil
	case *ParamExpr:
		if expr == nil {
			return "", fmt.Errorf("%w: expression is nil", ErrInvalidRuleset)
		}
		return r.renderExpr(ParamExpr(*expr), currentBinding)
	case GlobalExpr:
		return "*" + expr.Name + "*", nil
	case *GlobalExpr:
		if expr == nil {
			return "", fmt.Errorf("%w: expression is nil", ErrInvalidRuleset)
		}
		return r.renderExpr(GlobalExpr(*expr), currentBinding)
	case CallExpr:
		args := make([]string, 0, len(expr.Args))
		for _, arg := range expr.Args {
			rendered, err := r.renderExpr(arg, currentBinding)
			if err != nil {
				return "", err
			}
			args = append(args, rendered)
		}
		return "(" + expr.Name + joinTail(args) + ")", nil
	case *CallExpr:
		if expr == nil {
			return "", fmt.Errorf("%w: expression is nil", ErrInvalidRuleset)
		}
		return r.renderExpr(CallExpr(*expr), currentBinding)
	case CompareExpr:
		left, err := r.renderExpr(expr.Left, currentBinding)
		if err != nil {
			return "", err
		}
		right, err := r.renderExpr(expr.Right, currentBinding)
		if err != nil {
			return "", err
		}
		return "(" + renderCompareOp(expr.Operator) + " " + left + " " + right + ")", nil
	case *CompareExpr:
		if expr == nil {
			return "", fmt.Errorf("%w: expression is nil", ErrInvalidRuleset)
		}
		return r.renderExpr(CompareExpr(*expr), currentBinding)
	case BooleanExpr:
		args := make([]string, 0, len(expr.Operands))
		for _, operand := range expr.Operands {
			rendered, err := r.renderExpr(operand, currentBinding)
			if err != nil {
				return "", err
			}
			args = append(args, rendered)
		}
		return "(" + string(expr.Operator) + joinTail(args) + ")", nil
	case *BooleanExpr:
		if expr == nil {
			return "", fmt.Errorf("%w: expression is nil", ErrInvalidRuleset)
		}
		return r.renderExpr(BooleanExpr(*expr), currentBinding)
	default:
		return "", fmt.Errorf("%w: unsupported expression %T", ErrInvalidRuleset, spec)
	}
}

func renderProjection(binding, field string, path PathSpec) (string, error) {
	resolved, ok := renderTopLevelField(field, path)
	if !ok {
		return "", fmt.Errorf("%w: nested paths cannot be rendered to .gess yet", ErrInvalidPath)
	}
	if binding == "" || resolved == "" {
		return "", fmt.Errorf("%w: projection requires binding and field", ErrInvalidRuleset)
	}
	return "?" + binding + ":" + resolved, nil
}

func renderTopLevelField(field string, path PathSpec) (string, bool) {
	if pathIsZero(path) {
		return field, strings.TrimSpace(field) != ""
	}
	if !pathTopLevel(path) {
		return "", false
	}
	return pathRoot(path), true
}

func joinTail(items []string) string {
	if len(items) == 0 {
		return ""
	}
	return " " + strings.Join(items, " ")
}

func renderQualifiedName(module ModuleName, name string) string {
	module = normalizeModuleName(module)
	if module == MainModule {
		return name
	}
	return module.String() + "::" + name
}

func renderKind(kind ValueKind) string {
	switch kind {
	case ValueString:
		return "STRING"
	case ValueInt:
		return "INTEGER"
	case ValueFloat:
		return "FLOAT"
	case ValueBool:
		return "BOOLEAN"
	case ValueList:
		return "LIST"
	case ValueMap:
		return "MAP"
	case ValueAny:
		return "ANY"
	case ValueNull:
		return "NULL"
	default:
		return "ANY"
	}
}

func renderBool(value bool) string {
	if value {
		return "TRUE"
	}
	return "FALSE"
}

func renderGessValue(value Value) string {
	switch value.Kind() {
	case ValueNull:
		return "NULL"
	case ValueBool:
		v, _ := value.AsBool()
		return renderBool(v)
	case ValueInt:
		v, _ := value.AsInt64()
		return strconv.FormatInt(v, 10)
	case ValueFloat:
		v, _ := value.AsFloat64()
		return strconv.FormatFloat(v, 'g', -1, 64)
	case ValueString:
		v, _ := value.AsString()
		return strconv.Quote(v)
	default:
		return strconv.Quote(value.String())
	}
}

func renderCompareOp(op ExpressionComparisonOperator) string {
	switch op {
	case ExpressionCompareEqual:
		return "="
	case ExpressionCompareNotEqual:
		return "!="
	case ExpressionCompareLessThan:
		return "<"
	case ExpressionCompareLessOrEqual:
		return "<="
	case ExpressionCompareGreaterThan:
		return ">"
	case ExpressionCompareGreaterOrEqual:
		return ">="
	default:
		return ""
	}
}

func renderFieldCompareOp(op FieldConstraintOperator) string {
	switch op {
	case FieldConstraintEqual:
		return "="
	case FieldConstraintNotEqual:
		return "!="
	case FieldConstraintLessThan:
		return "<"
	case FieldConstraintLessOrEqual:
		return "<="
	case FieldConstraintGreaterThan:
		return ">"
	case FieldConstraintGreaterOrEqual:
		return ">="
	default:
		return ""
	}
}
