package gess

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

type conditionTargetKind uint8

const (
	conditionTargetUnknown conditionTargetKind = iota
	conditionTargetName
	conditionTargetTemplateKey
)

type conditionIndexKind uint8

const (
	conditionIndexUnknown conditionIndexKind = iota
	conditionIndexName
	conditionIndexTemplateKey
)

type conditionTarget struct {
	kind        conditionTargetKind
	name        string
	templateKey TemplateKey
}

type compiledConditionPlan struct {
	id          ConditionID
	binding     string
	bindingSlot int
	path        []int
	target      conditionTarget
	indexable   bool
	indexKind   conditionIndexKind
}

type conditionMatch struct {
	conditionID ConditionID
	bindingSlot int
	fact        FactSnapshot
}

func isValidBindingName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case i == 0 && (r == '_' || ('A' <= r && r <= 'Z') || ('a' <= r && r <= 'z')):
		case i > 0 && (r == '_' || ('A' <= r && r <= 'Z') || ('a' <= r && r <= 'z') || ('0' <= r && r <= '9')):
		default:
			return false
		}
	}
	return true
}

func conditionIDFor(ruleID RuleID, order int, binding string, name string, templateKey TemplateKey) ConditionID {
	sum := sha256.New()
	sum.Write([]byte("gess/condition/v1\n"))
	sum.Write([]byte("rule:"))
	sum.Write([]byte(ruleID.String()))
	sum.Write([]byte("\norder:"))
	sum.Write(fmt.Appendf(nil, "%d", order))
	sum.Write([]byte("\nbinding:"))
	sum.Write([]byte(binding))
	sum.Write([]byte("\nname:"))
	sum.Write([]byte(name))
	sum.Write([]byte("\ntemplate-key:"))
	sum.Write([]byte(templateKey.String()))
	return ConditionID("sha256:" + hex.EncodeToString(sum.Sum(nil)))
}

func (p compiledConditionPlan) scan(snapshot Snapshot) []conditionMatch {
	if p.target.kind == conditionTargetUnknown {
		return nil
	}

	matches := make([]conditionMatch, 0, len(snapshot.facts))
	for _, fact := range snapshot.facts {
		if !p.matchesFact(fact) {
			continue
		}
		matches = append(matches, conditionMatch{
			conditionID: p.id,
			bindingSlot: p.bindingSlot,
			fact:        fact.clone(),
		})
	}
	return matches
}

func (p compiledConditionPlan) matchesFact(fact FactSnapshot) bool {
	switch p.target.kind {
	case conditionTargetName:
		return fact.Name() == p.target.name
	case conditionTargetTemplateKey:
		return fact.TemplateKey() == p.target.templateKey
	default:
		return false
	}
}

func (r compiledRule) scanCondition(snapshot Snapshot, conditionIndex int) []conditionMatch {
	if conditionIndex < 0 || conditionIndex >= len(r.conditionPlans) {
		return nil
	}
	return r.conditionPlans[conditionIndex].scan(snapshot)
}
