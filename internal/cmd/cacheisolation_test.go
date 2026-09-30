package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/greatliontech/gomutant/internal/fixtureguard"
)

// TestMain isolates the machine-local findings overlay: tests must
// never write the developer's real user cache, and cross-run overlay
// leakage would shadow fixture findings with stale entries. It also
// guards the tracked fixture trees the suites read in place (every
// module under internal/engine/testdata): a residue before the run,
// or a test in this run writing into a tree rather than into a copy —
// a member left, or created and removed — fails the suite naming the
// member (it moves the observation brackets of every package's tests
// reading the tree beside the writer).
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "gomutant-test-cache-*")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_CACHE_HOME", tmp)
	code := fixtureguard.Guard(os.Stderr, filepath.Dir(fixtureDir), m.Run)
	os.RemoveAll(tmp)
	os.Exit(code)
}
