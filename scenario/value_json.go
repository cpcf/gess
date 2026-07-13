// Package scenario defines portable scenario and report data contracts.
package scenario

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/cpcf/gess/rules"
)

// ErrInvalidValueJSON identifies invalid lossless Gess value JSON.
var ErrInvalidValueJSON = errors.New("invalid Gess value JSON")

// Value is an opaque JSON wrapper for a rules.Value.
type Value struct {
	value rules.Value
}

var (
	_ json.Marshaler   = Value{}
	_ json.Unmarshaler = (*Value)(nil)
)

// NewValue wraps value for lossless JSON encoding. Container storage is
// defensively copied.
func NewValue(value rules.Value) Value {
	return Value{value: rules.CloneValue(value)}
}

// RulesValue returns a defensive copy of the wrapped value.
func (v Value) RulesValue() rules.Value {
	return rules.CloneValue(v.value)
}

// MarshalJSON implements json.Marshaler.
func (v Value) MarshalJSON() ([]byte, error) {
	return MarshalValue(v.value)
}

// UnmarshalJSON implements json.Unmarshaler. The receiver is unchanged when
// decoding fails.
func (v *Value) UnmarshalJSON(data []byte) error {
	if v == nil {
		return invalidValueJSONf("cannot unmarshal into a nil *Value")
	}
	value, err := UnmarshalValue(data)
	if err != nil {
		return err
	}
	v.value = rules.CloneValue(value)
	return nil
}

// MarshalValue returns the canonical lossless JSON encoding of value.
func MarshalValue(value rules.Value) ([]byte, error) {
	var out bytes.Buffer
	if err := appendValueJSON(&out, value); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// UnmarshalValue decodes one strict lossless Gess value JSON envelope.
func UnmarshalValue(data []byte) (rules.Value, error) {
	if !utf8.Valid(data) {
		return rules.Value{}, invalidValueJSONf("input is not valid UTF-8")
	}
	if err := validateJSONStringEscapes(data); err != nil {
		return rules.Value{}, err
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	node, err := readJSONNode(decoder)
	if err != nil {
		return rules.Value{}, invalidValueJSONf("decode: %v", err)
	}
	if token, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err != nil {
			return rules.Value{}, invalidValueJSONf("trailing JSON: %v", err)
		}
		return rules.Value{}, invalidValueJSONf("trailing JSON token %v", token)
	}

	value, err := decodeValueEnvelope(node, "value")
	if err != nil {
		return rules.Value{}, err
	}
	return rules.CloneValue(value), nil
}

func appendValueJSON(out *bytes.Buffer, value rules.Value) error {
	switch value.Kind() {
	case rules.ValueNull:
		out.WriteString(`{"kind":"null"}`)
	case rules.ValueBool:
		payload, ok := value.AsBool()
		if !ok {
			return invalidValueJSONf("bool value has no bool payload")
		}
		out.WriteString(`{"kind":"bool","bool":`)
		out.WriteString(strconv.FormatBool(payload))
		out.WriteByte('}')
	case rules.ValueInt:
		payload, ok := value.AsInt64()
		if !ok {
			return invalidValueJSONf("int value has no int payload")
		}
		out.WriteString(`{"kind":"int","int":"`)
		out.WriteString(strconv.FormatInt(payload, 10))
		out.WriteString(`"}`)
	case rules.ValueFloat:
		payload, ok := value.AsFloat64()
		if !ok {
			return invalidValueJSONf("float value has no float payload")
		}
		if math.IsNaN(payload) || math.IsInf(payload, 0) {
			return invalidValueJSONf("cannot encode non-finite float")
		}
		out.WriteString(`{"kind":"float","float":"`)
		out.WriteString(strconv.FormatFloat(payload, 'g', -1, 64))
		out.WriteString(`"}`)
	case rules.ValueString:
		payload, ok := value.AsString()
		if !ok {
			return invalidValueJSONf("string value has no string payload")
		}
		if !utf8.ValidString(payload) {
			return invalidValueJSONf("cannot encode string payload with invalid UTF-8")
		}
		out.WriteString(`{"kind":"string","string":`)
		appendJSONString(out, payload)
		out.WriteByte('}')
	case rules.ValueList:
		payload, ok := value.AsList()
		if !ok {
			return invalidValueJSONf("list value has no list payload")
		}
		out.WriteString(`{"kind":"list","list":[`)
		for i, item := range payload {
			if i > 0 {
				out.WriteByte(',')
			}
			if err := appendValueJSON(out, item); err != nil {
				return fmt.Errorf("list item %d: %w", i, err)
			}
		}
		out.WriteString(`]}`)
	case rules.ValueMap:
		payload, ok := value.AsMap()
		if !ok {
			return invalidValueJSONf("map value has no map payload")
		}
		keys := make([]string, 0, len(payload))
		for key := range payload {
			if !utf8.ValidString(key) {
				return invalidValueJSONf("cannot encode map key with invalid UTF-8")
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out.WriteString(`{"kind":"map","map":[`)
		for i, key := range keys {
			if i > 0 {
				out.WriteByte(',')
			}
			out.WriteString(`{"key":`)
			appendJSONString(out, key)
			out.WriteString(`,"value":`)
			if err := appendValueJSON(out, payload[key]); err != nil {
				return fmt.Errorf("map key %q: %w", key, err)
			}
			out.WriteByte('}')
		}
		out.WriteString(`]}`)
	default:
		return invalidValueJSONf("cannot encode value kind %q", value.Kind())
	}
	return nil
}

func appendJSONString(out *bytes.Buffer, value string) {
	encoded, _ := json.Marshal(value)
	out.Write(encoded)
}

type jsonNodeKind uint8

const (
	jsonNodeNull jsonNodeKind = iota
	jsonNodeBool
	jsonNodeNumber
	jsonNodeString
	jsonNodeArray
	jsonNodeObject
)

type jsonNode struct {
	kind    jsonNodeKind
	boolean bool
	text    string
	array   []jsonNode
	object  []jsonMember
}

type jsonMember struct {
	name  string
	value jsonNode
}

func readJSONNode(decoder *json.Decoder) (jsonNode, error) {
	token, err := decoder.Token()
	if err != nil {
		return jsonNode{}, err
	}
	switch token := token.(type) {
	case nil:
		return jsonNode{kind: jsonNodeNull}, nil
	case bool:
		return jsonNode{kind: jsonNodeBool, boolean: token}, nil
	case json.Number:
		return jsonNode{kind: jsonNodeNumber, text: token.String()}, nil
	case float64:
		return jsonNode{kind: jsonNodeNumber, text: strconv.FormatFloat(token, 'g', -1, 64)}, nil
	case string:
		return jsonNode{kind: jsonNodeString, text: strings.Clone(token)}, nil
	case json.Delim:
		switch token {
		case '[':
			var values []jsonNode
			for decoder.More() {
				value, err := readJSONNode(decoder)
				if err != nil {
					return jsonNode{}, err
				}
				values = append(values, value)
			}
			end, err := decoder.Token()
			if err != nil {
				return jsonNode{}, err
			}
			if end != json.Delim(']') {
				return jsonNode{}, fmt.Errorf("array ended with %v", end)
			}
			return jsonNode{kind: jsonNodeArray, array: values}, nil
		case '{':
			var members []jsonMember
			seen := make(map[string]struct{})
			for decoder.More() {
				nameToken, err := decoder.Token()
				if err != nil {
					return jsonNode{}, err
				}
				name, ok := nameToken.(string)
				if !ok {
					return jsonNode{}, fmt.Errorf("object member name has type %T", nameToken)
				}
				if _, duplicate := seen[name]; duplicate {
					return jsonNode{}, fmt.Errorf("duplicate object member %q", name)
				}
				seen[name] = struct{}{}
				value, err := readJSONNode(decoder)
				if err != nil {
					return jsonNode{}, err
				}
				members = append(members, jsonMember{name: strings.Clone(name), value: value})
			}
			end, err := decoder.Token()
			if err != nil {
				return jsonNode{}, err
			}
			if end != json.Delim('}') {
				return jsonNode{}, fmt.Errorf("object ended with %v", end)
			}
			return jsonNode{kind: jsonNodeObject, object: members}, nil
		default:
			return jsonNode{}, fmt.Errorf("unexpected delimiter %q", token)
		}
	default:
		return jsonNode{}, fmt.Errorf("unexpected JSON token type %T", token)
	}
}

func decodeValueEnvelope(node jsonNode, path string) (rules.Value, error) {
	if node.kind != jsonNodeObject {
		return rules.Value{}, invalidValueJSONf("%s must be a kind-tagged object", path)
	}
	kindNode, ok := objectMember(node, "kind")
	if !ok {
		return rules.Value{}, invalidValueJSONf("%s is missing member %q", path, "kind")
	}
	if kindNode.kind != jsonNodeString {
		return rules.Value{}, invalidValueJSONf("%s member %q must be a string", path, "kind")
	}

	switch kindNode.text {
	case "null":
		if err := requireObjectMembers(node, path, "kind"); err != nil {
			return rules.Value{}, err
		}
		return rules.NullValue(), nil
	case "bool":
		if err := requireObjectMembers(node, path, "kind", "bool"); err != nil {
			return rules.Value{}, err
		}
		payload, _ := objectMember(node, "bool")
		if payload.kind != jsonNodeBool {
			return rules.Value{}, invalidValueJSONf("%s member %q must be a bool", path, "bool")
		}
		return rules.BoolValue(payload.boolean), nil
	case "int":
		if err := requireObjectMembers(node, path, "kind", "int"); err != nil {
			return rules.Value{}, err
		}
		payload, _ := objectMember(node, "int")
		if payload.kind != jsonNodeString {
			return rules.Value{}, invalidValueJSONf("%s member %q must be a string", path, "int")
		}
		parsed, err := strconv.ParseInt(payload.text, 10, 64)
		if err != nil || strconv.FormatInt(parsed, 10) != payload.text {
			return rules.Value{}, invalidValueJSONf("%s has non-canonical int %q", path, payload.text)
		}
		return rules.IntValue(parsed), nil
	case "float":
		if err := requireObjectMembers(node, path, "kind", "float"); err != nil {
			return rules.Value{}, err
		}
		payload, _ := objectMember(node, "float")
		if payload.kind != jsonNodeString {
			return rules.Value{}, invalidValueJSONf("%s member %q must be a string", path, "float")
		}
		parsed, err := strconv.ParseFloat(payload.text, 64)
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) ||
			strconv.FormatFloat(parsed, 'g', -1, 64) != payload.text {
			return rules.Value{}, invalidValueJSONf("%s has non-canonical float %q", path, payload.text)
		}
		return rules.FloatValue(parsed), nil
	case "string":
		if err := requireObjectMembers(node, path, "kind", "string"); err != nil {
			return rules.Value{}, err
		}
		payload, _ := objectMember(node, "string")
		if payload.kind != jsonNodeString {
			return rules.Value{}, invalidValueJSONf("%s member %q must be a string", path, "string")
		}
		return rules.StringValue(strings.Clone(payload.text)), nil
	case "list":
		if err := requireObjectMembers(node, path, "kind", "list"); err != nil {
			return rules.Value{}, err
		}
		payload, _ := objectMember(node, "list")
		if payload.kind != jsonNodeArray {
			return rules.Value{}, invalidValueJSONf("%s member %q must be an array", path, "list")
		}
		items := make([]rules.Value, len(payload.array))
		for i, item := range payload.array {
			decoded, err := decodeValueEnvelope(item, fmt.Sprintf("%s.list[%d]", path, i))
			if err != nil {
				return rules.Value{}, err
			}
			items[i] = decoded
		}
		value, err := rules.NewValue(items)
		if err != nil {
			return rules.Value{}, invalidValueJSONf("%s list payload: %v", path, err)
		}
		return value, nil
	case "map":
		if err := requireObjectMembers(node, path, "kind", "map"); err != nil {
			return rules.Value{}, err
		}
		payload, _ := objectMember(node, "map")
		if payload.kind != jsonNodeArray {
			return rules.Value{}, invalidValueJSONf("%s member %q must be an array", path, "map")
		}
		values := make(map[string]rules.Value, len(payload.array))
		previous := ""
		for i, entry := range payload.array {
			entryPath := fmt.Sprintf("%s.map[%d]", path, i)
			if entry.kind != jsonNodeObject {
				return rules.Value{}, invalidValueJSONf("%s must be an object", entryPath)
			}
			if err := requireObjectMembers(entry, entryPath, "key", "value"); err != nil {
				return rules.Value{}, err
			}
			keyNode, _ := objectMember(entry, "key")
			if keyNode.kind != jsonNodeString {
				return rules.Value{}, invalidValueJSONf("%s member %q must be a string", entryPath, "key")
			}
			key := strings.Clone(keyNode.text)
			if i > 0 && key <= previous {
				return rules.Value{}, invalidValueJSONf("%s map keys must be strictly increasing: %q after %q", path, key, previous)
			}
			valueNode, _ := objectMember(entry, "value")
			decoded, err := decodeValueEnvelope(valueNode, entryPath+".value")
			if err != nil {
				return rules.Value{}, err
			}
			values[key] = decoded
			previous = key
		}
		value, err := rules.NewValue(values)
		if err != nil {
			return rules.Value{}, invalidValueJSONf("%s map payload: %v", path, err)
		}
		return value, nil
	default:
		return rules.Value{}, invalidValueJSONf("%s has unknown kind %q", path, kindNode.text)
	}
}

func objectMember(node jsonNode, name string) (jsonNode, bool) {
	for _, member := range node.object {
		if member.name == name {
			return member.value, true
		}
	}
	return jsonNode{}, false
}

func requireObjectMembers(node jsonNode, path string, names ...string) error {
	wanted := make(map[string]struct{}, len(names))
	for _, name := range names {
		wanted[name] = struct{}{}
	}
	for _, member := range node.object {
		if _, ok := wanted[member.name]; !ok {
			return invalidValueJSONf("%s has unknown member %q", path, member.name)
		}
	}
	for _, name := range names {
		if _, ok := objectMember(node, name); !ok {
			return invalidValueJSONf("%s is missing member %q", path, name)
		}
	}
	return nil
}

func validateJSONStringEscapes(data []byte) error {
	for i := 0; i < len(data); i++ {
		if data[i] != '"' {
			continue
		}
		for i++; i < len(data); i++ {
			switch data[i] {
			case '"':
				goto nextString
			case '\\':
				if i+1 >= len(data) {
					return invalidValueJSONf("unterminated JSON string escape")
				}
				i++
				if data[i] != 'u' {
					continue
				}
				first, ok := decodeHexQuad(data, i+1)
				if !ok {
					return invalidValueJSONf("invalid JSON Unicode escape")
				}
				i += 4
				if first >= 0xd800 && first <= 0xdbff {
					if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
						return invalidValueJSONf("unpaired high surrogate in JSON string")
					}
					second, ok := decodeHexQuad(data, i+3)
					if !ok || second < 0xdc00 || second > 0xdfff || !utf16.IsSurrogate(rune(second)) {
						return invalidValueJSONf("unpaired high surrogate in JSON string")
					}
					i += 6
				} else if first >= 0xdc00 && first <= 0xdfff {
					return invalidValueJSONf("unpaired low surrogate in JSON string")
				}
			}
		}
		return invalidValueJSONf("unterminated JSON string")
	nextString:
	}
	return nil
}

func decodeHexQuad(data []byte, start int) (uint16, bool) {
	if start < 0 || start+4 > len(data) {
		return 0, false
	}
	var value uint16
	for _, digit := range data[start : start+4] {
		value <<= 4
		switch {
		case digit >= '0' && digit <= '9':
			value += uint16(digit - '0')
		case digit >= 'a' && digit <= 'f':
			value += uint16(digit-'a') + 10
		case digit >= 'A' && digit <= 'F':
			value += uint16(digit-'A') + 10
		default:
			return 0, false
		}
	}
	return value, true
}

func invalidValueJSONf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidValueJSON, fmt.Sprintf(format, args...))
}
