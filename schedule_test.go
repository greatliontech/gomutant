package gomutant

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/gomutant/internal/engine"
)

// scheduleBatches reconstructs the historical probe partition for bank and
// coverage-counterexample fixtures. Production never schedules these batches.
func scheduleBatches(fns []string) [][]string {
	if len(fns) == 0 {
		return nil
	}
	b := int(math.Ceil(math.Sqrt(float64(len(fns)))))
	size := (len(fns) + b - 1) / b
	var out [][]string
	for start := 0; start < len(fns); start += size {
		out = append(out, fns[start:min(start+size, len(fns))])
	}
	return out
}

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
			t.Fatalf("n=%d: partition = %v", n, flat)
		}
		if len(batches) > int(math.Ceil(math.Sqrt(float64(n)))) {
			t.Fatalf("n=%d: too many batches", n)
		}
	}
}

func scheduleTestWork(pkg, coverPkg string, fns []string) work {
	oracle := make([]string, len(fns))
	set := map[string]bool{}
	for i, fn := range fns {
		oracle[i] = pkg + "." + fn
		set[oracle[i]] = true
	}
	return work{oracle: oracle, oracleSet: set,
		groups:     []group{{pkgs: []string{pkg}, runRegex: testRunRegex(fns)}},
		targetView: &subjectView{subject: gofresh.Subject{Package: coverPkg}},
	}
}

// Every group's complete inventory, order, flags and bound reach the executor.
// A decoded historical coverage bank cannot affect that dispatch.
func TestCompleteGroupsPreserveMembershipAndBounds(t *testing.T) {
	const pkg = "example.com/p"
	w := scheduleTestWork(pkg, pkg, []string{"TestA", "TestB", "TestC", "TestD"})
	w.groups[0].flags = []string{"-count=1"}
	w.groups[0].moduleDir, w.groups[0].packageDir = "/module", "/module/p"
	w.groups = append(w.groups, group{pkgs: []string{"example.com/q"}, runRegex: "^TestQ$", flags: []string{"-count=2"}, moduleDir: "/other", packageDir: "/other/q"})
	m := engine.Mutant{Position: "f.go:10:2", Extent: "10:2-12:3", Replacements: []engine.Replacement{{File: "f.go"}}}
	bank := &baselineBank{file: baselineBankFile{Coverage: map[string]bankedCoverage{
		coverageKey(w.groups[0], pkg): {Plan: 2, Batches: []bankedBatch{
			{Index: 0, Fns: []string{"TestA", "TestB"}, Coverage: engine.CoverageForTest(nil).Persist()},
			{Index: 1, Fns: []string{"TestC", "TestD"}, Coverage: engine.CoverageForTest(map[string][]engine.CoverSpanForTest{pkg + "/f.go": {{StartLine: 9, StartCol: 1, EndLine: 20, EndCol: 1}}}).Persist()},
		}},
	}}}
	original := seams.runMutantObserved
	t.Cleanup(func() { seams.runMutantObserved = original })
	for _, derived := range []bool{false, true} {
		var got []group
		var budgets []time.Duration
		seams.runMutantObserved = func(_ context.Context, _ string, _ engine.Mutant, pkgs []string, pattern string, bound time.Duration, flags []string, module, dir string, _ []string, _ []runtimeinput.ScratchNamespace, _ []string, _ engine.OracleBounds) (engine.MutantOutcome, string, bool, runtimeinput.Observation, string, string, error) {
			got = append(got, group{pkgs: pkgs, runRegex: pattern, flags: flags, moduleDir: module, packageDir: dir})
			budgets = append(budgets, bound)
			return engine.MutantSurvived, "", false, runtimeinput.Observation{}, "full process incomplete", "", nil
		}
		opts := runOptions{Options: Options{OracleTimeout: time.Minute}, baselineBank: bank}
		wantBudgets := []time.Duration{time.Minute, time.Minute}
		if derived {
			opts.groupBudget = func(g group) time.Duration {
				if g.pkgs[0] == pkg {
					return 17 * time.Second
				}
				return 29 * time.Second
			}
			wantBudgets = []time.Duration{17 * time.Second, 29 * time.Second}
		}
		out, killer, _, _, incomplete, err := (&Tree{}).executeMutant(context.Background(), w, m, opts, nil)
		if err != nil || out != engine.MutantSurvived || killer != "" || incomplete != "full process incomplete" {
			t.Fatalf("result = %v %q %q %v", out, killer, incomplete, err)
		}
		if !reflect.DeepEqual(got, w.groups) || !slices.Equal(budgets, wantBudgets) {
			t.Fatalf("derived=%v: groups=%+v bounds=%v", derived, got, budgets)
		}
	}
}

// A fresh complete-group kill needs no additional subset baseline. Timeouts
// are scored once under the group's own bound; attribution still refuses a
// named test outside the oracle.
func TestCompleteGroupOutcomes(t *testing.T) {
	const pkg = "example.com/p"
	w := scheduleTestWork(pkg, pkg, []string{"TestA", "TestB"})
	m := engine.Mutant{Position: "f.go:10:2"}
	original, ground := seams.runMutantObserved, seams.killGround
	t.Cleanup(func() { seams.runMutantObserved, seams.killGround = original, ground })
	seams.killGround = func(context.Context, string, string, string, time.Duration, []string, string, string, []string, []runtimeinput.ScratchNamespace, []string, engine.OracleBounds) (int, bool, []string, string, runtimeinput.Observation, error) {
		t.Error("fresh full-group execution paid a subset baseline")
		return 0, false, nil, "", runtimeinput.Observation{}, nil
	}
	for _, tc := range []struct {
		name, killer string
		out          engine.MutantOutcome
		diagnostic   string
		wantErr      bool
	}{
		{"survivor", "", engine.MutantSurvived, "", false},
		{"test kill", pkg + ".TestB", engine.MutantKilled, "", false},
		{"timeout", TimeoutKiller, engine.MutantKilled, "", false},
		{"foreign killer", pkg + ".TestForeign", engine.MutantKilled, "", true},
		{"build discard", "", engine.MutantDiscarded, "compiler rejected mutant", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			seams.runMutantObserved = func(_ context.Context, _ string, _ engine.Mutant, _ []string, pattern string, bound time.Duration, _ []string, _, _ string, _ []string, _ []runtimeinput.ScratchNamespace, _ []string, _ engine.OracleBounds) (engine.MutantOutcome, string, bool, runtimeinput.Observation, string, string, error) {
				calls++
				if pattern != "^(TestA|TestB)$" || bound != 19*time.Second {
					t.Errorf("pattern=%q bound=%v", pattern, bound)
				}
				return tc.out, tc.killer, tc.killer == TimeoutKiller, runtimeinput.Observation{}, "", tc.diagnostic, nil
			}
			out, killer, decided, _, _, err := (&Tree{}).executeMutant(context.Background(), w, m, runOptions{Options: Options{OracleTimeout: 19 * time.Second}}, nil)
			if (err != nil) != tc.wantErr || calls != 1 {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
			if !tc.wantErr && (out != tc.out || killer != tc.killer || decided != (tc.killer == TimeoutKiller)) {
				t.Fatalf("result=%v/%q/%v", out, killer, decided)
			}
		})
	}
}

func TestSerialConfirmationRunsUnscheduled(t *testing.T) {
	const pkg = "example.com/p"
	w := scheduleTestWork(pkg, pkg, []string{"TestA", "TestB", "TestC", "TestD"})
	m := engine.Mutant{Position: "f.go:10:2", Extent: "10:2-12:3", Replacements: []engine.Replacement{{File: "f.go"}}}
	original := seams.runMutantObserved
	t.Cleanup(func() { seams.runMutantObserved = original })
	var patterns []string
	seams.runMutantObserved = func(_ context.Context, _ string, _ engine.Mutant, _ []string, pattern string, _ time.Duration, _ []string, _, _ string, _ []string, _ []runtimeinput.ScratchNamespace, _ []string, _ engine.OracleBounds) (engine.MutantOutcome, string, bool, runtimeinput.Observation, string, string, error) {
		patterns = append(patterns, pattern)
		return engine.MutantSurvived, "", false, runtimeinput.Observation{}, "", "", nil
	}
	memo := map[scopedBaselineKey]bool{{pkg: pkg, run: "^TestA$"}: false}
	if _, _, _, _, _, err := (&Tree{}).confirmMutant(context.Background(), w, m, pkg+".TestA", memo, runOptions{}, nil); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(patterns, []string{"^(TestA|TestB|TestC|TestD)$"}) {
		t.Fatalf("serial fallback patterns=%v", patterns)
	}
}

// A misleading coverage profile may change advisory buckets, never the
// candidate's first execution, its verdict, or its measured process evidence.
func TestRunKeepsCompleteOracleDespiteCoverage(t *testing.T) {
	if testing.Short() {
		t.Skip("runs real mutant oracles")
	}
	tr := fixtureTree(t)
	const pkg = "example.com/fixture/lib"
	full := testRunRegex([]string{"TestAdd", "TestWeak"})
	original, coverage := seams.runMutantObserved, seams.coveredPositions
	t.Cleanup(func() { seams.runMutantObserved, seams.coveredPositions = original, coverage })
	var mu sync.Mutex
	var patterns, probePatterns []string
	seams.runMutantObserved = func(ctx context.Context, dir string, m engine.Mutant, pkgs []string, pattern string, bound time.Duration, flags []string, module, wd string, brackets []string, namespaces []runtimeinput.ScratchNamespace, env []string, bounds engine.OracleBounds) (engine.MutantOutcome, string, bool, runtimeinput.Observation, string, string, error) {
		mu.Lock()
		patterns = append(patterns, pattern)
		mu.Unlock()
		return original(ctx, dir, m, pkgs, pattern, bound, flags, module, wd, brackets, namespaces, env, bounds)
	}
	seams.coveredPositions = func(_ context.Context, _, _, pattern, _ string, _ time.Duration, _ []string, _ []string, _ engine.DirectiveCoverageView, _ engine.OracleBounds) (engine.Coverage, error) {
		mu.Lock()
		probePatterns = append(probePatterns, pattern)
		mu.Unlock()
		return engine.CoverageForTest(nil), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	var events []ExecutionEvent
	findings, err := tr.Run(ctx, []Target{{Symbol: pkg + ".Add", Oracle: []string{pkg + ".TestAdd", pkg + ".TestWeak"}}}, Options{Jobs: 1, OracleTimeout: time.Minute, Executing: func(e ExecutionEvent) { events = append(events, e) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Killed == 0 || findings[0].OracleExecutionPolicy != FullOracleExecutionPolicy {
		t.Fatalf("fresh result=%+v", findings)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(patterns) < 2 {
		t.Fatalf("vacuous execution: %v", patterns)
	}
	for _, p := range patterns {
		if p != full {
			t.Errorf("mutant ran subset %q", p)
		}
	}
	for _, p := range probePatterns {
		if p != full {
			t.Errorf("paid scheduling probe %q", p)
		}
	}
	for _, s := range findings[0].Survivors {
		if s.Execution == "covering-passed" {
			t.Errorf("fresh narrowed survivor: %+v", s)
		}
	}
	for _, e := range events {
		if e.Phase == "probing" || e.Phase == "audit" || e.Phase == "audit-flip" || e.EstimateNarrowed != 0 || e.EstimateAudit != "" || len(e.EstimateNoSignal) != 0 {
			t.Errorf("obsolete scheduling event=%+v", e)
		}
	}
}

func TestFreshShapedFindingRecordsCompleteOraclePolicy(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a real shaped oracle")
	}
	dir := writeShapedFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	tr, err := LoadContext(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	target := Target{Symbol: "recipe:guard-empty-input",
		Manual: &ManualSpec{File: "guard/guard.go", Edits: []ManualEdit{{Find: `if s == "" {`, Replace: `if false {`}}},
		Oracle: []string{"example.com/shaped/guard.TestEmptyRefused"}, OracleExplicit: true,
	}
	findings, err := tr.Run(ctx, []Target{target}, Options{Jobs: 1, OracleTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Mutants != 1 || findings[0].Killed != 1 || findings[0].OracleExecutionPolicy != FullOracleExecutionPolicy {
		t.Fatalf("fresh shaped execution=%+v", findings)
	}
}

// Coverage still distinguishes positive reach for advisory survivor buckets.
// It probes each group once under its complete pattern, never batch patterns.
func TestCompleteOracleCoverageRemainsAdvisory(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree for advisory coverage")
	}
	tr := fixtureTree(t)
	const pkg = "example.com/fixture/lib"
	w := scheduleTestWork(pkg, pkg, []string{"TestAdd", "TestWeak"})
	original := seams.coveredPositions
	t.Cleanup(func() { seams.coveredPositions = original })
	calls := 0
	seams.coveredPositions = func(_ context.Context, _, testPkg, pattern, coverPkg string, bound time.Duration, _ []string, _ []string, _ engine.DirectiveCoverageView, _ engine.OracleBounds) (engine.Coverage, error) {
		calls++
		if testPkg != pkg || coverPkg != pkg || pattern != "^(TestAdd|TestWeak)$" || bound != 23*time.Second {
			t.Errorf("advisory probe=%s %q %s %v", testPkg, pattern, coverPkg, bound)
		}
		return engine.CoverageForTest(map[string][]engine.CoverSpanForTest{pkg + "/lib.go": {{StartLine: 9, StartCol: 1, EndLine: 20, EndCol: 1}}}), nil
	}
	cache := map[string]engine.Coverage{}
	opts := runOptions{Options: Options{OracleTimeout: time.Minute}, groupLeash: func(group) time.Duration { return 23 * time.Second }}
	f := Finding{Survivors: []Survivor{
		{Position: "lib.go:10:2", Extent: "10:2-12:3"},
		{Position: "lib.go:30:2", Extent: "30:2-31:3"},
		{Position: "lib.go:10:2", Extent: "10:2-12:3", Execution: "flipped-kill", WithdrawnKiller: pkg + ".TestAdd"},
	}}
	for range 2 {
		if err := tr.bucketSurvivorExecution(context.Background(), &f, w, opts, nil, cache, 0); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 || f.Survivors[0].Execution != "executed-and-passed" || f.Survivors[1].Execution != "coverage-unobserved" || f.Survivors[2].Execution != "flipped-kill" || f.Survivors[2].WithdrawnKiller != pkg+".TestAdd" {
		t.Fatalf("calls=%d survivors=%+v", calls, f.Survivors)
	}
}
