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
// serially scores from the serial run.
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
	// mutant; kills execute twice (concurrent + serial confirmation),
	// survivors once.
	const baselineRuns = 2
	want := baselineRuns + f.Killed*2 + (f.Mutants - f.Killed)
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
	for _, s := range f.Survivors {
		if s.Operator == "return: zero" {
			returnZero = true
		}
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
