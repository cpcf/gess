package engine

import (
	"context"
	"os"
	"testing"
)

func TestMannersArenaRecyclingDebug(t *testing.T) {
	if os.Getenv("GESS_MANNERS_ARENA_DEBUG") == "" {
		t.Skip("set GESS_MANNERS_ARENA_DEBUG=1 to run")
	}
	guests := mannersGuests(16)
	revision := mustCompileMannersRuleset(t)
	session, err := NewSession(revision, WithInitialFacts(mannersInitialFacts(guests)...))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := session.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	arena := session.rete.graphBeta.arena
	live, pending, zeroRef := 0, 0, 0
	for _, chunk := range arena.chunks {
		for i := range chunk {
			row := &chunk[i]
			if row.rowGen == 0 {
				continue
			}
			live++
			if row.pendingFree {
				pending++
			}
			if row.refs == 0 {
				zeroRef++
			}
		}
	}
	t.Logf("arena count=%d live=%d freeRows=%d pendingFree=%d liveZeroRef=%d recentRows=%d",
		arena.count, live, len(arena.freeRows), pending, zeroRef, len(arena.recentRows))
	t.Logf("arena stats grown=%d reused=%d freed=%d swept=%d flushes=%d",
		arena.statGrown, arena.statReused, arena.statFreed, arena.statSwept, arena.statFlushes)
}
