package session

import (
	"context"

	"github.com/cpcf/gess/internal/engine"
)

const MemoryInspectionSchemaVersion = engine.MemoryInspectionSchemaVersion

type MemoryInspectionRequest = engine.MemoryInspectionRequest
type MemoryOwnerSummary = engine.MemoryOwnerSummary
type MemoryNodeSummary = engine.MemoryNodeSummary
type MemoryRow = engine.MemoryRow
type MemoryRowCollection = engine.MemoryRowCollection
type MemoryInspectionReport = engine.MemoryInspectionReport

func (s *Session) MemoryInspection(ctx context.Context, request MemoryInspectionRequest) (MemoryInspectionReport, error) {
	return s.engineSession().MemoryInspection(ctx, request)
}
