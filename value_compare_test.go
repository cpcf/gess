package gess

import "testing"

func TestCompareValuesSemantics(t *testing.T) {
	tests := []struct {
		name           string
		left           Value
		right          Value
		want           int
		wantComparable bool
	}{
		{name: "int less", left: intValue(1), right: intValue(2), want: -1, wantComparable: true},
		{name: "int equal", left: intValue(2), right: intValue(2), want: 0, wantComparable: true},
		{name: "int greater", left: intValue(3), right: intValue(2), want: 1, wantComparable: true},
		{name: "float less", left: floatValue(1.5), right: floatValue(2.5), want: -1, wantComparable: true},
		{name: "float equal", left: floatValue(2.5), right: floatValue(2.5), want: 0, wantComparable: true},
		{name: "safe int float equal", left: intValue(18), right: floatValue(18.0), want: 0, wantComparable: true},
		{name: "safe int float less", left: intValue(18), right: floatValue(18.5), want: -1, wantComparable: true},
		{name: "safe int float greater", left: intValue(19), right: floatValue(18.5), want: 1, wantComparable: true},
		{name: "unsafe int float greater", left: intValue(maxExactFloatInt + 1), right: floatValue(float64(maxExactFloatInt + 1)), want: 1, wantComparable: true},
		{name: "unsafe int float less", left: intValue(-(maxExactFloatInt + 1)), right: floatValue(float64(-(maxExactFloatInt + 1))), want: -1, wantComparable: true},
		{name: "string less", left: stringValue("Ada"), right: stringValue("Zoe"), want: -1, wantComparable: true},
		{name: "string equal", left: stringValue("Ada"), right: stringValue("Ada"), want: 0, wantComparable: true},
		{name: "incompatible", left: stringValue("Ada"), right: intValue(1), want: 0, wantComparable: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := compareValues(tc.left, tc.right)
			if ok != tc.wantComparable {
				t.Fatalf("comparable = %v, want %v", ok, tc.wantComparable)
			}
			if got != tc.want {
				t.Fatalf("comparison = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestConstraintComparisonMatches(t *testing.T) {
	fact := factSnapshotWithFields(map[string]Value{
		"age":    intValue(18),
		"score":  floatValue(21.5),
		"name":   stringValue("Ada"),
		"status": stringValue("active"),
	})
	other := factSnapshotWithFields(map[string]Value{
		"age":   floatValue(18.0),
		"score": floatValue(20.5),
		"name":  stringValue("Zoe"),
	})

	fieldTests := []struct {
		name       string
		constraint compiledFieldConstraint
		snapshot   FactSnapshot
		want       bool
	}{
		{
			name: "int equal",
			constraint: compiledFieldConstraint{
				field:    "age",
				operator: FieldConstraintOpEqual,
				value:    intValue(18),
			},
			snapshot: fact,
			want:     true,
		},
		{
			name: "float greater",
			constraint: compiledFieldConstraint{
				field:    "score",
				operator: FieldConstraintOpGreaterThan,
				value:    floatValue(20.25),
			},
			snapshot: fact,
			want:     true,
		},
		{
			name: "safe int float less or equal",
			constraint: compiledFieldConstraint{
				field:    "age",
				operator: FieldConstraintOpLessOrEqual,
				value:    floatValue(18.0),
			},
			snapshot: fact,
			want:     true,
		},
		{
			name: "unsafe int float greater",
			constraint: compiledFieldConstraint{
				field:    "age",
				operator: FieldConstraintOpGreaterThan,
				value:    floatValue(float64(maxExactFloatInt + 1)),
			},
			snapshot: factSnapshotWithFields(map[string]Value{
				"age": intValue(maxExactFloatInt + 1),
			}),
			want: true,
		},
		{
			name: "string less",
			constraint: compiledFieldConstraint{
				field:    "name",
				operator: FieldConstraintOpLessThan,
				value:    stringValue("Zoe"),
			},
			snapshot: fact,
			want:     true,
		},
		{
			name: "incompatible type",
			constraint: compiledFieldConstraint{
				field:    "age",
				operator: FieldConstraintOpGreaterThan,
				value:    stringValue("17"),
			},
			snapshot: fact,
			want:     false,
		},
		{
			name: "missing field",
			constraint: compiledFieldConstraint{
				field:    "missing",
				operator: FieldConstraintOpGreaterThan,
				value:    intValue(1),
			},
			snapshot: fact,
			want:     false,
		},
	}

	for _, tc := range fieldTests {
		t.Run("field/"+tc.name, func(t *testing.T) {
			if got := tc.constraint.matches(tc.snapshot); got != tc.want {
				t.Fatalf("match = %v, want %v", got, tc.want)
			}
		})
	}

	joinTests := []struct {
		name       string
		constraint compiledJoinConstraint
		snapshot   FactSnapshot
		bindings   []conditionMatch
		want       bool
	}{
		{
			name: "int equal",
			constraint: compiledJoinConstraint{
				field:          "age",
				operator:       FieldConstraintOpEqual,
				refBindingSlot: 0,
				refField:       "age",
			},
			snapshot: fact,
			bindings: []conditionMatch{{fact: other}},
			want:     true,
		},
		{
			name: "safe mixed greater",
			constraint: compiledJoinConstraint{
				field:          "age",
				operator:       FieldConstraintOpGreaterThan,
				refBindingSlot: 0,
				refField:       "age",
			},
			snapshot: fact,
			bindings: []conditionMatch{{fact: factSnapshotWithFields(map[string]Value{
				"age": floatValue(17.5),
			})}},
			want: true,
		},
		{
			name: "missing ref field",
			constraint: compiledJoinConstraint{
				field:          "age",
				operator:       FieldConstraintOpEqual,
				refBindingSlot: 0,
				refField:       "missing",
			},
			snapshot: fact,
			bindings: []conditionMatch{{fact: other}},
			want:     false,
		},
		{
			name: "incompatible ref type",
			constraint: compiledJoinConstraint{
				field:          "age",
				operator:       FieldConstraintOpEqual,
				refBindingSlot: 0,
				refField:       "age",
			},
			snapshot: fact,
			bindings: []conditionMatch{{fact: factSnapshotWithFields(map[string]Value{
				"age": stringValue("18"),
			})}},
			want: false,
		},
	}

	for _, tc := range joinTests {
		t.Run("join/"+tc.name, func(t *testing.T) {
			got, err := tc.constraint.matches(tc.snapshot, tc.bindings)
			if err != nil {
				t.Fatalf("matches: %v", err)
			}
			if got != tc.want {
				t.Fatalf("match = %v, want %v", got, tc.want)
			}
		})
	}
}

func intValue(n int64) Value {
	return Value{kind: ValueInt, data: n}
}

func floatValue(n float64) Value {
	return Value{kind: ValueFloat, data: n}
}

func stringValue(s string) Value {
	return Value{kind: ValueString, data: s}
}

func factSnapshotWithFields(fields map[string]Value) FactSnapshot {
	return FactSnapshot{fields: fields}
}
