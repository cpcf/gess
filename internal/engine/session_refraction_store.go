package engine

// sessionRefractionStore retains compact, graph-independent records for fired
// matches after the agenda releases consumed rows. The graph remains the
// authority for whether a match exists; terminal removal deltas remove the
// corresponding record.
type sessionRefractionStore struct {
	byIdentity map[activationLookupKey]activation
}

func (s *sessionRefractionStore) record(agenda *agenda, fired activation) {
	if s == nil || agenda == nil || fired.ruleRevisionID.IsZero() || fired.identityKey == (candidateIdentityKey{}) {
		return
	}
	materialized := agenda.publicActivation(&fired)
	materialized.token = tokenRef{}
	materialized.status = activationStatusConsumed
	if s.byIdentity == nil {
		s.byIdentity = make(map[activationLookupKey]activation)
	}
	s.byIdentity[activationLookupKey{ruleRevisionID: fired.ruleRevisionID, identityKey: fired.identityKey}] = materialized
}

func (s *sessionRefractionStore) removeDelta(revision *Ruleset, removed []reteTerminalTokenDelta) {
	if s == nil || len(s.byIdentity) == 0 || revision == nil {
		return
	}
	for _, delta := range removed {
		if delta.token.isZero() || delta.ruleRevisionID.IsZero() {
			continue
		}
		identity := candidateIdentityForTerminalTokenDelta(revision, delta)
		delete(s.byIdentity, activationLookupKey{ruleRevisionID: delta.ruleRevisionID, identityKey: identity.key})
	}
	if len(s.byIdentity) == 0 {
		s.byIdentity = nil
	}
}

func (s *sessionRefractionStore) clear() {
	if s == nil {
		return
	}
	clear(s.byIdentity)
	s.byIdentity = nil
}

func (s sessionRefractionStore) clone() sessionRefractionStore {
	if len(s.byIdentity) == 0 {
		return sessionRefractionStore{}
	}
	out := sessionRefractionStore{byIdentity: make(map[activationLookupKey]activation, len(s.byIdentity))}
	for key, activation := range s.byIdentity {
		out.byIdentity[key] = activation.clone()
	}
	return out
}
