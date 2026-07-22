package gomutant

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/gomutant/internal/engine"
)

func observedSubjectViews(t *testing.T, tree *Tree, symbols []string) *subjectViewSet {
	t.Helper()
	views, err := tree.newSubjectViewsWithPackageContext(context.Background(), symbols, tree.eng.PackageContextContext, true, tree.newSubjectEngines(nil))
	if err != nil {
		t.Fatal(err)
	}
	return views
}

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
		singleton, err := engine.NewViewFor(context.Background(), []gofresh.Subject{batched.subject}, moduleDir, gofresh.CodeResult)
		if err != nil {
			t.Fatal(err)
		}
		fingerprint, err := singleton.Capture(context.Background(), batched.subject)
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

func TestMutantsContextEnumeratesMethods(t *testing.T) {
	tree, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, symbol := range []string{
		"github.com/greatliontech/gomutant.Tree.FilterTargetsContext",
		"github.com/greatliontech/gomutant/internal/engine.Tree.PackagePathContext",
	} {
		mutants, err := tree.eng.MutantsContext(context.Background(), symbol, 0)
		if err != nil || len(mutants) == 0 {
			t.Fatalf("%s mutants = %d, %v", symbol, len(mutants), err)
		}
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

func TestRunPreparationMemoizesPackageAnalysis(t *testing.T) {
	var testsCalls, validationCalls, contextCalls, rapidCalls int
	preparation := &runPreparation{
		packageOf: func(_ context.Context, symbol string) (string, string, error) {
			return "example.com/p", strings.TrimPrefix(symbol, "example.com/p."), nil
		},
		testsOf: func(context.Context, string) ([]string, error) {
			testsCalls++
			return []string{"example.com/p.TestP"}, nil
		},
		validate: func(context.Context, []string) error {
			validationCalls++
			return nil
		},
		contextFor: func(context.Context, string) (string, string, error) {
			contextCalls++
			return "/module", "/module/p", nil
		},
		splitRapidPkgs: func(_ context.Context, packages []string) ([]string, []string, error) {
			rapidCalls++
			return []string{packages[0]}, packages[1:], nil
		},
		derivedOracles: map[string][]string{},
		validations:    map[string]oracleValidationResult{},
		contexts:       map[string]packageContextResult{},
	}

	first, err := preparation.oracle(context.Background(), Target{Symbol: "example.com/p.F"})
	if err != nil {
		t.Fatal(err)
	}
	first[0] = "changed by caller"
	second, err := preparation.oracle(context.Background(), Target{Symbol: "example.com/p.G"})
	if err != nil {
		t.Fatal(err)
	}
	if testsCalls != 1 || !slices.Equal(second, []string{"example.com/p.TestP"}) {
		t.Fatalf("derived oracle calls = %d, second = %v", testsCalls, second)
	}
	if explicit, err := preparation.oracle(context.Background(), Target{Symbol: "example.com/p.H", Oracle: []string{"example.com/q.TestQ"}}); err != nil || !slices.Equal(explicit, []string{"example.com/q.TestQ"}) || testsCalls != 1 {
		t.Fatalf("explicit oracle = %v, derived calls = %d", explicit, testsCalls)
	}

	oracle := []string{"example.com/p.TestP", "example.com/q.TestQ"}
	if err := preparation.validateOracle(context.Background(), oracle); err != nil {
		t.Fatal(err)
	}
	if err := preparation.validateOracle(context.Background(), slices.Clone(oracle)); err != nil {
		t.Fatal(err)
	}
	if err := preparation.validateOracle(context.Background(), []string{oracle[1], oracle[0]}); err != nil {
		t.Fatal(err)
	}
	if validationCalls != 2 {
		t.Fatalf("validation calls = %d, want exact ordered sequences once", validationCalls)
	}

	for range 2 {
		moduleDir, packageDir, err := preparation.packageContext(context.Background(), "example.com/p")
		if err != nil || moduleDir != "/module" || packageDir != "/module/p" {
			t.Fatalf("package context = %q, %q, %v", moduleDir, packageDir, err)
		}
	}
	if contextCalls != 1 {
		t.Fatalf("package context calls = %d", contextCalls)
	}

	rapid, err := preparation.rapidPackages(context.Background(), []string{"example.com/p", "example.com/q"})
	if err != nil || !rapid["example.com/p"] {
		t.Fatal("rapid package not classified")
	}
	rapid, err = preparation.rapidPackages(context.Background(), []string{"ignored after first scan"})
	if err != nil || !rapid["example.com/p"] || rapidCalls != 1 {
		t.Fatalf("rapid scan calls = %d", rapidCalls)
	}
}

func TestRunPreparationDoesNotMemoizeCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	preparation := &runPreparation{
		validate: func(context.Context, []string) error {
			calls++
			cancel()
			return nil
		},
		validations: map[string]oracleValidationResult{},
	}
	if err := preparation.validateOracle(ctx, []string{"p.TestP"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled validation = %v", err)
	}
	if err := preparation.validateOracle(context.Background(), []string{"p.TestP"}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("validation calls = %d, cancellation was memoized", calls)
	}
}

func TestEvidenceSetMemoizesFindingRuntimeManifest(t *testing.T) {
	tree := fixtureTree(t)
	views := observedSubjectViews(t, tree, []string{
		"example.com/fixture/lib.Add",
		"example.com/fixture/lib.TestAdd",
		"example.com/fixture/lib.TestWeak",
	})
	target := views.bySymbol["example.com/fixture/lib.Add"]
	oracle := []*subjectView{
		views.bySymbol["example.com/fixture/lib.TestAdd"],
		views.bySymbol["example.com/fixture/lib.TestWeak"],
	}
	state, err := runtimeinput.FromTestLogEnv(nil, tree.dir, tree.dir, tree.eng.GoEnv(), runtimeinput.WithCompletedProcess("finding"), runtimeinput.WithBracket(testBracket(t, tree.dir)))
	if err != nil {
		t.Fatal(err)
	}
	targetEvidence, oracleEvidence, err := attachEvidence(target, oracle, state)
	if err != nil {
		t.Fatal(err)
	}
	prior := Finding{
		OperatorSet: engine.OperatorSet, OracleExplicit: true, OracleTimeout: time.Minute.String(),
		TargetEvidence: targetEvidence, OracleEvidence: oracleEvidence,
	}
	calls := 0
	current := func(context.Context, string, string, []string) (runtimeinput.State, error) {
		calls++
		return state.State, nil
	}
	matches, err := evidenceSetMatchesContextWithCurrent(context.Background(), prior, target, oracle, true, engine.OperatorSet, time.Minute.String(), current)
	if err != nil || !matches || calls != 2 {
		t.Fatalf("matches = %v, calls = %d, error = %v", matches, calls, err)
	}
	movementCalls := 0
	moved := state.State
	moved.Digest = "moved"
	matches, err = evidenceSetMatchesContextWithCurrent(context.Background(), prior, target, oracle, true, engine.OperatorSet, time.Minute.String(), func(context.Context, string, string, []string) (runtimeinput.State, error) {
		movementCalls++
		if movementCalls == 1 {
			return state.State, nil
		}
		return moved, nil
	})
	if err != nil || matches || movementCalls != 2 {
		t.Fatalf("moving manifest matches = %v, calls = %d, error = %v", matches, movementCalls, err)
	}

	workspace, err := Load("internal/engine/testdata/workspacemod")
	if err != nil {
		t.Fatal(err)
	}
	workspaceViews := observedSubjectViews(t, workspace, []string{"example.com/ws.Root", "example.com/ws/sub.TestNested"})
	workspaceTarget := workspaceViews.bySymbol["example.com/ws.Root"]
	workspaceOracle := []*subjectView{workspaceViews.bySymbol["example.com/ws/sub.TestNested"]}
	workspaceState, err := runtimeinput.FromTestLogEnv(nil, workspace.dir, workspace.dir, workspace.eng.GoEnv(), runtimeinput.WithCompletedProcess("workspace"), runtimeinput.WithBracket(testBracket(t, workspace.dir)))
	if err != nil {
		t.Fatal(err)
	}
	workspaceTargetEvidence, workspaceOracleEvidence, err := attachEvidence(workspaceTarget, workspaceOracle, workspaceState)
	if err != nil {
		t.Fatal(err)
	}
	workspacePrior := Finding{
		OperatorSet: engine.OperatorSet, OracleExplicit: true, OracleTimeout: time.Minute.String(),
		TargetEvidence: workspaceTargetEvidence, OracleEvidence: workspaceOracleEvidence,
	}
	calls = 0
	current = func(context.Context, string, string, []string) (runtimeinput.State, error) {
		calls++
		return workspaceState.State, nil
	}
	matches, err = evidenceSetMatchesContextWithCurrent(context.Background(), workspacePrior, workspaceTarget, workspaceOracle, true, engine.OperatorSet, time.Minute.String(), current)
	if err != nil || !matches || calls != 2 {
		t.Fatalf("cross-module matches = %v, calls = %d, error = %v", matches, calls, err)
	}
}

func TestEvidenceSetPropagatesRuntimeCancellation(t *testing.T) {
	tree := fixtureTree(t)
	views := observedSubjectViews(t, tree, []string{"example.com/fixture/lib.Add"})
	target := views.bySymbol["example.com/fixture/lib.Add"]
	state, err := runtimeinput.FromTestLogEnv(nil, tree.dir, tree.dir, tree.eng.GoEnv(), runtimeinput.WithCompletedProcess("cancellation"), runtimeinput.WithBracket(testBracket(t, tree.dir)))
	if err != nil {
		t.Fatal(err)
	}
	evidence, _, err := attachEvidence(target, nil, state)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	prior := Finding{OperatorSet: engine.OperatorSet, OracleExplicit: true, OracleTimeout: time.Minute.String(), TargetEvidence: evidence}
	matches, err := evidenceSetMatchesContextWithCurrent(ctx, prior, target, nil, true, engine.OperatorSet, time.Minute.String(), func(ctx context.Context, _, _ string, _ []string) (runtimeinput.State, error) {
		cancel()
		return runtimeinput.State{}, ctx.Err()
	})
	if !errors.Is(err, context.Canceled) || matches {
		t.Fatalf("cancelled runtime check = %v, %v", matches, err)
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
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(fixtureDir)); err != nil {
		t.Fatal(err)
	}
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	tg := Target{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/observed.TestObservedInput"}}
	fs, err := tr.Run(context.Background(), []Target{tg}, Options{Budget: 1})
	if err != nil {
		t.Fatal(err)
	}
	f := fs[0]
	if f.TargetEvidence.ObservationAssertion == "" || f.TargetEvidence.ObservationStrategy != gofresh.ObservationRTA ||
		f.TargetEvidence.ObservationEvidence == "" || f.OracleEvidence[0].ObservationEvidence == "" {
		t.Fatalf("measured finding lacks observation proof: %+v", f)
	}
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
		t.Fatalf("finding fresh under a different oracle timeout: %v %v", ok, err)
	}
	stale := f
	stale.TargetEvidence.MaximalClosure = "moved"
	if ok, err := tr.Fresh(stale, tg, 1); err != nil || ok {
		t.Fatalf("moved closure pin read fresh: %v %v", ok, err)
	}
	missingProof := f
	missingProof.OracleEvidence = append([]SubjectEvidence(nil), f.OracleEvidence...)
	missingProof.OracleEvidence[0].ObservationEvidence = ""
	if ok, err := tr.Fresh(missingProof, tg, 1); err != nil || ok {
		t.Fatalf("missing observation proof read fresh: %v %v", ok, err)
	}
	oldProof := f
	oldProof.TargetEvidence.ObservationStrategy = "gofresh/observation-rta@2"
	oldProof.TargetEvidence.ObservationEvidence = "b0c9aaba09049e1642fd517a09b00877"
	oldProof.OracleEvidence = append([]SubjectEvidence(nil), f.OracleEvidence...)
	oldProof.OracleEvidence[0].ObservationStrategy = "gofresh/observation-rta@2"
	oldProof.OracleEvidence[0].ObservationEvidence = "46056b8e7fea776a3b95b884b1b1c953"
	if ok, err := tr.Fresh(oldProof, tg, 1); err != nil || ok {
		t.Fatalf("superseded observation proof read fresh: %v %v", ok, err)
	}
	inspection, err = tr.InspectFinding(oldProof)
	if err != nil || inspection.State != FindingUnverifiable {
		t.Fatalf("superseded observation proof inspection = %+v, %v", inspection, err)
	}
	var oldProofDecisions []RunDecision
	remeasured, err := tr.Run(context.Background(), []Target{tg}, Options{Budget: 1, Prior: []Finding{oldProof}, Decision: func(decision RunDecision) {
		oldProofDecisions = append(oldProofDecisions, decision)
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(remeasured) != 1 || remeasured[0].Cached || len(oldProofDecisions) != 1 || !strings.HasPrefix(oldProofDecisions[0].Reason, "unverifiable: ") {
		t.Fatalf("superseded observation proof run = %+v, decisions %+v", remeasured, oldProofDecisions)
	}
	other := Target{Symbol: "example.com/fixture/lib.Weak"}
	if _, err := tr.Fresh(f, other, 1); err == nil || !strings.Contains(err.Error(), "checked against") {
		t.Fatalf("cross-symbol check accepted: %v", err)
	}
	unverifiable := f
	unverifiable.TargetEvidence.RuntimeUnverifiable = true
	unverifiable.TargetEvidence.RuntimeReason = "manual input"
	inspection, err = tr.InspectFinding(unverifiable)
	if err != nil || inspection.State != FindingUnverifiable || inspection.Reason != "target: manual input" {
		t.Fatalf("unverifiable inspection = %+v, %v; want the reason attributed to its subject", inspection, err)
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
	input := filepath.Join(dir, "observed", "input.txt")
	if err := os.WriteFile(input, []byte("moved\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	movedEnvironment, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := movedEnvironment.Fresh(f, tg, 1); err != nil || ok {
		t.Fatalf("finding fresh after runtime input changed: %v %v", ok, err)
	}
	inspection, err = movedEnvironment.InspectFinding(f)
	if err != nil || inspection.State != FindingStale {
		t.Fatalf("moved-input inspection = %+v, %v", inspection, err)
	}
	// The drift names the moved identity itself, not just that one moved
	// (REQ-result-inspection).
	if !strings.Contains(inspection.Reason, "runtime inputs changed") || !strings.Contains(inspection.Reason, "input.txt") {
		t.Fatalf("moved-input reason = %q, want the moved identity named", inspection.Reason)
	}
}

func TestInspectFindingStates(t *testing.T) {
	tr := fixtureTree(t)
	initialViews := observedSubjectViews(t, tr, []string{"example.com/fixture/lib.Add", "example.com/fixture/lib.TestAdd"})
	target := initialViews.bySymbol["example.com/fixture/lib.Add"]
	oracle := initialViews.bySymbol["example.com/fixture/lib.TestAdd"]
	moduleDir, packageDir, err := tr.eng.PackageContext("example.com/fixture/lib")
	if err != nil {
		t.Fatal(err)
	}
	state, err := runtimeinput.FromTestLogEnv(nil, moduleDir, packageDir, tr.eng.GoEnv(), runtimeinput.WithCompletedProcess("inspection"), runtimeinput.WithBracket(testBracket(t, moduleDir)))
	if err != nil {
		t.Fatal(err)
	}
	targetEvidence, oracleEvidence, err := attachEvidence(target, []*subjectView{oracle}, state)
	if err != nil {
		t.Fatal(err)
	}
	finding := Finding{Symbol: "example.com/fixture/lib.Add", OperatorSet: engine.OperatorSet, OracleExplicit: true, OracleTimeout: "1m0s", TargetEvidence: targetEvidence, OracleEvidence: oracleEvidence}
	inspection, err := tr.InspectFinding(finding)
	if err != nil || inspection.State != FindingCurrent {
		t.Fatalf("current inspection = %+v, %v", inspection, err)
	}
	batched := observedSubjectViews(t, tr, []string{
		"example.com/fixture/lib.Add",
		"example.com/fixture/lib.TestAdd",
		"example.com/fixture/lib.TestWeak",
	})
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
	if err != nil || inspection.State != FindingUnverifiable || inspection.Reason != "target: manual input" {
		t.Fatalf("unverifiable inspection = %+v, %v; want the reason attributed to its subject", inspection, err)
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
	badTimeout.OracleTimeout = "invalid"
	if _, err := tr.InspectFinding(badTimeout); err == nil {
		t.Fatal("invalid oracle timeout inspected")
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
	view := observedSubjectViews(t, tr, []string{"example.com/fixture/lib.Add"}).bySymbol["example.com/fixture/lib.Add"]
	state, err := runtimeinput.Incomplete(view.moduleDir, "timed-out-test", "test process timed out")
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
	findings, err := tr.Run(context.Background(), []Target{tg}, Options{Budget: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Mutants != 2 || findings[0].Killed != 1 || len(findings[0].Survivors) != 1 || findings[0].Survivors[0] != (Survivor{Position: "lib.go:24:2", Operator: "statement: delete", Execution: "executed-and-passed"}) {
		t.Fatalf("frozen-environment finding = %+v, want exact two-candidate prefix outcomes", findings)
	}
}
