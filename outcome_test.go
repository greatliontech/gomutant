package gomutant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBaselineProofCannotSupplyTransformedOutcomeSupport(t *testing.T) {
	if testing.Short() {
		t.Skip("measures an oracle whose file outcomes are unsupported")
	}
	tr := fixtureTree(t)
	ctx := context.Background()
	target := Target{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/observed.TestObservedInput"}}
	first, err := tr.Run(ctx, []Target{target}, Options{Budget: 1})
	if err != nil || len(first) != 1 {
		t.Fatalf("run=%+v err=%v", first, err)
	}
	if !first[0].OracleEvidence[0].ObservationProof.Observable {
		t.Fatal("fixture supplied no positive baseline proof")
	}
	inspection, err := tr.InspectFinding(ctx, first[0], nil)
	if err != nil || inspection.State != FindingUnverifiable {
		t.Fatalf("baseline proof borrowed by mutant: %+v %v", inspection, err)
	}
	second, err := tr.Run(ctx, []Target{target}, Options{Budget: 1, Prior: first})
	if err != nil || len(second) != 1 || second[0].Cached {
		t.Fatalf("unsupported outcomes served: %+v %v", second, err)
	}
	historical := first[0]
	historical.TargetEvidence.RuntimeInputs = "eyJ2IjoxfQ"
	inspection, err = tr.InspectFinding(ctx, historical, nil)
	if err != nil || inspection.State != FindingUnverifiable || !strings.Contains(inspection.Reason, "unsupported manifest version") {
		t.Fatalf("historical runtime evidence was upgraded: %+v %v", inspection, err)
	}
}

// assertFixturePurity makes the trust premise explicit in tests of mechanisms
// other than outcome derivation: portability, input drift and regime pins still
// apply to identity-only evidence under a caller's purity override.
func assertFixturePurity(t *testing.T, dir, file, symbol string) {
	t.Helper()
	path := filepath.Join(dir, file)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	old := "func " + symbol + "("
	if strings.Count(string(data), old) != 1 {
		t.Fatalf("fixture does not uniquely declare %s", symbol)
	}
	updated := strings.Replace(string(data), old, "//gofresh:pure\n"+old, 1)
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
}
