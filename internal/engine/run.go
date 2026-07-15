package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/gomutant/internal/contextio"
)

// MutantOutcome classifies one overlay run.
type MutantOutcome int

const (
	// MutantDiscarded: the mutant does not compile (or its run was
	// cancelled); it proves nothing — deliberately the zero value, so an
	// unwritten outcome can never read as a verdict.
	MutantDiscarded MutantOutcome = iota
	// MutantKilled: an oracle test failed (or the run timed out — behavior
	// changed).
	MutantKilled
	// MutantSurvived: every oracle test passed against the mutant.
	MutantSurvived
)

// rapidPkg is the recognized property-test library: its check runners
// persist a failure reproducer into the tree unless told not to, which a
// mutant run must never allow (REQ-mut-overlay).
const rapidPkg = "pgregory.net/rapid"

// SplitRapidPkgs partitions test packages by whether their test files
// (in-package or external variant) import pgregory.net/rapid. Rapid packages
// need -rapid.nofailfile so a mutant-induced property failure never writes a
// reproducer into the source tree — and one mutant's failfile cannot replay
// into the next mutant's run (REQ-mut-overlay). The flag is per-binary: a
// test binary that does not register it fails on the unknown flag and reads
// as a false kill, so the two groups must run in separate invocations. The
// scan is of direct imports only — a test driving rapid solely through a
// helper package escapes the guard; the failure mode there is visible
// failfile litter, never a false kill.
func (t *Tree) SplitRapidPkgs(testPkgs []string) (rapid, plain []string) {
	rapid, plain, _ = t.SplitRapidPkgsContext(context.Background(), testPkgs)
	return rapid, plain
}

// SplitRapidPkgsContext is SplitRapidPkgs with cooperative cancellation.
func (t *Tree) SplitRapidPkgsContext(ctx context.Context, testPkgs []string) (rapid, plain []string, err error) {
	byPath := map[string]bool{}
	for _, pkg := range t.pkgs {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if byPath[pkg.PkgPath] {
			continue
		}
		for _, f := range pkg.Syntax {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			for _, imp := range f.Imports {
				if strings.Trim(imp.Path.Value, `"`) == rapidPkg {
					byPath[pkg.PkgPath] = true
				}
			}
		}
	}
	for _, p := range testPkgs {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if byPath[p] || byPath[p+"_test"] {
			rapid = append(rapid, p)
		} else {
			plain = append(plain, p)
		}
	}
	return rapid, plain, nil
}

// TimeoutKiller is the killer attribution of a timed-out mutant run: the
// hang itself is the noticed breakage, so no named test claims the kill
// (REQ-exec-attribution).
const TimeoutKiller = "(timeout)"

// PackageKillerPrefix prefixes the killer attribution of a mutant that
// breaks a test binary at package scope — a panic in a goroutine, an
// os.Exit, a TestMain failure — where go test emits no test-level fail
// event. Such a kill is admitted only after a differential baseline probe
// clears the environment (REQ-exec-attribution).
const PackageKillerPrefix = "(package failure: "

// RunMutant executes the oracle tests against one mutant through a build
// overlay — the tree is never touched (REQ-mut-overlay). binFlags are passed
// to the test binaries after the package list.
//
// testPkgs must all be scoped by runRegex as their own oracle pattern: an
// oracle spanning differently-named tests runs per package
// (REQ-exec-oracle-run) — one union pattern would also run a same-named
// non-oracle test in a sibling package, whose failure is unattributable and
// aborts the sweep.
//
// A kill must be attributed (REQ-core-attributed-kills): a named failing
// test in the run's -json stream (returned as "<pkg>.<TopLevelTest>"), a
// timeout (TimeoutKiller — behavior changed: it hangs), or a package-scope
// failure the baseline probe attributes to the mutant (PackageKillerPrefix).
// A run that fails any other way is environmental noise — an unregistered
// flag, a loaded machine, a dying binary — and returns an error, never a
// kill: a corrupted measurement must never read as a sound one.
func RunMutant(ctx context.Context, dir string, m Mutant, testPkgs []string, runRegex string, timeout time.Duration, binFlags []string) (MutantOutcome, string, error) {
	outcome, killer, _, err := runMutant(ctx, dir, m, testPkgs, runRegex, timeout, binFlags, "", "", GoEnv(dir))
	return outcome, killer, err
}

// RunMutantEnv is RunMutant under an already-frozen complete environment.
func RunMutantEnv(ctx context.Context, dir string, m Mutant, testPkgs []string, runRegex string, timeout time.Duration, binFlags, env []string) (MutantOutcome, string, error) {
	outcome, killer, _, err := runMutant(ctx, dir, m, testPkgs, runRegex, timeout, binFlags, "", "", env)
	return outcome, killer, err
}

// RunMutantObserved is RunMutant with finalized absolute runtime-input evidence
// for the test process and any differential baseline process it launches.
func RunMutantObserved(ctx context.Context, dir string, m Mutant, testPkgs []string, runRegex string, timeout time.Duration, binFlags []string, moduleDir, packageDir string) (MutantOutcome, string, runtimeinput.State, error) {
	return runMutant(ctx, dir, m, testPkgs, runRegex, timeout, binFlags, moduleDir, packageDir, GoEnv(dir))
}

// RunMutantObservedEnv is RunMutantObserved under an already-frozen complete
// environment.
func RunMutantObservedEnv(ctx context.Context, dir string, m Mutant, testPkgs []string, runRegex string, timeout time.Duration, binFlags []string, moduleDir, packageDir string, env []string) (MutantOutcome, string, runtimeinput.State, error) {
	return runMutant(ctx, dir, m, testPkgs, runRegex, timeout, binFlags, moduleDir, packageDir, env)
}

func runMutant(ctx context.Context, dir string, m Mutant, testPkgs []string, runRegex string, timeout time.Duration, binFlags []string, moduleDir, packageDir string, env []string) (MutantOutcome, string, runtimeinput.State, error) {
	firstOutcome, firstKiller, firstState, err := runMutantOnce(ctx, dir, m, testPkgs, runRegex, timeout, binFlags, moduleDir, packageDir, env)
	if err != nil || !firstState.OK || firstState.Unverifiable {
		return firstOutcome, firstKiller, firstState, err
	}
	empty, err := runtimeinput.MergeEnv(moduleDir, env)
	if err != nil {
		return MutantDiscarded, "", runtimeinput.State{}, err
	}
	if err := ctx.Err(); err != nil {
		return MutantDiscarded, "", runtimeinput.State{}, err
	}
	empty, err = runtimeinput.AbsoluteEnv(empty, moduleDir, env)
	if err != nil {
		return MutantDiscarded, "", runtimeinput.State{}, err
	}
	if err := ctx.Err(); err != nil {
		return MutantDiscarded, "", runtimeinput.State{}, err
	}
	if firstState == empty {
		return firstOutcome, firstKiller, firstState, nil
	}

	// The first run discovers runtime identities. The second is the scored
	// measurement; disagreement makes its evidence non-reusable without
	// suppressing the attributed outcome.
	secondOutcome, secondKiller, secondState, err := runMutantOnce(ctx, dir, m, testPkgs, runRegex, timeout, binFlags, moduleDir, packageDir, env)
	if err != nil {
		return secondOutcome, secondKiller, secondState, err
	}
	combined, err := mergeRuntimeEvidenceContext(ctx, dir, env, firstState, secondState)
	if err != nil {
		return MutantDiscarded, "", runtimeinput.State{}, err
	}
	if secondState.Unverifiable {
		return secondOutcome, secondKiller, combined, nil
	}
	if firstState != secondState {
		combined, err = addRuntimeEvidenceReasonContext(ctx, dir, env, combined, "runtime input observations changed between repeated mutant executions")
		if err != nil {
			return MutantDiscarded, "", runtimeinput.State{}, err
		}
	}
	return secondOutcome, secondKiller, combined, nil
}

func runMutantOnce(ctx context.Context, dir string, m Mutant, testPkgs []string, runRegex string, timeout time.Duration, binFlags []string, moduleDir, packageDir string, env []string) (MutantOutcome, string, runtimeinput.State, error) {
	if err := ctx.Err(); err != nil {
		return MutantDiscarded, "", runtimeinput.State{}, err
	}
	capture := moduleDir != "" && packageDir != ""
	tmp, err := os.MkdirTemp("", "gomutant-*")
	if err != nil {
		return MutantDiscarded, "", runtimeinput.State{}, err
	}
	defer os.RemoveAll(tmp)
	if len(m.Replacements) == 0 {
		return MutantDiscarded, "", runtimeinput.State{}, fmt.Errorf("mutant has no file replacements")
	}
	replace := make(map[string]string, len(m.Replacements))
	for i, replacement := range m.Replacements {
		if err := ctx.Err(); err != nil {
			return MutantDiscarded, "", runtimeinput.State{}, err
		}
		if replacement.File == "" || replacement.Source == nil {
			return MutantDiscarded, "", runtimeinput.State{}, fmt.Errorf("mutant replacement %d is incomplete", i+1)
		}
		if _, duplicate := replace[replacement.File]; duplicate {
			return MutantDiscarded, "", runtimeinput.State{}, fmt.Errorf("mutant replaces %s more than once", replacement.File)
		}
		mutFile := filepath.Join(tmp, fmt.Sprintf("replacement-%d%s", i, filepath.Ext(replacement.File)))
		if err := contextio.WriteFile(ctx, mutFile, replacement.Source, 0o644); err != nil {
			return MutantDiscarded, "", runtimeinput.State{}, err
		}
		if err := ctx.Err(); err != nil {
			return MutantDiscarded, "", runtimeinput.State{}, err
		}
		replace[replacement.File] = mutFile
	}
	overlay := filepath.Join(tmp, "overlay.json")
	oj, err := json.Marshal(struct {
		Replace map[string]string
	}{Replace: replace})
	if err != nil {
		return MutantDiscarded, "", runtimeinput.State{}, err
	}
	if err := ctx.Err(); err != nil {
		return MutantDiscarded, "", runtimeinput.State{}, err
	}
	if err := contextio.WriteFile(ctx, overlay, oj, 0o644); err != nil {
		return MutantDiscarded, "", runtimeinput.State{}, err
	}
	if err := ctx.Err(); err != nil {
		return MutantDiscarded, "", runtimeinput.State{}, err
	}

	parent := ctx
	runCtx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	// -failfast: one oracle failure decides the binary's verdict; the
	// remaining tests in it prove nothing further about this mutant.
	testlog := filepath.Join(tmp, "mutant.testlog")
	baseTestlog := filepath.Join(tmp, "baseline.testlog")
	baseTail := append([]string{"-count=1", "-failfast", "-run", runRegex}, testPkgs...)
	baseArgs := goTestArgs(timeout, append(baseTail, binFlags...)...)
	mutantTail := append([]string{"-overlay", overlay, "-count=1", "-failfast", "-run", runRegex}, testPkgs...)
	args := goTestArgs(timeout, append(mutantTail, binFlags...)...)
	if capture {
		args = append(args, "-test.testlogfile="+testlog)
		baseArgs = append(baseArgs, "-test.testlogfile="+baseTestlog)
	}
	cmd := commandContext(runCtx, "go", args...)
	cmd.Dir = dir
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	if runCtx.Err() == context.DeadlineExceeded {
		state, err := processObservationContext(ctx, testlog, moduleDir, packageDir, "mutant test process timed out", env, capture)
		return MutantKilled, TimeoutKiller, state, err
	}
	if runCtx.Err() != nil {
		state, observationErr := processObservationContext(ctx, testlog, moduleDir, packageDir, "mutant test process was cancelled", env, capture)
		if observationErr != nil {
			return MutantDiscarded, "", runtimeinput.State{}, observationErr
		}
		return MutantDiscarded, "", state, ctx.Err()
	}
	killer, parseErr := firstFailingTest(stdout.Bytes())
	if parseErr != nil {
		state, observationErr := processObservationContext(ctx, testlog, moduleDir, packageDir, "go test output was malformed before observation finalization", env, capture)
		if observationErr != nil {
			return MutantDiscarded, "", runtimeinput.State{}, observationErr
		}
		return MutantDiscarded, "", state, fmt.Errorf("parse go test output: %w", parseErr)
	}
	switch {
	case runErr == nil:
		state, err := processObservationContext(ctx, testlog, moduleDir, packageDir, "", env, capture)
		return MutantSurvived, "", state, err
	case strings.Contains(stdout.String(), "[build failed]"):
		state, err := processObservationContext(ctx, testlog, moduleDir, packageDir, "mutant test process did not start because the mutant failed to build", env, capture)
		return MutantDiscarded, "", state, err
	case killer != "":
		reason := ""
		if testProcessPanicked(stdout.Bytes()) || !testFailureCompleted(stdout.Bytes(), killer) {
			reason = "mutant test process panicked before observation finalization"
			if !testProcessPanicked(stdout.Bytes()) {
				reason = "mutant test process exited before observation finalization"
			}
		}
		state, err := processObservationContext(ctx, testlog, moduleDir, packageDir, reason, env, capture)
		return MutantKilled, killer, state, err
	}

	// The run failed with no test-level attribution. Two very different
	// causes share this shape: the mutant breaking the binary at package
	// scope (a goroutine panic, an os.Exit, a TestMain failure — the
	// strongest kind of kill), and environmental noise. A differential
	// baseline probe — the same invocation without the overlay — tells them
	// apart: noise fails the baseline too; a mutant-caused break does not
	// (REQ-exec-attribution).
	if pkg := failedPackage(stdout.Bytes()); pkg != "" {
		baseCtx, baseCancel := context.WithTimeout(parent, timeout)
		defer baseCancel()
		base := commandContext(baseCtx, "go", baseArgs...)
		base.Dir = dir
		base.Env = env
		baseErr := base.Run()
		mutantState, err := processObservationContext(ctx, testlog, moduleDir, packageDir, "mutant test process exited before observation finalization", env, capture)
		if err != nil {
			return MutantDiscarded, "", runtimeinput.State{}, err
		}
		if baseCtx.Err() != nil {
			baselineState, observationErr := processObservationContext(ctx, baseTestlog, moduleDir, packageDir, "baseline test process did not complete", env, capture)
			if observationErr != nil {
				return MutantDiscarded, "", runtimeinput.State{}, observationErr
			}
			state, mergeErr := mergeProcessObservationsContext(ctx, dir, env, capture, mutantState, baselineState)
			if mergeErr != nil {
				return MutantDiscarded, "", runtimeinput.State{}, mergeErr
			}
			return MutantDiscarded, "", state, baseCtx.Err()
		}
		if baseErr == nil {
			baselineState, err := processObservationContext(ctx, baseTestlog, moduleDir, packageDir, "", env, capture)
			if err != nil {
				return MutantDiscarded, "", runtimeinput.State{}, err
			}
			state, err := mergeProcessObservationsContext(ctx, dir, env, capture, mutantState, baselineState)
			return MutantKilled, PackageKillerPrefix + pkg + ")", state, err
		}
		baselineState, observationErr := processObservationContext(ctx, baseTestlog, moduleDir, packageDir, "baseline test process failed before observation finalization", env, capture)
		if observationErr != nil {
			return MutantDiscarded, "", runtimeinput.State{}, observationErr
		}
		state, mergeErr := mergeProcessObservationsContext(ctx, dir, env, capture, mutantState, baselineState)
		if mergeErr != nil {
			return MutantDiscarded, "", runtimeinput.State{}, mergeErr
		}
		return MutantDiscarded, "", state, fmt.Errorf("mutant run failed with no test-attributed kill (environmental noise, not a kill; baseline probe did not clear it): %v: %s", runErr, tail(stderr.String()+stdout.String(), 400))
	}
	state, observationErr := processObservationContext(ctx, testlog, moduleDir, packageDir, "mutant test process failed before attributable completion", env, capture)
	if observationErr != nil {
		return MutantDiscarded, "", runtimeinput.State{}, observationErr
	}
	return MutantDiscarded, "", state, fmt.Errorf("mutant run failed with no test-attributed kill (environmental noise, not a kill; baseline probe did not clear it): %v: %s", runErr, tail(stderr.String()+stdout.String(), 400))
}

func processObservation(path, moduleDir, packageDir, incompleteReason string, env []string, capture bool) (runtimeinput.State, error) {
	return processObservationContext(context.Background(), path, moduleDir, packageDir, incompleteReason, env, capture)
}

func processObservationContext(ctx context.Context, path, moduleDir, packageDir, incompleteReason string, env []string, capture bool) (runtimeinput.State, error) {
	if err := ctx.Err(); err != nil {
		return runtimeinput.State{}, err
	}
	if !capture {
		return runtimeinput.State{}, nil
	}
	var state runtimeinput.State
	var err error
	if incompleteReason != "" {
		incomplete, incompleteErr := runtimeinput.IncompleteEnv(moduleDir, incompleteReason, env)
		if incompleteErr != nil {
			return runtimeinput.State{}, incompleteErr
		}
		data, readErr := contextio.ReadFile(ctx, path)
		if os.IsNotExist(readErr) {
			state = incomplete
		} else if readErr != nil {
			return runtimeinput.State{}, readErr
		} else {
			partial, parseErr := runtimeinput.FromTestLogEnv(data, moduleDir, packageDir, env)
			if parseErr != nil {
				// A killed process can leave an oversized partial scanner token.
				// Preserve every complete record before that unfinished tail.
				lastRecord := bytes.LastIndexByte(data, '\n')
				if lastRecord < 0 {
					return incomplete, nil
				}
				partial, parseErr = runtimeinput.FromTestLogEnv(data[:lastRecord+1], moduleDir, packageDir, env)
				if parseErr != nil {
					return runtimeinput.State{}, parseErr
				}
			}
			state, err = runtimeinput.MergeEnv(moduleDir, env, partial, incomplete)
		}
	} else {
		data, readErr := contextio.ReadFile(ctx, path)
		if os.IsNotExist(readErr) {
			state, err = runtimeinput.IncompleteEnv(moduleDir, "test process produced no runtime-input log", env)
		} else if readErr != nil {
			return runtimeinput.State{}, readErr
		} else {
			state, err = runtimeinput.FromTestLogEnv(data, moduleDir, packageDir, env)
		}
	}
	if err != nil {
		return runtimeinput.State{}, err
	}
	if err := ctx.Err(); err != nil {
		return runtimeinput.State{}, err
	}
	return absoluteRuntimeEvidenceContext(ctx, state, moduleDir, env)
}

func absoluteRuntimeEvidence(state runtimeinput.State, moduleDir string, env []string) (runtimeinput.State, error) {
	return absoluteRuntimeEvidenceContext(context.Background(), state, moduleDir, env)
}

func absoluteRuntimeEvidenceContext(ctx context.Context, state runtimeinput.State, moduleDir string, env []string) (runtimeinput.State, error) {
	if err := ctx.Err(); err != nil {
		return runtimeinput.State{}, err
	}
	absolute, err := runtimeinput.AbsoluteEnv(state, moduleDir, env)
	if cancelErr := ctx.Err(); cancelErr != nil {
		return runtimeinput.State{}, cancelErr
	}
	if err == nil {
		return absolute, nil
	}
	if !state.OK || state.Manifest == "" || state.Digest == "" {
		return runtimeinput.State{}, err
	}
	current, currentErr := runtimeinput.CurrentEnvContext(ctx, state.Manifest, moduleDir, env)
	if currentErr != nil && ctx.Err() != nil {
		return runtimeinput.State{}, ctx.Err()
	}
	if currentErr != nil || !current.OK || current == state {
		return runtimeinput.State{}, err
	}
	incomplete, incompleteErr := runtimeinput.IncompleteEnv(moduleDir, "runtime input observation could not be finalized for reuse: "+err.Error(), env)
	if incompleteErr != nil {
		return runtimeinput.State{}, incompleteErr
	}
	if merged, mergeErr := mergeRuntimeEvidenceContext(ctx, moduleDir, env, current, incomplete); mergeErr == nil {
		incomplete = merged
	}
	return absoluteNonReusableRuntimeEvidence(ctx, incomplete, moduleDir, env)
}

func absoluteNonReusableRuntimeEvidence(ctx context.Context, incomplete runtimeinput.State, moduleDir string, env []string) (runtimeinput.State, error) {
	absolute, err := runtimeinput.AbsoluteEnv(incomplete, moduleDir, env)
	if cancelErr := ctx.Err(); cancelErr != nil {
		return runtimeinput.State{}, cancelErr
	}
	if err == nil {
		return absolute, nil
	}
	// Once movement is proven, reuse is forbidden. If a preserved path moves
	// again during conversion, retain the reason without requiring that path
	// to stabilize merely to publish the fresh mutation outcome.
	incomplete, incompleteErr := runtimeinput.IncompleteEnv(moduleDir, "runtime input observation could not be finalized for reuse: "+err.Error(), env)
	if incompleteErr != nil {
		return runtimeinput.State{}, incompleteErr
	}
	absolute, err = runtimeinput.AbsoluteEnv(incomplete, moduleDir, env)
	if cancelErr := ctx.Err(); cancelErr != nil {
		return runtimeinput.State{}, cancelErr
	}
	return absolute, err
}

func mergeProcessObservations(root string, env []string, capture bool, states ...runtimeinput.State) (runtimeinput.State, error) {
	return mergeProcessObservationsContext(context.Background(), root, env, capture, states...)
}

func mergeProcessObservationsContext(ctx context.Context, root string, env []string, capture bool, states ...runtimeinput.State) (runtimeinput.State, error) {
	if !capture {
		return runtimeinput.State{}, nil
	}
	return mergeRuntimeEvidenceContext(ctx, root, env, states...)
}

func mergeRuntimeEvidence(root string, env []string, states ...runtimeinput.State) (runtimeinput.State, error) {
	return mergeRuntimeEvidenceContext(context.Background(), root, env, states...)
}

func mergeRuntimeEvidenceContext(ctx context.Context, root string, env []string, states ...runtimeinput.State) (runtimeinput.State, error) {
	if err := ctx.Err(); err != nil {
		return runtimeinput.State{}, err
	}
	state, err := runtimeinput.MergeEnv(root, env, states...)
	if cancelErr := ctx.Err(); cancelErr != nil {
		return runtimeinput.State{}, cancelErr
	}
	if err == nil {
		return state, nil
	}
	result, incompleteErr := runtimeinput.IncompleteEnv(root, "runtime input observations could not be merged for reuse: "+err.Error(), env)
	if incompleteErr != nil {
		return runtimeinput.State{}, incompleteErr
	}
	for _, input := range states {
		if err := ctx.Err(); err != nil {
			return runtimeinput.State{}, err
		}
		if input.Manifest == "" {
			continue
		}
		current, currentErr := runtimeinput.CurrentEnvContext(ctx, input.Manifest, root, env)
		if currentErr != nil && ctx.Err() != nil {
			return runtimeinput.State{}, ctx.Err()
		}
		if currentErr != nil || !current.OK {
			continue
		}
		merged, mergeErr := runtimeinput.MergeEnv(root, env, result, current)
		if err := ctx.Err(); err != nil {
			return runtimeinput.State{}, err
		}
		if mergeErr == nil {
			result = merged
		}
	}
	return result, nil
}

func addRuntimeEvidenceReason(root string, env []string, state runtimeinput.State, reason string) (runtimeinput.State, error) {
	return addRuntimeEvidenceReasonContext(context.Background(), root, env, state, reason)
}

func addRuntimeEvidenceReasonContext(ctx context.Context, root string, env []string, state runtimeinput.State, reason string) (runtimeinput.State, error) {
	incomplete, err := runtimeinput.IncompleteEnv(root, reason, env)
	if err != nil {
		return runtimeinput.State{}, err
	}
	return mergeRuntimeEvidenceContext(ctx, root, env, state, incomplete)
}

// TestProbe runs the named test on the unmutated tree and reports how many
// top-level tests ran and whether the run passed. It is the baseline an
// ephemeral run needs before scoring anything (REQ-exec-ephemeral): a -run
// matching zero tests, or a test already failing on the clean tree, cannot
// attribute a mutant, so a verdict against it would be a fabricated finding.
func TestProbe(ctx context.Context, dir, testPkg, run string, timeout time.Duration, binFlags []string) (ran int, passed bool, err error) {
	return TestProbeEnv(ctx, dir, testPkg, run, timeout, binFlags, GoEnv(dir))
}

// TestProbeEnv is TestProbe under an already-frozen complete environment.
func TestProbeEnv(ctx context.Context, dir, testPkg, run string, timeout time.Duration, binFlags, env []string) (ran int, passed bool, err error) {
	ran, passed, _, err = testProbeOnceObservedEnv(ctx, dir, testPkg, run, timeout, binFlags, "", "", env)
	return ran, passed, err
}

// TestProbeObservedEnv is TestProbe under a frozen environment with a
// runtime-input observation rooted at moduleDir and packageDir.
func TestProbeObservedEnv(ctx context.Context, dir, testPkg, run string, timeout time.Duration, binFlags []string, moduleDir, packageDir string, env []string) (ran int, passed bool, state runtimeinput.State, err error) {
	ran, passed, first, err := testProbeOnceObservedEnv(ctx, dir, testPkg, run, timeout, binFlags, moduleDir, packageDir, env)
	if err != nil {
		return ran, passed, first, err
	}
	if !passed {
		return ran, false, first, nil
	}
	if ran == 0 {
		return 0, true, first, nil
	}
	if !first.OK || first.Unverifiable {
		return ran, passed, first, err
	}
	empty, err := runtimeinput.MergeEnv(moduleDir, env)
	if err != nil {
		return 0, false, runtimeinput.State{}, err
	}
	if err := ctx.Err(); err != nil {
		return 0, false, runtimeinput.State{}, err
	}
	empty, err = runtimeinput.AbsoluteEnv(empty, moduleDir, env)
	if err != nil {
		return 0, false, runtimeinput.State{}, err
	}
	if err := ctx.Err(); err != nil {
		return 0, false, runtimeinput.State{}, err
	}
	if first == empty {
		return ran, passed, first, nil
	}
	secondRan, secondPassed, second, err := testProbeOnceObservedEnv(ctx, dir, testPkg, run, timeout, binFlags, moduleDir, packageDir, env)
	if err != nil {
		return secondRan, secondPassed, second, err
	}
	combined, err := mergeRuntimeEvidenceContext(ctx, dir, env, first, second)
	if err != nil {
		return 0, false, runtimeinput.State{}, err
	}
	if secondRan != ran {
		return secondRan, secondPassed, runtimeinput.State{}, fmt.Errorf("baseline test count changed between discovery and measurement")
	}
	if !secondPassed {
		return secondRan, false, runtimeinput.State{}, fmt.Errorf("baseline result changed between discovery and measurement")
	}
	if second.Unverifiable {
		return secondRan, secondPassed, combined, nil
	}
	if first != second {
		combined, err = addRuntimeEvidenceReasonContext(ctx, dir, env, combined, "runtime input observations changed between repeated baseline executions")
		if err != nil {
			return 0, false, runtimeinput.State{}, err
		}
	}
	return secondRan, secondPassed, combined, nil
}

func testProbeOnceObservedEnv(ctx context.Context, dir, testPkg, run string, timeout time.Duration, binFlags []string, moduleDir, packageDir string, env []string) (ran int, passed bool, state runtimeinput.State, err error) {
	ctx2, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// binFlags carries -rapid.nofailfile for rapid packages: a property that
	// fails on the clean baseline would otherwise write a reproducer into
	// the tree, the very invariant the runner protects (REQ-mut-overlay).
	args := goTestArgs(timeout, append([]string{"-count=1", "-run", run, testPkg}, binFlags...)...)
	capture := moduleDir != "" && packageDir != ""
	var testlog string
	if capture {
		tmp, err := os.MkdirTemp("", "gomutant-probe-*")
		if err != nil {
			return 0, false, runtimeinput.State{}, err
		}
		defer os.RemoveAll(tmp)
		testlog = filepath.Join(tmp, "baseline.testlog")
		args = append(args, "-test.testlogfile="+testlog)
	}
	cmd := commandContext(ctx2, "go", args...)
	cmd.Dir = dir
	cmd.Env = env
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	runErr := cmd.Run()
	if ctx2.Err() == context.DeadlineExceeded {
		state, observationErr := processObservationContext(ctx, testlog, moduleDir, packageDir, "baseline test process timed out", env, capture)
		if observationErr != nil {
			return 0, false, runtimeinput.State{}, observationErr
		}
		return 0, false, state, fmt.Errorf("baseline test timed out")
	}
	if err := ctx2.Err(); err != nil {
		state, observationErr := processObservationContext(ctx, testlog, moduleDir, packageDir, "baseline test process was cancelled", env, capture)
		if observationErr != nil {
			return 0, false, runtimeinput.State{}, observationErr
		}
		return 0, false, state, err
	}
	if strings.Contains(buf.String(), "[build failed]") {
		return 0, false, runtimeinput.State{}, fmt.Errorf("baseline test failed to build")
	}
	ran, err = countTopTests(buf.Bytes())
	if err != nil {
		return 0, false, runtimeinput.State{}, fmt.Errorf("parse baseline test output: %w", err)
	}
	state, err = processObservationContext(ctx, testlog, moduleDir, packageDir, "", env, capture)
	if err != nil {
		return 0, false, runtimeinput.State{}, err
	}
	return ran, runErr == nil, state, nil
}

func goTestArgs(timeout time.Duration, tail ...string) []string {
	testTimeout := timeout
	if timeout <= time.Duration(1<<63-1)-time.Second {
		testTimeout += time.Second
	}
	args := []string{"test", "-json", "-timeout", testTimeout.String()}
	return append(args, tail...)
}

// failedPackage scans a go test -json stream for a package-level fail event,
// returning the package or empty.
func failedPackage(stream []byte) string {
	type event struct {
		Action, Package, Test string
	}
	dec := json.NewDecoder(bytes.NewReader(stream))
	for dec.More() {
		var e event
		if dec.Decode(&e) != nil {
			return ""
		}
		if e.Action == "fail" && e.Test == "" && e.Package != "" {
			return e.Package
		}
	}
	return ""
}

// firstFailingTest scans a go test -json stream for the first test-level
// fail event, returning the failing test as "<pkg>.<TopLevelTest>" — the
// symbol form oracles pin. The subtest path is stripped HERE, where the Test
// field is unambiguous; in the joined form the first "/" lands inside the
// import path.
func firstFailingTest(stream []byte) (string, error) {
	type event struct {
		Action, Package, Test string
	}
	dec := json.NewDecoder(bytes.NewReader(stream))
	killer := ""
	for {
		var e event
		if err := dec.Decode(&e); err != nil {
			if err == io.EOF {
				return killer, nil
			}
			return "", err
		}
		if killer == "" && e.Action == "fail" && e.Test != "" {
			name := e.Test
			if i := strings.Index(name, "/"); i >= 0 {
				name = name[:i]
			}
			killer = e.Package + "." + name
		}
	}
}

func testProcessPanicked(stream []byte) bool {
	type event struct{ Output string }
	dec := json.NewDecoder(bytes.NewReader(stream))
	for dec.More() {
		var e event
		if dec.Decode(&e) != nil {
			return false
		}
		if strings.HasPrefix(strings.TrimSpace(e.Output), "panic:") {
			return true
		}
	}
	return false
}

func testFailureCompleted(stream []byte, failingTest string) bool {
	type event struct {
		Action  string
		Package string
		Test    string
		Output  string
	}
	dec := json.NewDecoder(bytes.NewReader(stream))
	active := map[string]bool{}
	marker := false
	for {
		var e event
		if err := dec.Decode(&e); err != nil {
			return err == io.EOF && marker && len(active) == 0
		}
		switch e.Action {
		case "run":
			if e.Test != "" {
				active[e.Test] = true
			}
		case "pass", "fail", "skip":
			if e.Test != "" {
				delete(active, e.Test)
			}
		}
		if e.Action != "output" || e.Test == "" {
			continue
		}
		name := e.Test
		if i := strings.Index(name, "/"); i >= 0 {
			name = name[:i]
		}
		expected := strings.TrimPrefix(failingTest, e.Package+".")
		if name == expected && strings.HasPrefix(strings.TrimSpace(e.Output), "--- FAIL: "+name) {
			marker = true
		}
	}
}

// countTopTests counts the distinct top-level tests (excluding subtests)
// that reported a pass or fail in a go test -json stream.
func countTopTests(stream []byte) (int, error) {
	type event struct{ Action, Test string }
	seen := map[string]bool{}
	dec := json.NewDecoder(bytes.NewReader(stream))
	for {
		var e event
		if err := dec.Decode(&e); err != nil {
			if err == io.EOF {
				return len(seen), nil
			}
			return 0, err
		}
		if e.Test == "" || strings.Contains(e.Test, "/") {
			continue
		}
		if e.Action == "pass" || e.Action == "fail" {
			seen[e.Test] = true
		}
	}
}

// tail returns the last n bytes of s, for error surfacing.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
