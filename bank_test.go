package gomutant

import (
	"context"
	"os"
	"path/filepath"
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
	b.putCoverage("c", bankedCoverage{Batches: []bankedBatch{{Fns: []string{"TestA"}, DurMillis: 7}}})
	again := openBaselineBank(moduleDir)
	if _, ok := again.baseline("k"); !ok {
		t.Fatal("the coverage deposit dropped the persisted baseline")
	}
	cov, ok := again.coverage("c")
	if !ok || len(cov.Batches) != 1 || cov.Batches[0].DurMillis != 7 {
		t.Fatalf("the coverage deposit did not persist immediately: %+v ok=%v", cov, ok)
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
	restoreProbe := groupBaselineProbe
	restoreCov := campaignCoveredPositions
	restoreMinT := scheduleMinTests
	restoreMinC := scheduleMinCandidates
	scheduleMinTests = 2
	scheduleMinCandidates = 1
	t.Cleanup(func() {
		groupBaselineProbe = restoreProbe
		campaignCoveredPositions = restoreCov
		scheduleMinTests = restoreMinT
		scheduleMinCandidates = restoreMinC
	})
	var baselineProbes, coverageProbes atomic.Int64
	groupBaselineProbe = func(ctx context.Context, dir, pkg, run string, timeout time.Duration, flags []string, moduleDir, packageDir string, brackets []string, namespaces []runtimeinput.ScratchNamespace, env []string) (int, bool, []string, runtimeinput.Observation, error) {
		baselineProbes.Add(1)
		return restoreProbe(ctx, dir, pkg, run, timeout, flags, moduleDir, packageDir, brackets, namespaces, env)
	}
	campaignCoveredPositions = func(ctx context.Context, dir, testPkg, runRegex, coverPkg string, timeout time.Duration, flags []string, env []string, view engine.DirectiveCoverageView) (engine.Coverage, error) {
		coverageProbes.Add(1)
		return engine.CoveredPositions(ctx, dir, testPkg, runRegex, coverPkg, timeout, flags, env, view)
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
