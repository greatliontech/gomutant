// Package testsuite is the one TestMain for every suite that reads the
// tracked fixture trees: it isolates the machine-local findings overlay
// (tool records and analysis memos must not enter the developer's cache, and
// cross-run overlay leakage would shadow fixture findings with stale
// entries), while retaining Go's compiler cache, takes the build selection's
// default for -short
// (internal/integrationtest: under the default selection the binary is
// the unit suite gomutant's own campaigns derive as their oracle), and
// guards the tracked fixture trees (internal/fixtureguard: a residue
// before the run, or a test in this run writing into a tree rather than
// into a copy, fails the suite naming the member).
package testsuite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gofresh/gotool"
	"github.com/greatliontech/gomutant/internal/fixtureguard"
	"github.com/greatliontech/gomutant/internal/integrationtest"
)

// Main runs the suite and exits with its code: the overlay isolated
// under a temporary cache home, the selection's -short default taken,
// and every tracked fixture tree under fixtureRoot guarded.
func Main(m *testing.M, fixtureRoot string, runner gotool.Runner) {
	// XDG isolation belongs to the tool's records and analysis memos, not
	// the Go compiler's content-addressed cache. Resolve it before changing
	// the cache home so later per-test XDG changes do not rebuild the stdlib.
	if err := preserveBuildCache(func() (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return resolveBuildCache(ctx, runner)
	}); err != nil {
		panic(err)
	}
	tmp, err := os.MkdirTemp("", "gomutant-test-cache-*")
	if err != nil {
		panic(err)
	}
	os.Setenv("XDG_CACHE_HOME", tmp)
	// The exit log's state home likewise: a suite's server sessions
	// never write the user's own ~/.local/state.
	os.Setenv("XDG_STATE_HOME", filepath.Join(tmp, "state"))
	// The fleet ceiling's consumer obligation: an environment carrying
	// GOMEMLIMIT suppresses the derivation for the whole process — and
	// an oracle running this binary sets it — so the suites clear it
	// before the process's first derivation. Clearing the variable
	// does not undo the limit the runtime read at startup: the first
	// derivation then runs capped at that prior limit, which is a
	// derivation still, where the operator's word would have been none.
	os.Unsetenv("GOMEMLIMIT")
	integrationtest.DefaultToShort()
	code := fixtureguard.Guard(os.Stderr, fixtureRoot, m.Run)
	os.RemoveAll(tmp)
	os.Exit(code)
}

// preserveBuildCache keeps an explicit operator setting (including off) or
// resolves Go's effective setting while the original cache home is in force.
func preserveBuildCache(resolve func() (string, error)) error {
	if os.Getenv("GOCACHE") != "" {
		return nil
	}
	cache, err := resolve()
	if err != nil {
		return err
	}
	// Go writes the setting followed by LF. Spaces are pathname bytes,
	// including a trailing space read from a GOENV file.
	cache = strings.TrimSuffix(cache, "\n")
	if cache != "off" && !filepath.IsAbs(cache) {
		return errors.New("testsuite: go env GOCACHE returned no absolute cache setting")
	}
	return os.Setenv("GOCACHE", cache)
}

func resolveBuildCache(ctx context.Context, runner gotool.Runner) (string, error) {
	out, err := runner.Run(ctx, "", os.Environ(), "env", "GOCACHE")
	return string(out), err
}
