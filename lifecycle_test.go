package gomutant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gomutant/internal/engine"
)

// lifecycleModule builds a tree with one declared target and one test,
// and a store seeded with the caller's findings.
func lifecycleModule(t *testing.T, seed ...Finding) (*Tree, *Store) {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":    "module example.com/life\n\ngo 1.26.4\n",
		"p.go":      "package life\n\nfunc F() int { return 1 }\n",
		"p_test.go": "package life\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) { if F() != 1 { t.Fatal() } }\n",
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
	store, err := OpenStore(filepath.Join(dir, ".gomutant", "findings.json"), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(seed) > 0 {
		if err := store.Update(context.Background(), func([]Finding) ([]Finding, error) {
			return seed, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	return tree, store
}

func lifecycleFinding(symbol string) Finding {
	evidence := func(name string) SubjectEvidence {
		return SubjectEvidence{Symbol: name, MaximalClosure: "closure", TestVariantClosure: "tv", Toolchain: "go", BuildConfig: "build",
			ObservationAssertion: "caller assertion", ObservationStrategy: "proof/v1",
			ObservationSubjectPackage: "example.com/life", ObservationSubjectSymbol: strings.TrimPrefix(name, "example.com/life."),
			ObservationObservable: true, ObservationEvidence: "proof",
			RuntimeInputs: "manifest", RuntimeDigest: "digest"}
	}
	return Finding{Symbol: symbol, BodyHash: "body", OperatorSet: engine.OperatorSet, OracleTimeout: "1m0s", Dirty: true,
		CandidateCount: 1, Generated: 1, Mutants: 1,
		TargetEvidence: evidence(symbol),
		OracleEvidence: []SubjectEvidence{evidence("example.com/life.TestF")},
		Operators:      []OperatorSummary{{Operator: "zero return", Generated: 1, Survived: 1}},
		Survivors:      []Survivor{{Position: "p.go:1:1", Operator: "zero return", Site: "cafe0123cafe0123"}},
		Attested:       []Attestation{{Position: "p.go:1:1", Operator: "zero return", Reason: "equivalent by inspection", Site: "cafe0123cafe0123"}}}
}

// Prune removes exactly the records whose symbol no declaration
// resolves, echoes their dispositions, and previews under check without
// touching the document (REQ-result-lifecycle).
func TestPruneRemovesOnlyResolvedDeadRecords(t *testing.T) {
	live := lifecycleFinding("example.com/life.F")
	dead := lifecycleFinding("example.com/life.Gone")
	dead.TargetEvidence.Symbol = dead.Symbol
	tree, store := lifecycleModule(t, live, dead)
	ctx := context.Background()

	preview, err := tree.PruneDetachedContext(ctx, store, true)
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Check || len(preview.Removed) != 1 || preview.Removed[0].Symbol != "example.com/life.Gone" || preview.Kept != 1 {
		t.Fatalf("preview = %+v", preview)
	}
	if all, err := store.Load(ctx); err != nil || len(all) != 2 {
		t.Fatalf("check touched the document: %d records, %v", len(all), err)
	}

	result, err := tree.PruneDetachedContext(ctx, store, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Removed) != 1 || result.Removed[0].Symbol != "example.com/life.Gone" || result.Kept != 1 {
		t.Fatalf("prune = %+v", result)
	}
	// The removal echo carries the dispositions - promote-then-delete,
	// never a silent drop.
	if len(result.Removed[0].Attested) != 1 || result.Removed[0].Attested[0].Reason != "equivalent by inspection" {
		t.Fatalf("removal echo lost the disposition reasoning: %+v", result.Removed[0])
	}
	after, err := store.Load(ctx)
	if err != nil || len(after) != 1 || after[0].Symbol != "example.com/life.F" {
		t.Fatalf("document after prune = %+v, %v", after, err)
	}
}

// Retarget rewrites every symbol-bearing field under the prefix pair
// while attestations ride by their own anchors; a rewrite must resolve,
// a collision refuses whole, and check previews (REQ-result-lifecycle).
func TestRetargetRewritesSymbolIdentityAndDispositionsRide(t *testing.T) {
	renamed := lifecycleFinding("example.com/old.F")
	renamed.OracleEvidence[0].Symbol = "example.com/old.TestF"
	renamed.OracleEvidence[0].ObservationSubjectPackage = "example.com/old"
	renamed.TargetEvidence.ObservationSubjectPackage = "example.com/old"
	renamed.Killed, renamed.Mutants, renamed.CandidateCount, renamed.Generated = 1, 2, 2, 2
	renamed.Kills = []Kill{{Position: "p.go:2:2", Operator: "statement: delete", Killer: "example.com/old.TestF"}}
	renamed.Operators = []OperatorSummary{{Operator: "statement: delete", Generated: 1, Killed: 1}, {Operator: "zero return", Generated: 1, Survived: 1}}
	tree, store := lifecycleModule(t, renamed)
	ctx := context.Background()

	preview, err := tree.RetargetContext(ctx, store, "example.com/old.", "example.com/life.", true)
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Check || len(preview.Rewritten) != 1 || preview.Rewritten[0].To != "example.com/life.F" {
		t.Fatalf("preview = %+v", preview)
	}
	if all, err := store.Load(ctx); err != nil || all[0].Symbol != "example.com/old.F" {
		t.Fatalf("check touched the document: %+v, %v", all, err)
	}

	result, err := tree.RetargetContext(ctx, store, "example.com/old.", "example.com/life.", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rewritten) != 1 || result.Rewritten[0].From != "example.com/old.F" || result.Rewritten[0].To != "example.com/life.F" {
		t.Fatalf("retarget = %+v", result)
	}
	after, err := store.Load(ctx)
	if err != nil || len(after) != 1 {
		t.Fatal(err)
	}
	got := after[0]
	if got.Symbol != "example.com/life.F" || got.TargetEvidence.Symbol != "example.com/life.F" ||
		got.OracleEvidence[0].Symbol != "example.com/life.TestF" ||
		got.Kills[0].Killer != "example.com/life.TestF" {
		t.Fatalf("symbol-bearing fields not rewritten: %+v", got)
	}
	// The observation-subject identity stores the package path and the
	// local name separately; the package half rewrites under the pair's
	// package projection (REQ-result-lifecycle).
	if got.TargetEvidence.ObservationSubjectPackage != "example.com/life" || got.OracleEvidence[0].ObservationSubjectPackage != "example.com/life" {
		t.Fatalf("observation-subject package not rewritten: %q/%q", got.TargetEvidence.ObservationSubjectPackage, got.OracleEvidence[0].ObservationSubjectPackage)
	}
	if len(got.Attested) != 1 || got.Attested[0].Position != "p.go:1:1" || got.Attested[0].Reason != "equivalent by inspection" {
		t.Fatalf("disposition did not ride the retarget: %+v", got.Attested)
	}

	// A record whose own symbol is outside the rename but whose killer
	// carries the prefix updates without owing resolution and reports
	// as touched, not rewritten.
	killerOnly := lifecycleFinding("example.com/life.F")
	killerOnly.Killed, killerOnly.Mutants, killerOnly.CandidateCount, killerOnly.Generated = 1, 2, 2, 2
	killerOnly.Kills = []Kill{{Position: "p.go:2:2", Operator: "statement: delete", Killer: "example.com/gone.TestHelper"}}
	killerOnly.Operators = []OperatorSummary{{Operator: "statement: delete", Generated: 1, Killed: 1}, {Operator: "zero return", Generated: 1, Survived: 1}}
	treeK, storeK := lifecycleModule(t, killerOnly)
	touched, err := treeK.RetargetContext(ctx, storeK, "example.com/gone.", "example.com/moved.", false)
	if err != nil {
		t.Fatalf("killer-only rename refused: %v", err)
	}
	if len(touched.Rewritten) != 0 || touched.Touched != 1 {
		t.Fatalf("killer-only rename = %+v, want touched only", touched)
	}
	// The touched surface owes no resolution, so its rewrites echo row
	// by row - the one audit point for that path.
	if len(touched.TouchedRewrites) != 1 || touched.TouchedRewrites[0] != (TouchedRewrite{Record: "example.com/life.F", From: "example.com/gone.TestHelper", To: "example.com/moved.TestHelper"}) {
		t.Fatalf("touched rewrites not echoed: %+v", touched.TouchedRewrites)
	}
	if allK, err := storeK.Load(ctx); err != nil || allK[0].Kills[0].Killer != "example.com/moved.TestHelper" {
		t.Fatalf("killer identity not rewritten: %+v, %v", allK, err)
	}

	// A rename prefix applies only at a segment boundary: a killer in
	// example.com/goneril is outside a rename of example.com/gone even
	// though the strings share a prefix.
	midSeg := lifecycleFinding("example.com/life.F")
	midSeg.Killed, midSeg.Mutants, midSeg.CandidateCount, midSeg.Generated = 1, 2, 2, 2
	midSeg.Kills = []Kill{{Position: "p.go:2:2", Operator: "statement: delete", Killer: "example.com/goneril.TestHelper"}}
	midSeg.Operators = []OperatorSummary{{Operator: "statement: delete", Generated: 1, Killed: 1}, {Operator: "zero return", Generated: 1, Survived: 1}}
	treeM, storeM := lifecycleModule(t, midSeg)
	boundary, err := treeM.RetargetContext(ctx, storeM, "example.com/gone", "example.com/moved", false)
	if err != nil {
		t.Fatalf("boundary-adjacent rename refused: %v", err)
	}
	if len(boundary.Rewritten) != 0 || boundary.Touched != 0 {
		t.Fatalf("mid-segment prefix rewrote outside the boundary: %+v", boundary)
	}
	if allM, err := storeM.Load(ctx); err != nil || allM[0].Kills[0].Killer != "example.com/goneril.TestHelper" {
		t.Fatalf("mid-segment killer corrupted: %+v, %v", allM, err)
	}

	// An oracle in a subpackage of the renamed path updates its
	// observation package under the path projection's subpath arm.
	subOracle := lifecycleFinding("example.com/life.F")
	subOracle.OracleEvidence[0].Symbol = "example.com/gone/sub.TestHelper"
	subOracle.OracleEvidence[0].ObservationSubjectPackage = "example.com/gone/sub"
	subOracle.OracleEvidence[0].ObservationSubjectSymbol = "TestHelper"
	treeS, storeS := lifecycleModule(t, subOracle)
	subResult, err := treeS.RetargetContext(ctx, storeS, "example.com/gone", "example.com/moved", false)
	if err != nil {
		t.Fatalf("subpackage oracle rename refused: %v", err)
	}
	if len(subResult.Rewritten) != 0 || subResult.Touched != 1 {
		t.Fatalf("subpackage oracle rename = %+v, want touched only", subResult)
	}
	allS, err := storeS.Load(ctx)
	if err != nil || allS[0].OracleEvidence[0].Symbol != "example.com/moved/sub.TestHelper" || allS[0].OracleEvidence[0].ObservationSubjectPackage != "example.com/moved/sub" {
		t.Fatalf("subpackage observation identity not rewritten: %+v, %v", allS[0].OracleEvidence[0], err)
	}

	// A full-symbol rename rewrites the package-local observation half
	// under the pair's local projection.
	goneLocal := lifecycleFinding("example.com/life.Gone")
	treeG, storeG := lifecycleModule(t, goneLocal)
	if _, err := treeG.RetargetContext(ctx, storeG, "example.com/life.Gone", "example.com/life.F", false); err != nil {
		t.Fatalf("full-symbol rename refused: %v", err)
	}
	allG, err := storeG.Load(ctx)
	if err != nil || allG[0].TargetEvidence.ObservationSubjectSymbol != "F" {
		t.Fatalf("local observation half not rewritten: %+v, %v", allG[0].TargetEvidence, err)
	}

	// A probe-confirmed package-failure kill attribution names a
	// package; under a package-shaped pair its embedded path rewrites
	// with the same projection.
	sentinel := lifecycleFinding("example.com/life.F")
	sentinel.Killed, sentinel.Mutants, sentinel.CandidateCount, sentinel.Generated = 1, 2, 2, 2
	sentinel.Kills = []Kill{{Position: "p.go:2:2", Operator: "statement: delete", Killer: engine.PackageKillerPrefix + "example.com/gone)"}}
	sentinel.Operators = []OperatorSummary{{Operator: "statement: delete", Generated: 1, Killed: 1}, {Operator: "zero return", Generated: 1, Survived: 1}}
	treeP, storeP := lifecycleModule(t, sentinel)
	sentinelResult, err := treeP.RetargetContext(ctx, storeP, "example.com/gone.", "example.com/moved.", false)
	if err != nil {
		t.Fatalf("sentinel-killer rename refused: %v", err)
	}
	if len(sentinelResult.Rewritten) != 0 || sentinelResult.Touched != 1 || len(sentinelResult.TouchedRewrites) != 1 {
		t.Fatalf("sentinel-killer rename = %+v, want touched only with the move echoed", sentinelResult)
	}
	if allP, err := storeP.Load(ctx); err != nil || allP[0].Kills[0].Killer != engine.PackageKillerPrefix+"example.com/moved)" {
		t.Fatalf("package-failure attribution kept the dead path: %+v, %v", allP, err)
	}

	// A structurally asymmetric pair would mangle the observation
	// halves into an identity naming nothing - refused whole.
	if _, err := treeK.RetargetContext(ctx, storeK, "example.com/x.C", "example.com/x", false); err == nil || !strings.Contains(err.Error(), "alike") {
		t.Fatalf("asymmetric pair accepted: %v", err)
	}

	// A '.' at a bare prefix's match edge is ambiguous between the
	// package's local half and a dotted sibling package - refused
	// whole, the document untouched.
	if _, err := treeK.RetargetContext(ctx, storeK, "example.com/life", "example.com/moved", false); err == nil || !strings.Contains(err.Error(), "'.' edge") {
		t.Fatalf("ambiguous dot-edge match accepted: %v", err)
	}
	if allK2, err := storeK.Load(ctx); err != nil || allK2[0].Symbol != "example.com/life.F" {
		t.Fatalf("refused retarget touched the document: %+v, %v", allK2, err)
	}

	// An unlike-terminated pair splices across unlike edges - the
	// matched separator is consumed and never re-emitted - refused.
	if _, err := treeK.RetargetContext(ctx, storeK, "example.com/gone.", "example.com/moved", false); err == nil || !strings.Contains(err.Error(), "like-terminated") {
		t.Fatalf("unlike-terminated pair accepted: %v", err)
	}

	// A separator-terminated prefix is an explicit boundary claim, but
	// the evidence's recorded subject package is authoritative: a match
	// crossing into a dotted sibling package refuses whole.
	sibling := lifecycleFinding("example.com/life.F")
	sibling.OracleEvidence[0].Symbol = "example.com/old.v2.TestX"
	sibling.OracleEvidence[0].ObservationSubjectPackage = "example.com/old.v2"
	sibling.OracleEvidence[0].ObservationSubjectSymbol = "TestX"
	treeV, storeV := lifecycleModule(t, sibling)
	if _, err := treeV.RetargetContext(ctx, storeV, "example.com/old.", "example.com/new.", false); err == nil || !strings.Contains(err.Error(), "does not name") {
		t.Fatalf("dotted-sibling evidence match accepted: %v", err)
	}
	if allV, err := storeV.Load(ctx); err != nil || allV[0].OracleEvidence[0].Symbol != "example.com/old.v2.TestX" {
		t.Fatalf("refused retarget touched the document: %+v, %v", allV, err)
	}

	// Where the stored fact CONFIRMS the claim, a dotted final segment
	// retargets by its true boundary: an in-package rename derives the
	// local half from the stored package, a dot-terminated claim
	// covering it exactly renames the package, and a move out of the
	// recorded package refuses.
	dotted := lifecycleFinding("example.com/life.F")
	dotted.OracleEvidence[0].Symbol = "gopkg.in/mylib.v2.TestOld"
	dotted.OracleEvidence[0].ObservationSubjectPackage = "gopkg.in/mylib.v2"
	dotted.OracleEvidence[0].ObservationSubjectSymbol = "TestOld"
	treeD, storeD := lifecycleModule(t, dotted)
	dottedResult, err := treeD.RetargetContext(ctx, storeD, "gopkg.in/mylib.v2.TestOld", "gopkg.in/mylib.v2.TestNew", false)
	if err != nil {
		t.Fatalf("dotted in-package rename refused: %v", err)
	}
	if dottedResult.Touched != 1 {
		t.Fatalf("dotted in-package rename = %+v, want touched", dottedResult)
	}
	allD, err := storeD.Load(ctx)
	if err != nil || allD[0].OracleEvidence[0].Symbol != "gopkg.in/mylib.v2.TestNew" ||
		allD[0].OracleEvidence[0].ObservationSubjectPackage != "gopkg.in/mylib.v2" ||
		allD[0].OracleEvidence[0].ObservationSubjectSymbol != "TestNew" {
		t.Fatalf("dotted in-package rename mangled the identity: %+v, %v", allD[0].OracleEvidence[0], err)
	}
	if _, err := treeD.RetargetContext(ctx, storeD, "gopkg.in/mylib.v2.", "gopkg.in/renamed.v2.", false); err != nil {
		t.Fatalf("dotted package rename refused: %v", err)
	}
	if allD, err := storeD.Load(ctx); err != nil || allD[0].OracleEvidence[0].Symbol != "gopkg.in/renamed.v2.TestNew" ||
		allD[0].OracleEvidence[0].ObservationSubjectPackage != "gopkg.in/renamed.v2" ||
		allD[0].OracleEvidence[0].ObservationSubjectSymbol != "TestNew" {
		t.Fatalf("dotted package rename mangled the identity: %+v, %v", allD[0].OracleEvidence[0], err)
	}
	// A symbol pair whose destination names a different package
	// projection refuses at entry; one that stays in the projection but
	// restructures into a sibling of the recorded dotted package
	// refuses on the stored fact ("cannot carry").
	if _, err := treeD.RetargetContext(ctx, storeD, "gopkg.in/renamed.v2.TestNew", "gopkg.in/other.TestNew", false); err == nil || !strings.Contains(err.Error(), "different packages") {
		t.Fatalf("cross-package symbol pair accepted: %v", err)
	}
	if _, err := treeD.RetargetContext(ctx, storeD, "gopkg.in/renamed.v2.TestNew", "gopkg.in/renamed.x2.TestNew", false); err == nil || !strings.Contains(err.Error(), "cannot carry") {
		t.Fatalf("dotted-sibling restructure accepted: %v", err)
	}

	// A destination that restructures the local name's segments is
	// lexically ambiguous with a package move - refused.
	if _, err := treeD.RetargetContext(ctx, storeD, "example.com/life.F", "example.com/life.v2.F", false); err == nil || !strings.Contains(err.Error(), "restructures") {
		t.Fatalf("segment-restructuring destination accepted: %v", err)
	}

	// A package rename INTO a dotted name: the trailing "." claims the
	// whole prefix as the package, so the destination projects by its
	// true boundary.
	intoDotted := lifecycleFinding("example.com/life.F")
	intoDotted.OracleEvidence[0].Symbol = "gopkg.in/mylib.TestZ"
	intoDotted.OracleEvidence[0].ObservationSubjectPackage = "gopkg.in/mylib"
	intoDotted.OracleEvidence[0].ObservationSubjectSymbol = "TestZ"
	treeI, storeI := lifecycleModule(t, intoDotted)
	intoResult, err := treeI.RetargetContext(ctx, storeI, "gopkg.in/mylib.", "gopkg.in/mylib.v2.", false)
	if err != nil {
		t.Fatalf("rename into a dotted package name refused: %v", err)
	}
	if intoResult.Touched != 1 {
		t.Fatalf("rename into a dotted package name = %+v, want touched", intoResult)
	}
	if allI, err := storeI.Load(ctx); err != nil || allI[0].OracleEvidence[0].Symbol != "gopkg.in/mylib.v2.TestZ" ||
		allI[0].OracleEvidence[0].ObservationSubjectPackage != "gopkg.in/mylib.v2" ||
		allI[0].OracleEvidence[0].ObservationSubjectSymbol != "TestZ" {
		t.Fatalf("rename into a dotted package name mangled the identity: %+v, %v", allI[0].OracleEvidence[0], err)
	}

	// Identical prefixes refuse: a no-op rewrite would report rewrites
	// that never happened.
	if _, err := treeK.RetargetContext(ctx, storeK, "example.com/life.", "example.com/life.", false); err == nil || !strings.Contains(err.Error(), "distinct") {
		t.Fatalf("identity retarget accepted: %v", err)
	}

	// A rewrite whose target does not resolve refuses - a retarget
	// follows a rename that happened.
	if _, err := tree.RetargetContext(ctx, store, "example.com/life.F", "example.com/life.Missing", false); err == nil || !strings.Contains(err.Error(), "does not resolve") {
		t.Fatalf("unresolved rewrite accepted: %v", err)
	}
	// A collision with an existing record refuses whole.
	collide := lifecycleFinding("example.com/life.F")
	other := lifecycleFinding("example.com/other.F")
	other.TargetEvidence.ObservationSubjectPackage = "example.com/other"
	tree2, store2 := lifecycleModule(t, collide, other)
	if _, err := tree2.RetargetContext(ctx, store2, "example.com/other.", "example.com/life.", false); err == nil || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("collision accepted: %v", err)
	}
	if all, err := store2.Load(ctx); err != nil || len(all) != 2 || all[0].Symbol == all[1].Symbol {
		t.Fatalf("refused retarget touched the document: %+v, %v", all, err)
	}
}

// A package with load errors "loads" with declarations silently missing
// from its partial syntax - indistinguishable from a rename at the
// symbol layer - so prune refuses instead of destroying live records
// (REQ-result-lifecycle).
func TestPruneRefusesAnUnhealthyLoad(t *testing.T) {
	live := lifecycleFinding("example.com/life.F")
	tree, store := lifecycleModule(t, live)
	ctx := context.Background()
	// Break a declaration syntactically in a second file: the package
	// still loads, carrying errors, with F potentially missing from the
	// partial syntax.
	if err := os.WriteFile(filepath.Join(tree.dir, "broken.go"), []byte("package life\n\nfunc Broken(\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	brokenTree, err := Load(tree.dir)
	if err != nil {
		// A load that fails outright also protects the records.
		return
	}
	if _, err := brokenTree.PruneDetachedContext(ctx, store, false); err == nil || !strings.Contains(err.Error(), "did not load cleanly") {
		t.Fatalf("prune under an unhealthy load = %v, want the refusal", err)
	}
	if all, err := store.Load(ctx); err != nil || len(all) != 1 {
		t.Fatalf("refused prune touched the document: %+v, %v", all, err)
	}
}

// The prefix-pair projections: package-shaped prefixes project to a
// package path with no local half - a trailing separator marks the
// boundary without joining the path - and a full-symbol prefix splits
// at the package-to-local dot (REQ-result-lifecycle).
func TestRetargetPrefixParts(t *testing.T) {
	for _, tc := range []struct{ prefix, pkg, local string }{
		{"example.com/old", "example.com/old", ""},
		{"example.com/old/", "example.com/old", ""},
		{"example.com/old.", "example.com/old", ""},
		{"example.com/x.Old", "example.com/x", "Old"},
		{"example.com/x.T.Method", "example.com/x", "T.Method"},
		// A trailing "." marks the whole prefix as a package claim, so
		// a dotted final segment projects by its true boundary.
		{"gopkg.in/mylib.v2.", "gopkg.in/mylib.v2", ""},
	} {
		pkg, local := retargetPrefixParts(tc.prefix)
		if pkg != tc.pkg || local != tc.local {
			t.Fatalf("retargetPrefixParts(%q) = %q, %q; want %q, %q", tc.prefix, pkg, local, tc.pkg, tc.local)
		}
	}
}
