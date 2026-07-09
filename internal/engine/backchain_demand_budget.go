package engine

type backchainDemandCascadeCounters struct {
	Cascades  int
	Steps     int
	LengthMax int
	LimitHits int
}

type backchainDemandCascadeBudget struct {
	session *Session
	steps   int
	started bool
}

func newBackchainDemandCascadeBudget(session *Session) backchainDemandCascadeBudget {
	return backchainDemandCascadeBudget{session: session}
}

func (b *backchainDemandCascadeBudget) consume() error {
	if b == nil || b.session == nil {
		return nil
	}
	if !b.started {
		b.started = true
		b.session.demandCounters.Cascades++
	}
	limit := b.session.demandLimit
	if limit > 0 && b.steps >= limit {
		b.session.demandCounters.LimitHits++
		return &DemandCascadeLimitError{Limit: limit, Steps: b.steps}
	}
	b.steps++
	b.session.demandCounters.Steps++
	b.session.demandCounters.LengthMax = max(
		b.session.demandCounters.LengthMax,
		b.steps,
	)
	return nil
}
