package engine

type factFieldEqualKey struct {
	target    conditionTarget
	fieldSlot int
	value     reteGraphAlphaRouteValue
}

func newFactFieldEqualKey(target conditionTarget, fieldSlot int, value reteGraphAlphaRouteValue) factFieldEqualKey {
	return factFieldEqualKey{
		target:    target,
		fieldSlot: fieldSlot,
		value:     value,
	}
}

func factSnapshotMatchesFieldEqualIndex(fact FactSnapshot, fieldSlot int, value reteGraphAlphaRouteValue) bool {
	fieldValue, ok := fact.compiledFieldValue("", fieldSlot)
	return valueMatchesFieldEqualIndex(fieldValue, ok, value)
}

func workingFactMatchesFieldEqualIndex(fact *workingFact, fieldSlot int, value reteGraphAlphaRouteValue, compactSlotStore *factCompactSlotStore) bool {
	if fact == nil {
		return false
	}
	fieldValue, ok := fact.compiledFieldValue("", fieldSlot, compactSlotStore)
	return valueMatchesFieldEqualIndex(fieldValue, ok, value)
}

func valueMatchesFieldEqualIndex(fieldValue Value, ok bool, value reteGraphAlphaRouteValue) bool {
	if !ok {
		return false
	}
	fieldRouteValue, ok := reteGraphAlphaRouteValueFromValue(fieldValue)
	return ok && fieldRouteValue == value
}
