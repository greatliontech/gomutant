package mixedprop

import (
	"testing"

	"github.com/leanovate/gopter"
	"pgregory.net/rapid"

	"example.com/fixture/lib"
)

// TestMixedProp imports both recognized property runtimes
// (mixed-runtime detection fixture): each runtime earns its own
// prerequisite statement.
func TestMixedProp(t *testing.T) {
	_ = gopter.NewProperties()
	rapid.Check(t, func(rt *rapid.T) {
		if lib.Add(1, 2) != 3 {
			rt.Fatal("broken")
		}
	})
}
