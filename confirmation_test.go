package gomutant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A kill measured under a concurrent pool re-executes alone and the
// serial execution is the scored one (REQ-exec-attribution): kills run
// twice at Jobs>1, once at Jobs=1, and a kill that does not reproduce
// serially scores from the serial run. The counting oracle writes its
// counter outside the observation bracket, so this window's evidence is
// volatile and the stride gate never samples — the every-kill formula
// below is exactly the volatile arm's pin; the clean-fixture stride is
// pinned by TestRunExecutingEventsAdvisory's formula.
func TestRunConfirmsKillsSerially(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	counter := filepath.Join(t.TempDir(), "executions")
	t.Setenv("GOMUTANT_EXECUTION_COUNTER", counter)
	tr, err := Load(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	target := Target{Symbol: "example.com/fixture/counting.Value", Oracle: []string{"example.com/fixture/counting.TestCountingStrict"}}
	fs, err := tr.Run(context.Background(), []Target{target}, Options{Jobs: 2})
	if err != nil {
		t.Fatal(err)
	}
	f := fs[0]
	if f.Killed == 0 {
		t.Fatalf("counting fixture killed nothing: %+v", f)
	}
	data, _ := os.ReadFile(counter)
	// The baseline validity repeat runs the oracle twice before any
	// mutant; the killer-scoped baseline probes the killing test
	// (validity-doubled, once per distinct killer — here the scoped
	// regex equals the whole single-test oracle); kills execute
	// twice (concurrent + serial confirmation), survivors once.
	const baselineRuns = 2
	const scopedBaselineRuns = 2
	want := baselineRuns + scopedBaselineRuns + f.Killed*2 + (f.Mutants - f.Killed)
	if got := strings.Count(string(data), "\n"); got != want {
		t.Fatalf("oracle executions = %d, want %d (kills confirmed serially, survivors once)", got, want)
	}

	single := filepath.Join(t.TempDir(), "executions-single")
	t.Setenv("GOMUTANT_EXECUTION_COUNTER", single)
	tr2, err := Load(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	fs2, err := tr2.Run(context.Background(), []Target{target}, Options{Jobs: 1})
	if err != nil {
		t.Fatal(err)
	}
	data2, _ := os.ReadFile(single)
	if got := strings.Count(string(data2), "\n"); got != baselineRuns+fs2[0].Mutants {
		t.Fatalf("Jobs=1 executions = %d, want %d (no siblings, no confirmation)", got, baselineRuns+fs2[0].Mutants)
	}
}

// A kill that does not reproduce alone is scored from the serial run:
// the interference-shaped failure flips to a survivor instead of a
// false kill (REQ-exec-attribution).
func TestRunSerialConfirmationReplacesNonReproducingKill(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	marker := filepath.Join(t.TempDir(), "marker")
	t.Setenv("GOMUTANT_FLAKY_MARKER", marker)
	tr, err := Load(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	target := Target{Symbol: "example.com/fixture/flaky.Value", Oracle: []string{"example.com/fixture/flaky.TestFlaky"}}
	fs, err := tr.Run(context.Background(), []Target{target}, Options{Jobs: 2})
	if err != nil {
		t.Fatal(err)
	}
	f := fs[0]
	returnZero := false
	flipped := 0
	for _, s := range f.Survivors {
		if s.Operator == "return: zero" {
			returnZero = true
		}
		// The flip rides the RECORD, not only the event stream
		// (REQ-exec-survivor-evidence's flipped-kill bucket): whichever
		// candidate's window run scored the marker kill, its survivor
		// row names the withdrawn killer so triage starts from the
		// nondeterminism, and the bucket marks it as oracle
		// nondeterminism — never a plain bucket, and never overwritten
		// by the unverifiability stamp this fixture's marker read
		// otherwise forces onto every row.
		if s.Execution == "flipped-kill" {
			flipped++
			if s.WithdrawnKiller == "" {
				t.Fatalf("flipped survivor carries no withdrawn killer: %+v", s)
			}
		}
		if s.WithdrawnKiller != "" && s.Execution != "flipped-kill" {
			t.Fatalf("withdrawn killer without the flipped-kill bucket: %+v", s)
		}
	}
	if flipped == 0 {
		t.Fatalf("no survivor records the confirmation flip: %+v", f.Survivors)
	}
	if !returnZero {
		t.Fatalf("non-reproducing kill was not rescored as a survivor: %+v", f)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the concurrent kill never happened (marker absent): %v", err)
	}
	// Replacement is wholesale: the clean serial run's evidence stands,
	// so no crash-shaped incompleteness from a concurrent look survives
	// onto any confirmed survivor (compile-rejection discard evidence is
	// legitimate and unrelated).
	for _, candidate := range f.CandidateEvidence {
		if strings.Contains(candidate.Reason, "before observation finalization") {
			t.Fatalf("a concurrent look's incomplete evidence survived the serial replacement: %+v", candidate)
		}
	}
}

// A test-attributed kill confirms KILLER-SCOPED: the serial run
// executes the killing test alone, never the oracle's -failfast
// prefix (REQ-exec-attribution's killer-scoped stage). The counting
// fixture's weak TestCounting sorts ahead of the killing
// TestCountingStrict, so the old full-oracle confirmation paid it on
// every kill; the counter arithmetic pins that confirmations now add
// exactly ONE execution per kill.
func TestRunConfirmationIsKillerScoped(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	counter := filepath.Join(t.TempDir(), "executions")
	t.Setenv("GOMUTANT_EXECUTION_COUNTER", counter)
	tr, err := Load(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	target := Target{Symbol: "example.com/fixture/counting.Value", Oracle: []string{
		"example.com/fixture/counting.TestCounting",
		"example.com/fixture/counting.TestCountingStrict",
	}}
	fs, err := tr.Run(context.Background(), []Target{target}, Options{Jobs: 2})
	if err != nil {
		t.Fatal(err)
	}
	f := fs[0]
	if f.Killed == 0 {
		t.Fatalf("counting fixture killed nothing: %+v", f)
	}
	// f.Mutants already excludes discards (generated = mutants +
	// discarded), and a discarded candidate never reaches the
	// oracle, so the formula below needs no discard term.
	for _, s := range f.Survivors {
		t.Logf("survivor: %+v", s)
	}
	// The counter writes outside the observation bracket, so this
	// window's evidence is volatile and the stride gate never
	// samples: every kill confirms, which is what makes the per-kill
	// arithmetic exact (the same volatile-arm pin
	// TestRunConfirmsKillsSerially documents).
	data, _ := os.ReadFile(counter)
	// Two baseline validity runs execute both tests; each measured
	// mutant executes both under -failfast (the weak test passes,
	// then the strict one decides); the killer-scoped BASELINE
	// probes the killing test alone (validity-doubled, memoized once
	// per distinct killer across the campaign); each kill's serial
	// confirmation then executes the KILLING test alone — one line,
	// not two.
	const baselineRuns = 2
	const scopedBaselineRuns = 2
	want := baselineRuns*2 + f.Mutants*2 + scopedBaselineRuns + f.Killed*1
	if got := strings.Count(string(data), "\n"); got != want {
		t.Fatalf("oracle executions = %d, want %d (killer-scoped confirmations add one execution per kill)", got, want)
	}
}

// A killer-scoped serial run that PASSES scores nothing by itself:
// the full serial oracle runs and its verdict is the scored one
// (REQ-exec-attribution's killer-scoped stage — a survivor verdict
// always rests on the whole oracle). The flakyattr fixture kills
// cleanly attributed on the first look and passes alone thereafter,
// so the flip must come out of the fallback, scored survivor.
func TestRunKillerScopedFlipFallsBackToFullOracle(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	marker := filepath.Join(t.TempDir(), "marker")
	t.Setenv("GOMUTANT_FLAKYATTR_MARKER", marker)
	counter := filepath.Join(t.TempDir(), "executions")
	t.Setenv("GOMUTANT_EXECUTION_COUNTER", counter)
	tr, err := Load(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	target := Target{Symbol: "example.com/fixture/flakyattr.Value", Oracle: []string{
		"example.com/fixture/flakyattr.TestAWeak",
		"example.com/fixture/flakyattr.TestFlakyAttr",
	}}
	fs, err := tr.Run(context.Background(), []Target{target}, Options{Jobs: 2})
	if err != nil {
		t.Fatal(err)
	}
	f := fs[0]
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("no first-look attributed kill ever happened (marker absent): %v", err)
	}
	if f.Killed != 0 {
		t.Fatalf("a non-reproducing attributed kill stayed killed: %+v", f)
	}
	if len(f.Survivors) == 0 {
		t.Fatalf("the flip scored no survivor: %+v", f)
	}
	// The fallback provably ran the FULL oracle: TestAWeak executes
	// in baselines and full-oracle runs only (a killer-scoped run
	// never reaches it), so its count exceeding baselines plus one
	// per measured mutant is the fallback's own signature — a flip
	// scored from the scoped run alone would leave it at exactly
	// that floor.
	data, _ := os.ReadFile(counter)
	weak := strings.Count(string(data), "TestAWeak\n")
	const baselineRuns = 2
	if floor := baselineRuns + f.Mutants; weak <= floor {
		t.Fatalf("TestAWeak executions = %d, want > %d: no full-oracle fallback ever ran", weak, floor)
	}
}

// An ORDER-DEPENDENT killer — one that fails standalone because it
// needs a sibling test's setup — must never let the killer-scoped
// stage confirm a sibling-shaped false kill: the scoped BASELINE of
// the unmutated tree fails, the scope is refused, and the full
// serial oracle scores the flip to a survivor
// (REQ-exec-attribution's scoped-baseline obligation).
func TestRunKillerScopedRefusesOrderDependentKiller(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	marker := filepath.Join(t.TempDir(), "marker")
	t.Setenv("GOMUTANT_ORDERDEP_MARKER", marker)
	tr, err := Load(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	target := Target{Symbol: "example.com/fixture/orderdep.Value", Oracle: []string{
		"example.com/fixture/orderdep.TestASetup",
		"example.com/fixture/orderdep.TestOrderDep",
	}}
	fs, err := tr.Run(context.Background(), []Target{target}, Options{Jobs: 2})
	if err != nil {
		t.Fatal(err)
	}
	f := fs[0]
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("no first-look false kill ever happened (marker absent): %v", err)
	}
	if f.Killed != 0 {
		t.Fatalf("an order-dependent killer's false kill was confirmed: %+v", f)
	}
	if len(f.Survivors) == 0 {
		t.Fatalf("the false kill did not flip to a survivor: %+v", f)
	}
}
