package gomutant

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"
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
		if _, err := store.Update(context.Background(), func([]Finding) ([]Finding, error) {
			return seed, nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	return tree, store
}

func lifecycleFinding(symbol string) Finding {
	evidence := func(name string) SubjectEvidence {
		return SubjectEvidence{Symbol: name, Fingerprint: gofresh.Fingerprint{MaximalClosure: "closure", TestVariantClosure: "tv", ObservationAssertion: "caller assertion", RuntimeInputs: "manifest", RuntimeDigest: "digest", Guards: guard.Guards{Toolchain: "go", BuildConfig: "build"}, ObservationProof: gofresh.ObservationProof{Strategy: "proof/v1", Subject: gofresh.Subject{Package: "example.com/life", Symbol: strings.TrimPrefix(name, "example.com/life.")}, Observable: true, Evidence: "proof"}, ResultKind: gofresh.CodeResult}}
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
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
	live := lifecycleFinding("example.com/life.F")
	dead := lifecycleFinding("example.com/life.Gone")
	dead.TargetEvidence.Symbol = dead.Symbol
	tree, store := lifecycleModule(t, live, dead)
	ctx := context.Background()

	preview, err := tree.PruneDetachedContext(ctx, store, true)
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Check || len(preview.Removed) != 1 || preview.Removed[0].Symbol != "example.com/life.Gone" || preview.Kept.Total() != 1 {
		t.Fatalf("preview = %+v", preview)
	}
	if all, err := store.Load(ctx); err != nil || len(all) != 2 {
		t.Fatalf("check touched the document: %d records, %v", len(all), err)
	}

	result, err := tree.PruneDetachedContext(ctx, store, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Removed) != 1 || result.Removed[0].Symbol != "example.com/life.Gone" || result.Kept.Total() != 1 {
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
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
	renamed := lifecycleFinding("example.com/old.F")
	renamed.OracleEvidence[0].Symbol = "example.com/old.TestF"
	renamed.OracleEvidence[0].ObservationProof.Subject.Package = "example.com/old"
	renamed.TargetEvidence.ObservationProof.Subject.Package = "example.com/old"
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
	if got.TargetEvidence.ObservationProof.Subject.Package != "example.com/life" || got.OracleEvidence[0].ObservationProof.Subject.Package != "example.com/life" {
		t.Fatalf("observation-subject package not rewritten: %q/%q", got.TargetEvidence.ObservationProof.Subject.Package, got.OracleEvidence[0].ObservationProof.Subject.Package)
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
	if len(touched.Rewritten) != 0 || touched.Touched.Total() != 1 {
		t.Fatalf("killer-only rename = %+v, want touched only", touched)
	}
	// The touched surface owes no resolution, so its rewrites echo row
	// by row - the one audit point for that path.
	if len(touched.TouchedRewrites) != 1 || touched.TouchedRewrites[0] != (TouchedRewrite{Record: "example.com/life.F", Layer: LayerLocal, From: "example.com/gone.TestHelper", To: "example.com/moved.TestHelper"}) {
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
	if len(boundary.Rewritten) != 0 || boundary.Touched.Total() != 0 {
		t.Fatalf("mid-segment prefix rewrote outside the boundary: %+v", boundary)
	}
	if allM, err := storeM.Load(ctx); err != nil || allM[0].Kills[0].Killer != "example.com/goneril.TestHelper" {
		t.Fatalf("mid-segment killer corrupted: %+v, %v", allM, err)
	}

	// An oracle in a subpackage of the renamed path updates its
	// observation package under the path projection's subpath arm.
	subOracle := lifecycleFinding("example.com/life.F")
	subOracle.OracleEvidence[0].Symbol = "example.com/gone/sub.TestHelper"
	subOracle.OracleEvidence[0].ObservationProof.Subject.Package = "example.com/gone/sub"
	subOracle.OracleEvidence[0].ObservationProof.Subject.Symbol = "TestHelper"
	treeS, storeS := lifecycleModule(t, subOracle)
	subResult, err := treeS.RetargetContext(ctx, storeS, "example.com/gone", "example.com/moved", false)
	if err != nil {
		t.Fatalf("subpackage oracle rename refused: %v", err)
	}
	if len(subResult.Rewritten) != 0 || subResult.Touched.Total() != 1 {
		t.Fatalf("subpackage oracle rename = %+v, want touched only", subResult)
	}
	allS, err := storeS.Load(ctx)
	if err != nil || allS[0].OracleEvidence[0].Symbol != "example.com/moved/sub.TestHelper" || allS[0].OracleEvidence[0].ObservationProof.Subject.Package != "example.com/moved/sub" {
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
	if err != nil || allG[0].TargetEvidence.ObservationProof.Subject.Symbol != "F" {
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
	if len(sentinelResult.Rewritten) != 0 || sentinelResult.Touched.Total() != 1 || len(sentinelResult.TouchedRewrites) != 1 {
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
	sibling.OracleEvidence[0].ObservationProof.Subject.Package = "example.com/old.v2"
	sibling.OracleEvidence[0].ObservationProof.Subject.Symbol = "TestX"
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
	dotted.OracleEvidence[0].ObservationProof.Subject.Package = "gopkg.in/mylib.v2"
	dotted.OracleEvidence[0].ObservationProof.Subject.Symbol = "TestOld"
	treeD, storeD := lifecycleModule(t, dotted)
	dottedResult, err := treeD.RetargetContext(ctx, storeD, "gopkg.in/mylib.v2.TestOld", "gopkg.in/mylib.v2.TestNew", false)
	if err != nil {
		t.Fatalf("dotted in-package rename refused: %v", err)
	}
	if dottedResult.Touched.Total() != 1 {
		t.Fatalf("dotted in-package rename = %+v, want touched", dottedResult)
	}
	allD, err := storeD.Load(ctx)
	if err != nil || allD[0].OracleEvidence[0].Symbol != "gopkg.in/mylib.v2.TestNew" ||
		allD[0].OracleEvidence[0].ObservationProof.Subject.Package != "gopkg.in/mylib.v2" ||
		allD[0].OracleEvidence[0].ObservationProof.Subject.Symbol != "TestNew" {
		t.Fatalf("dotted in-package rename mangled the identity: %+v, %v", allD[0].OracleEvidence[0], err)
	}
	if _, err := treeD.RetargetContext(ctx, storeD, "gopkg.in/mylib.v2.", "gopkg.in/renamed.v2.", false); err != nil {
		t.Fatalf("dotted package rename refused: %v", err)
	}
	if allD, err := storeD.Load(ctx); err != nil || allD[0].OracleEvidence[0].Symbol != "gopkg.in/renamed.v2.TestNew" ||
		allD[0].OracleEvidence[0].ObservationProof.Subject.Package != "gopkg.in/renamed.v2" ||
		allD[0].OracleEvidence[0].ObservationProof.Subject.Symbol != "TestNew" {
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
	intoDotted.OracleEvidence[0].ObservationProof.Subject.Package = "gopkg.in/mylib"
	intoDotted.OracleEvidence[0].ObservationProof.Subject.Symbol = "TestZ"
	treeI, storeI := lifecycleModule(t, intoDotted)
	intoResult, err := treeI.RetargetContext(ctx, storeI, "gopkg.in/mylib.", "gopkg.in/mylib.v2.", false)
	if err != nil {
		t.Fatalf("rename into a dotted package name refused: %v", err)
	}
	if intoResult.Touched.Total() != 1 {
		t.Fatalf("rename into a dotted package name = %+v, want touched", intoResult)
	}
	if allI, err := storeI.Load(ctx); err != nil || allI[0].OracleEvidence[0].Symbol != "gopkg.in/mylib.v2.TestZ" ||
		allI[0].OracleEvidence[0].ObservationProof.Subject.Package != "gopkg.in/mylib.v2" ||
		allI[0].OracleEvidence[0].ObservationProof.Subject.Symbol != "TestZ" {
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
	other.TargetEvidence.ObservationProof.Subject.Package = "example.com/other"
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
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
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

// Prune judges every stored record in its own layer: a detached symbol
// held in both layers leaves the findings document and the overlay
// alike, an overlay entry a hand edit parked under a foreign name is
// removed under that name, each removal names its layer, and a check
// preview writes nothing (REQ-result-lifecycle, REQ-result-layers).
func TestPruneActsOnEveryLayer(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	live := storeFinding("example.com/life.F", nil)
	deadRepo := storeFinding("example.com/life.Gone", nil)
	tree, store := lifecycleModule(t, live, deadRepo)
	ctx := context.Background()
	// A later machine-local measurement of each symbol shadows its
	// committed row: both symbols are now two records.
	deadLocal := storeFinding("example.com/life.Gone", func(f *Finding) { f.Dirty = true; f.BodyHash = "h2" })
	liveLocal := storeFinding("example.com/life.F", func(f *Finding) { f.Dirty = true; f.BodyHash = "h2" })
	// A shaped record held in the overlay alone (kept unconditionally —
	// declaration absence is its normal state): the kept count's two
	// layers differ, so a count landing in the wrong layer is seen.
	overlayOnly := storeFinding("structural:local", func(f *Finding) {
		f.Dirty = true
		f.Shape = &TargetShape{Structural: &StructuralSpec{Class: "import-boundary", Packages: []string{"p"}, Forbidden: "q"}}
		f.TargetEvidence = SubjectEvidence{} // a shaped record carries no target evidence
	})
	if _, err := store.Update(ctx, func([]Finding) ([]Finding, error) {
		return []Finding{deadLocal, liveLocal, overlayOnly}, nil
	}); err != nil {
		t.Fatal(err)
	}
	liveEntryBefore, err := os.Stat(store.entryPath("example.com/life.F"))
	if err != nil {
		t.Fatal(err)
	}
	// A record parked under a foreign name by a hand rewrite of the
	// overlay: served by its content, addressed by its name.
	stray := storeFinding("example.com/life.Stray", func(f *Finding) { f.Dirty = true })
	strayDoc, err := Export([]Finding{stray}, nil)
	if err != nil {
		t.Fatal(err)
	}
	strayPath := filepath.Join(store.overlayDir, "0000feedfacefeedfacefeed.json")
	if err := os.WriteFile(strayPath, strayDoc, 0o644); err != nil {
		t.Fatal(err)
	}
	docBefore, err := os.Stat(store.path)
	if err != nil {
		t.Fatal(err)
	}
	type removal struct{ symbol, layer string }
	removals := func(r PruneResult) []removal {
		var out []removal
		for _, record := range r.Removed {
			out = append(out, removal{record.Symbol, record.Layer})
		}
		return out
	}
	want := []removal{{"example.com/life.Gone", LayerRepo}, {"example.com/life.Gone", LayerLocal}, {"example.com/life.Stray", LayerLocal}}

	preview, err := tree.PruneDetachedContext(ctx, store, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := removals(preview); !reflect.DeepEqual(got, want) || preview.Kept != (LayerCounts{Repo: 1, Local: 2}) {
		t.Fatalf("preview removals = %v (kept %+v), want %v (kept one repo, two local)", got, preview.Kept, want)
	}
	docAfterCheck, err := os.Stat(store.path)
	if err != nil || !os.SameFile(docBefore, docAfterCheck) {
		t.Fatalf("check rewrote the document: %v", err)
	}
	// The preview's read records placement like any read.
	if !store.Overlaid("example.com/life.Gone") || !store.Overlaid("example.com/life.F") {
		t.Fatal("the preview's read did not record the overlay placement")
	}
	for _, path := range []string{strayPath, store.entryPath("example.com/life.Gone")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("check touched the overlay: %s: %v", path, err)
		}
	}

	result, err := tree.PruneDetachedContext(ctx, store, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := removals(result); !reflect.DeepEqual(got, want) || result.Kept != (LayerCounts{Repo: 1, Local: 2}) {
		t.Fatalf("prune removals = %v (kept %+v), want %v (kept one repo, two local)", got, result.Kept, want)
	}
	// The kept overlay record was not rewritten: its entry keeps its
	// identity.
	if liveEntryAfter, err := os.Stat(store.entryPath("example.com/life.F")); err != nil || !os.SameFile(liveEntryBefore, liveEntryAfter) {
		t.Fatalf("the kept overlay entry was rewritten: %v", err)
	}
	for _, path := range []string{strayPath, store.entryPath("example.com/life.Gone")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("pruned overlay entry %s still present: %v", path, err)
		}
	}
	repo, err := store.loadRepo()
	if err != nil || len(repo) != 1 || repo[0].Symbol != "example.com/life.F" {
		t.Fatalf("document after prune = %+v, %v", repo, err)
	}
	// The merged view: the live symbol's overlay record and the shaped
	// overlay record the prune kept.
	if all, err := store.Load(ctx); err != nil || len(all) != 2 || all[0].Symbol != "example.com/life.F" || all[0].BodyHash != "h2" || all[1].Symbol != "structural:local" {
		t.Fatalf("merged view after prune = %+v, %v", all, err)
	}
}

// A prune whose only removal sits in the overlay writes exactly that:
// the entry goes and the findings document is not rewritten — not even
// to identical bytes (REQ-result-lifecycle).
func TestPruneOfAnOverlayRecordLeavesTheDocumentUntouched(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	live := storeFinding("example.com/life.F", nil)
	dead := storeFinding("example.com/life.Gone", func(f *Finding) { f.Dirty = true })
	tree, store := lifecycleModule(t, live, dead)
	ctx := context.Background()
	before, err := os.Stat(store.path)
	if err != nil {
		t.Fatal(err)
	}
	bytesBefore, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	result, err := tree.PruneDetachedContext(ctx, store, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Removed) != 1 || result.Removed[0].Layer != LayerLocal || result.Kept.Total() != 1 {
		t.Fatalf("prune = %+v", result)
	}
	after, err := os.Stat(store.path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("the document was rewritten for an overlay-only removal: %v", err)
	}
	if bytesAfter, _ := os.ReadFile(store.path); !bytes.Equal(bytesBefore, bytesAfter) {
		t.Fatal("the document's bytes changed")
	}
	if _, err := os.Stat(store.entryPath("example.com/life.Gone")); !os.IsNotExist(err) {
		t.Fatalf("the overlay entry survived the prune: %v", err)
	}
	if all, err := store.Load(ctx); err != nil || len(all) != 1 || all[0].Symbol != "example.com/life.F" {
		t.Fatalf("merged view after prune = %+v, %v", all, err)
	}
}

// lifecycleRepoFinding is a committable record for the lifecycle tree
// whose subject packages agree with its symbol, so the retarget's
// package-boundary gate admits it.
func lifecycleRepoFinding(symbol, pkg string) Finding {
	f := lifecycleFinding(symbol)
	f.Dirty, f.Commit = false, "abc"
	f.TargetEvidence.RuntimeInputs = storeManifest()
	f.TargetEvidence.ObservationProof.Subject.Package = pkg
	f.OracleEvidence[0].Symbol = pkg + ".TestF"
	f.OracleEvidence[0].RuntimeInputs = storeManifest()
	f.OracleEvidence[0].ObservationProof.Subject.Package = pkg
	return f
}

// Retarget rewrites every stored record in its own layer: a symbol held
// in both layers — a committed row shadowed by a newer machine-local
// measurement — is two rewrites, the document keeps its row under the
// new symbol, the overlay its entry, and the identical rerun over the
// restored document succeeds with the same end state
// (REQ-result-lifecycle, REQ-result-layers).
func TestRetargetActsOnEveryLayer(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	committed := lifecycleRepoFinding("example.com/old.F", "example.com/old")
	tree, store := lifecycleModule(t, committed)
	ctx := context.Background()
	shadow := lifecycleRepoFinding("example.com/old.F", "example.com/old")
	shadow.Dirty, shadow.BodyHash = true, "moved"
	if _, err := store.Update(ctx, func([]Finding) ([]Finding, error) { return []Finding{shadow}, nil }); err != nil {
		t.Fatal(err)
	}
	documentBefore, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	type rewrite struct{ from, to, layer string }
	rewrites := func(r RetargetResult) []rewrite {
		var out []rewrite
		for _, record := range r.Rewritten {
			out = append(out, rewrite{record.From, record.To, record.Layer})
		}
		return out
	}
	want := []rewrite{{"example.com/old.F", "example.com/life.F", LayerRepo}, {"example.com/old.F", "example.com/life.F", LayerLocal}}
	verifyStore := func() {
		t.Helper()
		repo, err := store.loadRepo()
		if err != nil || len(repo) != 1 || repo[0].Symbol != "example.com/life.F" || repo[0].BodyHash != "body" {
			t.Fatalf("document after retarget = %+v, %v; want the committed row under the new symbol", repo, err)
		}
		merged, err := store.Load(ctx)
		if err != nil || len(merged) != 1 || merged[0].Symbol != "example.com/life.F" || merged[0].BodyHash != "moved" {
			t.Fatalf("merged view after retarget = %+v, %v; want the shadow under the new symbol", merged, err)
		}
		if _, err := os.Stat(store.entryPath("example.com/old.F")); !os.IsNotExist(err) {
			t.Fatalf("the old symbol's overlay entry survived: %v", err)
		}
	}
	result, err := tree.RetargetContext(ctx, store, "example.com/old.", "example.com/life.", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := rewrites(result); !reflect.DeepEqual(got, want) {
		t.Fatalf("rewrites = %v, want %v", got, want)
	}
	// The overlay's record, rewritten beside the row, shadows it once
	// the write is done; the overlay row itself is never shadowed.
	if !result.Rewritten[0].Shadowed || result.Rewritten[1].Shadowed {
		t.Fatalf("shadowed = %v/%v, want the document row alone", result.Rewritten[0].Shadowed, result.Rewritten[1].Shadowed)
	}
	verifyStore()
	// The consumer's shape: the document restored from the commit
	// while the overlay holds the first run's rewrite. The repo row
	// rewrites again; the overlay's entry is the same symbol's record
	// in another layer, never a collision.
	if err := os.WriteFile(store.path, documentBefore, 0o644); err != nil {
		t.Fatal(err)
	}
	rerun, err := tree.RetargetContext(ctx, store, "example.com/old.", "example.com/life.", false)
	if err != nil {
		t.Fatalf("the rerun over the restored document refused: %v", err)
	}
	if got := rewrites(rerun); !reflect.DeepEqual(got, want[:1]) {
		t.Fatalf("rerun rewrites = %v, want the repo row alone", got)
	}
	// The overlay's entry — the first run's rewrite — holds the new
	// symbol and shadows the restored row exactly as it shadowed the old
	// one; the rerun says so.
	if !rerun.Rewritten[0].Shadowed {
		t.Fatal("the rerun did not report the restored row shadowed by the overlay's entry")
	}
	verifyStore()
}

// A rewrite collides only within a layer: two document rows mapping
// onto one symbol refuse whole before any write, while a document row
// renamed onto a symbol the overlay already holds is the overlay
// shadowing the document (REQ-result-lifecycle).
func TestRetargetCollisionIsJudgedWithinALayer(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	old := lifecycleRepoFinding("example.com/old.F", "example.com/old")
	standing := lifecycleRepoFinding("example.com/life.F", "example.com/life")
	standing.BodyHash = "standing"
	tree, store := lifecycleModule(t, old, standing)
	ctx := context.Background()
	before, err := os.Stat(store.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []bool{true, false} {
		_, err := tree.RetargetContext(ctx, store, "example.com/old.", "example.com/life.", check)
		var collision *RecordCollisionError
		if !errors.As(err, &collision) || collision.Symbol != "example.com/life.F" || collision.Layer != LayerRepo || !strings.Contains(err.Error(), "findings document") {
			t.Fatalf("check=%v: err = %v, want the within-document collision", check, err)
		}
	}
	if after, err := os.Stat(store.path); err != nil || !os.SameFile(before, after) {
		t.Fatalf("a refused retarget rewrote the document: %v", err)
	}
	// The same shape across layers is no collision: the overlay's
	// record shadows the document's rewritten row.
	if _, err := store.Update(ctx, func(prior []Finding) ([]Finding, error) {
		return []Finding{old}, nil
	}); err != nil {
		t.Fatal(err)
	}
	local := lifecycleRepoFinding("example.com/life.F", "example.com/life")
	local.Dirty, local.BodyHash = true, "local"
	if _, err := store.Update(ctx, func(prior []Finding) ([]Finding, error) {
		return append(prior, local), nil
	}); err != nil {
		t.Fatal(err)
	}
	result, err := tree.RetargetContext(ctx, store, "example.com/old.", "example.com/life.", false)
	if err != nil || len(result.Rewritten) != 1 || result.Rewritten[0].Layer != LayerRepo || !result.Rewritten[0].Shadowed {
		t.Fatalf("cross-layer retarget = %+v, %v; want the repo row rewritten and reported shadowed", result, err)
	}
	repo, err := store.loadRepo()
	if err != nil || len(repo) != 1 || repo[0].Symbol != "example.com/life.F" || repo[0].BodyHash != "body" {
		t.Fatalf("document = %+v, %v", repo, err)
	}
	if merged, err := store.Load(ctx); err != nil || len(merged) != 1 || merged[0].BodyHash != "local" {
		t.Fatalf("merged view = %+v, %v; want the overlay shadowing", merged, err)
	}
	// Two overlay records mapping onto one symbol collide in the overlay.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	localOld := lifecycleRepoFinding("example.com/old.F", "example.com/old")
	localOld.Dirty = true
	localNew := lifecycleRepoFinding("example.com/life.F", "example.com/life")
	localNew.Dirty = true
	tree2, store2 := lifecycleModule(t, localOld, localNew)
	_, err = tree2.RetargetContext(ctx, store2, "example.com/old.", "example.com/life.", false)
	var overlayCollision *RecordCollisionError
	if !errors.As(err, &overlayCollision) || overlayCollision.Layer != LayerLocal || !strings.Contains(err.Error(), "machine-local overlay") {
		t.Fatalf("overlay collision = %v", err)
	}
}

// Rewritten and touched records are counted per layer: a symbol held in
// both layers whose killer carries the rename is two touched records,
// each naming its layer (REQ-result-lifecycle).
func TestRetargetCountsTouchedRecordsPerLayer(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	killed := func(dirty bool) Finding {
		f := lifecycleRepoFinding("example.com/life.F", "example.com/life")
		f.Dirty = dirty
		f.Killed, f.Mutants, f.CandidateCount, f.Generated = 1, 1, 1, 1
		f.Kills = []Kill{{Position: "p.go:2:2", Operator: "statement: delete", Killer: "example.com/gone.TestHelper"}}
		f.Operators = []OperatorSummary{{Operator: "statement: delete", Generated: 1, Killed: 1}}
		f.Survivors, f.Attested = nil, nil
		return f
	}
	tree, store := lifecycleModule(t, killed(false))
	ctx := context.Background()
	// Two overlay records against one committed: the touched count's
	// layers differ, so a count landing in the wrong layer is seen.
	other := killed(true)
	other.Symbol = "example.com/life.G"
	other.TargetEvidence.Symbol = "example.com/life.G"
	if _, err := store.Update(ctx, func([]Finding) ([]Finding, error) { return []Finding{killed(true), other}, nil }); err != nil {
		t.Fatal(err)
	}
	result, err := tree.RetargetContext(ctx, store, "example.com/gone.", "example.com/moved.", false)
	if err != nil {
		t.Fatal(err)
	}
	want := []TouchedRewrite{
		{Record: "example.com/life.F", Layer: LayerRepo, From: "example.com/gone.TestHelper", To: "example.com/moved.TestHelper"},
		{Record: "example.com/life.F", Layer: LayerLocal, From: "example.com/gone.TestHelper", To: "example.com/moved.TestHelper"},
		{Record: "example.com/life.G", Layer: LayerLocal, From: "example.com/gone.TestHelper", To: "example.com/moved.TestHelper"},
	}
	if result.Touched != (LayerCounts{Repo: 1, Local: 2}) || len(result.Rewritten) != 0 || !reflect.DeepEqual(result.TouchedRewrites, want) {
		t.Fatalf("touched per layer = %+v, want one repo and two local with %v", result, want)
	}
}

// A retarget rewrites the reviewed exemption entries whose subjects the
// rename moves, with the records — a subject is identity, the reason
// and rationale the reviewed content — lists them, and leaves an entry
// outside the prefix as reviewed; a check preview writes neither the
// document nor the record, and a rerun finds nothing left to rewrite
// (REQ-result-lifecycle, REQ-result-exemptions).
func TestRetargetRewritesTheExemptionRecordsSubjects(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	old := lifecycleRepoFinding("example.com/old.F", "example.com/old")
	tree, store := lifecycleModule(t, old)
	ctx := context.Background()
	// Two entries under the old prefix: one a subject the record's
	// evidence names, one no record carries; one outside the prefix.
	record := `{"version":1,"exemptions":[{"subject":"example.com/old.TestF","reason":"runtime input outside the tree: /tmp/x","rationale":"reviewed"},{"subject":"example.com/old.TestUnmeasured","reason":"r","rationale":"why"},{"subject":"example.com/other.TestG","reason":"r","rationale":"why"}]}`
	if err := os.WriteFile(ExemptionsPathFor(store.path), []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(store.path, tree.dir)
	if err != nil {
		t.Fatal(err)
	}
	docBefore, err := os.Stat(store.path)
	if err != nil {
		t.Fatal(err)
	}
	want := []RewrittenExemption{{From: "example.com/old.TestF", To: "example.com/life.TestF"}, {From: "example.com/old.TestUnmeasured", To: "example.com/life.TestUnmeasured"}}
	preview, err := tree.RetargetContext(ctx, reopened, "example.com/old.", "example.com/life.", true)
	if err != nil || !preview.Check || len(preview.Rewritten) != 1 || !reflect.DeepEqual(preview.Exemptions, want) {
		t.Fatalf("preview = %+v, %v; want the rewrite and the two moved subjects listed", preview, err)
	}
	if after, err := os.Stat(store.path); err != nil || !os.SameFile(docBefore, after) {
		t.Fatalf("the preview rewrote the document: %v", err)
	}
	if got, _, err := LoadExemptions(ExemptionsPathFor(store.path), store.moduleDir); err != nil || got[0].Subject != "example.com/old.TestF" {
		t.Fatalf("the preview rewrote the exemption record: %+v, %v", got, err)
	}
	result, err := tree.RetargetContext(ctx, reopened, "example.com/old.", "example.com/life.", false)
	if err != nil || len(result.Rewritten) != 1 || !reflect.DeepEqual(result.Exemptions, want) {
		t.Fatalf("retarget = %+v, %v; want the rewrite and the two moved subjects", result, err)
	}
	got, _, err := LoadExemptions(ExemptionsPathFor(store.path), store.moduleDir)
	if err != nil || len(got) != 3 || got[0] != (Exemption{Subject: "example.com/life.TestF", Reason: "runtime input outside the tree: /tmp/x", Rationale: "reviewed"}) ||
		got[1].Subject != "example.com/life.TestUnmeasured" || got[2].Subject != "example.com/other.TestG" {
		t.Fatalf("the exemption record after the rewrite = %+v, %v; want the two moved subjects rewritten in place, the reviewed content and the outside entry as they were", got, err)
	}
	// The file's form is the contract: version 1, two-space JSON, a
	// trailing newline, the file's mode kept.
	if raw, err := os.ReadFile(ExemptionsPathFor(store.path)); err != nil || !strings.HasPrefix(string(raw), "{\n  \"version\": 1,\n  \"exemptions\": [\n") || !strings.HasSuffix(string(raw), "}\n") {
		t.Fatalf("the rewritten record's form = %q, %v", raw, err)
	}
	if info, err := os.Stat(ExemptionsPathFor(store.path)); err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("the rewritten record's mode = %v, %v; want the file's own kept", info.Mode(), err)
	}
	// The store serves the record it wrote: the rewritten record is
	// committable under the rewritten entry.
	if live := reopened.Exemptions(); len(live) != 3 || live[0].Subject != "example.com/life.TestF" {
		t.Fatalf("the store's live exemptions after the rewrite = %+v", live)
	}
	// A rerun finds nothing under the old prefix.
	again, err := tree.RetargetContext(ctx, reopened, "example.com/old.", "example.com/life.", false)
	if err != nil || len(again.Rewritten) != 0 || len(again.Exemptions) != 0 {
		t.Fatalf("rerun = %+v, %v; want nothing left to rewrite", again, err)
	}
	// A retarget rewrites identity only: the stamp a measuring write
	// derived stays as measured (REQ-result-exemptions).
	stamped := lifecycleRepoFinding("example.com/old.F", "example.com/old")
	stamped.Exempted = []Exemption{{Subject: "example.com/old.TestF", Reason: "r", Rationale: "why"}}
	tree2, store2 := lifecycleModule(t, stamped)
	if _, err := tree2.RetargetContext(ctx, store2, "example.com/old.", "example.com/life.", false); err != nil {
		t.Fatal(err)
	}
	if after, err := store2.Load(ctx); err != nil || len(after) != 1 || len(after[0].Exempted) != 1 || after[0].Exempted[0].Subject != "example.com/old.TestF" {
		t.Fatalf("the stamp moved under the rename: %+v, %v", after, err)
	}
}

// A failing exemption write refuses the retarget whole: the entries
// are written under the lock ahead of the records, so nothing is
// rewritten and a rerun rewrites both; a record torn since the verb
// opened it refuses the rewrite naming the record, the records
// untouched and the torn file never written over
// (REQ-result-lifecycle, REQ-result-exemptions).
func TestRetargetRefusesWhenTheExemptionWriteFails(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	old := lifecycleRepoFinding("example.com/old.F", "example.com/old")
	tree, store := lifecycleModule(t, old)
	ctx := context.Background()
	record := `{"version":1,"exemptions":[{"subject":"example.com/old.TestF","reason":"r","rationale":"why"}]}`
	if err := os.WriteFile(ExemptionsPathFor(store.path), []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(store.path, tree.dir)
	if err != nil {
		t.Fatal(err)
	}
	refused := errors.New("disk full")
	reopened.hooks.beforeExemptionWrite = func() error { return refused }
	if _, err := tree.RetargetContext(ctx, reopened, "example.com/old.", "example.com/life.", false); !errors.Is(err, refused) || strings.Contains(err.Error(), "the exemption record was rewritten") {
		t.Fatalf("failed exemption write = %v; want the refusal naming no record write", err)
	}
	if got, _, _ := LoadExemptions(ExemptionsPathFor(store.path), store.moduleDir); len(got) != 1 || got[0].Subject != "example.com/old.TestF" {
		t.Fatalf("the record moved under a refused write: %+v", got)
	}
	if after, err := reopened.Load(ctx); err != nil || len(after) != 1 || after[0].Symbol != "example.com/old.F" {
		t.Fatalf("the records were rewritten under a refused exemption write: %+v, %v", after, err)
	}
	reopened.hooks.beforeExemptionWrite = nil
	again, err := tree.RetargetContext(ctx, reopened, "example.com/old.", "example.com/life.", false)
	if err != nil || len(again.Rewritten) != 1 || !reflect.DeepEqual(again.Exemptions, []RewrittenExemption{{From: "example.com/old.TestF", To: "example.com/life.TestF"}}) {
		t.Fatalf("rerun = %+v, %v; want the records and the entry rewritten together", again, err)
	}
	if got, _, _ := LoadExemptions(ExemptionsPathFor(store.path), store.moduleDir); len(got) != 1 || got[0].Subject != "example.com/life.TestF" {
		t.Fatalf("the rerun did not rewrite the record: %+v", got)
	}
	// A record torn since the open.
	torn := lifecycleRepoFinding("example.com/old.F", "example.com/old")
	tree2, store2 := lifecycleModule(t, torn)
	if err := os.WriteFile(ExemptionsPathFor(store2.path), []byte(`{"version":1,"exemptions":[{"subject":"example.com/old.TestF","reason":"r","rationale":"why"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	tornStore, err := OpenStore(store2.path, tree2.dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ExemptionsPathFor(store2.path), []byte("{ torn"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := tree2.RetargetContext(ctx, tornStore, "example.com/old.", "example.com/life.", false); err == nil || !strings.Contains(err.Error(), "malformed since this verb opened it") {
		t.Fatalf("the retarget over a torn record = %v; want the refusal naming it", err)
	}
	if got, _ := os.ReadFile(ExemptionsPathFor(store2.path)); string(got) != "{ torn" {
		t.Fatalf("the torn record was written over:\n%s", got)
	}
	if after, err := tornStore.Load(ctx); err != nil || len(after) != 1 || after[0].Symbol != "example.com/old.F" {
		t.Fatalf("the records were rewritten under a torn record: %+v, %v", after, err)
	}
	// A write failing AFTER the record's own: the overlay edit refused
	// by a read-only overlay. The record holds the moved subject, the
	// failure names it, and the rerun rewrites the records alone.
	if os.Getuid() == 0 {
		t.Skip("a read-only directory does not refuse root")
	}
	late := lifecycleRepoFinding("example.com/old.F", "example.com/old")
	tree3, store3 := lifecycleModule(t, late)
	shadow := lifecycleRepoFinding("example.com/old.F", "example.com/old")
	shadow.Dirty, shadow.BodyHash = true, "moved"
	if _, err := store3.Update(ctx, func([]Finding) ([]Finding, error) { return []Finding{shadow}, nil }); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ExemptionsPathFor(store3.path), []byte(`{"version":1,"exemptions":[{"subject":"example.com/old.TestF","reason":"r","rationale":"why"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	lateStore, err := OpenStore(store3.path, tree3.dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(lateStore.overlayDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(lateStore.overlayDir, 0o755) })
	_, err = tree3.RetargetContext(ctx, lateStore, "example.com/old.", "example.com/life.", false)
	if err == nil || !strings.Contains(err.Error(), "the exemption record was rewritten (1 subject(s) moved) ahead of the write that failed: ") || !strings.Contains(err.Error(), "machine-local overlay edits landed before") {
		t.Fatalf("a failure after the record's write = %v; want the record named ahead of the overlay failure", err)
	}
	if got, _, _ := LoadExemptions(ExemptionsPathFor(store3.path), store3.moduleDir); len(got) != 1 || got[0].Subject != "example.com/life.TestF" {
		t.Fatalf("the record after the failed overlay edit = %+v; want the moved subject it holds", got)
	}
	os.Chmod(lateStore.overlayDir, 0o755)
	again, err = tree3.RetargetContext(ctx, lateStore, "example.com/old.", "example.com/life.", false)
	if err != nil || len(again.Rewritten) != 1 || again.Rewritten[0].Layer != LayerLocal || len(again.Exemptions) != 0 {
		t.Fatalf("the rerun = %+v, %v; want the overlay record rewritten alone (the document landed before the failure) and no entry moved", again, err)
	}
}

// A rewrite that would give two reviewed entries one subject and reason
// refuses whole before any write, naming both — the second acceptance's
// rationale would otherwise be dead text behind the first
// (REQ-result-lifecycle, REQ-result-exemptions).
func TestRetargetRefusesAnExemptionCollision(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	old := lifecycleRepoFinding("example.com/old.F", "example.com/old")
	tree, store := lifecycleModule(t, old)
	ctx := context.Background()
	record := `{"version":1,"exemptions":[{"subject":"example.com/old.TestF","reason":"r","rationale":"first"},{"subject":"example.com/life.TestF","reason":"r","rationale":"second"}]}`
	if err := os.WriteFile(ExemptionsPathFor(store.path), []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(store.path, tree.dir)
	if err != nil {
		t.Fatal(err)
	}
	docBefore, err := os.Stat(store.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []bool{true, false} {
		_, err := tree.RetargetContext(ctx, reopened, "example.com/old.", "example.com/life.", check)
		if err == nil || !strings.Contains(err.Error(), "would carry example.com/life.TestF twice for \"r\" (from example.com/old.TestF and example.com/life.TestF)") {
			t.Fatalf("check=%v: err = %v, want the collision refused naming both entries", check, err)
		}
	}
	if after, err := os.Stat(store.path); err != nil || !os.SameFile(docBefore, after) {
		t.Fatalf("a refused retarget rewrote the document: %v", err)
	}
	if got, _, _ := LoadExemptions(ExemptionsPathFor(store.path), store.moduleDir); len(got) != 2 || got[0].Subject != "example.com/old.TestF" {
		t.Fatalf("a refused retarget rewrote the exemption record: %+v", got)
	}
}

// The exemption record's form is the contract: a key the form does not
// name refuses the load, never carried silently past a rewrite
// (REQ-result-exemptions).
func TestLoadExemptionsRefusesAnUnknownKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exemptions.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"exemptions":[{"subject":"a.T","reason":"r","rationale":"why","ticket":"X-1"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadExemptions(path, filepath.Dir(path)); err == nil || !strings.Contains(err.Error(), "ticket") {
		t.Fatalf("an unknown key loaded: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"$schema":"x","exemptions":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadExemptions(path, filepath.Dir(path)); err == nil || !strings.Contains(err.Error(), "$schema") {
		t.Fatalf("an unknown top-level key loaded: %v", err)
	}
}

// The exemption record's form is the contract: data past the document
// refuses the load (a second document appended behind the first would
// otherwise load as the first alone), and two entries accepting one
// subject for one reason refuse it (the second acceptance would be dead
// text behind the first) (REQ-result-lifecycle, REQ-result-exemptions).
func TestLoadExemptionsRefusesTrailingDataAndADuplicatedAcceptance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exemptions.json")
	first := `{"version":1,"exemptions":[]}`
	second := `{"version":1,"exemptions":[{"subject":"a.T","reason":"r","rationale":"why"}]}`
	for _, trailing := range []string{first + second, first + "\nok\n", first + " x", first + "{}", first + "\nnull\n"} {
		if err := os.WriteFile(path, []byte(trailing), 0o644); err != nil {
			t.Fatal(err)
		}
		if got, _, err := LoadExemptions(path, filepath.Dir(path)); err == nil {
			t.Fatalf("trailing data loaded as %+v from %q", got, trailing)
		}
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"exemptions":[{"subject":"a.T","reason":"r","rationale":"first"},{"subject":"a.U","reason":"r","rationale":"other"},{"subject":"a.T","reason":"r","rationale":"second"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadExemptions(path, filepath.Dir(path)); err == nil || !strings.Contains(err.Error(), `entries 0 and 2 both accept a.T for "r"`) {
		t.Fatalf("a duplicated acceptance loaded: %v", err)
	}
	// A shared subject under another reason, or a shared reason under
	// another subject, is two acceptances of two things.
	for _, record := range []string{
		`{"version":1,"exemptions":[{"subject":"a.T","reason":"r","rationale":"first"},{"subject":"a.T","reason":"s","rationale":"second"}]}`,
		`{"version":1,"exemptions":[{"subject":"a.T","reason":"r","rationale":"first"},{"subject":"a.U","reason":"r","rationale":"second"}]}`,
	} {
		if err := os.WriteFile(path, []byte(record), 0o644); err != nil {
			t.Fatal(err)
		}
		if got, _, err := LoadExemptions(path, filepath.Dir(path)); err != nil || len(got) != 2 {
			t.Fatalf("two acceptances of two things refused: %+v, %v", got, err)
		}
	}
	// A pair malformed on its own is named for its own fault first.
	if err := os.WriteFile(path, []byte(`{"version":1,"exemptions":[{},{}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadExemptions(path, filepath.Dir(path)); err == nil || !strings.Contains(err.Error(), "entry 0 needs subject, reason, and rationale") {
		t.Fatalf("an empty pair was named as a pair: %v", err)
	}
}
