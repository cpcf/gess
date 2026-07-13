package server

import (
	"encoding/json"
	"fmt"
	"math"

	"github.com/cpcf/gess/rules"
	"github.com/cpcf/gess/scenario"
)

func projectValue(value rules.Value) any {
	return scenario.NewValue(value)
}

func decodeJSONObject(value map[string]any) (map[string]any, error) {
	if value == nil {
		return nil, nil
	}
	out := make(map[string]any, len(value))
	for key, item := range value {
		decoded, err := decodeJSONValue(item)
		if err != nil {
			return nil, fmt.Errorf("value %q: %w", key, err)
		}
		out[key] = decoded
	}
	return out, nil
}

func decodeJSONValue(value any) (any, error) {
	switch value := value.(type) {
	case float64:
		if !math.IsNaN(value) && !math.IsInf(value, 0) && math.Trunc(value) == value && value >= math.MinInt64 && value < math.MaxInt64 {
			return int64(value), nil
		}
		return value, nil
	case []any:
		out := make([]any, len(value))
		for i, item := range value {
			decoded, err := decodeJSONValue(item)
			if err != nil {
				return nil, fmt.Errorf("list item %d: %w", i, err)
			}
			out[i] = decoded
		}
		return out, nil
	case map[string]any:
		if kind, ok := value["kind"].(string); ok && isTypedValueKind(kind) {
			encoded, err := json.Marshal(value)
			if err != nil {
				return nil, fmt.Errorf("marshal typed value: %w", err)
			}
			decoded, err := scenario.UnmarshalValue(encoded)
			if err != nil {
				return nil, err
			}
			return decoded, nil
		}
		return decodeJSONObject(value)
	default:
		return value, nil
	}
}

func isTypedValueKind(kind string) bool {
	switch kind {
	case "null", "bool", "int", "float", "string", "list", "map":
		return true
	default:
		return false
	}
}
