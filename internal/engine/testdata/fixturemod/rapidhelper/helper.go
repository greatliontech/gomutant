// Package rapidhelper drives rapid on behalf of tests that never
// import it directly: the helper-indirection shape whose runtime the
// linked-closure detection must still see.
package rapidhelper

import (
	"flag"
	"os"
	"testing"

	"pgregory.net/rapid"
)

// Drive runs the caller's property under rapid; the caller never
// imports the runtime. Under GOMUTANT_REQUIRE_RAPID_FLAG it asserts
// the pinning flags were delivered — the end-to-end proof that
// linked-closure detection put the flags in front of a binary whose
// tests never import rapid directly.
func Drive(t *testing.T, prop func() bool) {
	if os.Getenv("GOMUTANT_REQUIRE_RAPID_FLAG") != "" {
		if flag.Lookup("rapid.nofailfile").Value.String() != "true" {
			t.Fatal("rapid failfile guard is not enabled")
		}
		if flag.Lookup("rapid.seed").Value.String() != "1" {
			t.Fatal("rapid draws are not pinned")
		}
	}
	rapid.Check(t, func(rt *rapid.T) {
		if !prop() {
			rt.Fatal("property failed")
		}
	})
}
