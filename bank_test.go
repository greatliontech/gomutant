package gomutant

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/gomutant/internal/engine"
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
		t.Fatal("planting historical coverage dropped the persisted baseline")
	}
	cov, ok := again.coverage("c")
	if !ok || cov.Plan != 2 || len(cov.Batches) != 1 || cov.Batches[0].Index != 1 || cov.Batches[0].DurMillis != 7 || len(cov.Failed) != 1 || cov.Failed[0].Index != 0 || cov.Failed[0].Reason != "exit status 1" {
		t.Fatalf("historical per-batch coverage did not round-trip: %+v ok=%v", cov, ok)
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
// unchanged tree serves its baselines from the bank, reporting banked
// baseline events and
// still deriving budgets; editing an oracle test breaks the pins and
// the third campaign probes again. Findings agree throughout.
func TestRunServesBankedBaselinesAcrossRuns(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test baselines across three campaigns")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	restoreProbe := seams.baselineProbe
	restoreCov := seams.coveredPositions
	t.Cleanup(func() {
		seams.baselineProbe = restoreProbe
		seams.coveredPositions = restoreCov
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
	// every baseline serves from the bank, and the
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
		t.Fatalf("second run probed %d coverage passes despite having no survivors", got)
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
	// control re-probes baselines.
	baselineProbes.Store(0)
	coverageProbes.Store(0)
	if _, err := load().Run(context.Background(), target, opts(2, true)); err != nil {
		t.Fatal(err)
	}
	if baselineProbes.Load() == 0 {
		t.Fatal("--force served the banked baseline")
	}

	// A membership-PRESERVING oracle body edit breaks the CONTENT
	// pins (the bank key — package, pattern, flags — is unchanged, so
	// only the evidence-row comparison can catch it): the third
	// campaign probes baselines again.
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

// Historical coverage is decoded and preserved beside baseline writes as
// data. There is no restored schedule or resume decision to derive from it.
func TestLegacyCoverageBankDecodesWithoutScheduling(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	path, err := bankPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const raw = `{"version":2,"coverage":{"legacy":{
		"evidence":[{"symbol":"p.TestA","maximalClosure":"oracle-source","testVariantClosure":"oracle-tests","toolchain":"oracle-toolchain","buildConfig":"oracle-build"}],
		"coverRow":{"symbol":"p.F","maximalClosure":"target-source","testVariantClosure":"target-tests","toolchain":"target-toolchain","buildConfig":"target-build"},
		"plan":3,
		"batches":[{"index":1,"fns":["TestB","TestC"],"durMillis":17,"coverage":{"covered":{"p/f.go":[[2,1,4,7]]},"unsound":["p/generated.go"]}}],
		"failed":[{"index":2,"fns":["TestD"],"reason":"old probe failed"}]
	}}}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	want := bankedCoverage{
		Evidence: []closureRow{{Symbol: "p.TestA", MaximalClosure: "oracle-source", TestVariantClosure: "oracle-tests", Toolchain: "oracle-toolchain", BuildConfig: "oracle-build"}},
		CoverRow: closureRow{Symbol: "p.F", MaximalClosure: "target-source", TestVariantClosure: "target-tests", Toolchain: "target-toolchain", BuildConfig: "target-build"},
		Plan:     3,
		Batches:  []bankedBatch{{Index: 1, Fns: []string{"TestB", "TestC"}, DurMillis: 17, Coverage: engine.PersistedCoverage{Covered: map[string][][4]int{"p/f.go": {{2, 1, 4, 7}}}, Unsound: []string{"p/generated.go"}}}},
		Failed:   []bankedFailure{{Index: 2, Fns: []string{"TestD"}, Reason: "old probe failed"}},
	}
	b := openBaselineBank(dir)
	if got := b.file.Coverage["legacy"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("decoded historical entry=%+v, want %+v", got, want)
	}
	b.putBaseline("new", bankedBaseline{Manifest: "baseline-manifest", Digest: "baseline-digest", RawMillis: 29})
	again := openBaselineBank(dir)
	if got := again.file.Coverage["legacy"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("baseline write changed historical coverage: %+v", got)
	}
	if got, ok := again.baseline("new"); !ok || got.Manifest != "baseline-manifest" || got.Digest != "baseline-digest" || got.RawMillis != 29 {
		t.Fatalf("baseline deposit=%+v, present=%v", got, ok)
	}
}

// Historical complete, partial and failed coverage entries remain data.
// Fresh execution neither restores their omission policy nor resumes probes.
func TestRunIgnoresHistoricalCoverageBanks(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test baselines across campaigns")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	restoreCov := seams.coveredPositions
	restoreRun := seams.runMutantObserved
	t.Cleanup(func() {
		seams.coveredPositions = restoreCov
		seams.runMutantObserved = restoreRun
	})
	var patternsMu sync.Mutex
	var patterns []string
	seams.runMutantObserved = func(ctx context.Context, dir string, m engine.Mutant, pkgs []string, pattern string, bound time.Duration, flags []string, moduleDir, packageDir string, brackets []string, namespaces []runtimeinput.ScratchNamespace, env []string, bounds engine.OracleBounds) (engine.MutantOutcome, string, bool, runtimeinput.Observation, string, string, error) {
		patternsMu.Lock()
		patterns = append(patterns, pattern)
		patternsMu.Unlock()
		return restoreRun(ctx, dir, m, pkgs, pattern, bound, flags, moduleDir, packageDir, brackets, namespaces, env, bounds)
	}
	var probes atomic.Int64
	seams.coveredPositions = func(ctx context.Context, dir, testPkg, runRegex, coverPkg string, timeout time.Duration, flags []string, env []string, view engine.DirectiveCoverageView, bounds engine.OracleBounds) (engine.Coverage, error) {
		// Only the complete advisory pattern remains legitimate.
		if strings.Contains(runRegex, "TestAdd1") && strings.Contains(runRegex, "TestAdd9") {
			return engine.CoverageForTest(nil), nil
		}
		probes.Add(1)
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
		// A body with several operators keeps every increasing budget
		// below the available candidate count.
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
	var fns []string
	for i := 1; i <= 9; i++ {
		fns = append(fns, fmt.Sprintf("TestAdd%d", i))
	}
	full := testRunRegex(fns)
	key := coverageKey(group{pkgs: []string{"example.com/bankmod/a"}, runRegex: full}, "example.com/bankmod/a")
	plan := scheduleBatches(fns)
	complete := bankedCoverage{Plan: len(plan)}
	for i, names := range plan {
		coverage := engine.CoverageForTest(nil).Persist()
		if i == 0 {
			coverage = engine.CoverageForTest(map[string][]engine.CoverSpanForTest{"example.com/bankmod/a/a.go": {{StartLine: 1, StartCol: 1, EndLine: 100, EndCol: 1}}}).Persist()
		}
		complete.Batches = append(complete.Batches, bankedBatch{Index: i, Fns: names, DurMillis: 1, Coverage: coverage})
	}
	partial := bankedCoverage{Plan: 3, Batches: complete.Batches[:1], Failed: []bankedFailure{{Index: 1, Fns: plan[1], Reason: "old probe failed"}}}
	for i, entry := range []bankedCoverage{complete, partial, {Plan: 0, Batches: complete.Batches}} {
		patternsMu.Lock()
		patterns = nil
		patternsMu.Unlock()
		bank := openBaselineBank(dir)
		bank.putCoverage(key, entry)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		findings, err := load().Run(ctx, target, Options{Budget: i + 1, OwnWrites: own, Jobs: 1})
		cancel()
		if err != nil || len(findings) != 1 || findings[0].Mutants == 0 {
			t.Fatalf("run %d: %+v %v", i, findings, err)
		}
		if probes.Load() != 0 {
			t.Fatalf("run %d paid %d batch probes", i, probes.Load())
		}
		patternsMu.Lock()
		gotPatterns := append([]string(nil), patterns...)
		patternsMu.Unlock()
		if len(gotPatterns) == 0 {
			t.Fatalf("run %d executed no candidate", i)
		}
		for _, pattern := range gotPatterns {
			if pattern != full {
				t.Errorf("run %d: historical coverage omitted oracle tests: %q, want %q", i, pattern, full)
			}
		}
		kept, ok := openBaselineBank(dir).coverage(key)
		if !ok || !reflect.DeepEqual(kept, entry) {
			t.Fatalf("fresh run rewrote historical coverage: %+v", kept)
		}
	}
}

// putCoverage plants a historical bank fixture. Production publishes only
// baselines; test fixtures use the existing writer to preserve its file form.
func (b *baselineBank) putCoverage(key string, entry bankedCoverage) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.file.Coverage == nil {
		b.file.Coverage = map[string]bankedCoverage{}
	}
	b.file.Coverage[key] = entry
	b.dirty = true
	b.persistLocked()
}

// coverage reads a historical fixture back without restoring any schedule.
func (b *baselineBank) coverage(key string) (bankedCoverage, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	entry, ok := b.file.Coverage[key]
	return entry, ok
}
