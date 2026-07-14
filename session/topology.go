package session

import (
	"context"

	"github.com/cpcf/gess/internal/engine"
)

// TopologySchemaVersion is the current immutable Rete topology document version.
const TopologySchemaVersion = engine.TopologySchemaVersion

type TopologyMode = engine.TopologyMode

const (
	TopologyModeFull    = engine.TopologyModeFull
	TopologyModeFocused = engine.TopologyModeFocused
	TopologyModeSummary = engine.TopologyModeSummary
)

type TopologySelector = engine.TopologySelector
type TopologyRequest = engine.TopologyRequest
type TopologyFocus = engine.TopologyFocus
type TopologySource = engine.TopologySource
type TopologyOwner = engine.TopologyOwner
type TopologyNode = engine.TopologyNode
type TopologyEdge = engine.TopologyEdge
type TopologyTotals = engine.TopologyTotals
type TopologyReport = engine.TopologyReport

// Topology returns a bounded, immutable projection of the compiled Rete graph.
// Session state is used only to resolve fact and activation focus selectors.
func (s *Session) Topology(ctx context.Context, request TopologyRequest) (TopologyReport, error) {
	return s.engineSession().Topology(ctx, request)
}
