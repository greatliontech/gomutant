package gomutant

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/gomutant/internal/engine"
)

// work is one target's resolved measurement state across the run's
// three phases.
type work struct {
	target int
	oracle []string
	reason string
	// shaped marks a shaped target's work: no target view, no
	// compartment ledger, wholesale serve-or-remeasure, and survivor
	// buckets stay unclassified (coverage is body semantics).
	shaped      bool
	candidates  []engine.Candidate
	groups      []group
	oracleSet   map[string]bool
	targetView  *subjectView
	oracleViews []*subjectView
	producer    *subjectViewSet
	// currentLedger is the target package's compartment ledger, read from
	// the view's observation at preparation time: the value is fixed at
	// view build (every sibling narrowing serves the union's one
	// observation), so one read here keeps phase three free of
	// per-finding view reads.
	currentLedger gofresh.TestVariantLedger
	baselines     []runtimeinput.Observation
	// serve is the prior record being served with candidate-local
	// re-execution (REQ-result-stale): only the candidate indexes in
	// flagged execute, and phase three splices their fresh outcomes and
	// evidence into the served record.
	serve   *Finding
	flagged map[int]bool
	// extend is the prior capped record whose measured prefix stands under
	// a wider budget request (REQ-mut-budget, REQ-result-stale's
	// budget-extension carve-out): candidates below extendFrom keep their
	// recorded outcomes, only the unmeasured suffix executes, and phase
	// three splices the suffix outcomes onto the record.
	extend     *Finding
	extendFrom int
	// shapedFiles are a shaped target's provenance inputs, threaded
	// from the resolve-time derivation to the stamp (nil for symbol
	// targets).
	shapedFiles []string
}

// oracleScope renders a work item's effective oracle scope — each
// group's -run pattern — for the executedScope and confirmScoped seams.
func oracleScope(w work) []string {
	scope := make([]string, len(w.groups))
	for i, g := range w.groups {
		scope[i] = g.runRegex
	}
	return scope
}

// executeWorkMutant dispatches one work item's mutant to its executor:
// shaped candidates materialize in a scratch tree (a structural oracle
// analyzes source at runtime, so the forbidden state must exist on
// disk — the overlay reaches only the oracle binary's own
// compilation), body mutants run through the build overlay.
func (t *Tree) executeWorkMutant(ctx context.Context, w work, m engine.Mutant, opts runOptions, runEnv []string) (engine.MutantOutcome, string, bool, runtimeinput.Observation, string, error) {
	if opts.executedScope != nil {
		// Emitted off the same w whose groups execute below: the
		// observation and the execution cannot diverge.
		opts.executedScope(m.Position, m.Operator, oracleScope(w))
	}
	if w.shaped {
		// Shaped candidates execute their explicit oracle inventory.
		outcome, killer, memoryDecided, err := t.executeShapedCandidate(ctx, w, m, opts, runEnv)
		return outcome, killer, memoryDecided, runtimeinput.Observation{}, "", err
	}
	return t.executeMutant(ctx, w, m, opts, runEnv)
}

// confirmMutant is the serial kill confirmation's execution
// (REQ-exec-attribution): a test-attributed kill runs KILLER-SCOPED
// first — the killing test alone, same oracle bounds — and an
// attributed test kill from that run is the scored measurement,
// admitted only over a passing KILLER-SCOPED BASELINE of the
// unmutated tree: differential attribution needs the vouching pass
// to share the mutant run's shape (REQ-exec-attribution-symmetry's
// "differ in the overlay alone", here on the run-regex axis — a
// killer that fails standalone regardless of the mutant, a
// prefix-dependent setup ordering, must never convert a
// sibling-induced false kill into a confirmed one). The scoped
// baseline memoizes per (group, killer) across the campaign — one
// extra one-test probe per distinct killer, not per confirmation.
// Anything weaker than an attributed scoped kill over a passing
// scoped baseline — the killer passing, a timeout either side, a
// discard, an unscopable killer — falls back to the serial run of the
// candidate's whole oracle scope, whose verdict scores: a survivor
// verdict always rests on every complete current oracle group.
func (t *Tree) confirmMutant(ctx context.Context, w work, m engine.Mutant, killer string, scopedBaselines map[scopedBaselineKey][]string, opts runOptions, runEnv []string) (engine.MutantOutcome, string, bool, runtimeinput.Observation, string, error) {
	if opts.confirmScoped != nil {
		// Emitted off the same w both confirmation paths derive from:
		// the serial fallback executes w.groups verbatim, and the
		// killer-scoped branch runs its killer sub-probe within one of
		// those groups — the candidate's group scope, which is what
		// this seam reports.
		opts.confirmScoped(m.Position, m.Operator, oracleScope(w))
	}
	confirmBudget := opts.OracleTimeout
	scopedGroup := func() *group {
		if w.shaped || killer == TimeoutKiller || strings.HasPrefix(killer, PackageKillerPrefix) {
			return nil
		}
		pkg, fn := splitTestSymbol(killer)
		if pkg == "" || fn == "" {
			return nil
		}
		// Defensive: the engine's attribution already truncates a
		// subtest to its top-level function; a slash here would still
		// scope soundly to the parent (which runs its children).
		if i := strings.IndexByte(fn, '/'); i > 0 {
			fn = fn[:i]
		}
		for i := range w.groups {
			g := &w.groups[i]
			// Group constructors build single-package groups; the
			// length check is defensive against a future multi-pkg
			// shape, which would fall back to the full oracle.
			if len(g.pkgs) == 1 && g.pkgs[0] == pkg {
				scoped := *g
				scoped.runRegex = "^" + regexp.QuoteMeta(fn) + "$"
				confirmBudget = stepBudget(*g, opts)
				return &scoped
			}
		}
		return nil
	}()
	var scopedPasses []string
	if scopedGroup != nil {
		scopedPasses = t.scopedBaselinePasses(ctx, *scopedGroup, scopedBaselines, opts, runEnv)
	}
	if slices.Contains(scopedPasses, killer) {
		out, gk, confirmMemoryDecided, state, incomplete, diagnostic, err := seams.runMutantObserved(ctx, t.dir, m, scopedGroup.pkgs, scopedGroup.runRegex, confirmBudget, scopedGroup.flags, scopedGroup.moduleDir, scopedGroup.packageDir, opts.BracketPaths, opts.ScratchNamespaces, runEnv, opts.bounds)
		if err != nil {
			return out, gk, confirmMemoryDecided, runtimeinput.Observation{}, "", fmt.Errorf("%s: mutant %s %s: killer-scoped confirmation: %w", m.Symbol, m.Position, m.Operator, err)
		}
		if diagnostic == "" && out == engine.MutantKilled && gk != TimeoutKiller && !strings.HasPrefix(gk, PackageKillerPrefix) {
			if !w.oracleSet[gk] {
				aerr := attributedKill(gk, w.oracleSet, scopedPasses)
				return out, gk, confirmMemoryDecided, runtimeinput.Observation{}, "", fmt.Errorf("%s: mutant %s %s: %w", m.Symbol, m.Position, m.Operator, aerr)
			}
			if slices.Contains(scopedPasses, gk) {
				merged, err := mergeFindingObservationsContext(ctx, t.dir, runEnv, state)
				if err != nil {
					return out, gk, confirmMemoryDecided, runtimeinput.Observation{}, "", fmt.Errorf("%s: merge runtime observations: %w", m.Symbol, err)
				}
				return out, gk, confirmMemoryDecided, merged, incomplete, nil
			}
		}
	}
	// The serial fallback runs the complete oracle scope.
	out, gk, md, state, incomplete, err := t.executeWorkMutant(ctx, w, m, opts, runEnv)
	return out, gk, md, state, incomplete, err
}

// scopedBaselineKey identifies one killer-scoped baseline probe: the
// memo's identity is the probe's whole shape.
type scopedBaselineKey struct {
	pkg, run, flags, moduleDir, packageDir string
}

// scopedBaselinePasses reports the tests that actually passed the scoped
// unmutated run — the killer-scoped stage's differential
// ground — memoized in the caller's Run-local map (this Run's probe
// gate serializes every confirmation, so the memo needs no lock, and
// Run-locality is what keeps the key complete across bounds). Any
// non-pass — a standalone failure, a timeout, a probe error —
// answers no passes and the confirmation falls back to the full oracle;
// the probe is best-effort ground, never a campaign abort.
func (t *Tree) scopedBaselinePasses(ctx context.Context, g group, memo map[scopedBaselineKey][]string, opts runOptions, runEnv []string) []string {
	key := scopedBaselineKey{pkg: g.pkgs[0], run: g.runRegex, flags: strings.Join(g.flags, "\x00"), moduleDir: g.moduleDir, packageDir: g.packageDir}
	if passed, ok := memo[key]; ok {
		return passed
	}
	baseline, err := seams.killGround(ctx, t.dir, g.pkgs[0], g.runRegex, opts.OracleTimeout, g.flags, g.moduleDir, g.packageDir, opts.BracketPaths, opts.ScratchNamespaces, runEnv, opts.bounds)
	var passed []string
	if err == nil && baseline.Passed {
		passed = baseline.Tests
	}
	memo[key] = passed
	return passed
}

// executeMutant runs complete unsplit oracle groups and merges their
// observations. Coverage is advisory and is not an input to dispatch.
func (t *Tree) executeMutant(ctx context.Context, w work, m engine.Mutant, opts runOptions, runEnv []string) (engine.MutantOutcome, string, bool, runtimeinput.Observation, string, error) {
	outcome := engine.MutantSurvived
	killer := ""
	memoryDecided := false
	incompleteReason := ""
	var processStates []runtimeinput.Observation
	for _, g := range w.groups {
		if err := ctx.Err(); err != nil {
			return outcome, killer, memoryDecided, runtimeinput.Observation{}, "", err
		}
		if outcome != engine.MutantSurvived {
			break
		}
		out, who, decided, state, incomplete, diagnostic, err := seams.runMutantObserved(ctx, t.dir, m, g.pkgs, g.runRegex, stepBudget(g, opts), g.flags, g.moduleDir, g.packageDir, opts.BracketPaths, opts.ScratchNamespaces, runEnv, opts.bounds)
		if err != nil {
			return outcome, killer, memoryDecided, runtimeinput.Observation{}, "", fmt.Errorf("%s: mutant %s %s: %w", m.Symbol, m.Position, m.Operator, err)
		}
		if diagnostic == "" && out == engine.MutantKilled && who != TimeoutKiller {
			if err := attributedKill(who, w.oracleSet, g.passed); err != nil {
				return outcome, killer, memoryDecided, runtimeinput.Observation{}, "", fmt.Errorf("%s: mutant %s %s: %w", m.Symbol, m.Position, m.Operator, err)
			}
		}
		outcome, killer = out, who
		memoryDecided = memoryDecided || decided
		if incompleteReason == "" {
			incompleteReason = incomplete
		}
		if diagnostic == "" {
			// Build discards contributed no process; preceding groups'
			// observations still belong to their executions.
			processStates = append(processStates, state)
		}
	}
	if err := ctx.Err(); err != nil {
		return outcome, killer, memoryDecided, runtimeinput.Observation{}, "", err
	}
	state, err := mergeFindingObservationsContext(ctx, t.dir, runEnv, processStates...)
	if err != nil {
		return outcome, killer, memoryDecided, runtimeinput.Observation{}, "", fmt.Errorf("%s: merge runtime observations: %w", m.Symbol, err)
	}
	return outcome, killer, memoryDecided, state, incompleteReason, nil
}

// windowScores is one window's verdict-bearing measurement for one
// target: outcomes, their killers, and the serial-confirmation flips,
// indexed by candidate. The three travel as one value because every
// survivor-assembly path needs all of them — a signature that could
// accept outcomes and killers while dropping the flips is exactly the
// shape that produced unmarked flipped survivors
// (REQ-exec-survivor-evidence).
type windowScores struct {
	outcomes []engine.MutantOutcome
	killers  []string
	flips    map[int]string
	// memoryDecided marks, per candidate, a kill whose verdict the
	// oracle memory ceiling decided (REQ-exec-oracle-memory); nil at
	// carry-prior sites, whose candidates did not run.
	memoryDecided []bool
}

// anyMemoryDecided reports whether any executed candidate's verdict
// was memory-decided — the finding-level fact that pins the exact
// ceiling (REQ-result-stale).
func (s windowScores) anyMemoryDecided() bool {
	for _, decided := range s.memoryDecided {
		if decided {
			return true
		}
	}
	return false
}

// mergeScoredFacts joins one execution window's ceiling facts into the
// record — the ONE entry every assembly arm (fresh, extend, splice,
// grow, drift) uses, so an arm cannot drop them
// (REQ-exec-oracle-memory, REQ-result-stale). A memory-decided verdict
// joins the record's ceiling-decided fact; and because the merged
// record now carries verdicts measured under both its recorded ceiling
// and the current one, the pin raises to the larger effective ceiling —
// the directional serve's premise is that the recorded pin bounds
// every verdict's measurement ceiling.
func mergeScoredFacts(rec Finding, scores windowScores, currentPin int64, currentBudget time.Duration, budgetDerived bool) Finding {
	rec.OracleCeilingDecided = rec.OracleCeilingDecided || scores.anyMemoryDecided()
	if effectiveCeiling(currentPin) > effectiveCeiling(rec.OracleMemoryBytes) {
		rec.OracleMemoryBytes = currentPin
	}
	// The oracle budget mirrors the ceiling's directional pin: a
	// derived record's OracleTimeout is the LOOSEST bound any of its
	// verdicts ran under, raised as fresh verdicts join under wider
	// derived budgets (REQ-result-stale's timeout-kill rule).
	if budgetDerived || rec.OracleTimeoutDerived {
		if prior, err := time.ParseDuration(rec.OracleTimeout); err == nil && currentBudget > prior {
			rec.OracleTimeout = currentBudget.String()
		}
		rec.OracleTimeoutDerived = rec.OracleTimeoutDerived || budgetDerived
	}
	return rec
}

// maxGroupBudget is the loosest bound any of the work's verdict-bearing
// processes ran under — the value a derived record pins.
func maxGroupBudget(w work, opts runOptions) time.Duration {
	budget := time.Duration(0)
	for _, g := range w.groups {
		if b := stepBudget(g, opts); b > budget {
			budget = b
		}
	}
	return budget
}

// carrySurvivor is the carry-prior constructor: the candidate did not
// execute this run, so no new flip can exist — the recorded row's
// advisory evidence carries verbatim, its WithdrawnKiller included (a
// recorded flip is evidence exactly like a bucket; rebuilding from the
// bucket alone silently dropped the killer's name).
func carrySurvivor(candidate engine.Candidate, prior Survivor) Survivor {
	return Survivor{Position: candidate.Position, Operator: candidate.Operator, Site: candidate.Site, Extent: candidate.Extent, Execution: prior.Execution, WithdrawnKiller: prior.WithdrawnKiller, OtherSelection: prior.OtherSelection}
}

// newSurvivor is the ONE survivor-row constructor: every assembly path
// — fresh, served splice, grown delta, extend suffix, drift re-measure,
// and the two carry-prior sites — builds its rows here, deciding the
// flip question at the call (REQ-exec-survivor-evidence). flips is the
// window's confirmation-flip map for candidates that EXECUTED this run
// (nil at carry-prior sites, whose candidates did not run and cannot
// have flipped): a withdrawn window kill marks the row flipped-kill
// with its killer named. A helper applied post-hoc was tried first and
// silently missed two of the assembly paths in consecutive review
// rounds — construction is where omission becomes uncompilable.
func newSurvivor(candidate engine.Candidate, execution string, flips map[int]string, mi int) Survivor {
	row := Survivor{Position: candidate.Position, Operator: candidate.Operator, Site: candidate.Site, Extent: candidate.Extent, Execution: execution}
	if withdrawn, flipped := flips[mi]; flipped {
		row.Execution = "flipped-kill"
		row.WithdrawnKiller = withdrawn
	}
	return row
}

// bucketSurvivorExecution classifies why each survivor lived
// (REQ-exec-survivor-evidence): unverifiable runtime evidence buckets
// every survivor "unstable-oracle" without probing; otherwise one
// baseline coverage probe per oracle group (cached across the run's
// targets sharing a group and cover package) decides "coverage-unobserved"
// versus "executed-and-passed" by whether any executed block spans the
// mutated position. A negative parent profile establishes neither absent
// child execution nor absent compile-time influence. Advisory data on the unmutated tree,
// never a measurement pin. Survivors below from keep their recorded
// buckets verbatim: a splice carries its served prefix's advisory data —
// measured under the record's own verifiable conditions — like its
// dispositions and attestations, immune to a suffix-local divergence
// stamp or re-probe.
func (t *Tree) bucketSurvivorExecution(ctx context.Context, f *Finding, w work, opts runOptions, runEnv []string, cache map[string]engine.Coverage, from int) error {
	if from >= len(f.Survivors) {
		return nil
	}
	if unstableForBuckets(f, opts.Exemptions) {
		for si := from; si < len(f.Survivors); si++ {
			// A flip is direct execution evidence recorded at
			// confirmation time - the position demonstrably executed
			// and a test demonstrably failed on it - so the advisory
			// unverifiability stamp never overwrites it
			// (REQ-exec-survivor-evidence).
			if f.Survivors[si].Execution == "flipped-kill" {
				continue
			}
			f.Survivors[si].Execution = "unstable-oracle"
		}
		return nil
	}
	// The mutant executes through the build overlay, so an oracle whose
	// observed union read any mutated file's on-disk bytes derived its
	// verdict from the unmutated tree - the false-survivor channel a
	// disk-walking oracle opens. The union read is target-scoped truth:
	// every re-measured survivor buckets overlay-bypassed, the
	// labeled-never-silent direction (REQ-exec-survivor-evidence).
	if overlayBypassedRead(f, w) {
		for si := from; si < len(f.Survivors); si++ {
			f.Survivors[si].Execution = "overlay-bypassed"
		}
		return nil
	}
	coverage, probed, err := t.oracleCoverage(ctx, w, opts, runEnv, cache)
	if err != nil {
		return err
	}
	if !probed {
		// Best-effort: an unprobeable oracle leaves the bucket empty
		// rather than failing a run whose measurement is already sound.
		return nil
	}
	coverPkg := w.targetView.subject.Package
	for si := from; si < len(f.Survivors); si++ {
		if f.Survivors[si].Execution == "flipped-kill" {
			// The flip outranks the coverage probe: it is per-mutant
			// execution evidence, not baseline-advisory
			// (REQ-exec-survivor-evidence).
			continue
		}
		if f.Survivors[si].Execution == "covering-passed" {
			// Preserve a carried historical bucket. A fresh execution
			// never mints this class; the reuse gate decides whether a
			// historical row can be carried at all.
			continue
		}
		covered, ok := survivorCovered(coverage, coverPkg, f.Survivors[si])
		if !ok {
			// An unparseable position leaves the bucket UNSET — the
			// best-effort posture — never a claimed never-executed.
			continue
		}
		if covered {
			f.Survivors[si].Execution = "executed-and-passed"
		} else {
			f.Survivors[si].Execution = "coverage-unobserved"
		}
	}
	return nil
}

// survivorCovered is the one coverage question every bucket decision
// asks — fresh classification and the drift re-derivation alike: does
// any executed block intersect the mutated node's extent? A point
// anchor sits on toolchain-dependent block boundaries (go1.27 moved
// body spans off the brace token), so the range is the probe;
// extent-less records — prior generations — keep the anchor-point
// form they were bucketed under.
func survivorCovered(coverage engine.Coverage, coverPkg string, s Survivor) (covered, ok bool) {
	file, line, col, posOK := splitSurvivorPosition(s.Position)
	if !posOK {
		// Not a coverage verdict: the caller owns unparseable-position
		// policy (fresh classification leaves the bucket unset; the
		// drift re-derivation leaves the recorded bucket standing).
		return false, false
	}
	if coverage.Unsound(coverPkg + "/" + file) {
		// The directive seam refused this file's re-keying
		// (contention or collision): its profile entries are absent
		// or polluted, so a verdict either way would be manufactured
		// — the bucket stays empty (REQ-exec-survivor-evidence's
		// fail-closed posture).
		return false, false
	}
	if sl, sc, el, ec, extOK := parseSurvivorExtent(s.Extent); extOK {
		return coverage.Intersects(coverPkg+"/"+file, sl, sc, el, ec), true
	}
	return coverage.Covered(coverPkg+"/"+file, line, col), true
}

// parseSurvivorExtent splits "line:col-line:col"; ok is false on the
// empty extents of prior-generation records. A carried survivor may
// pair a current candidate's extent with a bucket recorded under the
// point probe — the extent describes the mutant, never which probe
// produced the bucket.
func parseSurvivorExtent(extent string) (startLine, startCol, endLine, endCol int, ok bool) {
	if extent == "" {
		return 0, 0, 0, 0, false
	}
	if n, err := fmt.Sscanf(extent, "%d:%d-%d:%d", &startLine, &startCol, &endLine, &endCol); err != nil || n != 4 {
		return 0, 0, 0, 0, false
	}
	if fmt.Sprintf("%d:%d-%d:%d", startLine, startCol, endLine, endCol) != extent {
		// Sscanf tolerates trailing bytes; a round-trip mismatch is a
		// malformed extent, and malformed means the point fallback.
		return 0, 0, 0, 0, false
	}
	return startLine, startCol, endLine, endCol, true
}

// overlayBypassedRead reports whether the finding's observed union
// recorded a read of any mutated file's own path: the replacements'
// catalog paths and the finalized manifest identities are both
// absolute, so the comparison is exact. Best-effort advisory - an
// unreadable manifest leaves the coverage buckets to speak.
func overlayBypassedRead(f *Finding, w work) bool {
	if f.TargetEvidence.RuntimeInputs == "" || w.targetView == nil {
		return false
	}
	mutated := map[string]bool{}
	for _, candidate := range w.candidates {
		for _, replacement := range candidate.Replacements {
			mutated[replacement.File] = true
		}
	}
	if len(mutated) == 0 {
		return false
	}
	paths, err := runtimeinput.Paths(f.TargetEvidence.RuntimeInputs, evidenceBase(w.targetView.evidenceDir, f.TargetEvidence))
	if err != nil {
		return false
	}
	for _, path := range paths {
		if mutated[path] {
			return true
		}
	}
	return false
}

// splitSurvivorPosition splits "file.go:line:col" (an occurrence suffix
// "#n" stripped from the column).
func splitSurvivorPosition(position string) (file string, line, col int, ok bool) {
	parts := strings.Split(position, ":")
	if len(parts) != 3 {
		return "", 0, 0, false
	}
	colPart, _, _ := strings.Cut(parts[2], "#")
	line, lineErr := strconv.Atoi(parts[1])
	col, colErr := strconv.Atoi(colPart)
	if lineErr != nil || colErr != nil {
		return "", 0, 0, false
	}
	return parts[0], line, col, true
}

// oracleCoverage merges the baseline coverage probes of a work item's oracle
// groups over the target's package, cached per (package, run pattern, cover
// package) across the run; probed reports false when any group cannot be
// probed (best-effort advisory data, never a refusal).
func (t *Tree) oracleCoverage(ctx context.Context, w work, opts runOptions, runEnv []string, cache map[string]engine.Coverage) (engine.Coverage, bool, error) {
	coverPkg := w.targetView.subject.Package
	coverage := engine.Coverage{}
	for _, g := range w.groups {
		if err := ctx.Err(); err != nil {
			return engine.Coverage{}, false, err
		}
		key := coverageKey(g, coverPkg)
		got, ok := cache[key]
		if !ok {
			probed, err := seams.coveredPositions(ctx, t.dir, g.pkgs[0], g.runRegex, coverPkg, opts.advisoryLeash(g), g.flags, runEnv, t.eng.DirectiveCoverage(), opts.bounds)
			if err != nil {
				if ctx.Err() != nil {
					return engine.Coverage{}, false, ctx.Err()
				}
				return engine.Coverage{}, false, nil
			}
			got = probed
			cache[key] = got
		}
		coverage = coverage.Merge(got)
	}
	return coverage, true, nil
}

// attributedKill enforces the oracle as the sole arbiter (REQ-target-oracle,
// REQ-exec-attribution): a kill must name an oracle test, a timeout, or a
// probe-confirmed package failure. A named killer outside the oracle means
// the run pattern matched a test the target never claimed — an
// unattributable measurement, aborted rather than scored.
func attributedKill(killer string, oracleSet map[string]bool, baselinePassed []string) error {
	if killer == TimeoutKiller || strings.HasPrefix(killer, PackageKillerPrefix) {
		return nil
	}
	if oracleSet[killer] {
		return baselineAllowsKill(killer, baselinePassed)
	}
	return fmt.Errorf("killed by %s, which is not in the target's oracle", killer)
}

// baselineAllowsKill checks the named test's own terminal disposition. Pattern
// membership and a different test's pass cannot turn a skipped test into a pass.
func baselineAllowsKill(killer string, passed []string) error {
	if killer == TimeoutKiller || strings.HasPrefix(killer, PackageKillerPrefix) || slices.Contains(passed, killer) {
		return nil
	}
	return fmt.Errorf("killed by %s, which did not pass in the pre-measurement baseline; no finding recorded", killer)
}

// TimeoutKiller and PackageKillerPrefix re-export the engine's kill
// attributions for callers reading finding output.
const (
	TimeoutKiller       = engine.TimeoutKiller
	PackageKillerPrefix = engine.PackageKillerPrefix
)
