package gomutant

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// The batched judge builds the records' subject views once and judges
// every record against them: no record builds a supplementary view,
// one build per package-process posture, and each record's state and
// reason equal its own per-record judgment (REQ-result-inspection).
func TestInspectFindingsJudgesInOnePass(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
	tr := fixtureTree(t)
	ctx := context.Background()
	records := []Finding{
		{Symbol: "example.com/fixture/lib.Add", OperatorSet: "go/12", OracleTimeout: "1m0s", BodyHash: "stale", OracleExplicit: true,
			TargetEvidence: SubjectEvidence{Symbol: "example.com/fixture/lib.Add", MaximalClosure: "x", TestVariantClosure: "x", Toolchain: "go", BuildConfig: "x", RuntimeInputs: "eyJ2IjoxfQ", RuntimeDigest: "x"},
			OracleEvidence: []SubjectEvidence{{Symbol: "example.com/fixture/lib.TestAdd", MaximalClosure: "x", TestVariantClosure: "x", Toolchain: "go", BuildConfig: "x", RuntimeInputs: "eyJ2IjoxfQ", RuntimeDigest: "x"}}},
		{Symbol: "example.com/fixture/lib.Weak", OperatorSet: "go/12", OracleTimeout: "1m0s", BodyHash: "stale", OracleExplicit: true,
			TargetEvidence: SubjectEvidence{Symbol: "example.com/fixture/lib.Weak", MaximalClosure: "x", TestVariantClosure: "x", Toolchain: "go", BuildConfig: "x", RuntimeInputs: "eyJ2IjoxfQ", RuntimeDigest: "x"},
			OracleEvidence: []SubjectEvidence{{Symbol: "example.com/fixture/lib.TestWeak", MaximalClosure: "x", TestVariantClosure: "x", Toolchain: "go", BuildConfig: "x", RuntimeInputs: "eyJ2IjoxfQ", RuntimeDigest: "x"}}},
		{Symbol: "example.com/fixture/lib.Gone", OperatorSet: "go/12", OracleTimeout: "1m0s", BodyHash: "x", OracleExplicit: true},
	}
	// A record whose oracle lives outside its package is judged under
	// the other package-process posture — a second view set; a shaped
	// record (a manual recipe whose digest still derives) reads its
	// oracle's view from the set too.
	_, digest, _, err := tr.shapedCandidates(ctx, Target{Symbol: "recipe:lib-add", Manual: &ManualSpec{File: "lib/lib.go", Edits: []ManualEdit{{Find: "return a + b", Replace: "return a - b"}}}, OracleExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	records = append(records,
		Finding{Symbol: "example.com/fixture/lib.PickInput", OperatorSet: "go/12", OracleTimeout: "1m0s", BodyHash: "stale", OracleExplicit: true,
			TargetEvidence: SubjectEvidence{Symbol: "example.com/fixture/lib.PickInput", MaximalClosure: "x", TestVariantClosure: "x", Toolchain: "go", BuildConfig: "x", RuntimeInputs: "eyJ2IjoxfQ", RuntimeDigest: "x"},
			OracleEvidence: []SubjectEvidence{{Symbol: "example.com/fixture/counting.TestCounting", MaximalClosure: "x", TestVariantClosure: "x", Toolchain: "go", BuildConfig: "x", RuntimeInputs: "eyJ2IjoxfQ", RuntimeDigest: "x"}}},
		Finding{Symbol: "recipe:lib-add", Shape: &TargetShape{Manual: &ManualSpec{File: "lib/lib.go", Edits: []ManualEdit{{Find: "return a + b", Replace: "return a - b"}}}}, OperatorSet: shapedOperatorSet, OracleTimeout: "1m0s", BodyHash: digest,
			OracleEvidence: []SubjectEvidence{{Symbol: "example.com/fixture/lib.TestAdd", MaximalClosure: "x", TestVariantClosure: "x", Toolchain: "go", BuildConfig: "x", RuntimeInputs: "eyJ2IjoxfQ", RuntimeDigest: "x"}}},
	)
	// A derived-oracle record whose recorded oracle no longer matches
	// the derived one is decided by the delta: without a compartment
	// ledger the enrichment can name nothing, so the pass admits no
	// view for it at all.
	records = append(records, Finding{Symbol: "example.com/fixture/lib.F", OperatorSet: "go/12", OracleTimeout: "1m0s", BodyHash: "x",
		OracleEvidence: []SubjectEvidence{{Symbol: "example.com/fixture/lib.TestGone"}}})
	// With a ledger but no recorded oracle test left in the derived set,
	// the enrichment still names nothing: no view admitted either.
	records = append(records, Finding{Symbol: "example.com/fixture/lib.PanicValue", OperatorSet: "go/12", OracleTimeout: "1m0s", BodyHash: "x", CompartmentLedger: &CompartmentLedger{},
		OracleEvidence: []SubjectEvidence{{Symbol: "example.com/fixture/lib.TestGone"}}})
	var supplementary, builds int
	var built [][]string
	prior, priorBuild := inspectionSupplementaryViewHook, subjectViewBuildHook
	inspectionSupplementaryViewHook = func([]string) { supplementary++ }
	subjectViewBuildHook = func(symbols []string) { builds++; built = append(built, symbols) }
	defer func() { inspectionSupplementaryViewHook, subjectViewBuildHook = prior, priorBuild }()
	var progressed []string
	batched, err := tr.InspectFindingsContext(ctx, records, func(stage string) { progressed = append(progressed, stage) })
	if err != nil {
		t.Fatal(err)
	}
	if supplementary != 0 || builds != 2 {
		t.Fatalf("batched judge built %d view set(s) and %d supplementary; want one set per posture, none supplementary", builds, supplementary)
	}
	// The built sets are exactly the views the records read: the
	// cross-package oracle's record and the shaped record under the
	// other posture (a shaped identity is no package symbol), the
	// detached record and the delta-decided record's oracles nowhere.
	want := [][]string{
		{"example.com/fixture/counting.TestCounting", "example.com/fixture/lib.PickInput", "example.com/fixture/lib.TestAdd"},
		{"example.com/fixture/lib.Add", "example.com/fixture/lib.TestAdd", "example.com/fixture/lib.TestWeak", "example.com/fixture/lib.Weak"},
	}
	if !slices.Equal(built[0], want[0]) || !slices.Equal(built[1], want[1]) {
		t.Fatalf("built view sets = %q, want %q", built, want)
	}
	if len(progressed) != 1+2+len(records) || progressed[0] != "admitting 7 record(s)" || progressed[len(progressed)-1] != "judging example.com/fixture/lib.PanicValue" {
		t.Fatalf("progress = %v", progressed)
	}
	// Records the pre-checks decide without a view build none: a pass of
	// early-decided records builds no view set.
	builds = 0
	if _, err := tr.InspectFindingsContext(ctx, []Finding{records[2], {Symbol: "example.com/fixture/lib.Add", OperatorSet: "old/1", OracleTimeout: "1m0s", BodyHash: "x"}}, nil); err != nil {
		t.Fatal(err)
	}
	if builds != 0 {
		t.Fatalf("a pass of view-free records built %d view set(s)", builds)
	}
	// A cancelled context ends even a view-free pass with its error —
	// before it starts, and when the cancellation lands after the last
	// record's admission (the pass's own ending check).
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := tr.InspectFindingsContext(cancelled, []Finding{records[2]}, nil); err == nil {
		t.Fatal("a view-free pass completed under a cancelled context")
	}
	late, cancelLate := context.WithCancel(ctx)
	defer cancelLate()
	if _, err := tr.InspectFindingsContext(late, []Finding{records[2]}, func(stage string) {
		if strings.HasPrefix(stage, "judging ") {
			cancelLate()
		}
	}); err == nil {
		t.Fatal("a pass cancelled at its last record completed without its error")
	}
	for i, f := range records {
		single, err := tr.inspectFindingStateContext(ctx, f, nil)
		if err != nil {
			t.Fatal(err)
		}
		if batched[i].State != single.State || batched[i].Reason != single.Reason {
			t.Fatalf("%s: batched %s (%s) != per-record %s (%s)", f.Symbol, batched[i].State, batched[i].Reason, single.State, single.Reason)
		}
	}
	if batched[2].State != FindingDetached || batched[0].State != FindingStale || batched[3].State != FindingStale || batched[4].State != FindingStale || batched[5].State != FindingStale || batched[6].State != FindingStale {
		t.Fatalf("states = %v", batched)
	}
}
