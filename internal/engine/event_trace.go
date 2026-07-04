package engine

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// TraceOption configures the output emitted by NewTraceListener.
type TraceOption func(*traceConfig)

type traceConfig struct {
	timestamps bool
}

// TraceWithTimestamps includes the event timestamp in each trace line.
func TraceWithTimestamps() TraceOption {
	return func(cfg *traceConfig) {
		cfg.timestamps = true
	}
}

type traceListener struct {
	writer     io.Writer
	timestamps bool
}

// NewTraceListener returns an EventListener that writes one stable, readable
// line per delivered event. Delivery is synchronous; callers own writer
// buffering and synchronization when the writer is shared.
func NewTraceListener(w io.Writer, opts ...TraceOption) EventListener {
	var cfg traceConfig
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return traceListener{
		writer:     w,
		timestamps: cfg.timestamps,
	}
}

func (l traceListener) HandleEvent(_ context.Context, event Event) error {
	if l.writer == nil {
		return nil
	}
	line := traceEventLine(event, l.timestamps)
	_, err := io.WriteString(l.writer, line+"\n")
	return err
}

func traceEventLine(event Event, timestamps bool) string {
	var b strings.Builder
	b.WriteString("seq=")
	b.WriteString(strconv.FormatUint(event.Sequence, 10))
	if timestamps {
		b.WriteString(" time=")
		b.WriteString(traceTime(event.Timestamp))
	}
	b.WriteString(" type=")
	b.WriteString(string(event.Type))
	if event.Severity != "" {
		b.WriteString(" severity=")
		b.WriteString(string(event.Severity))
	}
	if !event.RunID.IsZero() {
		b.WriteString(" run=")
		b.WriteString(event.RunID.String())
	}

	switch event.Type {
	case EventFactAsserted:
		traceWriteFactEvent(&b, event, "fact", eventDeltaAfter(event.Delta))
	case EventFactModified:
		traceWriteFactEvent(&b, event, "fact", eventDeltaAfter(event.Delta))
		if event.Delta != nil && len(event.Delta.ChangedFields) > 0 {
			b.WriteString(" changes=")
			traceWriteFieldChanges(&b, event.Delta.ChangedFields)
		}
	case EventFactRetracted:
		traceWriteFactEvent(&b, event, "fact", eventDeltaBefore(event.Delta))
	case EventReset:
		if event.Delta != nil {
			b.WriteString(" generation=")
			b.WriteString(strconv.FormatUint(uint64(event.Delta.Generation), 10))
			b.WriteString(" old_generation=")
			b.WriteString(strconv.FormatUint(uint64(event.Delta.OldGeneration), 10))
		} else if event.Generation != 0 {
			b.WriteString(" generation=")
			b.WriteString(strconv.FormatUint(uint64(event.Generation), 10))
		}
	case EventRuleActivated, EventRuleDeactivated, EventRuleFired:
		traceWriteRuleEvent(&b, event)
	case EventActionFailed:
		traceWriteRuleEvent(&b, event)
		if event.ActionName != "" {
			b.WriteString(" action=")
			b.WriteString(strconv.Quote(event.ActionName))
		}
		if event.ActionIndex >= 0 {
			b.WriteString(" action_index=")
			b.WriteString(strconv.Itoa(event.ActionIndex))
		}
		if event.Cause != nil {
			b.WriteString(" error=")
			b.WriteString(strconv.Quote(event.Cause.Error()))
		}
	case EventLogicalSupportAdded, EventLogicalSupportRemoved:
		traceWriteSupportEvent(&b, event)
	default:
		traceWriteFactIDs(&b, event.FactIDs)
	}

	return b.String()
}

func traceTime(t time.Time) string {
	if t.IsZero() {
		return "zero"
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func traceWriteFactEvent(b *strings.Builder, event Event, label string, snapshot *FactSnapshot) {
	if snapshot != nil {
		b.WriteByte(' ')
		b.WriteString(label)
		b.WriteByte('=')
		b.WriteString(snapshot.ID().String())
		b.WriteString(" template=")
		b.WriteString(snapshot.Name())
		b.WriteString(" fields=")
		traceWriteFields(b, snapshot.Fields())
	} else {
		traceWriteFactIDs(b, event.FactIDs)
	}
	traceWriteOrigin(b, event)
}

func eventDeltaBefore(delta *MutationDelta) *FactSnapshot {
	if delta == nil {
		return nil
	}
	return delta.Before
}

func eventDeltaAfter(delta *MutationDelta) *FactSnapshot {
	if delta == nil {
		return nil
	}
	return delta.After
}

func traceWriteRuleEvent(b *strings.Builder, event Event) {
	traceWriteOrigin(b, event)
	traceWriteFactIDs(b, event.FactIDs)
}

func traceWriteOrigin(b *strings.Builder, event Event) {
	if !event.RuleID.IsZero() {
		b.WriteString(" rule=")
		b.WriteString(event.RuleID.String())
	}
	if loc := sourceSpanLocation(event.Source); loc != "" {
		b.WriteString(" source=")
		b.WriteString(loc)
	}
	if !event.RuleRevisionID.IsZero() {
		b.WriteString(" revision=")
		b.WriteString(event.RuleRevisionID.String())
	}
	if !event.ActivationID.IsZero() {
		b.WriteString(" activation=")
		b.WriteString(event.ActivationID.String())
	}
}

func traceWriteFactIDs(b *strings.Builder, ids []FactID) {
	if len(ids) == 0 {
		return
	}
	b.WriteString(" facts=[")
	for i, id := range ids {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(id.String())
	}
	b.WriteByte(']')
}

func traceWriteSupportEvent(b *strings.Builder, event Event) {
	if event.SupportEdge == nil {
		traceWriteOrigin(b, event)
		traceWriteFactIDs(b, event.FactIDs)
		return
	}
	edge := event.SupportEdge
	b.WriteString(" support=")
	b.WriteString(edge.SupportID.String())
	b.WriteString(" fact=")
	b.WriteString(edge.FactID.String())
	if !edge.RuleID.IsZero() {
		b.WriteString(" rule=")
		b.WriteString(edge.RuleID.String())
	}
	if !edge.RuleRevisionID.IsZero() {
		b.WriteString(" revision=")
		b.WriteString(edge.RuleRevisionID.String())
	}
	if !edge.ActivationID.IsZero() {
		b.WriteString(" activation=")
		b.WriteString(edge.ActivationID.String())
	}
	if len(edge.SupportingFacts) > 0 {
		b.WriteString(" supporting=")
		traceWriteFactIDList(b, edge.SupportingFacts)
	}
}

func traceWriteFactIDList(b *strings.Builder, ids []FactID) {
	b.WriteByte('[')
	for i, id := range ids {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(id.String())
	}
	b.WriteByte(']')
}

func traceWriteFields(b *strings.Builder, fields Fields) {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	b.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(key)
		b.WriteByte('=')
		traceWriteValue(b, fields[key])
	}
	b.WriteByte('}')
}

func traceWriteFieldChanges(b *strings.Builder, changes []FieldChange) {
	ordered := make([]FieldChange, len(changes))
	copy(ordered, changes)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].Field < ordered[j].Field
	})
	b.WriteByte('{')
	for i, change := range ordered {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(change.Field)
		b.WriteByte('=')
		traceWriteValue(b, change.Old)
		b.WriteString("->")
		traceWriteValue(b, change.New)
	}
	b.WriteByte('}')
}

func traceWriteValue(b *strings.Builder, value Value) {
	switch value.Kind() {
	case ValueNull:
		b.WriteString("null")
	case ValueBool:
		raw, _ := value.AsBool()
		b.WriteString(strconv.FormatBool(raw))
	case ValueInt:
		raw, _ := value.AsInt64()
		b.WriteString(strconv.FormatInt(raw, 10))
	case ValueFloat:
		raw, _ := value.AsFloat64()
		b.WriteString(strconv.FormatFloat(raw, 'g', -1, 64))
	case ValueString:
		raw, _ := value.AsString()
		b.WriteString(strconv.Quote(raw))
	case ValueList:
		raw := value.data.([]Value)
		b.WriteByte('[')
		for i, item := range raw {
			if i > 0 {
				b.WriteString(", ")
			}
			traceWriteValue(b, item)
		}
		b.WriteByte(']')
	case ValueMap:
		raw := value.data.(map[string]Value)
		keys := make([]string, 0, len(raw))
		for key := range raw {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(key)
			b.WriteByte('=')
			traceWriteValue(b, raw[key])
		}
		b.WriteByte('}')
	default:
		fmt.Fprint(b, value)
	}
}
