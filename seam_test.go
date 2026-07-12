package gomutant

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gofresh/runtimeinput"
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

func TestLoadStoresAbsoluteRoot(t *testing.T) {
	tr, err := Load(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(tr.dir) {
		t.Fatalf("tree root = %q, want absolute", tr.dir)
	}
}

// TestFresh pins the pin-check query (REQ-result-stale as a question): a
// finding measured now is fresh for the same request, stale for a wider
// budget, stale when its body pin lies, and the check never runs a mutant.
func TestFresh(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	t.Setenv("GOMUTANT_TEST_INPUT", "one")
	tr := fixtureTree(t)
	tg := Target{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestAdd"}}
	fs, err := tr.Run(context.Background(), []Target{tg}, Options{Budget: 1})
	if err != nil {
		t.Fatal(err)
	}
	f := fs[0]
	doc, err := Export(fs)
	if err != nil {
		t.Fatal(err)
	}
	withoutBudget := bytes.Replace(doc, []byte(`"budget": 1,`), nil, 1)
	if bytes.Equal(withoutBudget, doc) {
		t.Fatal("exported finding did not carry an explicit budget pin")
	}
	if _, err := ParseFindings(withoutBudget); err == nil {
		t.Fatal("finding with missing budget accepted")
	}
	if ok, err := tr.Fresh(f, tg, 1); err != nil || !ok {
		t.Fatalf("just-measured finding not fresh: %v %v", ok, err)
	}
	if ok, err := tr.Fresh(f, tg, 0); err != nil || ok {
		t.Fatalf("capped finding fresh for an exhaustive request: %v %v", ok, err)
	}
	if ok, err := tr.FreshFor(f, tg, 1, 2*time.Minute); err != nil || ok {
		t.Fatalf("finding fresh under a different timeout: %v %v", ok, err)
	}
	t.Setenv("GOMUTANT_TEST_INPUT", "two")
	movedEnvironment := fixtureTree(t)
	if ok, err := movedEnvironment.Fresh(f, tg, 1); err != nil || ok {
		t.Fatalf("finding fresh after runtime input changed: %v %v", ok, err)
	}
	t.Setenv("GOMUTANT_TEST_INPUT", "one")
	stale := f
	stale.TargetEvidence.MaximalClosure = "moved"
	if ok, err := tr.Fresh(stale, tg, 1); err != nil || ok {
		t.Fatalf("moved closure pin read fresh: %v %v", ok, err)
	}
	missingPurity := f
	missingPurity.OracleEvidence = append([]SubjectEvidence(nil), f.OracleEvidence...)
	missingPurity.OracleEvidence[0].PurityAssertion = ""
	if ok, err := tr.Fresh(missingPurity, tg, 1); err != nil || ok {
		t.Fatalf("missing purity pin read fresh: %v %v", ok, err)
	}
	other := Target{Symbol: "example.com/fixture/lib.Weak"}
	if _, err := tr.Fresh(f, other, 1); err == nil || !strings.Contains(err.Error(), "checked against") {
		t.Fatalf("cross-symbol check accepted: %v", err)
	}
}

// TestIncompleteObservationCannotBeFresh pins the fail-closed process boundary
// (REQ-exec-observation, REQ-result-stale): finalized incomplete evidence is
// persistable but never reusable, and its explicit disposition must agree with
// the manifest.
func TestIncompleteObservationCannotBeFresh(t *testing.T) {
	tr := fixtureTree(t)
	view, err := tr.newSubjectView("example.com/fixture/lib.Add")
	if err != nil {
		t.Fatal(err)
	}
	state, err := runtimeinput.Incomplete(view.moduleDir, "test process timed out")
	if err != nil {
		t.Fatal(err)
	}
	state, err = runtimeinput.Absolute(state, view.moduleDir)
	if err != nil {
		t.Fatal(err)
	}
	evidence, _, err := attachEvidence(view, nil, state)
	if err != nil {
		t.Fatal(err)
	}
	if valid, err := view.valid(evidence); err != nil || valid {
		t.Fatalf("incomplete evidence valid = %v, err = %v", valid, err)
	}
	tampered := evidence
	tampered.RuntimeUnverifiable = false
	tampered.RuntimeReason = ""
	if valid, err := view.valid(tampered); err != nil || valid {
		t.Fatalf("inconsistent disposition valid = %v, err = %v", valid, err)
	}
}

// TestFreshnessUsesTreeWorkspaceMode pins the Gofresh composition boundary:
// package loading, mutation execution, and freshness analysis all use the
// tree's explicit workspace mode rather than an enclosing ambient workspace.
func TestFreshnessUsesTreeWorkspaceMode(t *testing.T) {
	t.Setenv("GOFLAGS", "")
	t.Setenv("GOWORK", filepath.Join(t.TempDir(), "missing.work"))
	tr := fixtureTree(t)
	t.Setenv("GOFLAGS", "-tags=changed-after-load")
	if _, err := tr.newSubjectView("example.com/fixture/lib.Add"); err != nil {
		t.Fatalf("freshness inherited ambient GOWORK: %v", err)
	}
	for _, entry := range tr.eng.GoEnv() {
		if entry == "GOFLAGS=-tags=changed-after-load" {
			t.Fatal("tree environment changed after Load")
		}
	}
}

func TestRunUsesEnvironmentFrozenAtLoad(t *testing.T) {
	t.Setenv("GOMUTANT_FROZEN_INPUT", "loaded")
	tr := fixtureTree(t)
	t.Setenv("GOMUTANT_FROZEN_INPUT", "changed-after-load")
	tg := Target{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestFrozenEnvironment"}}
	findings, err := tr.Run(context.Background(), []Target{tg}, Options{Budget: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Mutants != 1 || findings[0].Killed != 1 {
		t.Fatalf("frozen-environment finding = %+v, want one killed mutant", findings)
	}
}
