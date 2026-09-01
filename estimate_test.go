package gomutant

import (
	"fmt"
	"testing"
	"time"

	"github.com/greatliontech/gomutant/internal/engine"
)

// The window cost model prices only what the run measured: a narrowed
// candidate costs its reaching batches, a whole-group one its group's
// baseline, and a candidate with any unpriced part is COUNTED but
// never folded into the bound — the model fabricates no duration
// (REQ-exec-run-status's estimate class). The narrowing decision is
// the schedule's own (narrowingBatches), so the classes here mirror
// exactly what the executor would run.
func TestEstimateWindowPricesOnlyMeasuredDurations(t *testing.T) {
	const pkg, coverPkg = "example.com/p", "example.com/p"
	fns := []string{"TestA", "TestB", "TestC", "TestD"}
	w := scheduleTestWork(pkg, coverPkg, fns)
	reach := engine.CoverageForTest(map[string][]engine.CoverSpanForTest{coverPkg + "/f.go": {{StartLine: 9, StartCol: 1, EndLine: 20, EndCol: 1}}})
	store := newScheduleStore()
	store.byKey[coverageKey(w.groups[0], coverPkg)] = &groupSchedule{batches: []scheduleBatch{
		{fns: []string{"TestA", "TestB"}, cov: reach, dur: 3 * time.Second},
		{fns: []string{"TestC", "TestD"}, cov: engine.CoverageForTest(nil), dur: 5 * time.Second},
	}}
	repl := []engine.Replacement{{File: "f.go"}}
	w.candidates = []engine.Candidate{
		// Covered extent → narrowed: priced at the reaching batch (3s).
		{Symbol: "S", Operator: "op", Position: "f.go:10:2", Extent: "10:2-12:3", Replacements: repl},
		// Extent outside every recorded span → none-reaching partition
		// degrades to the whole group: priced at the baseline (60s).
		{Symbol: "S", Operator: "op", Position: "f.go:40:2", Extent: "40:2-41:3", Replacements: repl},
		// No extent → whole group.
		{Symbol: "S", Operator: "op", Position: "f.go:50:2", Replacements: repl},
		// Non-runnable (no replacements) → not an executing candidate.
		{Symbol: "S", Operator: "op", Position: "f.go:60:2", Extent: "60:2-61:3"},
	}
	baseline := func(group) (time.Duration, bool) { return time.Minute, true }

	est := estimateWindow([]work{w}, store, baseline, 4)
	if est.narrowed != 1 || est.full != 2 || est.unknown != 0 {
		t.Fatalf("classes = %d narrowed, %d full, %d unknown; want 1/2/0", est.narrowed, est.full, est.unknown)
	}
	if want := 3*time.Second + 2*time.Minute; est.projected != want {
		t.Fatalf("bound = %v, want %v (reaching batch + two whole-group baselines)", est.projected, want)
	}
	// One narrowed candidate → one worst-case audit at the work's full
	// oracle (its single group's baseline).
	if est.auditProjected != time.Minute {
		t.Fatalf("audit bound = %v, want %v", est.auditProjected, time.Minute)
	}

	// Without a measured baseline nothing whole-group is priced: the
	// narrowed candidate keeps its batch price, the whole-group ones
	// count unpriced, and the audit bound prices nothing.
	est = estimateWindow([]work{w}, store, nil, 4)
	if est.narrowed != 1 || est.full != 0 || est.unknown != 2 {
		t.Fatalf("unpriced classes = %d narrowed, %d full, %d unknown; want 1/0/2", est.narrowed, est.full, est.unknown)
	}
	if est.projected != 3*time.Second {
		t.Fatalf("unpriced bound = %v, want the reaching batch alone", est.projected)
	}
	if est.auditProjected != 0 {
		t.Fatalf("unpriced audit bound = %v, want 0 — no fabricated full-oracle price", est.auditProjected)
	}

	// A batch recorded without a duration poisons only the candidates
	// narrowed onto it: the candidate counts unpriced, never a
	// fabricated zero-cost narrowing.
	store.byKey[coverageKey(w.groups[0], coverPkg)].batches[0].dur = 0
	est = estimateWindow([]work{w}, store, baseline, 4)
	if est.unknown != 1 || est.narrowed != 0 {
		t.Fatalf("dur-less batch: %d narrowed, %d unknown; want 0/1", est.narrowed, est.unknown)
	}
	if want := 2 * time.Minute; est.projected != want {
		t.Fatalf("dur-less batch bound = %v, want %v", est.projected, want)
	}

	// The audit projection derives the SAME savings-based cap the
	// executed audit uses: six narrowed candidates model ~6x57s of
	// savings against a 60s unit — below auditShareDivisor x 2 — so
	// the projection floors at ONE re-run, not the ceiling.
	store.byKey[coverageKey(w.groups[0], coverPkg)].batches[0].dur = 3 * time.Second
	many := w
	many.candidates = nil
	for i := 0; i < 6; i++ {
		many.candidates = append(many.candidates, engine.Candidate{Symbol: "S", Operator: "op", Position: "f.go:10:2", Extent: "10:2-12:3", Replacements: repl})
	}
	est = estimateWindow([]work{many}, store, baseline, 4)
	if est.narrowed != 6 {
		t.Fatalf("narrowed = %d, want 6", est.narrowed)
	}
	if want := time.Minute; est.auditProjected != want {
		t.Fatalf("audit projection = %v, want the derived floor x full oracle = %v", est.auditProjected, want)
	}
	// Enough modeled savings leaves the floor: twenty narrowed
	// candidates model ~20x57s against the 60s unit — share 2.
	sated := w
	sated.candidates = nil
	for i := 0; i < 20; i++ {
		sated.candidates = append(sated.candidates, engine.Candidate{Symbol: "S", Operator: "op", Position: "f.go:10:2", Extent: "10:2-12:3", Replacements: repl})
	}
	est = estimateWindow([]work{sated}, store, baseline, 4)
	if want := 2 * time.Minute; est.auditProjected != want {
		t.Fatalf("audit projection = %v, want the derived share x full oracle = %v", est.auditProjected, want)
	}
}

// auditSample truncates the hash-ordered pool at the savings-derived
// depth: twenty narrowed survivors modeling ~57s of savings each
// against a 60s unit derive share 2 — a collapse to the constant
// floor (or to the fixed ceiling) moves the sampled count. The
// selection is content-stable: two calls over the same window return
// identical picks (REQ-exec-oracle-run's savings-derived audit
// bound).
func TestAuditSampleDerivesDepthFromSavings(t *testing.T) {
	const pkg, coverPkg = "example.com/p", "example.com/p"
	w := scheduleTestWork(pkg, coverPkg, []string{"TestA", "TestB", "TestC", "TestD"})
	reach := engine.CoverageForTest(map[string][]engine.CoverSpanForTest{coverPkg + "/f.go": {{StartLine: 9, StartCol: 1, EndLine: 20, EndCol: 1}}})
	store := newScheduleStore()
	store.byKey[coverageKey(w.groups[0], coverPkg)] = &groupSchedule{batches: []scheduleBatch{
		{fns: []string{"TestA", "TestB"}, cov: reach, dur: 3 * time.Second},
		{fns: []string{"TestC", "TestD"}, cov: engine.CoverageForTest(nil), dur: 5 * time.Second},
	}}
	repl := []engine.Replacement{{File: "f.go"}}
	for i := 0; i < 20; i++ {
		w.candidates = append(w.candidates, engine.Candidate{Symbol: "S", Operator: "op", Position: fmt.Sprintf("f.go:10:%d", i+2), Extent: "10:2-12:3", Replacements: repl})
	}
	baseline := func(group) (time.Duration, bool) { return time.Minute, true }
	all := func(int, int) bool { return true }
	sym := func(int) string { return "example.com/p.S" }

	picks := auditSample([]work{w}, sym, all, store, baseline)
	if len(picks) != 2 {
		t.Fatalf("audit depth = %d, want the derived share 2 — neither the floor nor the ceiling", len(picks))
	}
	again := auditSample([]work{w}, sym, all, store, baseline)
	if len(again) != 2 || again[0] != picks[0] || again[1] != picks[1] {
		t.Fatalf("selection not content-stable: %v vs %v", again, picks)
	}
	// No narrowed survivor, no audit.
	if got := auditSample([]work{w}, sym, func(int, int) bool { return false }, store, baseline); len(got) != 0 {
		t.Fatalf("empty pool sampled %d", len(got))
	}
}

// The estimator prices the same candidate selection the dispatcher
// executes — the shared executesCandidate predicate: a served
// record's flagged indexes only, an extension's unmeasured suffix
// only, a drift serve's re-measure set only — a window prices (and
// counts) nothing it will not run.
func TestEstimateWindowMirrorsExecutingSelection(t *testing.T) {
	const pkg, coverPkg = "example.com/p", "example.com/p"
	base := scheduleTestWork(pkg, coverPkg, []string{"TestA", "TestB", "TestC", "TestD"})
	repl := []engine.Replacement{{File: "f.go"}}
	base.candidates = []engine.Candidate{
		{Symbol: "S", Operator: "op", Position: "f.go:10:2", Replacements: repl},
		{Symbol: "S", Operator: "op", Position: "f.go:20:2", Replacements: repl},
		{Symbol: "S", Operator: "op", Position: "f.go:30:2", Replacements: repl},
	}
	baseline := func(group) (time.Duration, bool) { return time.Minute, true }

	served := base
	served.serve = &Finding{}
	served.flagged = map[int]bool{1: true}
	extended := base
	extended.extend = &Finding{}
	extended.extendFrom = 2
	drifted := base
	drifted.drift = &Finding{}
	drifted.driftRemeasure = map[int]bool{0: true, 2: true}

	for _, tc := range []struct {
		name string
		w    work
		want int
	}{
		{"serve prices flagged only", served, 1},
		{"extension prices the suffix only", extended, 1},
		{"drift prices the re-measure set only", drifted, 2},
	} {
		est := estimateWindow([]work{tc.w}, newScheduleStore(), baseline, 4)
		if est.full != tc.want || est.narrowed != 0 || est.unknown != 0 {
			t.Fatalf("%s: classes = %d/%d/%d, want %d whole-group", tc.name, est.narrowed, est.full, est.unknown, tc.want)
		}
		if est.projected != time.Duration(tc.want)*time.Minute {
			t.Fatalf("%s: bound = %v, want %d whole-group run(s)", tc.name, est.projected, tc.want)
		}
	}
}

// advanceDone trues the done counter up to the window's candidate
// lens and never regresses it: a drained window nils a discarded
// work's candidates before the true-up, and completions must not
// un-happen on any advisory face.
func TestAdvanceDoneNeverRegresses(t *testing.T) {
	if got := advanceDone(10, 5, 12); got != 15 {
		t.Fatalf("advance = %d, want the window lens 15", got)
	}
	if got := advanceDone(10, 0, 12); got != 12 {
		t.Fatalf("drained true-up = %d, want the already-ticked 12 kept", got)
	}
	if got := advanceDone(10, 2, 12); got != 12 {
		t.Fatalf("undershooting true-up = %d, want monotone 12", got)
	}
}

// The ready pool executes value-ordered: cheapest priced window
// first; an unpriced window never jumps the queue on fabricated
// cheapness — it waits behind every priced window. Pool order IS
// arrival order (append-only, order-preserving deletes), so strict
// comparisons keep the first of any tie: ties and unpriced windows
// run in gather order.
func TestNextReadyWindowOrdersByCost(t *testing.T) {
	pool := []readyWindow{
		{cost: 30 * time.Second, priced: true},
		{priced: false},
		{cost: 5 * time.Second, priced: true},
		{cost: 5 * time.Second, priced: true},
	}
	if got := nextReadyWindow(pool); got != 2 {
		t.Fatalf("pick = %d, want the cheapest priced window (index 2; index 3 ties but arrived later)", got)
	}
	mixed := []readyWindow{{priced: false}, {cost: time.Hour, priced: true}}
	if got := nextReadyWindow(mixed); got != 1 {
		t.Fatalf("pick = %d — an expensive PRICED window still outranks an unpriced one", got)
	}
}

// The audit cap derives from the narrowing's own modeled savings
// (REQ-exec-oracle-run's narrowed-survivor clause): at most
// savings/auditShareDivisor full-oracle re-runs, floored at one
// sample so the disagreement rate is measured in every narrowing
// window, ceilinged at the fixed per-window cap; an unpriced unit
// derives the floor, never a fabricated share.
func TestDerivedAuditCapSharesSavings(t *testing.T) {
	unit := 21 * time.Minute
	if got := derivedAuditCap(0, unit); got != 1 {
		t.Fatalf("zero savings cap = %d, want the floor 1 — the rate stays measured", got)
	}
	if got := derivedAuditCap(auditShareDivisor*2*unit, unit); got != 2 {
		t.Fatalf("cap = %d, want savings/(%d×unit) = 2", got, auditShareDivisor)
	}
	if got := derivedAuditCap(1000*unit, unit); got != auditNarrowedCap {
		t.Fatalf("cap = %d, want the ceiling %d", got, auditNarrowedCap)
	}
	if got := derivedAuditCap(time.Hour, 0); got != 1 {
		t.Fatalf("unpriced unit cap = %d, want the floor — no fabricated share", got)
	}
}

// candidateNarrowingSavings models full minus covering and fabricates
// nothing: an unpriced batch or baseline yields zero, and a candidate
// whose groups all ran whole saved nothing.
func TestCandidateNarrowingSavingsFabricatesNothing(t *testing.T) {
	const pkg, coverPkg = "example.com/p", "example.com/p"
	w := scheduleTestWork(pkg, coverPkg, []string{"TestA", "TestB", "TestC", "TestD"})
	reach := engine.CoverageForTest(map[string][]engine.CoverSpanForTest{coverPkg + "/f.go": {{StartLine: 9, StartCol: 1, EndLine: 20, EndCol: 1}}})
	store := newScheduleStore()
	store.byKey[coverageKey(w.groups[0], coverPkg)] = &groupSchedule{batches: []scheduleBatch{
		{fns: []string{"TestA", "TestB"}, cov: reach, dur: 3 * time.Second},
		{fns: []string{"TestC", "TestD"}, cov: engine.CoverageForTest(nil), dur: 5 * time.Second},
	}}
	w.candidates = []engine.Candidate{{Symbol: "S", Operator: "op", Position: "f.go:10:2", Extent: "10:2-12:3", Replacements: []engine.Replacement{{File: "f.go"}}}}
	baseline := func(group) (time.Duration, bool) { return time.Minute, true }

	if got := candidateNarrowingSavings(w, 0, store, baseline); got != time.Minute-3*time.Second {
		t.Fatalf("savings = %v, want full − covering = %v", got, time.Minute-3*time.Second)
	}
	if got := candidateNarrowingSavings(w, 0, store, nil); got != 0 {
		t.Fatalf("unpriced baseline savings = %v, want 0 — never fabricated", got)
	}
	// A selectively-unpriced group refuses the WHOLE candidate: with a
	// second, unpriced group the priced one must not leak a fabricated
	// positive saving (full would undercount while covering keeps the
	// priced batches).
	twoGroups := w
	twoGroups.groups = append(append([]group(nil), w.groups...), group{pkgs: []string{"example.com/q"}, runRegex: "^(TestQ)$"})
	selective := func(g group) (time.Duration, bool) {
		if g.pkgs[0] == pkg {
			return time.Minute, true
		}
		return 0, false
	}
	if got := candidateNarrowingSavings(twoGroups, 0, store, selective); got != 0 {
		t.Fatalf("selectively-unpriced savings = %v, want 0 — never fabricated", got)
	}
	if got := candidateNarrowingSavings(w, 0, newScheduleStore(), baseline); got != 0 {
		t.Fatalf("whole-group savings = %v, want 0 — a group run whole saved nothing", got)
	}
	store.byKey[coverageKey(w.groups[0], coverPkg)].batches[0].dur = 0
	if got := candidateNarrowingSavings(w, 0, store, baseline); got != 0 {
		t.Fatalf("dur-less batch savings = %v, want 0 — never fabricated", got)
	}
}

// The estimate's rendered strings fabricate nothing: an all-unpriced
// window renders no bound (never "0s"), a real sub-second price never
// erases to "0s", and an unpriced audit renders nothing.
func TestEstimateRenderStringsFabricateNothing(t *testing.T) {
	if s := (windowEstimate{unknown: 3}).projectedString(); s != "" {
		t.Fatalf("all-unpriced bound rendered %q, want empty — a zero-cost window is a fabrication", s)
	}
	if s := (windowEstimate{full: 1, projected: 250 * time.Millisecond}).projectedString(); s != "250ms" {
		t.Fatalf("sub-second bound rendered %q, want 250ms — never a fabricated 0s", s)
	}
	if s := (windowEstimate{full: 2, projected: 90 * time.Second}).projectedString(); s != "1m30s" {
		t.Fatalf("bound rendered %q, want 1m30s", s)
	}
	if s := (windowEstimate{}).auditString(); s != "" {
		t.Fatalf("unpriced audit rendered %q, want empty", s)
	}
	if s := (windowEstimate{auditProjected: time.Minute}).auditString(); s != "1m0s" {
		t.Fatalf("audit rendered %q, want 1m0s", s)
	}
}
