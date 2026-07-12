package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
)

const (
	checkpointWireFormat  = "gess/session-checkpoint"
	checkpointWireVersion = 1
)

type checkpointWireDocument struct {
	Format    string                      `json:"format"`
	Version   int                         `json:"version"`
	RulesetID RulesetID                   `json:"rulesetId"`
	SessionID SessionID                   `json:"sessionId,omitempty"`
	Config    checkpointWireSessionConfig `json:"config"`
	State     checkpointWireSessionState  `json:"state"`
}

type checkpointWireSessionConfig struct {
	InitialFacts        []checkpointWireInitialFact `json:"initialFacts"`
	Globals             []checkpointWireNamedValue  `json:"globals"`
	Strategy            string                      `json:"strategy"`
	InitialFocusStack   []ModuleName                `json:"initialFocusStack"`
	ResetBeforeSnapshot bool                        `json:"resetBeforeSnapshot"`
	DemandCascadeLimit  int                         `json:"demandCascadeLimit"`
}

type checkpointWireSessionState struct {
	Generation        Generation                        `json:"generation"`
	NextFactSequence  uint64                            `json:"nextFactSequence"`
	NextRecency       Recency                           `json:"nextRecency"`
	NextRunSequence   uint64                            `json:"nextRunSequence"`
	NextEventSequence uint64                            `json:"nextEventSequence"`
	Facts             []checkpointWireFact              `json:"facts"`
	LogicalSupport    checkpointWireLogicalSupportState `json:"logicalSupport"`
	Agenda            checkpointWireAgendaState         `json:"agenda"`
	Backchain         checkpointWireBackchainState      `json:"backchain"`
}

type checkpointWireInitialFact struct {
	Name        string                `json:"name,omitempty"`
	TemplateKey TemplateKey           `json:"templateKey,omitempty"`
	Fields      []checkpointWireField `json:"fields"`
}

type checkpointWireNamedValue struct {
	Name  string              `json:"name"`
	Value checkpointWireValue `json:"value"`
}

type checkpointWireFactID struct {
	Generation Generation `json:"generation"`
	Sequence   uint64     `json:"sequence"`
}

type checkpointWireFact struct {
	ID          checkpointWireFactID  `json:"id"`
	Name        string                `json:"name,omitempty"`
	TemplateKey TemplateKey           `json:"templateKey,omitempty"`
	Version     FactVersion           `json:"version"`
	Recency     Recency               `json:"recency"`
	Support     FactSupportState      `json:"support"`
	Fields      []checkpointWireField `json:"fields"`
}

type checkpointWireField struct {
	Name     string              `json:"name"`
	Presence FieldPresence       `json:"presence,omitempty"`
	Value    checkpointWireValue `json:"value"`
}

type checkpointWireLogicalSupportState struct {
	Edges    []checkpointWireLogicalSupportEdge   `json:"edges"`
	Counters checkpointWireLogicalSupportCounters `json:"counters"`
}

type checkpointWireLogicalSupportCounters struct {
	CurrentLogicalFacts          int `json:"currentLogicalFacts"`
	CurrentStatedAndLogicalFacts int `json:"currentStatedAndLogicalFacts"`
	CurrentSupportEdges          int `json:"currentSupportEdges"`
	LogicalFactsAsserted         int `json:"logicalFactsAsserted"`
	LogicalFactsRetracted        int `json:"logicalFactsRetracted"`
	SupportEdgesAdded            int `json:"supportEdgesAdded"`
	SupportEdgesRemoved          int `json:"supportEdgesRemoved"`
	MetadataOnlyTransitions      int `json:"metadataOnlyTransitions"`
	CascadeRetractions           int `json:"cascadeRetractions"`
	CascadeBreadthMax            int `json:"cascadeBreadthMax"`
	CascadeDepthMax              int `json:"cascadeDepthMax"`
}

type checkpointWireLogicalSupportEdge struct {
	SupportID       SupportID                       `json:"supportId"`
	FactID          checkpointWireFactID            `json:"factId"`
	RuleID          RuleID                          `json:"ruleId"`
	RuleRevisionID  RuleRevisionID                  `json:"ruleRevisionId"`
	ActivationID    ActivationID                    `json:"activationId"`
	Generation      Generation                      `json:"generation"`
	Source          checkpointWireCandidateIdentity `json:"source"`
	SupportingFacts []checkpointWireFactID          `json:"supportingFacts"`
}

type checkpointWireCandidateIdentity struct {
	ScopeHash uint64 `json:"scopeHash"`
	Hash      uint64 `json:"hash"`
}

type checkpointWireAgendaState struct {
	Ready             bool                       `json:"ready"`
	Dirty             bool                       `json:"dirty"`
	FocusStack        []ModuleName               `json:"focusStack"`
	NextOrdinal       uint64                     `json:"nextOrdinal"`
	NextBirthEpoch    uint64                     `json:"nextBirthEpoch"`
	InitialBirthEpoch uint64                     `json:"initialBirthEpoch"`
	HandleGeneration  uint32                     `json:"handleGeneration"`
	Activations       []checkpointWireActivation `json:"activations"`
}

type checkpointWireActivation struct {
	Ordinal          uint64                          `json:"ordinal"`
	RuleRevisionID   RuleRevisionID                  `json:"ruleRevisionId"`
	Identity         checkpointWireCandidateIdentity `json:"identity"`
	BirthEpoch       uint64                          `json:"birthEpoch"`
	BirthRank        uint64                          `json:"birthRank"`
	Module           ModuleName                      `json:"module"`
	Salience         int                             `json:"salience"`
	DeclarationOrder int                             `json:"declarationOrder"`
	MaxRecency       Recency                         `json:"maxRecency"`
	TotalRecency     Recency                         `json:"totalRecency"`
	SupportCount     uint32                          `json:"supportCount"`
	Status           string                          `json:"status"`
	Path             []int                           `json:"path"`
	FactIDs          []checkpointWireFactID          `json:"factIds"`
	FactVersions     []FactVersion                   `json:"factVersions"`
	Bindings         []checkpointWireBinding         `json:"bindings"`
}

type checkpointWireBinding struct {
	Name           string               `json:"name"`
	Slot           int                  `json:"slot"`
	ConditionOrder int                  `json:"conditionOrder"`
	ConditionID    ConditionID          `json:"conditionId"`
	ConditionPath  []int                `json:"conditionPath"`
	FactID         checkpointWireFactID `json:"factId"`
	FactVersion    FactVersion          `json:"factVersion"`
	Value          *checkpointWireValue `json:"value,omitempty"`
}

type checkpointWireBackchainState struct {
	Cascades  int `json:"cascades"`
	Steps     int `json:"steps"`
	LengthMax int `json:"lengthMax"`
	LimitHits int `json:"limitHits"`
}

type checkpointWireValue struct {
	Kind   string                    `json:"kind"`
	Bool   *bool                     `json:"bool,omitempty"`
	Int    string                    `json:"int,omitempty"`
	Float  string                    `json:"float,omitempty"`
	String string                    `json:"string,omitempty"`
	List   *[]checkpointWireValue    `json:"list,omitempty"`
	Map    *[]checkpointWireMapEntry `json:"map,omitempty"`
}

type checkpointWireMapEntry struct {
	Key   string              `json:"key"`
	Value checkpointWireValue `json:"value"`
}

func encodeCheckpointWire(document checkpointWireDocument) ([]byte, error) {
	if err := validateCheckpointWireDocument(document); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("%w: encode: %v", ErrInvalidCheckpoint, err)
	}
	return encoded, nil
}

func decodeCheckpointWire(encoded []byte) (checkpointWireDocument, error) {
	if err := rejectDuplicateCheckpointJSONKeys(encoded); err != nil {
		return checkpointWireDocument{}, err
	}
	var header struct {
		Format  string `json:"format"`
		Version int    `json:"version"`
	}
	if err := decodeCheckpointJSON(encoded, &header, false); err != nil {
		return checkpointWireDocument{}, err
	}
	if header.Format != checkpointWireFormat {
		return checkpointWireDocument{}, fmt.Errorf("%w: format %q", ErrInvalidCheckpoint, header.Format)
	}
	if header.Version != checkpointWireVersion {
		return checkpointWireDocument{}, fmt.Errorf("%w: version %d", ErrUnsupportedCheckpointVersion, header.Version)
	}

	var document checkpointWireDocument
	if err := decodeCheckpointJSON(encoded, &document, true); err != nil {
		return checkpointWireDocument{}, err
	}
	if err := validateCheckpointWireDocument(document); err != nil {
		return checkpointWireDocument{}, err
	}
	return document, nil
}

func rejectDuplicateCheckpointJSONKeys(encoded []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	if err := scanCheckpointJSONValue(decoder); err != nil {
		return fmt.Errorf("%w: decode: %v", ErrInvalidCheckpoint, err)
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("%w: trailing JSON token %v", ErrInvalidCheckpoint, token)
		}
		return fmt.Errorf("%w: trailing data: %v", ErrInvalidCheckpoint, err)
	}
	return nil
}

func scanCheckpointJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("object key is not a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate object key %q", key)
			}
			seen[key] = struct{}{}
			if err := scanCheckpointJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case '[':
		for decoder.More() {
			if err := scanCheckpointJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	default:
		return fmt.Errorf("unexpected delimiter %q", delim)
	}
}

func decodeCheckpointJSON(encoded []byte, target any, strict bool) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	if strict {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: decode: %v", ErrInvalidCheckpoint, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("%w: trailing JSON value", ErrInvalidCheckpoint)
		}
		return fmt.Errorf("%w: trailing data: %v", ErrInvalidCheckpoint, err)
	}
	return nil
}

func validateCheckpointWireDocument(document checkpointWireDocument) error {
	if document.Format != checkpointWireFormat {
		return fmt.Errorf("%w: format %q", ErrInvalidCheckpoint, document.Format)
	}
	if document.Version != checkpointWireVersion {
		return fmt.Errorf("%w: version %d", ErrUnsupportedCheckpointVersion, document.Version)
	}
	if document.RulesetID == "" {
		return fmt.Errorf("%w: missing ruleset ID", ErrInvalidCheckpoint)
	}
	if document.State.Generation == 0 {
		return fmt.Errorf("%w: zero generation", ErrInvalidCheckpoint)
	}
	if document.Config.Strategy != "depth" && document.Config.Strategy != "breadth" {
		return fmt.Errorf("%w: invalid strategy %q", ErrInvalidCheckpoint, document.Config.Strategy)
	}
	if document.Config.DemandCascadeLimit < 0 {
		return fmt.Errorf("%w: negative demand cascade limit", ErrInvalidCheckpoint)
	}
	if err := validateCheckpointNamedValues(document.Config.Globals, "global"); err != nil {
		return err
	}
	for i, initial := range document.Config.InitialFacts {
		if (initial.Name == "") == (initial.TemplateKey == "") {
			return fmt.Errorf("%w: initial fact %d must have exactly one target", ErrInvalidCheckpoint, i)
		}
		if err := validateCheckpointFields(initial.Fields, fmt.Sprintf("initial fact %d", i)); err != nil {
			return err
		}
	}

	facts := make(map[checkpointWireFactID]struct{}, len(document.State.Facts))
	var maxSequence uint64
	var maxRecency Recency
	for i, fact := range document.State.Facts {
		if fact.ID.Generation != document.State.Generation || fact.ID.Sequence == 0 {
			return fmt.Errorf("%w: fact %d has invalid identity", ErrInvalidCheckpoint, i)
		}
		if _, exists := facts[fact.ID]; exists {
			return fmt.Errorf("%w: duplicate fact identity", ErrInvalidCheckpoint)
		}
		facts[fact.ID] = struct{}{}
		maxSequence = max(maxSequence, fact.ID.Sequence)
		maxRecency = max(maxRecency, fact.Recency)
		if (fact.Name == "") == (fact.TemplateKey == "") {
			return fmt.Errorf("%w: fact %d must have exactly one target", ErrInvalidCheckpoint, i)
		}
		switch fact.Support {
		case FactSupportStated, FactSupportLogical, FactSupportStatedAndLogical, FactSupportMetadataOnly:
		default:
			return fmt.Errorf("%w: fact %d has invalid support %q", ErrInvalidCheckpoint, i, fact.Support)
		}
		if err := validateCheckpointFields(fact.Fields, fmt.Sprintf("fact %d", i)); err != nil {
			return err
		}
	}
	if document.State.NextFactSequence < maxSequence || document.State.NextRecency < maxRecency {
		return fmt.Errorf("%w: allocator precedes stored fact", ErrInvalidCheckpoint)
	}
	if document.State.Agenda.Ready && document.State.Agenda.Dirty {
		return fmt.Errorf("%w: agenda cannot be ready and dirty", ErrInvalidCheckpoint)
	}
	for i, activation := range document.State.Agenda.Activations {
		if activation.Ordinal == 0 || activation.RuleRevisionID.IsZero() || activation.Identity == (checkpointWireCandidateIdentity{}) {
			return fmt.Errorf("%w: activation %d has invalid identity", ErrInvalidCheckpoint, i)
		}
		if activation.Status != "pending" && activation.Status != "consumed" {
			return fmt.Errorf("%w: activation %d has invalid status %q", ErrInvalidCheckpoint, i, activation.Status)
		}
		if len(activation.FactIDs) != len(activation.FactVersions) {
			return fmt.Errorf("%w: activation %d fact/version length mismatch", ErrInvalidCheckpoint, i)
		}
		for _, id := range activation.FactIDs {
			if _, ok := facts[id]; !ok {
				return fmt.Errorf("%w: activation %d references missing fact", ErrInvalidCheckpoint, i)
			}
		}
		for j, binding := range activation.Bindings {
			if binding.FactID != (checkpointWireFactID{}) {
				if _, ok := facts[binding.FactID]; !ok {
					return fmt.Errorf("%w: activation %d binding %d references missing fact", ErrInvalidCheckpoint, i, j)
				}
			}
			if binding.Value != nil {
				if _, err := binding.Value.value(); err != nil {
					return fmt.Errorf("%w: activation %d binding %d: %v", ErrInvalidCheckpoint, i, j, err)
				}
			}
		}
	}
	for i, edge := range document.State.LogicalSupport.Edges {
		if edge.SupportID.IsZero() || edge.RuleRevisionID.IsZero() || edge.Source == (checkpointWireCandidateIdentity{}) {
			return fmt.Errorf("%w: logical support edge %d has invalid identity", ErrInvalidCheckpoint, i)
		}
		if _, ok := facts[edge.FactID]; !ok {
			return fmt.Errorf("%w: logical support edge %d references missing fact", ErrInvalidCheckpoint, i)
		}
		for _, id := range edge.SupportingFacts {
			if _, ok := facts[id]; !ok {
				return fmt.Errorf("%w: logical support edge %d references missing supporting fact", ErrInvalidCheckpoint, i)
			}
		}
	}
	return nil
}

func validateCheckpointNamedValues(values []checkpointWireNamedValue, label string) error {
	last := ""
	for i, named := range values {
		if named.Name == "" || (i > 0 && named.Name <= last) {
			return fmt.Errorf("%w: %s names must be non-empty and strictly ordered", ErrInvalidCheckpoint, label)
		}
		if _, err := named.Value.value(); err != nil {
			return fmt.Errorf("%w: %s %q: %v", ErrInvalidCheckpoint, label, named.Name, err)
		}
		last = named.Name
	}
	return nil
}

func validateCheckpointFields(fields []checkpointWireField, owner string) error {
	last := ""
	for i, field := range fields {
		if field.Name == "" || (i > 0 && field.Name <= last) {
			return fmt.Errorf("%w: %s fields must be non-empty and strictly ordered", ErrInvalidCheckpoint, owner)
		}
		switch field.Presence {
		case "", FieldPresenceOmitted, FieldPresenceDefault, FieldPresenceExplicit:
		default:
			return fmt.Errorf("%w: %s field %q has invalid presence", ErrInvalidCheckpoint, owner, field.Name)
		}
		if _, err := field.Value.value(); err != nil {
			return fmt.Errorf("%w: %s field %q: %v", ErrInvalidCheckpoint, owner, field.Name, err)
		}
		last = field.Name
	}
	return nil
}

func checkpointWireValueFromValue(value Value) (checkpointWireValue, error) {
	encoded := checkpointWireValue{Kind: value.Kind().String()}
	switch value.Kind() {
	case ValueNull:
	case ValueBool:
		stored, _ := value.AsBool()
		encoded.Bool = &stored
	case ValueInt:
		stored, _ := value.AsInt64()
		encoded.Int = strconv.FormatInt(stored, 10)
	case ValueFloat:
		stored, _ := value.AsFloat64()
		encoded.Float = strconv.FormatFloat(stored, 'g', -1, 64)
	case ValueString:
		encoded.String, _ = value.AsString()
	case ValueList:
		stored, _ := value.AsList()
		list := make([]checkpointWireValue, len(stored))
		encoded.List = &list
		for i, item := range stored {
			var err error
			(*encoded.List)[i], err = checkpointWireValueFromValue(item)
			if err != nil {
				return checkpointWireValue{}, err
			}
		}
	case ValueMap:
		stored, _ := value.AsMap()
		keys := make([]string, 0, len(stored))
		for key := range stored {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		entries := make([]checkpointWireMapEntry, len(keys))
		encoded.Map = &entries
		for i, key := range keys {
			item, err := checkpointWireValueFromValue(stored[key])
			if err != nil {
				return checkpointWireValue{}, err
			}
			(*encoded.Map)[i] = checkpointWireMapEntry{Key: key, Value: item}
		}
	default:
		return checkpointWireValue{}, fmt.Errorf("%w: value kind %q", ErrInvalidCheckpoint, value.Kind())
	}
	return encoded, nil
}

func (value checkpointWireValue) value() (Value, error) {
	invalidPayload := func() (Value, error) {
		return Value{}, fmt.Errorf("invalid %q value payload", value.Kind)
	}
	switch value.Kind {
	case "null":
		if value.Bool != nil || value.Int != "" || value.Float != "" || value.String != "" || value.List != nil || value.Map != nil {
			return invalidPayload()
		}
		return NullValue(), nil
	case "bool":
		if value.Bool == nil || value.Int != "" || value.Float != "" || value.String != "" || value.List != nil || value.Map != nil {
			return invalidPayload()
		}
		return newBoolValue(*value.Bool), nil
	case "int":
		if value.Bool != nil || value.Int == "" || value.Float != "" || value.String != "" || value.List != nil || value.Map != nil {
			return invalidPayload()
		}
		stored, err := strconv.ParseInt(value.Int, 10, 64)
		if err != nil || strconv.FormatInt(stored, 10) != value.Int {
			return invalidPayload()
		}
		return newIntValue(stored), nil
	case "float":
		if value.Bool != nil || value.Int != "" || value.Float == "" || value.String != "" || value.List != nil || value.Map != nil {
			return invalidPayload()
		}
		stored, err := strconv.ParseFloat(value.Float, 64)
		if err != nil || math.IsNaN(stored) || math.IsInf(stored, 0) || strconv.FormatFloat(stored, 'g', -1, 64) != value.Float {
			return invalidPayload()
		}
		return newFloatValue(stored), nil
	case "string":
		if value.Bool != nil || value.Int != "" || value.Float != "" || value.List != nil || value.Map != nil {
			return invalidPayload()
		}
		return newStringValue(value.String), nil
	case "list":
		if value.Bool != nil || value.Int != "" || value.Float != "" || value.String != "" || value.List == nil || value.Map != nil {
			return invalidPayload()
		}
		stored := make([]any, len(*value.List))
		for i, item := range *value.List {
			decoded, err := item.value()
			if err != nil {
				return Value{}, err
			}
			stored[i] = decoded
		}
		return NewValue(stored)
	case "map":
		if value.Bool != nil || value.Int != "" || value.Float != "" || value.String != "" || value.List != nil || value.Map == nil {
			return invalidPayload()
		}
		stored := make(map[string]Value, len(*value.Map))
		last := ""
		for i, entry := range *value.Map {
			if i > 0 && entry.Key <= last {
				return Value{}, fmt.Errorf("map keys must be strictly ordered")
			}
			decoded, err := entry.Value.value()
			if err != nil {
				return Value{}, err
			}
			stored[entry.Key] = decoded
			last = entry.Key
		}
		return NewValue(stored)
	default:
		return Value{}, fmt.Errorf("unknown value kind %q", value.Kind)
	}
}
