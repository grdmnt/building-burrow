package wire_test

import (
	"testing"
	"time"

	"burrow/internal/testkit"
)

// Your own fuzz target in wire/ is graded here: it must pass its seed corpus
// and survive two seconds of real fuzzing against your code.
func TestL2_1_YourFuzzTarget(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped in -short mode")
	}
	names := testkit.YourFuzzTargets(t, "wire")
	if len(names) == 0 {
		t.Fatal("no fuzz target of your own in wire/. Write one, such as FuzzRoundTrip in wire/record_test.go, " +
			"seed it with f.Add and check a property that must hold for every input")
	}
	for _, name := range names {
		testkit.RunFuzz(t, "wire", name, 2*time.Second)
	}
}
