package engine

import "testing"

func mustWorkingFactByID(t testing.TB, session *Session, id FactID) *workingFact {
	t.Helper()
	fact, ok := session.workingFactByID(id)
	if !ok || fact == nil {
		t.Fatalf("missing working fact %q", id)
	}
	return fact
}
