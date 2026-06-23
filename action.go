package gess

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

type ActionFunc func(ActionContext) error

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

type ActionContext struct {
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
}

func newActionContext(ctx context.Context, session *Session, activation activation, entries []bindingTupleEntry) ActionContext {
	if ctx == nil {
		ctx = context.Background()
	}

	out := ActionContext{
		ctx:            ctx,
		session:        session,
		activationID:   activation.id,
		activationKey:  activation.identity.key,
		activationOrd:  activation.publicOrdinal,
		ruleID:         activation.ruleID,
		ruleRevisionID: activation.ruleRevisionID,
		generation:     activation.generation,
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

func newTokenActionContext(ctx context.Context, session *Session, activation activation, rule compiledRule) ActionContext {
	out := newActionContext(ctx, session, activation, nil)
	if !activation.token.isZero() {
		bindings := &actionContextBindingState{}
		bindings.resetToken(rule, activation.token)
		out.bindings = bindings
	}
	return out
}

func newTokenActionContextWithBindingState(ctx context.Context, session *Session, activation activation, rule compiledRule, bindings *actionContextBindingState) ActionContext {
	out := newActionContext(ctx, session, activation, nil)
	if !activation.token.isZero() && bindings != nil {
		bindings.resetToken(rule, activation.token)
		out.bindings = bindings
	}
	return out
}

func (c ActionContext) Context() context.Context {
	if c.ctx != nil {
		return c.ctx
	}
	return context.Background()
}

func (c ActionContext) SessionID() SessionID {
	return c.sessionID
}

func (c ActionContext) RulesetID() RulesetID {
	return c.rulesetID
}

func (c ActionContext) ActivationID() ActivationID {
	if !c.activationID.IsZero() {
		return c.activationID
	}
	return activationIDForIdentityKey(c.activationKey, c.activationOrd)
}

func (c ActionContext) RuleID() RuleID {
	return c.ruleID
}

func (c ActionContext) RuleRevisionID() RuleRevisionID {
	return c.ruleRevisionID
}

func (c ActionContext) Generation() Generation {
	return c.generation
}

func (c ActionContext) BoundFacts() []FactSnapshot {
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

func (c ActionContext) Binding(name string) (FactSnapshot, bool) {
	if name == "" || c.bindings == nil {
		return FactSnapshot{}, false
	}
	if index, ok := c.bindings.bindingIndex(name); ok {
		return c.materializeBinding(index)
	}
	return FactSnapshot{}, false
}

func (c ActionContext) BindingValue(name string) (Value, bool) {
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
func (c ActionContext) BindingScalarValue(name, field string) (Value, bool) {
	return c.bindingScalarValue(name, field)
}

func (c ActionContext) bindingScalarValue(name, field string) (Value, bool) {
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

func (c ActionContext) bindingScalarValueAt(bindingSlot int, field string) (Value, bool) {
	if field == "" || c.bindings == nil || bindingSlot < 0 || bindingSlot >= c.bindings.len() {
		return Value{}, false
	}
	c.bindings.mu.Lock()
	defer c.bindings.mu.Unlock()
	return c.bindingScalarValueLocked(bindingSlot, field)
}

func (c ActionContext) bindingScalarValueAtSlot(bindingSlot, fieldSlot int) (Value, bool) {
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

func (c ActionContext) Assert(name string, fields Fields) (AssertResult, error) {
	if c.session == nil {
		return AssertResult{Status: AssertClosed}, ErrClosedSession
	}
	return c.session.insertFactWithContextAndOrigin(c.Context(), name, "", fields, c.mutationOrigin())
}

func (c ActionContext) AssertTemplate(templateKey TemplateKey, fields Fields) (AssertResult, error) {
	if c.session == nil {
		return AssertResult{Status: AssertClosed}, ErrClosedSession
	}
	return c.session.insertFactWithContextAndOrigin(c.Context(), "", templateKey, fields, c.mutationOrigin())
}

func (c ActionContext) AssertLogical(name string, fields Fields) (AssertResult, error) {
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

// AssertTemplateValues asserts a fixed-template fact using values in template
// field order and returns only whether the effect succeeded. It is intended for
// generated facts where callers do not need an AssertResult.
func (c ActionContext) AssertTemplateValues(templateKey TemplateKey, values ...Value) error {
	if c.session == nil {
		return ErrClosedSession
	}
	return c.session.insertTemplateValuesWithContextAndOrigin(c.Context(), templateKey, values, c.mutationOrigin())
}

func (c ActionContext) Modify(id FactID, patch FactPatch) (ModifyResult, error) {
	if c.session == nil {
		return ModifyResult{Status: ModifyClosed}, ErrClosedSession
	}
	if err := c.materializeAllBindings(); err != nil {
		return ModifyResult{Status: ModifyMissing}, err
	}
	return c.session.modifyWithContextAndOrigin(c.Context(), id, patch, c.mutationOrigin())
}

func (c ActionContext) Retract(id FactID) (RetractResult, error) {
	if c.session == nil {
		return RetractResult{Status: RetractClosed}, ErrClosedSession
	}
	if err := c.materializeAllBindings(); err != nil {
		return RetractResult{Status: RetractMissing}, err
	}
	return c.session.retractWithContextAndOrigin(c.Context(), id, c.mutationOrigin())
}

func (c ActionContext) supportingFactIDs() []FactID {
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

func (c ActionContext) mutationOrigin() mutationOrigin {
	return mutationOrigin{
		ActivationID:          c.activationID,
		RuleID:                c.ruleID,
		RuleRevisionID:        c.ruleRevisionID,
		activationIdentityKey: c.activationKey,
		activationOrdinal:     c.activationOrd,
	}
}

func (c ActionContext) materializeAllBindings() error {
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

func (c ActionContext) materializeBinding(index int) (FactSnapshot, bool) {
	if c.bindings == nil || index < 0 || index >= c.bindings.len() {
		return FactSnapshot{}, false
	}
	c.bindings.mu.Lock()
	defer c.bindings.mu.Unlock()
	return c.materializeBindingLocked(index)
}

func (c ActionContext) bindingScalarValueLocked(index int, field string) (Value, bool) {
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

func (c ActionContext) bindingScalarValueLive(index int, field string) (Value, bool) {
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
	template, ok := c.session.revision.templateByKey(fact.templateKey)
	if !ok || !template.closed {
		return Value{}, false
	}
	slot, ok := template.fieldSlot(field)
	if !ok || slot < 0 || slot >= len(fact.fieldSlots) {
		return Value{}, false
	}
	resolved := fact.fieldSlots[slot]
	if !resolved.ok || !valueShareable(resolved.value) {
		return Value{}, false
	}
	return resolved.value, true
}

func (c ActionContext) bindingScalarValueLiveAtSlot(index, fieldSlot int) (Value, bool) {
	if c.bindings == nil || index < 0 || index >= c.bindings.len() || fieldSlot < 0 {
		return Value{}, false
	}
	if c.session == nil {
		return Value{}, false
	}
	entry := c.bindings.entryAt(index)
	fact, ok := c.session.workingFactByID(entry.factID)
	if !ok || fact == nil {
		return Value{}, false
	}
	if fact.id.Generation() != c.generation || fact.version != entry.factVersion || fieldSlot >= len(fact.fieldSlots) {
		return Value{}, false
	}
	resolved := fact.fieldSlots[fieldSlot]
	if !resolved.ok || !valueShareable(resolved.value) {
		return Value{}, false
	}
	return resolved.value, true
}

func (c ActionContext) materializeBindingLocked(index int) (FactSnapshot, bool) {
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
	snapshot := fact.detachedSnapshotForRevision(c.session.revision)
	c.bindings.snapshots[index] = snapshot
	return snapshot, true
}

func (s *actionContextBindingState) bindingIndex(name string) (int, bool) {
	if s == nil {
		return 0, false
	}
	if !s.token.isZero() {
		for i, condition := range s.conditions {
			if condition.binding == name {
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
			binding:        condition.binding,
			bindingSlot:    match.bindingSlot,
			conditionOrder: condition.order,
			conditionID:    condition.id,
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
	if !ok || slot < 0 || slot >= len(fact.fieldSlots) {
		return Value{}, false
	}
	resolved := fact.fieldSlots[slot]
	if !resolved.ok || !valueShareable(resolved.value) {
		return Value{}, false
	}
	return resolved.value, true
}

type ActionSpec struct {
	Name string
	Fn   ActionFunc
	// NonEscaping allows the engine to skip freezing unread bindings after a
	// rule fires. Set it only when Fn does not retain ActionContext or any
	// binding-derived data that depends on post-return defensive snapshots.
	NonEscaping bool
}

func (s ActionSpec) clone() ActionSpec {
	out := s
	out.Name = strings.TrimSpace(out.Name)
	return out
}

func normalizeActionSpec(spec ActionSpec) (ActionSpec, error) {
	normalized := spec.clone()
	if normalized.Name == "" {
		return ActionSpec{}, &ValidationError{
			Reason: "action name is required",
		}
	}
	if normalized.Fn == nil {
		return ActionSpec{}, &ValidationError{
			Reason: "action function is required",
		}
	}
	return normalized, nil
}

type Action struct {
	name  string
	order int
}

func (a Action) Name() string {
	return a.name
}

func (a Action) DeclarationOrder() int {
	return a.order
}

func (a Action) clone() Action {
	return a
}

type compiledAction struct {
	name              string
	fn                ActionFunc
	order             int
	skipBindingFreeze bool
}

func compileActionSpec(spec ActionSpec, order int) (compiledAction, error) {
	normalized, err := normalizeActionSpec(spec)
	if err != nil {
		return compiledAction{}, err
	}

	return compiledAction{
		name:              normalized.Name,
		fn:                normalized.Fn,
		order:             order,
		skipBindingFreeze: normalized.NonEscaping,
	}, nil
}

func (a compiledAction) inspect() Action {
	return Action{
		name:  a.name,
		order: a.order,
	}
}

func (a compiledAction) clone() compiledAction {
	return a
}

func (s *Session) executeActivationActions(ctx context.Context, runID RunID, activation activation) (err error) {
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
	if rule.id != activation.ruleID {
		return fmt.Errorf("%w: rule metadata mismatch for revision %q", ErrMatcher, activation.ruleRevisionID)
	}

	skipBindingFreeze := rule.allActionsSkipBindingFreeze
	actionCtx, err := s.actionContextForActivationWithScratch(ctx, activation, skipBindingFreeze)
	if err != nil {
		return err
	}
	defer func() {
		if skipBindingFreeze {
			return
		}
		if freezeErr := actionCtx.materializeAllBindings(); err == nil && freezeErr != nil {
			err = freezeErr
		}
	}()

	for _, actionSpec := range rule.actions {
		if err := ctx.Err(); err != nil {
			return err
		}
		action, ok := s.revision.actions[actionSpec.name]
		if !ok {
			return fmt.Errorf("%w: missing action %q", ErrInvalidRuleset, actionSpec.name)
		}
		if err := action.fn(actionCtx); err != nil {
			_, _ = s.removeLogicalSupportsForSources(ctx, []logicalSupportSourceKey{logicalSupportSourceFromActivation(activation)}, activation.mutationOrigin())
			return &ActionFailureError{
				RunID:          runID,
				RuleID:         activation.ruleID,
				RuleRevisionID: activation.ruleRevisionID,
				ActivationID:   activation.activationID(),
				ActionName:     actionSpec.name,
				ActionIndex:    actionSpec.order,
				Err:            err,
			}
		}
	}

	return nil
}

func (s *Session) actionContextForActivation(ctx context.Context, activation activation) (ActionContext, error) {
	return s.actionContextForActivationWithScratch(ctx, activation, false)
}

func (s *Session) actionContextForActivationWithScratch(ctx context.Context, activation activation, useScratch bool) (ActionContext, error) {
	if s == nil {
		return ActionContext{}, ErrClosedSession
	}
	if s.closed {
		return ActionContext{}, ErrClosedSession
	}
	if s.revision == nil {
		return ActionContext{}, ErrInvalidRuleset
	}

	rule, ok := s.revision.rulesByRevisionID[activation.ruleRevisionID]
	if !ok {
		return ActionContext{}, fmt.Errorf("%w: unknown rule revision %q", ErrMatcher, activation.ruleRevisionID)
	}
	if rule.id != activation.ruleID {
		return ActionContext{}, fmt.Errorf("%w: rule metadata mismatch for revision %q", ErrMatcher, activation.ruleRevisionID)
	}
	factCount := activationFactCount(&activation)
	if activation.token.isZero() && len(activation.bindings) == 0 && (factCount != activationFactVersionCount(&activation) || factCount != len(rule.conditions)) {
		return ActionContext{}, fmt.Errorf("%w: malformed activation for rule %q", ErrMatcher, rule.name)
	}
	if !activation.token.isZero() {
		if err := s.validateActivationTokenFacts(rule, activation); err != nil {
			return ActionContext{}, err
		}
		if useScratch {
			return newTokenActionContextWithBindingState(ctx, s, activation, rule, &s.actionBindingScratch), nil
		}
		return newTokenActionContext(ctx, s, activation, rule), nil
	}

	entries := cloneBindingTupleEntries(activation.bindings)
	if len(entries) == 0 {
		entries = activationBindingTupleEntriesForActivation(rule, &activation, false)
	}
	for i, entry := range entries {
		if entry.hasValue || entry.factID.IsZero() {
			continue
		}
		fact, ok := s.workingFactByID(entry.factID)
		if !ok {
			return ActionContext{}, fmt.Errorf("%w: missing fact %q for activation %q", ErrMatcher, entry.factID, activation.activationID())
		}
		if fact.id.Generation() != activation.generation || fact.version != entry.factVersion {
			return ActionContext{}, fmt.Errorf("%w: stale fact %q for activation %q", ErrMatcher, entry.factID, activation.activationID())
		}
		entries[i] = entry
	}

	return newActionContext(ctx, s, activation, entries), nil
}

func (s *Session) validateActivationTokenFacts(rule compiledRule, activation activation) error {
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
		if fact.id.Generation() != activation.generation || fact.version != match.fact.Version() {
			return fmt.Errorf("%w: stale fact %q for activation %q", ErrMatcher, factID, activation.activationID())
		}
	}
	return nil
}
