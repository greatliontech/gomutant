package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	iofs "io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

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

// gopterPkg is the property runtime gomutant recognizes but cannot pin:
// gopter carries no invocation-level seed flag, so determinism is the
// suite's own responsibility (REQ-exec-property-oracles).
const gopterPkg = "github.com/leanovate/gopter"

// propertyRuntimeNames maps recognized property-runtime import paths to
// their short names for prerequisite statements.
var propertyRuntimeNames = map[string]string{rapidPkg: "rapid", gopterPkg: "gopter"}

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

// PropertyRuntimesContext maps each named test package to the recognized
// property runtimes its package (test variants included) imports,
// sorted - the preflight input for property-oracle prerequisites
// (REQ-exec-property-oracles). A mixed package carries every detected
// runtime, so each earns its own statement; packages importing none
// are absent.
func (t *Tree) PropertyRuntimesContext(ctx context.Context, testPkgs []string) (map[string][]string, error) {
	byPath := map[string]map[string]bool{}
	for _, pkg := range t.pkgs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, f := range pkg.Syntax {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			for _, imp := range f.Imports {
				if name := propertyRuntimeNames[strings.Trim(imp.Path.Value, `"`)]; name != "" {
					if byPath[pkg.PkgPath] == nil {
						byPath[pkg.PkgPath] = map[string]bool{}
					}
					byPath[pkg.PkgPath][name] = true
				}
			}
		}
	}
	runtimes := map[string][]string{}
	for _, p := range testPkgs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		names := map[string]bool{}
		for name := range byPath[p] {
			names[name] = true
		}
		for name := range byPath[p+"_test"] {
			names[name] = true
		}
		if len(names) == 0 {
			continue
		}
		sorted := make([]string, 0, len(names))
		for name := range names {
			sorted = append(sorted, name)
		}
		sort.Strings(sorted)
		runtimes[p] = sorted
	}
	return runtimes, nil
}

// PropertyOracleBinFlags is the rapid invocation pinning shared by the
// campaign and ephemeral probes: reproducer files suppressed and draws
// pinned, so every mutant faces the same draw sequence and a verdict is
// reproducible (REQ-exec-property-oracles).
func PropertyOracleBinFlags() []string {
	return []string{"-rapid.nofailfile", "-rapid.seed=1"}
}

// PropertyRegimeRapid is the recorded measurement regime of a finding
// whose oracle ran under the rapid pinning; the empty regime is a
// property-runtime-free oracle. The regime is a measurement pin: a
// record measured under other draws must not serve as if reproducible
// under this one (REQ-exec-property-oracles, REQ-result-stale).
const PropertyRegimeRapid = "rapid:nofailfile,seed=1"

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
//
// The diagnostic return carries the compiler's own text when the mutant
// failed to build, empty otherwise, so a manual-probe refusal can name
// the reason instead of leaving the caller to guess (REQ-exec-ephemeral).
// Ambient-environment convenience: selection-bearing paths use the Env
// form with the tree's frozen environment.
func RunMutant(ctx context.Context, dir string, m Mutant, testPkgs []string, runRegex string, timeout time.Duration, binFlags []string) (MutantOutcome, string, string, error) {
	outcome, killer, _, _, diagnostic, err := runMutant(ctx, dir, m, testPkgs, runRegex, timeout, binFlags, "", "", nil, nil, GoEnv(dir))
	return outcome, killer, diagnostic, err
}

// RunMutantEnv is RunMutant under an already-frozen complete environment.
func RunMutantEnv(ctx context.Context, dir string, m Mutant, testPkgs []string, runRegex string, timeout time.Duration, binFlags, env []string) (MutantOutcome, string, string, error) {
	outcome, killer, _, _, diagnostic, err := runMutant(ctx, dir, m, testPkgs, runRegex, timeout, binFlags, "", "", nil, nil, env)
	return outcome, killer, diagnostic, err
}

// RunMutantEvidenceEnv is RunMutantEnv additionally deriving the kill's
// interactive evidence from the mutant run's own -json stream: the
// killing test's bounded output head, the timeout verdict naming the
// governing oracle_timeout_sec, or the package-scope crash's bounded
// text - so acting on a kill needs no parallel oracle re-run
// (REQ-exec-ephemeral). The evidence is empty when the mutant survived
// or was discarded.
func RunMutantEvidenceEnv(ctx context.Context, dir string, m Mutant, testPkgs []string, runRegex string, timeout time.Duration, binFlags, env []string) (MutantOutcome, string, string, string, error) {
	var sink bytes.Buffer
	outcome, killer, _, _, diagnostic, err := runMutantOnce(ctx, dir, m, testPkgs, runRegex, timeout, binFlags, "", "", nil, nil, env, &sink)
	evidence := ""
	if err == nil && outcome == MutantKilled {
		evidence = killEvidence(sink.Bytes(), killer, timeout)
	}
	return outcome, killer, evidence, diagnostic, err
}

// killerOutputLines and killerOutputLineCap bound the kill evidence:
// enough of the killing test's own output to act on, never the whole
// stream.
const (
	killerOutputLines   = 20
	killerOutputLineCap = 240
)

// killEvidence derives a kill's bounded evidence text from the mutant
// run's -json stream by verdict shape: the killing test's output head,
// the timeout naming its governing option, or the package-scope
// crash's bounded tail.
func killEvidence(stream []byte, killer string, timeout time.Duration) string {
	switch {
	case killer == TimeoutKiller:
		return fmt.Sprintf("oracle timed out after %s - the oracle timeout governs this bound (oracle_timeout_sec / --oracle-timeout)", timeout)
	case strings.HasPrefix(killer, PackageKillerPrefix):
		if excerpt := outputTail(stream, func(pkg, test string) bool { return test == "" }); excerpt != "" {
			return excerpt
		}
		return tail(string(stream), 400)
	default:
		return outputTail(stream, func(pkg, test string) bool {
			return pkg+"."+test == killer
		})
	}
}

// outputTail collects the last killerOutputLines non-empty output
// lines the keep predicate admits from a go test -json stream (subtest
// output folds to its top-level test), each line capped at a rune
// boundary, the dropped earlier remainder counted - a truncation is
// never silent. The excerpt anchors at the END because Go emits the
// failure block (--- FAIL, assertion text, property counterexamples)
// last: a head would bury the failure reason under run banners and
// force exactly the parallel re-run the evidence exists to remove.
func outputTail(stream []byte, keep func(pkg, test string) bool) string {
	type event struct {
		Action  string
		Package string
		Test    string
		Output  string
	}
	ring := make([]string, 0, killerOutputLines)
	next := 0
	dropped := 0
	dec := json.NewDecoder(bytes.NewReader(stream))
	for {
		var e event
		if err := dec.Decode(&e); err != nil {
			break
		}
		if e.Action != "output" {
			continue
		}
		name := e.Test
		if i := strings.Index(name, "/"); i >= 0 {
			name = name[:i]
		}
		if !keep(e.Package, name) {
			continue
		}
		line := strings.TrimRight(e.Output, "\n")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(line) > killerOutputLineCap {
			cut := killerOutputLineCap
			for cut > 0 && !utf8.RuneStart(line[cut]) {
				cut--
			}
			line = line[:cut] + "..."
		}
		if len(ring) < killerOutputLines {
			ring = append(ring, line)
			continue
		}
		ring[next] = line
		next = (next + 1) % killerOutputLines
		dropped++
	}
	lines := append(append([]string(nil), ring[next:]...), ring[:next]...)
	if dropped > 0 {
		lines = append([]string{fmt.Sprintf("(%d earlier output lines dropped)", dropped)}, lines...)
	}
	return strings.Join(lines, "\n")
}

// RunMutantObserved is RunMutant with finalized absolute runtime-input evidence
// for the test process and any differential baseline process it launches. The
// incomplete result names the candidate-local reason when the mutant's own
// test process could not prove its runtime-input log complete — a timeout,
// panic, exit before harness completion, compile rejection, or missing log —
// and is empty otherwise; that incompleteness attaches to the measured
// candidate alone, while content-unverifiable or disagreeing COMPLETED
// observations stay finding-wide (REQ-exec-observation). The diagnostic
// return carries the compiler's own text exactly when the mutant failed to
// build — the caller's signal that no test process started, so the run has
// no runtime exposure at all.
func RunMutantObserved(ctx context.Context, dir string, m Mutant, testPkgs []string, runRegex string, timeout time.Duration, binFlags []string, moduleDir, packageDir string, bracketPaths []string, namespaces []runtimeinput.ScratchNamespace) (MutantOutcome, string, runtimeinput.Observation, string, string, error) {
	return runMutant(ctx, dir, m, testPkgs, runRegex, timeout, binFlags, moduleDir, packageDir, bracketPaths, namespaces, GoEnv(dir))
}

// RunMutantObservedEnv is RunMutantObserved under an already-frozen complete
// environment.
func RunMutantObservedEnv(ctx context.Context, dir string, m Mutant, testPkgs []string, runRegex string, timeout time.Duration, binFlags []string, moduleDir, packageDir string, bracketPaths []string, namespaces []runtimeinput.ScratchNamespace, env []string) (MutantOutcome, string, runtimeinput.Observation, string, string, error) {
	return runMutant(ctx, dir, m, testPkgs, runRegex, timeout, binFlags, moduleDir, packageDir, bracketPaths, namespaces, env)
}

// runMutant executes each mutant exactly once: the pre-spawn observation
// bracket vouches the values the run read, so the historical
// discovery-then-score double execution and its evidence-drift
// comparison are retired - bracket verdicts are the truth
// (REQ-exec-observation).
func runMutant(ctx context.Context, dir string, m Mutant, testPkgs []string, runRegex string, timeout time.Duration, binFlags []string, moduleDir, packageDir string, bracketPaths []string, namespaces []runtimeinput.ScratchNamespace, env []string) (MutantOutcome, string, runtimeinput.Observation, string, string, error) {
	return runMutantOnce(ctx, dir, m, testPkgs, runRegex, timeout, binFlags, moduleDir, packageDir, bracketPaths, namespaces, env, nil)
}

func runMutantOnce(ctx context.Context, dir string, m Mutant, testPkgs []string, runRegex string, timeout time.Duration, binFlags []string, moduleDir, packageDir string, bracketPaths []string, namespaces []runtimeinput.ScratchNamespace, env []string, sink *bytes.Buffer) (MutantOutcome, string, runtimeinput.Observation, string, string, error) {
	if err := ctx.Err(); err != nil {
		return MutantDiscarded, "", runtimeinput.Observation{}, "", "", err
	}
	capture := moduleDir != "" && packageDir != ""
	if capture && len(testPkgs) > 1 {
		// One run, one testlog: each sequential test binary truncates
		// the shared capture file, so a multi-package observed run
		// would ingest only the last binary's reads as a completed
		// observation silently covering one package - refused instead.
		// Campaign groups are single-package; this guards the exported
		// engine API (REQ-exec-observation).
		return MutantDiscarded, "", runtimeinput.Observation{}, "", "", fmt.Errorf("observed mutant runs cover one test package per process: %d packages requested with observation enabled; run per package and merge the observations", len(testPkgs))
	}
	tmp, err := os.MkdirTemp("", "gomutant-*")
	if err != nil {
		return MutantDiscarded, "", runtimeinput.Observation{}, "", "", err
	}
	defer os.RemoveAll(tmp)
	scratchEnv, scratchRoot, sweepScratch, removeScratch, err := oracleScratch(env)
	if err != nil {
		return MutantDiscarded, "", runtimeinput.Observation{}, "", "", err
	}
	defer removeScratch()
	if len(m.Replacements) == 0 {
		return MutantDiscarded, "", runtimeinput.Observation{}, "", "", fmt.Errorf("mutant has no file replacements")
	}
	replace := make(map[string]string, len(m.Replacements))
	for i, replacement := range m.Replacements {
		if err := ctx.Err(); err != nil {
			return MutantDiscarded, "", runtimeinput.Observation{}, "", "", err
		}
		if replacement.File == "" || replacement.Source == nil {
			return MutantDiscarded, "", runtimeinput.Observation{}, "", "", fmt.Errorf("mutant replacement %d is incomplete", i+1)
		}
		if _, duplicate := replace[replacement.File]; duplicate {
			return MutantDiscarded, "", runtimeinput.Observation{}, "", "", fmt.Errorf("mutant replaces %s more than once", replacement.File)
		}
		mutFile := filepath.Join(tmp, fmt.Sprintf("replacement-%d%s", i, filepath.Ext(replacement.File)))
		if err := contextio.WriteFile(ctx, mutFile, replacement.Source, 0o644); err != nil {
			return MutantDiscarded, "", runtimeinput.Observation{}, "", "", err
		}
		if err := ctx.Err(); err != nil {
			return MutantDiscarded, "", runtimeinput.Observation{}, "", "", err
		}
		replace[replacement.File] = mutFile
	}
	overlay := filepath.Join(tmp, "overlay.json")
	oj, err := json.Marshal(struct {
		Replace map[string]string
	}{Replace: replace})
	if err != nil {
		return MutantDiscarded, "", runtimeinput.Observation{}, "", "", err
	}
	if err := ctx.Err(); err != nil {
		return MutantDiscarded, "", runtimeinput.Observation{}, "", "", err
	}
	if err := contextio.WriteFile(ctx, overlay, oj, 0o644); err != nil {
		return MutantDiscarded, "", runtimeinput.Observation{}, "", "", err
	}
	if err := ctx.Err(); err != nil {
		return MutantDiscarded, "", runtimeinput.Observation{}, "", "", err
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
	var oracleFrame runtimeinput.ProducerFrame
	if capture {
		oracleFrame = captureOracleFrame(ctx, moduleDir, packageDir, bracketPaths)
	}
	cmd := commandContext(runCtx, "go", args...)
	cmd.Dir = dir
	cmd.Env = oracleEnv(scratchEnv)
	// The sink, when given, receives the mutant run's raw -json stream -
	// the evidence surface RunMutantEvidenceEnv derives kill output from.
	stdout := &bytes.Buffer{}
	if sink != nil {
		stdout = sink
	}
	var stderr bytes.Buffer
	cmd.Stdout = stdout
	cmd.Stderr = &stderr
	runErr := runOracleProcess(cmd)
	// The sweep precedes observation finalization: the evidence union
	// merges this observation with the probes' under revalidation, and
	// a content digest of a swept file would read moved - the input's
	// paths dropped and the union stamped unverifiable.
	sweepScratch()

	if runCtx.Err() == context.DeadlineExceeded {
		state, incomplete, err := processObservationContext(ctx, testlog, moduleDir, "mutant test process timed out", env, scratchRoot, capture, oracleFrame, namespaces)
		return MutantKilled, TimeoutKiller, state, incomplete, "", err
	}
	if runCtx.Err() != nil {
		state, incomplete, observationErr := processObservationContext(ctx, testlog, moduleDir, "mutant test process was cancelled", env, scratchRoot, capture, oracleFrame, namespaces)
		if observationErr != nil {
			return MutantDiscarded, "", runtimeinput.Observation{}, "", "", observationErr
		}
		return MutantDiscarded, "", state, incomplete, "", ctx.Err()
	}
	killer, parseErr := firstFailingTest(stdout.Bytes())
	if parseErr != nil {
		state, incomplete, observationErr := processObservationContext(ctx, testlog, moduleDir, "go test output was malformed before observation finalization", env, scratchRoot, capture, oracleFrame, namespaces)
		if observationErr != nil {
			return MutantDiscarded, "", runtimeinput.Observation{}, "", "", observationErr
		}
		return MutantDiscarded, "", state, incomplete, "", fmt.Errorf("parse go test output: %w", parseErr)
	}
	switch {
	case runErr == nil:
		state, incomplete, err := processObservationContext(ctx, testlog, moduleDir, "", env, scratchRoot, capture, oracleFrame, namespaces)
		return MutantSurvived, "", state, incomplete, "", err
	case buildRejected(stdout.Bytes()):
		// The harness itself reported the failed build: no test process
		// started, so there is no observation to finalize and no
		// incomplete-process evidence to carry — the discard is a pure
		// function of the mutant source under the toolchain and
		// build-configuration pins (REQ-result-stale). The diagnostic
		// carries the compiler's text for interactive surfaces.
		return MutantDiscarded, "", runtimeinput.Observation{}, "", compileDiagnostics(stdout.Bytes(), stderr.Bytes()), nil
	case killer != "":
		reason := ""
		if testProcessPanicked(stdout.Bytes()) || !testFailureCompleted(stdout.Bytes(), killer) {
			reason = "mutant test process panicked before observation finalization"
			if !testProcessPanicked(stdout.Bytes()) {
				reason = "mutant test process exited before observation finalization"
			}
		}
		state, incomplete, err := processObservationContext(ctx, testlog, moduleDir, reason, env, scratchRoot, capture, oracleFrame, namespaces)
		return MutantKilled, killer, state, incomplete, "", err
	}

	// The run failed with no test-level attribution. Two very different
	// causes share this shape: the mutant breaking the binary at package
	// scope (a goroutine panic, an os.Exit, a TestMain failure — the
	// strongest kind of kill), and environmental noise. A differential
	// baseline probe — the same invocation without the overlay — tells them
	// apart: noise fails the baseline too; a mutant-caused break does not
	// (REQ-exec-attribution). A hard crash can truncate the -json stream
	// before any package-level fail event, so the probe runs for that
	// shape too — attribution requiring a well-formed stream would make
	// exactly the strongest kills unmeasurable.
	pkg := failedPackage(stdout.Bytes())
	baseCtx, baseCancel := context.WithTimeout(parent, timeout)
	defer baseCancel()
	base := commandContext(baseCtx, "go", baseArgs...)
	base.Dir = dir
	baseScratchEnv, baseScratchRoot, sweepBaseScratch, removeBaseScratch, err := oracleScratch(env)
	if err != nil {
		return MutantDiscarded, "", runtimeinput.Observation{}, "", "", err
	}
	defer removeBaseScratch()
	base.Env = oracleEnv(baseScratchEnv)
	baseErr := runOracleProcess(base)
	// Sweep before finalization - the record captures the swept truth
	// (see the mutant site). The defer above is the panic backstop;
	// the sweep is idempotent.
	sweepBaseScratch()
	mutantState, mutantIncomplete, err := processObservationContext(ctx, testlog, moduleDir, "mutant test process exited before observation finalization", env, scratchRoot, capture, oracleFrame, namespaces)
	if err != nil {
		return MutantDiscarded, "", runtimeinput.Observation{}, "", "", err
	}
	if baseCtx.Err() != nil {
		baselineState, _, observationErr := processObservationContext(ctx, baseTestlog, moduleDir, "baseline test process did not complete", env, baseScratchRoot, capture, oracleFrame, namespaces)
		if observationErr != nil {
			return MutantDiscarded, "", runtimeinput.Observation{}, "", "", observationErr
		}
		state, mergeErr := mergeProcessObservationsContext(ctx, dir, env, capture, mutantState, baselineState)
		if mergeErr != nil {
			return MutantDiscarded, "", runtimeinput.Observation{}, "", "", mergeErr
		}
		// A cancelled parent is campaign cancellation and stays fatal
		// (REQ-exec-cancellation). The probe hitting the ORACLE timeout
		// is a per-mutant outcome: the package-scope failure cannot be
		// proven mutant-caused, so the candidate discards with its
		// diagnostic riding the incomplete channel — never a kill, never
		// a campaign abort (REQ-exec-attribution's noise arm; an abort
		// that discards completed measurements is reserved for corrupted
		// orchestration state).
		if parent.Err() != nil {
			return MutantDiscarded, "", state, mutantIncomplete, "", parent.Err()
		}
		diagnostic := fmt.Sprintf("unclassifiable mutant-run failure: the baseline probe exceeded the oracle timeout (%s), so the failure is not provably mutant-caused: %v: %s", timeout, runErr, tail(stderr.String()+stdout.String(), 400))
		return MutantDiscarded, "", state, diagnostic, "", nil
	}
	if baseErr == nil {
		baselineState, _, err := processObservationContext(ctx, baseTestlog, moduleDir, "", env, baseScratchRoot, capture, oracleFrame, namespaces)
		if err != nil {
			return MutantDiscarded, "", runtimeinput.Observation{}, "", "", err
		}
		state, err := mergeProcessObservationsContext(ctx, dir, env, capture, mutantState, baselineState)
		killer := PackageKillerPrefix + "unattributed crash)"
		if pkg != "" {
			killer = PackageKillerPrefix + pkg + ")"
		}
		return MutantKilled, killer, state, mutantIncomplete, "", err
	}
	// The baseline failed alongside the mutant: environmental noise. One
	// odd mutant records candidate-locally with its diagnostic and the
	// campaign continues; an abort is reserved for corrupted
	// orchestration state (REQ-exec-attribution).
	baselineState, _, observationErr := processObservationContext(ctx, baseTestlog, moduleDir, "baseline test process failed before observation finalization", env, baseScratchRoot, capture, oracleFrame, namespaces)
	if observationErr != nil {
		return MutantDiscarded, "", runtimeinput.Observation{}, "", "", observationErr
	}
	state, mergeErr := mergeProcessObservationsContext(ctx, dir, env, capture, mutantState, baselineState)
	if mergeErr != nil {
		return MutantDiscarded, "", runtimeinput.Observation{}, "", "", mergeErr
	}
	diagnostic := fmt.Sprintf("unclassifiable mutant-run failure: the baseline probe failed alongside the mutant (environmental noise, not a kill): %v: %s", runErr, tail(stderr.String()+stdout.String(), 400))
	return MutantDiscarded, "", state, diagnostic, "", nil
}

func processObservation(path, moduleDir, incompleteReason string, env []string, capture bool) (runtimeinput.Observation, string, error) {
	return processObservationContext(context.Background(), path, moduleDir, incompleteReason, env, "", capture, runtimeinput.ProducerFrame{}, nil)
}

// captureOracleFrame captures the pre-spawn producer frame a completed
// observation binds through (mutants run through a build overlay, so
// the on-disk tree the bracket covers is unmutated and stable across
// the run). The facade owns resolution, containment, and the capture
// refusals; a refused frame degrades the observation to incomplete
// with the frame's stated reason - fail-closed, never an error.
// Tool-owned bookkeeping directories are excluded from the bracket: an
// orchestrating corpus check or gomutant's own cache writing mid-run is
// not the oracle's runtime input. A module-root oracle package makes
// the bracket span the whole module - conservative and priced per
// spawn; any other volatile in-tree subtree seals it. Caller-declared
// bracket paths extend the covered surface to external fixed inputs the
// oracle legitimately reads; declaring one carries the bracket
// contract's mutation-free assertion.
func captureOracleFrame(ctx context.Context, moduleDir, packageDir string, bracketPaths []string) runtimeinput.ProducerFrame {
	return runtimeinput.CaptureProducerFrame(ctx, moduleDir, packageDir, runtimeinput.FrameOptions{
		BracketPaths:  bracketPaths,
		ExcludedPaths: []string{".stipulator", ".gomutant"},
	})
}

// oracleIngestEnv mirrors the environment the test binary actually ran
// under: the go tool spawns each test binary in its package directory
// with PWD pinned there, so the ingest mirror pins the same value - the
// facade refuses an environment whose PWD does not name the frame's
// package directory, which ends the silent process-local seal every
// PWD read got under the parent's inherited PWD. The spawn env's
// TMPDIR and GOMEMLIMIT additions deliberately stay out of the mirror
// - the minted scratch value is per-run noise the ephemeral-root
// declaration covers, and the ceiling's value is a measurement pin
// that stales findings itself - but the injected GOMAXPROCS is
// mirrored: the width is neither a pin nor per-run noise, so an
// oracle that observably reads it must have the value it actually saw
// recorded as runtime-input evidence - a mirror that hid it would
// serve stale verdicts to width-sensitive oracles across jobs changes
// (REQ-exec-oracle-parallelism). Applying the same composer the spawn
// used reproduces the spawn's exact narrowing decision.
func oracleIngestEnv(env []string, frame runtimeinput.ProducerFrame) []string {
	if frame.PkgDir == "" {
		return oracleCPUEnv(env)
	}
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, "PWD=") {
			out = append(out, entry)
		}
	}
	return oracleCPUEnv(append(out, "PWD="+frame.PkgDir))
}

// processObservationContext finalizes one launched test process's runtime-input
// observation. The returned reason is the process's effective incompleteness —
// the caller's incompleteReason, or the missing-log reason discovered here —
// and is empty exactly when the process proved its log complete; a completed
// observation that later fails absolute finalization keeps an empty reason
// because that is input movement, which stays finding-wide
// (REQ-exec-observation).
func processObservationContext(ctx context.Context, path, moduleDir, incompleteReason string, env []string, scratchRoot string, capture bool, frame runtimeinput.ProducerFrame, namespaces []runtimeinput.ScratchNamespace) (runtimeinput.Observation, string, error) {
	if err := ctx.Err(); err != nil {
		return runtimeinput.Observation{}, "", err
	}
	if !capture {
		return runtimeinput.Observation{}, "", nil
	}
	// The facade owns the fold discipline (caller verdict, missing or
	// unreadable or headerless capture, refused frame, PWD mismatch,
	// ingestion failure) and the ingest exclusions. The minted oracle
	// scratch root is declared as an ephemeral temp root: the tool
	// created it for this process tree and sweeps it after, so its
	// identity carries no observable state - without the declaration,
	// testing.TempDir's stat of TMPDIR records the root as an uncovered
	// runtime input and seals verifiability for every temp-touching
	// oracle (REQ-exec-oracle-scratch-declared).
	ingestEnv := oracleIngestEnv(env, frame)
	observation, reason, err := frame.Observe(ctx, path, runtimeinput.ProducerIngest{
		Identity:          path,
		Env:               ingestEnv,
		IncompleteReason:  incompleteReason,
		Roots:             runtimeinput.ClassificationRoots{EphemeralTemp: scratchRoot},
		ScratchNamespaces: namespaces,
	})
	if err != nil {
		return runtimeinput.Observation{}, "", err
	}
	if err := ctx.Err(); err != nil {
		return runtimeinput.Observation{}, "", err
	}
	observation, err = absoluteRuntimeEvidenceContext(ctx, observation, moduleDir, ingestEnv)
	return observation, reason, err
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
	// Merge re-evaluates each child's recorded digests against the
	// merge-time environment; the children ingested under the evidence
	// env (the injected width included), so merging under the raw env
	// would read a width-reading oracle's records as moved and degrade
	// the union to incomplete - silent evidence loss on exactly the
	// differential-attribution path (REQ-exec-oracle-parallelism).
	return mergeRuntimeEvidenceContext(ctx, root, OracleEvidenceEnv(env), states...)
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
	ran, passed, _, err = testProbeOnceObservedEnv(ctx, dir, testPkg, run, timeout, binFlags, "", "", nil, nil, env)
	return ran, passed, err
}

// TestProbeObservedEnv is TestProbe under a frozen environment with a
// runtime-input observation rooted at moduleDir and packageDir.
func TestProbeObservedEnv(ctx context.Context, dir, testPkg, run string, timeout time.Duration, binFlags []string, moduleDir, packageDir string, bracketPaths []string, namespaces []runtimeinput.ScratchNamespace, env []string) (ran int, passed bool, state runtimeinput.Observation, err error) {
	ran, passed, first, err := testProbeOnceObservedEnv(ctx, dir, testPkg, run, timeout, binFlags, moduleDir, packageDir, bracketPaths, namespaces, env)
	if err != nil {
		return ran, passed, first, err
	}
	if !passed {
		return ran, false, first, nil
	}
	if ran == 0 {
		return 0, true, first, nil
	}
	// The repeat guards baseline VALIDITY (a flaky pass fabricating
	// verdicts), which no observation bracket subsumes: it runs even
	// when the first observation's evidence is already unverifiable.
	// Only the empty-observation shortcut below needs verifiable
	// evidence to be meaningful.
	if !first.OK {
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
	if !first.Unverifiable && first.State == empty.State {
		return ran, passed, first, nil
	}
	secondRan, secondPassed, second, err := testProbeOnceObservedEnv(ctx, dir, testPkg, run, timeout, binFlags, moduleDir, packageDir, bracketPaths, namespaces, env)
	if err != nil {
		return secondRan, secondPassed, second, err
	}
	if secondRan != ran {
		return secondRan, secondPassed, runtimeinput.Observation{}, fmt.Errorf("baseline test count changed between discovery and measurement")
	}
	if !secondPassed {
		return secondRan, false, runtimeinput.Observation{}, fmt.Errorf("baseline result changed between discovery and measurement")
	}
	// The repeat guards baseline VALIDITY only; the evidence is the scored
	// second run's own bracket-vouched observation - the historical
	// cross-run evidence comparison is retired (REQ-exec-observation).
	return secondRan, secondPassed, second, nil
}

func testProbeOnceObservedEnv(ctx context.Context, dir, testPkg, run string, timeout time.Duration, binFlags []string, moduleDir, packageDir string, bracketPaths []string, namespaces []runtimeinput.ScratchNamespace, env []string) (ran int, passed bool, state runtimeinput.Observation, err error) {
	ctx2, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	scratchEnv, scratchRoot, sweepScratch, removeScratch, err := oracleScratch(env)
	if err != nil {
		return 0, false, runtimeinput.Observation{}, err
	}
	defer removeScratch()
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
	var oracleFrame runtimeinput.ProducerFrame
	if capture {
		oracleFrame = captureOracleFrame(ctx, moduleDir, packageDir, bracketPaths)
	}
	cmd := commandContext(ctx2, "go", args...)
	cmd.Dir = dir
	cmd.Env = oracleEnv(scratchEnv)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	runErr := runOracleProcess(cmd)
	// Sweep before finalization - the record captures the swept truth
	// (see the mutant site).
	sweepScratch()
	if ctx2.Err() == context.DeadlineExceeded {
		state, _, observationErr := processObservationContext(ctx, testlog, moduleDir, "baseline test process timed out", env, scratchRoot, capture, oracleFrame, namespaces)
		if observationErr != nil {
			return 0, false, runtimeinput.Observation{}, observationErr
		}
		return 0, false, state, fmt.Errorf("baseline test timed out after %s - the oracle timeout governs this bound (oracle_timeout_sec / --oracle-timeout)", timeout)
	}
	if err := ctx2.Err(); err != nil {
		state, _, observationErr := processObservationContext(ctx, testlog, moduleDir, "baseline test process was cancelled", env, scratchRoot, capture, oracleFrame, namespaces)
		if observationErr != nil {
			return 0, false, runtimeinput.Observation{}, observationErr
		}
		return 0, false, state, err
	}
	if strings.Contains(buf.String(), "[build failed]") {
		if diagnostic := compileDiagnostics(buf.Bytes(), nil); diagnostic != "" {
			return 0, false, runtimeinput.Observation{}, fmt.Errorf("baseline test failed to build:\n%s", diagnostic)
		}
		return 0, false, runtimeinput.Observation{}, fmt.Errorf("baseline test failed to build")
	}
	ran, err = countTopTests(buf.Bytes())
	if err != nil {
		return 0, false, runtimeinput.Observation{}, fmt.Errorf("parse baseline test output: %w", err)
	}
	state, _, err = processObservationContext(ctx, testlog, moduleDir, "", env, scratchRoot, capture, oracleFrame, namespaces)
	if err != nil {
		return 0, false, runtimeinput.Observation{}, err
	}
	return ran, runErr == nil, state, nil
}

// compileDiagnostics extracts the compiler's own text from a failed
// build's captured output: raw stderr lines plus the -json stream's
// build-output events and any non-JSON lines interleaved in it, capped
// so a pathological diagnostic cannot flood a refusal message.
func compileDiagnostics(stdout, stderr []byte) string {
	var b strings.Builder
	add := func(line string) {
		line = strings.TrimRight(line, "\n")
		if strings.TrimSpace(line) == "" {
			return
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	for _, line := range strings.Split(string(stderr), "\n") {
		add(line)
	}
	type event struct{ Action, Output string }
	for _, line := range strings.Split(string(stdout), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "{") {
			add(line)
			continue
		}
		var e event
		if json.Unmarshal([]byte(line), &e) != nil {
			add(line)
			continue
		}
		if e.Action == "build-output" {
			add(e.Output)
		}
	}
	out := strings.TrimSpace(b.String())
	const limit = 4096
	if len(out) > limit {
		cut := limit
		for cut > 0 && !utf8.RuneStart(out[cut]) {
			cut--
		}
		out = out[:cut] + "\n[diagnostic truncated]"
	}
	return out
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

// buildRejected reports whether the test harness itself reported a failed
// build: the top-level "build-fail" event, or a package-level fail event
// carrying FailedBuild. Both are harness-generated and unforgeable — a test's
// own output rides only inside "output" events' Output strings, so a test
// printing a captured "[build failed]" line can never classify here
// (candidate evidence term: the no-process claim is tied to the harness's
// build-failure event, never to output text).
func buildRejected(stream []byte) bool {
	type event struct {
		Action      string
		FailedBuild string
	}
	dec := json.NewDecoder(bytes.NewReader(stream))
	for {
		var e event
		if err := dec.Decode(&e); err != nil {
			return false
		}
		if e.Action == "build-fail" || e.FailedBuild != "" {
			return true
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

// oracleScratch gives one oracle process tree its own TMPDIR and
// returns the swept cleanup. A mutant killed on timeout or by the
// memory ceiling never runs its deferred cleanups; on a tmpfs-backed
// temp directory the leaked test directories are leaked RAM
// compounding the very pressure that triggered the kill, so each
// process tree writes under its own directory removed as soon as the
// process ends - with directory permissions restored first, because
// leaked test dirs can carry modes a plain removal cannot descend
// (REQ-exec-oracle-scratch). The directory lands under the operator's
// own TMPDIR, so pointing campaigns at disk-backed space (/var/tmp)
// needs one environment variable.
func oracleScratch(env []string) ([]string, string, func(), func(), error) {
	dir, err := os.MkdirTemp("", "gomutant-oracle-*")
	if err != nil {
		return nil, "", nil, nil, err
	}
	restoreModes := func() {
		filepath.WalkDir(dir, func(path string, d iofs.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				os.Chmod(path, 0o755)
			}
			return nil
		})
	}
	// sweep empties the root but keeps it: observation finalization
	// admits the root and its absent deeper reads only while the root
	// still resolves - an unresolvable root declares nothing under the
	// runtimeinput contract - so the emptied root outlives finalization
	// and remove drops it with the run's other ephemera
	// (REQ-exec-oracle-scratch-order).
	sweep := func() {
		restoreModes()
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
	remove := func() {
		restoreModes()
		os.RemoveAll(dir)
	}
	return append(append([]string(nil), env...), "TMPDIR="+dir), dir, sweep, remove, nil
}
