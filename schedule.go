package gomutant

import (
	"context"
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/gomutant/internal/engine"
)

// The execution schedule (REQ-exec-oracle-run's verdict-preserving
// schedule): a mutant's oracle group may run as two phases — the tests
// whose baseline coverage reaches the mutated extent first, the rest
// only when no phase-one test killed — because -failfast ends a kill
// at the first failing test, so fronting the probable killers cuts the
// common kill to a fraction of the suite while a survivor still runs
// everything. Reach is probed at BATCH granularity: the group's tests
// split into ~sqrt(N) contiguous batches, each probed as one coverage
// run — per-test probes would multiply a heavy TestMain by N, while
// batches bound the extra setup cost at sqrt(N) and the phases
// partition exactly by construction (a phase is a union of whole
// batches). The schedule preserves the verdict's whole envelope:
//   - the two phases of a group share ONE oracle-timeout budget (the
//     unsplit run's bound applied in aggregate; the memory ceiling is
//     per process tree by REQ-exec-oracle-memory's own definition),
//     and a TIMEOUT under any narrowed phase is never a verdict — the
//     split's own second-process overhead is charged inside that
//     budget, so only the unsplit re-run's bound decides a timeout
//     kill, in either direction;
//   - a test-attributed kill from a narrowed phase pattern is admitted
//     only over a passing baseline of that same pattern
//     (REQ-exec-attribution's shape symmetry on the run-regex axis) —
//     otherwise the mutant re-runs unsplit and the group stops
//     scheduling (an order-dependent suite);
//   - a split pair whose individually verifiable observations merged
//     unverifiable re-runs unsplit the same way — the split must never
//     manufacture unverifiability a single process would not have had
//     (phases unverifiable alone carry the suite's own truth);
//   - serial confirmations run UNSPLIT: the corrector must not
//     reproduce the narrowing it exists to correct.
// Coverage guides the order under its advisory posture only
// (REQ-exec-survivor-evidence): a failed probe, an unsound file, a
// missing extent, or a one-sided split all degrade to today's
// unordered run — the schedule reorders execution, never narrows it.
// The survivor buckets' aggregate coverage stays a separate,
// full-pattern probe: a union of subset runs is not the same
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
// so a test can count oracle processes — the schedule is deliberately
// verdict-invisible, so process count is the one observable that pins
// a split actually executing as two phases.
var runMutantObservedEnv = engine.RunMutantObservedEnv

// phaseBaselineProbe is the phase-pattern baseline runner; a variable
// so the order-dependent-suite degrade is testable without an
// order-dependent fixture.
var phaseBaselineProbe = engine.TestProbeObservedEnv

// scheduleBatch is one probe batch: the test function names it ran
// (package-local, sorted) and the coverage they produced together.
type scheduleBatch struct {
	fns []string
	cov engine.Coverage
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
// unordered — the degrade for an order-dependent suite or a
// split-manufactured unverifiable observation.
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

// executingCandidates counts the candidates this work will actually
// run — a served record re-executes only its flagged indexes, an
// extension only its unmeasured suffix, a drift serve only its
// re-measure set — so a near-empty window never pays a probe pass it
// cannot amortize.
func executingCandidates(w work) int {
	switch {
	case w.serve != nil:
		return len(w.flagged)
	case w.extend != nil:
		return len(w.candidates) - w.extendFrom
	case w.drift != nil:
		return len(w.driftRemeasure)
	default:
		return len(w.candidates)
	}
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
		entry := &groupSchedule{}
		for _, batch := range scheduleBatches(fns) {
			cov, err := campaignCoveredPositions(ctx, t.dir, g.pkgs[0], testRunRegex(batch), coverPkg, opts.OracleTimeout, g.flags, runEnv, t.eng.DirectiveCoverage())
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				entry = &groupSchedule{}
				break
			}
			entry.batches = append(entry.batches, scheduleBatch{fns: batch, cov: cov})
		}
		store.mu.Lock()
		store.byKey[key] = entry
		store.mu.Unlock()
	}
	return nil
}

// scheduleStep is one budget window of a mutant's schedule: a group
// run whole, or a split pair — the reaching phase and its remainder —
// executing under one shared budget. The split is ONE value, so a
// remainder can never dangle without the partner whose window it
// shares, and both patterns of a split are narrowed by construction.
type scheduleStep struct {
	first     group
	remainder *group
}

func unscheduledSteps(groups []group) []scheduleStep {
	out := make([]scheduleStep, len(groups))
	for i, g := range groups {
		out[i] = scheduleStep{first: g}
	}
	return out
}

// scheduleSteps is the schedule transform: each group either stands
// whole or becomes a split step whose (reaching, remainder) patterns
// are unions of whole probe batches — together exactly the group's own
// test set, the partition REQ-exec-oracle-run's schedule clause
// demands; the reach question is survivorCovered, the one range-shaped
// probe every classification pass shares
// (REQ-exec-survivor-evidence).
func (t *Tree) scheduleSteps(w work, m engine.Mutant, opts Options) []scheduleStep {
	store := opts.scheduleStore
	if store == nil || w.shaped || w.targetView == nil || m.Extent == "" {
		return unscheduledSteps(w.groups)
	}
	coverPkg := w.targetView.subject.Package
	probe := Survivor{Position: m.Position, Extent: m.Extent}
	var out []scheduleStep
	for _, g := range w.groups {
		entry := store.get(coverageKey(g, coverPkg))
		if entry == nil || len(entry.batches) < 2 {
			out = append(out, scheduleStep{first: g})
			continue
		}
		var reaching, rest []string
		sound := true
		for _, b := range entry.batches {
			covered, ok := survivorCovered(b.cov, coverPkg, probe)
			if !ok {
				// Unsound file or unparseable position: no coverage
				// verdict exists, so no schedule either — the group
				// runs unordered (the advisory posture).
				sound = false
				break
			}
			if covered {
				reaching = append(reaching, b.fns...)
			} else {
				rest = append(rest, b.fns...)
			}
		}
		if !sound || len(reaching) == 0 || len(rest) == 0 {
			out = append(out, scheduleStep{first: g})
			continue
		}
		sort.Strings(reaching)
		sort.Strings(rest)
		gFirst, gRest := g, g
		gFirst.runRegex = testRunRegex(reaching)
		gRest.runRegex = testRunRegex(rest)
		out = append(out, scheduleStep{first: gFirst, remainder: &gRest})
	}
	return out
}

// pairManufacturedUnverifiability reports a split pair whose merged
// observation went unverifiable while each phase was individually
// sound — sealed evidence (OK) that is itself verifiable: two
// same-package processes can surface an inter-phase input divergence a
// single process would have absorbed, and the split must never
// MANUFACTURE unverifiability — the caller re-runs unsplit. A phase
// already unverifiable alone carries the suite's own truth, which an
// unsplit re-run could not improve (REQ-exec-observation's posture).
func pairManufacturedUnverifiability(a, b, merged runtimeinput.Observation) bool {
	if !merged.Unverifiable {
		return false
	}
	for _, ph := range []runtimeinput.Observation{a, b} {
		if !ph.OK || ph.Unverifiable {
			return false
		}
	}
	return true
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
func (t *Tree) phaseKillVouched(ctx context.Context, g group, opts Options, runEnv []string) bool {
	if opts.scheduleStore == nil {
		return false
	}
	key := scopedBaselineKey{pkg: g.pkgs[0], run: g.runRegex, flags: strings.Join(g.flags, "\x00"), moduleDir: g.moduleDir, packageDir: g.packageDir}
	return opts.scheduleStore.phaseBaselinePasses(ctx, key, func() bool {
		if opts.probeGate != nil {
			opts.probeGate.RLock()
			defer opts.probeGate.RUnlock()
		}
		ran, passed, _, _, err := phaseBaselineProbe(ctx, t.dir, g.pkgs[0], g.runRegex, opts.OracleTimeout, g.flags, g.moduleDir, g.packageDir, opts.BracketPaths, opts.ScratchNamespaces, runEnv)
		return err == nil && ran > 0 && passed
	})
}
