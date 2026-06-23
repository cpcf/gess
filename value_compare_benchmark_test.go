package gess

import "testing"

var (
	benchmarkCompareInt    int
	benchmarkCompareBool   bool
	benchmarkCompareResult bool
)

func BenchmarkCompareValues(b *testing.B) {
	cases := []struct {
		name  string
		left  Value
		right Value
	}{
		{name: "IntInt", left: intValue(123), right: intValue(456)},
		{name: "FloatFloat", left: floatValue(123.5), right: floatValue(456.5)},
		{name: "SafeIntFloat", left: intValue(123), right: floatValue(456.5)},
		{name: "StringEqual", left: stringValue("Ada"), right: stringValue("Ada")},
		{name: "StringOrder", left: stringValue("Ada"), right: stringValue("Zoe")},
	}

	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			left := tc.left
			right := tc.right
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchmarkCompareInt, benchmarkCompareBool = compareValues(left, right)
			}
			benchmarkCompareResult = benchmarkCompareBool
		})
	}
}

func BenchmarkConstraintCompareMatches(b *testing.B) {
	fact := factSnapshotWithFields(map[string]Value{
		"age":   intValue(18),
		"score": floatValue(21.5),
		"name":  stringValue("Ada"),
	})
	safeFloatFact := factSnapshotWithFields(map[string]Value{
		"age": intValue(18),
	})
	unsafeFact := factSnapshotWithFields(map[string]Value{
		"age": intValue(maxExactFloatInt + 1),
	})
	unsafeRight := newConditionFactRefFromSnapshot(factSnapshotWithFields(map[string]Value{
		"age": floatValue(float64(maxExactFloatInt + 1)),
	}))

	fieldCases := []struct {
		name       string
		constraint compiledFieldConstraint
		snapshot   FactSnapshot
	}{
		{
			name: "IntInt",
			constraint: compiledFieldConstraint{
				access:   testCompiledPathAccess("age"),
				operator: FieldConstraintOpEqual,
				value:    intValue(18),
			},
			snapshot: fact,
		},
		{
			name: "FloatFloat",
			constraint: compiledFieldConstraint{
				access:   testCompiledPathAccess("score"),
				operator: FieldConstraintOpGreaterThan,
				value:    floatValue(20.0),
			},
			snapshot: fact,
		},
		{
			name: "SafeIntFloat",
			constraint: compiledFieldConstraint{
				access:   testCompiledPathAccess("age"),
				operator: FieldConstraintOpLessOrEqual,
				value:    floatValue(18.0),
			},
			snapshot: safeFloatFact,
		},
		{
			name: "StringEqual",
			constraint: compiledFieldConstraint{
				access:   testCompiledPathAccess("name"),
				operator: FieldConstraintOpEqual,
				value:    stringValue("Ada"),
			},
			snapshot: fact,
		},
		{
			name: "StringOrder",
			constraint: compiledFieldConstraint{
				access:   testCompiledPathAccess("name"),
				operator: FieldConstraintOpLessThan,
				value:    stringValue("Zoe"),
			},
			snapshot: fact,
		},
		{
			name: "UnsafeIntFloat",
			constraint: compiledFieldConstraint{
				access:   testCompiledPathAccess("age"),
				operator: FieldConstraintOpGreaterThan,
				value:    floatValue(float64(maxExactFloatInt + 1)),
			},
			snapshot: unsafeFact,
		},
	}

	for _, tc := range fieldCases {
		b.Run("Field/"+tc.name, func(b *testing.B) {
			constraint := tc.constraint
			snapshot := tc.snapshot
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchmarkCompareResult = constraint.matches(newConditionFactRefFromSnapshot(snapshot))
			}
		})
	}

	joinCases := []struct {
		name       string
		constraint compiledJoinConstraint
		snapshot   FactSnapshot
		bindings   []conditionMatch
	}{
		{
			name: "IntInt",
			constraint: compiledJoinConstraint{
				operator:       FieldConstraintOpEqual,
				refBindingSlot: 0,
				access:         testCompiledPathAccess("age"),
				refAccess:      testCompiledPathAccess("age"),
			},
			snapshot: fact,
			bindings: []conditionMatch{{fact: newConditionFactRefFromSnapshot(FactSnapshot{fields: map[string]Value{
				"age": intValue(18),
			}})}},
		},
		{
			name: "SafeIntFloat",
			constraint: compiledJoinConstraint{
				operator:       FieldConstraintOpGreaterThan,
				refBindingSlot: 0,
				access:         testCompiledPathAccess("age"),
				refAccess:      testCompiledPathAccess("age"),
			},
			snapshot: fact,
			bindings: []conditionMatch{{fact: newConditionFactRefFromSnapshot(FactSnapshot{fields: map[string]Value{
				"age": floatValue(17.5),
			}})}},
		},
		{
			name: "UnsafeIntFloat",
			constraint: compiledJoinConstraint{
				operator:       FieldConstraintOpGreaterThan,
				refBindingSlot: 0,
				access:         testCompiledPathAccess("age"),
				refAccess:      testCompiledPathAccess("age"),
			},
			snapshot: unsafeFact,
			bindings: []conditionMatch{{fact: unsafeRight}},
		},
	}

	for _, tc := range joinCases {
		b.Run("Join/"+tc.name, func(b *testing.B) {
			constraint := tc.constraint
			snapshot := tc.snapshot
			bindings := tc.bindings
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				benchmarkCompareResult, _ = constraint.matches(newConditionFactRefFromSnapshot(snapshot), bindings)
			}
		})
	}
}
