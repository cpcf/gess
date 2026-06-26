package gess

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

type backchainDemandSupportRecord struct {
	key          string
	demandFactID FactID
	supportFacts []backchainDemandSupportFact
}

func (s *Session) ensureBackchainDemandSupportMaps() {
	if s == nil {
		return
	}
	if s.backchainDemandSupports == nil {
		s.backchainDemandSupports = make(map[string]backchainDemandSupportRecord)
	}
	if s.backchainDemandByFact == nil {
		s.backchainDemandByFact = make(map[FactID]map[string]struct{})
	}
	if s.backchainDemandByDemand == nil {
		s.backchainDemandByDemand = make(map[FactID]map[string]struct{})
	}
}

func (s *Session) addBackchainDemandSupport(demandFact *workingFact, request backchainDemandRequest) {
	if s == nil || demandFact == nil || demandFact.id.IsZero() || len(request.supportFacts) == 0 {
		return
	}
	key := backchainDemandSupportKey(request)
	if key == "" {
		return
	}
	s.ensureBackchainDemandSupportMaps()
	if _, exists := s.backchainDemandSupports[key]; exists {
		return
	}
	record := backchainDemandSupportRecord{
		key:          key,
		demandFactID: demandFact.id,
		supportFacts: cloneBackchainDemandSupportFacts(request.supportFacts),
	}
	s.backchainDemandSupports[key] = record
	for _, support := range record.supportFacts {
		keys := s.backchainDemandByFact[support.id]
		if keys == nil {
			keys = make(map[string]struct{})
			s.backchainDemandByFact[support.id] = keys
		}
		keys[key] = struct{}{}
	}
	demandKeys := s.backchainDemandByDemand[demandFact.id]
	if demandKeys == nil {
		demandKeys = make(map[string]struct{})
		s.backchainDemandByDemand[demandFact.id] = demandKeys
	}
	demandKeys[key] = struct{}{}
}

func (s *Session) removeBackchainDemandSupportForRequest(ctx context.Context, request backchainDemandRequest, origin mutationOrigin) (reteAgendaDelta, error) {
	key := backchainDemandSupportKey(request)
	if key == "" {
		return reteAgendaDelta{supported: true}, nil
	}
	return s.removeBackchainDemandSupportKey(ctx, key, origin)
}

func (s *Session) removeBackchainDemandSupportsForFact(ctx context.Context, id FactID, origin mutationOrigin) (reteAgendaDelta, error) {
	return s.removeBackchainDemandSupportsForFactVersionMatch(ctx, id, 0, false, origin)
}

func (s *Session) removeBackchainDemandSupportsForFactVersion(ctx context.Context, id FactID, version FactVersion, origin mutationOrigin) (reteAgendaDelta, error) {
	return s.removeBackchainDemandSupportsForFactVersionMatch(ctx, id, version, true, origin)
}

func (s *Session) removeBackchainDemandSupportsForFactVersionMatch(ctx context.Context, id FactID, version FactVersion, matchVersion bool, origin mutationOrigin) (reteAgendaDelta, error) {
	combined := reteAgendaDelta{supported: true}
	if s == nil || id.IsZero() || len(s.backchainDemandByFact) == 0 {
		return combined, nil
	}
	keys := s.backchainDemandByFact[id]
	if len(keys) == 0 {
		return combined, nil
	}
	supportKeys := make([]string, 0, len(keys))
	for key := range keys {
		record, ok := s.backchainDemandSupports[key]
		if !ok {
			continue
		}
		if matchVersion && !backchainDemandSupportRecordContainsFactVersion(record, id, version) {
			continue
		}
		supportKeys = append(supportKeys, key)
	}
	sort.Strings(supportKeys)
	for _, key := range supportKeys {
		delta, err := s.removeBackchainDemandSupportKey(ctx, key, origin)
		if err != nil {
			return combined, err
		}
		combined = mergeReteAgendaDelta(combined, delta)
	}
	return combined, nil
}

func (s *Session) removeBackchainDemandSupportKey(ctx context.Context, key string, origin mutationOrigin) (reteAgendaDelta, error) {
	combined := reteAgendaDelta{supported: true}
	if s == nil || key == "" || len(s.backchainDemandSupports) == 0 {
		return combined, nil
	}
	record, ok := s.backchainDemandSupports[key]
	if !ok {
		return combined, nil
	}
	delete(s.backchainDemandSupports, key)
	for _, support := range record.supportFacts {
		keys := s.backchainDemandByFact[support.id]
		delete(keys, key)
		if len(keys) == 0 {
			delete(s.backchainDemandByFact, support.id)
		}
	}
	demandKeys := s.backchainDemandByDemand[record.demandFactID]
	delete(demandKeys, key)
	if len(demandKeys) > 0 {
		return combined, nil
	}
	delete(s.backchainDemandByDemand, record.demandFactID)
	if _, ok := s.workingFactByID(record.demandFactID); !ok {
		return combined, nil
	}
	_, delta, err := s.removeFactImmediate(ctx, record.demandFactID, origin, true)
	if err != nil {
		return combined, err
	}
	delta = normalizeBackchainDemandNoopDelta(delta)
	return mergeReteAgendaDelta(combined, delta), nil
}

func normalizeBackchainDemandNoopDelta(delta reteAgendaDelta) reteAgendaDelta {
	if delta.supported {
		return delta
	}
	if len(delta.added) != 0 || len(delta.removed) != 0 || len(delta.updated) != 0 || len(delta.demands) != 0 || len(delta.resolvedDemands) != 0 {
		return delta
	}
	delta.supported = true
	return delta
}

func (s *Session) clearBackchainDemandSupports() {
	if s == nil {
		return
	}
	clear(s.backchainDemandSupports)
	clear(s.backchainDemandByFact)
	clear(s.backchainDemandByDemand)
}

func backchainDemandSupportRecordContainsFactVersion(record backchainDemandSupportRecord, id FactID, version FactVersion) bool {
	for _, support := range record.supportFacts {
		if support.id == id && support.version == version {
			return true
		}
	}
	return false
}

func backchainDemandSupportKey(request backchainDemandRequest) string {
	if request.templateKey == "" || len(request.supportFacts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("template=")
	b.WriteString(string(request.templateKey))
	b.WriteString("|support=")
	for i, support := range request.supportFacts {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(support.id.String())
		b.WriteByte('@')
		b.WriteString(fmt.Sprint(uint64(support.version)))
	}
	b.WriteString("|slots=")
	for i, slot := range request.slots {
		if i > 0 {
			b.WriteByte(',')
		}
		if !slot.ok {
			b.WriteString("<missing>")
			continue
		}
		b.WriteString(slot.value.canonicalKey())
	}
	return b.String()
}

func cloneBackchainDemandSupportFacts(in []backchainDemandSupportFact) []backchainDemandSupportFact {
	if len(in) == 0 {
		return nil
	}
	out := make([]backchainDemandSupportFact, len(in))
	copy(out, in)
	return out
}
