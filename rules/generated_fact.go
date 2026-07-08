package rules

// GeneratedFactObservabilityKind classifies whether generated facts must enter
// working memory for downstream rule/query visibility.
type GeneratedFactObservabilityKind string

const (
	// GeneratedFactReactiveWorkingMemory means generated facts can affect rule
	// matching and must retain working-memory identity.
	GeneratedFactReactiveWorkingMemory GeneratedFactObservabilityKind = "reactive-working-memory"
	// GeneratedFactQueryVisible means generated facts are not rule-reactive but
	// can be returned by compiled queries.
	GeneratedFactQueryVisible GeneratedFactObservabilityKind = "query-visible"
	// GeneratedFactOutputOnly means no compiled rule or query condition observes
	// the generated template.
	GeneratedFactOutputOnly GeneratedFactObservabilityKind = "output-only"
)

// GeneratedFactObservability describes the compiler proof for a generated
// template's downstream visibility.
type GeneratedFactObservability struct {
	TemplateKey       TemplateKey
	TemplateName      string
	Kind              GeneratedFactObservabilityKind
	RuleMatchVisible  bool
	QueryVisible      bool
	StoresName        bool
	DiagnosticReasons []string
}

// CloneGeneratedFactObservability returns a defensive copy of o.
func CloneGeneratedFactObservability(o GeneratedFactObservability) GeneratedFactObservability {
	o.DiagnosticReasons = append([]string(nil), o.DiagnosticReasons...)
	return o
}

// CloneGeneratedFactObservabilityDiagnostics returns a defensive copy of
// diagnostics.
func CloneGeneratedFactObservabilityDiagnostics(diagnostics []GeneratedFactObservability) []GeneratedFactObservability {
	out := make([]GeneratedFactObservability, len(diagnostics))
	for i, diagnostic := range diagnostics {
		out[i] = CloneGeneratedFactObservability(diagnostic)
	}
	return out
}
