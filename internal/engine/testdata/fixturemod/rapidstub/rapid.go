// Package rapid is a hermetic stub of pgregory.net/rapid: the rapid-split
// partition resolves the import path from the type checker, so the fixture
// needs the shapes, not the behavior.
package rapid

import (
	"flag"
	"testing"
)

// The real library registers its flags in package init; test binaries
// linking this stub must accept them too, or every mutant run against a
// rapid-importing fixture package would die on an unknown flag and read
// as a false kill.
func init() {
	flag.Bool("rapid.nofailfile", false, "rapid: do not write fail files on test failures")
	flag.Uint64("rapid.seed", 0, "rapid: PRNG seed (0 means random)")
}

// T mirrors rapid.T as the property callback's handle. The real
// rapid.T holds its harness handle in an unexported field and exposes
// its own methods - the internal dispatch happens inside this package,
// never in the callback's body - so the stub mirrors that shape rather
// than embedding, which would materialize promoted-method dispatch in
// user code the real library never produces.
type T struct{ tb testing.TB }

// Fatal mirrors the failing-report method the fixtures drive.
func (t *T) Fatal(args ...any) { t.tb.Fatal(args...) }

// Check mirrors the check driver: it runs the property once.
func Check(t *testing.T, prop func(*T)) { prop(&T{tb: t}) }

// MakeCheck mirrors the subtest-shaped driver.
func MakeCheck(prop func(*T)) func(*testing.T) {
	return func(t *testing.T) { prop(&T{tb: t}) }
}

// Int mirrors a generator constructor: construction alone must not
// count as rapid importers for the split.
func Int() int { return 0 }
