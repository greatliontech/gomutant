package gomutant

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/gomutant/internal/engine"
)

// The schedule's phases must partition the oracle group exactly —
// REQ-exec-oracle-run's verdict-preserving schedule: a batch split
// that loses or duplicates a test silently narrows or inflates the
// verdict's ground. The domain is exhaustively small: every oracle
// size up to 512 is checked outright.
func TestScheduleBatchesPartition(t *testing.T) {
	for n := 0; n <= 512; n++ {
		fns := make([]string, n)
		for i := range fns {
			fns[i] = fmt.Sprintf("Test%04d", i)
		}
		batches := scheduleBatches(fns)
		var flat []string
		for _, b := range batches {
			flat = append(flat, b...)
		}
		if !slices.Equal(flat, fns) {
			t.Fatalf("n=%d: batches do not partition: %v vs %v", n, flat, fns)
		}
		if n > 0 {
			want := int(math.Ceil(math.Sqrt(float64(n))))
			if len(batches) > want {
				t.Fatalf("n=%d: batch count %d exceeds ceil(sqrt(n)) = %d", n, len(batches), want)
			}
		}
	}
}

func scheduleTestWork(pkg, coverPkg string, fns []string) work {
	oracle := make([]string, len(fns))
	for i, fn := range fns {
		oracle[i] = pkg + "." + fn
	}
	return work{
		oracle:     oracle,
		groups:     []group{{pkgs: []string{pkg}, runRegex: testRunRegex(fns)}},
		targetView: &subjectView{subject: gofresh.Subject{Package: coverPkg}},
	}
}

// The schedule transform: reaching batches front the run, the two
// phases together are exactly the group's test set, and every no-signal
// arm — no store, no entry, unsound file, empty side, missing extent —
// degrades to the unordered group (the advisory posture).
func TestScheduledGroupsPartitionAndOrder(t *testing.T) {
	const pkg, coverPkg = "example.com/p", "example.com/p"
	fns := []string{"TestA", "TestB", "TestC", "TestD"}
	w := scheduleTestWork(pkg, coverPkg, fns)
	m := engine.Mutant{Position: "f.go:10:2", Extent: "10:2-12:3"}
	reach := engine.CoverageForTest(map[string][]engine.CoverSpanForTest{coverPkg + "/f.go": {{StartLine: 9, StartCol: 1, EndLine: 20, EndCol: 1}}})
	miss := engine.CoverageForTest(nil)
	key := coverageKey(w.groups[0], coverPkg)
	entry := &groupSchedule{batches: []scheduleBatch{
		{fns: []string{"TestA", "TestB"}, cov: miss},
		{fns: []string{"TestC", "TestD"}, cov: reach},
	}}
	store := newScheduleStore()
	store.byKey[key] = entry
	tr := &Tree{}

	got := tr.scheduleSteps(w, m, Options{scheduleStore: store})
	if len(got) != 1 || got[0].remainder == nil || got[0].first.runRegex != testRunRegex([]string{"TestC", "TestD"}) || got[0].remainder.runRegex != testRunRegex([]string{"TestA", "TestB"}) {
		t.Fatalf("schedule steps = %+v, want one split step with the reaching batch fronted", got)
	}

	// The split's two patterns partition the group exactly.
	var phaseTests []string
	for _, pattern := range []string{got[0].first.runRegex, got[0].remainder.runRegex} {
		phaseTests = append(phaseTests, strings.Split(strings.TrimSuffix(strings.TrimPrefix(pattern, "^("), ")$"), "|")...)
	}
	slices.Sort(phaseTests)
	if !slices.Equal(phaseTests, fns) {
		t.Fatalf("phases do not partition the oracle: %v vs %v", phaseTests, fns)
	}

	// No-signal arms all degrade to the unordered group.
	unordered := func(name string, o Options, mm engine.Mutant, ww work) {
		got := tr.scheduleSteps(ww, mm, o)
		if len(got) != len(ww.groups) || got[0].remainder != nil || got[0].first.runRegex != ww.groups[0].runRegex {
			t.Fatalf("%s: schedule did not degrade to the unordered group: %+v", name, got)
		}
	}
	unordered("nil store", Options{}, m, w)
	unordered("no entry", Options{scheduleStore: newScheduleStore()}, m, w)
	unordered("no extent", Options{scheduleStore: store}, engine.Mutant{Position: m.Position}, w)
	allReach := &groupSchedule{batches: []scheduleBatch{
		{fns: []string{"TestA", "TestB"}, cov: reach},
		{fns: []string{"TestC", "TestD"}, cov: reach},
	}}
	oneSided := newScheduleStore()
	oneSided.byKey[key] = allReach
	unordered("empty remainder", Options{scheduleStore: oneSided}, m, w)
	unsound := newScheduleStore()
	unsound.byKey[key] = &groupSchedule{batches: []scheduleBatch{
		{fns: []string{"TestA", "TestB"}, cov: miss.UnsoundForTest(coverPkg + "/f.go")},
		{fns: []string{"TestC", "TestD"}, cov: reach},
	}}
	unordered("unsound file", Options{scheduleStore: unsound}, m, w)
	degraded := newScheduleStore()
	degraded.byKey[key] = entry
	degraded.unschedule(key)
	unordered("unscheduled after degrade", Options{scheduleStore: degraded}, m, w)
}

// A mutant whose killer sits ENTIRELY in the remainder phase is still
// killed: the schedule fronts the (crafted) reaching phase, the killer
// runs second, and the verdicts equal an unscheduled control run —
// REQ-exec-oracle-run's verdict-preserving schedule, pinned end to end
// with the killer outside phase one.
func TestRunSchedulesKillerInRemainderPhase(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	restoreMin := scheduleMinTests
	scheduleMinTests = 2
	restoreProbe := campaignCoveredPositions
	restoreRun := runMutantObservedEnv
	defer func() {
		scheduleMinTests = restoreMin
		campaignCoveredPositions = restoreProbe
		runMutantObservedEnv = restoreRun
	}()
	var oracleRuns atomic.Int64
	runMutantObservedEnv = func(ctx context.Context, dir string, m engine.Mutant, testPkgs []string, runRegex string, timeout time.Duration, binFlags []string, moduleDir, packageDir string, bracketPaths []string, namespaces []runtimeinput.ScratchNamespace, env []string) (engine.MutantOutcome, string, bool, runtimeinput.Observation, string, string, error) {
		oracleRuns.Add(1)
		return restoreRun(ctx, dir, m, testPkgs, runRegex, timeout, binFlags, moduleDir, packageDir, bracketPaths, namespaces, env)
	}

	const coverFile = "example.com/fixture/lib/lib.go"
	everything := engine.CoverageForTest(map[string][]engine.CoverSpanForTest{coverFile: {{StartLine: 1, StartCol: 1, EndLine: 10000, EndCol: 1}}})
	var mu sync.Mutex
	var probed []string
	// TestWeak's crafted coverage claims the whole file (the reaching
	// phase that kills nothing on Add mutants); TestAdd — the real
	// killer — claims nothing, landing it in the remainder phase.
	campaignCoveredPositions = func(_ context.Context, _, testPkg, runRegex, _ string, _ time.Duration, _ []string, _ []string, _ engine.DirectiveCoverageView) (engine.Coverage, error) {
		mu.Lock()
		probed = append(probed, runRegex)
		mu.Unlock()
		if strings.Contains(runRegex, "TestWeak") {
			return everything, nil
		}
		return engine.CoverageForTest(nil), nil
	}

	tr := fixtureTree(t)
	ctx := context.Background()
	target := Target{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestAdd", "example.com/fixture/lib.TestWeak"}}

	scheduled, err := tr.Run(ctx, []Target{target}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	scheduledProbes := append([]string(nil), probed...)
	mu.Unlock()
	scheduledRuns := oracleRuns.Load()

	// Control: same target, same oracle, scheduling gated off.
	scheduleMinTests = 1 << 30
	oracleRuns.Store(0)
	control, err := tr.Run(ctx, []Target{target}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	controlRuns := oracleRuns.Load()

	s, c := scheduled[0], control[0]
	if s.Mutants != c.Mutants || s.Killed != c.Killed || s.Discarded != c.Discarded || len(s.Survivors) != len(c.Survivors) {
		t.Fatalf("scheduled verdicts diverge from control: scheduled=%+v control=%+v — the schedule narrowed the verdict", s, c)
	}
	if s.Killed == 0 {
		t.Fatal("no kills — the killer-in-remainder pin is vacuous")
	}
	for i := range s.Survivors {
		if s.Survivors[i].Position != c.Survivors[i].Position || s.Survivors[i].Operator != c.Survivors[i].Operator {
			t.Fatalf("survivor sets diverge: %+v vs %+v", s.Survivors[i], c.Survivors[i])
		}
	}

	// The schedule engaged: both batch patterns were probed, and the
	// survivor buckets still ran their OWN full-pattern probe — a union
	// of subset runs is not that measurement (REQ-exec-survivor-evidence's
	// once-per-group probe stands apart from the schedule signal).
	full := testRunRegex([]string{"TestAdd", "TestWeak"})
	if !slices.Contains(scheduledProbes, testRunRegex([]string{"TestAdd"})) || !slices.Contains(scheduledProbes, testRunRegex([]string{"TestWeak"})) {
		t.Fatalf("batch probes did not run: %v", scheduledProbes)
	}
	if !slices.Contains(scheduledProbes, full) {
		t.Fatalf("the survivor buckets' full-pattern probe never ran — the schedule signal must not stand in for it: %v", scheduledProbes)
	}
	// The split actually EXECUTED as two phases: with the crafted reach
	// (phase one kills nothing) every measured mutant pays a second
	// process, so the scheduled run's oracle-process count strictly
	// exceeds the unscheduled control's — the one observable of a
	// schedule that is deliberately verdict-invisible.
	if scheduledRuns <= controlRuns {
		t.Fatalf("scheduled run used %d oracle processes vs control %d — the schedule never split an execution", scheduledRuns, controlRuns)
	}
}

// A test-attributed kill from a narrowed phase whose pattern FAILS its
// own baseline never scores: the mutant re-runs unsplit (the scored
// measurement) and the group stops scheduling — the order-dependent-
// suite degrade (REQ-exec-attribution's shape symmetry on the
// run-regex axis).
func TestPhaseKillWithoutPhaseBaselineDegradesToUnsplit(t *testing.T) {
	const pkg, coverPkg = "example.com/p", "example.com/p"
	fns := []string{"TestA", "TestB", "TestC", "TestD"}
	w := scheduleTestWork(pkg, coverPkg, fns)
	w.candidates = make([]engine.Candidate, 2)
	w.oracleSet = map[string]bool{pkg + ".TestA": true, pkg + ".TestB": true, pkg + ".TestC": true, pkg + ".TestD": true}
	m := engine.Mutant{Position: "f.go:10:2", Extent: "10:2-12:3", Replacements: []engine.Replacement{{File: "f.go"}}}
	reach := engine.CoverageForTest(map[string][]engine.CoverSpanForTest{coverPkg + "/f.go": {{StartLine: 9, StartCol: 1, EndLine: 20, EndCol: 1}}})
	store := newScheduleStore()
	store.byKey[coverageKey(w.groups[0], coverPkg)] = &groupSchedule{batches: []scheduleBatch{
		{fns: []string{"TestA", "TestB"}, cov: reach},
		{fns: []string{"TestC", "TestD"}, cov: engine.CoverageForTest(nil)},
	}}
	opts := Options{scheduleStore: store}

	restoreRun := runMutantObservedEnv
	restoreProbe := phaseBaselineProbe
	defer func() {
		runMutantObservedEnv = restoreRun
		phaseBaselineProbe = restoreProbe
	}()
	var patterns []string
	runMutantObservedEnv = func(_ context.Context, _ string, _ engine.Mutant, _ []string, runRegex string, _ time.Duration, _ []string, _, _ string, _ []string, _ []runtimeinput.ScratchNamespace, _ []string) (engine.MutantOutcome, string, bool, runtimeinput.Observation, string, string, error) {
		patterns = append(patterns, runRegex)
		if runRegex == testRunRegex([]string{"TestA", "TestB"}) {
			// The narrowed phase claims a kill — which the failing
			// phase baseline below reveals as the suite failing alone.
			return engine.MutantKilled, pkg + ".TestA", false, runtimeinput.Observation{}, "", "", nil
		}
		return engine.MutantSurvived, "", false, runtimeinput.Observation{}, "", "", nil
	}
	phaseBaselineProbe = func(_ context.Context, _, _, _ string, _ time.Duration, _ []string, _, _ string, _ []string, _ []runtimeinput.ScratchNamespace, _ []string) (int, bool, []string, runtimeinput.Observation, error) {
		return 1, false, []string{pkg + ".TestA"}, runtimeinput.Observation{}, nil
	}

	tr := &Tree{}
	outcome, killer, _, _, _, err := tr.executeMutant(context.Background(), w, m, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != engine.MutantSurvived || killer != "" {
		t.Fatalf("unvouched phase kill scored: outcome=%v killer=%q — a fabricated kill in the flattering direction", outcome, killer)
	}
	// The re-run executed the FULL unsplit pattern, and the group is
	// now unscheduled for every later mutant.
	if !slices.Contains(patterns, testRunRegex(fns)) {
		t.Fatalf("no unsplit re-run: %v", patterns)
	}
	if got := tr.scheduleSteps(w, m, opts); len(got) != 1 || got[0].remainder != nil {
		t.Fatalf("group still scheduling after the degrade: %+v", got)
	}
}

// The phase-kill vouch probe is an advisory baseline, never a
// verdict-bearing process: it runs under the run-wide bound, not the
// remainder phase's residual budget — a residual-starved probe would
// fail the vouch and unschedule the group over a budget artifact. The
// residual is strictly below the run-wide bound (the first phase's
// spend is always positive), so the exact-bound pin discriminates.
func TestPhaseKillVouchRunsUnderRunWideBound(t *testing.T) {
	const pkg, coverPkg = "example.com/p", "example.com/p"
	fns := []string{"TestA", "TestB", "TestC", "TestD"}
	w := scheduleTestWork(pkg, coverPkg, fns)
	w.candidates = make([]engine.Candidate, 2)
	w.oracleSet = map[string]bool{pkg + ".TestA": true, pkg + ".TestB": true, pkg + ".TestC": true, pkg + ".TestD": true}
	m := engine.Mutant{Position: "f.go:10:2", Extent: "10:2-12:3", Replacements: []engine.Replacement{{File: "f.go"}}}
	reach := engine.CoverageForTest(map[string][]engine.CoverSpanForTest{coverPkg + "/f.go": {{StartLine: 9, StartCol: 1, EndLine: 20, EndCol: 1}}})
	store := newScheduleStore()
	store.byKey[coverageKey(w.groups[0], coverPkg)] = &groupSchedule{batches: []scheduleBatch{
		{fns: []string{"TestA", "TestB"}, cov: reach},
		{fns: []string{"TestC", "TestD"}, cov: engine.CoverageForTest(nil)},
	}}
	opts := Options{scheduleStore: store, OracleTimeout: time.Minute}

	restoreRun := runMutantObservedEnv
	restoreProbe := phaseBaselineProbe
	defer func() {
		runMutantObservedEnv = restoreRun
		phaseBaselineProbe = restoreProbe
	}()
	runMutantObservedEnv = func(_ context.Context, _ string, _ engine.Mutant, _ []string, runRegex string, _ time.Duration, _ []string, _, _ string, _ []string, _ []runtimeinput.ScratchNamespace, _ []string) (engine.MutantOutcome, string, bool, runtimeinput.Observation, string, string, error) {
		if runRegex == testRunRegex([]string{"TestC", "TestD"}) {
			return engine.MutantKilled, pkg + ".TestC", false, runtimeinput.Observation{}, "", "", nil
		}
		return engine.MutantSurvived, "", false, runtimeinput.Observation{}, "", "", nil
	}
	var vouchBounds []time.Duration
	phaseBaselineProbe = func(_ context.Context, _, _, _ string, bound time.Duration, _ []string, _, _ string, _ []string, _ []runtimeinput.ScratchNamespace, _ []string) (int, bool, []string, runtimeinput.Observation, error) {
		vouchBounds = append(vouchBounds, bound)
		return 1, true, nil, runtimeinput.Observation{}, nil
	}

	tr := &Tree{}
	outcome, killer, _, _, _, err := tr.executeMutant(context.Background(), w, m, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != engine.MutantKilled || killer != pkg+".TestC" {
		t.Fatalf("vouched remainder-phase kill = outcome %v killer %q", outcome, killer)
	}
	if len(vouchBounds) != 1 || vouchBounds[0] != time.Minute {
		t.Fatalf("vouch probe bounds = %v, want exactly the run-wide bound — a residual bound starves the probe", vouchBounds)
	}
}

// The two phases of a split share ONE oracle-timeout budget: phase two
// launches with the unsplit bound minus phase one's spend, so a
// timeout kill stays schedule-invariant (REQ-exec-oracle-run's
// verdict-preserving schedule; the reviewer's split-halves-the-bound
// walk).
func TestSplitPhasesShareOneOracleBudget(t *testing.T) {
	const pkg, coverPkg = "example.com/p", "example.com/p"
	fns := []string{"TestA", "TestB", "TestC", "TestD"}
	w := scheduleTestWork(pkg, coverPkg, fns)
	m := engine.Mutant{Position: "f.go:10:2", Extent: "10:2-12:3", Replacements: []engine.Replacement{{File: "f.go"}}}
	reach := engine.CoverageForTest(map[string][]engine.CoverSpanForTest{coverPkg + "/f.go": {{StartLine: 9, StartCol: 1, EndLine: 20, EndCol: 1}}})
	store := newScheduleStore()
	store.byKey[coverageKey(w.groups[0], coverPkg)] = &groupSchedule{batches: []scheduleBatch{
		{fns: []string{"TestA", "TestB"}, cov: reach},
		{fns: []string{"TestC", "TestD"}, cov: engine.CoverageForTest(nil)},
	}}
	opts := Options{scheduleStore: store, OracleTimeout: time.Minute}

	restoreRun := runMutantObservedEnv
	defer func() { runMutantObservedEnv = restoreRun }()
	var timeouts []time.Duration
	runMutantObservedEnv = func(_ context.Context, _ string, _ engine.Mutant, _ []string, _ string, timeout time.Duration, _ []string, _, _ string, _ []string, _ []runtimeinput.ScratchNamespace, _ []string) (engine.MutantOutcome, string, bool, runtimeinput.Observation, string, string, error) {
		timeouts = append(timeouts, timeout)
		time.Sleep(30 * time.Millisecond)
		incomplete := ""
		if len(timeouts) == 1 {
			// The FIRST phase carries an incomplete-process reason: a
			// split survivor must not report less incomplete evidence
			// than the identical unsplit run (one process cannot lose
			// its own reason).
			incomplete = "first-phase process exited before observation finalization"
		}
		return engine.MutantSurvived, "", false, runtimeinput.Observation{}, incomplete, "", nil
	}

	tr := &Tree{}
	_, _, _, _, incomplete, err := tr.executeMutant(context.Background(), w, m, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(timeouts) != 2 || timeouts[0] != time.Minute {
		t.Fatalf("timeouts = %v, want phase one under the full bound", timeouts)
	}
	if timeouts[1] > time.Minute-20*time.Millisecond {
		t.Fatalf("phase two launched with %v of a 1m budget after a 30ms phase one — the split hands each phase a fresh bound", timeouts[1])
	}
	if incomplete != "first-phase process exited before observation finalization" {
		t.Fatalf("incomplete = %q — the pair arm lost the first phase's incomplete-process reason", incomplete)
	}
}

// The probe gate counts the candidates that will EXECUTE — a served
// record's flagged indexes, an extension's suffix, a drift serve's
// re-measure set — so a near-empty window never pays a probe pass it
// cannot amortize; and a probe failure stores an empty signal that is
// never re-probed.
func TestProbeScheduleCoverageGatesAndDegrades(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
	tr := fixtureTree(t)
	restoreProbe := campaignCoveredPositions
	restoreMin := scheduleMinTests
	scheduleMinTests = 2
	defer func() {
		campaignCoveredPositions = restoreProbe
		scheduleMinTests = restoreMin
	}()
	var calls atomic.Int64
	campaignCoveredPositions = func(_ context.Context, _, _, _, _ string, _ time.Duration, _ []string, _ []string, _ engine.DirectiveCoverageView) (engine.Coverage, error) {
		calls.Add(1)
		return engine.Coverage{}, fmt.Errorf("probe refused")
	}
	const pkg = "example.com/p"
	w := scheduleTestWork(pkg, pkg, []string{"TestA", "TestB", "TestC", "TestD", "TestE", "TestF", "TestG", "TestH", "TestI"})
	w.candidates = make([]engine.Candidate, 5)
	ctx := context.Background()

	// A served work with one flagged candidate never probes.
	served := w
	served.serve = &Finding{}
	served.flagged = map[int]bool{0: true}
	store := newScheduleStore()
	if err := tr.probeScheduleCoverage(ctx, served, Options{scheduleStore: store}, nil); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 || len(store.byKey) != 0 {
		t.Fatalf("a one-candidate serve paid %d probes", calls.Load())
	}

	// A fresh work probes — ceil(sqrt(9)) = 3 batches — and the
	// failure stores an empty signal exactly once.
	if err := tr.probeScheduleCoverage(ctx, w, Options{scheduleStore: store}, nil); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("failed probe retried within one pass: %d calls (first batch fails, probe stops)", calls.Load())
	}
	if err := tr.probeScheduleCoverage(ctx, w, Options{scheduleStore: store}, nil); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("failed probe re-probed on a later window: %d calls", calls.Load())
	}
	if entry := store.get(coverageKey(w.groups[0], pkg)); entry == nil || len(entry.batches) != 0 {
		t.Fatalf("failed probe stored a usable signal: %+v", entry)
	}

	// A healthy probe records one coverage run per batch.
	calls.Store(0)
	campaignCoveredPositions = func(_ context.Context, _, _, _, _ string, _ time.Duration, _ []string, _ []string, _ engine.DirectiveCoverageView) (engine.Coverage, error) {
		calls.Add(1)
		return engine.CoverageForTest(nil), nil
	}
	healthy := newScheduleStore()
	if err := tr.probeScheduleCoverage(ctx, w, Options{scheduleStore: healthy}, nil); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatalf("9 tests probed as %d batches, want ceil(sqrt(9)) = 3", calls.Load())
	}
	if entry := healthy.get(coverageKey(w.groups[0], pkg)); entry == nil || len(entry.batches) != 3 {
		t.Fatalf("healthy probe stored %+v", entry)
	}
}

// A serial confirmation executes UNSPLIT: the serial run is the scored
// measurement and the one corrector for a shape-induced window kill —
// a scheduled corrector would reproduce the very narrowing it exists
// to re-examine (REQ-exec-oracle-run's schedule clause;
// REQ-exec-attribution).
func TestSerialConfirmationRunsUnscheduled(t *testing.T) {
	const pkg, coverPkg = "example.com/p", "example.com/p"
	fns := []string{"TestA", "TestB", "TestC", "TestD"}
	w := scheduleTestWork(pkg, coverPkg, fns)
	w.oracleSet = map[string]bool{pkg + ".TestA": true, pkg + ".TestB": true, pkg + ".TestC": true, pkg + ".TestD": true}
	m := engine.Mutant{Position: "f.go:10:2", Extent: "10:2-12:3", Replacements: []engine.Replacement{{File: "f.go"}}}
	reach := engine.CoverageForTest(map[string][]engine.CoverSpanForTest{coverPkg + "/f.go": {{StartLine: 9, StartCol: 1, EndLine: 20, EndCol: 1}}})
	store := newScheduleStore()
	store.byKey[coverageKey(w.groups[0], coverPkg)] = &groupSchedule{batches: []scheduleBatch{
		{fns: []string{"TestA", "TestB"}, cov: reach},
		{fns: []string{"TestC", "TestD"}, cov: engine.CoverageForTest(nil)},
	}}
	opts := Options{scheduleStore: store}

	restoreRun := runMutantObservedEnv
	defer func() { runMutantObservedEnv = restoreRun }()
	var patterns []string
	runMutantObservedEnv = func(_ context.Context, _ string, _ engine.Mutant, _ []string, runRegex string, _ time.Duration, _ []string, _, _ string, _ []string, _ []runtimeinput.ScratchNamespace, _ []string) (engine.MutantOutcome, string, bool, runtimeinput.Observation, string, string, error) {
		patterns = append(patterns, runRegex)
		return engine.MutantSurvived, "", false, runtimeinput.Observation{}, "", "", nil
	}
	// The killer-scoped baseline memo is pre-seeded failed, forcing the
	// serial full-oracle fallback — the path under test.
	memo := map[scopedBaselineKey]bool{{pkg: pkg, run: "^TestA$"}: false}

	tr := &Tree{}
	if _, _, _, _, _, err := tr.confirmMutant(context.Background(), w, m, pkg+".TestA", memo, opts, nil); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(patterns, testRunRegex(fns)) {
		t.Fatalf("serial fallback never ran the full pattern: %v", patterns)
	}
	for _, p := range patterns {
		if p != testRunRegex(fns) {
			t.Fatalf("serial confirmation executed a narrowed phase %q — the corrector reproduced the narrowing", p)
		}
	}
}

// The split-manufactured-unverifiability predicate: degrade exactly
// when a SPLIT's individually sound phases merged unverifiable —
// never on an unsplit run, a single observation, a verifiable merge,
// or phases the suite already made unverifiable alone
// (REQ-exec-observation's posture; the schedule clause's re-measure
// arm).
func TestSplitSurvivorUnverifiableMergeDegradesToUnsplit(t *testing.T) {
	sound := runtimeinput.Observation{State: runtimeinput.State{OK: true}}
	uncomputed := runtimeinput.Observation{State: runtimeinput.State{OK: false}}
	inherited := runtimeinput.Observation{State: runtimeinput.State{OK: true, Unverifiable: true}}
	unvMerge := runtimeinput.Observation{State: runtimeinput.State{Unverifiable: true}}
	if !pairManufacturedUnverifiability(sound, sound, unvMerge) {
		t.Fatal("sound phases merging unverifiable did not degrade — the split manufactured unverifiability and it scored")
	}
	if pairManufacturedUnverifiability(sound, sound, sound) {
		t.Fatal("a verifiable merge degraded")
	}
	if pairManufacturedUnverifiability(sound, uncomputed, unvMerge) {
		t.Fatal("an uncomputed phase degraded")
	}
	// The decisive arm: a phase that recorded genuinely unverifiable
	// evidence is OK=true AND Unverifiable=true — inherited, never
	// manufactured; an unsplit re-run cannot improve the suite's own
	// truth (the round-2 finding: OK alone tests computability, not
	// verifiability).
	if pairManufacturedUnverifiability(sound, inherited, unvMerge) {
		t.Fatal("an inherently unverifiable phase degraded — cost inversion and a permanent unschedule for the suite's own truth")
	}
}

// A TimeoutKiller from a narrowed phase never scores: the split's own
// second-process overhead is charged inside the shared budget, so only
// the UNSPLIT run's bound may decide a timeout kill — in either
// direction — and this per-mutant degrade leaves the group's schedule
// standing for its siblings (REQ-exec-oracle-run's schedule clause).
func TestNarrowedPhaseTimeoutDegradesToUnsplit(t *testing.T) {
	const pkg, coverPkg = "example.com/p", "example.com/p"
	fns := []string{"TestA", "TestB", "TestC", "TestD"}
	w := scheduleTestWork(pkg, coverPkg, fns)
	w.oracleSet = map[string]bool{pkg + ".TestA": true, pkg + ".TestB": true, pkg + ".TestC": true, pkg + ".TestD": true}
	m := engine.Mutant{Position: "f.go:10:2", Extent: "10:2-12:3", Replacements: []engine.Replacement{{File: "f.go"}}}
	reach := engine.CoverageForTest(map[string][]engine.CoverSpanForTest{coverPkg + "/f.go": {{StartLine: 9, StartCol: 1, EndLine: 20, EndCol: 1}}})
	store := newScheduleStore()
	store.byKey[coverageKey(w.groups[0], coverPkg)] = &groupSchedule{batches: []scheduleBatch{
		{fns: []string{"TestA", "TestB"}, cov: reach},
		{fns: []string{"TestC", "TestD"}, cov: engine.CoverageForTest(nil)},
	}}
	opts := Options{scheduleStore: store, OracleTimeout: time.Minute}

	restoreRun := runMutantObservedEnv
	defer func() { runMutantObservedEnv = restoreRun }()
	var patterns []string
	full := testRunRegex(fns)
	runMutantObservedEnv = func(_ context.Context, _ string, _ engine.Mutant, _ []string, runRegex string, _ time.Duration, _ []string, _, _ string, _ []string, _ []runtimeinput.ScratchNamespace, _ []string) (engine.MutantOutcome, string, bool, runtimeinput.Observation, string, string, error) {
		patterns = append(patterns, runRegex)
		if runRegex != full {
			// Every narrowed phase claims a timeout kill — the shape
			// the shared budget can fabricate on a slow survivor.
			return engine.MutantKilled, TimeoutKiller, false, runtimeinput.Observation{}, "", "", nil
		}
		return engine.MutantKilled, TimeoutKiller, false, runtimeinput.Observation{State: runtimeinput.State{OK: true}}, "", "", nil
	}

	tr := &Tree{}
	outcome, killer, _, _, _, err := tr.executeMutant(context.Background(), w, m, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	// The scored verdict is the UNSPLIT run's own timeout kill, never
	// the narrowed phase's.
	if outcome != engine.MutantKilled || killer != TimeoutKiller {
		t.Fatalf("unsplit re-run verdict lost: outcome=%v killer=%q", outcome, killer)
	}
	if !slices.Contains(patterns, full) {
		t.Fatalf("no unsplit re-run after the narrowed-phase timeout: %v", patterns)
	}
	// Per-mutant slowness leaves the schedule standing for siblings.
	if got := tr.scheduleSteps(w, m, opts); len(got) != 1 || got[0].remainder == nil {
		t.Fatalf("a narrowed-bound timeout unscheduled the group: %+v", got)
	}
}
