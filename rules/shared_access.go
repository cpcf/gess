package rules

// AsListShared returns the value's backing list without copying when the kind
// is ValueList. The returned slice aliases internal storage and must not be
// modified; use AsList for a defensive copy.
func (v Value) AsListShared() ([]Value, bool) {
	if v.Kind() != ValueList {
		return nil, false
	}
	return v.data.([]Value), true
}

// AsMapShared returns the value's backing map without copying when the kind is
// ValueMap. The returned map aliases internal storage and must not be modified;
// use AsMap for a defensive copy.
func (v Value) AsMapShared() (map[string]Value, bool) {
	if v.Kind() != ValueMap {
		return nil, false
	}
	return v.data.(map[string]Value), true
}
