package lib

import (
	"testing"

	"pgregory.net/rapid"
)

// TestPropRapidCheck drives the rapid check runner (rapid-split fixture).
func TestPropRapidCheck(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		if Add(1, 2) != 3 {
			rt.Fatal("broken")
		}
	})
}

// TestPropRapidMakeCheck drives the subtest-shaped runner: also a
// rapid-split fixture through an alias.
func TestPropRapidMakeCheck(t *testing.T) {
	t.Run("prop", rapid.MakeCheck(func(rt *rapid.T) {
		if Add(2, 2) != 4 {
			rt.Fatal("broken")
		}
	}))
}

// TestPropRapidGeneratorOnly constructs a generator but never drives a
// check runner: generator construction alone, no driver.
func TestPropRapidGeneratorOnly(t *testing.T) {
	if got := rapid.Int(); got != Add(got, 0) {
		t.Fatal("broken")
	}
}
