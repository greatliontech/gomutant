// Package testsuite is the one TestMain for every suite that reads the
// tracked fixture trees: it isolates the machine-local findings overlay
// (tests must never write the developer's real user cache, and
// cross-run overlay leakage would shadow fixture findings with stale
// entries), takes the build selection's default for -short
// (internal/integrationtest: under the default selection the binary is
// the unit suite gomutant's own campaigns derive as their oracle), and
// guards the tracked fixture trees (internal/fixtureguard: a residue
// before the run, or a test in this run writing into a tree rather than
// into a copy, fails the suite naming the member).
package testsuite

import (
	"os"
	"testing"

	"github.com/greatliontech/gomutant/internal/fixtureguard"
	"github.com/greatliontech/gomutant/internal/integrationtest"
)

// Main runs the suite and exits with its code: the overlay isolated
// under a temporary cache home, the selection's -short default taken,
// and every tracked fixture tree under fixtureRoot guarded.
func Main(m *testing.M, fixtureRoot string) {
	tmp, err := os.MkdirTemp("", "gomutant-test-cache-*")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_CACHE_HOME", tmp)
	integrationtest.DefaultToShort()
	code := fixtureguard.Guard(os.Stderr, fixtureRoot, m.Run)
	os.RemoveAll(tmp)
	os.Exit(code)
}
