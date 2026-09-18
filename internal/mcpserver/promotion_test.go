package mcpserver

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gomutant"
)

// The run response counts records promoted from the machine-local
// overlay into the committed findings document - a document change git
// only sees when committed (REQ-mcp-findings-doc). The dirty measure
// reports none; the clean serve that promotes reports it.
func TestToolRunReportsPromotedRecords(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	cacheDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheDir)
	s := serverAt(t)
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = s.dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=gomutant", "GIT_AUTHOR_EMAIL=gomutant@example.invalid",
			"GIT_COMMITTER_NAME=gomutant", "GIT_COMMITTER_EMAIL=gomutant@example.invalid",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	runGit("init", "-q")
	runGit("add", "-A")
	runGit("commit", "-q", "-m", "base")
	docFile := filepath.Join(s.dir, "lib", "doc.go")
	original, err := os.ReadFile(docFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(docFile, append(original, []byte("\n// uncommitted edit\n")...), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	in := runIn{
		TargetsJSON:      `{"targets":[{"symbol":"example.com/fixture/lib.Weak","oracle":["example.com/fixture/lib.TestWeak"],"oracleExplicit":true}]}`,
		Budget:           1,
		OracleTimeoutSec: 120,
	}
	_, dirty, err := s.toolRun(ctx, nil, in)
	if err != nil {
		t.Fatal(err)
	}
	if dirty.Promoted != 0 {
		t.Fatalf("dirty measure claimed %d promotions", dirty.Promoted)
	}
	// The aggregate machine-local count survives the findings-list cap
	// (REQ-result-local-signpost): the dirty measure's one record stayed
	// out of the repo document, and the response says so.
	if dirty.MachineLocalOnly != 1 {
		t.Fatalf("dirty measure reported %d machine-local records, want 1", dirty.MachineLocalOnly)
	}

	runGit("add", "-A")
	runGit("commit", "-q", "-m", "content lands")

	_, clean, err := s.toolRun(ctx, nil, in)
	if err != nil {
		t.Fatal(err)
	}
	if clean.Promoted != 1 {
		t.Fatalf("clean serve promoted = %d, want 1", clean.Promoted)
	}

	// Attesting against a record that can no longer serve as it stands
	// warns at disposition time (REQ-attest-survivor's echo clause).
	// Editing the oracle test's own body moves the recorded oracle
	// closure - a genuine pin move, not the growth carve-out.
	libTest := filepath.Join(s.dir, "lib", "lib_test.go")
	src, err := os.ReadFile(libTest)
	if err != nil {
		t.Fatal(err)
	}
	moved := strings.Replace(string(src), "t.Fatal(\"small arm\")", "t.Fatal(\"small arm moved\")", 1)
	if moved == string(src) {
		t.Fatal("TestWeak edit anchor missing")
	}
	if err := os.WriteFile(libTest, []byte(moved), 0o644); err != nil {
		t.Fatal(err)
	}
	all, err := s.loadFindings("")
	if err != nil || len(all) != 1 || len(all[0].Open()) == 0 {
		t.Fatalf("promoted document = %+v, %v", all, err)
	}
	open := all[0].Open()[0]
	_, echo, err := s.toolAttest(ctx, nil, attestIn{
		Symbol: all[0].Symbol, Position: open.Position, Operator: open.Operator, Reason: "equivalent by inspection",
	})
	if err != nil {
		t.Fatal(err)
	}
	if echo.Layer == "" || echo.Posture.Reuse != gomutant.FindingStale || len(echo.Posture.Reasons) == 0 || echo.Posture.Reasons[0].Channel != gomutant.PostureFreshness || echo.Posture.Analysis != gomutant.AnalysisRemeasure {
		t.Fatalf("birth-stale attest echo = %+v", echo)
	}

	// The re-measure judges the birth-stale disposition afresh - by
	// re-executing the mutant under the current oracle. The mutation
	// domain held (the body and operator set are untouched; only oracle
	// pins moved), the survivor re-appears at its exact site, and the
	// edited oracle still cannot distinguish the mutant - so the
	// disposition rides, reported as a carry, with the response rows
	// agreeing with the document (REQ-attest-survivor,
	// REQ-mcp-findings-doc).
	reIn := in
	reIn.OracleTimeoutSec = 180
	_, reMeasured, err := s.toolRun(ctx, nil, reIn)
	if err != nil {
		t.Fatal(err)
	}
	carrySeen := false
	for _, carry := range reMeasured.AttestationCarries {
		if strings.Contains(carry, "measurement pins moved") {
			carrySeen = true
		}
	}
	if !carrySeen {
		t.Fatalf("re-measure did not report the carried disposition: carries %+v sheds %+v", reMeasured.AttestationCarries, reMeasured.AttestationSheds)
	}
	if len(reMeasured.AttestationSheds) != 0 {
		t.Fatalf("held-domain re-measure shed the disposition: %+v", reMeasured.AttestationSheds)
	}
	if len(reMeasured.Findings) != 1 || reMeasured.Findings[0].Attested != 1 {
		t.Fatalf("re-measure response lost the disposition: %+v", reMeasured.Findings)
	}
	final, err := s.loadFindings("")
	if err != nil || len(final) != 1 || len(final[0].Attested) != 1 {
		t.Fatalf("document lost the carried disposition: %+v, %v", final, err)
	}
}

// A whole-tree run states its reconcile's drop on the response — the
// measuring main path and the zero-target reconcile alike
// (REQ-mcp-findings-doc).
func TestToolRunStatesTheReconcileDrop(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test for one mutant")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":          "module example.com/current\n\ngo 1.26.4\n",
		"current.go":      "package current\n\nfunc Value() int { return 1 }\n",
		"current_test.go": "package current\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) { if Value() != 1 { t.Fatal(Value()) } }\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	evidence := func(symbol string) gomutant.SubjectEvidence {
		return gomutant.SubjectEvidence{Symbol: symbol, MaximalClosure: "closure", TestVariantClosure: "tv", Toolchain: "go", BuildConfig: "build",
			ObservationAssertion: "caller assertion", ObservationStrategy: "proof/v1", ObservationSubjectPackage: "p",
			ObservationSubjectSymbol: symbol, ObservationObservable: true, ObservationEvidence: "proof",
			RuntimeInputs: "manifest", RuntimeDigest: "digest"}
	}
	stale := func(symbol string) gomutant.Finding {
		return gomutant.Finding{Symbol: symbol, BodyHash: "body", OperatorSet: "go/2", OracleTimeout: "1m0s", Dirty: true,
			TargetEvidence: evidence(symbol), OracleEvidence: []gomutant.SubjectEvidence{evidence(symbol + "Test")}}
	}
	path := gomutant.FindingsPathAt(dir, "")
	if err := gomutant.UpdateDocument(context.Background(), path, func([]gomutant.Finding) ([]gomutant.Finding, error) {
		return []gomutant.Finding{stale("example.com/current.Old")}, nil
	}); err != nil {
		t.Fatal(err)
	}
	s := New(dir)
	_, run, err := s.toolRun(context.Background(), nil, runIn{Budget: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Findings) != 1 || run.Note != "the whole-tree reconcile dropped 1 record(s) whose targets left the code" {
		t.Fatalf("measuring whole-tree run: note %q, rows %d; want the drop stated", run.Note, len(run.Findings))
	}
	// A scoped run, zero targets or not, claims none.
	if err := gomutant.UpdateDocument(context.Background(), path, func(current []gomutant.Finding) ([]gomutant.Finding, error) {
		return append(current, stale("example.com/current.Older")), nil
	}); err != nil {
		t.Fatal(err)
	}
	_, empty, err := s.toolRun(context.Background(), nil, runIn{Budget: 1, Symbols: []string{"example.com/current.Value"}, Packages: nil})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(empty.Note, "reconcile dropped") {
		t.Fatalf("a scoped run claimed a reconcile drop: %q", empty.Note)
	}
	if _, zero, err := s.toolRun(context.Background(), nil, runIn{Budget: 1, TargetsJSON: `{"targets":[]}`}); err != nil || strings.Contains(zero.Note, "reconcile dropped") {
		t.Fatalf("a scoped zero-target run: note %q, %v", zero.Note, err)
	}
}

// The zero-target whole-tree reconcile states a promotion it made on
// the response, as the measuring path does (REQ-mcp-findings-doc).
func TestToolRunStatesAPromotionOnTheZeroTargetReconcile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/empty\n\ngo 1.26.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "empty.go"), []byte("package empty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oracle := gomutant.SubjectEvidence{Symbol: "example.com/empty.TestBoundary", MaximalClosure: "closure", TestVariantClosure: "tv", Toolchain: "go", BuildConfig: "build",
		ObservationAssertion: "caller assertion", ObservationStrategy: "proof/v1", ObservationSubjectPackage: "p",
		ObservationSubjectSymbol: "example.com/empty.TestBoundary", ObservationObservable: true, ObservationEvidence: "proof",
		RuntimeInputs: "eyJ2IjoxfQ", RuntimeDigest: "digest", RuntimeUnverifiable: true, RuntimeReason: "sealed reason"}
	shaped := gomutant.Finding{Symbol: "example.com/empty.Boundary", BodyHash: "body", OperatorSet: "go/2", OracleTimeout: "1m0s", Commit: "abc",
		Shape:          &gomutant.TargetShape{Structural: &gomutant.StructuralSpec{Class: "import-boundary", Packages: []string{"p"}, Forbidden: "q"}},
		OracleEvidence: []gomutant.SubjectEvidence{oracle}}
	path := gomutant.FindingsPathAt(dir, "")
	// Written through the store, the unverifiable record is placed in
	// the machine-local overlay; the exemption then lands.
	seed, err := gomutant.OpenStore(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.Update(context.Background(), func([]gomutant.Finding) ([]gomutant.Finding, error) { return []gomutant.Finding{shaped}, nil }); err != nil {
		t.Fatal(err)
	}
	if layer, _ := seed.Layer(shaped); layer != "local" {
		t.Fatalf("seeded record layered %s, want local", layer)
	}
	record := `{"version":1,"exemptions":[{"subject":"example.com/empty.TestBoundary","reason":"sealed reason","rationale":"reviewed"}]}`
	if err := os.WriteFile(gomutant.ExemptionsPathFor(path), []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}
	_, out, err := New(dir).toolRun(context.Background(), nil, runIn{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Promoted != 1 {
		t.Fatalf("zero-target reconcile promoted %d on the response, want 1", out.Promoted)
	}
}

// A drift-refused whole-tree run still persisted its reconcile: the
// dropped count rides the drift error, as it rides every error exit
// after the final merge (REQ-mcp-findings-doc).
func TestToolRunDriftExitCarriesThePersistedDrop(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test for two mutants and drifts the tree mid-run")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	src := "package current\n\nfunc Value() int { return 1 }\n\nfunc Other() int { return 2 }\n"
	for name, content := range map[string]string{
		"go.mod":          "module example.com/current\n\ngo 1.26.4\n",
		"current.go":      src,
		"current_test.go": "package current\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) { if Value() != 1 { t.Fatal(Value()) } }\n\nfunc TestOther(t *testing.T) { if Other() != 2 { t.Fatal(Other()) } }\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	evidence := func(symbol string) gomutant.SubjectEvidence {
		return gomutant.SubjectEvidence{Symbol: symbol, MaximalClosure: "closure", TestVariantClosure: "tv", Toolchain: "go", BuildConfig: "build",
			ObservationAssertion: "caller assertion", ObservationStrategy: "proof/v1", ObservationSubjectPackage: "p",
			ObservationSubjectSymbol: symbol, ObservationObservable: true, ObservationEvidence: "proof",
			RuntimeInputs: "manifest", RuntimeDigest: "digest"}
	}
	path := gomutant.FindingsPathAt(dir, "")
	if err := gomutant.UpdateDocument(context.Background(), path, func([]gomutant.Finding) ([]gomutant.Finding, error) {
		return []gomutant.Finding{{Symbol: "example.com/current.Old", BodyHash: "body", OperatorSet: "go/2", OracleTimeout: "1m0s", Dirty: true,
			TargetEvidence: evidence("example.com/current.Old"), OracleEvidence: []gomutant.SubjectEvidence{evidence("example.com/current.OldTest")}}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	afterCommitForTest = func(gomutant.Finding) {
		if err := os.WriteFile(filepath.Join(dir, "current.go"), []byte(src+"\nfunc Drifted() int { return 9 }\n"), 0o644); err != nil {
			t.Error(err)
		}
		afterCommitForTest = nil
	}
	t.Cleanup(func() { afterCommitForTest = nil })
	_, _, err := New(dir).toolRun(context.Background(), nil, runIn{Jobs: 1, OracleTimeoutSec: 60})
	if err == nil || !strings.Contains(err.Error(), "tree changed under measurement") {
		t.Fatalf("run over a tree moved after its first commit = %v; want the drift refusal", err)
	}
	if !strings.Contains(err.Error(), "additionally, ") || !strings.Contains(err.Error(), "reconcile dropped 1 record(s)") || !strings.Contains(err.Error(), "(persisted)") {
		t.Fatalf("drift exit = %v; want the reconcile's persisted drop riding it", err)
	}
}
