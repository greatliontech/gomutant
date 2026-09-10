package gomutant

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/gomutant/internal/engine"
	"github.com/greatliontech/gomutant/internal/windowcost"
)

// The measurement leash is lifted by a banked duration to the budget
// that duration derives, and never tightened: the fixed value is the
// floor (REQ-exec-oracle-budget).
func TestLeashForLiftsAndNeverTightens(t *testing.T) {
	fixed := 10 * time.Minute
	for _, c := range []struct {
		name   string
		banked time.Duration
		want   time.Duration
	}{
		{"no entry", 0, fixed},
		{"faster than the floor", time.Second, fixed},
		{"budget under the floor", 2 * time.Minute, fixed},
		{"slow group lifts", 20 * time.Minute, derivedOracleBudget(20 * time.Minute)},
	} {
		if got := leashFor(fixed, c.banked); got != c.want {
			t.Fatalf("%s: leash = %s; want %s", c.name, got, c.want)
		}
	}
}

// The ephemeral face's baseline and coverage probes run under the leash
// the bank lifts for the test package — any banked measurement of it,
// under any pattern or scope — and under the fixed leash otherwise.
func TestEphemeralLeashLiftsFromTheBank(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	tr := fixtureTree(t)
	captured := errors.New("bound captured")
	var bound time.Duration
	probe := testProbe
	defer func() { testProbe = probe }()
	testProbe = func(_ context.Context, _, _, _ string, timeout time.Duration, _, _ []string, bounds engine.OracleBounds) (int, bool, string, error) {
		bound = timeout
		return 0, false, "", captured
	}
	req := EphemeralRequest{File: "lib/lib.go", Mutant: []byte("package lib\n\nfunc Add(a, b int) int { return a - b }\n"), TestPkg: "example.com/fixture/lib", Run: "^TestAdd$", Runs: 1}
	if _, err := tr.RunEphemeral(context.Background(), req); !errors.Is(err, captured) {
		t.Fatalf("probe seam not reached: %v", err)
	}
	if bound != ephemeralBaselineLeash {
		t.Fatalf("unbanked leash = %s; want the fixed %s", bound, ephemeralBaselineLeash)
	}
	bank := openBaselineBank(tr.dir)
	entry := func(pkg, run string, d time.Duration) {
		bank.putBaseline(pkg+"\x00"+run+"\x00\x00"+tr.dir+"\x00"+tr.dir+"\x00", bankedBaseline{RawMillis: int64(d / time.Millisecond)})
	}
	entry("example.com/fixture/lib", "^TestOther$", 20*time.Minute)
	entry("example.com/fixture/lib", "^TestQuick$", 5*time.Second)
	entry("example.com/fixture/other", "^TestX$", 3*time.Hour)
	bank.save()
	if _, err := tr.RunEphemeral(context.Background(), req); !errors.Is(err, captured) {
		t.Fatalf("probe seam not reached: %v", err)
	}
	want := derivedOracleBudget(20 * time.Minute)
	if bound != want {
		t.Fatalf("banked leash = %s; want the lift to %s (the package's LONGEST entry; another package's entry must not lift it)", bound, want)
	}
	// The command-deadline refusal names the leash that governed.
	testProbe = func(context.Context, string, string, string, time.Duration, []string, []string, engine.OracleBounds) (int, bool, string, error) {
		return 0, false, "", fmt.Errorf("running baseline: %w", context.DeadlineExceeded)
	}
	if _, err := tr.RunEphemeral(context.Background(), req); err == nil || !strings.Contains(err.Error(), want.String()+"-leashed") {
		t.Fatalf("deadline refusal = %v; want it to name the lifted %s leash", err, want)
	}
}

// The ephemeral coverage probe runs under the lifted leash in both
// modes — the probe is an instrumented rebuild heavier than the oracle,
// so a slow oracle's lift applies to it whether the budget is derived
// or explicit.
func TestEphemeralCoverageProbeLeashLiftsFromTheBank(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per probe")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	restore := coveredPositions
	var got []time.Duration
	coveredPositions = func(_ context.Context, _, _, _, _ string, timeout time.Duration, _ []string, _ []string, _ engine.DirectiveCoverageView, _ engine.OracleBounds) (engine.Coverage, error) {
		got = append(got, timeout)
		return engine.Coverage{}, errors.New("probe refused")
	}
	defer func() { coveredPositions = restore }()
	tr := fixtureTree(t)
	bank := openBaselineBank(tr.dir)
	bank.putBaseline("example.com/fixture/lib\x00^TestOther$\x00\x00"+tr.dir+"\x00"+tr.dir+"\x00", bankedBaseline{RawMillis: int64((20 * time.Minute) / time.Millisecond)})
	bank.save()
	linkedIdle, err := os.ReadFile("internal/engine/testdata/fixturemod/genp/gen.go")
	if err != nil {
		t.Fatal(err)
	}
	mutated := strings.Replace(string(linkedIdle), "type G struct{}", "type G struct{ X int }", 1)
	for _, timeout := range []time.Duration{0, time.Minute} {
		res, err := tr.RunEphemeral(context.Background(), EphemeralRequest{File: "genp/gen.go", Mutant: []byte(mutated), TestPkg: "example.com/fixture/lib", Run: "^TestWeak$", OracleTimeout: timeout, Runs: 1})
		if err != nil {
			t.Fatal(err)
		}
		if res.Killed {
			t.Fatalf("linked-unexecuted replacement killed: %+v", res)
		}
	}
	want := derivedOracleBudget(20 * time.Minute)
	if len(got) != 2 || got[0] != want || got[1] != want {
		t.Fatalf("coverage probe bounds = %v, want the lifted %v leash in both modes", got, want)
	}
}

// A campaign's probes run under the leash its group's banked duration
// lifts: the advisory coverage probe of a group served from the bank,
// the baseline probe on the miss path (the entry's pins moved but it
// still says how long the oracle takes) — and an explicit oracle
// timeout is never lifted (the caller's uniform override).
func TestCampaignLeashLiftsFromTheBank(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test baselines across four campaigns")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	restoreProbe := groupBaselineProbe
	restoreCov := campaignCoveredPositions
	restoreMinT := windowcost.ScheduleMinTests
	restoreMinC := windowcost.ScheduleMinCandidates
	// Lowered so the schedule's coverage probe fires over this two-test
	// fixture (its production minimums gate it out of a probe this
	// small); the survivor-bucket probe fires on Sub's surviving mutants.
	windowcost.ScheduleMinTests = 2
	windowcost.ScheduleMinCandidates = 1
	t.Cleanup(func() {
		groupBaselineProbe = restoreProbe
		campaignCoveredPositions = restoreCov
		windowcost.ScheduleMinTests = restoreMinT
		windowcost.ScheduleMinCandidates = restoreMinC
	})
	var bounds, probeBounds []time.Duration
	groupBaselineProbe = func(ctx context.Context, dir, pkg, run string, timeout time.Duration, flags []string, moduleDir, packageDir string, brackets []string, namespaces []runtimeinput.ScratchNamespace, env []string, oracleBounds engine.OracleBounds) (int, bool, []string, string, runtimeinput.Observation, error) {
		bounds = append(bounds, timeout)
		return restoreProbe(ctx, dir, pkg, run, timeout, flags, moduleDir, packageDir, brackets, namespaces, env, oracleBounds)
	}
	campaignCoveredPositions = func(ctx context.Context, dir, testPkg, runRegex, coverPkg string, timeout time.Duration, flags []string, env []string, view engine.DirectiveCoverageView, oracleBounds engine.OracleBounds) (engine.Coverage, error) {
		probeBounds = append(probeBounds, timeout)
		return engine.CoveredPositions(ctx, dir, testPkg, runRegex, coverPkg, timeout, flags, env, view, oracleBounds)
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":      "module example.com/leashmod\n\ngo 1.26\n",
		"a/a.go":      "package a\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n\nfunc Sub(a, b int) int {\n\treturn a - b\n}\n",
		"a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal()\n\t}\n}\n\nfunc TestAddZero(t *testing.T) {\n\tif Add(0, 0) != 0 {\n\t\tt.Fatal()\n\t}\n}\n\nfunc TestSub(t *testing.T) {\n\t_ = Sub(1, 1)\n}\n",
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
	// Sub's test asserts nothing, so its mutants survive and the
	// survivor-bucket coverage probe runs beside the schedule's.
	target := []Target{{Symbol: "example.com/leashmod/a.Add"}, {Symbol: "example.com/leashmod/a.Sub"}}
	own := RunOwnWrites(filepath.Join(dir, ".gomutant", "findings.json"))
	run := func(name string, o Options) {
		t.Helper()
		bounds, probeBounds = nil, nil
		o.OwnWrites = own
		if _, err := load().Run(context.Background(), target, o); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	allEqual := func(ds []time.Duration, want time.Duration) bool {
		for _, d := range ds {
			if d != want {
				return false
			}
		}
		return len(ds) > 0
	}
	run("first", Options{Budget: 1, Jobs: 2})
	if !allEqual(bounds, campaignBaselineLeash) || !allEqual(probeBounds, campaignBaselineLeash) {
		t.Fatalf("first campaign: baseline bounds %v, probe bounds %v; want the fixed leash", bounds, probeBounds)
	}
	// The banked entry says the oracle takes twenty minutes; the
	// coverage entries are dropped so the advisory probes run (a served
	// coverage probe launches nothing) while the baseline serves.
	lifted := derivedOracleBudget(20 * time.Minute)
	rewriteBank := func() {
		t.Helper()
		bank := openBaselineBank(dir)
		bank.mu.Lock()
		if len(bank.file.Baselines) == 0 {
			bank.mu.Unlock()
			t.Fatal("bank holds no entries; want the campaign's")
		}
		for key, e := range bank.file.Baselines {
			e.RawMillis = int64((20 * time.Minute) / time.Millisecond)
			bank.file.Baselines[key] = e
		}
		bank.file.Coverage = nil
		bank.dirty = true
		bank.mu.Unlock()
		bank.save()
	}
	rewriteBank()
	// Served from the bank (a moved budget pin re-measures the target,
	// the entry's pins still hold): no baseline probe, and the advisory
	// probes run under the leash the served duration lifts.
	run("served", Options{Budget: 2, Jobs: 2})
	if len(bounds) != 0 || len(probeBounds) < 3 || !allEqual(probeBounds, lifted) {
		t.Fatalf("served campaign: baseline bounds %v, probe bounds %v; want no baseline probe and the lifted %s on every advisory probe (the schedule's and the survivor buckets')", bounds, probeBounds, lifted)
	}
	// The entry's pins stop serving when the test moves: the baseline
	// probe runs under the leash the stale entry lifts, and the advisory
	// probes then follow the fresh measurement, which lifts nothing.
	edited := strings.Replace(files["a/a_test.go"], "Add(1, 2) != 3", "Add(2, 1) != 3", 1)
	if err := os.WriteFile(filepath.Join(dir, "a", "a_test.go"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	run("miss", Options{Budget: 2, Jobs: 2})
	if !allEqual(bounds, lifted) || !allEqual(probeBounds, campaignBaselineLeash) {
		t.Fatalf("miss campaign: baseline bounds %v, probe bounds %v; want the lifted %s baseline and the fixed advisory leash", bounds, probeBounds, lifted)
	}
	// An explicit timeout is never lifted: neither the advisory probes
	// of a served group nor the baseline probe on a forced miss.
	rewriteBank()
	run("explicit served", Options{Budget: 3, Jobs: 2, OracleTimeout: time.Minute})
	if len(bounds) != 0 || !allEqual(probeBounds, time.Minute) {
		t.Fatalf("explicit served campaign: baseline bounds %v, probe bounds %v; want no baseline probe and the caller's minute", bounds, probeBounds)
	}
	rewriteBank()
	run("explicit forced", Options{Budget: 3, Jobs: 2, OracleTimeout: time.Minute, Force: true})
	if !allEqual(bounds, time.Minute) {
		t.Fatalf("explicit forced campaign: baseline bounds %v; want the caller's minute", bounds)
	}
}
