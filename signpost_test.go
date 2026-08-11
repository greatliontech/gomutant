package gomutant

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/greatliontech/gomutant/internal/engine"
)

// A changed test file's residue row names what the change closed over:
// prior findings outside the target set that are stale for an
// oracle-caused reason, counted, with the re-measure suggestion -
// changed-scope discovery alone would never re-measure them
// (REQ-target-changed).
func TestOracleClosureSignpostNamesStaleFindingsBeyondTheTargetSet(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":    "module example.com/closure\n\ngo 1.26.4\n",
		"p.go":      "package closure\n\nfunc F() int { return 1 }\n\nfunc G() int { return 2 }\n",
		"p_test.go": "package closure\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) { if F() != 1 { t.Fatal() } }\n\nfunc TestG(t *testing.T) { if G() != 2 { t.Fatal() } }\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tree, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	// A recorded derived oracle that departs from the current one is the
	// cheapest oracle-caused staleness: inspection classifies it before
	// touching subject evidence.
	staleByOracle := func(symbol string) Finding {
		return Finding{Symbol: symbol, OperatorSet: engine.OperatorSet, OracleTimeout: "1m0s",
			OracleEvidence: []SubjectEvidence{{Symbol: "example.com/closure.TestGone"}}}
	}
	// A record stale for a non-oracle cause (operator set drift) is not
	// oracle closure and never counts.
	otherStale := Finding{Symbol: "example.com/closure.G", OperatorSet: "old/1", OracleTimeout: "1m0s",
		OracleEvidence: []SubjectEvidence{{Symbol: "example.com/closure.TestG"}}}
	prior := []Finding{staleByOracle("example.com/closure.F"), otherStale}
	residue := []Residue{
		{Path: "p_test.go", Reason: testFileResidueReason},
		{Path: "gen.go", Reason: "generated file"},
	}
	// G is stale for a non-oracle reason: only F counts.
	var targets []Target
	got, err := tree.OracleClosureSignpostContext(context.Background(), residue, prior, targets)
	if err != nil {
		t.Fatal(err)
	}
	want := testFileResidueReason + "; oracle closure of 1 stale finding(s) - re-measure by symbol: example.com/closure.F"
	if got[0].Reason != want {
		t.Fatalf("test-file row = %q, want %q", got[0].Reason, want)
	}
	if got[1].Reason != "generated file" {
		t.Fatalf("non-test row disturbed: %q", got[1].Reason)
	}

	// No test-file row: the rows pass through untouched even with
	// qualifying findings.
	untouched, err := tree.OracleClosureSignpostContext(context.Background(), residue[1:], prior, nil)
	if err != nil || untouched[0].Reason != "generated file" {
		t.Fatalf("no-test-row pass-through = %+v, %v", untouched, err)
	}

	// Nothing qualifying: every prior finding is targeted - the run
	// re-measures them itself.
	all := []Target{{Symbol: "example.com/closure.F"}, {Symbol: "example.com/closure.G"}}
	plain, err := tree.OracleClosureSignpostContext(context.Background(), residue, prior, all)
	if err != nil || plain[0].Reason != testFileResidueReason {
		t.Fatalf("fully-targeted pass-through = %+v, %v", plain, err)
	}
}
