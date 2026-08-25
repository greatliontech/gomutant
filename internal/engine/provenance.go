package engine

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/greatliontech/gofresh"
)

// goVersionSampler reports the version of the `go` that will serve a
// load's package listing and the run's test executions: sampled in
// the TARGET directory under the SELECTION-APPLIED environment — the
// declared GOTOOLCHAIN directive honored, the operator's stray
// GOWORK already stripped — so the witnessed toolchain is the one
// the run actually uses (gofresh.ToolchainSkew's sampling contract;
// a cwd- or ambient-env-sampled version can agree while the
// selection's toolchain skews). A seam for the skew tests; the
// production sampler always execs.
var goVersionSampler = func(ctx context.Context, dir string, env []string) (string, error) {
	cmd := goVersionCmd(ctx, dir, env)
	out, err := cmd.Output()
	if err != nil {
		// The sampler can be the first consumer of a declared
		// toolchain directive: its refusal names go's own cause (an
		// unknown or undownloadable toolchain), never a bare exit
		// status.
		var exit *exec.ExitError
		if errors.As(err, &exit) && len(exit.Stderr) > 0 {
			return "", fmt.Errorf("gomutant: sample toolchain version: %w: %s", err, strings.TrimSpace(string(exit.Stderr)))
		}
		return "", fmt.Errorf("gomutant: sample toolchain version: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// goVersionCmd is the sampler's pure construction, split so the
// Dir/Env wiring is unit-testable UNDER the whole-function test seam
// (a seam-swapped sampler never exercises it, and a healthy host's
// `go env GOVERSION` is invariant under both — nothing else could
// notice a dropped env).
func goVersionCmd(ctx context.Context, dir string, env []string) *exec.Cmd {
	cmd := commandContext(ctx, "go", "env", "GOVERSION")
	cmd.Dir = dir
	cmd.Env = env
	return cmd
}

// SwapGoVersionSamplerForTest replaces the sampler and returns the
// restore — the skew paths are unreachable under a healthy real
// toolchain, so their tests inject the sample. NOT parallel-safe:
// the seam is package state, and no test in this module runs
// t.Parallel(); a test that starts must not.
func SwapGoVersionSamplerForTest(f func(context.Context, string, []string) (string, error)) (restore func()) {
	prior := goVersionSampler
	goVersionSampler = f
	return func() { goVersionSampler = prior }
}

// toolchainProvenance guards a load (REQ-exec-provenance): a
// compiled-in frontend OLDER than the toolchain serving the load
// judges sources it predates — parse refusals, analysis panics,
// silently shifted evidence — and refuses here, once, for every
// entry, before any package loads. The sample is returned so ONE
// exec also serves the build-events floor check — two probes of the
// same toolchain in the same dir under the same env would be the
// same subprocess twice.
func toolchainProvenance(ctx context.Context, dir string, env []string) (sampled string, err error) {
	sampled, err = goVersionSampler(ctx, dir, env)
	if err != nil {
		return "", err
	}
	return sampled, gofresh.ToolchainSkew(sampled)
}

// CheckToolchainProvenance runs the load guard standalone, for verbs
// that mutate state BEFORE any tree load would fire it (attest
// writes the findings document first): same dir, same
// selection-applied environment, same refusal.
func CheckToolchainProvenance(ctx context.Context, dir string, sel Selection) error {
	env, err := sel.applyEnv(GoEnv(dir))
	if err != nil {
		return err
	}
	_, err = toolchainProvenance(ctx, dir, env)
	return err
}
