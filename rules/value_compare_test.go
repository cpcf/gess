package rules

import (
	"math"
	"testing"
)

func TestNumericTrichotomyBeyondExactFloatRange(t *testing.T) {
	mustValue := func(v any) Value {
		t.Helper()
		value, err := NewValue(v)
		if err != nil {
			t.Fatalf("NewValue(%v): %v", v, err)
		}
		return value
	}

	huge := int64(1) << 60
	hugeInt := mustValue(huge)
	hugeFloat := mustValue(float64(huge))

	if !hugeInt.Equal(hugeFloat) || !hugeFloat.Equal(hugeInt) {
		t.Fatalf("2^60 int and float must be equal in both directions")
	}
	if cmp, ok := CompareValues(hugeInt, hugeFloat); !ok || cmp != 0 {
		t.Fatalf("CompareValues(2^60 int, 2^60 float) = (%d, %t), want (0, true)", cmp, ok)
	}

	// 2^60+1 is not representable as float64; ordering must still hold and
	// equality must not report true.
	hugePlus := mustValue(huge + 1)
	if hugePlus.Equal(hugeFloat) {
		t.Fatal("2^60+1 int must not equal 2^60 float")
	}
	if cmp, ok := CompareValues(hugePlus, hugeFloat); !ok || cmp != 1 {
		t.Fatalf("CompareValues(2^60+1 int, 2^60 float) = (%d, %t), want (1, true)", cmp, ok)
	}
	if cmp, ok := CompareValues(hugeFloat, hugePlus); !ok || cmp != -1 {
		t.Fatalf("CompareValues(2^60 float, 2^60+1 int) = (%d, %t), want (-1, true)", cmp, ok)
	}

	// Trichotomy: exactly one of <, ==, > for every pair in the suspect range.
	for _, integer := range []int64{huge - 2, huge, huge + 2, -huge, math.MaxInt64, math.MinInt64} {
		intValue := mustValue(integer)
		for _, floating := range []float64{float64(huge), -float64(huge), float64(math.MaxInt64)} {
			floatValue := mustValue(floating)
			cmp, ok := CompareValues(intValue, floatValue)
			if !ok {
				t.Fatalf("CompareValues(%d, %g) not comparable", integer, floating)
			}
			if equal := intValue.Equal(floatValue); equal != (cmp == 0) {
				t.Fatalf("Equal(%d, %g) = %t but CompareValues = %d", integer, floating, equal, cmp)
			}
		}
	}

	// Small ints keep exact semantics.
	if !mustValue(int64(2)).Equal(mustValue(2.0)) {
		t.Fatal("2 must equal 2.0")
	}
	if mustValue(int64(2)).Equal(mustValue(2.5)) {
		t.Fatal("2 must not equal 2.5")
	}
}
