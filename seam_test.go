package gomutant

import (
	"context"
	"strings"
	"testing"
)

// TestParseStipulatorTargets pins the reference external producer's adapter
// (REQ-target-producers): witnesses become the oracle, requirement ids ride
// as labels, unknown versions and symbol-less entries are refused.
func TestParseStipulatorTargets(t *testing.T) {
	doc := `{"stipulatorTargets": 1, "targets": [
		{"symbol": "example.com/p.F", "witnesses": ["example.com/p.TestF"], "requirements": ["REQ-a", "REQ-b"]},
		{"symbol": "example.com/p.G"}
	]}`
	targets, err := ParseStipulatorTargets([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 {
		t.Fatalf("targets = %+v", targets)
	}
	if targets[0].Oracle[0] != "example.com/p.TestF" || targets[0].Labels[1] != "REQ-b" {
		t.Fatalf("mapping wrong: %+v", targets[0])
	}
	if len(targets[1].Oracle) != 0 {
		t.Fatalf("witness-less entry grew an oracle: %+v", targets[1])
	}
	// The export is a complete statement: a witness-less entry is an
	// explicitly empty oracle, never the package-test default.
	if !targets[0].OracleExplicit || !targets[1].OracleExplicit {
		t.Fatalf("export oracles not explicit: %+v", targets)
	}
	if _, err := ParseStipulatorTargets([]byte(`{"stipulatorTargets": 2, "targets": []}`)); err == nil {
		t.Fatal("unknown export version accepted")
	}
	if _, err := ParseStipulatorTargets([]byte(`{"stipulatorTargets": 1, "targets": [{"witnesses": ["x"]}]}`)); err == nil {
		t.Fatal("symbol-less entry accepted")
	}
}

// TestFresh pins the pin-check query (REQ-result-stale as a question): a
// finding measured now is fresh for the same request, stale for a wider
// budget, stale when its body pin lies, and the check never runs a mutant.
func TestFresh(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	tg := Target{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestAdd"}}
	fs, err := tr.Run(context.Background(), []Target{tg}, Options{Budget: 1})
	if err != nil {
		t.Fatal(err)
	}
	f := fs[0]
	if ok, err := tr.Fresh(f, tg, 1); err != nil || !ok {
		t.Fatalf("just-measured finding not fresh: %v %v", ok, err)
	}
	if ok, err := tr.Fresh(f, tg, 0); err != nil || ok {
		t.Fatalf("capped finding fresh for an exhaustive request: %v %v", ok, err)
	}
	stale := f
	stale.BodyHash = "moved"
	if ok, err := tr.Fresh(stale, tg, 1); err != nil || ok {
		t.Fatalf("moved body pin read fresh: %v %v", ok, err)
	}
	other := Target{Symbol: "example.com/fixture/lib.Weak"}
	if _, err := tr.Fresh(f, other, 1); err == nil || !strings.Contains(err.Error(), "checked against") {
		t.Fatalf("cross-symbol check accepted: %v", err)
	}
}
