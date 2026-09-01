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
	if len(got) != 1 || !got[0].narrowed || got[0].first.runRegex != testRunRegex([]string{"TestC", "TestD"}) {
		t.Fatalf("schedule steps = %+v, want one narrowed step whose executing pattern is exactly the reaching batch", got)
	}

	// The covering pattern is exactly the reaching tests — the exempt
	// set is the complement by construction of whole batches.
	coveringTests := strings.Split(strings.TrimSuffix(strings.TrimPrefix(got[0].first.runRegex, "^("), ")$"), "|")
	slices.Sort(coveringTests)
	if !slices.Equal(coveringTests, []string{"TestC", "TestD"}) {
		t.Fatalf("covering pattern is not the reaching set: %v", coveringTests)
	}

	// No-signal arms all degrade to the unordered group.
	unordered := func(name string, o Options, mm engine.Mutant, ww work) {
		got := tr.scheduleSteps(ww, mm, o)
		if len(got) != len(ww.groups) || got[0].narrowed || got[0].first.runRegex != ww.groups[0].runRegex {
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
	// The mirror arm: NO batch reaches the extent — the exemption must
	// never narrow to zero execution; the group runs whole and the
	// survivor buckets read never-executed (the pre-ruling behavior).
	allMiss := &groupSchedule{batches: []scheduleBatch{
		{fns: []string{"TestA", "TestB"}, cov: miss},
		{fns: []string{"TestC", "TestD"}, cov: miss},
	}}
	noReach := newScheduleStore()
	noReach.byKey[key] = allMiss
	unordered("empty covering set", Options{scheduleStore: noReach}, m, w)
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

// The narrowed survivor end to end, on the canonical residual-risk
// shape (REQ-exec-oracle-run's narrowed-survivor clause): CRAFTED
// coverage claims the real killer (TestAdd) reaches nothing, so every
// mutant it would kill is exempt from its execution and scores a
// narrowed survivor with the covering-passed class — the exact
// false-survivor scenario the campaign audit exists to re-score. The
// unscheduled control run shows the kills the lie hides, and the
// scheduled run's oracle-process count is strictly LOWER than the
// control's: the narrowing's cost win, observed.
func TestRunNarrowsSurvivorsToCoveringTests(t *testing.T) {
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
	var patternMu sync.Mutex
	var mutantPatterns []string
	// markerActive tags the scheduled run's FULL-pattern mutant
	// executions — exactly the audit's re-scores in this fixture — with
	// a synthetic incomplete-process reason, so the record proves the
	// audit's authority replaced the evidence channels, not only the
	// verdict (the full run's incomplete lands as candidate evidence).
	var markerActive atomic.Bool
	const auditMarker = "audit-authority-marker: full-run evidence replaced the narrowed measurement"
	fullPattern := testRunRegex([]string{"TestAdd", "TestWeak"})
	runMutantObservedEnv = func(ctx context.Context, dir string, m engine.Mutant, testPkgs []string, runRegex string, timeout time.Duration, binFlags []string, moduleDir, packageDir string, bracketPaths []string, namespaces []runtimeinput.ScratchNamespace, env []string) (engine.MutantOutcome, string, bool, runtimeinput.Observation, string, string, error) {
		oracleRuns.Add(1)
		patternMu.Lock()
		mutantPatterns = append(mutantPatterns, runRegex)
		patternMu.Unlock()
		out, killer, md, state, incomplete, diag, err := restoreRun(ctx, dir, m, testPkgs, runRegex, timeout, binFlags, moduleDir, packageDir, bracketPaths, namespaces, env)
		if markerActive.Load() && runRegex == fullPattern && incomplete == "" {
			incomplete = auditMarker
		}
		return out, killer, md, state, incomplete, diag, err
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

	var audited, auditFlips int
	var flipKillers []string
	var estimates []ExecutionEvent
	var executingEvents, ticks, lastTickDone int
	tickMonotonic := true
	var emu sync.Mutex
	markerActive.Store(true)
	scheduled, err := tr.Run(ctx, []Target{target}, Options{Executing: func(e ExecutionEvent) {
		emu.Lock()
		defer emu.Unlock()
		switch e.Phase {
		case "audit":
			audited += e.AuditedNarrowed
			auditFlips += e.AuditDisagreed
		case "audit-flip":
			flipKillers = append(flipKillers, e.FlipKiller)
		case "executing":
			executingEvents++
		case "estimate":
			estimates = append(estimates, e)
		case "tick":
			ticks++
			if e.CandidatesDone <= lastTickDone || e.CandidatesDone > e.CandidatesTotal {
				tickMonotonic = false
			}
			lastTickDone = e.CandidatesDone
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	scheduledProbes := append([]string(nil), probed...)
	mu.Unlock()
	patternMu.Lock()
	scheduledPatterns := append([]string(nil), mutantPatterns...)
	patternMu.Unlock()

	// Control: same target, same oracle, scheduling gated off.
	markerActive.Store(false)
	scheduleMinTests = 1 << 30
	oracleRuns.Store(0)
	control, err := tr.Run(ctx, []Target{target}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	controlRuns := oracleRuns.Load()

	s, c := scheduled[0], control[0]
	if s.Mutants != c.Mutants || s.Discarded != c.Discarded {
		t.Fatalf("candidate accounting diverged: scheduled=%+v control=%+v", s, c)
	}
	if c.Killed == 0 {
		t.Fatal("control found no kills — the lying-coverage pin is vacuous")
	}
	// The lie hides the killer's verdicts; the audit's deterministic
	// sample recovers up to auditNarrowedCap of them from the full
	// oracle (each recovery is an audit-flip attributed to the real
	// killer). The assertions hold under any hash luck.
	narrowedRows := 0
	for _, row := range s.Survivors {
		if row.Execution == "covering-passed" {
			narrowedRows++
		}
	}
	if s.Killed > c.Killed {
		t.Fatalf("scheduled kills %d exceed control %d", s.Killed, c.Killed)
	}
	if hidden := int(c.Killed - s.Killed); hidden > narrowedRows {
		t.Fatalf("%d hidden kills but only %d covering-passed survivors: %+v", hidden, narrowedRows, s.Survivors)
	}
	if narrowedRows == 0 && auditFlips == 0 {
		t.Fatal("the lie hid nothing — the narrowing never engaged")
	}
	// The DERIVED cap: this fixture's modeled savings are about
	// N_narrowed x the full-oracle price (the crafted covering batches
	// cost microseconds), so the share is N/auditShareDivisor and the
	// cap floors at exactly ONE audited sample while N stays below
	// 2 x auditShareDivisor — a fixed count-per-window cap would audit
	// up to its ceiling here (REQ-exec-oracle-run's savings-derived
	// audit bound). The premise asserts separately so operator-set
	// growth is self-diagnosing, not a mystery flake.
	if narrowedRows+audited >= 2*auditShareDivisor {
		t.Fatalf("fixture drift: %d narrowed survivors reaches 2 x auditShareDivisor = %d — the derived cap leaves the floor and the exact assertion below needs re-derivation", narrowedRows+audited, 2*auditShareDivisor)
	}
	if audited != 1 {
		t.Fatalf("audit sampled %d narrowed survivors, want exactly the derived floor of 1", audited)
	}
	if auditFlips > audited {
		t.Fatalf("audit disagreed %d of %d", auditFlips, audited)
	}
	// TestWeak kills nothing, so every scheduled kill is an audit
	// recovery: the re-scored verdicts ARE the kill count — a reporting
	// audit whose authority never replaced the scores would break this
	// identity.
	if int(s.Killed) != auditFlips {
		t.Fatalf("scheduled kills %d vs audit flips %d — the full run's verdicts must replace the narrowed scores", s.Killed, auditFlips)
	}
	for _, killer := range flipKillers {
		if killer != "example.com/fixture/lib.TestAdd" {
			t.Fatalf("audit flip attributed to %q, want the real killer TestAdd", killer)
		}
	}
	// The audit's authority replaces the EVIDENCE channels wholesale,
	// not only the verdict: every audited row carries the full run's
	// (marker-tagged) incomplete reason as its candidate evidence.
	markerRows := 0
	for _, ev := range s.CandidateEvidence {
		if ev.Reason == auditMarker {
			markerRows++
		}
	}
	if markerRows != audited {
		t.Fatalf("%d audited rows but %d carry the full run's evidence marker — the audit's authority must replace the narrowed measurement's evidence channels: %+v", audited, markerRows, s.CandidateEvidence)
	}

	// The window cost model (REQ-exec-run-status's estimate class):
	// one estimate per window, priced from the measured baselines and
	// batch probes — and its candidate classes count exactly the
	// dispatched candidates, which the completion ticks then walk
	// monotonically.
	if executingEvents == 0 || len(estimates) != executingEvents {
		t.Fatalf("%d estimate events for %d windows — want one per window", len(estimates), executingEvents)
	}
	classed := 0
	for _, e := range estimates {
		classed += e.EstimateNarrowed + e.EstimateFull + e.EstimateUnknown
		if e.EstimateProjected == "" && e.EstimateUnknown == 0 {
			t.Fatalf("estimate event carries no bound and no unpriced count: %+v", e)
		}
	}
	if ticks == 0 || !tickMonotonic {
		t.Fatalf("completion ticks = %d monotonic=%v — done must advance candidate by candidate", ticks, tickMonotonic)
	}
	if classed != ticks {
		t.Fatalf("estimate classified %d candidates but %d completion ticks fired — the model must price the dispatched selection exactly", classed, ticks)
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
	// The exemption itself, observed: the non-reaching remainder's
	// phase pattern (TestAdd alone) NEVER executes against a mutant —
	// TestAdd appears only inside the audit's full unsplit pattern.
	// (On a fixture this small the audit's full re-runs can offset the
	// exemption's raw process count, so the pattern set is the honest
	// observable, not the net total; controlRuns anchors that the
	// control genuinely ran unscheduled.)
	if controlRuns == 0 {
		t.Fatal("control executed no mutants — the pin is vacuous")
	}
	remainderPhase := testRunRegex([]string{"TestAdd"})
	coveringPhase := testRunRegex([]string{"TestWeak"})
	if slices.Contains(scheduledPatterns, remainderPhase) {
		t.Fatalf("the exempt remainder phase executed: %v", scheduledPatterns)
	}
	if !slices.Contains(scheduledPatterns, coveringPhase) {
		t.Fatalf("the covering phase never executed: %v", scheduledPatterns)
	}
	if audited > 0 && !slices.Contains(scheduledPatterns, fullPattern) {
		t.Fatalf("the audit's full unsplit pattern never executed: %v", scheduledPatterns)
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
	outcome, killer, _, _, _, _, err := tr.executeMutant(context.Background(), w, m, opts, nil)
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
	if got := tr.scheduleSteps(w, m, opts); len(got) != 1 || got[0].narrowed {
		t.Fatalf("group still scheduling after the degrade: %+v", got)
	}
}

// The phase-kill vouch probe is an advisory baseline, never a
// verdict-bearing process: a COVERING-phase kill (the one narrowed
// pattern that still executes under the narrowed-survivor clause)
// vouches over a probe run at the run-wide bound, not any residual —
// a starved probe would fail the vouch and unschedule the group over
// a budget artifact. The exact-bound pin discriminates against every
// residual arithmetic.
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
		if runRegex == testRunRegex([]string{"TestA", "TestB"}) {
			return engine.MutantKilled, pkg + ".TestA", false, runtimeinput.Observation{}, "", "", nil
		}
		return engine.MutantSurvived, "", false, runtimeinput.Observation{}, "", "", nil
	}
	var vouchBounds []time.Duration
	phaseBaselineProbe = func(_ context.Context, _, _, _ string, bound time.Duration, _ []string, _, _ string, _ []string, _ []runtimeinput.ScratchNamespace, _ []string) (int, bool, []string, runtimeinput.Observation, error) {
		vouchBounds = append(vouchBounds, bound)
		return 1, true, nil, runtimeinput.Observation{}, nil
	}

	tr := &Tree{}
	outcome, killer, _, _, _, _, err := tr.executeMutant(context.Background(), w, m, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != engine.MutantKilled || killer != pkg+".TestA" {
		t.Fatalf("vouched covering-phase kill = outcome %v killer %q", outcome, killer)
	}
	if len(vouchBounds) != 1 || vouchBounds[0] != time.Minute {
		t.Fatalf("vouch probe bounds = %v, want exactly the run-wide bound — a residual bound starves the probe", vouchBounds)
	}
}

// The narrowed survivor's execution shape (REQ-exec-oracle-run's
// narrowed-survivor clause): the covering phase runs under the FULL
// group budget, the non-reaching remainder never launches, the verdict
// is a narrowed survivor, and the covering phase's incomplete-process
// evidence still rides the result (one process cannot lose its own
// reason).
func TestNarrowedSurvivorSkipsNonCoveringRemainder(t *testing.T) {
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
	var patterns []string
	var timeouts []time.Duration
	runMutantObservedEnv = func(_ context.Context, _ string, _ engine.Mutant, _ []string, runRegex string, timeout time.Duration, _ []string, _, _ string, _ []string, _ []runtimeinput.ScratchNamespace, _ []string) (engine.MutantOutcome, string, bool, runtimeinput.Observation, string, string, error) {
		patterns = append(patterns, runRegex)
		timeouts = append(timeouts, timeout)
		return engine.MutantSurvived, "", false, runtimeinput.Observation{}, "covering-phase process exited before observation finalization", "", nil
	}

	tr := &Tree{}
	outcome, _, _, _, incomplete, narrowed, err := tr.executeMutant(context.Background(), w, m, opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(patterns) != 1 || patterns[0] != testRunRegex([]string{"TestA", "TestB"}) {
		t.Fatalf("executed patterns = %v, want the covering phase alone — the non-reaching remainder is exempt", patterns)
	}
	if timeouts[0] != time.Minute {
		t.Fatalf("covering phase ran under %v, want the full group budget", timeouts[0])
	}
	if outcome != engine.MutantSurvived || !narrowed {
		t.Fatalf("verdict = %v narrowed=%v, want the narrowed survivor", outcome, narrowed)
	}
	if incomplete != "covering-phase process exited before observation finalization" {
		t.Fatalf("incomplete = %q — the narrowed path lost the covering phase's incomplete-process reason", incomplete)
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
	// Runnable candidates: the amortization gate counts the executing
	// selection, and a pre-execution discard (no replacements) is not
	// in it.
	w.candidates = make([]engine.Candidate, 5)
	for i := range w.candidates {
		w.candidates[i].Replacements = []engine.Replacement{{File: "f.go"}}
	}
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
		// A measurable probe duration for the batch-price assertion —
		// an instant stub could round to zero on a coarse clock.
		time.Sleep(time.Millisecond)
		return engine.CoverageForTest(nil), nil
	}
	healthy := newScheduleStore()
	if err := tr.probeScheduleCoverage(ctx, w, Options{scheduleStore: healthy}, nil); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatalf("9 tests probed as %d batches, want ceil(sqrt(9)) = 3", calls.Load())
	}
	entry := healthy.get(coverageKey(w.groups[0], pkg))
	if entry == nil || len(entry.batches) != 3 {
		t.Fatalf("healthy probe stored %+v", entry)
	}
	// Each batch records its probe's wall-clock — the window cost
	// model's per-batch price (REQ-exec-run-status's estimate class).
	for i, b := range entry.batches {
		if b.dur <= 0 {
			t.Fatalf("batch %d recorded no duration: %+v", i, b)
		}
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
	outcome, killer, _, _, _, _, err := tr.executeMutant(context.Background(), w, m, opts, nil)
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
	if got := tr.scheduleSteps(w, m, opts); len(got) != 1 || !got[0].narrowed {
		t.Fatalf("a narrowed-bound timeout unscheduled the group: %+v", got)
	}
}
