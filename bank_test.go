package gomutant

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/gomutant/internal/engine"
	"github.com/greatliontech/gomutant/internal/windowcost"
)

// The bank is pure cache with a hard honesty rule: an absent,
// unreadable, malformed, or version-skewed file reads as EMPTY —
// never an error, never a served entry — and a saved bank round-trips
// its deposits (REQ-result-baseline-bank).
func TestBaselineBankRoundTripAndCorruptionReadsEmpty(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	moduleDir := t.TempDir()

	b := openBaselineBank(moduleDir)
	if _, ok := b.baseline("k"); ok {
		t.Fatal("empty bank served an entry")
	}
	// EACH deposit persists IMMEDIATELY — no save() call, reopened
	// after every verb: a killed process must lose nothing already
	// deposited.
	b.putBaseline("k", bankedBaseline{Manifest: "m", Digest: "d", RawMillis: 1234, MeasuredAtUnix: 5})
	got, ok := openBaselineBank(moduleDir).baseline("k")
	if !ok || got.Manifest != "m" || got.RawMillis != 1234 {
		t.Fatalf("the baseline deposit did not persist immediately: %+v ok=%v", got, ok)
	}
	b.putCoverage("c", bankedCoverage{Plan: 2, Batches: []bankedBatch{{Index: 1, Fns: []string{"TestA"}, DurMillis: 7}}, Failed: []bankedFailure{{Index: 0, Fns: []string{"TestZ"}, Reason: "exit status 1"}}})
	again := openBaselineBank(moduleDir)
	if _, ok := again.baseline("k"); !ok {
		t.Fatal("the coverage deposit dropped the persisted baseline")
	}
	cov, ok := again.coverage("c")
	if !ok || cov.Plan != 2 || len(cov.Batches) != 1 || cov.Batches[0].Index != 1 || cov.Batches[0].DurMillis != 7 || len(cov.Failed) != 1 || cov.Failed[0].Index != 0 || cov.Failed[0].Reason != "exit status 1" {
		t.Fatalf("the coverage deposit did not persist immediately in the per-batch form: %+v ok=%v", cov, ok)
	}

	path, err := bankPath(moduleDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if corrupt := openBaselineBank(moduleDir); len(corrupt.file.Baselines) != 0 || len(corrupt.file.Coverage) != 0 {
		t.Fatal("corrupt bank served entries instead of reading empty")
	}
	if err := os.WriteFile(path, []byte(`{"version":99,"baselines":{"k":{}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if skewed := openBaselineBank(moduleDir); len(skewed.file.Baselines) != 0 {
		t.Fatal("version-skewed bank served entries instead of reading empty")
	}

	// A nil bank is inert on every verb — the bank-less library run.
	var nilBank *baselineBank
	if _, ok := nilBank.baseline("k"); ok {
		t.Fatal("nil bank served")
	}
	nilBank.putBaseline("k", bankedBaseline{})
	nilBank.save()
}

// A FAILED persist keeps the deposit dirty on EVERY failing branch —
// the directory guard and the rename commit point alike — so the
// next deposit or the exit flush retries and a transient write
// failure never silently drops a completed measurement
// (REQ-result-baseline-bank).
func TestBaselineBankFailedPersistStaysDirty(t *testing.T) {
	retryTarget := filepath.Join(t.TempDir(), "clean")
	if err := os.MkdirAll(retryTarget, 0o755); err != nil {
		t.Fatal(err)
	}

	// Branch 1: the path's parent is a FILE — the MkdirAll guard fails.
	parentFile := filepath.Join(t.TempDir(), "notadir")
	if err := os.WriteFile(parentFile, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	b := &baselineBank{path: filepath.Join(parentFile, "baselines.json"), file: baselineBankFile{Version: bankVersion}}
	b.putBaseline("k", bankedBaseline{Manifest: "m"})
	if !b.dirty {
		t.Fatal("a failed MkdirAll cleared dirty — the deposit would be silently lost")
	}

	// Branch 2: the path IS a non-empty directory — MkdirAll and the
	// tmp write succeed, the RENAME commit point fails (ENOTEMPTY).
	dirPath := filepath.Join(t.TempDir(), "x", "baselines.json")
	if err := os.MkdirAll(filepath.Join(dirPath, "occupant"), 0o755); err != nil {
		t.Fatal(err)
	}
	b.path = dirPath
	b.putBaseline("k2", bankedBaseline{Manifest: "m2"})
	if !b.dirty {
		t.Fatal("a failed rename cleared dirty — the deposit would be silently lost")
	}

	// The retried flush against a healthy path lands everything.
	b.path = filepath.Join(retryTarget, "baselines.json")
	b.save()
	if b.dirty {
		t.Fatal("the retried flush did not persist")
	}
	data, err := os.ReadFile(b.path)
	if err != nil || len(data) == 0 {
		t.Fatalf("retried flush wrote nothing: %v", err)
	}
}

// The bank and the findings overlay share one machine-local home per
// resolved tree — every machine-local artifact of a tree under one
// key (REQ-result-baseline-bank).
func TestBankPathSharesOverlayKeying(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	moduleDir := t.TempDir()
	path, err := bankPath(moduleDir)
	if err != nil {
		t.Fatal(err)
	}
	_, machineDir, err := machineLocalDir(moduleDir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != machineDir || filepath.Base(path) != "baselines.json" {
		t.Fatalf("bank path %q not the machine-local sibling of %q", path, machineDir)
	}
}

// The bank across runs, end to end (REQ-result-baseline-bank): the
// first campaign probes and deposits; a second campaign over the
// unchanged tree serves every baseline and coverage probe from the
// bank — ZERO probe processes — reporting banked baseline events and
// still deriving budgets; editing an oracle test breaks the pins and
// the third campaign probes again. Findings agree throughout.
func TestRunServesBankedBaselinesAcrossRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test baselines across three campaigns")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	restoreProbe := seams.baselineProbe
	restoreCov := seams.coveredPositions
	restoreMinT := windowcost.ScheduleMinTests
	restoreMinC := windowcost.ScheduleMinCandidates
	windowcost.ScheduleMinTests = 2
	windowcost.ScheduleMinCandidates = 1
	t.Cleanup(func() {
		seams.baselineProbe = restoreProbe
		seams.coveredPositions = restoreCov
		windowcost.ScheduleMinTests = restoreMinT
		windowcost.ScheduleMinCandidates = restoreMinC
	})
	var baselineProbes, coverageProbes atomic.Int64
	seams.baselineProbe = func(ctx context.Context, dir, pkg, run string, timeout time.Duration, flags []string, moduleDir, packageDir string, brackets []string, namespaces []runtimeinput.ScratchNamespace, env []string, bounds engine.OracleBounds) (int, bool, []string, string, runtimeinput.Observation, error) {
		baselineProbes.Add(1)
		return restoreProbe(ctx, dir, pkg, run, timeout, flags, moduleDir, packageDir, brackets, namespaces, env, bounds)
	}
	seams.coveredPositions = func(ctx context.Context, dir, testPkg, runRegex, coverPkg string, timeout time.Duration, flags []string, env []string, view engine.DirectiveCoverageView, bounds engine.OracleBounds) (engine.Coverage, error) {
		coverageProbes.Add(1)
		return engine.CoveredPositions(ctx, dir, testPkg, runRegex, coverPkg, timeout, flags, env, view, bounds)
	}

	dir := t.TempDir()
	files := map[string]string{
		"go.mod":      "module example.com/bankmod\n\ngo 1.26\n",
		"a/a.go":      "package a\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n",
		"a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal()\n\t}\n}\n\nfunc TestAddZero(t *testing.T) {\n\tif Add(0, 0) != 0 {\n\t\tt.Fatal()\n\t}\n}\n",
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	load := func() *Tree {
		tr, err := Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		return tr
	}
	target := []Target{{Symbol: "example.com/bankmod/a.Add"}}
	own := RunOwnWrites(filepath.Join(dir, ".gomutant", "findings.json"))
	var bankedEvents atomic.Int64
	var budgets []string
	var bmu sync.Mutex
	opts := func(budget int, force bool) Options {
		return Options{Budget: budget, Force: force, OwnWrites: own, Progress: func(e PreparationEvent) {
			if e.Stage == PreparationBaseline && e.Banked {
				bankedEvents.Add(1)
			}
			if e.Stage == PreparationOracleBudget {
				bmu.Lock()
				budgets = append(budgets, e.OracleBudget)
				bmu.Unlock()
			}
		}}
	}

	first, err := load().Run(context.Background(), target, opts(1, false))
	if err != nil || len(first) != 1 {
		t.Fatalf("first run = %+v, %v", first, err)
	}
	if baselineProbes.Load() == 0 {
		t.Fatal("first run probed no baselines — the fixture is vacuous")
	}
	if bankedEvents.Load() != 0 {
		t.Fatal("first run reported banked events with an empty bank")
	}
	deposited := openBaselineBank(dir)
	if len(deposited.file.Baselines) == 0 {
		t.Fatalf("first run deposited no baselines (coverage entries: %d) — the deposit path is dead", len(deposited.file.Coverage))
	}
	for k, e := range deposited.file.Baselines {
		if e.Manifest == "" {
			t.Fatalf("banked baseline %q has an empty manifest", k)
		}
	}

	bmu.Lock()
	firstBudgets := append([]string(nil), budgets...)
	budgets = nil
	bmu.Unlock()
	if len(firstBudgets) == 0 {
		t.Fatal("first run derived no budgets — the fixture is vacuous")
	}

	// Run 2: a BUDGET EXTENSION re-measures (Force would rightly
	// bypass the bank — the operator's distrust-the-cache control) —
	// every baseline and coverage probe serves from the bank, and the
	// SERVED measurement is the banked one: the derived budget equals
	// run 1's, so a zero-duration or fabricated serve cannot hide.
	baselineProbes.Store(0)
	coverageProbes.Store(0)
	second, err := load().Run(context.Background(), target, opts(2, false))
	if err != nil || len(second) != 1 {
		t.Fatalf("second run = %+v, %v", second, err)
	}
	if got := baselineProbes.Load(); got != 0 {
		t.Fatalf("second run probed %d baselines — the bank must serve an unchanged tree", got)
	}
	if got := coverageProbes.Load(); got != 0 {
		t.Fatalf("second run probed %d coverage passes — the bank must serve an unchanged tree", got)
	}
	if bankedEvents.Load() == 0 {
		t.Fatal("second run served silently — a cross-run serve must report its banked baseline event")
	}
	bmu.Lock()
	secondBudgets := append([]string(nil), budgets...)
	budgets = nil
	bmu.Unlock()
	if len(secondBudgets) == 0 || secondBudgets[0] != firstBudgets[0] {
		t.Fatalf("served budget %v, want the banked measurement's %v — the serve must carry the banked duration", secondBudgets, firstBudgets)
	}
	if second[0].Killed < first[0].Killed || second[0].Mutants < first[0].Mutants {
		t.Fatalf("extension lost verdicts across the banked serve: %+v vs %+v", first[0], second[0])
	}

	// Force bypasses the bank: the operator's distrust-the-cache
	// control re-probes everything.
	baselineProbes.Store(0)
	coverageProbes.Store(0)
	if _, err := load().Run(context.Background(), target, opts(2, true)); err != nil {
		t.Fatal(err)
	}
	if baselineProbes.Load() == 0 || coverageProbes.Load() == 0 {
		t.Fatalf("--force served from the bank (baselines probed: %d, coverage probed: %d) — the distrust-the-cache control must re-probe BOTH halves", baselineProbes.Load(), coverageProbes.Load())
	}

	// A membership-PRESERVING oracle body edit breaks the CONTENT
	// pins (the bank key — package, pattern, flags — is unchanged, so
	// only the evidence-row comparison can catch it): the third
	// campaign probes both baselines and coverage again.
	bodyEdited := strings.Replace(files["a/a_test.go"], "Add(1, 2) != 3", "Add(2, 1) != 3", 1)
	if bodyEdited == files["a/a_test.go"] {
		t.Fatal("fixture drift: the body edit matched nothing")
	}
	if err := os.WriteFile(filepath.Join(dir, "a/a_test.go"), []byte(bodyEdited), 0o644); err != nil {
		t.Fatal(err)
	}
	baselineProbes.Store(0)
	coverageProbes.Store(0)
	if _, err := load().Run(context.Background(), target, opts(2, false)); err != nil {
		t.Fatal(err)
	}
	if baselineProbes.Load() == 0 {
		t.Fatal("a membership-preserving oracle body edit did not break the baseline pins — stale measurement served")
	}
	if coverageProbes.Load() == 0 {
		t.Fatal("a membership-preserving oracle body edit did not break the coverage pins — stale coverage served")
	}

	// A membership-CHANGING edit misses on the key itself: the fourth
	// campaign probes.
	added := bodyEdited + "\nfunc TestAddNeg(t *testing.T) {\n\tif Add(-1, 1) != 0 {\n\t\tt.Fatal()\n\t}\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "a/a_test.go"), []byte(added), 0o644); err != nil {
		t.Fatal(err)
	}
	baselineProbes.Store(0)
	if _, err := load().Run(context.Background(), target, opts(2, false)); err != nil {
		t.Fatal(err)
	}
	if baselineProbes.Load() == 0 {
		t.Fatal("an oracle membership change did not miss the bank key — stale measurement served")
	}
}

// A banked coverage entry resumes against the current plan by
// position: matching batches serve, prior failures are reported by
// position, and a plan the entry does not match — a different batch
// count, tests differing at a position, a position outside the plan,
// a duplicated position — discards the entry whole
// (REQ-result-baseline-bank).
func TestBankedCoverageResumeMatchesThePlan(t *testing.T) {
	plan := [][]string{{"TestA", "TestB"}, {"TestC", "TestD"}, {"TestE"}}
	batch := func(i int) bankedBatch { return bankedBatch{Index: i, Fns: plan[i], DurMillis: int64(i + 1)} }
	complete := bankedCoverage{Plan: 3, Batches: []bankedBatch{batch(0), batch(1), batch(2)}}
	banked, failed, ok := complete.resume(plan)
	if !ok || len(banked) != 3 || len(failed) != 0 || !complete.complete() || banked[1].dur != 2*time.Millisecond {
		t.Fatalf("complete entry: ok=%v banked=%d failed=%d complete=%v", ok, len(banked), len(failed), complete.complete())
	}
	partial := bankedCoverage{Plan: 3, Batches: []bankedBatch{batch(0), batch(2)}, Failed: []bankedFailure{{Index: 1, Fns: plan[1], Reason: "exit status 1"}}}
	banked, failed, ok = partial.resume(plan)
	if !ok || len(banked) != 2 || banked[2].fns[0] != "TestE" || failed[1] != "exit status 1" || partial.complete() {
		t.Fatalf("partial entry: ok=%v banked=%v failed=%v complete=%v", ok, banked, failed, partial.complete())
	}
	for name, entry := range map[string]bankedCoverage{
		"plan size moved":     {Plan: 2, Batches: []bankedBatch{batch(0)}},
		"tests moved":         {Plan: 3, Batches: []bankedBatch{{Index: 1, Fns: []string{"TestC", "TestX"}}}},
		"position outside":    {Plan: 3, Batches: []bankedBatch{{Index: 3, Fns: []string{"TestE"}}}},
		"duplicated":          {Plan: 3, Batches: []bankedBatch{batch(0), batch(0)}},
		"failure outside":     {Plan: 3, Failed: []bankedFailure{{Index: 5}}},
		"failure tests moved": {Plan: 3, Batches: []bankedBatch{batch(0)}, Failed: []bankedFailure{{Index: 1, Fns: []string{"TestC", "TestX"}, Reason: "exit status 1"}}},
		"version-1 shape":     {Plan: 0, Batches: []bankedBatch{{Fns: plan[0]}, {Fns: plan[1]}, {Fns: plan[2]}}},
	} {
		if _, _, ok := entry.resume(plan); ok {
			t.Fatalf("%s: a mismatched entry resumed", name)
		}
	}
}

// A coverage probe's bank entry is per batch: a run whose batch fails
// banks the passing batches and the failure by position; the next run
// probes exactly the failed batch, its retry naming the prior failure;
// a run on which it passes completes the entry; and the run after
// probes nothing. A run cancelled mid-unit leaves its completed
// batches banked, so the rerun probes only the remainder
// (REQ-result-baseline-bank's resume; REQ-exec-run-status's
// failed-batch event).
func TestBankResumesAPartialCoverageProbe(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test baselines across campaigns")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	restoreCov := seams.coveredPositions
	restoreMinT := windowcost.ScheduleMinTests
	restoreMinC := windowcost.ScheduleMinCandidates
	windowcost.ScheduleMinTests = 2
	windowcost.ScheduleMinCandidates = 1
	t.Cleanup(func() {
		seams.coveredPositions = restoreCov
		windowcost.ScheduleMinTests = restoreMinT
		windowcost.ScheduleMinCandidates = restoreMinC
	})
	var probes atomic.Int64
	var failing, failingFirst, failingThird, cancelAfterFirst, cancelAtThird atomic.Bool
	var cancel context.CancelFunc
	seams.coveredPositions = func(ctx context.Context, dir, testPkg, runRegex, coverPkg string, timeout time.Duration, flags []string, env []string, view engine.DirectiveCoverageView, bounds engine.OracleBounds) (engine.Coverage, error) {
		// The survivor bucket's advisory probe rides the same seam under
		// the whole group's pattern; only the batch probes (a third of
		// the nine tests each) are the schedule's.
		if strings.Contains(runRegex, "TestAdd1") && strings.Contains(runRegex, "TestAdd9") {
			return engine.CoverageForTest(nil), nil
		}
		n := probes.Add(1)
		if failing.Load() && strings.Contains(runRegex, "TestAdd4") {
			return engine.Coverage{}, fmt.Errorf("probe refused: exit status 1")
		}
		if failingFirst.Load() && strings.Contains(runRegex, "TestAdd1") {
			return engine.Coverage{}, fmt.Errorf("probe refused: exit status 1")
		}
		if cancelAfterFirst.Load() && n == 1 {
			cancel()
		}
		if cancelAtThird.Load() && strings.Contains(runRegex, "TestAdd7") {
			cancel()
			return engine.Coverage{}, ctx.Err()
		}
		if failingThird.Load() && strings.Contains(runRegex, "TestAdd7") {
			return engine.Coverage{}, fmt.Errorf("probe refused: exit status 1")
		}
		return engine.CoverageForTest(nil), nil
	}

	dir := t.TempDir()
	var tests strings.Builder
	tests.WriteString("package a\n\nimport \"testing\"\n\n")
	for i := 1; i <= 9; i++ {
		fmt.Fprintf(&tests, "func TestAdd%d(t *testing.T) {\n\tif Add(%d, 1) != %d {\n\t\tt.Fatal()\n\t}\n}\n\n", i, i, i+1)
	}
	files := map[string]string{
		"go.mod": "module example.com/bankmod\n\ngo 1.26\n",
		// A body with several operators, so six budget extensions each
		// find an unmeasured candidate.
		"a/a.go":      "package a\n\nfunc Add(a, b int) int {\n\tx := a + b\n\tif x < 0 {\n\t\tx = -x\n\t}\n\tif x > 1000 {\n\t\tx = x - 1\n\t}\n\treturn x\n}\n",
		"a/a_test.go": tests.String(),
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	load := func() *Tree {
		tr, err := Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		return tr
	}
	target := []Target{{Symbol: "example.com/bankmod/a.Add"}}
	own := RunOwnWrites(filepath.Join(dir, ".gomutant", "findings.json"))
	var events []AnalysisEvent
	var emu sync.Mutex
	// Every run is a BUDGET EXTENSION of the one before — a forced run
	// would rightly bypass the bank (REQ-result-baseline-bank) — so each
	// run re-measures one more candidate and pays the probe phase.
	opts := func(budget int) Options {
		return Options{Budget: budget, OwnWrites: own, AnalysisEvent: func(e AnalysisEvent) {
			if e.Phase != "probe-failed" {
				return
			}
			emu.Lock()
			events = append(events, e)
			emu.Unlock()
		}}
	}
	entry := func() bankedCoverage {
		bank := openBaselineBank(dir)
		if len(bank.file.Coverage) != 1 {
			t.Fatalf("bank holds %d coverage entries, want the one group", len(bank.file.Coverage))
		}
		for _, e := range bank.file.Coverage {
			return e
		}
		return bankedCoverage{}
	}
	run := func(ctx context.Context, budget int, wantProbes int64) ([]Finding, error) {
		probes.Store(0)
		emu.Lock()
		events = nil
		emu.Unlock()
		findings, err := load().Run(ctx, target, opts(budget))
		if got := probes.Load(); got != wantProbes {
			t.Fatalf("run (budget %d) probed %d coverage batches, want %d (err %v)", budget, got, wantProbes, err)
		}
		return findings, err
	}

	// Run 1: batch 2 of 3 fails — every batch probes once, the two
	// passing ones bank at their positions, the failure by its position.
	failing.Store(true)
	findings, err := run(context.Background(), 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].CandidateCount < 6 {
		t.Fatalf("the fixture must enumerate at least six candidates for the extensions below: %+v", findings)
	}
	first := entry()
	if first.Plan != 3 || len(first.Batches) != 2 || first.Batches[0].Index != 0 || first.Batches[1].Index != 2 || len(first.Failed) != 1 || first.Failed[0].Index != 1 || !strings.Contains(first.Failed[0].Reason, "probe refused") {
		t.Fatalf("after the failing run the bank holds %+v", first)
	}
	if len(events) != 1 || !strings.Contains(events[0].Detail, "batch 2/3 over example.com/bankmod/a (TestAdd4, TestAdd5, TestAdd6): ") || strings.Contains(events[0].Detail, "previous run") {
		t.Fatalf("failing run reported %+v", events)
	}
	// Run 2: only the failed batch probes; still failing, the retry
	// names the prior failure.
	if _, err := run(context.Background(), 2, 1); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || !strings.Contains(events[0].Detail, "failed in the previous run too: probe refused") {
		t.Fatalf("retry reported %+v, want the prior failure named", events)
	}
	// Run 3: the batch passes — the entry completes.
	failing.Store(false)
	if _, err := run(context.Background(), 3, 1); err != nil {
		t.Fatal(err)
	}
	if done := entry(); !done.complete() || len(done.Failed) != 0 || len(events) != 0 {
		t.Fatalf("after the passing retry the bank holds %+v (events %d)", done, len(events))
	}
	// Run 4: nothing probes.
	if _, err := run(context.Background(), 4, 0); err != nil {
		t.Fatal(err)
	}

	// A fresh bank, a run cancelled after its first batch: the batch
	// stays banked and the rerun probes the remaining two.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	var ctx context.Context
	ctx, cancel = context.WithCancel(context.Background())
	defer func() { cancel() }()
	cancelAfterFirst.Store(true)
	if _, err := run(ctx, 5, 1); err == nil {
		t.Fatal("a run cancelled mid-probe returned no error")
	}
	cancelAfterFirst.Store(false)
	if cut := entry(); cut.Plan != 3 || len(cut.Batches) != 1 || cut.Batches[0].Index != 0 {
		t.Fatalf("after the cancelled run the bank holds %+v, want the one completed batch", cut)
	}
	if _, err := run(context.Background(), 5, 2); err != nil {
		t.Fatal(err)
	}
	if resumed := entry(); !resumed.complete() {
		t.Fatalf("the rerun did not complete the entry: %+v", resumed)
	}

	// A fresh bank again, protodb's own shape: deterministic failures
	// at batches 1 and 3, then a deadline. Run A banks batch 2 and the
	// two failures. Run B retries batch 1 (fails again), serves batch 2
	// from the bank, and is cut during batch 3 — every deposit its
	// retry writes must still carry batch 2, which this run never
	// re-probed, AND the failure at 3, which this run never reached: a
	// resumed unit's partial deposits never shrink the entry below what
	// the bank held. Run C completes it with two probes.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	failingFirst.Store(true)
	failingThird.Store(true)
	if _, err := run(context.Background(), 6, 3); err != nil {
		t.Fatal(err)
	}
	if a := entry(); a.Plan != 3 || len(a.Batches) != 1 || a.Batches[0].Index != 1 || len(a.Failed) != 2 || a.Failed[0].Index != 0 || a.Failed[1].Index != 2 {
		t.Fatalf("after run A the bank holds %+v", a)
	}
	failingThird.Store(false)
	cancelAtThird.Store(true)
	ctx, cancel = context.WithCancel(context.Background())
	if _, err := run(ctx, 6, 2); err == nil {
		t.Fatal("run B: a run cancelled mid-probe returned no error")
	}
	cancel()
	if b := entry(); b.Plan != 3 || len(b.Batches) != 1 || b.Batches[0].Index != 1 || len(b.Failed) != 2 || b.Failed[0].Index != 0 || b.Failed[1].Index != 2 {
		t.Fatalf("after run B the bank holds %+v — the served batch or the unreached failure was dropped by a partial deposit", b)
	}
	if len(events) != 1 || !strings.Contains(events[0].Detail, "failed in the previous run too") {
		t.Fatalf("run B reported %+v", events)
	}
	failingFirst.Store(false)
	cancelAtThird.Store(false)
	if _, err := run(context.Background(), 6, 2); err != nil {
		t.Fatal(err)
	}
	if c := entry(); !c.complete() || len(c.Failed) != 0 {
		t.Fatalf("after run C the bank holds %+v", c)
	}
}
