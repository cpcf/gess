package rules

import (
	"strconv"
	"strings"
)

// PathSegment is one step of a PathSpec.
type PathSegment struct {
	Kind  PathSegmentKind
	Key   string
	Index int
}

// PathSpec describes a path into a structured value.
type PathSpec struct {
	Segments []PathSegment
}

// Path builds a PathSpec rooted at the field root, followed by segments.
func Path(root string, segments ...PathSegment) PathSpec {
	out := PathSpec{Segments: make([]PathSegment, 0, len(segments)+1)}
	out.Segments = append(out.Segments, PathSegment{Kind: PathSegmentRoot, Key: strings.TrimSpace(root)})
	out.Segments = append(out.Segments, segments...)
	return out
}

// MapKey builds a map-key path segment.
func MapKey(key string) PathSegment {
	return PathSegment{Kind: PathSegmentMap, Key: key}
}

// ListIndex builds a list-index path segment.
func ListIndex(index int) PathSegment {
	return PathSegment{Kind: PathSegmentIndex, Index: index}
}

func (p PathSpec) String() string {
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
