package engine

import (
	"context"

	gessrules "github.com/cpcf/gess/rules"
)

type actionContextHandle struct {
	ctx   actionContext
	valid bool
}

func wrapActionContext(ctx actionContext) ActionContext {
	return gessrules.NewActionContext(actionContextHandle{ctx: ctx, valid: true})
}

func unwrapActionContext(ctx ActionContext) (*actionContext, bool) {
	handle, ok := gessrules.ActionContextHandleOf(ctx).(actionContextHandle)
	if !ok || !handle.valid {
		return nil, false
	}
	return &handle.ctx, true
}

func (h actionContextHandle) SetRHSBind(name string, value Value) {
	if !h.valid {
		return
	}
	h.ctx.SetRHSBind(name, value)
}

func (h actionContextHandle) RHSBind(name string) (Value, bool) {
	if !h.valid {
		return Value{}, false
	}
	return h.ctx.RHSBind(name)
}

func (h actionContextHandle) Context() context.Context {
	if !h.valid {
		return context.Background()
	}
	return h.ctx.Context()
}

func (h actionContextHandle) SessionID() SessionID {
	if !h.valid {
		return ""
	}
	return h.ctx.SessionID()
}

func (h actionContextHandle) RulesetID() RulesetID {
	if !h.valid {
		return ""
	}
	return h.ctx.RulesetID()
}

func (h actionContextHandle) ActivationID() ActivationID {
	if !h.valid {
		return ""
	}
	return h.ctx.ActivationID()
}

func (h actionContextHandle) RuleID() RuleID {
	if !h.valid {
		return ""
	}
	return h.ctx.RuleID()
}

func (h actionContextHandle) RuleRevisionID() RuleRevisionID {
	if !h.valid {
		return ""
	}
	return h.ctx.RuleRevisionID()
}

func (h actionContextHandle) Generation() Generation {
	if !h.valid {
		return 0
	}
	return h.ctx.Generation()
}

func (h actionContextHandle) BoundFacts() []gessrules.FactSnapshot {
	if !h.valid {
		return nil
	}
	return publicFactSnapshots(h.ctx.BoundFacts())
}

func (h actionContextHandle) Binding(name string) (gessrules.FactSnapshot, bool) {
	if !h.valid {
		return gessrules.FactSnapshot{}, false
	}
	fact, ok := h.ctx.Binding(name)
	if !ok {
		return gessrules.FactSnapshot{}, false
	}
	return publicFactSnapshot(fact), true
}

func (h actionContextHandle) BindingID(name string) (FactID, bool) {
	if !h.valid {
		return FactID{}, false
	}
	return h.ctx.BindingID(name)
}

func (h actionContextHandle) BindingValue(name string) (Value, bool) {
	if !h.valid {
		return Value{}, false
	}
	return h.ctx.BindingValue(name)
}

func (h actionContextHandle) BindingScalarValue(name, field string) (Value, bool) {
	if !h.valid {
		return Value{}, false
	}
	return h.ctx.BindingScalarValue(name, field)
}

func (h actionContextHandle) Global(name string) (Value, bool) {
	if !h.valid {
		return Value{}, false
	}
	return h.ctx.Global(name)
}

func (h actionContextHandle) Assert(templateKey TemplateKey, fields Fields) (gessrules.AssertResult, error) {
	if !h.valid {
		return gessrules.AssertResult{Status: gessrules.AssertClosed}, ErrClosedSession
	}
	result, err := h.ctx.Assert(templateKey, fields)
	return publicAssertResult(result), err
}

func (h actionContextHandle) AssertLogical(templateKey TemplateKey, fields Fields) (gessrules.AssertResult, error) {
	if !h.valid {
		return gessrules.AssertResult{Status: gessrules.AssertClosed}, ErrClosedSession
	}
	result, err := h.ctx.AssertLogical(templateKey, fields)
	return publicAssertResult(result), err
}

func (h actionContextHandle) AssertTemplateValues(templateKey TemplateKey, values ...Value) error {
	if !h.valid {
		return ErrClosedSession
	}
	return h.ctx.AssertTemplateValues(templateKey, values...)
}

func (h actionContextHandle) Modify(id FactID, patch FactPatch) (gessrules.ModifyResult, error) {
	if !h.valid {
		return gessrules.ModifyResult{Status: gessrules.ModifyClosed}, ErrClosedSession
	}
	result, err := h.ctx.Modify(id, patch)
	return publicModifyResult(result), err
}

func (h actionContextHandle) Retract(id FactID) (gessrules.RetractResult, error) {
	if !h.valid {
		return gessrules.RetractResult{Status: gessrules.RetractClosed}, ErrClosedSession
	}
	result, err := h.ctx.Retract(id)
	return publicRetractResult(result), err
}

func (h actionContextHandle) Halt() error {
	if !h.valid {
		return ErrClosedSession
	}
	return h.ctx.Halt()
}

func (h actionContextHandle) Emit(values ...Value) error {
	if !h.valid {
		return ErrClosedSession
	}
	return h.ctx.Emit(values...)
}

func (h actionContextHandle) PushFocus(module ModuleName) error {
	if !h.valid {
		return ErrClosedSession
	}
	return h.ctx.PushFocus(module)
}

func (h actionContextHandle) SetFocus(module ModuleName) error {
	if !h.valid {
		return ErrClosedSession
	}
	return h.ctx.SetFocus(module)
}

func (h actionContextHandle) PopFocus() (ModuleName, error) {
	if !h.valid {
		return MainModule, ErrClosedSession
	}
	return h.ctx.PopFocus()
}

func (h actionContextHandle) ClearFocusStack() error {
	if !h.valid {
		return ErrClosedSession
	}
	return h.ctx.ClearFocusStack()
}

func publicFactSnapshots(facts []FactSnapshot) []gessrules.FactSnapshot {
	out := make([]gessrules.FactSnapshot, len(facts))
	for i, fact := range facts {
		out[i] = publicFactSnapshot(fact)
	}
	return out
}

func publicFactSnapshot(fact FactSnapshot) gessrules.FactSnapshot {
	return gessrules.FactSnapshot{
		IDValue:             fact.ID(),
		NameValue:           fact.Name(),
		TemplateKeyValue:    fact.TemplateKey(),
		VersionValue:        fact.Version(),
		RecencyValue:        fact.Recency(),
		GenerationValue:     fact.Generation(),
		FieldValues:         fact.Fields(),
		FieldPresenceValues: fact.FieldPresenceMap(),
		SupportValue:        publicFactSupportProvenance(fact.Support()),
	}
}

func publicFactSnapshotPtr(fact *FactSnapshot) *gessrules.FactSnapshot {
	if fact == nil {
		return nil
	}
	out := publicFactSnapshot(*fact)
	return &out
}

func publicFactSupportProvenance(support FactSupportProvenance) gessrules.FactSupportProvenance {
	return gessrules.FactSupportProvenance{
		State: gessrules.FactSupportState(support.State),
	}
}

func publicFieldChanges(changes []FieldChange) []gessrules.FieldChange {
	out := make([]gessrules.FieldChange, len(changes))
	for i, change := range changes {
		out[i] = gessrules.FieldChange{
			Field: change.Field,
			Old:   cloneValue(change.Old),
			New:   cloneValue(change.New),
		}
	}
	return out
}

func publicMutationDelta(delta MutationDelta) gessrules.MutationDelta {
	return gessrules.MutationDelta{
		Kind:           gessrules.MutationKind(delta.Kind),
		Generation:     delta.Generation,
		OldGeneration:  delta.OldGeneration,
		ActivationID:   delta.ActivationID,
		RuleID:         delta.RuleID,
		RuleRevisionID: delta.RuleRevisionID,
		SupportBefore:  publicFactSupportProvenance(delta.SupportBefore),
		SupportAfter:   publicFactSupportProvenance(delta.SupportAfter),
		Recency:        delta.Recency,
		FactID:         delta.FactID,
		OldVersion:     delta.OldVersion,
		NewVersion:     delta.NewVersion,
		Before:         publicFactSnapshotPtr(delta.Before),
		After:          publicFactSnapshotPtr(delta.After),
		OldDuplicate:   gessrules.DuplicateKey(delta.OldDuplicate),
		NewDuplicate:   gessrules.DuplicateKey(delta.NewDuplicate),
		ChangedFields:  publicFieldChanges(delta.ChangedFields),
	}
}

func publicMutationDeltaPtr(delta *MutationDelta) *gessrules.MutationDelta {
	if delta == nil {
		return nil
	}
	out := publicMutationDelta(*delta)
	return &out
}

func publicAssertResult(result AssertResult) gessrules.AssertResult {
	return gessrules.AssertResult{
		Status:       gessrules.AssertStatus(result.Status),
		Fact:         publicFactSnapshot(result.Fact),
		DuplicateKey: gessrules.DuplicateKey(result.DuplicateKey),
		Delta:        publicMutationDeltaPtr(result.Delta),
	}
}

func publicModifyResult(result ModifyResult) gessrules.ModifyResult {
	return gessrules.ModifyResult{
		Status: gessrules.ModifyStatus(result.Status),
		Fact:   publicFactSnapshot(result.Fact),
		Delta:  publicMutationDeltaPtr(result.Delta),
	}
}

func publicRetractResult(result RetractResult) gessrules.RetractResult {
	return gessrules.RetractResult{
		Status: gessrules.RetractStatus(result.Status),
		Fact:   publicFactSnapshot(result.Fact),
		Delta:  publicMutationDeltaPtr(result.Delta),
	}
}
