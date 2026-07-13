package gomutant

import (
	"bytes"
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"
)

func TestSubjectViewsBatchByModule(t *testing.T) {
	tr := fixtureTree(t)
	symbols := []string{
		"example.com/fixture/lib.Add",
		"example.com/fixture/lib.Weak",
		"example.com/fixture/lib.TestAdd",
		"example.com/fixture/methods.Counter.Inc",
	}
	views, err := tr.newSubjectViews(context.Background(), symbols)
	if err != nil {
		t.Fatal(err)
	}
	if len(views.modules) != 1 || len(views.bySymbol) != len(symbols) {
		t.Fatalf("batched views = %d modules, %d subjects", len(views.modules), len(views.bySymbol))
	}
	shared := views.bySymbol[symbols[0]].view
	moduleDir := views.bySymbol[symbols[0]].moduleDir
	engine, err := gofresh.New(gofresh.WithDir(moduleDir), gofresh.WithEnv(tr.eng.GoEnv()...))
	if err != nil {
		t.Fatal(err)
	}
	for _, symbol := range symbols {
		batched := views.bySymbol[symbol]
		if batched.view != shared || batched.module != views.modules[0] {
			t.Fatalf("%s does not share the module view", symbol)
		}
		singleton, err := engine.NewViewFor([]gofresh.Subject{batched.subject}, moduleDir, gofresh.CodeResult)
		if err != nil {
			t.Fatal(err)
		}
		fingerprint, err := singleton.Capture(batched.subject)
		if err != nil {
			t.Fatal(err)
		}
		files, err := singleton.SourceFilesFor(batched.subject)
		if err != nil {
			t.Fatal(err)
		}
		if fingerprint != batched.fp || !slices.Equal(files, batched.sourceFiles) {
			t.Fatalf("%s batch differs: fingerprint %+v != %+v, files %v != %v", symbol, batched.fp, fingerprint, batched.sourceFiles, files)
		}
	}
	validations := 0
	views.modules[0].producer = true
	views.modules[0].validate = func(context.Context) error {
		validations++
		return nil
	}
	if err := views.validateProducers(context.Background()); err != nil || validations != 1 {
		t.Fatalf("module validations = %d, error %v", validations, err)
	}
}

func TestSubjectViewsPartitionWorkspaceModules(t *testing.T) {
	tree, err := Load("internal/engine/testdata/workspacemod")
	if err != nil {
		t.Fatal(err)
	}
	symbols := []string{
		"example.com/ws.Root",
		"example.com/ws.TestRoot",
		"example.com/ws/sub.Nested",
		"example.com/ws/sub.TestNested",
	}
	views, err := tree.newSubjectViews(context.Background(), symbols)
	if err != nil {
		t.Fatal(err)
	}
	if len(views.modules) != 2 {
		t.Fatalf("workspace batch has %d modules, want 2", len(views.modules))
	}
	root := views.bySymbol[symbols[0]]
	rootTest := views.bySymbol[symbols[1]]
	sub := views.bySymbol[symbols[2]]
	subTest := views.bySymbol[symbols[3]]
	if root.view != rootTest.view || sub.view != subTest.view || root.view == sub.view || root.moduleDir == sub.moduleDir {
		t.Fatalf("workspace partition = root %p/%s, sub %p/%s", root.view, root.moduleDir, sub.view, sub.moduleDir)
	}
}

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
	inspection, err := tr.InspectFinding(f)
	if err != nil || inspection.State != FindingCurrent {
		t.Fatalf("just-measured inspection = %+v, %v", inspection, err)
	}
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
	inspection, err = movedEnvironment.InspectFinding(f)
	if err != nil || inspection.State != FindingStale {
		t.Fatalf("moved-input inspection = %+v, %v", inspection, err)
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
	unverifiable := f
	unverifiable.TargetEvidence.RuntimeUnverifiable = true
	unverifiable.TargetEvidence.RuntimeReason = "manual input"
	inspection, err = tr.InspectFinding(unverifiable)
	if err != nil || inspection.State != FindingUnverifiable || inspection.Reason != "manual input" {
		t.Fatalf("unverifiable inspection = %+v, %v", inspection, err)
	}
	detached := f
	detached.Symbol = "example.com/fixture/lib.Deleted"
	detached.OperatorSet = "go/1"
	inspection, err = tr.InspectFinding(detached)
	if err != nil || inspection.State != FindingDetached {
		t.Fatalf("detached inspection = %+v, %v", inspection, err)
	}
	oldOperator := f
	oldOperator.OperatorSet = "go/1"
	inspection, err = tr.InspectFinding(oldOperator)
	if err != nil || inspection.State != FindingStale || !strings.Contains(inspection.Reason, "operator") {
		t.Fatalf("operator inspection = %+v, %v", inspection, err)
	}
	missingOracle := f
	missingOracle.OracleEvidence = append([]SubjectEvidence(nil), f.OracleEvidence...)
	missingOracle.OracleEvidence[0].Symbol = "example.com/fixture/lib.TestDeleted"
	inspection, err = tr.InspectFinding(missingOracle)
	if err != nil || inspection.State != FindingStale || !strings.Contains(inspection.Reason, "oracle") {
		t.Fatalf("missing-oracle inspection = %+v, %v", inspection, err)
	}
}

func TestInspectFindingStates(t *testing.T) {
	tr := fixtureTree(t)
	target, err := tr.newSubjectView("example.com/fixture/lib.Add")
	if err != nil {
		t.Fatal(err)
	}
	oracle, err := tr.newSubjectView("example.com/fixture/lib.TestAdd")
	if err != nil {
		t.Fatal(err)
	}
	moduleDir, packageDir, err := tr.eng.PackageContext("example.com/fixture/lib")
	if err != nil {
		t.Fatal(err)
	}
	state, err := runtimeinput.FromTestLogEnv(nil, moduleDir, packageDir, tr.eng.GoEnv())
	if err != nil {
		t.Fatal(err)
	}
	targetEvidence, oracleEvidence, err := attachEvidence(target, []*subjectView{oracle}, state)
	if err != nil {
		t.Fatal(err)
	}
	finding := Finding{Symbol: "example.com/fixture/lib.Add", OperatorSet: "go/2", OracleExplicit: true, Timeout: "1m0s", TargetEvidence: targetEvidence, OracleEvidence: oracleEvidence}
	inspection, err := tr.InspectFinding(finding)
	if err != nil || inspection.State != FindingCurrent {
		t.Fatalf("current inspection = %+v, %v", inspection, err)
	}
	batched, err := tr.newSubjectViews(context.Background(), []string{
		"example.com/fixture/lib.Add",
		"example.com/fixture/lib.TestAdd",
		"example.com/fixture/lib.TestWeak",
	})
	if err != nil {
		t.Fatal(err)
	}
	multiTarget, multiOracle, err := attachEvidence(batched.bySymbol["example.com/fixture/lib.Add"], []*subjectView{
		batched.bySymbol["example.com/fixture/lib.TestAdd"],
		batched.bySymbol["example.com/fixture/lib.TestWeak"],
	}, state)
	if err != nil {
		t.Fatal(err)
	}
	multi := finding
	multi.TargetEvidence = multiTarget
	multi.OracleEvidence = multiOracle
	inspection, err = tr.InspectFinding(multi)
	if err != nil || inspection.State != FindingCurrent {
		t.Fatalf("multi-oracle current inspection = %+v, %v", inspection, err)
	}
	derived := finding
	derived.OracleExplicit = false
	inspection, err = tr.InspectFinding(derived)
	if err != nil || inspection.State != FindingStale || !strings.Contains(inspection.Reason, "derived oracle") {
		t.Fatalf("changed derived oracle inspection = %+v, %v", inspection, err)
	}
	oldOperator := finding
	oldOperator.OperatorSet = "go/1"
	inspection, err = tr.InspectFinding(oldOperator)
	if err != nil || inspection.State != FindingStale || !strings.Contains(inspection.Reason, "operator") {
		t.Fatalf("operator inspection = %+v, %v", inspection, err)
	}
	detached := finding
	detached.Symbol = "example.com/fixture/lib.Deleted"
	detached.OperatorSet = "go/1"
	inspection, err = tr.InspectFinding(detached)
	if err != nil || inspection.State != FindingDetached {
		t.Fatalf("detached precedence = %+v, %v", inspection, err)
	}
	staleTarget := finding
	staleTarget.TargetEvidence.MaximalClosure = "moved"
	inspection, err = tr.InspectFinding(staleTarget)
	if err != nil || inspection.State != FindingStale {
		t.Fatalf("target stale inspection = %+v, %v", inspection, err)
	}
	staleRuntime := finding
	staleRuntime.TargetEvidence.RuntimeDigest = "moved"
	inspection, err = tr.InspectFinding(staleRuntime)
	if err != nil || inspection.State != FindingStale || !strings.Contains(inspection.Reason, "runtime") {
		t.Fatalf("runtime stale inspection = %+v, %v", inspection, err)
	}
	unverifiable := finding
	unverifiable.TargetEvidence.RuntimeUnverifiable = true
	unverifiable.TargetEvidence.RuntimeReason = "manual input"
	inspection, err = tr.InspectFinding(unverifiable)
	if err != nil || inspection.State != FindingUnverifiable || inspection.Reason != "manual input" {
		t.Fatalf("unverifiable inspection = %+v, %v", inspection, err)
	}
	staleOracle := finding
	staleOracle.OracleEvidence = append([]SubjectEvidence(nil), finding.OracleEvidence...)
	staleOracle.OracleEvidence[0].MaximalClosure = "moved"
	inspection, err = tr.InspectFinding(staleOracle)
	if err != nil || inspection.State != FindingStale || !strings.Contains(inspection.Reason, "oracle") {
		t.Fatalf("oracle stale inspection = %+v, %v", inspection, err)
	}
	missingOracle := finding
	missingOracle.OracleEvidence = append([]SubjectEvidence(nil), finding.OracleEvidence...)
	missingOracle.OracleEvidence[0].Symbol = "example.com/fixture/lib.TestDeleted"
	inspection, err = tr.InspectFinding(missingOracle)
	if err != nil || inspection.State != FindingStale || !strings.Contains(inspection.Reason, "no longer resolves") {
		t.Fatalf("missing oracle inspection = %+v, %v", inspection, err)
	}
	staleAndMissing := missingOracle
	staleAndMissing.TargetEvidence.MaximalClosure = "moved"
	inspection, err = tr.InspectFinding(staleAndMissing)
	if err != nil || inspection.State != FindingStale || strings.Contains(inspection.Reason, "no longer resolves") {
		t.Fatalf("target-first inspection = %+v, %v", inspection, err)
	}
	staleBeforeMissing := multi
	staleBeforeMissing.OracleEvidence = append([]SubjectEvidence(nil), multi.OracleEvidence...)
	staleBeforeMissing.OracleEvidence[0].MaximalClosure = "moved"
	staleBeforeMissing.OracleEvidence[1].Symbol = "example.com/fixture/lib.TestZZZDeleted"
	inspection, err = tr.InspectFinding(staleBeforeMissing)
	if err != nil || inspection.State != FindingStale || strings.Contains(inspection.Reason, "TestZZZDeleted") {
		t.Fatalf("canonical valid-oracle precedence = %+v, %v", inspection, err)
	}
	setOrder := finding
	unverifiableOracle := finding.OracleEvidence[0]
	unverifiableOracle.RuntimeUnverifiable = true
	unverifiableOracle.RuntimeReason = "manual oracle input"
	missingFirst := finding.OracleEvidence[0]
	missingFirst.Symbol = "example.com/fixture/lib.TestAAADeleted"
	setOrder.OracleEvidence = []SubjectEvidence{unverifiableOracle, missingFirst}
	inspection, err = tr.InspectFinding(setOrder)
	if err != nil || inspection.State != FindingStale || !strings.Contains(inspection.Reason, "TestAAADeleted") {
		t.Fatalf("canonical oracle-set inspection = %+v, %v", inspection, err)
	}
	badTimeout := finding
	badTimeout.Timeout = "invalid"
	if _, err := tr.InspectFinding(badTimeout); err == nil {
		t.Fatal("invalid timeout inspected")
	}
}

func TestSortedSubjectEvidence(t *testing.T) {
	original := []SubjectEvidence{{Symbol: "p.TestZ"}, {Symbol: "p.TestA"}}
	got := sortedSubjectEvidence(original)
	if got[0].Symbol != "p.TestA" || got[1].Symbol != "p.TestZ" {
		t.Fatalf("sorted evidence = %+v", got)
	}
	if original[0].Symbol != "p.TestZ" {
		t.Fatal("sorting mutated the finding record")
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
