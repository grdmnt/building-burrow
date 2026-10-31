package basics_test

import (
	"math"
	"testing"

	"burrow/primer/basics"
)

// firstNext is the very first call to Next. Package-level variables are
// set before any test runs, so nothing else has called Next yet.
var firstNext = basics.Next()

func TestLP_1_Greet(t *testing.T) {
	cases := []struct{ name, want string }{
		{"burrow", "hello, burrow"},
		{"gopher", "hello, gopher"},
		{"", "hello, "},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			if got := basics.Greet(tc.name); got != tc.want {
				t.Errorf("Greet(%q) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestLP_1_SplitBill(t *testing.T) {
	cases := []struct {
		cents, people  int
		each, leftover int
	}{
		{1000, 3, 333, 1},
		{1000, 4, 250, 0},
		{999, 1, 999, 0},
		{5, 10, 0, 5},
		{0, 3, 0, 0},
		{12345, 7, 1763, 4},
	}
	for _, tc := range cases {
		each, leftover := basics.SplitBill(tc.cents, tc.people)
		if each != tc.each || leftover != tc.leftover {
			t.Errorf("SplitBill(%d, %d) = (%d, %d), want (%d, %d): "+
				"each is cents / people and leftover is cents %% people",
				tc.cents, tc.people, each, leftover, tc.each, tc.leftover)
			continue
		}
		if each*tc.people+leftover != tc.cents {
			t.Errorf("SplitBill(%d, %d): %d people paying %d plus %d "+
				"left over should add back up to %d",
				tc.cents, tc.people, tc.people, each, leftover, tc.cents)
		}
	}
}

func TestLP_1_Average(t *testing.T) {
	cases := []struct {
		a, b, c int
		want    float64
	}{
		{2, 2, 2, 2},
		{1, 2, 2, 5.0 / 3},
		{1, 1, 0, 2.0 / 3},
		{-1, 0, 0, -1.0 / 3},
		{10, 20, 31, 61.0 / 3},
	}
	for _, tc := range cases {
		got := basics.Average(tc.a, tc.b, tc.c)
		if math.Abs(got-tc.want) < 1e-12 {
			continue
		}
		hint := ""
		if got == math.Trunc(tc.want) {
			hint = ". That's the answer rounded toward zero: the division " +
				"happened between ints. Convert to float64 before dividing"
		}
		t.Errorf("Average(%d, %d, %d) = %v, want %v%s",
			tc.a, tc.b, tc.c, got, tc.want, hint)
	}
}

// The assignments below only compile if the constants are untyped:
// a constant declared as `int` can't be stored in a float64, and one
// declared as `float64` can't be stored in an int64.
func TestLP_1_SizeConstants(t *testing.T) {
	var asInt64 int64 = basics.TiB
	var asFloat float64 = basics.TiB
	var asUint32 uint32 = basics.GiB
	var huge float64 = basics.TiB * basics.TiB // 2^80, too big for any int

	sizes := []struct {
		name string
		got  int64
		want int64
	}{
		{"KiB", basics.KiB, 1 << 10},
		{"MiB", basics.MiB, 1 << 20},
		{"GiB", basics.GiB, 1 << 30},
		{"TiB", basics.TiB, 1 << 40},
	}
	for _, s := range sizes {
		if s.got != s.want {
			t.Errorf("%s should be %d bytes, got %d", s.name, s.want, s.got)
		}
	}
	if asInt64 != 1<<40 || asFloat != 1<<40 || asUint32 != 1<<30 {
		t.Errorf("TiB and GiB should keep their value in every type: "+
			"got %d, %v, %d", asInt64, asFloat, asUint32)
	}
	if huge != math.Pow(2, 80) {
		t.Errorf("TiB * TiB should be 2^80 as a float64, got %v", huge)
	}
}

func TestLP_1_Next(t *testing.T) {
	if firstNext != 1 {
		t.Errorf("the first call to Next() should return 1, got %d. A "+
			"package-level `var calls int` starts at 0, its zero value, "+
			"so there's nothing to initialise", firstNext)
	}
	a := basics.Next()
	b := basics.Next()
	if b != a+1 {
		t.Errorf("each call to Next() should return one more than the "+
			"last: got %d then %d", a, b)
	}
}
