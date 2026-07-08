package engine

import gessrules "github.com/cpcf/gess/rules"

type RulesetID = gessrules.RulesetID
type ModuleName = gessrules.ModuleName

const MainModule = gessrules.MainModule

type SessionID = gessrules.SessionID
type RunID = gessrules.RunID
type RuleID = gessrules.RuleID
type RuleRevisionID = gessrules.RuleRevisionID
type ActivationID = gessrules.ActivationID
type SupportID = gessrules.SupportID
type ConditionID = gessrules.ConditionID
type TemplateKey = gessrules.TemplateKey
type FactVersion = gessrules.FactVersion
type Recency = gessrules.Recency
type Generation = gessrules.Generation
type FactID = gessrules.FactID

func newFactID(generation Generation, sequence uint64) FactID {
	return gessrules.NewFactID(generation, sequence)
}
