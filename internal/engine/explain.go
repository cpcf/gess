package engine

// Derivation is the recursive explanation of one fact: its support state, the
// rule firing that produced it, the facts it logically depends on (recursively),
// and — with a retained explain log — its mutation lineage this generation.
//
// Snapshot.Explain populates Fact, Support, DependsOn, Truncated, and, for
// logically-supported facts, ProducedBy from the support edge. Session.Explain
// additionally fills ProducedBy for stated facts and History from the log.
type Derivation struct {
	Fact       FactSnapshot
	Support    FactSupportState
	ProducedBy *Firing
	DependsOn  []Derivation
	History    []MutationRecord
	Truncated  bool
}

// Firing identifies the rule activation that produced a fact state, with the
// concrete bound values its action evaluated against. Action and Bindings are
// only populated by the session-level, log-backed explanation; the snapshot
// tier leaves them empty.
type Firing struct {
	RuleID          RuleID
	RuleName        string
	RuleRevisionID  RuleRevisionID
	ActivationID    ActivationID
	Generation      Generation
	Action          string
	Bindings        []BindingValue
	BindingsPartial bool
	SupportingFacts []FactID
}

// BindingValue is one condition binding and the value an action saw for it.
// FromFact is the zero FactID for computed or scalar bindings.
type BindingValue struct {
	Name     string
	Value    Value
	FromFact FactID
}

// MutationRecord is one entry in a fact's assert -> modify... lineage, as
// reconstructed from a retained explain log.
type MutationRecord struct {
	Kind          MutationKind
	Firing        *Firing
	ChangedFields []FieldChange
	Sequence      uint64
}

const (
	// DefaultExplainMaxDepth bounds derivation recursion depth. A support
	// chain deeper than this yields a Truncated node instead of recursing.
	DefaultExplainMaxDepth = 64
	// DefaultExplainMaxNodes bounds the total number of derivation nodes a
	// single Explain call produces, guarding against pathological graphs.
	DefaultExplainMaxNodes = 10_000
)

type explainConfig struct {
	maxDepth int
	maxNodes int
}

// ExplainOption configures an Explain call. Options are shared by
// Snapshot.Explain and Session.Explain.
type ExplainOption func(*explainConfig)

func newExplainConfig(opts []ExplainOption) explainConfig {
	cfg := explainConfig{maxDepth: DefaultExplainMaxDepth, maxNodes: DefaultExplainMaxNodes}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.maxDepth <= 0 {
		cfg.maxDepth = DefaultExplainMaxDepth
	}
	if cfg.maxNodes <= 0 {
		cfg.maxNodes = DefaultExplainMaxNodes
	}
	return cfg
}

// WithExplainMaxDepth caps derivation recursion depth (default
// DefaultExplainMaxDepth). Depth beyond the cap surfaces as Truncated.
func WithExplainMaxDepth(depth int) ExplainOption {
	return func(cfg *explainConfig) { cfg.maxDepth = depth }
}

// WithExplainMaxNodes caps the total derivation node count for one call
// (default DefaultExplainMaxNodes). Reaching the cap surfaces as Truncated.
func WithExplainMaxNodes(nodes int) ExplainOption {
	return func(cfg *explainConfig) { cfg.maxNodes = nodes }
}

type explainBudget struct {
	maxNodes int
	maxDepth int
	nodes    int
}

// Explain returns the logical-support derivation of the fact identified by id.
// It is a pure read of the snapshot: no session state, no engine mutation.
//
// The support state, the producing rule firing (for logically-supported
// facts), and the recursive logical supporters are populated from the support
// graph; ProducedBy.Action, ProducedBy.Bindings, and History require the
// session-level, log-backed Session.Explain. Only snapshots produced by
// Session.Snapshot carry support edges, so a derivation built from an
// internally-constructed snapshot has an empty DependsOn.
//
// Recursion is cycle-guarded and bounded by WithExplainMaxDepth and
// WithExplainMaxNodes; a node cut short by a cycle revisit or a cap is marked
// Truncated. The second argument reports whether the fact exists in the
// snapshot.
func (s Snapshot) Explain(id FactID, opts ...ExplainOption) (Derivation, bool) {
	fact, ok := s.Fact(id)
	if !ok {
		return Derivation{}, false
	}
	cfg := newExplainConfig(opts)
	index := s.supportEdgesByFact()
	budget := &explainBudget{maxNodes: cfg.maxNodes, maxDepth: cfg.maxDepth}
	visited := make(map[FactID]struct{})
	return s.explainFact(fact, index, visited, budget, 0), true
}

func (s Snapshot) explainFact(fact FactSnapshot, index map[FactID][]LogicalSupportEdge, visited map[FactID]struct{}, budget *explainBudget, depth int) Derivation {
	d := Derivation{Fact: fact, Support: fact.Support().State}
	id := fact.ID()
	edges := index[id]
	if len(edges) > 0 {
		d.ProducedBy = s.firingFromSupportEdge(edges[0])
	}

	budget.nodes++
	if budget.nodes > budget.maxNodes {
		d.Truncated = true
		return d
	}
	if _, seen := visited[id]; seen {
		d.Truncated = true
		return d
	}
	if depth >= budget.maxDepth {
		if len(edges) > 0 {
			d.Truncated = true
		}
		return d
	}

	visited[id] = struct{}{}
	for _, edge := range edges {
		for _, supportID := range edge.SupportingFacts {
			supportFact, ok := s.Fact(supportID)
			if !ok {
				d.DependsOn = append(d.DependsOn, Derivation{Fact: FactSnapshot{id: supportID}, Truncated: true})
				continue
			}
			d.DependsOn = append(d.DependsOn, s.explainFact(supportFact, index, visited, budget, depth+1))
		}
	}
	return d
}

func (s Snapshot) supportEdgesByFact() map[FactID][]LogicalSupportEdge {
	edges := s.support.Edges
	if len(edges) == 0 {
		return nil
	}
	index := make(map[FactID][]LogicalSupportEdge, len(edges))
	for _, edge := range edges {
		index[edge.FactID] = append(index[edge.FactID], edge)
	}
	return index
}

func (s Snapshot) firingFromSupportEdge(edge LogicalSupportEdge) *Firing {
	firing := &Firing{
		RuleID:          edge.RuleID,
		RuleRevisionID:  edge.RuleRevisionID,
		ActivationID:    edge.ActivationID,
		Generation:      edge.Generation,
		SupportingFacts: cloneFactIDs(edge.SupportingFacts),
	}
	firing.RuleName = s.ruleName(edge.RuleRevisionID, edge.RuleID)
	return firing
}

func (s Snapshot) ruleName(revisionID RuleRevisionID, ruleID RuleID) string {
	if s.revision == nil {
		return ""
	}
	if rule, ok := s.revision.RuleByRevisionID(revisionID); ok {
		return rule.Name()
	}
	if rule, ok := s.revision.RuleByID(ruleID); ok {
		return rule.Name()
	}
	return ""
}
