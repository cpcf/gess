package engine

// sessionBackchainStore owns persistent demand-support state, demand cascade
// configuration and metrics, and the transient cascade/query-proof contexts
// for one session. Forks copy configuration and counters, rebuild persistent
// support state from their graph, and always start with fresh transient state.
type sessionBackchainStore struct {
	nextDemandSupportID  backchainDemandSupportID
	freeDemandSupportIDs []backchainDemandSupportID
	demandSupports       backchainDemandSupportTable
	demandSupportRecords []backchainDemandSupportRecord
	demandOwnerRecords   []backchainDemandOwnerSupportRecord
	inlineSupports       backchainDemandInlineSupportIndex
	supportOwners        backchainDemandOwnerSupportIndex
	demandByFact         backchainDemandFactSupportTable
	demandByDemand       backchainDemandFactSupportTable
	demandLimit          int
	demandCounters       backchainDemandCascadeCounters
	activeDemandCascade  *backchainDemandCascadeBudget
	activeQueryProof     *backchainQueryProofContext
	queryProofScratch    backchainQueryProofContext
}

func newSessionBackchainStore(demandLimit int) sessionBackchainStore {
	return sessionBackchainStore{demandLimit: demandLimit}
}

func (s sessionBackchainStore) forkForRebuild(demandLimit int) sessionBackchainStore {
	return sessionBackchainStore{
		demandLimit:    demandLimit,
		demandCounters: s.demandCounters,
	}
}

func (s *sessionBackchainStore) clearPersistent() {
	if s == nil {
		return
	}
	s.demandSupports.clear()
	s.inlineSupports.clear()
	s.supportOwners.clear()
	clear(s.demandSupportRecords)
	s.demandSupportRecords = s.demandSupportRecords[:0]
	clear(s.demandOwnerRecords)
	s.demandOwnerRecords = s.demandOwnerRecords[:0]
	s.demandByFact.clear()
	s.demandByDemand.clear()
	s.nextDemandSupportID = 0
	clear(s.freeDemandSupportIDs)
	s.freeDemandSupportIDs = s.freeDemandSupportIDs[:0]
}

func (s *sessionBackchainStore) nextSupportID() backchainDemandSupportID {
	if count := len(s.freeDemandSupportIDs); count > 0 {
		id := s.freeDemandSupportIDs[count-1]
		s.freeDemandSupportIDs[count-1] = 0
		s.freeDemandSupportIDs = s.freeDemandSupportIDs[:count-1]
		return id
	}
	s.nextDemandSupportID++
	if s.nextDemandSupportID == 0 {
		s.nextDemandSupportID++
	}
	return s.nextDemandSupportID
}
