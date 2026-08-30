package gomutant

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/greatliontech/gomutant/internal/contextio"
	"github.com/greatliontech/gomutant/internal/engine"
)

// coveredPositions is the baseline coverage probe; a variable so the
// probe-failure arm - the label stays absent and the measurement stays
// sound - is testable without constructing a genuinely unbuildable
// probe.
var coveredPositions = engine.CoveredPositions

// testProbe is the baseline probe; a variable so the bound each mode
// hands it — the measurement leash in derived-budget mode, the
// caller's override otherwise — is pinnable by a delegating recorder
// without constructing a genuinely slow baseline.
var testProbe = engine.TestProbeEnv

// EphemeralResult is one manual mutant's evidence (REQ-exec-ephemeral): what
// was mutated, the test it ran against, whether that test killed it, and the
// attributed killer. It is evidence for the caller to act on, never
// persisted to a finding record.
type EphemeralResult struct {
	Files   []string `json:"files"`
	TestPkg string   `json:"testPkg"`
	Run     string   `json:"run"`
	// Killed means every run killed the mutant: with Runs > 1 it is the
	// "killed N consecutive runs" claim that splits a deterministic kill
	// from a property generator's draw luck. A mixed outcome reads
	// Killed=false with KilledRuns naming how many runs killed.
	Killed bool `json:"killed"`
	// Killer names the failing test, a timeout, or a package-scope failure
	// from the first killing run; empty when no run killed.
	Killer string `json:"killer,omitempty"`
	// KillerOutput is the first killing run's bounded evidence: the
	// killing test's first output lines (remainder counted), the timeout
	// naming its governing option, or the package-scope crash's bounded
	// text - so acting on a kill needs no parallel oracle re-run.
	KillerOutput string `json:"killerOutput,omitempty"`
	// Runs is how many times the mutant ran against the once-probed
	// baseline; KilledRuns how many of them killed; RunVerdicts each
	// run's verdict in order ("killed: <killer>" or "survived").
	Runs        int      `json:"runs"`
	KilledRuns  int      `json:"killedRuns"`
	RunVerdicts []string `json:"runVerdicts"`
	// UnexercisedFiles names replacement files no baseline-covered block
	// touches (non-kill verdicts - plain survival and the mixed
	// killed-some-runs outcome alike): the file is linked into the
	// oracle's binary — an unlinked replacement refuses at validation —
	// yet the probed run never reached it, so killed=false over it is
	// not evidence the oracle noticed anything. Advisory, absent when
	// the coverage probe fails (REQ-exec-ephemeral).
	UnexercisedFiles []string `json:"unexercisedFiles,omitempty"`
	// EditDigest identifies the measured mutant: a digest over the
	// ordered replacement set (tree-relative file, full replacement
	// content), the identity an equivalence attestation records
	// (REQ-result-ephemeral-attest).
	EditDigest string `json:"editDigest"`
	// OracleBudget is the effective per-process oracle bound the
	// mutant runs used — the caller's explicit timeout, or the budget
	// derived from the measured baseline (REQ-exec-ephemeral's
	// derived budget), as a canonical duration string.
	OracleBudget string `json:"oracleBudget"`
	// MeasuredBaseline is the baseline probe's wall-clock duration
	// rounded to the millisecond — the recorded value IS the derivation
	// input for OracleBudget in derived-budget mode (recorded in
	// explicit-timeout mode too: it is a measurement either way), as a
	// canonical duration string.
	MeasuredBaseline string `json:"measuredBaseline"`
	// CoverageUnknown marks a non-kill verdict whose exercise state
	// could not be established (the baseline coverage probe failed):
	// UnexercisedFiles absent then means UNKNOWN, not exercised — the
	// two must never share one encoding, or an unverifiable survivor
	// reads as a vouched one (REQ-exec-ephemeral,
	// REQ-result-ephemeral-attest's unverifiable refusal).
	CoverageUnknown bool `json:"coverageUnknown,omitempty"`
}

// SetOracleMemoryLimit installs the per-oracle-process memory ceiling
// for this process's runs and ephemeral probes
// (REQ-exec-oracle-memory): bytes > 0 is the explicit cap, 0 derives
// RAM / (2 x jobs) floored at 1 GiB, negative disables. Run installs
// it from RunOptions; ephemeral callers install it directly, and an
// uninstalled process derives the default at its first probe.
func SetOracleMemoryLimit(bytes int64, jobs int) {
	engine.SetOracleMemoryLimit(bytes, jobs)
}

// OracleMemoryLimitBytes reports the installed per-oracle ceiling; 0
// means disabled or not yet derived.
func OracleMemoryLimitBytes() int64 {
	return engine.OracleMemoryLimitBytes()
}

// OracleMemorySnapshot mirrors the engine's ceiling state for exact
// restore around a scoped override.
type OracleMemorySnapshot = engine.OracleMemorySnapshot

// SnapshotOracleMemory captures the ceiling state; RestoreOracleMemory
// reinstates it verbatim, the installed flag included.
func SnapshotOracleMemory() OracleMemorySnapshot { return engine.SnapshotOracleMemory() }

// RestoreOracleMemory reinstates a snapshot captured by
// SnapshotOracleMemory.
func RestoreOracleMemory(s OracleMemorySnapshot) { engine.RestoreOracleMemory(s) }

// SetOracleParallelism installs the per-oracle inner-parallelism cap
// for this process's runs (REQ-exec-oracle-parallelism): each oracle
// tree's width becomes max(1, NumCPU/jobs). Run installs it from its
// job count; a long-lived caller probing between campaigns installs
// jobs=1 (full width for the lone tree) around the probe.
func SetOracleParallelism(jobs int) { engine.SetOracleParallelism(jobs) }

// OracleParallelismSnapshot mirrors the engine's width state for exact
// restore around a scoped override.
type OracleParallelismSnapshot = engine.OracleParallelismSnapshot

// SnapshotOracleParallelism captures the width state;
// RestoreOracleParallelism reinstates it verbatim.
func SnapshotOracleParallelism() OracleParallelismSnapshot {
	return engine.SnapshotOracleParallelism()
}

// RestoreOracleParallelism reinstates a snapshot captured by
// SnapshotOracleParallelism.
func RestoreOracleParallelism(s OracleParallelismSnapshot) { engine.RestoreOracleParallelism(s) }

// Ephemeral runs one manual mutant — a caller-supplied replacement of one
// source file, for the mutations the operator set cannot generate
// (generated-data drift, resolver seams, caller mappings): it overlays file
// with mutant (the whole replacement source), runs the named test (testPkg
// filtered to run), and reports whether the test killed it — all through a
// build overlay, the tree never touched (REQ-exec-ephemeral). Before running
// it probes the named test on the unmutated tree: a run pattern matching
// nothing, or a test already failing clean, cannot attribute a mutant, so
// either refuses the run rather than scoring it. file is tree-relative;
// testPkg is a go package path; run is a -run pattern. A non-positive
// oracleTimeout derives the mutant budget from the measured baseline (a
// leash-bounded baseline, then a multiple with a floor); an explicit value
// is the caller's bound for the baseline and mutant runs alike — the
// result reports the effective budget and the measured baseline either
// way. A mutant that fails
// to compile is an error, not a survivor: nothing was measured.
func (t *Tree) Ephemeral(ctx context.Context, file string, mutant []byte, testPkg, run string, oracleTimeout time.Duration, runs int) (*EphemeralResult, error) {
	engine.EnsureOracleMemoryDefault(1)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	abs, err := resolveTreeFile(t.dir, file)
	if err != nil {
		return nil, err
	}
	// The overlay silently no-ops if abs is not a real source file, and an
	// identical replacement measures nothing — both would read as a false
	// survivor. Resolve and compare against the original first.
	orig, err := readFileContext(ctx, abs)
	if err != nil {
		return nil, fmt.Errorf("reading source %s: %w", file, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if bytes.Equal(orig, mutant) {
		return nil, fmt.Errorf("mutant is identical to %s: nothing to measure", file)
	}

	return t.runEphemeral(ctx, []fileReplacement{{File: file, Abs: abs, Source: mutant}}, testPkg, run, oracleTimeout, runs)
}

// ephemeralEditDigest derives the mutant's identity: a digest over the
// ordered replacement set, each entry keyed by the resolved
// tree-relative path (so two alias spellings of one file share one
// identity, and the identity travels across checkouts) with its full
// replacement content. Distinct content, or the same content in a
// different file, is a different mutant (REQ-result-ephemeral-attest).
func ephemeralEditDigest(dir string, replacements []fileReplacement) string {
	// The root is resolved to its physical form: replacement.Abs is
	// symlink-resolved by resolveTreeFile, so an aliased tree root
	// would otherwise make every Rel non-local and fall back to the
	// caller's spelling — two alias spellings of one file earning two
	// identities.
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
		if resolved, rerr := filepath.EvalSymlinks(abs); rerr == nil {
			dir = resolved
		}
	}
	digest := sha256.New()
	for _, replacement := range replacements {
		key := replacement.File
		if rel, err := filepath.Rel(dir, replacement.Abs); err == nil && filepath.IsLocal(rel) {
			key = filepath.ToSlash(rel)
		}
		content := sha256.Sum256(replacement.Source)
		digest.Write([]byte(key))
		digest.Write([]byte{0})
		digest.Write(content[:])
		digest.Write([]byte{0})
	}
	return hex.EncodeToString(digest.Sum(nil))
}

// discardError maps a discarded probe to its repairing reason: an
// environmental-noise discard must not read as a compile failure - the
// caller would "check the replacements" when the environment was at
// fault.
func discardError(files []string, diagnostic string) error {
	if strings.HasPrefix(diagnostic, "unclassifiable mutant-run failure") {
		return fmt.Errorf("mutant run was unclassifiable - not a measurement:\n%s", diagnostic)
	}
	if diagnostic != "" {
		return fmt.Errorf("mutant did not compile: nothing was measured — check the replacements for %s\n%s", strings.Join(files, ", "), diagnostic)
	}
	return fmt.Errorf("mutant did not compile: nothing was measured — check the replacements for %s", strings.Join(files, ", "))
}

// MaxEphemeralRuns bounds runs:N - each run is a full oracle process,
// and an unbounded N would let one probe request scale like a campaign.
const MaxEphemeralRuns = 10

// ephemeralBaselineLeash is the generous non-measurement ceiling for
// two workloads the oracle bound does not fit: the derived-budget
// baseline (the measurement the budget derives from, which must not
// run under the budget it exists to derive) and the coverage probe in
// both modes (an instrumented whole-closure rebuild, structurally
// heavier than the measured oracle). The command timeout still bounds
// the whole probe.
const ephemeralBaselineLeash = 10 * time.Minute

// campaignBaselineLeash bounds baselines and advisory probes in
// derived-budget mode: the baseline is the measurement each group's
// budget derives from, so it gets a generous ceiling rather than the
// budget it exists to derive — sized for suite-class oracles (the
// measured field shapes: ~6.5m and ~16m root suites), where the
// ephemeral face's 10m leash was sized for probe-class ones. A hung
// suite burns it once per group; the command timeout still bounds.
const campaignBaselineLeash = time.Hour

// ephemeralBudgetFloor keeps a derived budget from ever being less
// patient than the fixed default it replaced: the floor is the
// retired 60s, because a warm-cache baseline pays no compile while
// the mutant run always recompiles the mutated package inside its
// bound — the multiple alone would time out honest slow mutants of
// fast tests, and a timeout is a kill, the flattering direction. A
// variable only so tests can pin the measured derivation and the
// timeout-kill serve without minute-class hangs; production never
// writes it.
var ephemeralBudgetFloor = 60 * time.Second

// derivedOracleBudget maps a measured baseline duration to the mutant
// budget: a multiple with a floor (REQ-exec-ephemeral's derived
// budget; the values are incidental, not contract).
func derivedOracleBudget(baseline time.Duration) time.Duration {
	budget := 4 * baseline
	if budget < ephemeralBudgetFloor {
		budget = ephemeralBudgetFloor
	}
	return budget
}

// derivedBaselineRefusal re-frames a baseline refusal for
// derived-budget mode, naming the bound that actually fired
// (REQ-exec-ephemeral's derived budget names its bounds honestly):
//   - the oracle bound's own expiry means the measurement leash fired
//     (the oracle knob the engine's message names never governed) —
//     the refusal REPLACES the engine's error rather than wrapping
//     it, because the wrapped text's knob claim would contradict the
//     re-frame and the typed error carries nothing else any consumer
//     reads (audited: the attest record, CLI, and MCP faces render
//     Error() only);
//   - a bare deadline expiry is the command deadline dying during the
//     leashed baseline (on faces whose command timeout is shorter
//     than the leash the leash can never fire), named as such;
//   - every other refusal — cancellation included — passes through.
func derivedBaselineRefusal(err error) error {
	var bt *engine.BaselineTimeoutError
	if errors.As(err, &bt) {
		return fmt.Errorf("baseline test ran past the %s measurement leash - the derived budget needs a measured baseline; pass an explicit oracle timeout (oracle_timeout_sec / --oracle-timeout) to bound this oracle instead", bt.Bound)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("the command deadline expired during the %s-leashed baseline measurement - raise the command timeout (timeout_sec / --timeout), or pass an explicit oracle timeout (oracle_timeout_sec / --oracle-timeout) to bound this oracle instead: %w", ephemeralBaselineLeash, err)
	}
	return err
}

// derivedTimeoutEvidence is a timeout kill's evidence text in
// derived-budget mode: the engine's names the oracle knob, which here
// never governed — the bound's provenance is the measured baseline,
// and the override path rides along.
func derivedTimeoutEvidence(mutantBudget, measuredBaseline time.Duration) string {
	return fmt.Sprintf("oracle timed out after %s - the bound is the budget derived from the measured %s baseline; an explicit oracle timeout (oracle_timeout_sec / --oracle-timeout) overrides the derivation", mutantBudget, measuredBaseline)
}

// timeoutEvidenceForMode picks a kill's evidence text: only a timeout
// kill under a DERIVED bound is re-framed (the engine's text names the
// oracle knob, which never governed); every other kill — explicit
// bound, or any non-timeout killer — keeps the engine's evidence
// verbatim.
func timeoutEvidenceForMode(derive bool, killer, evidence string, mutantBudget, measuredBaseline time.Duration) string {
	if derive && killer == engine.TimeoutKiller {
		return derivedTimeoutEvidence(mutantBudget, measuredBaseline)
	}
	return evidence
}

func (t *Tree) runEphemeral(ctx context.Context, replacements []fileReplacement, testPkg, run string, oracleTimeout time.Duration, runs int) (*EphemeralResult, error) {
	engine.EnsureOracleMemoryDefault(1)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(replacements) == 0 {
		return nil, fmt.Errorf("manual mutant has no file replacements")
	}
	seen := map[string]bool{}
	for i, replacement := range replacements {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if replacement.File == "" || replacement.Abs == "" || replacement.Source == nil {
			return nil, fmt.Errorf("manual mutant replacement %d is incomplete", i+1)
		}
		if seen[replacement.Abs] {
			return nil, fmt.Errorf("manual mutant replaces %s more than once", replacement.File)
		}
		seen[replacement.Abs] = true
	}
	// The oracle budget: an explicit timeout is the caller's override;
	// zero derives it from the baseline itself — the baseline run IS a
	// measurement of the oracle's cost, so the budget follows the
	// measured tree instead of a fixed knob that dies at baseline on a
	// loaded host and idles through most of itself on a quiet one
	// (REQ-exec-ephemeral's derived budget). The baseline runs under a
	// generous measurement leash (and the command timeout still
	// bounds); the derived budget is a multiple with a floor — the
	// values are incidental like the seed: headroom for scheduler
	// noise and genuine mutant-induced slowdown without unmooring the
	// timeout-kill meaning.
	derive := oracleTimeout <= 0
	baselineBound := oracleTimeout
	if derive {
		baselineBound = ephemeralBaselineLeash
	}
	if runs == 0 {
		runs = 1
	}
	if runs < 1 || runs > MaxEphemeralRuns {
		return nil, fmt.Errorf("runs must be between 1 and %d - each run is a full oracle process", MaxEphemeralRuns)
	}
	// The build ignores what it does not compile: an overlay of a
	// build-excluded or non-Go file measures a mutant that was never
	// present, and a test package in a go test option position changes
	// the invocation being measured - both refuse before any process
	// launches (REQ-exec-ephemeral).
	if !t.eng.HasPackage(testPkg) {
		return nil, fmt.Errorf("test package %q is not a loaded package import path", testPkg)
	}
	for _, replacement := range replacements {
		if !t.eng.BuildCompilesFile(replacement.Abs) {
			return nil, fmt.Errorf("replacement %s is not compiled by the loaded build (build-constraint-excluded, or not a Go source of any loaded package): the mutation would never be exercised", replacement.File)
		}
	}
	// A file the build compiles SOMEWHERE can still be outside the named
	// oracle's own binary: a fixture or sibling package the test package
	// never imports overlays cleanly, every test passes — even a syntax
	// error goes unnoticed, the broken package is never built — and the
	// verdict would be a false survivor. The linked dependency set is
	// the discriminator, and it refuses before any process launches; a
	// linked-but-uncovered replacement remains an honest survivor with
	// the unexercised advisory (REQ-exec-ephemeral).
	linked, err := t.eng.LinkedTestPackagesContext(ctx, testPkg)
	if err != nil {
		return nil, err
	}
	// A nil set means the closure itself does not resolve or build: the
	// gate stands down and the baseline probe owns the refusal with the
	// compiler's own diagnostic, the spec's canonical framing.
	if linked != nil {
		for _, replacement := range replacements {
			if pkg := t.eng.FileImportPath(replacement.Abs); !linked[pkg] {
				return nil, fmt.Errorf("the oracle never compiles %s: package %s is outside %s's linked dependency set — no verdict; name an oracle that links the edited package", replacement.File, pkg, testPkg)
			}
		}
	}
	// A rapid property failing on the baseline or against the mutant must
	// never write a reproducer into the tree (REQ-mut-overlay).
	var binFlags []string
	rapid, _, err := t.eng.SplitRapidPkgsContext(ctx, []string{testPkg})
	if err != nil {
		return nil, err
	}
	if len(rapid) > 0 {
		// A property oracle's draws are pinned so the probe's verdict -
		// and runs:N's per-run verdicts - are reproducible; the
		// reproducer-file suppression protects the tree exactly as
		// before (REQ-exec-property-oracles).
		binFlags = engine.PropertyOracleBinFlags()
	}

	env := t.eng.GoEnv()
	baselineStart := time.Now()
	ran, passed, err := testProbe(ctx, t.dir, testPkg, run, baselineBound, binFlags, env)
	if err != nil {
		if derive {
			return nil, derivedBaselineRefusal(err)
		}
		return nil, err
	}
	if ran == 0 {
		return nil, fmt.Errorf("%q matched no tests in %s: nothing can attribute the mutant", run, testPkg)
	}
	if !passed {
		return nil, fmt.Errorf("the named test does not pass on the unmutated tree in %s: a kill against it would be fabricated", testPkg)
	}
	measuredBaseline := time.Since(baselineStart).Round(time.Millisecond)
	mutantBudget := oracleTimeout
	if derive {
		// The baseline just measured the oracle's cost on this tree
		// under this load. The measurement can UNDERSTATE the mutant
		// run's cost — a warm-cache baseline pays no compile while
		// the mutant always recompiles the mutated package inside its
		// bound — which is why the floor is the retired fixed
		// default, never lower.
		mutantBudget = derivedOracleBudget(measuredBaseline)
	}

	files := make([]string, len(replacements))
	engineReplacements := make([]engine.Replacement, len(replacements))
	for i, replacement := range replacements {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		files[i] = replacement.File
		engineReplacements[i] = engine.Replacement{File: replacement.Abs, Source: replacement.Source}
	}
	m := engine.Mutant{Replacements: engineReplacements}
	res := &EphemeralResult{
		Files:            files,
		TestPkg:          testPkg,
		Run:              run,
		Runs:             runs,
		EditDigest:       ephemeralEditDigest(t.dir, replacements),
		OracleBudget:     mutantBudget.String(),
		MeasuredBaseline: measuredBaseline.String(),
	}
	// N runs against the once-probed baseline: per-run verdicts split a
	// deterministic kill (every run killed) from a property generator's
	// draw luck (REQ-exec-ephemeral).
	for i := 0; i < runs; i++ {
		outcome, killer, evidence, diagnostic, err := engine.RunMutantEvidenceEnv(ctx, t.dir, m, []string{testPkg}, run, mutantBudget, binFlags, env)
		if err != nil {
			return nil, err
		}
		if outcome == engine.MutantDiscarded {
			return nil, discardError(files, diagnostic)
		}
		if outcome == engine.MutantKilled {
			evidence = timeoutEvidenceForMode(derive, killer, evidence, mutantBudget, measuredBaseline)
			res.KilledRuns++
			res.RunVerdicts = append(res.RunVerdicts, "killed: "+killer)
			if res.Killer == "" {
				res.Killer = killer
				res.KillerOutput = evidence
			}
			continue
		}
		res.RunVerdicts = append(res.RunVerdicts, "survived")
	}
	res.Killed = res.KilledRuns == runs
	if !res.Killed {
		// A survivor verdict over a replacement the probed run never
		// exercised is not evidence the oracle noticed anything — the
		// linked-but-unexecuted false-survivor channel (an UNLINKED
		// replacement already refused at validation): the file is in
		// the binary, but no covered block reaches it, so every test
		// passing says nothing about the mutant. One baseline coverage probe (non-kill
		// verdicts only - the mixed killed-some-runs outcome leaves the
		// false-survivor reading open too; kills need no qualifier)
		// classifies each
		// replacement file; a probe failure leaves the advisory label
		// absent rather than failing a sound measurement — and marks
		// the exercise state UNKNOWN, so absence never reads as
		// exercised (REQ-exec-ephemeral).
		// The coverage probe recompiles the linked closure instrumented
		// — a structurally different (heavier) workload than the
		// measured oracle — so neither the derived budget (scaled to
		// the uninstrumented baseline) nor the caller's explicit oracle
		// bound (sized for the oracle, not the rebuild) fits it: it
		// runs under the measurement leash in both modes. Its expiry is
		// the advisory posture, never a verdict — CoverageUnknown, the
		// label absent — and the command timeout still bounds.
		if coverage, err := coveredPositions(ctx, t.dir, testPkg, run, "./...", ephemeralBaselineLeash, binFlags, t.eng.GoEnv(), t.eng.DirectiveCoverage()); err != nil {
			res.CoverageUnknown = true
		} else {
			for i, replacement := range replacements {
				pkgPath := t.eng.FileImportPath(replacement.Abs)
				if pkgPath != "" && coverage.Unsound(pkgPath+"/"+filepath.Base(replacement.Abs)) {
					// A refused re-keying is not evidence of anything:
					// claiming "unexercised" for a file whose profile
					// entry the seam could not soundly attribute would
					// manufacture the advisory (REQ-exec-ephemeral's
					// probe-failure posture: the label stays absent).
					continue
				}
				if pkgPath == "" || !coverage.CoversFile(pkgPath+"/"+filepath.Base(replacement.Abs)) {
					res.UnexercisedFiles = append(res.UnexercisedFiles, files[i])
				}
			}
		}
	}
	return res, nil
}

// EphemeralBatch runs one atomic multi-file exact-match edit batch as a manual
// mutant. Every edit resolves against the original files before one overlay
// exposes all effective replacements to the named test. oracleTimeout
// carries Ephemeral's semantics: non-positive derives the mutant budget
// from the measured baseline.
func (t *Tree) EphemeralBatch(ctx context.Context, edits []BatchEdit, testPkg, run string, oracleTimeout time.Duration, runs int) (*EphemeralResult, error) {
	replacements, err := prepareEditBatchContext(ctx, t.dir, edits)
	if err != nil {
		return nil, err
	}
	return t.runEphemeral(ctx, replacements, testPkg, run, oracleTimeout, runs)
}

// Edit is one exact-match replacement inside an ephemeral mutant's source:
// Old must occur exactly once in the current content — a match of zero or
// more than one is refused rather than guessed, because a mutation applied
// somewhere the caller did not mean measures the wrong mutant
// (REQ-exec-ephemeral).
type Edit struct {
	Old string `json:"old"`
	New string `json:"new"`
}

// ApplyEdits applies exact-match edits to src in order and returns the
// mutated content — the edits form of an ephemeral mutant's replacement
// source (REQ-exec-ephemeral).
func ApplyEdits(src []byte, edits []Edit) ([]byte, error) {
	return ApplyEditsContext(context.Background(), src, edits)
}

// ApplyEditsContext is ApplyEdits with cooperative cancellation.
func ApplyEditsContext(ctx context.Context, src []byte, edits []Edit) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(edits) == 0 {
		return nil, fmt.Errorf("gomutant: no edits given")
	}
	out := string(src)
	for i, e := range edits {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if e.Old == "" {
			return nil, fmt.Errorf("gomutant: edit %d has an empty match", i+1)
		}
		switch n := overlappingMatchStarts(out, e.Old); n {
		case 0:
			return nil, fmt.Errorf("gomutant: edit %d matches nothing: %q", i+1, e.Old)
		case 1:
			out = strings.Replace(out, e.Old, e.New, 1)
		default:
			return nil, fmt.Errorf("gomutant: edit %d is ambiguous (%d matches): %q", i+1, n, e.Old)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []byte(out), nil
}

// overlappingMatchStarts counts every valid match start, overlapping
// included: in "aaa" the pattern "aa" starts at 0 and 1, so it is
// ambiguous even though the non-overlapping count is 1 - an edit
// applied at a guessed start is a measurement of the wrong mutant
// (REQ-exec-ephemeral).
func overlappingMatchStarts(s, pattern string) int {
	if pattern == "" {
		return 0
	}
	count := 0
	for from := 0; ; {
		i := strings.Index(s[from:], pattern)
		if i < 0 {
			return count
		}
		count++
		from += i + 1
	}
}

// EphemeralEdits runs an ephemeral mutant given as exact-match edits against
// the file's current content (REQ-exec-ephemeral): the edits are applied and
// the result runs exactly as a whole replacement would, oracleTimeout
// carrying Ephemeral's derive-on-non-positive semantics.
func (t *Tree) EphemeralEdits(ctx context.Context, file string, edits []Edit, testPkg, run string, oracleTimeout time.Duration, runs int) (*EphemeralResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	abs, err := resolveTreeFile(t.dir, file)
	if err != nil {
		return nil, err
	}
	orig, err := readFileContext(ctx, abs)
	if err != nil {
		return nil, fmt.Errorf("reading source %s: %w", file, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	mutant, err := ApplyEditsContext(ctx, orig, edits)
	if err != nil {
		return nil, err
	}
	return t.Ephemeral(ctx, file, mutant, testPkg, run, oracleTimeout, runs)
}

func readFileContext(ctx context.Context, path string) ([]byte, error) {
	return contextio.ReadFile(ctx, path)
}
