package gomutant

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
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

// runMutantEvidence is the mutant runner seam: tests plant a compiler
// crash the probe must retry once and never read as a verdict.
var runMutantEvidence = engine.RunMutantEvidenceEnv

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
	// OracleMemoryBytes is the memory ceiling every oracle process of
	// the probe — the baseline and the mutant runs — ran under; 0 when
	// disabled (REQ-exec-oracle-memory). The probe's own bounds, stated
	// so a reader judging a memory-shaped verdict knows the ceiling it
	// was measured under.
	OracleMemoryBytes int64 `json:"oracleMemoryBytes"`
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
	// MutatedTests names the replacement files that are test files: a
	// probe may mutate the oracle's own source (to ask whether an
	// assertion is load-bearing), but its verdict is then about the
	// test, never about the code under test — a survivor over a mutated
	// test says the edited part was not load-bearing for the named run
	// and says nothing about coverage (REQ-exec-ephemeral's blind
	// spots).
	MutatedTests []string `json:"mutatedTests,omitempty"`
	// PrunedImports names the imports the probe dropped from each
	// replacement before compiling it — "path (file)" — because nothing
	// in the mutant referenced them: a deletion probe strands its
	// guard's imports, and a probe declares no import intent, so the
	// prune changes no meaning; stated so a verdict over a pruned
	// mutant reads honestly (REQ-exec-ephemeral).
	PrunedImports []string `json:"prunedImports,omitempty"`
	// CoverageUnknown marks a non-kill verdict whose exercise state
	// could not be established for some replacement — the baseline
	// coverage probe failed (every file unknown), or a file's profile
	// entry could not be soundly attributed (that file unknown, named
	// in CoverageUnknownFiles): UnexercisedFiles absent then means
	// UNKNOWN, not exercised — the two must never share one encoding,
	// or an unverifiable survivor reads as a vouched one
	// (REQ-exec-ephemeral, REQ-result-ephemeral-attest's unverifiable
	// refusal).
	CoverageUnknown bool `json:"coverageUnknown,omitempty"`
	// CoverageUnknownFiles names the replacement files whose exercise
	// state is unknown; every file when the probe itself failed.
	CoverageUnknownFiles []string `json:"coverageUnknownFiles,omitempty"`
}

// OracleBounds are the resource bounds a run or a probe applies to
// every oracle process tree it spawns; DeriveOracleBounds derives them
// from a memory choice (bytes > 0 explicit, 0 derived RAM/(2 x jobs)
// floored at 1 GiB, negative disabled) and a job count
// (REQ-exec-oracle-memory, REQ-exec-oracle-parallelism). Bounds are a
// value each run derives for itself, never process state: a campaign
// and a probe in one process, or two probes, each spawn under their
// own.
type OracleBounds = engine.OracleBounds

// DeriveOracleBounds derives a run's bounds from its memory choice and
// job count: memoryBytes > 0 is the explicit ceiling, 0 derives total
// RAM over twice the job count floored at 1 GiB, negative disables;
// the width is max(1, NumCPU/jobs).
func DeriveOracleBounds(memoryBytes int64, jobs int) OracleBounds {
	return engine.DeriveOracleBounds(memoryBytes, jobs)
}

// OracleMemoryBytesFromMiB is the one MiB-to-bytes policy every face's
// memory knob shares: a positive count is the ceiling in MiB, 0 derives
// the default, a negative count disables the ceiling.
func OracleMemoryBytesFromMiB(mib int64) int64 {
	if mib <= 0 {
		return mib
	}
	return mib << 20
}

// EphemeralRequest is one ephemeral probe in any of its three edit forms
// — exactly one of Mutant (a whole replacement of File), Edits
// (sequential exact-match edits of File), or BatchEdits (atomic
// file-scoped edits) — with the probe's oracle and, optionally, a
// Progress sink receiving the probe's phases as they begin: the
// baseline probe, each mutant run, the advisory coverage probe
// (REQ-exec-run-status).
type EphemeralRequest struct {
	File          string
	Mutant        []byte
	Edits         []Edit
	BatchEdits    []BatchEdit
	TestPkg, Run  string
	OracleTimeout time.Duration
	Runs          int
	Progress      func(PreparationEvent)
	// OracleMemoryBytes chooses the probe's memory ceiling: > 0 explicit,
	// 0 derived for a lone oracle tree, negative disabled. The probe's
	// width is a lone tree's — the full host (REQ-exec-oracle-parallelism).
	OracleMemoryBytes int64
}

// RunEphemeral runs the request's probe.
func (t *Tree) RunEphemeral(ctx context.Context, req EphemeralRequest) (*EphemeralResult, error) {
	forms := 0
	for _, given := range []bool{req.Mutant != nil, len(req.Edits) != 0, len(req.BatchEdits) != 0} {
		if given {
			forms++
		}
	}
	if forms != 1 {
		return nil, errors.New("gomutant: an ephemeral request carries exactly one of Mutant, Edits, or BatchEdits")
	}
	switch {
	case len(req.BatchEdits) != 0:
		return t.ephemeralBatch(ctx, req.BatchEdits, req.TestPkg, req.Run, req.OracleTimeout, req.Runs, req.Progress, DeriveOracleBounds(req.OracleMemoryBytes, 1))
	case len(req.Edits) != 0:
		return t.ephemeralEdits(ctx, req.File, req.Edits, req.TestPkg, req.Run, req.OracleTimeout, req.Runs, req.Progress, DeriveOracleBounds(req.OracleMemoryBytes, 1))
	default:
		return t.ephemeral(ctx, req.File, req.Mutant, req.TestPkg, req.Run, req.OracleTimeout, req.Runs, req.Progress, DeriveOracleBounds(req.OracleMemoryBytes, 1))
	}
}

func (t *Tree) ephemeral(ctx context.Context, file string, mutant []byte, testPkg, run string, oracleTimeout time.Duration, runs int, progress func(PreparationEvent), bounds OracleBounds) (*EphemeralResult, error) {
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

	return t.runEphemeral(ctx, []fileReplacement{{File: file, Abs: abs, Source: mutant}}, testPkg, run, oracleTimeout, runs, progress, bounds)
}

// refuseUnselectedRun refuses a run pattern that selects none of the
// package's top-level names. go test reads the pattern as top-level
// alternatives of slash-separated elements and matches each element
// unanchored against the corresponding level of a test's name, so the
// first element of every alternative is matched here the same way
// against the top-level test, fuzz, and example names: any alternative
// selecting a name admits the pattern. An element that does not
// compile is left to go test's own diagnostic, and the post-probe
// zero-run check keeps guarding what only the harness decides (a
// selected test excluded by its build constraints).
// resolveTestPackage accepts the oracle package as an import path or
// as a package directory spelled the way `go test` spells one — "." or
// a "./"-prefixed path — resolved against the tree root the invocation
// named, so a module-local caller need not spell the full import path
// per probe (REQ-exec-ephemeral). A relative spelling that escapes the
// tree, or names no loaded package's directory, refuses before any
// process launches.
func (t *Tree) resolveTestPackage(testPkg string) (string, error) {
	if testPkg == "." || testPkg == ".." || strings.HasPrefix(testPkg, "./") || strings.HasPrefix(testPkg, "../") {
		clean := path.Clean(testPkg)
		if clean == ".." || strings.HasPrefix(clean, "../") {
			return "", fmt.Errorf("test package directory %q escapes the tree root", testPkg)
		}
		importPath, ok := t.eng.PackageAtDir(filepath.Join(t.dir, filepath.FromSlash(clean)))
		if !ok {
			return "", fmt.Errorf("test package directory %q holds no loaded package (relative to the tree root %s)", testPkg, t.dir)
		}
		return importPath, nil
	}
	if !t.eng.HasPackage(testPkg) {
		return "", fmt.Errorf("test package %q is not a loaded package import path", testPkg)
	}
	return testPkg, nil
}

func (t *Tree) refuseUnselectedRun(ctx context.Context, testPkg, run string) error {
	var patterns []*regexp.Regexp
	for _, first := range runPatternFirstElements(run) {
		pattern, err := regexp.Compile(first)
		if err != nil {
			return nil
		}
		patterns = append(patterns, pattern)
	}
	names, err := t.eng.RunSelectableNamesContext(ctx, testPkg)
	if err != nil {
		return err
	}
	for _, pattern := range patterns {
		for _, name := range names {
			if pattern.MatchString(name) {
				return nil
			}
		}
	}
	return fmt.Errorf("%q matched no tests in %s: nothing can attribute the mutant", run, testPkg)
}

// runPatternFirstElements splits a -run pattern exactly as the testing
// harness does — top-level '|' separates alternatives, top-level '/'
// separates the elements of one alternative, where top-level means
// outside brackets and parentheses and not escaped — and returns each
// alternative's first element, the one matched against top-level names.
func runPatternFirstElements(run string) []string {
	var firsts []string
	start, brackets, parens := 0, 0, 0
	inFirst := true
	for i := 0; i < len(run); i++ {
		switch run[i] {
		case '[':
			brackets++
		case ']':
			if brackets--; brackets < 0 {
				brackets = 0
			}
		case '(':
			if brackets == 0 {
				parens++
			}
		case ')':
			if brackets == 0 {
				parens--
			}
		case '\\':
			i++
		case '/':
			if brackets == 0 && parens == 0 && inFirst {
				firsts = append(firsts, run[start:i])
				inFirst = false
			}
		case '|':
			if brackets == 0 && parens == 0 {
				if inFirst {
					firsts = append(firsts, run[start:i])
				}
				start, inFirst = i+1, true
			}
		}
	}
	if inFirst {
		firsts = append(firsts, run[start:])
	}
	return firsts
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

// leashFor is the measurement leash a baseline runs under when the
// bank holds a measured duration for its oracle: the fixed leash
// lifted to the budget that duration would derive, never tightened.
// The fixed value is the floor the contract makes generous — a loaded
// host must not die at baseline — and the lift serves the group the
// bank knows is slow: a suite-class oracle probed on the ephemeral
// face (its leash sized for probe-class ones), or a campaign whose
// host slowed since the entry was banked (REQ-exec-oracle-budget).
func leashFor(fixed, banked time.Duration) time.Duration {
	if lifted := derivedOracleBudget(banked); lifted > fixed {
		return lifted
	}
	return fixed
}

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
func derivedBaselineRefusal(err error, leash time.Duration) error {
	var bt *engine.BaselineTimeoutError
	if errors.As(err, &bt) {
		return fmt.Errorf("baseline test ran past the %s measurement leash - the derived budget needs a measured baseline; pass an explicit oracle timeout (oracle_timeout_sec / --oracle-timeout) to bound this oracle instead", bt.Bound)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("the command deadline expired during the %s-leashed baseline measurement - raise the command timeout (timeout_sec / --timeout), or pass an explicit oracle timeout (oracle_timeout_sec / --oracle-timeout) to bound this oracle instead: %w", leash, err)
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

func (t *Tree) runEphemeral(ctx context.Context, replacements []fileReplacement, testPkg, run string, oracleTimeout time.Duration, runs int, progress func(PreparationEvent), bounds OracleBounds) (*EphemeralResult, error) {
	report := func(event PreparationEvent) {
		if progress != nil {
			progress(event)
		}
	}
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
	// The leash the baseline (derive mode) and the advisory coverage
	// probe (both modes) run under; lifted by the bank just before the
	// first probe, after every refusal that needs no bank read.
	probeLeash := ephemeralBaselineLeash
	if runs == 0 {
		runs = 1
	}
	if err := ValidateEphemeralRuns(runs); err != nil {
		return nil, err
	}
	// The build ignores what it does not compile: an overlay of a
	// build-excluded or non-Go file measures a mutant that was never
	// present, and a test package in a go test option position changes
	// the invocation being measured - both refuse before any process
	// launches (REQ-exec-ephemeral).
	testPkg, err := t.resolveTestPackage(testPkg)
	if err != nil {
		return nil, err
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
	// The pattern is matched against the package's run-selectable names
	// before any process launches: a pattern selecting none has nothing
	// to attribute the mutant to, and the baseline probe would only
	// rediscover that at its own cost (REQ-exec-preparation's loaded-set
	// stage).
	if err := t.refuseUnselectedRun(ctx, testPkg, run); err != nil {
		return nil, err
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
	if banked, hit := openBaselineBank(t.dir).longestBaselineFor(testPkg); hit {
		probeLeash = leashFor(ephemeralBaselineLeash, banked)
	}
	if derive {
		baselineBound = probeLeash
	}
	report(PreparationEvent{Stage: PreparationBaseline, Symbol: run, Package: testPkg, OracleBudget: baselineBound.String()})
	baselineStart := time.Now()
	ran, passed, diagnostic, err := testProbe(ctx, t.dir, testPkg, run, baselineBound, binFlags, env, bounds)
	if baselineCompilerCrashed(err) {
		// The baseline's compiler died: a toolchain transient, retried
		// once so it never reads as the baseline failing to build; a
		// second death is reported as the crash it is.
		report(PreparationEvent{Stage: PreparationBaseline, Symbol: run + " (compiler crashed; retrying once)", Package: testPkg, OracleBudget: baselineBound.String()})
		ran, passed, diagnostic, err = testProbe(ctx, t.dir, testPkg, run, baselineBound, binFlags, env, bounds)
		if baselineCompilerCrashed(err) {
			return nil, fmt.Errorf("compiler crashed twice on the baseline — re-run to confirm; not a verdict, and not the baseline failing to build:\n%w", err)
		}
	}
	if err != nil {
		if derive {
			return nil, derivedBaselineRefusal(err, baselineBound)
		}
		return nil, err
	}
	if ran == 0 {
		return nil, fmt.Errorf("%q matched no tests in %s: nothing can attribute the mutant", run, testPkg)
	}
	if !passed {
		// The refusal carries what the oracle saw: a baseline that
		// fails under the probe but passes for the caller's own go test
		// is otherwise undiagnosable from either side
		// (REQ-exec-ephemeral).
		refusal := fmt.Sprintf("the named test does not pass on the unmutated tree in %s: a kill against it would be fabricated", testPkg)
		if diagnostic != "" {
			refusal += "\n" + diagnostic
		}
		return nil, errors.New(refusal)
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
	var prunedImports, mutatedTests []string
	for i, replacement := range replacements {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		files[i] = replacement.File
		if strings.HasSuffix(replacement.File, "_test.go") {
			mutatedTests = append(mutatedTests, replacement.File)
		}
		// The edit digest identifies the caller's own spelling
		// (computed over the given replacements); the compiled mutant
		// is the pruned source.
		source, pruned := pruneUnusedImports(replacement.Source, func(importPath string) (string, bool) {
			return t.eng.ImportedPackageName(replacement.Abs, importPath)
		})
		for _, path := range pruned {
			prunedImports = append(prunedImports, path+" ("+replacement.File+")")
		}
		engineReplacements[i] = engine.Replacement{File: replacement.Abs, Source: source}
	}
	m := engine.Mutant{Replacements: engineReplacements}
	res := &EphemeralResult{
		Files:             files,
		TestPkg:           testPkg,
		Run:               run,
		Runs:              runs,
		OracleMemoryBytes: bounds.MemoryBytes,
		EditDigest:        ephemeralEditDigest(t.dir, replacements),
		OracleBudget:      mutantBudget.String(),
		MeasuredBaseline:  measuredBaseline.String(),
		PrunedImports:     prunedImports,
		MutatedTests:      mutatedTests,
	}
	// N runs against the once-probed baseline: per-run verdicts split a
	// deterministic kill (every run killed) from a property generator's
	// draw luck (REQ-exec-ephemeral).
	for i := 0; i < runs; i++ {
		report(PreparationEvent{Stage: PreparationMutantRun, Symbol: fmt.Sprintf("%d/%d", i+1, runs), Package: testPkg, OracleBudget: mutantBudget.String()})
		outcome, killer, evidence, diagnostic, err := runMutantEvidence(ctx, t.dir, m, []string{testPkg}, run, mutantBudget, binFlags, env, bounds)
		if err != nil {
			return nil, err
		}
		if outcome == engine.MutantDiscarded && engine.CompilerCrashed(diagnostic) {
			// A compiler signal death is the toolchain's, not the
			// mutant's: one retry tells a transient from a mutant that
			// crashes the compiler, and neither reads as "did not
			// compile" (REQ-exec-ephemeral).
			report(PreparationEvent{Stage: PreparationMutantRun, Symbol: fmt.Sprintf("%d/%d (compiler crashed; retrying once)", i+1, runs), Package: testPkg, OracleBudget: mutantBudget.String()})
			outcome, killer, evidence, diagnostic, err = runMutantEvidence(ctx, t.dir, m, []string{testPkg}, run, mutantBudget, binFlags, env, bounds)
			if err != nil {
				return nil, err
			}
			if outcome == engine.MutantDiscarded && engine.CompilerCrashed(diagnostic) {
				return nil, fmt.Errorf("compiler crashed twice on this mutant — re-run to confirm; not a verdict, and not the mutant failing to compile:\n%s", diagnostic)
			}
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
		report(PreparationEvent{Stage: PreparationCoverage, Symbol: run, Package: testPkg, OracleBudget: probeLeash.String()})
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
		if coverage, err := coveredPositions(ctx, t.dir, testPkg, run, "./...", probeLeash, binFlags, t.eng.GoEnv(), t.eng.DirectiveCoverage(), bounds); err != nil {
			// Every measured file is unknown; a mutated test file is
			// never measured, so it is not unknown either.
			for _, file := range files {
				if !strings.HasSuffix(file, "_test.go") {
					res.CoverageUnknownFiles = append(res.CoverageUnknownFiles, file)
				}
			}
			res.CoverageUnknown = len(res.CoverageUnknownFiles) > 0
		} else {
			for i, replacement := range replacements {
				if strings.HasSuffix(replacement.File, "_test.go") {
					// The coverage probe instruments the code under
					// test, never the test files: a mutated test's
					// exercise is not measured, and its verdict is
					// about the test itself (MutatedTests).
					continue
				}
				pkgPath := t.eng.FileImportPath(replacement.Abs)
				if pkgPath != "" && coverage.Unsound(pkgPath+"/"+filepath.Base(replacement.Abs)) {
					// A refused re-keying is not evidence of anything:
					// claiming "unexercised" for a file whose profile
					// entry the seam could not soundly attribute would
					// manufacture the advisory, and claiming exercised
					// would manufacture the vouch — the state is
					// UNKNOWN, said so (REQ-exec-ephemeral's
					// probe-failure posture).
					res.CoverageUnknown = true
					res.CoverageUnknownFiles = append(res.CoverageUnknownFiles, files[i])
					continue
				}
				if pkgPath == "" || !coverage.CoversFile(pkgPath+"/"+filepath.Base(replacement.Abs)) {
					res.UnexercisedFiles = append(res.UnexercisedFiles, files[i])
				}
			}
		}
		// A plain survivor over a replacement the probed run never
		// reached is no verdict at all: the file is linked but
		// unexercised, so "did not notice" would assert what the label
		// exists to deny — a guard that observes the TREE (a
		// source-reading test, a `go list`-based layering check) sees
		// the unmutated sources and can never kill. Refused, naming the
		// reachable repair; a mixed killed-some-runs outcome keeps the
		// advisory, since some run did reach it (REQ-exec-ephemeral).
		if res.KilledRuns == 0 && len(res.UnexercisedFiles) > 0 {
			return nil, fmt.Errorf("no verdict: the probed run never reached %s (linked into %s's binary, unexercised by %s) — survival would prove nothing; a guard that observes the tree (a source-reading test, a go list-based check) sees the unmutated sources: mutate the guard's own input instead, or route it to review", cappedNameList(res.UnexercisedFiles, "files"), testPkg, run)
		}
	}
	return res, nil
}

// baselineCompilerCrashed reports whether the baseline probe's error is
// its build failing under a compiler crash — the one baseline failure
// that is the toolchain's, not the tree's (a failing test's own output
// is never consulted: a test printing crash-shaped text is a failing
// test).
func baselineCompilerCrashed(err error) bool {
	var build *engine.BaselineBuildError
	return errors.As(err, &build) && engine.CompilerCrashed(build.Diagnostic)
}

func (t *Tree) ephemeralBatch(ctx context.Context, edits []BatchEdit, testPkg, run string, oracleTimeout time.Duration, runs int, progress func(PreparationEvent), bounds OracleBounds) (*EphemeralResult, error) {
	replacements, err := prepareEditBatchContext(ctx, t.dir, edits)
	if err != nil {
		return nil, err
	}
	return t.runEphemeral(ctx, replacements, testPkg, run, oracleTimeout, runs, progress, bounds)
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

func (t *Tree) ephemeralEdits(ctx context.Context, file string, edits []Edit, testPkg, run string, oracleTimeout time.Duration, runs int, progress func(PreparationEvent), bounds OracleBounds) (*EphemeralResult, error) {
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
	return t.ephemeral(ctx, file, mutant, testPkg, run, oracleTimeout, runs, progress, bounds)
}

func readFileContext(ctx context.Context, path string) ([]byte, error) {
	return contextio.ReadFile(ctx, path)
}
