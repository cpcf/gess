package engine

import (
	"context"
	"time"
)

// sessionDiagnosticsExporter owns event delivery and explain-history state.
// It is named so Fork must make an explicit ownership decision for every
// diagnostics field rather than accidentally shallow-copying Session fields.
type sessionDiagnosticsExporter struct {
	listeners           []eventListenerRegistration
	allEventListeners   int
	eventListenerCounts map[EventType]int
	explainLog          *explainLog
	eventClock          func() time.Time
	nextEventSequence   uint64
}

func newSessionDiagnosticsExporter(cfg sessionConfig, nextEventSequence uint64) sessionDiagnosticsExporter {
	listeners := cloneEventListenerRegistrations(cfg.listeners)
	all, counts := countEventListenerSubscriptions(listeners)
	clock := cfg.eventClock
	if clock == nil {
		clock = time.Now
	}
	return sessionDiagnosticsExporter{
		listeners:           listeners,
		allEventListeners:   all,
		eventListenerCounts: counts,
		explainLog:          cfg.explainLog,
		eventClock:          clock,
		nextEventSequence:   nextEventSequence,
	}
}

func (d *sessionDiagnosticsExporter) nextSequence() uint64 {
	d.nextEventSequence++
	return d.nextEventSequence
}

func (d *sessionDiagnosticsExporter) now() time.Time { return d.eventClock() }

func (d *sessionDiagnosticsExporter) emit(ctx context.Context, event Event) {
	for _, registration := range d.listeners {
		if registration.subscribesTo(event.Type) {
			_ = registration.listener.HandleEvent(ctx, event.clone())
		}
	}
}

func (d *sessionDiagnosticsExporter) hasListenersFor(eventType EventType) bool {
	if len(d.listeners) == 0 {
		return false
	}
	return d.allEventListeners > 0 || d.eventListenerCounts[eventType] > 0
}

func (d *sessionDiagnosticsExporter) hasExplainLog() bool { return d.explainLog != nil }

func (d *sessionDiagnosticsExporter) captureBindings(activationID ActivationID, bindings []BindingValue) {
	if d.explainLog != nil {
		d.explainLog.captureBindings(activationID, bindings)
	}
}

func (d *sessionDiagnosticsExporter) enrich(derivation *Derivation, revision *Ruleset) bool {
	if d.explainLog == nil {
		return false
	}
	d.explainLog.enrich(derivation, revision)
	return true
}
