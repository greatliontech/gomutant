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
	"sync/atomic"
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

var observationSequence atomic.Uint64

func observationProcess(kind string) string {
	return fmt.Sprintf("gomutant-%s-%d", kind, observationSequence.Add(1))
}

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
	outcome, killer, _, _, err := runMutant(ctx, dir, m, testPkgs, runRegex, timeout, binFlags, "", "", GoEnv(dir))
	return outcome, killer, err
}

// RunMutantEnv is RunMutant under an already-frozen complete environment.
func RunMutantEnv(ctx context.Context, dir string, m Mutant, testPkgs []string, runRegex string, timeout time.Duration, binFlags, env []string) (MutantOutcome, string, error) {
	outcome, killer, _, _, err := runMutant(ctx, dir, m, testPkgs, runRegex, timeout, binFlags, "", "", env)
	return outcome, killer, err
}

// RunMutantObserved is RunMutant with finalized absolute runtime-input evidence
// for the test process and any differential baseline process it launches. The
// incomplete result names the candidate-local reason when the mutant's own
// test process could not prove its runtime-input log complete — a timeout,
// panic, exit before harness completion, compile rejection, or missing log —
// and is empty otherwise; that incompleteness attaches to the measured
// candidate alone, while content-unverifiable or disagreeing COMPLETED
// observations stay finding-wide (REQ-exec-observation).
func RunMutantObserved(ctx context.Context, dir string, m Mutant, testPkgs []string, runRegex string, timeout time.Duration, binFlags []string, moduleDir, packageDir string) (MutantOutcome, string, runtimeinput.Observation, string, error) {
	return runMutant(ctx, dir, m, testPkgs, runRegex, timeout, binFlags, moduleDir, packageDir, GoEnv(dir))
}

// RunMutantObservedEnv is RunMutantObserved under an already-frozen complete
// environment.
func RunMutantObservedEnv(ctx context.Context, dir string, m Mutant, testPkgs []string, runRegex string, timeout time.Duration, binFlags []string, moduleDir, packageDir string, env []string) (MutantOutcome, string, runtimeinput.Observation, string, error) {
	return runMutant(ctx, dir, m, testPkgs, runRegex, timeout, binFlags, moduleDir, packageDir, env)
}

func runMutant(ctx context.Context, dir string, m Mutant, testPkgs []string, runRegex string, timeout time.Duration, binFlags []string, moduleDir, packageDir string, env []string) (MutantOutcome, string, runtimeinput.Observation, string, error) {
	firstOutcome, firstKiller, firstState, firstIncomplete, err := runMutantOnce(ctx, dir, m, testPkgs, runRegex, timeout, binFlags, moduleDir, packageDir, env)
	if err != nil || !firstState.OK || firstState.Unverifiable {
		return firstOutcome, firstKiller, firstState, firstIncomplete, err
	}
	empty, err := runtimeinput.MergeEnv(moduleDir, env)
	if err != nil {
		return MutantDiscarded, "", runtimeinput.Observation{}, "", err
	}
	if err := ctx.Err(); err != nil {
		return MutantDiscarded, "", runtimeinput.Observation{}, "", err
	}
	empty, err = runtimeinput.AbsoluteEnv(empty, moduleDir, env)
	if err != nil {
		return MutantDiscarded, "", runtimeinput.Observation{}, "", err
	}
	if err := ctx.Err(); err != nil {
		return MutantDiscarded, "", runtimeinput.Observation{}, "", err
	}
	if firstState.State == empty.State {
		return firstOutcome, firstKiller, firstState, "", nil
	}

	// The first run discovers runtime identities. The second is the scored
	// measurement; disagreement between the two COMPLETED observations makes
	// the evidence non-reusable without suppressing the attributed outcome,
	// and stays finding-wide — incoherence among completed states is never
	// candidate-local (REQ-exec-observation).
	secondOutcome, secondKiller, secondState, secondIncomplete, err := runMutantOnce(ctx, dir, m, testPkgs, runRegex, timeout, binFlags, moduleDir, packageDir, env)
	if err != nil {
		return secondOutcome, secondKiller, secondState, secondIncomplete, err
	}
	combined, err := mergeRuntimeEvidenceContext(ctx, dir, env, firstState, secondState)
	if err != nil {
		return MutantDiscarded, "", runtimeinput.Observation{}, "", err
	}
	if secondState.Unverifiable {
		return secondOutcome, secondKiller, combined, secondIncomplete, nil
	}
	if firstState.State != secondState.State {
		combined, err = addRuntimeEvidenceReasonContext(ctx, dir, env, combined, "runtime input observations changed between repeated mutant executions")
		if err != nil {
			return MutantDiscarded, "", runtimeinput.Observation{}, "", err
		}
	}
	return secondOutcome, secondKiller, combined, "", nil
}

func runMutantOnce(ctx context.Context, dir string, m Mutant, testPkgs []string, runRegex string, timeout time.Duration, binFlags []string, moduleDir, packageDir string, env []string) (MutantOutcome, string, runtimeinput.Observation, string, error) {
	if err := ctx.Err(); err != nil {
		return MutantDiscarded, "", runtimeinput.Observation{}, "", err
	}
	capture := moduleDir != "" && packageDir != ""
	tmp, err := os.MkdirTemp("", "gomutant-*")
	if err != nil {
		return MutantDiscarded, "", runtimeinput.Observation{}, "", err
	}
	defer os.RemoveAll(tmp)
	if len(m.Replacements) == 0 {
		return MutantDiscarded, "", runtimeinput.Observation{}, "", fmt.Errorf("mutant has no file replacements")
	}
	replace := make(map[string]string, len(m.Replacements))
	for i, replacement := range m.Replacements {
		if err := ctx.Err(); err != nil {
			return MutantDiscarded, "", runtimeinput.Observation{}, "", err
		}
		if replacement.File == "" || replacement.Source == nil {
			return MutantDiscarded, "", runtimeinput.Observation{}, "", fmt.Errorf("mutant replacement %d is incomplete", i+1)
		}
		if _, duplicate := replace[replacement.File]; duplicate {
			return MutantDiscarded, "", runtimeinput.Observation{}, "", fmt.Errorf("mutant replaces %s more than once", replacement.File)
		}
		mutFile := filepath.Join(tmp, fmt.Sprintf("replacement-%d%s", i, filepath.Ext(replacement.File)))
		if err := contextio.WriteFile(ctx, mutFile, replacement.Source, 0o644); err != nil {
			return MutantDiscarded, "", runtimeinput.Observation{}, "", err
		}
		if err := ctx.Err(); err != nil {
			return MutantDiscarded, "", runtimeinput.Observation{}, "", err
		}
		replace[replacement.File] = mutFile
	}
	overlay := filepath.Join(tmp, "overlay.json")
	oj, err := json.Marshal(struct {
		Replace map[string]string
	}{Replace: replace})
	if err != nil {
		return MutantDiscarded, "", runtimeinput.Observation{}, "", err
	}
	if err := ctx.Err(); err != nil {
		return MutantDiscarded, "", runtimeinput.Observation{}, "", err
	}
	if err := contextio.WriteFile(ctx, overlay, oj, 0o644); err != nil {
		return MutantDiscarded, "", runtimeinput.Observation{}, "", err
	}
	if err := ctx.Err(); err != nil {
		return MutantDiscarded, "", runtimeinput.Observation{}, "", err
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
		state, incomplete, err := processObservationContext(ctx, testlog, moduleDir, packageDir, "mutant test process timed out", env, capture)
		return MutantKilled, TimeoutKiller, state, incomplete, err
	}
	if runCtx.Err() != nil {
		state, incomplete, observationErr := processObservationContext(ctx, testlog, moduleDir, packageDir, "mutant test process was cancelled", env, capture)
		if observationErr != nil {
			return MutantDiscarded, "", runtimeinput.Observation{}, "", observationErr
		}
		return MutantDiscarded, "", state, incomplete, ctx.Err()
	}
	killer, parseErr := firstFailingTest(stdout.Bytes())
	if parseErr != nil {
		state, incomplete, observationErr := processObservationContext(ctx, testlog, moduleDir, packageDir, "go test output was malformed before observation finalization", env, capture)
		if observationErr != nil {
			return MutantDiscarded, "", runtimeinput.Observation{}, "", observationErr
		}
		return MutantDiscarded, "", state, incomplete, fmt.Errorf("parse go test output: %w", parseErr)
	}
	switch {
	case runErr == nil:
		state, incomplete, err := processObservationContext(ctx, testlog, moduleDir, packageDir, "", env, capture)
		return MutantSurvived, "", state, incomplete, err
	case strings.Contains(stdout.String(), "[build failed]"):
		state, incomplete, err := processObservationContext(ctx, testlog, moduleDir, packageDir, "mutant test process did not start because the mutant failed to build", env, capture)
		return MutantDiscarded, "", state, incomplete, err
	case killer != "":
		reason := ""
		if testProcessPanicked(stdout.Bytes()) || !testFailureCompleted(stdout.Bytes(), killer) {
			reason = "mutant test process panicked before observation finalization"
			if !testProcessPanicked(stdout.Bytes()) {
				reason = "mutant test process exited before observation finalization"
			}
		}
		state, incomplete, err := processObservationContext(ctx, testlog, moduleDir, packageDir, reason, env, capture)
		return MutantKilled, killer, state, incomplete, err
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
		mutantState, mutantIncomplete, err := processObservationContext(ctx, testlog, moduleDir, packageDir, "mutant test process exited before observation finalization", env, capture)
		if err != nil {
			return MutantDiscarded, "", runtimeinput.Observation{}, "", err
		}
		if baseCtx.Err() != nil {
			baselineState, _, observationErr := processObservationContext(ctx, baseTestlog, moduleDir, packageDir, "baseline test process did not complete", env, capture)
			if observationErr != nil {
				return MutantDiscarded, "", runtimeinput.Observation{}, "", observationErr
			}
			state, mergeErr := mergeProcessObservationsContext(ctx, dir, env, capture, mutantState, baselineState)
			if mergeErr != nil {
				return MutantDiscarded, "", runtimeinput.Observation{}, "", mergeErr
			}
			return MutantDiscarded, "", state, mutantIncomplete, baseCtx.Err()
		}
		if baseErr == nil {
			baselineState, _, err := processObservationContext(ctx, baseTestlog, moduleDir, packageDir, "", env, capture)
			if err != nil {
				return MutantDiscarded, "", runtimeinput.Observation{}, "", err
			}
			state, err := mergeProcessObservationsContext(ctx, dir, env, capture, mutantState, baselineState)
			return MutantKilled, PackageKillerPrefix + pkg + ")", state, mutantIncomplete, err
		}
		baselineState, _, observationErr := processObservationContext(ctx, baseTestlog, moduleDir, packageDir, "baseline test process failed before observation finalization", env, capture)
		if observationErr != nil {
			return MutantDiscarded, "", runtimeinput.Observation{}, "", observationErr
		}
		state, mergeErr := mergeProcessObservationsContext(ctx, dir, env, capture, mutantState, baselineState)
		if mergeErr != nil {
			return MutantDiscarded, "", runtimeinput.Observation{}, "", mergeErr
		}
		return MutantDiscarded, "", state, mutantIncomplete, fmt.Errorf("mutant run failed with no test-attributed kill (environmental noise, not a kill; baseline probe did not clear it): %v: %s", runErr, tail(stderr.String()+stdout.String(), 400))
	}
	state, incomplete, observationErr := processObservationContext(ctx, testlog, moduleDir, packageDir, "mutant test process failed before attributable completion", env, capture)
	if observationErr != nil {
		return MutantDiscarded, "", runtimeinput.Observation{}, "", observationErr
	}
	return MutantDiscarded, "", state, incomplete, fmt.Errorf("mutant run failed with no test-attributed kill (environmental noise, not a kill; baseline probe did not clear it): %v: %s", runErr, tail(stderr.String()+stdout.String(), 400))
}

func processObservation(path, moduleDir, packageDir, incompleteReason string, env []string, capture bool) (runtimeinput.Observation, string, error) {
	return processObservationContext(context.Background(), path, moduleDir, packageDir, incompleteReason, env, capture)
}

// processObservationContext finalizes one launched test process's runtime-input
// observation. The returned reason is the process's effective incompleteness —
// the caller's incompleteReason, or the missing-log reason discovered here —
// and is empty exactly when the process proved its log complete; a completed
// observation that later fails absolute finalization keeps an empty reason
// because that is input movement, which stays finding-wide
// (REQ-exec-observation).
func processObservationContext(ctx context.Context, path, moduleDir, packageDir, incompleteReason string, env []string, capture bool) (runtimeinput.Observation, string, error) {
	if err := ctx.Err(); err != nil {
		return runtimeinput.Observation{}, "", err
	}
	if !capture {
		return runtimeinput.Observation{}, "", nil
	}
	var observation runtimeinput.Observation
	var err error
	if incompleteReason != "" {
		observation, err = runtimeinput.IncompleteEnv(moduleDir, path, incompleteReason, env)
	} else {
		data, readErr := contextio.ReadFile(ctx, path)
		if os.IsNotExist(readErr) {
			incompleteReason = "test process produced no runtime-input log"
			observation, err = runtimeinput.IncompleteEnv(moduleDir, path, incompleteReason, env)
		} else if readErr != nil {
			return runtimeinput.Observation{}, "", readErr
		} else {
			observation, err = runtimeinput.FromTestLogEnv(data, moduleDir, packageDir, env, runtimeinput.WithCompletedProcess(path))
		}
	}
	if err != nil {
		return runtimeinput.Observation{}, "", err
	}
	if err := ctx.Err(); err != nil {
		return runtimeinput.Observation{}, "", err
	}
	observation, err = absoluteRuntimeEvidenceContext(ctx, observation, moduleDir, env)
	return observation, incompleteReason, err
}

func absoluteRuntimeEvidence(observation runtimeinput.Observation, moduleDir string, env []string) (runtimeinput.Observation, error) {
	return absoluteRuntimeEvidenceContext(context.Background(), observation, moduleDir, env)
}

func absoluteRuntimeEvidenceContext(ctx context.Context, observation runtimeinput.Observation, moduleDir string, env []string) (runtimeinput.Observation, error) {
	if err := ctx.Err(); err != nil {
		return runtimeinput.Observation{}, err
	}
	if _, err := runtimeinput.CompletedState(observation); err != nil {
		return runtimeinput.Observation{}, err
	}
	absolute, err := runtimeinput.AbsoluteEnv(observation, moduleDir, env)
	if cancelErr := ctx.Err(); cancelErr != nil {
		return runtimeinput.Observation{}, cancelErr
	}
	if err == nil {
		return absolute, nil
	}
	if !observation.OK || observation.Manifest == "" || observation.Digest == "" {
		return runtimeinput.Observation{}, err
	}
	incomplete, incompleteErr := runtimeinput.IncompleteEnv(moduleDir, observationProcess("absolute"), "runtime input observation could not be finalized for reuse: "+err.Error(), env)
	if incompleteErr != nil {
		return runtimeinput.Observation{}, incompleteErr
	}
	return absoluteNonReusableRuntimeEvidence(ctx, incomplete, moduleDir, env)
}

func absoluteNonReusableRuntimeEvidence(ctx context.Context, incomplete runtimeinput.Observation, moduleDir string, env []string) (runtimeinput.Observation, error) {
	absolute, err := runtimeinput.AbsoluteEnv(incomplete, moduleDir, env)
	if cancelErr := ctx.Err(); cancelErr != nil {
		return runtimeinput.Observation{}, cancelErr
	}
	if err == nil {
		return absolute, nil
	}
	// Once movement is proven, reuse is forbidden. If a preserved path moves
	// again during conversion, retain the reason without requiring that path
	// to stabilize merely to publish the fresh mutation outcome.
	incomplete, incompleteErr := runtimeinput.IncompleteEnv(moduleDir, observationProcess("absolute"), "runtime input observation could not be finalized for reuse: "+err.Error(), env)
	if incompleteErr != nil {
		return runtimeinput.Observation{}, incompleteErr
	}
	absolute, err = runtimeinput.AbsoluteEnv(incomplete, moduleDir, env)
	if cancelErr := ctx.Err(); cancelErr != nil {
		return runtimeinput.Observation{}, cancelErr
	}
	return absolute, err
}

func mergeProcessObservations(root string, env []string, capture bool, states ...runtimeinput.Observation) (runtimeinput.Observation, error) {
	return mergeProcessObservationsContext(context.Background(), root, env, capture, states...)
}

func mergeProcessObservationsContext(ctx context.Context, root string, env []string, capture bool, states ...runtimeinput.Observation) (runtimeinput.Observation, error) {
	if !capture {
		return runtimeinput.Observation{}, nil
	}
	return mergeRuntimeEvidenceContext(ctx, root, env, states...)
}

func mergeRuntimeEvidence(root string, env []string, states ...runtimeinput.Observation) (runtimeinput.Observation, error) {
	return mergeRuntimeEvidenceContext(context.Background(), root, env, states...)
}

func mergeRuntimeEvidenceContext(ctx context.Context, root string, env []string, states ...runtimeinput.Observation) (runtimeinput.Observation, error) {
	if err := ctx.Err(); err != nil {
		return runtimeinput.Observation{}, err
	}
	state, err := runtimeinput.MergeEnv(root, env, states...)
	if cancelErr := ctx.Err(); cancelErr != nil {
		return runtimeinput.Observation{}, cancelErr
	}
	if err == nil {
		return state, nil
	}
	result, incompleteErr := runtimeinput.IncompleteEnv(root, observationProcess("merge"), "runtime input observations could not be merged for reuse: "+err.Error(), env)
	if incompleteErr != nil {
		return runtimeinput.Observation{}, incompleteErr
	}
	for _, input := range states {
		if err := ctx.Err(); err != nil {
			return runtimeinput.Observation{}, err
		}
		if input.Manifest == "" {
			continue
		}
		merged, mergeErr := runtimeinput.MergeEnv(root, env, result, input)
		if err := ctx.Err(); err != nil {
			return runtimeinput.Observation{}, err
		}
		if mergeErr == nil {
			result = merged
		}
	}
	return result, nil
}

func addRuntimeEvidenceReason(root string, env []string, state runtimeinput.Observation, reason string) (runtimeinput.Observation, error) {
	return addRuntimeEvidenceReasonContext(context.Background(), root, env, state, reason)
}

func addRuntimeEvidenceReasonContext(ctx context.Context, root string, env []string, state runtimeinput.Observation, reason string) (runtimeinput.Observation, error) {
	incomplete, err := runtimeinput.IncompleteEnv(root, observationProcess("disagreement"), reason, env)
	if err != nil {
		return runtimeinput.Observation{}, err
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
func TestProbeObservedEnv(ctx context.Context, dir, testPkg, run string, timeout time.Duration, binFlags []string, moduleDir, packageDir string, env []string) (ran int, passed bool, state runtimeinput.Observation, err error) {
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
		return 0, false, runtimeinput.Observation{}, err
	}
	if err := ctx.Err(); err != nil {
		return 0, false, runtimeinput.Observation{}, err
	}
	empty, err = runtimeinput.AbsoluteEnv(empty, moduleDir, env)
	if err != nil {
		return 0, false, runtimeinput.Observation{}, err
	}
	if err := ctx.Err(); err != nil {
		return 0, false, runtimeinput.Observation{}, err
	}
	if first.State == empty.State {
		return ran, passed, first, nil
	}
	secondRan, secondPassed, second, err := testProbeOnceObservedEnv(ctx, dir, testPkg, run, timeout, binFlags, moduleDir, packageDir, env)
	if err != nil {
		return secondRan, secondPassed, second, err
	}
	combined, err := mergeRuntimeEvidenceContext(ctx, dir, env, first, second)
	if err != nil {
		return 0, false, runtimeinput.Observation{}, err
	}
	if secondRan != ran {
		return secondRan, secondPassed, runtimeinput.Observation{}, fmt.Errorf("baseline test count changed between discovery and measurement")
	}
	if !secondPassed {
		return secondRan, false, runtimeinput.Observation{}, fmt.Errorf("baseline result changed between discovery and measurement")
	}
	if second.Unverifiable {
		return secondRan, secondPassed, combined, nil
	}
	if first.State != second.State {
		combined, err = addRuntimeEvidenceReasonContext(ctx, dir, env, combined, "runtime input observations changed between repeated baseline executions")
		if err != nil {
			return 0, false, runtimeinput.Observation{}, err
		}
	}
	return secondRan, secondPassed, combined, nil
}

func testProbeOnceObservedEnv(ctx context.Context, dir, testPkg, run string, timeout time.Duration, binFlags []string, moduleDir, packageDir string, env []string) (ran int, passed bool, state runtimeinput.Observation, err error) {
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
			return 0, false, runtimeinput.Observation{}, err
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
		state, _, observationErr := processObservationContext(ctx, testlog, moduleDir, packageDir, "baseline test process timed out", env, capture)
		if observationErr != nil {
			return 0, false, runtimeinput.Observation{}, observationErr
		}
		return 0, false, state, fmt.Errorf("baseline test timed out")
	}
	if err := ctx2.Err(); err != nil {
		state, _, observationErr := processObservationContext(ctx, testlog, moduleDir, packageDir, "baseline test process was cancelled", env, capture)
		if observationErr != nil {
			return 0, false, runtimeinput.Observation{}, observationErr
		}
		return 0, false, state, err
	}
	if strings.Contains(buf.String(), "[build failed]") {
		return 0, false, runtimeinput.Observation{}, fmt.Errorf("baseline test failed to build")
	}
	ran, err = countTopTests(buf.Bytes())
	if err != nil {
		return 0, false, runtimeinput.Observation{}, fmt.Errorf("parse baseline test output: %w", err)
	}
	state, _, err = processObservationContext(ctx, testlog, moduleDir, packageDir, "", env, capture)
	if err != nil {
		return 0, false, runtimeinput.Observation{}, err
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
