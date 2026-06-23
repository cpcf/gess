package gess

import (
	"fmt"
	"strconv"
	"strings"
)

type PathSegmentKind string

const (
	PathSegmentRoot  PathSegmentKind = "root"
	PathSegmentMap   PathSegmentKind = "map"
	PathSegmentIndex PathSegmentKind = "index"
)

type PathSegment struct {
	Kind  PathSegmentKind
	Key   string
	Index int
}

type PathSpec struct {
	Segments []PathSegment
}

type compiledPathAccess struct {
	path               PathSpec
	root               string
	rootSlot           int
	presenceGuaranteed bool
}

func Path(root string, segments ...PathSegment) PathSpec {
	out := PathSpec{Segments: make([]PathSegment, 0, len(segments)+1)}
	out.Segments = append(out.Segments, PathSegment{Kind: PathSegmentRoot, Key: strings.TrimSpace(root)})
	out.Segments = append(out.Segments, segments...)
	return out
}

func MapKey(key string) PathSegment {
	return PathSegment{Kind: PathSegmentMap, Key: key}
}

func ListIndex(index int) PathSegment {
	return PathSegment{Kind: PathSegmentIndex, Index: index}
}

func fieldPath(field string) PathSpec {
	return Path(strings.TrimSpace(field))
}

func pathOrField(path PathSpec, field string) PathSpec {
	if !path.isZero() {
		return path.clone()
	}
	field = strings.TrimSpace(field)
	if field == "" {
		return PathSpec{}
	}
	return fieldPath(field)
}

func hasAmbiguousFieldAndPath(field string, path PathSpec) bool {
	return strings.TrimSpace(field) != "" && !path.isZero()
}

func (p PathSpec) clone() PathSpec {
	if len(p.Segments) == 0 {
		return PathSpec{}
	}
	return PathSpec{Segments: append([]PathSegment(nil), p.Segments...)}
}

func (p PathSpec) isZero() bool {
	return len(p.Segments) == 0
}

func (p PathSpec) root() string {
	if len(p.Segments) == 0 || p.Segments[0].Kind != PathSegmentRoot {
		return ""
	}
	return p.Segments[0].Key
}

func (p PathSpec) topLevel() bool {
	return len(p.Segments) == 1 && p.Segments[0].Kind == PathSegmentRoot
}

func (p PathSpec) String() string {
	return p.display()
}

func (p PathSpec) display() string {
	if len(p.Segments) == 0 {
		return "<invalid-path>"
	}
	var b strings.Builder
	for i, segment := range p.Segments {
		switch segment.Kind {
		case PathSegmentRoot:
			if i != 0 {
				b.WriteString(".<invalid-root>")
				continue
			}
			b.WriteString(segment.Key)
		case PathSegmentMap:
			b.WriteByte('.')
			b.WriteString(strconv.Quote(segment.Key))
		case PathSegmentIndex:
			b.WriteByte('[')
			b.WriteString(strconv.Itoa(segment.Index))
			b.WriteByte(']')
		default:
			b.WriteString(".<invalid-segment>")
		}
	}
	return b.String()
}

func (p PathSpec) validate() error {
	if len(p.Segments) == 0 {
		return fmt.Errorf("%w: path requires a root segment", ErrInvalidPath)
	}
	for i, segment := range p.Segments {
		switch segment.Kind {
		case PathSegmentRoot:
			if i != 0 {
				return fmt.Errorf("%w: root segment must be first", ErrInvalidPath)
			}
			if strings.TrimSpace(segment.Key) == "" {
				return fmt.Errorf("%w: root field is required", ErrInvalidPath)
			}
		case PathSegmentMap:
			if i == 0 {
				return fmt.Errorf("%w: map key cannot be the root segment", ErrInvalidPath)
			}
		case PathSegmentIndex:
			if i == 0 {
				return fmt.Errorf("%w: list index cannot be the root segment", ErrInvalidPath)
			}
			if segment.Index < 0 {
				return fmt.Errorf("%w: list index must be non-negative", ErrInvalidPath)
			}
		default:
			return fmt.Errorf("%w: unknown path segment kind %q", ErrInvalidPath, segment.Kind)
		}
	}
	return nil
}

func compilePathAccess(path PathSpec, template *Template) (compiledPathAccess, ValueKind, error) {
	normalized := path.clone()
	if len(normalized.Segments) > 0 && normalized.Segments[0].Kind == PathSegmentRoot {
		normalized.Segments[0].Key = strings.TrimSpace(normalized.Segments[0].Key)
	}
	if err := normalized.validate(); err != nil {
		return compiledPathAccess{}, "", err
	}
	root := normalized.root()
	access := compiledPathAccess{
		path:     normalized,
		root:     root,
		rootSlot: -1,
	}
	kind := ValueAny
	if template == nil || !template.closed {
		return access, kind, nil
	}
	slot, ok := template.fieldSlot(root)
	if !ok {
		return compiledPathAccess{}, "", fmt.Errorf("%w: unknown root field %q", ErrInvalidPath, root)
	}
	access.rootSlot = slot
	if spec, ok := template.fieldsByName[root]; ok {
		kind = spec.Kind
		access.presenceGuaranteed = spec.Required || spec.HasDefault
	}
	if len(normalized.Segments) > 1 {
		switch kind {
		case ValueMap:
			if normalized.Segments[1].Kind != PathSegmentMap {
				return compiledPathAccess{}, "", fmt.Errorf("%w: map root %q cannot be traversed by %s", ErrInvalidPath, root, normalized.Segments[1].Kind)
			}
			kind = ValueAny
		case ValueList:
			if normalized.Segments[1].Kind != PathSegmentIndex {
				return compiledPathAccess{}, "", fmt.Errorf("%w: list root %q cannot be traversed by %s", ErrInvalidPath, root, normalized.Segments[1].Kind)
			}
			kind = ValueAny
		case ValueAny:
			kind = ValueAny
		default:
			return compiledPathAccess{}, "", fmt.Errorf("%w: scalar root %q cannot be traversed", ErrInvalidPath, root)
		}
	}
	return access, kind, nil
}

func (a compiledPathAccess) clone() compiledPathAccess {
	a.path = a.path.clone()
	return a
}

func (a compiledPathAccess) topLevel() bool {
	return a.path.topLevel()
}

func (a compiledPathAccess) nested() bool {
	return len(a.path.Segments) > 1
}

func (a compiledPathAccess) display() string {
	return a.path.display()
}

func (a compiledPathAccess) valueFromFact(fact conditionFactRef) (Value, bool) {
	if len(a.path.Segments) == 0 {
		return Value{}, false
	}
	value, ok := fact.compiledFieldValue(a.root, a.rootSlot)
	if !ok {
		return Value{}, false
	}
	return resolveValuePathTail(value, a.path.Segments[1:])
}

func (a compiledPathAccess) valueFromFactWithCounters(fact conditionFactRef, span *propagationCounterSpan) (Value, bool) {
	value, ok := a.valueFromFact(fact)
	if a.nested() && span != nil {
		span.recordNestedPathEvaluation(ok)
	}
	return value, ok
}

func (a compiledPathAccess) valueFromSnapshot(fact FactSnapshot) (Value, bool) {
	if len(a.path.Segments) == 0 {
		return Value{}, false
	}
	value, ok := fact.compiledFieldValue(a.root, a.rootSlot)
	if !ok {
		return Value{}, false
	}
	return resolveValuePathTail(value, a.path.Segments[1:])
}

func (a compiledPathAccess) valueFromWorkingFact(fact *workingFact) (Value, bool) {
	if len(a.path.Segments) == 0 {
		return Value{}, false
	}
	value, ok := fact.compiledFieldValue(a.root, a.rootSlot)
	if !ok {
		return Value{}, false
	}
	return resolveValuePathTail(value, a.path.Segments[1:])
}

func (a compiledPathAccess) valueFromWorkingFactWithCounters(fact *workingFact, span *propagationCounterSpan) (Value, bool) {
	value, ok := a.valueFromWorkingFact(fact)
	if a.nested() && span != nil {
		span.recordNestedPathEvaluation(ok)
	}
	return value, ok
}

func resolveValuePathTail(value Value, segments []PathSegment) (Value, bool) {
	current := value
	for _, segment := range segments {
		switch segment.Kind {
		case PathSegmentMap:
			if current.Kind() != ValueMap {
				return Value{}, false
			}
			values, ok := current.data.(map[string]Value)
			if !ok {
				return Value{}, false
			}
			next, ok := values[segment.Key]
			if !ok {
				return Value{}, false
			}
			current = next
		case PathSegmentIndex:
			if current.Kind() != ValueList {
				return Value{}, false
			}
			values, ok := current.data.([]Value)
			if !ok || segment.Index < 0 || segment.Index >= len(values) {
				return Value{}, false
			}
			current = values[segment.Index]
		default:
			return Value{}, false
		}
	}
	return current, true
}
