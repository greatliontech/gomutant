package engine

import (
	"context"
	"os/exec"

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
// gofresh's memoized sampler under the tree's runner, asked once per
// ladder — the provenance check judges the skew and hands the floor
// the sample it judged — in the TARGET directory under the
// SELECTION-APPLIED environment (the declared GOTOOLCHAIN directive
// honored, the operator's stray GOWORK already stripped), so the
// witnessed toolchain is the one the run actually uses
// (gofresh.ToolchainSkew's sampling contract; a cwd- or
// ambient-env-sampled version can agree while the selection's
// toolchain skews). The sampler is one ladder's, never the process's:
// a long-lived server samples again at its next load, so a toolchain
// moved between two requests is witnessed there (the served face's
// tree cache keys on GOVERSION for the same reason). A seam for the
// skew tests; production always mints gofresh's sampler.
var newToolchainSampler = func() gofresh.ToolchainSampler {
	return &gotool.Sampler{Runner: goRunner}
}

// SwapGoVersionSamplerForTest replaces the sample every minted sampler
// answers and returns the restore — the skew paths are unreachable
// under a healthy real toolchain, so their tests inject the sample.
// The injected function is the sampler itself, unmemoized, so a test
// counting its asks sees exactly the ladder's reads.
func SwapGoVersionSamplerForTest(f func(context.Context, string, []string) (string, error)) (restore func()) {
	prior := newToolchainSampler
	newToolchainSampler = func() gofresh.ToolchainSampler { return gofresh.SampleFunc(f) }
	return func() { newToolchainSampler = prior }
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
