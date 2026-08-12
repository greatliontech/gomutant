package unstable_test

import (
	"fmt"
	"os"
	"testing"

	"example.com/fixture/unstable"
)

// TestMain simulates harness-level instability: with the count marker
// set, the second run exits before any test registers, so the binary's
// baseline test count drifts between discovery and measurement.
func TestMain(m *testing.M) {
	marker := os.Getenv("GOMUTANT_UNSTABLE_COUNT")
	if marker != "" {
		exists, err := countMarkerExistsAndSet(marker)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if exists {
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}

// TestAdd reads a runtime input so the baseline probe's validity
// repeat actually runs - a pure baseline short-circuits the second
// run the count-drift simulation needs.
func TestAdd(t *testing.T) {
	got := unstable.Add(1, 2)
	_, _ = os.ReadFile(fmt.Sprintf("input-%d.txt", got))
	if got != 3 {
		t.Fatal("broken")
	}
}
