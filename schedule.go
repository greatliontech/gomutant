package gomutant

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/greatliontech/gomutant/internal/engine"
	"github.com/greatliontech/gomutant/internal/windowcost"
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
// signal — the group runs unordered and is not re-probed — and
// unscheduled then names WHY the group carries none, in the window
// estimate's words (REQ-exec-run-status): the plan's size gate, a
// failed probe batch by position, or an unvouched covering-phase kill
// (the reservation's pending marker reaches no estimate). Empty when
// the group is scheduled.
type groupSchedule struct {
	batches     []scheduleBatch
	unscheduled string
}

// Reasons a group carries no schedule signal. The pending marker is
// the reservation's value between the plan and the unit's own store —
// no estimate reads it (the plan probes every reserved unit before the
// window's estimate, or the run returns); it keeps a reserved key
// distinguishable from a scheduled one.
const (
	unscheduledPending  = "coverage probe pending"
	unscheduledDegraded = "an unvouched covering-phase kill"
)

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

// setUnscheduled records why a reserved group carries no signal.
func (s *scheduleStore) setUnscheduled(key, reason string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byKey[key] = &groupSchedule{unscheduled: reason}
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
// covering-phase kill), named as such to the window estimate.
func (s *scheduleStore) unschedule(key string) { s.setUnscheduled(key, unscheduledDegraded) }

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

// probeUnit is one group the window's probe phase will probe: its
// store key, the group, the covered package, the batches to probe
// (ceil(sqrt(N)) over the group's N oracle tests), and the pins a
// complete probe banks under (REQ-result-baseline-bank) — the oracle
// views' closure rows and the covered package's own row, taken at plan
// time from the views the decision consulted.
type probeUnit struct {
	key      string
	g        group
	coverPkg string
	// batches is the group's whole plan; banked holds the batches a
	// partial bank entry already carries, by plan position, and
	// failedBefore the prior run's failures by position — the loop
	// serves the former and names the latter on its retry.
	batches      [][]string
	banked       map[int]scheduleBatch
	failedBefore map[int]string
	bankable     bool
	evidence     []closureRow
	coverRow     closureRow
}

// toProbe counts the batches the unit will probe — the plan less the
// banked ones — the number the announcement and the ticks speak of.
func (u probeUnit) toProbe() int { return len(u.batches) - len(u.banked) }

// scheduleProbePlan decides which of the work's oracle groups the probe
// phase pays for — an unseen group with enough tests whose banked probe
// cannot serve — reserving each key and serving the bank as it decides,
// and returns the units to probe. It is the one decision the pricing
// announcement and the probing loop both consult, so the announced
// total is exactly the batches the loop runs.
func (t *Tree) scheduleProbePlan(ctx context.Context, w work, opts runOptions) ([]probeUnit, error) {
	store := opts.scheduleStore
	if store == nil || w.shaped || w.targetView == nil || executingCandidates(w) < windowcost.ScheduleMinCandidates {
		return nil, nil
	}
	coverPkg := w.targetView.subject.Package
	var plan []probeUnit
	for _, g := range w.groups {
		if err := ctx.Err(); err != nil {
			return nil, err
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
		store.byKey[key] = &groupSchedule{unscheduled: unscheduledPending}
		store.mu.Unlock()

		fns := groupTestFns(w.oracle, g.pkgs[0])
		if len(fns) < windowcost.ScheduleMinTests {
			store.setUnscheduled(key, fmt.Sprintf("fewer than %d tests", windowcost.ScheduleMinTests))
			continue
		}
		batches := scheduleBatches(fns)
		unit := probeUnit{key: key, g: g, coverPkg: coverPkg, batches: batches}
		// The bank consult (REQ-result-baseline-bank): a banked probe
		// whose pins — the group's oracle subjects AND the covered
		// package's own row, coverage speaking about both sides —
		// re-verify serves its batches without probing; any failure
		// falls through to the probe.
		// A complete entry serves whole; a partial one — a prior run
		// cut by its deadline or a failed batch — seeds the unit, which
		// probes only the failed and the unprobed batches
		// (REQ-result-baseline-bank's resume); a plan the entry does
		// not match discards it (resume's own fail-closed rule).
		if banked, hit := opts.baselineBank.coverage(key); !opts.Force && hit && w.targetView != nil {
			if closurePinsHold(banked.Evidence, groupOracleViews(w, g)) && banked.CoverRow == closureRowOf(w.targetView) {
				if seed, failed, ok := banked.resume(batches); ok {
					if banked.complete() {
						entry := &groupSchedule{}
						for i := range batches {
							entry.batches = append(entry.batches, seed[i])
						}
						store.mu.Lock()
						store.byKey[key] = entry
						store.mu.Unlock()
						continue
					}
					unit.banked, unit.failedBefore = seed, failed
				}
			}
		}
		if views := groupOracleViews(w, g); len(views) > 0 {
			unit.bankable, unit.evidence, unit.coverRow = true, closureRows(views), closureRowOf(w.targetView)
		}
		plan = append(plan, unit)
	}
	return plan, nil
}

// probePlanBatches counts the batches a plan will probe — the banked
// batches of a resumed unit are served, never counted.
func probePlanBatches(plan []probeUnit) int {
	total := 0
	for _, unit := range plan {
		total += unit.toProbe()
	}
	return total
}

// probePlanCost prices a plan from the groups' measured baselines — a
// coverage batch is one oracle process over a share of the group's
// tests, priced at the group's whole baseline, so the sum is an upper
// bound (a batch of 1/ceil(sqrt N) of the tests costs at most the
// suite) — and counts the batches of groups with no measured baseline
// as unpriced, never folded into the projection.
func probePlanCost(plan []probeUnit, baselineDur func(group) (time.Duration, bool)) (priced time.Duration, unpriced int) {
	for _, unit := range plan {
		if baselineDur != nil {
			if dur, ok := baselineDur(unit.g); ok {
				priced += dur * time.Duration(unit.toProbe())
				continue
			}
		}
		unpriced += unit.toProbe()
	}
	return priced, unpriced
}

// probeScheduleUnit probes one planned group batch by batch — every
// batch of the plan exactly once, a banked batch served in place —
// calling tick after each batch paid, deposits the unit after every
// paid batch (the bank persists per deposit, so a deadline or a kill
// keeps what completed), and stores the group's schedule. The deposit
// is seeded with the banked batches and the prior failures BEFORE the
// loop, so a deposit written partway never drops a batch or a failure
// the plan has not reached yet. A batch whose probe fails does not
// stop the unit: it is named on the analysis channel with its
// position, its tests, and the probe's own output (one payload-bearing
// event per failed batch, REQ-exec-run-status), recorded in the bank
// by position so the next run retries exactly it, and the group
// carries no signal THIS run — the narrowed-survivor rule wants every
// batch's verdict (REQ-exec-oracle-run); its key stays reserved with
// the reason.
func (t *Tree) probeScheduleUnit(ctx context.Context, unit probeUnit, opts runOptions, runEnv []string, tick func()) error {
	store := opts.scheduleStore
	entry := &groupSchedule{}
	deposit := bankedCoverage{Evidence: unit.evidence, CoverRow: unit.coverRow, Plan: len(unit.batches)}
	for i, served := range unit.banked {
		deposit.Batches = append(deposit.Batches, bankedBatchOf(i, served))
	}
	for i, reason := range unit.failedBefore {
		deposit.Failed = append(deposit.Failed, bankedFailure{Index: i, Fns: unit.batches[i], Reason: reason})
	}
	var failed []string
	for i, batch := range unit.batches {
		if err := ctx.Err(); err != nil {
			return err
		}
		if served, ok := unit.banked[i]; ok {
			entry.batches = append(entry.batches, served)
			continue
		}
		// This run's verdict on the batch replaces the prior run's.
		deposit.Failed = slices.DeleteFunc(deposit.Failed, func(f bankedFailure) bool { return f.Index == i })
		probeStart := time.Now()
		cov, err := seams.coveredPositions(ctx, t.dir, unit.g.pkgs[0], testRunRegex(batch), unit.coverPkg, opts.advisoryLeash(unit.g), unit.g.flags, runEnv, t.eng.DirectiveCoverage(), opts.bounds)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			failed = append(failed, fmt.Sprintf("%d/%d", i+1, len(unit.batches)))
			deposit.Failed = append(deposit.Failed, bankedFailure{Index: i, Fns: batch, Reason: err.Error()})
			if opts.AnalysisEvent != nil {
				opts.AnalysisEvent(AnalysisEvent{Phase: "probe-failed", Package: unit.g.pkgs[0], Detail: probeFailureDetail(i, len(unit.batches), unit.coverPkg, batch, unit.failedBefore[i], err)})
			}
		} else {
			probed := scheduleBatch{fns: batch, cov: cov, dur: time.Since(probeStart)}
			entry.batches = append(entry.batches, probed)
			deposit.Batches = append(deposit.Batches, bankedBatchOf(i, probed))
		}
		// Deposit (REQ-result-baseline-bank): the unit banks after each
		// paid batch — every passing batch a clean probe of its own,
		// every failure a record by position — so a run cut before the
		// unit completes resumes from here.
		if unit.bankable {
			slices.SortFunc(deposit.Batches, func(a, b bankedBatch) int { return a.Index - b.Index })
			slices.SortFunc(deposit.Failed, func(a, b bankedFailure) int { return a.Index - b.Index })
			opts.baselineBank.putCoverage(unit.key, deposit)
		}
		tick()
	}
	if len(failed) > 0 {
		entry = &groupSchedule{unscheduled: "coverage probe batch " + boundedNames(failed, probeFailureNames) + " failed"}
	}
	store.mu.Lock()
	store.byKey[unit.key] = entry
	store.mu.Unlock()
	return nil
}

// bankedBatchOf is the persisted form of one probed batch at its plan
// position.
func bankedBatchOf(index int, b scheduleBatch) bankedBatch {
	return bankedBatch{Index: index, Fns: b.fns, DurMillis: b.dur.Milliseconds(), Coverage: b.cov.Persist()}
}

// probeFailureDetail composes a failed batch's payload: its position,
// the covered package, its tests (bounded), the prior run's failure
// of the same batch when the bank recorded one, and the probe's own
// error — which carries the oracle's output tail.
func probeFailureDetail(index, plan int, coverPkg string, fns []string, before string, err error) string {
	detail := fmt.Sprintf("batch %d/%d over %s (%s): %v", index+1, plan, coverPkg, boundedNames(fns, probeFailureNames), err)
	if before != "" {
		detail += "; failed in the previous run too: " + before
	}
	return detail
}

// probeFailureNames bounds the test names a failed batch's payload
// spells; the remainder is counted.
const probeFailureNames = 8

func boundedNames(names []string, bound int) string {
	if len(names) <= bound {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:bound], ", ") + fmt.Sprintf(", +%d more", len(names)-bound)
}

// probeProjection renders a plan's priced cost for the announcement:
// absent when nothing is priced — an all-unpriced phase never reads as
// a zero-cost one, the estimate class's own rule.
func probeProjection(priced time.Duration) string {
	if priced == 0 {
		return ""
	}
	return roundedDuration(priced)
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

func stepBudget(g group, opts runOptions) time.Duration {
	if opts.groupBudget != nil {
		return opts.groupBudget(g)
	}
	return opts.OracleTimeout
}

func unscheduledSteps(groups []group, opts runOptions) []scheduleStep {
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
func (t *Tree) scheduleSteps(w work, m engine.Mutant, opts runOptions) []scheduleStep {
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
func (t *Tree) phaseKillVouched(ctx context.Context, g group, bound time.Duration, opts runOptions, runEnv []string) bool {
	if opts.scheduleStore == nil {
		return false
	}
	key := scopedBaselineKey{pkg: g.pkgs[0], run: g.runRegex, flags: strings.Join(g.flags, "\x00"), moduleDir: g.moduleDir, packageDir: g.packageDir}
	return opts.scheduleStore.phaseBaselinePasses(ctx, key, func() bool {
		if opts.probeGate != nil {
			opts.probeGate.RLock()
			defer opts.probeGate.RUnlock()
		}
		ran, passed, _, _, _, err := seams.killGround(ctx, t.dir, g.pkgs[0], g.runRegex, bound, g.flags, g.moduleDir, g.packageDir, opts.BracketPaths, opts.ScratchNamespaces, runEnv, opts.bounds)
		return err == nil && ran > 0 && passed
	})
}
