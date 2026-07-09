package engine

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"

	gessrules "github.com/cpcf/gess/rules"
)

type ActionFunc = gessrules.ActionFunc
type ActionContext = gessrules.ActionContext
type DSLCallFunc = gessrules.DSLCallFunc

const inlineActionContextBindingSnapshots = 2

type actionContextBindingState struct {
	mu              sync.Mutex
	conditions      []RuleCondition
	conditionPlans  []compiledConditionPlan
	token           tokenRef
	entries         []bindingTupleEntry
	snapshots       []FactSnapshot
	inlineSnapshots [inlineActionContextBindingSnapshots]FactSnapshot
}

type actionContext struct {
	ctx            context.Context
	session        *Session
	sessionID      SessionID
	rulesetID      RulesetID
	activationID   ActivationID
	activationKey  candidateIdentityKey
	activationOrd  uint64
	ruleID         RuleID
	ruleRevisionID RuleRevisionID
	generation     Generation
	bindings       *actionContextBindingState
	rhsBinds       *rhsBindStore
	actionName     string
	actionIndex    int
}

// rhsBindStore holds the RHS-local variables produced by bind actions. It is
// created fresh per activation and shared across that firing's actions through
// the ActionContext pointer, so a later action reads what an earlier bind wrote.
type rhsBindStore struct {
	values map[string]Value
}

// SetRHSBind records an RHS-local bound value for the current firing.
func (c actionContext) SetRHSBind(name string, value Value) {
	if c.rhsBinds == nil || name == "" {
		return
	}
	if c.rhsBinds.values == nil {
		c.rhsBinds.values = make(map[string]Value, 4)
	}
	c.rhsBinds.values[name] = cloneValue(value)
}

// RHSBind returns an RHS-local bound value recorded earlier in this firing.
func (c actionContext) RHSBind(name string) (Value, bool) {
	if c.rhsBinds == nil || c.rhsBinds.values == nil {
		return Value{}, false
	}
	value, ok := c.rhsBinds.values[name]
	if !ok {
		return Value{}, false
	}
	return cloneValue(value), true
}

func newActionContext(ctx context.Context, session *Session, activation activation, entries []bindingTupleEntry) actionContext {
	if ctx == nil {
		ctx = context.Background()
	}

	out := actionContext{
		ctx:            ctx,
		session:        session,
		activationKey:  activation.identityKey,
		activationOrd:  activation.key.ordinal,
		ruleRevisionID: activation.ruleRevisionID,
		generation:     activation.Generation(),
	}
	if len(entries) > 0 {
		out.bindings = &actionContextBindingState{
			entries: entries,
		}
	}
	if session != nil {
		out.sessionID = session.id
		if session.revision != nil {
			out.rulesetID = session.revision.ID()
		}
	}
	return out
}

func newTokenActionContext(ctx context.Context, session *Session, activation activation, rule compiledRule) actionContext {
	out := newActionContext(ctx, session, activation, nil)
	out.ruleID = rule.id
	if !activation.token.isZero() {
		bindings := &actionContextBindingState{}
		bindings.resetToken(rule, activation.token)
		out.bindings = bindings
	}
	return out
}

func newTokenActionContextWithBindingState(ctx context.Context, session *Session, activation activation, rule compiledRule, bindings *actionContextBindingState) actionContext {
	out := newActionContext(ctx, session, activation, nil)
	out.ruleID = rule.id
	if !activation.token.isZero() && bindings != nil {
		bindings.resetToken(rule, activation.token)
		out.bindings = bindings
	}
	return out
}

func (c actionContext) Context() context.Context {
	if c.ctx != nil {
		return c.ctx
	}
	return context.Background()
}

func (c actionContext) SessionID() SessionID {
	return c.sessionID
}

func (c actionContext) RulesetID() RulesetID {
	return c.rulesetID
}

func (c actionContext) ActivationID() ActivationID {
	if !c.activationID.IsZero() {
		return c.activationID
	}
	return activationIDForIdentityKey(c.activationKey, c.activationOrd)
}

func (c actionContext) RuleID() RuleID {
	return c.ruleID
}

func (c actionContext) RuleRevisionID() RuleRevisionID {
	return c.ruleRevisionID
}

func (c actionContext) Generation() Generation {
	return c.generation
}

func (c actionContext) BoundFacts() []FactSnapshot {
	if c.bindings == nil || c.bindings.len() == 0 {
		return nil
	}
	if err := c.materializeAllBindings(); err != nil {
		return nil
	}
	out := make([]FactSnapshot, 0, c.bindings.len())
	for i := range c.bindings.len() {
		entry := c.bindings.entryAt(i)
		if entry.hasValue {
			continue
		}
		if i < len(c.bindings.snapshots) {
			out = append(out, c.bindings.snapshots[i])
		}
	}
	return out
}

func (c actionContext) Binding(name string) (FactSnapshot, bool) {
	if name == "" || c.bindings == nil {
		return FactSnapshot{}, false
	}
	if index, ok := c.bindings.bindingIndex(name); ok {
		return c.materializeBinding(index)
	}
	return FactSnapshot{}, false
}

// BindingID returns the FactID of a bound fact without materializing a public
// snapshot. Unlike Binding, it declares no whole-snapshot read, so it composes
// with field reads on the same binding (used by modify/retract to target a
// fact while its sibling fields are read as scalars).
func (c actionContext) BindingID(name string) (FactID, bool) {
	if name == "" || c.bindings == nil {
		return FactID{}, false
	}
	index, ok := c.bindings.bindingIndex(name)
	if !ok {
		return FactID{}, false
	}
	entry := c.bindings.entryAt(index)
	if entry.hasValue || entry.factID.IsZero() {
		return FactID{}, false
	}
	return entry.factID, true
}

func (c actionContext) BindingValue(name string) (Value, bool) {
	if name == "" || c.bindings == nil {
		return Value{}, false
	}
	index, ok := c.bindings.bindingIndex(name)
	if !ok {
		return Value{}, false
	}
	entry := c.bindings.entryAt(index)
	if !entry.hasValue {
		return Value{}, false
	}
	return cloneValue(entry.value), true
}

// BindingScalarValue returns one scalar field from a fixed-template bound fact
// without materializing a public FactSnapshot.
func (c actionContext) BindingScalarValue(name, field string) (Value, bool) {
	return c.bindingScalarValue(name, field)
}

func (c actionContext) Global(name string) (Value, bool) {
	if c.session == nil || c.session.revision == nil {
		return Value{}, false
	}
	global, ok := c.session.revision.globals[strings.TrimSpace(name)]
	if !ok || global.slot < 0 || global.slot >= len(c.session.globalValues) {
		return Value{}, false
	}
	return cloneValue(c.session.globalValues[global.slot]), true
}

func (c actionContext) bindingScalarValue(name, field string) (Value, bool) {
	if name == "" || field == "" || c.bindings == nil {
		return Value{}, false
	}
	index, ok := c.bindings.bindingIndex(name)
	if !ok {
		return Value{}, false
	}

	c.bindings.mu.Lock()
	defer c.bindings.mu.Unlock()
	return c.bindingScalarValueLocked(index, field)
}

func (c actionContext) bindingScalarValueAt(bindingSlot int, field string) (Value, bool) {
	if field == "" || c.bindings == nil || bindingSlot < 0 || bindingSlot >= c.bindings.len() {
		return Value{}, false
	}
	c.bindings.mu.Lock()
	defer c.bindings.mu.Unlock()
	return c.bindingScalarValueLocked(bindingSlot, field)
}

func (c actionContext) bindingScalarValueAtSlot(bindingSlot, fieldSlot int) (Value, bool) {
	if c.bindings == nil || bindingSlot < 0 || bindingSlot >= c.bindings.len() || fieldSlot < 0 {
		return Value{}, false
	}
	if len(c.bindings.snapshots) > bindingSlot {
		if snapshot := c.bindings.snapshots[bindingSlot]; !snapshot.id.IsZero() {
			if fieldSlot >= len(snapshot.fieldSlots) {
				return Value{}, false
			}
			resolved := snapshot.fieldSlots[fieldSlot]
			if !resolved.ok || !valueShareable(resolved.value) {
				return Value{}, false
			}
			return resolved.value, true
		}
	}
	return c.bindingScalarValueLiveAtSlot(bindingSlot, fieldSlot)
}

// assertByName asserts an untemplated (dynamic) fact. It is engine-internal
// plumbing — dynamic facts are not a public concept — retained for query
// triggers and white-box tests. Public callers use Assert.
func (c actionContext) assertByName(name string, fields Fields) (AssertResult, error) {
	if c.session == nil {
		return AssertResult{Status: AssertClosed}, ErrClosedSession
	}
	return c.session.insertFactWithContextAndOrigin(c.Context(), name, "", fields, c.mutationOrigin())
}

func (c actionContext) Assert(templateKey TemplateKey, fields Fields) (AssertResult, error) {
	if c.session == nil {
		return AssertResult{Status: AssertClosed}, ErrClosedSession
	}
	return c.session.insertFactWithContextAndOrigin(c.Context(), "", templateKey, fields, c.mutationOrigin())
}

// AssertLogical asserts a logically-supported fact of the given template,
// justified by the firing activation's matched facts. Retracting the
// supporting facts cascades the retraction of this fact.
func (c actionContext) AssertLogical(templateKey TemplateKey, fields Fields) (AssertResult, error) {
	if c.session == nil {
		return AssertResult{Status: AssertClosed}, ErrClosedSession
	}
	if c.RuleRevisionID().IsZero() || c.ActivationID().IsZero() {
		return AssertResult{Status: AssertValidationFailure}, ErrLogicalSupportUnavailable
	}
	if err := c.materializeAllBindings(); err != nil {
		return AssertResult{Status: AssertValidationFailure}, err
	}
	return c.session.insertLogicalFactWithContextAndOrigin(c.Context(), "", templateKey, fields, c.mutationOrigin(), c.supportingFactIDs())
}

// assertLogicalByName is the untemplated form of AssertLogical, engine-internal
// only (query triggers and white-box tests).
func (c actionContext) assertLogicalByName(name string, fields Fields) (AssertResult, error) {
	if c.session == nil {
		return AssertResult{Status: AssertClosed}, ErrClosedSession
	}
	if c.RuleRevisionID().IsZero() || c.ActivationID().IsZero() {
		return AssertResult{Status: AssertValidationFailure}, ErrLogicalSupportUnavailable
	}
	if err := c.materializeAllBindings(); err != nil {
		return AssertResult{Status: AssertValidationFailure}, err
	}
	return c.session.insertLogicalFactWithContextAndOrigin(c.Context(), name, "", fields, c.mutationOrigin(), c.supportingFactIDs())
}

// AssertTemplateValues asserts a working-memory fact using values in template
// field order and returns only whether the effect succeeded. It preserves fact
// assertion semantics: inserted facts can be matched, queried, modified,
// retracted, logically supported, returned in snapshots, and observed through
// fact assertion events.
func (c actionContext) AssertTemplateValues(templateKey TemplateKey, values ...Value) error {
	if c.session == nil {
		return ErrClosedSession
	}
	return c.session.insertTemplateValuesWithContextAndOrigin(c.Context(), templateKey, values, c.mutationOrigin())
}

func (c actionContext) Modify(id FactID, patch FactPatch) (ModifyResult, error) {
	if c.session == nil {
		return ModifyResult{Status: ModifyClosed}, ErrClosedSession
	}
	if err := c.materializeAllBindings(); err != nil {
		return ModifyResult{Status: ModifyMissing}, err
	}
	return c.session.modifyWithContextAndOrigin(c.Context(), id, patch, c.mutationOrigin())
}

func (c actionContext) Retract(id FactID) (RetractResult, error) {
	if c.session == nil {
		return RetractResult{Status: RetractClosed}, ErrClosedSession
	}
	if err := c.materializeAllBindings(); err != nil {
		return RetractResult{Status: RetractMissing}, err
	}
	return c.session.retractWithContextAndOrigin(c.Context(), id, c.mutationOrigin())
}

func (c actionContext) Halt() error {
	if c.session == nil || c.session.closed {
		return ErrClosedSession
	}
	c.session.requestRunHalt()
	return nil
}

// Emit writes the display forms of values, concatenated, to the session's
// configured output writer. When no writer is configured the output is
// discarded. It is the runtime behind the .gess emit action.
func (c actionContext) Emit(values ...Value) error {
	if c.session == nil {
		return ErrClosedSession
	}
	if c.session.output == nil {
		return nil
	}
	var b strings.Builder
	for _, value := range values {
		b.WriteString(gessDisplayValue(value))
	}
	_, err := io.WriteString(c.session.output, b.String())
	return err
}

func (c actionContext) supportingFactIDs() []FactID {
	if c.bindings == nil || c.bindings.len() == 0 {
		return nil
	}
	out := make([]FactID, 0, c.bindings.len())
	for i := range c.bindings.len() {
		entry := c.bindings.entryAt(i)
		if !entry.factID.IsZero() {
			out = append(out, entry.factID)
		}
	}
	return out
}

func (c actionContext) mutationOrigin() mutationOrigin {
	return mutationOrigin{
		RuleID:                c.ruleID,
		RuleRevisionID:        c.ruleRevisionID,
		ActionName:            c.actionName,
		ActionIndex:           c.actionIndex,
		activationIdentityKey: c.activationKey,
		activationOrdinal:     c.activationOrd,
	}
}

func (c actionContext) materializeAllBindings() error {
	if c.bindings == nil || c.bindings.len() == 0 {
		return nil
	}
	c.bindings.mu.Lock()
	defer c.bindings.mu.Unlock()
	for i := range c.bindings.len() {
		if c.bindings.entryAt(i).hasValue {
			continue
		}
		entry := c.bindings.entryAt(i)
		if entry.factID.IsZero() || entry.factID.Sequence() == 0 {
			continue
		}
		if _, ok := c.materializeBindingLocked(i); !ok {
			return fmt.Errorf("%w: stale fact %q for activation %q", ErrMatcher, entry.factID, c.ActivationID())
		}
	}
	return nil
}

func (c actionContext) materializeBinding(index int) (FactSnapshot, bool) {
	if c.bindings == nil || index < 0 || index >= c.bindings.len() {
		return FactSnapshot{}, false
	}
	c.bindings.mu.Lock()
	defer c.bindings.mu.Unlock()
	return c.materializeBindingLocked(index)
}

func (c actionContext) bindingScalarValueLocked(index int, field string) (Value, bool) {
	if c.bindings == nil || index < 0 || index >= c.bindings.len() {
		return Value{}, false
	}
	if len(c.bindings.snapshots) > index {
		if snapshot := c.bindings.snapshots[index]; !snapshot.id.IsZero() {
			if value, ok := scalarFieldValue(snapshot, field); ok {
				return value, true
			}
			return Value{}, false
		}
	}
	return c.bindingScalarValueLive(index, field)
}

func (c actionContext) bindingScalarValueLive(index int, field string) (Value, bool) {
	if c.bindings == nil || index < 0 || index >= c.bindings.len() {
		return Value{}, false
	}
	if c.session == nil || c.session.revision == nil {
		return Value{}, false
	}
	entry := c.bindings.entryAt(index)
	fact, ok := c.session.workingFactByID(entry.factID)
	if !ok || fact == nil {
		return Value{}, false
	}
	if fact.id.Generation() != c.generation || fact.version != entry.factVersion {
		return Value{}, false
	}
	template, ok := fact.templateForRevision(c.session.revision)
	if !ok || !template.closed {
		return Value{}, false
	}
	slot, ok := template.fieldSlot(field)
	if !ok || slot < 0 {
		return Value{}, false
	}
	value, ok := fact.compiledFieldValue(field, slot, c.session.compactSlotStore)
	if !ok || !valueShareable(value) {
		return Value{}, false
	}
	return value, true
}

func (c actionContext) bindingScalarValueLiveAtSlot(index, fieldSlot int) (Value, bool) {
	if c.bindings == nil || index < 0 || index >= c.bindings.len() || fieldSlot < 0 {
		return Value{}, false
	}
	if c.session == nil {
		return Value{}, false
	}
	entry := c.bindings.entryAt(index)
	if entry.factID.Generation() != c.generation {
		return Value{}, false
	}
	value, ok := c.session.factScalarValueAtSlot(entry.factID, entry.factVersion, fieldSlot)
	if !ok || !valueShareable(value) {
		return Value{}, false
	}
	return value, true
}

func (c actionContext) materializeBindingLocked(index int) (FactSnapshot, bool) {
	if len(c.bindings.snapshots) == 0 {
		if c.bindings.len() <= len(c.bindings.inlineSnapshots) {
			c.bindings.snapshots = c.bindings.inlineSnapshots[:c.bindings.len()]
		} else {
			c.bindings.snapshots = make([]FactSnapshot, c.bindings.len())
		}
	}
	if snapshot := c.bindings.snapshots[index]; !snapshot.id.IsZero() {
		return snapshot, true
	}
	if c.session == nil {
		return FactSnapshot{}, false
	}
	entry := c.bindings.entryAt(index)
	if entry.hasValue {
		return FactSnapshot{}, false
	}
	fact, ok := c.session.workingFactByID(entry.factID)
	if !ok {
		return FactSnapshot{}, false
	}
	if fact.id.Generation() != c.generation || fact.version != entry.factVersion {
		return FactSnapshot{}, false
	}
	snapshot := fact.detachedSnapshotForRevision(c.session.revision, c.session.compactSlotStore)
	c.bindings.snapshots[index] = snapshot
	return snapshot, true
}

func (s *actionContextBindingState) bindingIndex(name string) (int, bool) {
	if s == nil {
		return 0, false
	}
	if !s.token.isZero() {
		for i, condition := range s.conditions {
			if condition.BindingName == name {
				return i, true
			}
		}
		return 0, false
	}
	for i, entry := range s.entries {
		if entry.binding == name {
			return i, true
		}
	}
	return 0, false
}

func (s *actionContextBindingState) resetToken(rule compiledRule, token tokenRef) {
	if s == nil {
		return
	}
	s.conditions = rule.conditions
	s.conditionPlans = rule.conditionPlans
	s.token = token
	s.entries = nil
	for i := range s.snapshots {
		s.snapshots[i] = FactSnapshot{}
	}
	s.snapshots = nil
	for i := range s.inlineSnapshots {
		s.inlineSnapshots[i] = FactSnapshot{}
	}
}

func (s *actionContextBindingState) len() int {
	if s == nil {
		return 0
	}
	if !s.token.isZero() {
		return len(s.conditions)
	}
	return len(s.entries)
}

func (s *actionContextBindingState) entryAt(index int) bindingTupleEntry {
	if s == nil || index < 0 {
		return bindingTupleEntry{}
	}
	if !s.token.isZero() {
		match, ok := tokenRefAtSlot(s.token, index)
		if !ok || match.bindingSlot < 0 || match.bindingSlot >= len(s.conditions) {
			return bindingTupleEntry{}
		}
		condition := s.conditions[match.bindingSlot]
		return bindingTupleEntry{
			binding:        condition.BindingName,
			bindingSlot:    match.bindingSlot,
			conditionOrder: condition.Order,
			conditionID:    condition.IDValue,
			factID:         match.fact.ID(),
			factVersion:    match.fact.Version(),
			value:          cloneValue(match.value),
			hasValue:       match.hasValue,
		}
	}
	if index >= len(s.entries) {
		return bindingTupleEntry{}
	}
	return s.entries[index]
}

func scalarFieldValue(fact FactSnapshot, field string) (Value, bool) {
	slot, ok := fact.fieldSlot(field)
	if !ok || slot < 0 {
		return Value{}, false
	}
	if slot < len(fact.fieldSlots) {
		resolved := fact.fieldSlots[slot]
		if resolved.ok && valueShareable(resolved.value) {
			return resolved.value, true
		}
	}
	if slot < len(fact.compactSlots) {
		if value, ok := fact.compactSlots[slot].value(); ok && valueShareable(value) {
			return value, true
		}
	}
	return Value{}, false
}

type ActionSpec = gessrules.ActionSpec

// ActionEffectKind identifies the mutation an [ActionEffectSpec] performs.
type ActionEffectKind = gessrules.ActionEffectKind

const (
	// ActionEffectAssert asserts a fact from Name(Effect.Target as the
	// template/name) with Fields/Values.
	ActionEffectAssert        = gessrules.ActionEffectAssert
	ActionEffectAssertLogical = gessrules.ActionEffectAssertLogical
	ActionEffectModify        = gessrules.ActionEffectModify
	ActionEffectRetract       = gessrules.ActionEffectRetract
	ActionEffectEmit          = gessrules.ActionEffectEmit
	ActionEffectBind          = gessrules.ActionEffectBind
	// Focus-stack and run control. These carry no values; PushFocus uses
	// Target as the module name.
	ActionEffectPushFocus  = gessrules.ActionEffectPushFocus
	ActionEffectPopFocus   = gessrules.ActionEffectPopFocus
	ActionEffectClearFocus = gessrules.ActionEffectClearFocus
	ActionEffectHalt       = gessrules.ActionEffectHalt
)

type ActionEffectSpec = gessrules.ActionEffectSpec

type ActionCallSpec = gessrules.ActionCallSpec

func cloneActionEffectSpec(s *ActionEffectSpec) *ActionEffectSpec {
	return gessrules.CloneActionEffectSpec(s)
}

type ActionBindingReadSetSpec = gessrules.ActionBindingReadSetSpec

type ActionBindingReadSpec = gessrules.ActionBindingReadSpec

// AssertTemplateValuesActionSpec describes a generated rule action that emits
// values in template field order. When the compiler proves the target template
// is not visible to downstream rule matching, queries, modification, retraction,
// logical support, public fact enumeration, or fact events, the runtime treats
// the emitted value as output-only instead of as a working-memory fact.
//
// Output-only emitted values are validated/defaulted and then discarded unless
// an explicit non-fact output consumer exists. They do not receive FactIDs or
// recency, cannot be modified, retracted, logically supported, found by fact ID,
// returned in snapshots, returned by queries, or observed as EventFactAsserted.
// Use Session.AssertTemplateValues or ActionContext.AssertTemplateValues when a
// value must be a Jess-style working-memory fact.
type AssertTemplateValuesActionSpec = gessrules.AssertTemplateValuesActionSpec

func cloneActionBindingReadSetSpec(s *ActionBindingReadSetSpec) *ActionBindingReadSetSpec {
	return gessrules.CloneActionBindingReadSetSpec(s)
}

func cloneActionBindingReadSpec(s ActionBindingReadSpec) ActionBindingReadSpec {
	return gessrules.CloneActionBindingReadSpec(s)
}

func cloneAssertTemplateValuesActionSpec(s *AssertTemplateValuesActionSpec) *AssertTemplateValuesActionSpec {
	return gessrules.CloneAssertTemplateValuesActionSpec(s)
}

func normalizeActionSpec(spec ActionSpec) (ActionSpec, error) {
	normalized := gessrules.CloneActionSpec(spec)
	if normalized.Name == "" {
		return ActionSpec{}, &ValidationError{
			Reason: "action name is required",
		}
	}
	actionKinds := 0
	if normalized.Fn != nil {
		actionKinds++
	}
	if normalized.AssertTemplateValues != nil {
		actionKinds++
		if normalized.AssertTemplateValues.TemplateKey == "" {
			return ActionSpec{}, &ValidationError{
				Reason: "assert template values action requires a template key",
			}
		}
		for i, value := range normalized.AssertTemplateValues.Values {
			if value == nil {
				return ActionSpec{}, &ValidationError{
					Reason:         "assert template values action requires a value expression",
					ActionIndex:    i,
					HasActionIndex: true,
				}
			}
		}
	}
	if normalized.Effect != nil {
		actionKinds++
		for i, value := range normalized.Effect.Values {
			if value == nil {
				return ActionSpec{}, &ValidationError{
					Reason:         "effect action requires a value expression",
					ActionIndex:    i,
					HasActionIndex: true,
				}
			}
		}
	}
	if normalized.Call != nil {
		actionKinds++
		if normalized.Call.Fn == nil {
			return ActionSpec{}, &ValidationError{
				Reason: "call action requires a host function",
			}
		}
		for i, value := range normalized.Call.Args {
			if value == nil {
				return ActionSpec{}, &ValidationError{
					Reason:         "call action requires an argument expression",
					ActionIndex:    i,
					HasActionIndex: true,
				}
			}
		}
	}
	if actionKinds == 0 {
		return ActionSpec{}, &ValidationError{
			Reason: "action function or native action is required",
		}
	}
	if actionKinds > 1 {
		return ActionSpec{}, &ValidationError{
			Reason: "action must declare exactly one action implementation",
		}
	}
	if normalized.BindingReads != nil && normalized.Fn == nil {
		return ActionSpec{}, &ValidationError{
			Reason: "binding reads can only be declared for action functions",
		}
	}
	return normalized, nil
}

type Action = gessrules.Action

func cloneAction(a Action) Action {
	return gessrules.CloneAction(a)
}

type compiledAction struct {
	name                 string
	fn                   ActionFunc
	assertTemplateValues *AssertTemplateValuesActionSpec
	effect               *ActionEffectSpec
	call                 *ActionCallSpec
	bindingReads         *ActionBindingReadSetSpec
	gessSource           string
	order                int
	skipBindingFreeze    bool
}

type compiledRuleActionKind uint8

const (
	compiledRuleActionFunction compiledRuleActionKind = iota
	compiledRuleActionAssertTemplateValues
	compiledRuleActionEffect
	compiledRuleActionCall
)

type compiledRuleAction struct {
	kind                 compiledRuleActionKind
	name                 string
	order                int
	source               SourceSpan
	fn                   ActionFunc
	assertTemplateValues compiledAssertTemplateValuesAction
	effect               compiledEffectAction
	call                 compiledCallAction
	bindingReads         actionBindingReadSet
	skipBindingFreeze    bool
}

// compiledCallAction is the compiled form of an ActionCallSpec: expression-backed
// arguments compiled once, plus the host closure to invoke with the results.
type compiledCallAction struct {
	name string
	fn   DSLCallFunc
	args []compiledExpression
}

// compiledEffectAction is the compiled form of an ActionEffectSpec: its Values
// are compiled to expressions once, evaluated at fire time against the firing's
// frozen bindings.
type compiledEffectAction struct {
	kind        ActionEffectKind
	target      string
	templateKey TemplateKey
	factName    string
	fields      []string
	unset       []string
	values      []compiledExpression
}

type compiledAssertTemplateValuesAction struct {
	template    compiledTemplate
	insertPlan  compiledGeneratedFactInsertPlan
	values      []compiledExpression
	tokenValues []compiledTokenActionValue
}

type compiledTokenActionValueKind uint8

const (
	compiledTokenActionValueGeneric compiledTokenActionValueKind = iota
	compiledTokenActionValueConst
	compiledTokenActionValueBindingField
	compiledTokenActionValueBindingValue
	compiledTokenActionValueStringCall2ConstBindingField
)

type compiledTokenActionValue struct {
	kind        compiledTokenActionValueKind
	value       Value
	bindingSlot int
	access      compiledPathAccess
	function    compiledPureFunction
	expression  compiledExpression
}

type actionBindingReadSet struct {
	known bool
	reads []actionBindingRead
}

type actionBindingRead struct {
	bindingSlot int
	access      compiledPathAccess
	whole       bool
	value       bool
}

func compileDeclaredActionBindingReads(ruleName string, actionIndex int, spec *ActionBindingReadSetSpec, conditions []RuleCondition, bindingSlots map[string]int, templatesByKey map[TemplateKey]compiledTemplate) (actionBindingReadSet, error) {
	if spec == nil {
		return actionBindingReadSet{}, nil
	}
	out := actionBindingReadSet{
		known: true,
		reads: make([]actionBindingRead, 0, len(spec.Reads)),
	}
	for _, declared := range spec.Reads {
		read, err := compileDeclaredActionBindingRead(ruleName, actionIndex, declared, conditions, bindingSlots, templatesByKey)
		if err != nil {
			return actionBindingReadSet{}, err
		}
		out.add(read)
	}
	return out, nil
}

func compileDeclaredActionBindingRead(ruleName string, actionIndex int, spec ActionBindingReadSpec, conditions []RuleCondition, bindingSlots map[string]int, templatesByKey map[TemplateKey]compiledTemplate) (actionBindingRead, error) {
	normalized := cloneActionBindingReadSpec(spec)
	if normalized.Binding == "" {
		return actionBindingRead{}, &ValidationError{
			RuleName:       ruleName,
			ActionIndex:    actionIndex,
			HasActionIndex: true,
			Reason:         "action binding read requires a binding",
		}
	}
	if hasAmbiguousFieldAndPath(normalized.Field, normalized.Path) {
		return actionBindingRead{}, &ValidationError{
			RuleName:       ruleName,
			ActionIndex:    actionIndex,
			HasActionIndex: true,
			FieldName:      normalized.Field,
			Reason:         "action binding read cannot set both field and path",
			Err:            ErrInvalidPath,
		}
	}
	bindingSlot, ok := bindingSlots[normalized.Binding]
	if !ok {
		return actionBindingRead{}, &ValidationError{
			RuleName:       ruleName,
			ActionIndex:    actionIndex,
			HasActionIndex: true,
			Reason:         "action binding read must refer to a rule binding",
		}
	}
	if bindingSlot < 0 || bindingSlot >= len(conditions) {
		return actionBindingRead{}, fmt.Errorf("%w: malformed action binding slot %d", ErrMatcher, bindingSlot)
	}
	path := pathOrField(normalized.Path, normalized.Field)
	if pathIsZero(path) {
		return actionBindingRead{bindingSlot: bindingSlot, whole: true}, nil
	}
	condition := conditions[bindingSlot]
	access := compiledPathAccess{path: clonePathSpec(path), root: pathRoot(path), rootSlot: -1}
	if condition.TemplateKeyValue != "" {
		template, ok := templatesByKey[condition.TemplateKeyValue]
		if !ok {
			return actionBindingRead{}, fmt.Errorf("%w: missing template for action binding %q", ErrMatcher, normalized.Binding)
		}
		compiled, _, err := compileExpressionPathRef(ruleName, -1, actionIndex, &template, path)
		if err != nil {
			return actionBindingRead{}, err
		}
		access = compiled
	} else if err := validatePathSpec(path); err != nil {
		return actionBindingRead{}, &ValidationError{
			RuleName:       ruleName,
			ActionIndex:    actionIndex,
			HasActionIndex: true,
			FieldName:      pathRoot(path),
			Reason:         "invalid action binding read path",
			Err:            err,
		}
	}
	return actionBindingRead{bindingSlot: bindingSlot, access: access}, nil
}

func actionBindingReadsForExpressions(expressions []compiledExpression) actionBindingReadSet {
	out := actionBindingReadSet{known: true}
	for _, expression := range expressions {
		collectExpressionBindingReads(expression, &out)
	}
	return out
}

func collectExpressionBindingReads(expression compiledExpression, out *actionBindingReadSet) {
	if out == nil {
		return
	}
	switch expression.kind {
	case expressionNodeBindingField:
		out.add(actionBindingRead{bindingSlot: expression.bindingSlot, access: expression.access})
	case expressionNodeBindingValue:
		out.add(actionBindingRead{bindingSlot: expression.bindingSlot, whole: true, value: true})
	case expressionNodeCall, expressionNodeCompare, expressionNodeBoolean:
		for _, operand := range expression.operands {
			collectExpressionBindingReads(operand, out)
		}
	}
}

func (s *actionBindingReadSet) add(read actionBindingRead) {
	if s == nil || read.bindingSlot < 0 {
		return
	}
	for i, existing := range s.reads {
		if existing.bindingSlot != read.bindingSlot {
			continue
		}
		if existing.whole || read.whole {
			s.reads[i] = actionBindingRead{bindingSlot: read.bindingSlot, whole: true}
			return
		}
		if existing.access.root == read.access.root && existing.access.rootSlot == read.access.rootSlot && slices.Equal(existing.access.path.Segments, read.access.path.Segments) {
			return
		}
	}
	s.reads = append(s.reads, read)
}

func (s actionBindingReadSet) observesModify(bindingSlot int, summary factModifySummary) bool {
	if !s.known {
		return true
	}
	for _, read := range s.reads {
		if read.bindingSlot != bindingSlot {
			continue
		}
		if read.whole || summary.observesAccess(read.access) {
			return true
		}
	}
	return false
}

func compileActionSpec(spec ActionSpec, order int) (compiledAction, error) {
	normalized, err := normalizeActionSpec(spec)
	if err != nil {
		return compiledAction{}, err
	}

	return compiledAction{
		name:                 normalized.Name,
		fn:                   normalized.Fn,
		assertTemplateValues: normalized.AssertTemplateValues,
		effect:               normalized.Effect,
		call:                 normalized.Call,
		bindingReads:         normalized.BindingReads,
		gessSource:           normalized.GessSource,
		order:                order,
		skipBindingFreeze:    normalized.NonEscaping || normalized.AssertTemplateValues != nil || normalized.Effect != nil || normalized.Call != nil || (normalized.BindingReads != nil && len(normalized.BindingReads.Reads) == 0),
	}, nil
}

func (a compiledAction) inspect() Action {
	return Action{
		NameValue:                  a.name,
		Order:                      a.order,
		GessSourceText:             a.gessSource,
		AssertTemplateValuesAction: cloneAssertTemplateValuesActionSpec(a.assertTemplateValues),
	}
}

func (a compiledAction) clone() compiledAction {
	a.assertTemplateValues = cloneAssertTemplateValuesActionSpec(a.assertTemplateValues)
	a.effect = cloneActionEffectSpec(a.effect)
	a.call = gessrules.CloneActionCallSpec(a.call)
	a.bindingReads = cloneActionBindingReadSetSpec(a.bindingReads)
	return a
}

func compileEffectAction(ruleName string, actionIndex int, spec *ActionEffectSpec, conditions []RuleCondition, bindingSlots map[string]int, modules map[ModuleName]Module, templatesByKey map[TemplateKey]compiledTemplate, functions map[string]compiledPureFunction, globals map[string]compiledGlobal, rhsBinds map[string]struct{}) (compiledEffectAction, error) {
	out := compiledEffectAction{
		kind:        spec.Kind,
		target:      strings.TrimSpace(spec.Target),
		templateKey: spec.TemplateKey,
		factName:    strings.TrimSpace(spec.FactName),
		fields:      append([]string(nil), spec.Fields...),
		unset:       append([]string(nil), spec.Unset...),
		values:      make([]compiledExpression, len(spec.Values)),
	}
	if !validActionEffectKind(out.kind) {
		return compiledEffectAction{}, effectValidationError(ruleName, actionIndex, "invalid action effect kind")
	}
	if len(out.fields) > 0 && out.kind != ActionEffectAssert && out.kind != ActionEffectAssertLogical && out.kind != ActionEffectModify {
		return compiledEffectAction{}, effectValidationError(ruleName, actionIndex, fmt.Sprintf("action effect kind %d does not support fields", out.kind))
	}
	if len(out.unset) > 0 && out.kind != ActionEffectModify {
		return compiledEffectAction{}, effectValidationError(ruleName, actionIndex, fmt.Sprintf("action effect kind %d does not support unset fields", out.kind))
	}
	switch out.kind {
	case ActionEffectRetract, ActionEffectPushFocus, ActionEffectPopFocus, ActionEffectClearFocus, ActionEffectHalt:
		if len(out.values) > 0 {
			return compiledEffectAction{}, effectValidationError(ruleName, actionIndex, fmt.Sprintf("action effect kind %d does not support values", out.kind))
		}
	}
	if out.kind == ActionEffectPushFocus {
		if out.target == "" {
			return compiledEffectAction{}, effectValidationError(ruleName, actionIndex, "focus target module is required")
		}
		module := normalizeModuleName(ModuleName(out.target))
		if _, ok := modules[module]; !ok {
			return compiledEffectAction{}, effectValidationError(ruleName, actionIndex, fmt.Sprintf("focus target %q is not a declared module", module))
		}
		out.target = module.String()
	}
	// Assert effects require a declared template; reject an undeclared target at
	// compile time rather than failing mid-firing (dynamic facts are not a
	// public concept).
	if out.kind == ActionEffectAssert || out.kind == ActionEffectAssertLogical {
		if _, ok := templatesByKey[out.templateKey]; out.templateKey == "" || !ok {
			verb := "assert"
			if out.kind == ActionEffectAssertLogical {
				verb = "assert-logical"
			}
			return compiledEffectAction{}, &ValidationError{
				RuleName:       ruleName,
				ActionIndex:    actionIndex,
				HasActionIndex: true,
				Reason:         fmt.Sprintf("%s target %q is not a declared template", verb, out.factName),
			}
		}
	}
	for i, valueSpec := range spec.Values {
		if err := validateActionRHSBindReferences(ruleName, actionIndex, valueSpec, rhsBinds); err != nil {
			return compiledEffectAction{}, err
		}
		if nativeActionExpressionUsesCurrent(valueSpec) {
			return compiledEffectAction{}, &ValidationError{
				RuleName:       ruleName,
				ActionIndex:    actionIndex,
				HasActionIndex: true,
				Reason:         "action value cannot reference a current fact or query parameter",
			}
		}
		value, _, err := compileExpressionSpecWithParams(valueSpec, ruleName, -1, i, nil, conditions, bindingSlots, templatesByKey, nil, functions, globals)
		if err != nil {
			return compiledEffectAction{}, err
		}
		out.values[i] = value
	}
	// Effects that name template fields or a target binding are validated at
	// compile time so a firing can never abort partway through — after earlier
	// effects have already mutated working memory — on a typo, a static type
	// mismatch, a bad mutation target, or a malformed bind. Runtime enforces the
	// same rules (template.applyDefaultsAndValidate, ctx.BindingID); mirroring
	// them here keeps a compiled ruleset free of these static failures. Value
	// expressions are already compiled above, so their errors surface first.
	//
	// Set fields pair positionally with their values; a spec supplying a
	// different count of each would index out of range at fire time.
	if out.kind == ActionEffectAssert || out.kind == ActionEffectAssertLogical || out.kind == ActionEffectModify {
		if len(out.fields) != len(out.values) {
			return compiledEffectAction{}, &ValidationError{
				RuleName:       ruleName,
				ActionIndex:    actionIndex,
				HasActionIndex: true,
				Reason:         fmt.Sprintf("effect has %d fields but %d values", len(out.fields), len(out.values)),
			}
		}
	}
	switch out.kind {
	case ActionEffectBind:
		if out.target == "" {
			return compiledEffectAction{}, effectValidationError(ruleName, actionIndex, "bind target is required")
		}
		if !isValidBindingName(out.target) {
			return compiledEffectAction{}, effectValidationError(ruleName, actionIndex, "invalid bind target")
		}
		if _, exists := rhsBinds[out.target]; exists {
			return compiledEffectAction{}, effectValidationError(ruleName, actionIndex, fmt.Sprintf("bind target %q is already defined", out.target))
		}
		if len(out.values) != 1 {
			return compiledEffectAction{}, &ValidationError{
				RuleName:       ruleName,
				ActionIndex:    actionIndex,
				HasActionIndex: true,
				Reason:         "bind requires exactly one value",
			}
		}
	case ActionEffectRetract:
		if _, ok := factBindingTarget(out.target, conditions); !ok {
			return compiledEffectAction{}, &ValidationError{
				RuleName:       ruleName,
				ActionIndex:    actionIndex,
				HasActionIndex: true,
				Reason:         fmt.Sprintf("retract target %q is not a bound fact", out.target),
			}
		}
	case ActionEffectModify:
		templateKey, ok := factBindingTarget(out.target, conditions)
		if !ok {
			return compiledEffectAction{}, &ValidationError{
				RuleName:       ruleName,
				ActionIndex:    actionIndex,
				HasActionIndex: true,
				Reason:         fmt.Sprintf("modify target %q is not a bound fact", out.target),
			}
		}
		// A dynamic fact binding has no template key; its fields cannot be
		// checked statically, so skip rather than risk a false rejection. A
		// modify patches a subset of slots, so it does not enforce required
		// fields.
		if template, ok := templatesByKey[templateKey]; templateKey != "" && ok {
			if err := validateEffectTemplateFields(ruleName, actionIndex, template, out.fields, out.unset, out.values, false); err != nil {
				return compiledEffectAction{}, err
			}
		}
	case ActionEffectAssert, ActionEffectAssertLogical:
		// out.templateKey is already validated as declared above. An assert
		// materializes a whole fact, so every required no-default field must be
		// supplied.
		if template, ok := templatesByKey[out.templateKey]; ok {
			if err := validateEffectTemplateFields(ruleName, actionIndex, template, out.fields, nil, out.values, true); err != nil {
				return compiledEffectAction{}, err
			}
		}
	}
	return out, nil
}

func validActionEffectKind(kind ActionEffectKind) bool {
	switch kind {
	case ActionEffectAssert, ActionEffectAssertLogical, ActionEffectModify, ActionEffectRetract,
		ActionEffectEmit, ActionEffectBind, ActionEffectPushFocus, ActionEffectPopFocus,
		ActionEffectClearFocus, ActionEffectHalt:
		return true
	default:
		return false
	}
}

func effectValidationError(ruleName string, actionIndex int, reason string) *ValidationError {
	return &ValidationError{
		RuleName:       ruleName,
		ActionIndex:    actionIndex,
		HasActionIndex: true,
		Reason:         reason,
	}
}

// factBindingTarget resolves a modify/retract target binding to its fact
// condition at compile time, mirroring ActionContext.BindingID: a target names a
// fact binding only when a condition binds it and that condition carries a fact
// target (a template key or a dynamic name). List-pattern element bindings and
// aggregate result bindings have an empty target and are value bindings, exactly
// the entries BindingID rejects at fire time. The returned template key may be
// empty for a dynamic fact binding, so callers must gate template-dependent
// checks on a non-empty key. It matches the runtime binding lookup exactly —
// same conditions slice, same trimmed exact-string compare (binding names never
// carry a '?') — so it neither rejects a target the runtime accepts nor accepts
// one the runtime rejects.
func factBindingTarget(target string, conditions []RuleCondition) (TemplateKey, bool) {
	name := strings.TrimSpace(target)
	if name == "" {
		return "", false
	}
	for _, cond := range conditions {
		if cond.Binding() != name {
			continue
		}
		if cond.TemplateKey() != "" || cond.Name() != "" {
			return cond.TemplateKey(), true
		}
		return "", false
	}
	return "", false
}

// effectValueKindAssignable reports whether a set value of static kind got may
// store into a template field of kind want. It mirrors the runtime's
// isValueCompatibleWithKind: a ValueAny field accepts any value, and an
// indeterminate value kind (unknown or ValueAny) is deferred to fire time.
// Every other pair must match exactly — the storage path performs no int/float
// coercion, so a numeric cross is rejected here just as it would abort at fire
// time.
func effectValueKindAssignable(want, got ValueKind) bool {
	if want == ValueAny || want == valueKindUnknown {
		return true
	}
	if got == ValueAny || got == valueKindUnknown {
		return true
	}
	return want == got
}

// validateEffectTemplateFields checks, at compile time, the field-level rules
// template.applyDefaultsAndValidate enforces at fire time: every named set and
// unset field is a declared slot; a statically-typed set value matches its
// field kind (strict, no coercion, since runtime storage is exact-match); a
// constant set value is a member of the field's allowed set when it has one.
// With enforceRequired (asserts, which materialize a whole fact), it also
// rejects an omitted required no-default field; a modify patches a subset of
// slots, so it passes enforceRequired=false.
func validateEffectTemplateFields(ruleName string, actionIndex int, template compiledTemplate, fields, unset []string, values []compiledExpression, enforceRequired bool) error {
	set := make(map[string]struct{}, len(fields))
	for i, name := range fields {
		set[name] = struct{}{}
		kind, ok := template.fieldKind(name)
		if !ok {
			return &ValidationError{
				RuleName:       ruleName,
				ActionIndex:    actionIndex,
				HasActionIndex: true,
				TemplateName:   template.Name(),
				FieldName:      name,
				Reason:         "unknown field",
			}
		}
		if i >= len(values) {
			continue
		}
		value := values[i]
		if value.kind == expressionNodeConst {
			// A constant's static kind is exactly its runtime kind, so a
			// mismatch — including an int/float cross — will abort at fire time;
			// reject it strictly. Its literal is also checkable against the
			// field's allowed set.
			if !effectValueKindAssignable(kind, value.resultKind) {
				return &ValidationError{
					RuleName:       ruleName,
					ActionIndex:    actionIndex,
					HasActionIndex: true,
					TemplateName:   template.Name(),
					FieldName:      name,
					Reason:         "value type does not match template field",
				}
			}
			if allowed, ok := template.fieldAllowedValues(name); ok && !valueAllowed(allowed, value.value) {
				return &ValidationError{
					RuleName:       ruleName,
					ActionIndex:    actionIndex,
					HasActionIndex: true,
					TemplateName:   template.Name(),
					FieldName:      name,
					Reason:         "value not in allowed set",
				}
			}
		} else if !expressionKindAssignable(kind, value.resultKind) {
			// A binding or computed value carries only a declared/hint kind that
			// may resolve to a compatible kind at fire time (e.g. a function
			// declared to return float that returns int), so keep the runtime's
			// numeric tolerance rather than risk a false compile-time rejection.
			return &ValidationError{
				RuleName:       ruleName,
				ActionIndex:    actionIndex,
				HasActionIndex: true,
				TemplateName:   template.Name(),
				FieldName:      name,
				Reason:         "value type does not match template field",
			}
		}
	}
	for _, name := range unset {
		if _, ok := template.fieldKind(name); !ok {
			return &ValidationError{
				RuleName:       ruleName,
				ActionIndex:    actionIndex,
				HasActionIndex: true,
				TemplateName:   template.Name(),
				FieldName:      name,
				Reason:         "unknown field",
			}
		}
	}
	if enforceRequired {
		for _, field := range template.fields {
			if _, provided := set[field.Name]; provided {
				continue
			}
			if template.requiredFieldMissing(field) {
				return &ValidationError{
					RuleName:       ruleName,
					ActionIndex:    actionIndex,
					HasActionIndex: true,
					TemplateName:   template.Name(),
					FieldName:      field.Name,
					Reason:         "required field is missing",
				}
			}
		}
	}
	return nil
}

func compileCallAction(ruleName string, actionIndex int, spec *ActionCallSpec, conditions []RuleCondition, bindingSlots map[string]int, templatesByKey map[TemplateKey]compiledTemplate, functions map[string]compiledPureFunction, globals map[string]compiledGlobal, rhsBinds map[string]struct{}) (compiledCallAction, error) {
	out := compiledCallAction{
		name: strings.TrimSpace(spec.Name),
		fn:   spec.Fn,
		args: make([]compiledExpression, len(spec.Args)),
	}
	for i, argSpec := range spec.Args {
		if err := validateActionRHSBindReferences(ruleName, actionIndex, argSpec, rhsBinds); err != nil {
			return compiledCallAction{}, err
		}
		if nativeActionExpressionUsesCurrent(argSpec) {
			return compiledCallAction{}, &ValidationError{
				RuleName:       ruleName,
				ActionIndex:    actionIndex,
				HasActionIndex: true,
				Reason:         "call argument cannot reference a current fact or query parameter",
			}
		}
		value, _, err := compileExpressionSpecWithParams(argSpec, ruleName, -1, i, nil, conditions, bindingSlots, templatesByKey, nil, functions, globals)
		if err != nil {
			return compiledCallAction{}, err
		}
		out.args[i] = value
	}
	return out, nil
}

func (a compiledCallAction) clone() compiledCallAction {
	a.args = append([]compiledExpression(nil), a.args...)
	return a
}

func compileRuleActionExecution(ruleName string, actionIndex int, action compiledAction, conditions []RuleCondition, bindingSlots map[string]int, modules map[ModuleName]Module, templatesByKey map[TemplateKey]compiledTemplate, functions map[string]compiledPureFunction, globals map[string]compiledGlobal, rhsBinds map[string]struct{}) (compiledRuleAction, error) {
	out := compiledRuleAction{
		name:              action.name,
		order:             actionIndex,
		fn:                action.fn,
		skipBindingFreeze: action.skipBindingFreeze,
	}
	if action.fn != nil {
		out.kind = compiledRuleActionFunction
		readSet, err := compileDeclaredActionBindingReads(ruleName, actionIndex, action.bindingReads, conditions, bindingSlots, templatesByKey)
		if err != nil {
			return compiledRuleAction{}, err
		}
		out.bindingReads = readSet
		return out, nil
	}
	if action.effect != nil {
		effect, err := compileEffectAction(ruleName, actionIndex, action.effect, conditions, bindingSlots, modules, templatesByKey, functions, globals, rhsBinds)
		if err != nil {
			return compiledRuleAction{}, err
		}
		out.kind = compiledRuleActionEffect
		out.effect = effect
		return out, nil
	}
	if action.call != nil {
		callAction, err := compileCallAction(ruleName, actionIndex, action.call, conditions, bindingSlots, templatesByKey, functions, globals, rhsBinds)
		if err != nil {
			return compiledRuleAction{}, err
		}
		out.kind = compiledRuleActionCall
		out.call = callAction
		return out, nil
	}
	if action.assertTemplateValues == nil {
		return compiledRuleAction{}, &ValidationError{
			RuleName:       ruleName,
			ActionIndex:    actionIndex,
			HasActionIndex: true,
			Reason:         "action implementation is missing",
		}
	}
	spec := action.assertTemplateValues
	template, ok := templatesByKey[spec.TemplateKey]
	if !ok {
		return compiledRuleAction{}, &ValidationError{
			RuleName:       ruleName,
			ActionIndex:    actionIndex,
			HasActionIndex: true,
			TemplateName:   string(spec.TemplateKey),
			Reason:         "unknown template key",
		}
	}
	if !template.closed {
		return compiledRuleAction{}, &ValidationError{
			RuleName:       ruleName,
			ActionIndex:    actionIndex,
			HasActionIndex: true,
			TemplateName:   template.Name(),
			Reason:         "template values require a fixed template",
		}
	}
	if err := validatePublicTemplateMutation(template); err != nil {
		return compiledRuleAction{}, &ValidationError{
			RuleName:       ruleName,
			ActionIndex:    actionIndex,
			HasActionIndex: true,
			TemplateName:   template.Name(),
			Reason:         "backchain demand template is engine-owned",
			Err:            err,
		}
	}
	if len(spec.Values) > len(template.fields) {
		return compiledRuleAction{}, &ValidationError{
			RuleName:       ruleName,
			ActionIndex:    actionIndex,
			HasActionIndex: true,
			TemplateName:   template.Name(),
			Reason:         "too many field values",
		}
	}

	values := make([]compiledExpression, len(spec.Values))
	for i, valueSpec := range spec.Values {
		if err := validateActionRHSBindReferences(ruleName, actionIndex, valueSpec, rhsBinds); err != nil {
			return compiledRuleAction{}, err
		}
		if nativeActionExpressionUsesCurrent(valueSpec) {
			return compiledRuleAction{}, &ValidationError{
				RuleName:       ruleName,
				ActionIndex:    actionIndex,
				HasActionIndex: true,
				TemplateName:   template.Name(),
				FieldName:      template.fields[i].Name,
				Reason:         "native assert value cannot reference a current fact",
			}
		}
		value, _, err := compileExpressionSpecWithParams(valueSpec, ruleName, -1, i, nil, conditions, bindingSlots, templatesByKey, nil, functions, globals)
		if err != nil {
			return compiledRuleAction{}, err
		}
		field := template.fields[i]
		if field.Kind != ValueAny && value.resultKind != ValueAny && value.resultKind != field.Kind {
			return compiledRuleAction{}, &ValidationError{
				RuleName:       ruleName,
				ActionIndex:    actionIndex,
				HasActionIndex: true,
				TemplateName:   template.Name(),
				FieldName:      field.Name,
				Reason:         "native assert value type does not match template field",
			}
		}
		values[i] = value
	}

	out.kind = compiledRuleActionAssertTemplateValues
	tokenValues := make([]compiledTokenActionValue, len(values))
	out.bindingReads = actionBindingReadsForExpressions(values)
	for i, value := range values {
		tokenValues[i] = compileTokenActionValue(value)
	}
	out.assertTemplateValues = compiledAssertTemplateValuesAction{
		template:    template,
		insertPlan:  newCompiledGeneratedFactInsertPlan(template),
		values:      values,
		tokenValues: tokenValues,
	}
	return out, nil
}

func compileTokenActionValue(expression compiledExpression) compiledTokenActionValue {
	out := compiledTokenActionValue{kind: compiledTokenActionValueGeneric, expression: expression}
	switch expression.kind {
	case expressionNodeConst:
		out.kind = compiledTokenActionValueConst
		out.value = expression.value
	case expressionNodeBindingField:
		if tokenActionBindingFieldFastPath(expression) {
			out.kind = compiledTokenActionValueBindingField
			out.bindingSlot = expression.bindingSlot
			out.access = expression.access
		}
	case expressionNodeBindingValue:
		if expression.bindingSlot >= 0 {
			out.kind = compiledTokenActionValueBindingValue
			out.bindingSlot = expression.bindingSlot
		}
	case expressionNodeCall:
		if value, ok := compileStringCall2ConstBindingFieldTokenActionValue(expression); ok {
			return value
		}
	}
	return out
}

func compileStringCall2ConstBindingFieldTokenActionValue(expression compiledExpression) (compiledTokenActionValue, bool) {
	if expression.function.fn2 == nil || expression.function.ret != ValueString || len(expression.operands) != 2 {
		return compiledTokenActionValue{}, false
	}
	if len(expression.function.args) != 2 || expression.function.args[0] != ValueString || expression.function.args[1] != ValueString {
		return compiledTokenActionValue{}, false
	}
	prefix := expression.operands[0]
	field := expression.operands[1]
	if prefix.kind != expressionNodeConst || prefix.value.Kind() != ValueString || !tokenActionBindingFieldFastPath(field) {
		return compiledTokenActionValue{}, false
	}
	return compiledTokenActionValue{
		kind:        compiledTokenActionValueStringCall2ConstBindingField,
		value:       prefix.value,
		bindingSlot: field.bindingSlot,
		access:      field.access,
		function:    expression.function,
		expression:  expression,
	}, true
}

func tokenActionBindingFieldFastPath(expression compiledExpression) bool {
	return expression.bindingSlot >= 0 && expression.access.rootSlot >= 0 && expression.access.topLevel()
}

func validateActionRHSBindReferences(ruleName string, actionIndex int, spec ExpressionSpec, available map[string]struct{}) error {
	switch expression := spec.(type) {
	case nil:
		return nil
	case RHSBindExpr:
		return validateActionRHSBindReference(ruleName, actionIndex, expression.Name, available)
	case *RHSBindExpr:
		if expression == nil {
			return nil
		}
		return validateActionRHSBindReference(ruleName, actionIndex, expression.Name, available)
	case CallExpr:
		for _, arg := range expression.Args {
			if err := validateActionRHSBindReferences(ruleName, actionIndex, arg, available); err != nil {
				return err
			}
		}
	case *CallExpr:
		if expression != nil {
			return validateActionRHSBindReferences(ruleName, actionIndex, CallExpr(*expression), available)
		}
	case CompareExpr:
		if err := validateActionRHSBindReferences(ruleName, actionIndex, expression.Left, available); err != nil {
			return err
		}
		return validateActionRHSBindReferences(ruleName, actionIndex, expression.Right, available)
	case *CompareExpr:
		if expression != nil {
			return validateActionRHSBindReferences(ruleName, actionIndex, CompareExpr(*expression), available)
		}
	case BooleanExpr:
		for _, operand := range expression.Operands {
			if err := validateActionRHSBindReferences(ruleName, actionIndex, operand, available); err != nil {
				return err
			}
		}
	case *BooleanExpr:
		if expression != nil {
			return validateActionRHSBindReferences(ruleName, actionIndex, BooleanExpr(*expression), available)
		}
	}
	return nil
}

func validateActionRHSBindReference(ruleName string, actionIndex int, name string, available map[string]struct{}) error {
	name = strings.TrimSpace(name)
	if name == "" || !isValidBindingName(name) {
		return effectValidationError(ruleName, actionIndex, "invalid rhs bind reference")
	}
	if _, ok := available[name]; !ok {
		return effectValidationError(ruleName, actionIndex, fmt.Sprintf("rhs bind %q is not defined by an earlier action", name))
	}
	return nil
}

func nativeActionExpressionUsesCurrent(spec ExpressionSpec) bool {
	switch expression := spec.(type) {
	case nil:
		return false
	case CurrentFieldExpr, *CurrentFieldExpr, HasPathExpr, *HasPathExpr, ParamExpr, *ParamExpr:
		return true
	case CallExpr:
		if slices.ContainsFunc(expression.Args, nativeActionExpressionUsesCurrent) {
			return true
		}
	case *CallExpr:
		if expression == nil {
			return false
		}
		if slices.ContainsFunc(expression.Args, nativeActionExpressionUsesCurrent) {
			return true
		}
	case CompareExpr:
		return nativeActionExpressionUsesCurrent(expression.Left) || nativeActionExpressionUsesCurrent(expression.Right)
	case *CompareExpr:
		return expression != nil && (nativeActionExpressionUsesCurrent(expression.Left) || nativeActionExpressionUsesCurrent(expression.Right))
	case BooleanExpr:
		if slices.ContainsFunc(expression.Operands, nativeActionExpressionUsesCurrent) {
			return true
		}
	case *BooleanExpr:
		if expression == nil {
			return false
		}
		if slices.ContainsFunc(expression.Operands, nativeActionExpressionUsesCurrent) {
			return true
		}
	}
	return false
}

func serializeCompiledRuleAction(action compiledRuleAction) string {
	var b strings.Builder
	b.WriteString(action.name)
	b.WriteByte(':')
	b.WriteString(fmt.Sprint(action.order))
	b.WriteByte(':')
	switch action.kind {
	case compiledRuleActionFunction:
		b.WriteString("function")
		b.WriteString(":reads:")
		b.WriteString(serializeActionBindingReadSet(action.bindingReads))
	case compiledRuleActionAssertTemplateValues:
		b.WriteString("assert-template-values:")
		b.WriteString(action.assertTemplateValues.template.Key().String())
		for _, value := range action.assertTemplateValues.values {
			b.WriteByte(':')
			b.WriteString(serializeCompiledExpression(value))
		}
	case compiledRuleActionEffect:
		b.WriteString("effect:")
		b.WriteString(serializeCompiledEffectAction(action.effect))
	case compiledRuleActionCall:
		b.WriteString("call:")
		b.WriteString(action.call.name)
		for _, value := range action.call.args {
			b.WriteString(":a=")
			b.WriteString(serializeCompiledExpression(value))
		}
	default:
		b.WriteString("unknown")
	}
	return b.String()
}

func serializeCompiledEffectAction(effect compiledEffectAction) string {
	var b strings.Builder
	b.WriteString(fmt.Sprint(effect.kind))
	b.WriteByte(':')
	b.WriteString(effect.target)
	b.WriteByte(':')
	b.WriteString(effect.templateKey.String())
	b.WriteByte(':')
	b.WriteString(effect.factName)
	for _, field := range effect.fields {
		b.WriteString(":f=")
		b.WriteString(field)
	}
	for _, name := range effect.unset {
		b.WriteString(":u=")
		b.WriteString(name)
	}
	for _, value := range effect.values {
		b.WriteString(":v=")
		b.WriteString(serializeCompiledExpression(value))
	}
	return b.String()
}

func serializeCompiledActionDeclaration(action compiledAction) string {
	var b strings.Builder
	b.WriteString(action.name)
	b.WriteByte(':')
	b.WriteString(fmt.Sprint(action.order))
	b.WriteByte(':')
	b.WriteString(fmt.Sprint(action.skipBindingFreeze))
	if action.call != nil {
		b.WriteString(":call:")
		b.WriteString(strings.TrimSpace(action.call.Name))
		for _, value := range action.call.Args {
			b.WriteString(":a=")
			b.WriteString(serializeExpressionSpec(value))
		}
		return b.String()
	}
	if action.effect != nil {
		b.WriteString(":effect:")
		effect := action.effect
		b.WriteString(fmt.Sprint(effect.Kind))
		b.WriteByte(':')
		b.WriteString(effect.Target)
		b.WriteByte(':')
		b.WriteString(effect.TemplateKey.String())
		b.WriteByte(':')
		b.WriteString(effect.FactName)
		for _, field := range effect.Fields {
			b.WriteString(":f=")
			b.WriteString(field)
		}
		for _, name := range effect.Unset {
			b.WriteString(":u=")
			b.WriteString(name)
		}
		for _, value := range effect.Values {
			b.WriteString(":v=")
			b.WriteString(serializeExpressionSpec(value))
		}
		return b.String()
	}
	if action.assertTemplateValues == nil {
		b.WriteString(":function")
		b.WriteString(":reads:")
		if action.bindingReads == nil {
			b.WriteString("unknown")
		} else {
			b.WriteString(serializeActionBindingReadSetSpec(action.bindingReads))
		}
		return b.String()
	}
	b.WriteString(":assert-template-values:")
	b.WriteString(action.assertTemplateValues.TemplateKey.String())
	for _, value := range action.assertTemplateValues.Values {
		b.WriteByte(':')
		b.WriteString(serializeExpressionSpec(value))
	}
	return b.String()
}

func serializeActionBindingReadSet(set actionBindingReadSet) string {
	if !set.known {
		return "unknown"
	}
	var b strings.Builder
	for _, read := range set.reads {
		b.WriteString(fmt.Sprint(read.bindingSlot))
		if read.whole {
			b.WriteString(":*;")
			continue
		}
		b.WriteByte(':')
		b.WriteString(read.access.path.String())
		b.WriteByte(';')
	}
	return b.String()
}

func serializeActionBindingReadSetSpec(spec *ActionBindingReadSetSpec) string {
	if spec == nil {
		return "unknown"
	}
	var b strings.Builder
	for _, read := range spec.Reads {
		b.WriteString(strings.TrimSpace(read.Binding))
		path := pathOrField(read.Path, read.Field)
		if pathIsZero(path) {
			b.WriteString(":*;")
			continue
		}
		b.WriteByte(':')
		b.WriteString(path.String())
		b.WriteByte(';')
	}
	return b.String()
}

func serializeExpressionSpec(spec ExpressionSpec) string {
	switch expression := spec.(type) {
	case nil:
		return "<nil>"
	case ConstExpr:
		value, err := canonicalValue(expression.Value)
		if err != nil {
			return "const:error"
		}
		return "const:" + value.CanonicalKey()
	case *ConstExpr:
		if expression == nil {
			return "<nil>"
		}
		return serializeExpressionSpec(*expression)
	case CurrentFieldExpr:
		return "current:" + pathOrField(expression.Path, expression.Field).String()
	case *CurrentFieldExpr:
		if expression == nil {
			return "<nil>"
		}
		return serializeExpressionSpec(*expression)
	case BindingFieldExpr:
		return "binding-field:" + expression.Binding + ":" + pathOrField(expression.Path, expression.Field).String()
	case *BindingFieldExpr:
		if expression == nil {
			return "<nil>"
		}
		return serializeExpressionSpec(*expression)
	case BindingValueExpr:
		return "binding-value:" + expression.Binding
	case *BindingValueExpr:
		if expression == nil {
			return "<nil>"
		}
		return serializeExpressionSpec(*expression)
	case HasPathExpr:
		return "has-path:" + expression.Path.String()
	case *HasPathExpr:
		if expression == nil {
			return "<nil>"
		}
		return serializeExpressionSpec(*expression)
	case ParamExpr:
		return "param:" + expression.Name
	case *ParamExpr:
		if expression == nil {
			return "<nil>"
		}
		return serializeExpressionSpec(*expression)
	case GlobalExpr:
		return "global:" + expression.Name
	case *GlobalExpr:
		if expression == nil {
			return "<nil>"
		}
		return serializeExpressionSpec(*expression)
	case RHSBindExpr:
		return "rhs-bind:" + expression.Name
	case *RHSBindExpr:
		if expression == nil {
			return "<nil>"
		}
		return serializeExpressionSpec(*expression)
	case CallExpr:
		var b strings.Builder
		b.WriteString("call:")
		b.WriteString(expression.Name)
		for _, arg := range expression.Args {
			b.WriteByte('(')
			b.WriteString(serializeExpressionSpec(arg))
			b.WriteByte(')')
		}
		return b.String()
	case *CallExpr:
		if expression == nil {
			return "<nil>"
		}
		return serializeExpressionSpec(*expression)
	case CompareExpr:
		return "compare:" + string(expression.Operator) + "(" + serializeExpressionSpec(expression.Left) + ")(" + serializeExpressionSpec(expression.Right) + ")"
	case *CompareExpr:
		if expression == nil {
			return "<nil>"
		}
		return serializeExpressionSpec(*expression)
	case BooleanExpr:
		var b strings.Builder
		b.WriteString("bool:")
		b.WriteString(string(expression.Operator))
		for _, operand := range expression.Operands {
			b.WriteByte('(')
			b.WriteString(serializeExpressionSpec(operand))
			b.WriteByte(')')
		}
		return b.String()
	case *BooleanExpr:
		if expression == nil {
			return "<nil>"
		}
		return serializeExpressionSpec(*expression)
	default:
		return fmt.Sprintf("unsupported:%T", spec)
	}
}

func (s *Session) executeActivationActions(ctx context.Context, runID RunID, activation activation) (err error) {
	return s.executeActivationActionsInternal(ctx, runID, activation, false)
}

func (s *Session) executeTrustedActivationActions(ctx context.Context, runID RunID, activation activation) (err error) {
	return s.executeActivationActionsInternal(ctx, runID, activation, true)
}

func (s *Session) executeActivationActionsInternal(ctx context.Context, runID RunID, activation activation, trustTokenActivation bool) (err error) {
	if s == nil {
		return ErrClosedSession
	}
	if s.closed {
		return ErrClosedSession
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.revision == nil {
		return ErrInvalidRuleset
	}

	rule, ok := s.revision.rulesByRevisionID[activation.ruleRevisionID]
	if !ok {
		return fmt.Errorf("%w: unknown rule revision %q", ErrMatcher, activation.ruleRevisionID)
	}
	skipBindingFreeze := rule.allActionsSkipBindingFreeze
	var actionCtx actionContext
	actionCtxReady := false
	activationValidated := false
	defer func() {
		if !actionCtxReady || skipBindingFreeze {
			return
		}
		if freezeErr := actionCtx.materializeAllBindings(); err == nil && freezeErr != nil {
			err = freezeErr
		}
	}()
	for _, actionSpec := range rule.actionExecutions {
		if err := ctx.Err(); err != nil {
			return err
		}
		var actionErr error
		switch actionSpec.kind {
		case compiledRuleActionFunction:
			if !actionCtxReady {
				actionCtx, err = s.actionContextForActivationWithScratchTrusted(ctx, activation, skipBindingFreeze, trustTokenActivation)
				if err != nil {
					return err
				}
				actionCtx.rhsBinds = &rhsBindStore{}
				actionCtxReady = true
				activationValidated = true
			}
			actionCtx.actionName = actionSpec.name
			actionCtx.actionIndex = actionSpec.order
			if actionSpec.fn == nil {
				actionErr = fmt.Errorf("%w: missing action %q", ErrInvalidRuleset, actionSpec.name)
			} else {
				actionErr = actionSpec.fn(wrapActionContext(actionCtx))
			}
		case compiledRuleActionEffect:
			if !actionCtxReady {
				actionCtx, err = s.actionContextForActivationWithScratchTrusted(ctx, activation, skipBindingFreeze, trustTokenActivation)
				if err != nil {
					return err
				}
				actionCtx.rhsBinds = &rhsBindStore{}
				actionCtxReady = true
				activationValidated = true
			}
			actionCtx.actionName = actionSpec.name
			actionCtx.actionIndex = actionSpec.order
			actionErr = s.executeEffectAction(actionCtx, actionSpec.effect)
		case compiledRuleActionCall:
			if !actionCtxReady {
				actionCtx, err = s.actionContextForActivationWithScratchTrusted(ctx, activation, skipBindingFreeze, trustTokenActivation)
				if err != nil {
					return err
				}
				actionCtx.rhsBinds = &rhsBindStore{}
				actionCtxReady = true
				activationValidated = true
			}
			actionCtx.actionName = actionSpec.name
			actionCtx.actionIndex = actionSpec.order
			actionErr = s.executeCallAction(actionCtx, actionSpec.call)
		case compiledRuleActionAssertTemplateValues:
			if !activationValidated {
				if err := s.validateActivationTokenFacts(rule, activation, trustTokenActivation); err != nil {
					return err
				}
				activationValidated = true
			}
			actionErr = s.executeAssertTemplateValuesAction(ctx, activation, rule, actionSpec.assertTemplateValues, actionSpec.name, actionSpec.order)
		default:
			actionErr = fmt.Errorf("%w: unsupported action %q", ErrInvalidRuleset, actionSpec.name)
		}
		if actionErr != nil {
			_, _ = s.removeLogicalSupportsForSources(ctx, []logicalSupportSourceKey{logicalSupportSourceFromActivation(activation)}, mutationOriginForRuleActivation(rule, activation))
			return &ActionFailureError{
				RunID:          runID,
				RuleID:         rule.id,
				RuleRevisionID: activation.ruleRevisionID,
				ActivationID:   activation.activationID(),
				ActionName:     actionSpec.name,
				ActionIndex:    actionSpec.order,
				Source:         firstSourceSpan(actionSpec.source, rule.source),
				RuleSource:     rule.source,
				ActionSource:   actionSpec.source,
				Err:            actionErr,
			}
		}
	}

	if s.explainLog != nil {
		var ctxPtr *actionContext
		if actionCtxReady {
			ctxPtr = &actionCtx
		}
		s.captureFiringBindings(rule, activation, ctxPtr)
	}
	return nil
}

// conditionMatches builds the binding tuple an action-value expression is
// evaluated against, from the ActionContext's frozen snapshots. Unlike
// actionMatchesForActivation, it reads the frozen pre-modify state, so an
// action value that references a fact modified earlier in the same firing sees
// the value the rule matched on rather than erroring on a version bump.
func (c actionContext) conditionMatches() ([]conditionMatch, error) {
	n := c.bindings.len()
	if n == 0 {
		return nil, nil
	}
	maxSlot := -1
	for i := range n {
		if slot := c.bindings.entryAt(i).bindingSlot; slot > maxSlot {
			maxSlot = slot
		}
	}
	if maxSlot < 0 {
		return nil, nil
	}
	matches := make([]conditionMatch, maxSlot+1)
	for i := range n {
		entry := c.bindings.entryAt(i)
		slot := entry.bindingSlot
		if slot < 0 || slot > maxSlot {
			continue
		}
		matches[slot] = conditionMatch{
			conditionID: entry.conditionID,
			bindingSlot: slot,
			value:       cloneValue(entry.value),
			hasValue:    entry.hasValue,
		}
		if entry.hasValue || entry.factID.IsZero() {
			continue
		}
		snapshot, ok := c.materializeBinding(i)
		if !ok {
			return nil, fmt.Errorf("%w: stale fact %q for activation %q", ErrMatcher, entry.factID, c.ActivationID())
		}
		matches[slot].fact = newConditionFactRefFromSnapshot(snapshot)
	}
	return matches, nil
}

// rhsBindValues returns the firing's RHS-local bindings as a name→Value map for
// expression evaluation (nil when none have been set).
func (c actionContext) rhsBindValues() map[string]Value {
	if c.rhsBinds == nil {
		return nil
	}
	return c.rhsBinds.values
}

// evalActionValue evaluates a compiled action-value expression against the
// firing's frozen bindings, globals, and RHS-local binds.
func (c actionContext) evalActionValue(e compiledExpression, matches []conditionMatch) (Value, error) {
	var globals []Value
	if c.session != nil {
		globals = c.session.globalValues
	}
	value, ok, err := e.evaluateWithContextParamsGlobalsAndCounters(c.Context(), conditionFactRef{}, matches, c.rhsBindValues(), globals, nil, nil)
	if err != nil {
		return Value{}, err
	}
	if !ok {
		return Value{}, fmt.Errorf("%w: action value is unavailable", ErrMatcher)
	}
	return value, nil
}

func (s *Session) executeEffectAction(ctx actionContext, effect compiledEffectAction) error {
	switch effect.kind {
	case ActionEffectPushFocus:
		return ctx.PushFocus(ModuleName(effect.target))
	case ActionEffectPopFocus:
		_, err := ctx.PopFocus()
		return err
	case ActionEffectClearFocus:
		return ctx.ClearFocusStack()
	case ActionEffectHalt:
		return ctx.Halt()
	}
	matches, err := ctx.conditionMatches()
	if err != nil {
		return err
	}
	values := make([]Value, len(effect.values))
	for i, expr := range effect.values {
		v, err := ctx.evalActionValue(expr, matches)
		if err != nil {
			return err
		}
		values[i] = v
	}
	switch effect.kind {
	case ActionEffectBind:
		if len(values) != 1 {
			return fmt.Errorf("%w: bind requires one value", ErrInvalidRuleset)
		}
		ctx.SetRHSBind(effect.target, values[0])
		return nil
	case ActionEffectEmit:
		return ctx.Emit(values...)
	case ActionEffectRetract:
		id, ok := ctx.BindingID(effect.target)
		if !ok {
			return fmt.Errorf("gess: retract missing fact binding %s", effect.target)
		}
		_, err := ctx.Retract(id)
		return err
	case ActionEffectModify:
		id, ok := ctx.BindingID(effect.target)
		if !ok {
			return fmt.Errorf("gess: modify missing fact binding %s", effect.target)
		}
		patch := FactPatch{Unset: effect.unset}
		if len(effect.fields) > 0 {
			pairs := make([]any, 0, len(effect.fields)*2)
			for i, field := range effect.fields {
				pairs = append(pairs, field, values[i])
			}
			set, err := NewFieldsFromPairs(pairs...)
			if err != nil {
				return err
			}
			patch.Set = set
		}
		_, err := ctx.Modify(id, patch)
		return err
	case ActionEffectAssert, ActionEffectAssertLogical:
		pairs := make([]any, 0, len(effect.fields)*2)
		for i, field := range effect.fields {
			pairs = append(pairs, field, values[i])
		}
		fields, err := NewFieldsFromPairs(pairs...)
		if err != nil {
			return err
		}
		if effect.kind == ActionEffectAssertLogical {
			if effect.templateKey != "" {
				if ctx.session == nil {
					return ErrClosedSession
				}
				if ctx.RuleRevisionID().IsZero() || ctx.ActivationID().IsZero() {
					return ErrLogicalSupportUnavailable
				}
				if err := ctx.materializeAllBindings(); err != nil {
					return err
				}
				_, err = ctx.session.insertLogicalFactWithContextAndOrigin(ctx.Context(), "", effect.templateKey, fields, ctx.mutationOrigin(), ctx.supportingFactIDs())
				return err
			}
			return fmt.Errorf("%w: assert-logical target %q is not a declared template", ErrInvalidRuleset, effect.factName)
		}
		if effect.templateKey != "" {
			_, err = ctx.Assert(effect.templateKey, fields)
			return err
		}
		return fmt.Errorf("%w: assert target %q is not a declared template", ErrInvalidRuleset, effect.factName)
	default:
		return fmt.Errorf("%w: unsupported action effect", ErrInvalidRuleset)
	}
}

func (s *Session) executeCallAction(ctx actionContext, action compiledCallAction) error {
	if action.fn == nil {
		return fmt.Errorf("%w: missing call function %q", ErrInvalidRuleset, action.name)
	}
	matches, err := ctx.conditionMatches()
	if err != nil {
		return err
	}
	args := make([]Value, len(action.args))
	for i, expr := range action.args {
		v, err := ctx.evalActionValue(expr, matches)
		if err != nil {
			return err
		}
		args[i] = v
	}
	return action.fn(wrapActionContext(ctx), args)
}

func (s *Session) executeAssertTemplateValuesAction(ctx context.Context, activation activation, rule compiledRule, action compiledAssertTemplateValuesAction, actionName string, actionIndex int) error {
	if len(action.values) == len(action.template.fields) {
		return s.executePreparedAssertTemplateValuesAction(ctx, activation, rule, action, actionName, actionIndex)
	}

	values, err := s.evaluateAssertTemplateValuesAction(ctx, activation, rule, action)
	if err != nil {
		return err
	}
	origin := mutationOriginForRuleActivation(rule, activation)
	origin.ActionName = actionName
	origin.ActionIndex = actionIndex
	locked, ok := s.beginMutationForOrigin(origin)
	if !ok {
		return ErrConcurrencyMisuse
	}
	if locked {
		defer s.endMutation()
	}

	if action.insertPlan.outputOnlyNoRetainEligible() {
		state := s.activeFactWorkspace()
		mark := state.markGeneratedFactInsert()
		if action.insertPlan.compactSlots {
			compactSlots, compactSlotMark := state.reserveGeneratedCompactFactSlots(s.revision, len(action.template.fields))
			compactSlots, err := action.template.buildValidatedCompactFieldSlotsFromValuesInto(compactSlots, values)
			if err != nil {
				state.rollbackGeneratedCompactFactSlots(compactSlotMark)
				return err
			}
			inserted, agendaDelta, err := s.insertRuleActionGeneratedCompactFactSlotsImmediate(ctx, &state, &action.insertPlan, compactSlots, mark, compactSlotMark, origin)
			if err != nil {
				return err
			}
			if inserted && action.insertPlan.affectsRete {
				if !s.canMutateDuringRun(origin) {
					_, err = s.reconcileAgendaAfterMutation(ctx, agendaDelta)
					return err
				}
				if err := s.recordRunAgendaDelta(agendaDelta); err != nil {
					return err
				}
			}
			return nil
		}
		fieldSlots, slotMark := state.reserveGeneratedFactSlots(s.revision, len(action.template.fields))
		fieldSlots, err := action.template.buildValidatedFieldSlotsFromValuesInto(fieldSlots, values)
		if err != nil {
			state.rollbackGeneratedFactSlots(slotMark)
			return err
		}
		inserted, agendaDelta, err := s.insertRuleActionGeneratedFactSlotsImmediate(ctx, &state, &action.insertPlan, fieldSlots, mark, slotMark, origin)
		if err != nil {
			return err
		}
		if inserted && action.insertPlan.affectsRete {
			if !s.canMutateDuringRun(origin) {
				_, err = s.reconcileAgendaAfterMutation(ctx, agendaDelta)
				return err
			}
			if err := s.recordRunAgendaDelta(agendaDelta); err != nil {
				return err
			}
		}
		return nil
	}

	_, template, _, inserted, agendaDelta, err := s.insertTemplateValuesImmediate(ctx, action.template.Key(), values, origin)
	if err != nil {
		return err
	}
	if inserted && s.revision.factMayAffectReteByTarget(template.Name(), template.Key()) {
		if !s.canMutateDuringRun(origin) {
			_, err = s.reconcileAgendaAfterMutation(ctx, agendaDelta)
			return err
		}
		if err := s.recordRunAgendaDelta(agendaDelta); err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) executePreparedAssertTemplateValuesAction(ctx context.Context, activation activation, rule compiledRule, action compiledAssertTemplateValuesAction, actionName string, actionIndex int) error {
	origin := mutationOriginForRuleActivation(rule, activation)
	origin.ActionName = actionName
	origin.ActionIndex = actionIndex
	locked, ok := s.beginMutationForOrigin(origin)
	if !ok {
		return ErrConcurrencyMisuse
	}
	if locked {
		defer s.endMutation()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	state := s.activeFactWorkspace()
	mark := state.markGeneratedFactInsert()
	if action.insertPlan.compactSlots {
		slots, slotMark := state.reserveGeneratedCompactFactSlots(s.revision, len(action.template.fields))
		inserter := preparedTemplateValueInserter{template: action.template}

		if !activation.token.isZero() {
			for i, valueSpec := range action.tokenValues {
				value, err := evaluateTokenActionValue(ctx, valueSpec, activation.token, s.globalValues)
				if err != nil {
					state.rollbackGeneratedCompactFactSlots(slotMark)
					return err
				}
				if err := inserter.setPreparedCompactSlot(slots, i, value); err != nil {
					state.rollbackGeneratedCompactFactSlots(slotMark)
					return err
				}
			}
		} else {
			matches, err := s.actionMatchesForActivation(activation, rule)
			if err != nil {
				state.rollbackGeneratedCompactFactSlots(slotMark)
				return err
			}
			for i, expression := range action.values {
				value, ok, evalErr := expression.evaluateWithContextParamsGlobalsAndCounters(ctx, conditionFactRef{}, matches, nil, s.globalValues, nil, nil)
				if evalErr != nil {
					state.rollbackGeneratedCompactFactSlots(slotMark)
					return evalErr
				}
				if !ok {
					state.rollbackGeneratedCompactFactSlots(slotMark)
					return fmt.Errorf("%w: native assert value %d is unavailable", ErrMatcher, i)
				}
				if err := inserter.setPreparedCompactSlot(slots, i, value); err != nil {
					state.rollbackGeneratedCompactFactSlots(slotMark)
					return err
				}
			}
		}

		inserted, agendaDelta, err := s.insertRuleActionGeneratedCompactFactSlotsImmediate(ctx, &state, &action.insertPlan, slots, mark, slotMark, origin)
		if err != nil {
			return err
		}
		if inserted && action.insertPlan.affectsRete {
			if !s.canMutateDuringRun(origin) {
				_, err = s.reconcileAgendaAfterMutation(ctx, agendaDelta)
				return err
			}
			if err := s.recordRunAgendaDelta(agendaDelta); err != nil {
				return err
			}
		}
		return nil
	}

	slots, slotMark := state.reserveGeneratedFactSlots(s.revision, len(action.template.fields))
	inserter := preparedTemplateValueInserter{template: action.template}

	if !activation.token.isZero() {
		for i, valueSpec := range action.tokenValues {
			value, err := evaluateTokenActionValue(ctx, valueSpec, activation.token, s.globalValues)
			if err != nil {
				state.rollbackGeneratedFactSlots(slotMark)
				return err
			}
			if err := inserter.setPreparedSlot(slots, i, value); err != nil {
				state.rollbackGeneratedFactSlots(slotMark)
				return err
			}
		}
	} else {
		matches, err := s.actionMatchesForActivation(activation, rule)
		if err != nil {
			state.rollbackGeneratedFactSlots(slotMark)
			return err
		}
		for i, expression := range action.values {
			value, ok, evalErr := expression.evaluateWithContextParamsGlobalsAndCounters(ctx, conditionFactRef{}, matches, nil, s.globalValues, nil, nil)
			if evalErr != nil {
				state.rollbackGeneratedFactSlots(slotMark)
				return evalErr
			}
			if !ok {
				state.rollbackGeneratedFactSlots(slotMark)
				return fmt.Errorf("%w: native assert value %d is unavailable", ErrMatcher, i)
			}
			if err := inserter.setPreparedSlot(slots, i, value); err != nil {
				state.rollbackGeneratedFactSlots(slotMark)
				return err
			}
		}
	}

	inserted, agendaDelta, err := s.insertRuleActionGeneratedFactSlotsImmediate(ctx, &state, &action.insertPlan, slots, mark, slotMark, origin)
	if err != nil {
		return err
	}
	if inserted && action.insertPlan.affectsRete {
		if !s.canMutateDuringRun(origin) {
			_, err = s.reconcileAgendaAfterMutation(ctx, agendaDelta)
			return err
		}
		if err := s.recordRunAgendaDelta(agendaDelta); err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) evaluateAssertTemplateValuesAction(ctx context.Context, activation activation, rule compiledRule, action compiledAssertTemplateValuesAction) ([]Value, error) {
	if len(action.values) == 0 {
		return s.actionValueScratch[:0], nil
	}
	values := s.actionValueScratch
	if cap(values) < len(action.values) {
		values = make([]Value, len(action.values))
	} else {
		values = values[:len(action.values)]
	}
	var err error
	if !activation.token.isZero() {
		for i, valueSpec := range action.tokenValues {
			values[i], err = evaluateTokenActionValue(ctx, valueSpec, activation.token, s.globalValues)
			if err != nil {
				s.actionValueScratch = values
				return nil, err
			}
		}
		s.actionValueScratch = values
		return values, nil
	}

	matches, err := s.actionMatchesForActivation(activation, rule)
	if err != nil {
		s.actionValueScratch = values
		return nil, err
	}
	for i, expression := range action.values {
		value, ok, evalErr := expression.evaluateWithContextParamsGlobalsAndCounters(ctx, conditionFactRef{}, matches, nil, s.globalValues, nil, nil)
		if evalErr != nil {
			s.actionValueScratch = values
			return nil, evalErr
		}
		if !ok {
			s.actionValueScratch = values
			return nil, fmt.Errorf("%w: native assert value %d is unavailable", ErrMatcher, i)
		}
		values[i] = value
	}
	s.actionValueScratch = values
	return values, nil
}

func evaluateNativeActionExpressionWithToken(ctx context.Context, expression compiledExpression, token tokenRef, globals []Value) (Value, error) {
	value, ok, err := expression.evaluateTokenWithContextParamsGlobalsOffsetAndCounters(ctx, conditionFactRef{}, token, nil, globals, 0, nil, nil)
	if err != nil {
		return Value{}, err
	}
	if !ok {
		return Value{}, fmt.Errorf("%w: native assert value is unavailable", ErrMatcher)
	}
	return value, nil
}

func evaluateTokenActionValue(ctx context.Context, value compiledTokenActionValue, token tokenRef, globals []Value) (Value, error) {
	switch value.kind {
	case compiledTokenActionValueConst:
		return value.value, nil
	case compiledTokenActionValueBindingField:
		resolved, ok := tokenActionBindingFieldValue(token, value.bindingSlot, value.access)
		if !ok {
			return Value{}, fmt.Errorf("%w: native assert value is unavailable", ErrMatcher)
		}
		return resolved, nil
	case compiledTokenActionValueBindingValue:
		resolved, ok := tokenActionBindingValue(token, value.bindingSlot)
		if !ok {
			return Value{}, fmt.Errorf("%w: native assert value is unavailable", ErrMatcher)
		}
		return resolved, nil
	case compiledTokenActionValueStringCall2ConstBindingField:
		resolved, ok := tokenActionBindingFieldValue(token, value.bindingSlot, value.access)
		if !ok {
			return Value{}, fmt.Errorf("%w: native assert value is unavailable", ErrMatcher)
		}
		return evaluateTokenActionStringCall2(ctx, value, resolved)
	default:
		return evaluateNativeActionExpressionWithToken(ctx, value.expression, token, globals)
	}
}

func tokenActionBindingFieldValue(token tokenRef, bindingSlot int, access compiledPathAccess) (Value, bool) {
	match, ok := tokenRefAtSlot(token, bindingSlot)
	if !ok || match.hasValue {
		return Value{}, false
	}
	if access.rootSlot < 0 {
		return Value{}, false
	}
	value, ok := match.fact.compiledFieldValue(access.root, access.rootSlot)
	return value, ok
}

func tokenActionBindingValue(token tokenRef, bindingSlot int) (Value, bool) {
	match, ok := tokenRefAtSlot(token, bindingSlot)
	if !ok || !match.hasValue {
		return Value{}, false
	}
	return match.value, true
}

func evaluateTokenActionStringCall2(ctx context.Context, value compiledTokenActionValue, arg Value) (out Value, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Value{}, functionEvaluationError(nil, value.function.name, err)
	}
	if !expressionKindAssignable(ValueString, arg.Kind()) {
		return Value{}, functionEvaluationError(nil, value.function.name, fmt.Errorf("argument %d has kind %s, want %s", 1, arg.Kind(), ValueString))
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			out = Value{}
			err = functionEvaluationError(nil, value.function.name, fmt.Errorf("panic: %v", recovered))
		}
	}()
	out, err = value.function.fn2(ctx, value.value, arg)
	if err != nil {
		return Value{}, functionEvaluationError(nil, value.function.name, err)
	}
	if err := ctx.Err(); err != nil {
		return Value{}, functionEvaluationError(nil, value.function.name, err)
	}
	if out.Kind() == ValueNull {
		out = NullValue()
	}
	if !expressionKindAssignable(value.function.ret, out.Kind()) {
		return Value{}, functionEvaluationError(nil, value.function.name, fmt.Errorf("return has kind %s, want %s", out.Kind(), value.function.ret))
	}
	return out, nil
}

func (s *Session) actionMatchesForActivation(activation activation, rule compiledRule) ([]conditionMatch, error) {
	matches := s.actionMatchScratch
	if cap(matches) < len(rule.conditions) {
		matches = make([]conditionMatch, len(rule.conditions))
	} else {
		for i := range matches {
			matches[i] = conditionMatch{}
		}
		matches = matches[:len(rule.conditions)]
	}
	for i, condition := range rule.conditions {
		matches[i].conditionID = condition.IDValue
		matches[i].bindingSlot = i
	}
	if entries := activation.bindings(); len(entries) > 0 {
		for _, entry := range entries {
			slot := entry.bindingSlot
			if slot < 0 || slot >= len(rule.conditions) {
				continue
			}
			matches[slot] = conditionMatch{
				conditionID: entry.conditionID,
				bindingSlot: slot,
				value:       cloneValue(entry.value),
				hasValue:    entry.hasValue,
			}
			if entry.hasValue || entry.factID.IsZero() {
				continue
			}
			fact, ok := s.workingFactByID(entry.factID)
			if !ok {
				s.actionMatchScratch = matches
				return nil, fmt.Errorf("%w: missing fact %q for activation %q", ErrMatcher, entry.factID, activation.activationID())
			}
			if fact.id.Generation() != activation.Generation() || fact.version != entry.factVersion {
				s.actionMatchScratch = matches
				return nil, fmt.Errorf("%w: stale fact %q for activation %q", ErrMatcher, entry.factID, activation.activationID())
			}
			condition := rule.conditions[slot]
			matches[slot].fact = newConditionFactRefFromWorkingFactForTarget(fact, conditionTarget{
				kind:        conditionTargetKindForRuleCondition(condition),
				name:        condition.NameValue,
				templateKey: condition.TemplateKeyValue,
				templateID:  fact.templateID,
			}, s.compactSlotStore)
		}
		s.actionMatchScratch = matches
		return matches, nil
	}
	factIDs := cloneActivationFactIDs(&activation)
	factVersions := cloneActivationFactVersions(&activation)
	for i, condition := range rule.conditions {
		if i >= len(factIDs) || i >= len(factVersions) {
			s.actionMatchScratch = matches
			return nil, fmt.Errorf("%w: malformed activation for rule %q", ErrMatcher, rule.name)
		}
		factID := factIDs[i]
		if factID.IsZero() {
			continue
		}
		fact, ok := s.workingFactByID(factID)
		if !ok {
			s.actionMatchScratch = matches
			return nil, fmt.Errorf("%w: missing fact %q for activation %q", ErrMatcher, factID, activation.activationID())
		}
		if fact.id.Generation() != activation.Generation() || fact.version != factVersions[i] {
			s.actionMatchScratch = matches
			return nil, fmt.Errorf("%w: stale fact %q for activation %q", ErrMatcher, factID, activation.activationID())
		}
		matches[i] = conditionMatch{
			conditionID: condition.IDValue,
			bindingSlot: i,
			fact: newConditionFactRefFromWorkingFactForTarget(fact, conditionTarget{
				kind:        conditionTargetKindForRuleCondition(condition),
				name:        condition.NameValue,
				templateKey: condition.TemplateKeyValue,
				templateID:  fact.templateID,
			}, s.compactSlotStore),
		}
	}
	s.actionMatchScratch = matches
	return matches, nil
}

func (s *Session) actionContextForActivation(ctx context.Context, activation activation) (actionContext, error) {
	return s.actionContextForActivationWithScratch(ctx, activation, false)
}

func (s *Session) actionContextForActivationWithScratch(ctx context.Context, activation activation, useScratch bool) (actionContext, error) {
	return s.actionContextForActivationWithScratchTrusted(ctx, activation, useScratch, false)
}

func (s *Session) actionContextForActivationWithScratchTrusted(ctx context.Context, activation activation, useScratch bool, trustTokenActivation bool) (actionContext, error) {
	if s == nil {
		return actionContext{}, ErrClosedSession
	}
	if s.closed {
		return actionContext{}, ErrClosedSession
	}
	if s.revision == nil {
		return actionContext{}, ErrInvalidRuleset
	}

	rule, ok := s.revision.rulesByRevisionID[activation.ruleRevisionID]
	if !ok {
		return actionContext{}, fmt.Errorf("%w: unknown rule revision %q", ErrMatcher, activation.ruleRevisionID)
	}
	factCount := activationFactCount(&activation)
	if activation.token.isZero() && len(activation.bindings()) == 0 && (factCount != activationFactVersionCount(&activation) || factCount != len(rule.conditions)) {
		return actionContext{}, fmt.Errorf("%w: malformed activation for rule %q", ErrMatcher, rule.name)
	}
	if !activation.token.isZero() {
		if err := s.validateActivationTokenFacts(rule, activation, trustTokenActivation); err != nil {
			return actionContext{}, err
		}
		if useScratch {
			return newTokenActionContextWithBindingState(ctx, s, activation, rule, &s.actionBindingScratch), nil
		}
		return newTokenActionContext(ctx, s, activation, rule), nil
	}

	entries := cloneBindingTupleEntries(activation.bindings())
	if len(entries) == 0 {
		entries = activationBindingTupleEntriesForActivation(rule, &activation, false)
	}
	for i, entry := range entries {
		if entry.hasValue || entry.factID.IsZero() {
			continue
		}
		fact, ok := s.workingFactByID(entry.factID)
		if !ok {
			return actionContext{}, fmt.Errorf("%w: missing fact %q for activation %q", ErrMatcher, entry.factID, activation.activationID())
		}
		if fact.id.Generation() != activation.Generation() || fact.version != entry.factVersion {
			return actionContext{}, fmt.Errorf("%w: stale fact %q for activation %q", ErrMatcher, entry.factID, activation.activationID())
		}
		entries[i] = entry
	}

	out := newActionContext(ctx, s, activation, entries)
	out.ruleID = rule.id
	return out, nil
}

func (s *Session) validateActivationTokenFacts(rule compiledRule, activation activation, trustTokenActivation bool) error {
	if !activation.token.isZero() {
		if trustTokenActivation {
			return nil
		}
		for i := range rule.conditions {
			match, ok := tokenRefAtSlot(activation.token, i)
			if !ok {
				return fmt.Errorf("%w: malformed token activation for rule %q", ErrMatcher, rule.name)
			}
			if match.hasValue {
				continue
			}
			factID := match.fact.ID()
			if factID.IsZero() || factID.Sequence() == 0 {
				continue
			}
			fact, ok := s.workingFactByID(factID)
			if !ok {
				return fmt.Errorf("%w: missing fact %q for activation %q", ErrMatcher, factID, activation.activationID())
			}
			if fact.id.Generation() != activation.Generation() || fact.version != match.fact.Version() {
				return fmt.Errorf("%w: stale fact %q for activation %q", ErrMatcher, factID, activation.activationID())
			}
		}
		return nil
	}
	entries := activation.bindings()
	if len(entries) == 0 {
		entries = activationBindingTupleEntriesForActivation(rule, &activation, false)
	}
	for _, entry := range entries {
		if entry.hasValue || entry.factID.IsZero() {
			continue
		}
		fact, ok := s.workingFactByID(entry.factID)
		if !ok {
			return fmt.Errorf("%w: missing fact %q for activation %q", ErrMatcher, entry.factID, activation.activationID())
		}
		if fact.id.Generation() != activation.Generation() || fact.version != entry.factVersion {
			return fmt.Errorf("%w: stale fact %q for activation %q", ErrMatcher, entry.factID, activation.activationID())
		}
	}
	return nil
}
