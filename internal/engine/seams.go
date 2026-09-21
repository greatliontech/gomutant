package engine

import (
	"context"
	"os/exec"
	"strings"
	"sync"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/gotool"
)

// The engine package's two test seams, package state read by the
// tree's runner and the toolchain ladder. NOT parallel-safe: the
// ladder reads the OS environment on every load and tests set an
// ambient driver process-wide, so no test in this module runs
// t.Parallel(); a test that starts must not. Production never writes
// either seam, so the runner's hook and the ladder read them
// unsynchronized.

// newToolchainSampler mints the sampler one toolchain ladder reads:
// gofresh's memoized sampler under the tree's runner, so the ladder's
// two reads of the serving toolchain — the skew judgment and the
// build-events floor — are one `go env GOVERSION`, in the TARGET
// directory under the SELECTION-APPLIED environment (the declared
// GOTOOLCHAIN directive honored, the operator's stray GOWORK already
// stripped), so the witnessed toolchain is the one the run actually
// uses (gofresh.ToolchainSkew's sampling contract; a cwd- or
// ambient-env-sampled version can agree while the selection's
// toolchain skews). The memo is one ladder's, never the process's: a
// long-lived server samples again at its next load, so a toolchain
// moved between two requests is witnessed there (the served face's
// tree cache keys on GOVERSION for the same reason). A seam for the
// skew tests; production always mints the memoized sampler.
var newToolchainSampler = func() gofresh.ToolchainSampler {
	return &gotool.Sampler{Runner: goRunner}
}

// SwapGoVersionSamplerForTest replaces the sample every minted sampler
// answers and returns the restore — the skew paths are unreachable
// under a healthy real toolchain, so their tests inject the sample.
// The injected sample is memoized per ladder exactly as the production
// sampler is, so an injected sampler sees one ask per ladder.
func SwapGoVersionSamplerForTest(f func(context.Context, string, []string) (string, error)) (restore func()) {
	prior := newToolchainSampler
	newToolchainSampler = func() gofresh.ToolchainSampler { return &sampleMemo{f: f} }
	return func() { newToolchainSampler = prior }
}

// sampleMemo memoizes an injected sample per (directory, environment)
// for one ladder, a cancelled ask never memoized — gotool.Sampler's
// shape over a function.
type sampleMemo struct {
	f    func(context.Context, string, []string) (string, error)
	mu   sync.Mutex
	memo map[string]sampled
}

type sampled struct {
	version string
	err     error
}

func (s *sampleMemo) Sample(ctx context.Context, dir string, env []string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	key := dir + "\x00" + strings.Join(env, "\x00")
	s.mu.Lock()
	got, ok := s.memo[key]
	s.mu.Unlock()
	if ok {
		return got.version, got.err
	}
	got.version, got.err = s.f(ctx, dir, env)
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	s.mu.Lock()
	if s.memo == nil {
		s.memo = map[string]sampled{}
	}
	s.memo[key] = got
	s.mu.Unlock()
	return got.version, got.err
}

// goCommandObserver is the test seam behind the runner's hook
// (observeGoCommand): nil in production.
var goCommandObserver func(*exec.Cmd)

// ObserveGoCommandsForTest installs an observer of every go command
// prepared through the tree's runner and returns the restore — the
// wiring pins read it.
func ObserveGoCommandsForTest(observe func(*exec.Cmd)) (restore func()) {
	prior := goCommandObserver
	goCommandObserver = observe
	return func() { goCommandObserver = prior }
}
