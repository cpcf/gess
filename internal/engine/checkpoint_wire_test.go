package engine

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func TestCheckpointWireCanonicalDocumentContract(t *testing.T) {
	document := minimalCheckpointWireDocument()
	encoded, err := encodeCheckpointWire(document)
	if err != nil {
		t.Fatalf("encode checkpoint: %v", err)
	}

	const want = `{"format":"gess/session-checkpoint","version":1,"rulesetId":"ruleset:contract","sessionId":"session:contract","config":{"initialFacts":[],"globals":[],"strategy":"depth","initialFocusStack":["MAIN"],"resetBeforeSnapshot":false,"demandCascadeLimit":0},"state":{"generation":1,"nextFactSequence":0,"nextRecency":0,"nextRunSequence":0,"nextEventSequence":0,"facts":[],"logicalSupport":{"edges":[],"counters":{"currentLogicalFacts":0,"currentStatedAndLogicalFacts":0,"currentSupportEdges":0,"logicalFactsAsserted":0,"logicalFactsRetracted":0,"supportEdgesAdded":0,"supportEdgesRemoved":0,"metadataOnlyTransitions":0,"cascadeRetractions":0,"cascadeBreadthMax":0,"cascadeDepthMax":0}},"agenda":{"ready":true,"dirty":false,"focusStack":["MAIN"],"nextOrdinal":0,"nextBirthEpoch":0,"initialBirthEpoch":0,"handleGeneration":1,"activations":[]},"backchain":{"cascades":0,"steps":0,"lengthMax":0,"limitHits":0}}}`
	if string(encoded) != want {
		t.Fatalf("canonical checkpoint =\n%s\nwant\n%s", encoded, want)
	}

	decoded, err := decodeCheckpointWire(encoded)
	if err != nil {
		t.Fatalf("decode checkpoint: %v", err)
	}
	reencoded, err := encodeCheckpointWire(decoded)
	if err != nil {
		t.Fatalf("re-encode checkpoint: %v", err)
	}
	if string(reencoded) != want {
		t.Fatalf("round-trip checkpoint =\n%s\nwant\n%s", reencoded, want)
	}
}

func TestCheckpointWireValueRoundTrip(t *testing.T) {
	nested, err := NewValue(map[string]any{
		"alpha": []any{nil, true, int64(math.MinInt64), -0.0},
		"omega": map[string]any{"answer": int64(math.MaxInt64)},
	})
	if err != nil {
		t.Fatalf("nested value: %v", err)
	}

	tests := []struct {
		name  string
		value Value
	}{
		{name: "null", value: NullValue()},
		{name: "bool false", value: newBoolValue(false)},
		{name: "int minimum", value: newIntValue(math.MinInt64)},
		{name: "int maximum", value: newIntValue(math.MaxInt64)},
		{name: "float negative zero", value: newFloatValue(math.Copysign(0, -1))},
		{name: "float fraction", value: newFloatValue(1.25)},
		{name: "empty string", value: newStringValue("")},
		{name: "empty list", value: mustCheckpointValue(t, []any{})},
		{name: "empty map", value: mustCheckpointValue(t, map[string]any{})},
		{name: "nested", value: nested},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wire, err := checkpointWireValueFromValue(tc.value)
			if err != nil {
				t.Fatalf("encode value: %v", err)
			}
			got, err := wire.value()
			if err != nil {
				t.Fatalf("decode value: %v", err)
			}
			if !got.Equal(tc.value) || got.Kind() != tc.value.Kind() {
				t.Fatalf("round-trip value = %v (%s), want %v (%s)", got, got.Kind(), tc.value, tc.value.Kind())
			}
			if tc.name == "float negative zero" {
				stored, _ := got.AsFloat64()
				if !math.Signbit(stored) || wire.Float != "-0" {
					t.Fatalf("negative zero = %q signbit=%v", wire.Float, math.Signbit(stored))
				}
			}
		})
	}
}

func TestCheckpointWireMapEncodingIsDeterministic(t *testing.T) {
	first, err := NewValue(map[string]any{"z": int64(1), "a": int64(2), "m": int64(3)})
	if err != nil {
		t.Fatalf("first map: %v", err)
	}
	second, err := NewValue(map[string]any{"m": int64(3), "z": int64(1), "a": int64(2)})
	if err != nil {
		t.Fatalf("second map: %v", err)
	}

	firstWire, err := checkpointWireValueFromValue(first)
	if err != nil {
		t.Fatalf("encode first map: %v", err)
	}
	secondWire, err := checkpointWireValueFromValue(second)
	if err != nil {
		t.Fatalf("encode second map: %v", err)
	}
	if firstWire.Map == nil || len(*firstWire.Map) != 3 || (*firstWire.Map)[0].Key != "a" || (*firstWire.Map)[1].Key != "m" || (*firstWire.Map)[2].Key != "z" {
		t.Fatalf("map order = %+v", firstWire.Map)
	}
	for i := range *firstWire.Map {
		if (*firstWire.Map)[i].Key != (*secondWire.Map)[i].Key {
			t.Fatalf("map encodings differ: %+v != %+v", firstWire.Map, secondWire.Map)
		}
	}
}

func TestDecodeCheckpointWireRejectsEnvelopeAndJSONContractViolations(t *testing.T) {
	valid, err := encodeCheckpointWire(minimalCheckpointWireDocument())
	if err != nil {
		t.Fatalf("encode valid checkpoint: %v", err)
	}

	tests := []struct {
		name    string
		encoded string
		want    error
	}{
		{name: "wrong format", encoded: strings.Replace(string(valid), checkpointWireFormat, "other", 1), want: ErrInvalidCheckpoint},
		{name: "zero version", encoded: strings.Replace(string(valid), `"version":1`, `"version":0`, 1), want: ErrUnsupportedCheckpointVersion},
		{name: "future version", encoded: strings.Replace(string(valid), `"version":1`, `"version":2`, 1), want: ErrUnsupportedCheckpointVersion},
		{name: "unknown field", encoded: strings.Replace(string(valid), `"version":1`, `"version":1,"unknown":true`, 1), want: ErrInvalidCheckpoint},
		{name: "duplicate field", encoded: strings.Replace(string(valid), `"version":1`, `"version":1,"version":1`, 1), want: ErrInvalidCheckpoint},
		{name: "trailing value", encoded: string(valid) + `{}`, want: ErrInvalidCheckpoint},
		{name: "trailing garbage", encoded: string(valid) + `x`, want: ErrInvalidCheckpoint},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeCheckpointWire([]byte(tc.encoded))
			if !errors.Is(err, tc.want) {
				t.Fatalf("decode error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestCheckpointWireValidationRejectsSemanticInconsistency(t *testing.T) {
	falseValue := false
	tests := []struct {
		name   string
		mutate func(*checkpointWireDocument)
	}{
		{name: "missing ruleset", mutate: func(document *checkpointWireDocument) { document.RulesetID = "" }},
		{name: "invalid strategy", mutate: func(document *checkpointWireDocument) { document.Config.Strategy = "random" }},
		{name: "negative demand limit", mutate: func(document *checkpointWireDocument) { document.Config.DemandCascadeLimit = -1 }},
		{name: "ready and dirty", mutate: func(document *checkpointWireDocument) { document.State.Agenda.Dirty = true }},
		{name: "unordered globals", mutate: func(document *checkpointWireDocument) {
			document.Config.Globals = []checkpointWireNamedValue{
				{Name: "z", Value: checkpointWireValue{Kind: "null"}},
				{Name: "a", Value: checkpointWireValue{Kind: "null"}},
			}
		}},
		{name: "invalid typed value", mutate: func(document *checkpointWireDocument) {
			document.Config.Globals = []checkpointWireNamedValue{{Name: "x", Value: checkpointWireValue{Kind: "int", Int: "01"}}}
		}},
		{name: "mixed typed payload", mutate: func(document *checkpointWireDocument) {
			document.Config.Globals = []checkpointWireNamedValue{{Name: "x", Value: checkpointWireValue{Kind: "bool", Bool: &falseValue, Int: "1"}}}
		}},
		{name: "unordered map", mutate: func(document *checkpointWireDocument) {
			entries := []checkpointWireMapEntry{
				{Key: "z", Value: checkpointWireValue{Kind: "null"}},
				{Key: "a", Value: checkpointWireValue{Kind: "null"}},
			}
			document.Config.Globals = []checkpointWireNamedValue{{Name: "x", Value: checkpointWireValue{Kind: "map", Map: &entries}}}
		}},
		{name: "allocator before fact", mutate: func(document *checkpointWireDocument) {
			document.State.Facts = []checkpointWireFact{{
				ID: checkpointWireFactID{Generation: 1, Sequence: 2}, TemplateKey: "template:item", Version: 1,
				Recency: 3, Support: FactSupportStated, Fields: []checkpointWireField{},
			}}
			document.State.NextFactSequence = 1
			document.State.NextRecency = 3
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			document := minimalCheckpointWireDocument()
			tc.mutate(&document)
			if _, err := encodeCheckpointWire(document); !errors.Is(err, ErrInvalidCheckpoint) {
				t.Fatalf("encode error = %v, want ErrInvalidCheckpoint", err)
			}
		})
	}
}

func mustCheckpointValue(t *testing.T, raw any) Value {
	t.Helper()
	value, err := NewValue(raw)
	if err != nil {
		t.Fatalf("checkpoint value: %v", err)
	}
	return value
}

func minimalCheckpointWireDocument() checkpointWireDocument {
	return checkpointWireDocument{
		Format: checkpointWireFormat, Version: checkpointWireVersion,
		RulesetID: "ruleset:contract", SessionID: "session:contract",
		Config: checkpointWireSessionConfig{
			InitialFacts: []checkpointWireInitialFact{}, Globals: []checkpointWireNamedValue{},
			Strategy: "depth", InitialFocusStack: []ModuleName{MainModule},
		},
		State: checkpointWireSessionState{
			Generation: 1, Facts: []checkpointWireFact{},
			LogicalSupport: checkpointWireLogicalSupportState{Edges: []checkpointWireLogicalSupportEdge{}},
			Agenda: checkpointWireAgendaState{
				Ready: true, FocusStack: []ModuleName{MainModule}, HandleGeneration: 1,
				Activations: []checkpointWireActivation{},
			},
		},
	}
}
