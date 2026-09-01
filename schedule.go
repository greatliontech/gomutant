package gomutant

import (
	"context"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/greatliontech/gomutant/internal/engine"
)

// The execution schedule (REQ-exec-oracle-run's narrowed-survivor
// clause, user ruling 2026-08-31): a mutant's oracle group runs its
// COVERING phase — the tests whose baseline coverage reaches the
// mutated extent — and, when every batch of the group carries a sound
// coverage verdict, the non-reaching remainder is EXEMPT from
// execution: a mutation alters behavior only where execution reaches
// its extent, and per-batch coverage is per-process, so an extent
// executed before or outside test bodies (init, package-level,
// TestMain) is covered by every batch and degenerates to the full run
// by construction. Reach is probed at BATCH granularity: the group's
// tests split into ~sqrt(N) contiguous batches, each probed as one
// coverage run — per-test probes would multiply a heavy TestMain by N,
// while batches bound the extra setup cost at sqrt(N) and the exempt
// set is the complement of whole batches by construction. The
// narrowed verdict keeps its envelope:
//   - the covering phase runs under the group's ONE oracle-timeout
//     budget (the unsplit run's bound; the memory ceiling is per
//     process tree by REQ-exec-oracle-memory's own definition), and a
//     TIMEOUT under the narrowed pattern is never a verdict — only the
//     unsplit re-run's bound decides a timeout kill, in either
//     direction;
//   - a KILL is never narrowed: a test-attributed kill from the
//     covering pattern is admitted only over a passing baseline of
//     that same pattern (REQ-exec-attribution's shape symmetry on the
//     run-regex axis) — otherwise the mutant re-runs unsplit and the
//     group stops scheduling (an order-dependent suite);
//   - serial confirmations and the narrowed-survivor AUDIT run
//     UNSPLIT: the corrector must not reproduce the narrowing it
//     exists to examine, and the audit's full-oracle sample is what
//     keeps the exemption's residual risk a measured quantity.
// A failed probe, an unsound file, a missing extent, or an empty
// covering or exempt side all degrade to the unordered FULL run. The
// survivor buckets' aggregate coverage stays a separate, full-pattern
// probe: a union of subset runs is not the same
// measurement (inter-test state moves branches both ways), and the
// spec pins "measured once per oracle group".

// scheduleMinCandidates and scheduleMinTests gate the probe: below
// two EXECUTING candidates the probe cannot amortize, and below eight
// tests the split saves less than the extra process it adds.
// Derivation constants like the budget multiple — incidental, not
// contract. Vars for test reach only (the runWindowCandidates
// precedent); no production code writes them.
var (
	scheduleMinCandidates = 2
	scheduleMinTests      = 8
)

// campaignCoveredPositions is the schedule's (and the survivor
// buckets') probe; a variable so tests can supply crafted coverage
// without constructing real coverage fixtures per shape.
var campaignCoveredPositions = engine.CoveredPositions

// runMutantObservedEnv is the group executor's engine call; a variable
// so a test can observe the executed patterns — the exemption's one
// direct observable is which patterns ran (the exempt remainder's
// never appears outside the audit's full runs).
var runMutantObservedEnv = engine.RunMutantObservedEnv

// phaseBaselineProbe is the phase-pattern baseline runner; a variable
// so the order-dependent-suite degrade is testable without an
// order-dependent fixture.
var phaseBaselineProbe = engine.TestProbeObservedEnv

// scheduleBatch is one probe batch: the test function names it ran
// (package-local, sorted), the coverage they produced together, and
// the probe's own wall-clock — the batch's measured cost, the window
// cost model's per-batch price (coverage instrumentation makes it a
// mild over-estimate of the plain run — a known bias of the
// projection, stated rather than corrected).
type scheduleBatch struct {
	fns []string
	cov engine.Coverage
	dur time.Duration
}

// groupSchedule is one oracle group's probed schedule signal: fewer
// than two batches (a failed or skipped probe stores none) means no
// signal — the group runs unordered and is not re-probed.
type groupSchedule struct {
	batches []scheduleBatch
}

// scheduleStore is the run-scoped schedule state. The signal map is
// written only by the serial per-window probe pass (before that
// window's workers dispatch) and by the degrade path; the phase
// baseline memo is read and written by concurrent workers validating
// phase kills — the mutex serves both, and the once map keeps two
// workers from paying one baseline twice.
type scheduleStore struct {
	mu             sync.Mutex
	byKey          map[string]*groupSchedule
	phaseBaselines map[scopedBaselineKey]*phaseBaseline
}

type phaseBaseline struct {
	done chan struct{}
	pass bool
}

func newScheduleStore() *scheduleStore {
	return &scheduleStore{byKey: map[string]*groupSchedule{}, phaseBaselines: map[scopedBaselineKey]*phaseBaseline{}}
}

func (s *scheduleStore) get(key string) *groupSchedule {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.byKey[key]
}

// unschedule clears a group's signal so every later mutant runs
// unordered — the degrade for an order-dependent suite (an unvouched
// covering-phase kill).
func (s *scheduleStore) unschedule(key string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byKey[key] = &groupSchedule{}
}

// phaseBaselinePasses reports whether a phase pattern passes ALONE on
// the unmutated tree — the shape-symmetric ground a narrowed phase
// kill needs (REQ-exec-attribution) — probing at most once per
// pattern across all workers.
func (s *scheduleStore) phaseBaselinePasses(ctx context.Context, key scopedBaselineKey, probe func() bool) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	entry, ok := s.phaseBaselines[key]
	if !ok {
		entry = &phaseBaseline{done: make(chan struct{})}
		s.phaseBaselines[key] = entry
		s.mu.Unlock()
		entry.pass = probe()
		close(entry.done)
		return entry.pass
	}
	s.mu.Unlock()
	select {
	case <-entry.done:
		return entry.pass
	case <-ctx.Done():
		// Cancellation: answer false — the caller's own ctx checks
		// abort before anything scores.
		return false
	}
}

// coverageKey is the ONE identity of a coverage probe — the schedule
// signal and the survivor-bucket cache share it, and it carries the
// probe's whole shape (the flags axis included, exactly as the
// baseline memo keys do).
func coverageKey(g group, coverPkg string) string {
	return g.pkgs[0] + "\x00" + g.runRegex + "\x00" + coverPkg + "\x00" + strings.Join(g.flags, "\x00")
}

// scheduleBatches splits sorted test names into ceil(sqrt(N))
// contiguous batches whose concatenation is exactly the input — the
// partition the phases inherit.
func scheduleBatches(fns []string) [][]string {
	n := len(fns)
	if n == 0 {
		return nil
	}
	b := int(math.Ceil(math.Sqrt(float64(n))))
	size := (n + b - 1) / b
	var out [][]string
	for start := 0; start < n; start += size {
		end := min(start+size, n)
		out = append(out, fns[start:end])
	}
	return out
}

// groupTestFns returns the group's package-local test function names,
// sorted — the same derivation pkgRuns applied when it built the
// group's pattern, so the batches speak the group's own test set.
func groupTestFns(oracle []string, pkg string) []string {
	var fns []string
	for _, sym := range oracle {
		p, fn := splitTestSymbol(sym)
		if p == pkg && fn != "" {
			fns = append(fns, fn)
		}
	}
	sort.Strings(fns)
	return fns
}

// executesCandidate is the ONE candidate selection: whether this work
// dispatches candidate mi — a served record re-executes only its
// flagged indexes, an extension only its unmeasured suffix, a drift
// serve only its re-measure set, and a pre-execution discard (no
// replacements) never dispatches. The dispatch loop and the window
// cost model share exactly this predicate.
func executesCandidate(w work, mi int) bool {
	switch {
	case w.serve != nil && !w.flagged[mi]:
		return false
	case w.extend != nil && mi < w.extendFrom:
		return false
	case w.drift != nil && !w.driftRemeasure[mi]:
		return false
	}
	_, runnable := w.candidates[mi].Mutant()
	return runnable
}

// executingIndexes lists executesCandidate's indexes in order.
func executingIndexes(w work) []int {
	var out []int
	for mi := range w.candidates {
		if executesCandidate(w, mi) {
			out = append(out, mi)
		}
	}
	return out
}

// executingCandidates counts executingIndexes — the probe-pass
// amortization gate: a near-empty window never pays a probe pass it
// cannot amortize.
func executingCandidates(w work) int {
	return len(executingIndexes(w))
}

// probeScheduleCoverage populates the store for every group of w that
// qualifies, before the window's workers dispatch. Failures are
// recorded (never re-probed) and disable scheduling for the group —
// the advisory posture.
func (t *Tree) probeScheduleCoverage(ctx context.Context, w work, opts Options, runEnv []string) error {
	store := opts.scheduleStore
	if store == nil || w.shaped || w.targetView == nil || executingCandidates(w) < scheduleMinCandidates {
		return nil
	}
	coverPkg := w.targetView.subject.Package
	for _, g := range w.groups {
		if err := ctx.Err(); err != nil {
			return err
		}
		key := coverageKey(g, coverPkg)
		store.mu.Lock()
		_, seen := store.byKey[key]
		if seen {
			store.mu.Unlock()
			continue
		}
		// Reserve the key before the long probe: the write marks the
		// group attempted even if the probe below fails partway.
		store.byKey[key] = &groupSchedule{}
		store.mu.Unlock()

		fns := groupTestFns(w.oracle, g.pkgs[0])
		if len(fns) < scheduleMinTests {
			continue
		}
		// The bank consult (REQ-result-baseline-bank): a banked probe
		// whose pins — the group's oracle subjects AND the covered
		// package's own row, coverage speaking about both sides —
		// re-verify serves its batches without probing; any failure
		// falls through to the probe.
		if banked, hit := opts.baselineBank.coverage(key); !opts.Force && hit && w.targetView != nil {
			if closurePinsHold(banked.Evidence, groupOracleViews(w, g)) && banked.CoverRow == closureRowOf(w.targetView) {
				entry := &groupSchedule{}
				for _, b := range banked.Batches {
					entry.batches = append(entry.batches, scheduleBatch{fns: b.Fns, cov: b.Coverage.Restore(), dur: time.Duration(b.DurMillis) * time.Millisecond})
				}
				store.mu.Lock()
				store.byKey[key] = entry
				store.mu.Unlock()
				continue
			}
		}
		entry := &groupSchedule{}
		for _, batch := range scheduleBatches(fns) {
			probeStart := time.Now()
			cov, err := campaignCoveredPositions(ctx, t.dir, g.pkgs[0], testRunRegex(batch), coverPkg, opts.OracleTimeout, g.flags, runEnv, t.eng.DirectiveCoverage())
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				entry = &groupSchedule{}
				break
			}
			entry.batches = append(entry.batches, scheduleBatch{fns: batch, cov: cov, dur: time.Since(probeStart)})
		}
		store.mu.Lock()
		store.byKey[key] = entry
		store.mu.Unlock()
		// Deposit (REQ-result-baseline-bank): only a complete healthy
		// probe banks — a failed pass stored the empty no-signal entry.
		if len(entry.batches) > 0 && w.targetView != nil {
			if views := groupOracleViews(w, g); len(views) > 0 {
				banked := bankedCoverage{Evidence: closureRows(views), CoverRow: closureRowOf(w.targetView)}
				for _, b := range entry.batches {
					banked.Batches = append(banked.Batches, bankedBatch{Fns: b.fns, DurMillis: b.dur.Milliseconds(), Coverage: b.cov.Persist()})
				}
				opts.baselineBank.putCoverage(key, banked)
			}
		}
	}
	return nil
}

// scheduleStep is one budget window of a mutant's schedule: a group
// run whole, or a NARROWED step whose executing pattern is the
// covering tests alone — the exempt remainder is never materialized,
// so "run the remainder" is inexpressible by construction
// (REQ-exec-oracle-run's narrowed-survivor clause).
type scheduleStep struct {
	// first is the executing pattern: the covering tests when narrowed,
	// the whole group otherwise.
	first group
	// narrowed marks a sound covering/exempt partition: the first
	// pattern is the covering tests and a surviving run is a NARROWED
	// survivor — the exempt remainder never executes
	// (REQ-exec-oracle-run's narrowed-survivor clause).
	narrowed bool
	// budget is the step's oracle bound — the source group's derived
	// budget in derive mode, the caller's explicit timeout otherwise —
	// resolved from the UNSPLIT group before any narrowing
	// (REQ-exec-oracle-run's derived campaign budget).
	budget time.Duration
}

func stepBudget(g group, opts Options) time.Duration {
	if opts.groupBudget != nil {
		return opts.groupBudget(g)
	}
	return opts.OracleTimeout
}

func unscheduledSteps(groups []group, opts Options) []scheduleStep {
	out := make([]scheduleStep, len(groups))
	for i, g := range groups {
		out[i] = scheduleStep{first: g, budget: stepBudget(g, opts)}
	}
	return out
}

// scheduleSteps is the schedule transform: each group either stands
// whole or becomes a narrowed step whose executing pattern is the
// union of whole reaching probe batches — the exempt set is the
// complement of whole batches by construction, so the covering/exempt
// split partitions the group's own test set exactly as
// REQ-exec-oracle-run's schedule clause demands; the reach question is
// survivorCovered, the one range-shaped probe every classification
// pass shares (REQ-exec-survivor-evidence).
func (t *Tree) scheduleSteps(w work, m engine.Mutant, opts Options) []scheduleStep {
	store := opts.scheduleStore
	if store == nil || w.shaped || w.targetView == nil || m.Extent == "" {
		return unscheduledSteps(w.groups, opts)
	}
	coverPkg := w.targetView.subject.Package
	probe := Survivor{Position: m.Position, Extent: m.Extent}
	var out []scheduleStep
	for _, g := range w.groups {
		budget := stepBudget(g, opts)
		reaching, ok := narrowingBatches(store.get(coverageKey(g, coverPkg)), coverPkg, probe)
		if !ok {
			out = append(out, scheduleStep{first: g, budget: budget})
			continue
		}
		var fns []string
		for _, b := range reaching {
			fns = append(fns, b.fns...)
		}
		sort.Strings(fns)
		gFirst := g
		gFirst.runRegex = testRunRegex(fns)
		out = append(out, scheduleStep{first: gFirst, narrowed: true, budget: budget})
	}
	return out
}

// narrowingBatches is the ONE covering/exempt decision: the recorded
// batches whose coverage reaches the mutant's extent, and whether the
// split is sound and non-trivial — a nil or single-batch entry, any
// batch without a sound coverage verdict, an all-reaching or
// none-reaching partition all answer false, and the group runs whole
// (the advisory posture). The schedule transform and the window cost
// model both consume this decision, so an estimate can never disagree
// with the schedule about what would execute
// (REQ-exec-oracle-run's narrowed-survivor clause).
func narrowingBatches(entry *groupSchedule, coverPkg string, probe Survivor) ([]scheduleBatch, bool) {
	if entry == nil || len(entry.batches) < 2 {
		return nil, false
	}
	var reaching []scheduleBatch
	rest := 0
	for _, b := range entry.batches {
		covered, ok := survivorCovered(b.cov, coverPkg, probe)
		if !ok {
			// Unsound file or unparseable position: no coverage
			// verdict exists, so no schedule either.
			return nil, false
		}
		if covered {
			reaching = append(reaching, b)
		} else {
			rest++
		}
	}
	if len(reaching) == 0 || rest == 0 {
		return nil, false
	}
	return reaching, true
}

// phaseKillVouched reports whether a narrowed phase's test-attributed
// kill stands: the phase's own pattern must pass alone on the
// unmutated tree (REQ-exec-attribution's shape symmetry — the full
// group baseline vouches the full pattern, never a subset of it). The
// probe holds the run's probe gate shared, exactly like every other
// producer-side oracle probe, so it can never share a window with a
// serial confirmation's scored run. A non-pass means the suite is
// order-dependent under this pattern: the caller re-runs the mutant
// unsplit and the group stops scheduling.
func (t *Tree) phaseKillVouched(ctx context.Context, g group, bound time.Duration, opts Options, runEnv []string) bool {
	if opts.scheduleStore == nil {
		return false
	}
	key := scopedBaselineKey{pkg: g.pkgs[0], run: g.runRegex, flags: strings.Join(g.flags, "\x00"), moduleDir: g.moduleDir, packageDir: g.packageDir}
	return opts.scheduleStore.phaseBaselinePasses(ctx, key, func() bool {
		if opts.probeGate != nil {
			opts.probeGate.RLock()
			defer opts.probeGate.RUnlock()
		}
		ran, passed, _, _, err := phaseBaselineProbe(ctx, t.dir, g.pkgs[0], g.runRegex, bound, g.flags, g.moduleDir, g.packageDir, opts.BracketPaths, opts.ScratchNamespaces, runEnv)
		return err == nil && ran > 0 && passed
	})
}
