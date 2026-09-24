package gomutant

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/greatliontech/gomutant/internal/engine"
)

// A changed test file's residue row names what the change closed over:
// prior findings outside the target set that are stale for an
// oracle-caused reason, counted, with the re-measure suggestion -
// changed-scope discovery alone would never re-measure them
// (REQ-target-changed).
func TestOracleClosureSignpostNamesStaleFindingsBeyondTheTargetSet(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":      "module example.com/closure\n\ngo 1.26.4\n",
		"p.go":        "package closure\n\nfunc F() int { return 1 }\n\nfunc G() int { return 2 }\n",
		"p_test.go":   "package closure\n\nimport (\n\t\"testing\"\n\n\t\"example.com/closure/s\"\n)\n\nfunc TestF(t *testing.T) { if F() != 1 { t.Fatal() } }\n\nfunc TestG(t *testing.T) { if G() != 2 || s.S() != 4 { t.Fatal() } }\n",
		"q/q.go":      "package q\n\nfunc H() int { return 3 }\n\nfunc H2() int { return 6 }\n",
		"q/q_test.go": "package q\n\nimport \"testing\"\n\nfunc TestH(t *testing.T) { if H() != 3 { t.Fatal() } }\n",
		"s/s.go":      "package s\n\nfunc S() int { return 4 }\n",
		"s/s_test.go": "package s\n\nimport \"testing\"\n\nfunc TestS(t *testing.T) { if S() != 4 { t.Fatal() } }\n",
		// A package whose last path element carries a dot: the string
		// cut that guesses a package at the first dot names it short.
		"nats.go/n.go":      "package natsgo\n\nfunc N() int { return 5 }\n",
		"nats.go/n_test.go": "package natsgo\n\nimport \"testing\"\n\nfunc TestN(t *testing.T) { if N() != 5 { t.Fatal() } }\n",
	}
	for name, content := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
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
	// Best-effort: a record whose judgment errors (an unparseable
	// oracle timeout) is skipped; the rest still count.
	broken := Finding{Symbol: "example.com/closure.G", OperatorSet: engine.OperatorSet, OracleTimeout: "bogus",
		OracleEvidence: []SubjectEvidence{{Symbol: "example.com/closure.TestGone"}}}
	// A record of another package, stale for an oracle cause too: the
	// changed test file's package does not reach it — its recorded
	// oracle names no test there and the root's test binary does not
	// link q — so it is read and never judged, never counted.
	unreached := Finding{Symbol: "example.com/closure/q.Unreached", OperatorSet: engine.OperatorSet, OracleTimeout: "1m0s",
		OracleEvidence: []SubjectEvidence{{Symbol: "example.com/closure/q.TestGone"}}}
	// A record of s, whose package the root's test binary links (the
	// root's tests import s): a test written in the root would join its
	// derived oracle, so the changed root test file reaches it — stale
	// for an oracle cause, counted.
	linked := Finding{Symbol: "example.com/closure/s.S", OperatorSet: engine.OperatorSet, OracleTimeout: "1m0s",
		OracleEvidence: []SubjectEvidence{{Symbol: "example.com/closure/s.TestGone"}}}
	// The same under an explicit oracle: the caller chose the tests, so
	// a test written in the root joins nothing; unreached.
	chosen := Finding{Symbol: "example.com/closure/s.S", OperatorSet: engine.OperatorSet, OracleTimeout: "1m0s", OracleExplicit: true,
		OracleEvidence: []SubjectEvidence{{Symbol: "example.com/closure/s.TestGone"}}}
	// A record of q whose recorded oracle names a root test: the root's
	// test binary does not link q, but the record's own evidence names
	// the changed package's test, so the change reaches it by the
	// record alone — stale, since q's derived oracle is q's tests.
	crossReached := Finding{Symbol: "example.com/closure/q.H", OperatorSet: engine.OperatorSet, OracleTimeout: "1m0s",
		OracleEvidence: []SubjectEvidence{{Symbol: "example.com/closure.TestF"}}}
	// The dotted package's changed test reaches a record of q by the
	// record's oracle naming its test, and a record of its own — whose
	// recorded oracle names only an unchanged package's test — by its
	// test binary linking its own package: both exact only where the
	// cut honors the dotted last element.
	dottedByRecord := Finding{Symbol: "example.com/closure/q.H2", OperatorSet: engine.OperatorSet, OracleTimeout: "1m0s",
		OracleEvidence: []SubjectEvidence{{Symbol: "example.com/closure/nats.go.TestN"}}}
	dottedLinked := Finding{Symbol: "example.com/closure/nats.go.N", OperatorSet: engine.OperatorSet, OracleTimeout: "1m0s",
		OracleEvidence: []SubjectEvidence{{Symbol: "example.com/closure/q.TestH"}}}
	prior := []Finding{broken, staleByOracle("example.com/closure.F"), otherStale, unreached, linked, crossReached, dottedByRecord, dottedLinked}
	residue := []Residue{
		{Path: "p_test.go", Reason: testFileResidueReason, Package: "example.com/closure"},
		{Path: "gen.go", Reason: "generated file"},
		{Path: "nats.go/n_test.go", Reason: testFileResidueReason, Package: "example.com/closure/nats.go"},
	}
	// G is stale for a non-oracle reason and q.Unreached is out of
	// reach; the five reached, oracle-stale records count, and the pass
	// is priced as the records and subjects the changed tests reach.
	var targets []Target
	var stages []string
	got, err := tree.OracleClosureSignpostContext(context.Background(), residue, prior, targets, func(s string) { stages = append(stages, s) })
	if err != nil {
		t.Fatal(err)
	}
	want := testFileResidueReason + "; oracle closure of 5 stale finding(s) - re-measure by symbol: 5 symbols: example.com/closure.F, example.com/closure/nats.go.N, example.com/closure/q.H, ... (+2 more)"
	if got[0].Reason != want || got[2].Reason != want {
		t.Fatalf("test-file rows = %q, %q, want %q", got[0].Reason, got[2].Reason, want)
	}
	// The row's name cap hides two of the five: the names beyond it are
	// pinned by rows over subsets small enough to render whole.
	for _, subset := range []struct {
		prior []Finding
		names string
	}{
		{[]Finding{crossReached, dottedByRecord, dottedLinked}, "example.com/closure/nats.go.N, example.com/closure/q.H, example.com/closure/q.H2"},
		{[]Finding{linked, staleByOracle("example.com/closure.F")}, "example.com/closure.F, example.com/closure/s.S"},
	} {
		rows, err := tree.OracleClosureSignpostContext(context.Background(), residue, subset.prior, targets, nil)
		if err != nil {
			t.Fatal(err)
		}
		if want := fmt.Sprintf("%s; oracle closure of %d stale finding(s) - re-measure by symbol: %s", testFileResidueReason, len(subset.prior), subset.names); rows[0].Reason != want {
			t.Fatalf("subset row = %q, want %q", rows[0].Reason, want)
		}
	}
	wantStages := []string{
		"closure signpost listing the test closure of 2 changed package(s)",
		"closure signpost over 7 prior record(s) the changed tests reach (6 subject(s) in 2 package(s))",
	}
	if !slices.Equal(stages, wantStages) {
		t.Fatalf("stages = %q, want the priced listings and the priced pass over the seven reached records", stages)
	}
	// The explicit-oracle record in s is not reached by the root's
	// change: the listings run, the record is judged never, counted
	// never.
	stages = nil
	chosenOnly, err := tree.OracleClosureSignpostContext(context.Background(), residue, []Finding{chosen}, targets, func(s string) { stages = append(stages, s) })
	if err != nil || chosenOnly[0].Reason != testFileResidueReason || len(stages) != 1 {
		t.Fatalf("explicit-oracle record = %+v, stages %q, %v; want unreached, no pass", chosenOnly, stages, err)
	}
	// A test file with no package (outside every main module) reaches
	// nothing: no pass, the rows pass through untouched.
	stages = nil
	unbound := []Residue{{Path: "../elsewhere_test.go", Reason: testFileResidueReason}}
	if plain, err := tree.OracleClosureSignpostContext(context.Background(), unbound, prior, targets, func(s string) { stages = append(stages, s) }); err != nil || plain[0].Reason != testFileResidueReason || len(stages) != 0 {
		t.Fatalf("unbound test file = %+v, stages %q, %v", plain, stages, err)
	}
	if got[1].Reason != "generated file" {
		t.Fatalf("non-test row disturbed: %q", got[1].Reason)
	}

	// No test-file row: the rows pass through untouched even with
	// qualifying findings.
	untouched, err := tree.OracleClosureSignpostContext(context.Background(), residue[1:2], prior, nil, nil)
	if err != nil || untouched[0].Reason != "generated file" {
		t.Fatalf("no-test-row pass-through = %+v, %v", untouched, err)
	}

	// Nothing qualifying: every prior finding is targeted - the run
	// re-measures them itself.
	all := []Target{{Symbol: "example.com/closure.F"}, {Symbol: "example.com/closure.G"}, {Symbol: "example.com/closure/s.S"}, {Symbol: "example.com/closure/q.H"}, {Symbol: "example.com/closure/q.H2"}, {Symbol: "example.com/closure/nats.go.N"}}
	plain, err := tree.OracleClosureSignpostContext(context.Background(), residue, prior, all, nil)
	if err != nil || plain[0].Reason != testFileResidueReason {
		t.Fatalf("fully-targeted pass-through = %+v, %v", plain, err)
	}
}
