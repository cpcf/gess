package rules

// ValueKind is a Value's type tag.
type ValueKind uint8

const (
	valueKindUnknown ValueKind = iota
	ValueAny
	ValueNull
	ValueBool
	ValueInt
	ValueFloat
	ValueString
	ValueList
	ValueMap
	valueKindInvalid ValueKind = 255
)

func (k ValueKind) String() string {
	switch k {
	case ValueAny:
		return "any"
	case ValueNull:
		return "null"
	case ValueBool:
		return "bool"
	case ValueInt:
		return "int"
	case ValueFloat:
		return "float"
	case ValueString:
		return "string"
	case ValueList:
		return "list"
	case ValueMap:
		return "map"
	default:
		return "invalid"
	}
}
