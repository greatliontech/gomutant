package gopterprop

import (
	"testing"

	"github.com/leanovate/gopter"

	"example.com/fixture/lib"
)

// TestGopterProp drives a gopter-shaped suite (property-runtime
// detection fixture): gomutant cannot pin gopter's draws, so this
// oracle earns a prerequisite statement, never a pin.
func TestGopterProp(t *testing.T) {
	_ = gopter.NewProperties()
	if lib.Add(1, 2) != 3 {
		t.Fatal("broken")
	}
}
