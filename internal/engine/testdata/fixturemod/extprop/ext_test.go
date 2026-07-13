package extprop_test

import (
	"flag"
	"os"
	"testing"

	"example.com/fixture/extprop"
	"pgregory.net/rapid"
)

// TestExtProp drives rapid from the external test variant only.
func TestExtProp(t *testing.T) {
	if os.Getenv("GOMUTANT_REQUIRE_RAPID_FLAG") != "" && flag.Lookup("rapid.nofailfile").Value.String() != "true" {
		t.Fatal("rapid failfile guard is not enabled")
	}
	rapid.Check(t, func(rt *rapid.T) {
		if !extprop.Ok() {
			rt.Fatal("broken")
		}
	})
}
