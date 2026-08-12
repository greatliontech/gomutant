package prop

import (
	"testing"

	"pgregory.net/rapid"
)

// TestPropRapidCheck drives the rapid check runner against Add: the
// serve-pin oracle.
func TestPropRapidCheck(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		if Add(1, 2) != 3 {
			rt.Fatal("broken")
		}
	})
}
