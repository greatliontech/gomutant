package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/gotool"
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
	// A plain command, not commandContext: the sampler is a metadata
	// read, not an oracle process — the oracle constructor's process
	// containment (job objects on windows, where its wrapper type does
	// not even satisfy this signature) has no business here.
	cmd := exec.CommandContext(ctx, "go", "env", "GOVERSION")
	cmd.Dir = dir
	cmd.Env = env
	return cmd
}

// SwapGoVersionSamplerForTest replaces the sampler and returns the
// restore — the skew paths are unreachable under a healthy real
// toolchain, so their tests inject the sample. NOT parallel-safe:
// the seam is package state, the ladder reads the OS environment on
// every load, and tests set an ambient driver process-wide, so no
// test in this module runs t.Parallel(); a test that starts must not.
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
// selection-applied environment, the same ladder (toolchainGuard).
func CheckToolchainProvenance(ctx context.Context, dir string, sel Selection) error {
	env, err := sel.applyEnv(GoEnv(dir))
	if err != nil {
		return err
	}
	_, err = toolchainGuard(ctx, dir, env)
	return err
}

// CheckHarnessEnvironment is the ladder's environment arm alone,
// decidable from the OS environment, the tree root, and the selection
// with no process spawned: every verb's preparation stage fires it
// before the lock (REQ-exec-preparation), and the load's ladder fires
// it again by construction.
func CheckHarnessEnvironment(dir string, sel Selection) error {
	env, err := sel.applyEnv(GoEnv(dir))
	if err != nil {
		return err
	}
	return environmentArms(env)
}

// environmentArms is the ladder's environment arm in its two halves,
// the one composition every entry reads — the preparation stage and
// the ladder's head alike — so a half added to one cannot miss the
// other: the OS environment's own refusals first, then the composed
// environment's GODEBUG.
func environmentArms(env []string) error {
	if err := ambientEnvironmentRefused(os.Environ()); err != nil {
		return err
	}
	return harnessEventsSilenced(env)
}

// ambientEnvironmentRefused is the environment arm's read of the OS
// environment itself, before any composition: a package driver
// (GOPACKAGESDRIVER other than off) would hand every load to a
// program of the operator's, never the go command's listing — GoEnv
// pins it off for every load and spawn, so the operator hears here
// that the driver is not honoured rather than measuring a source
// model they did not ask for; and an environment the exec key=value
// form cannot express — a duplicate key under the platform's rule, a
// malformed entry, a NUL byte — is what the declared producer
// environment refuses, named here in the operator's own words rather
// than by gofresh at the first engine (REQ-exec-preparation).
func ambientEnvironmentRefused(env []string) error {
	if _, err := gotool.NormalizeEnv(env); err != nil {
		return fmt.Errorf("gomutant: ambient environment: %w", err)
	}
	if driver, ok := LookupEnvKey(env, "GOPACKAGESDRIVER"); ok && driver != "" && driver != "off" {
		return fmt.Errorf("gomutant: GOPACKAGESDRIVER=%q is unsupported because freshness analysis requires Go package loading: the loader would answer from that program, never the go command's listing", driver)
	}
	return nil
}

// toolchainGuard is the load's one toolchain ladder, three arms in
// order: the environment arm (environmentArms — the OS environment's
// own refusals, then the composed environment's GODEBUG that silences
// the harness's build-fail events; no process spawned, so first), the
// skew provenance over the sampled toolchain, then the build-events
// floor over the same sample — returning the sample; the load and the
// pre-write check read this one function, so the two cannot drift
// (they did: the check once ran the skew half alone).
func toolchainGuard(ctx context.Context, dir string, env []string) (string, error) {
	// The environment arm spawns no process, so it runs before the
	// sampler's own (the stage question rides REQ-exec-provenance's
	// ladder; REQ-exec-preparation's inventory holds no toolchain
	// refusal).
	if err := environmentArms(env); err != nil {
		return "", err
	}
	sampled, err := toolchainProvenance(ctx, dir, env)
	if err != nil {
		return "", err
	}
	if err := toolchainSupportsBuildEvents(sampled); err != nil {
		return "", err
	}
	return sampled, nil
}

// harnessEventsSilenced refuses an environment under which the go tool
// reports build failures as text instead of its build-fail event: the
// GODEBUG setting gotestjsonbuildtext=1, which the go command reads
// from the OS environment alone (go.mod's godebug directive and go.env
// reach only the built binaries). The effective setting is resolved
// as the go command resolves it — three of its rules, each mirrored
// here: the spawn hands the tool the LAST entry of a duplicated key
// (os/exec's dedupEnv keeps the last occurrence, case-folded on
// windows), the tool's own parse takes the LAST pair of a repeated
// setting (internal/godebug's parse scans backward), and a bisect
// suffix `#pattern` is stripped from the value before the read
// (internal/godebug cuts the text at the first `#`), so `1#x` reads
// as 1 — refused here unconditionally, since whether the bisect fires
// depends on the stack and a refusal never scores. It is the floor's
// sibling in the one ladder: both name the event the classifier
// reads.
func harnessEventsSilenced(env []string) error {
	// The spawn hands the go tool one GODEBUG — the last entry under
	// the platform's key rule — and the tool reads the last pair of
	// that one value.
	value, _ := LookupEnvKey(env, "GODEBUG")
	setting := ""
	for _, pair := range strings.Split(value, ",") {
		if name, v, ok := strings.Cut(pair, "="); ok && name == "gotestjsonbuildtext" {
			setting = v
		}
	}
	setting, _, _ = strings.Cut(setting, "#")
	if setting == "1" {
		return errors.New("gomutant: GODEBUG sets gotestjsonbuildtext=1, which silences the harness's build-fail events: build-failure classification requires them")
	}
	return nil
}
