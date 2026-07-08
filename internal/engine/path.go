package engine

import (
	"fmt"
	"strings"

	gessrules "github.com/cpcf/gess/rules"
)

type PathSegmentKind = gessrules.PathSegmentKind

const (
	PathSegmentRoot  = gessrules.PathSegmentRoot
	PathSegmentMap   = gessrules.PathSegmentMap
	PathSegmentIndex = gessrules.PathSegmentIndex
)

type PathSegment = gessrules.PathSegment
type PathSpec = gessrules.PathSpec

type compiledPathAccess struct {
	path               PathSpec
	root               string
	rootSlot           int
	presenceGuaranteed bool
}

func Path(root string, segments ...PathSegment) PathSpec {
	return gessrules.Path(root, segments...)
}

func MapKey(key string) PathSegment {
	return gessrules.MapKey(key)
}

func ListIndex(index int) PathSegment {
	return gessrules.ListIndex(index)
}

func fieldPath(field string) PathSpec {
	return Path(strings.TrimSpace(field))
}

func pathOrField(path PathSpec, field string) PathSpec {
	if !pathIsZero(path) {
		return clonePathSpec(path)
	}
	field = strings.TrimSpace(field)
	if field == "" {
		return PathSpec{}
	}
	return fieldPath(field)
}

func hasAmbiguousFieldAndPath(field string, path PathSpec) bool {
	return strings.TrimSpace(field) != "" && !pathIsZero(path)
}

func clonePathSpec(p PathSpec) PathSpec {
	if len(p.Segments) == 0 {
		return PathSpec{}
	}
	return PathSpec{Segments: append([]PathSegment(nil), p.Segments...)}
}

func pathIsZero(p PathSpec) bool {
	return len(p.Segments) == 0
}

func pathRoot(p PathSpec) string {
	if len(p.Segments) == 0 || p.Segments[0].Kind != PathSegmentRoot {
		return ""
	}
	return p.Segments[0].Key
}

func pathTopLevel(p PathSpec) bool {
	return len(p.Segments) == 1 && p.Segments[0].Kind == PathSegmentRoot
}

func pathDisplay(p PathSpec) string {
	return p.String()
}

func validatePathSpec(p PathSpec) error {
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

func compilePathAccess(path PathSpec, template *compiledTemplate) (compiledPathAccess, ValueKind, error) {
	normalized := clonePathSpec(path)
	if len(normalized.Segments) > 0 && normalized.Segments[0].Kind == PathSegmentRoot {
		normalized.Segments[0].Key = strings.TrimSpace(normalized.Segments[0].Key)
	}
	if err := validatePathSpec(normalized); err != nil {
		return compiledPathAccess{}, valueKindUnknown, err
	}
	root := pathRoot(normalized)
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
		return compiledPathAccess{}, valueKindUnknown, fmt.Errorf("%w: unknown root field %q", ErrInvalidPath, root)
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
				return compiledPathAccess{}, valueKindUnknown, fmt.Errorf("%w: map root %q cannot be traversed by %s", ErrInvalidPath, root, normalized.Segments[1].Kind)
			}
			kind = ValueAny
		case ValueList:
			if normalized.Segments[1].Kind != PathSegmentIndex {
				return compiledPathAccess{}, valueKindUnknown, fmt.Errorf("%w: list root %q cannot be traversed by %s", ErrInvalidPath, root, normalized.Segments[1].Kind)
			}
			kind = ValueAny
		case ValueAny:
			kind = ValueAny
		default:
			return compiledPathAccess{}, valueKindUnknown, fmt.Errorf("%w: scalar root %q cannot be traversed", ErrInvalidPath, root)
		}
	}
	return access, kind, nil
}

func (a compiledPathAccess) clone() compiledPathAccess {
	a.path = clonePathSpec(a.path)
	return a
}

func (a compiledPathAccess) topLevel() bool {
	return pathTopLevel(a.path)
}

func (a compiledPathAccess) nested() bool {
	return len(a.path.Segments) > 1
}

func (a compiledPathAccess) display() string {
	return pathDisplay(a.path)
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

func (a compiledPathAccess) valueFromWorkingFact(fact *workingFact, compactSlotStore *factCompactSlotStore) (Value, bool) {
	if len(a.path.Segments) == 0 {
		return Value{}, false
	}
	value, ok := fact.compiledFieldValue(a.root, a.rootSlot, compactSlotStore)
	if !ok {
		return Value{}, false
	}
	return resolveValuePathTail(value, a.path.Segments[1:])
}

func (a compiledPathAccess) valueFromWorkingFactWithCounters(fact *workingFact, compactSlotStore *factCompactSlotStore, span *propagationCounterSpan) (Value, bool) {
	value, ok := a.valueFromWorkingFact(fact, compactSlotStore)
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
			values, _ := current.AsMapShared()
			next, ok := values[segment.Key]
			if !ok {
				return Value{}, false
			}
			current = next
		case PathSegmentIndex:
			if current.Kind() != ValueList {
				return Value{}, false
			}
			values, _ := current.AsListShared()
			if segment.Index < 0 || segment.Index >= len(values) {
				return Value{}, false
			}
			current = values[segment.Index]
		default:
			return Value{}, false
		}
	}
	return current, true
}
