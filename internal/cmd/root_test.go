package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gomutant "github.com/greatliontech/gomutant"
)

func TestExecuteContextCancellationStopsBeforeLoading(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ExecuteContext(ctx, []string{"discover", "--dir", fixtureDir}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled discover = %v", err)
	}
}

func TestRunCommandTimeoutCancelsBeforeCommit(t *testing.T) {
	docPath := filepath.Join(t.TempDir(), "findings.json")
	document, err := gomutant.Export(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(docPath, document, 0o644); err != nil {
		t.Fatal(err)
	}
	err = runCommand(context.Background(), runOptions{
		dir: filepath.Join(t.TempDir(), "missing"), findingsFile: docPath, timeout: time.Nanosecond, oracleTimeout: time.Hour,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("command timeout = %v, want context.DeadlineExceeded", err)
	}
	got, err := os.ReadFile(docPath)
	if err != nil || !bytes.Equal(got, document) {
		t.Fatalf("timed-out command changed findings: %v\n%s", err, got)
	}
}

func TestRunTimeoutFlagsNameIndependentLimits(t *testing.T) {
	cmd := newRunCommand()
	if got := cmd.Flags().Lookup("timeout"); got == nil || got.DefValue != "0s" {
		t.Fatalf("--timeout = %+v, want unlimited command default", got)
	}
	if got := cmd.Flags().Lookup("oracle-timeout"); got == nil || got.DefValue != "1m0s" {
		t.Fatalf("--oracle-timeout = %+v, want one-minute oracle default", got)
	}
}

type cancellingWriter struct{ cancel context.CancelFunc }

func (w cancellingWriter) Write(p []byte) (int, error) {
	if strings.Contains(string(p), "summary") {
		w.cancel()
	}
	return len(p), nil
}

func TestFindingsAtAndUpdate(t *testing.T) {
	dir := t.TempDir()
	path := findingsAt(dir, defaultFindings)
	if filepath.Dir(filepath.Dir(path)) != dir {
		t.Fatalf("default document not anchored at the tree: %s", path)
	}
	abs := filepath.Join(t.TempDir(), "f.json")
	if findingsAt(dir, abs) != abs {
		t.Fatal("absolute findings path rewritten")
	}
	evidence := func(symbol string) gomutant.SubjectEvidence {
		return gomutant.SubjectEvidence{Symbol: symbol, MaximalClosure: "closure", Toolchain: "go", BuildConfig: "build",
			ObservationAssertion: "caller assertion", ObservationStrategy: "proof/v1", ObservationSubjectPackage: "p",
			ObservationSubjectSymbol: symbol, ObservationObservable: true, ObservationEvidence: "proof",
			RuntimeInputs: "manifest", RuntimeDigest: "digest"}
	}
	fresh := []gomutant.Finding{{Symbol: "p.A", BodyHash: "h", OperatorSet: "go/2", OracleTimeout: "1m0s", Dirty: true,
		TargetEvidence: evidence("p.A"), OracleEvidence: []gomutant.SubjectEvidence{evidence("p.TestA")}, CandidateCount: 1, Generated: 1, Mutants: 1, Killed: 1,
		Operators: []gomutant.OperatorSummary{{Operator: "zero return", Generated: 1, Killed: 1}}}}
	err := gomutant.UpdateDocument(path, func(prior []gomutant.Finding) ([]gomutant.Finding, error) {
		return gomutant.MergeFindings(prior, fresh), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := loadFindings(path)
	if err != nil || len(got) != 1 || got[0].Symbol != "p.A" {
		t.Fatalf("round trip = %+v, %v", got, err)
	}
}

func TestRunCommandWholeTreePrunesWhenNoTargetsRemain(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/empty\n\ngo 1.26.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "empty.go"), []byte("package empty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	evidence := func(symbol string) gomutant.SubjectEvidence {
		return gomutant.SubjectEvidence{Symbol: symbol, MaximalClosure: "closure", Toolchain: "go", BuildConfig: "build",
			ObservationAssertion: "caller assertion", ObservationStrategy: "proof/v1", ObservationSubjectPackage: "p",
			ObservationSubjectSymbol: symbol, ObservationObservable: true, ObservationEvidence: "proof",
			RuntimeInputs: "manifest", RuntimeDigest: "digest"}
	}
	seed := gomutant.Finding{Symbol: "example.com/empty.Old", BodyHash: "body", OperatorSet: "go/2", OracleTimeout: "1m0s", Dirty: true,
		TargetEvidence: evidence("example.com/empty.Old"), OracleEvidence: []gomutant.SubjectEvidence{evidence("example.com/empty.TestOld")}}
	path := findingsAt(dir, defaultFindings)
	if err := gomutant.UpdateDocument(path, func([]gomutant.Finding) ([]gomutant.Finding, error) { return []gomutant.Finding{seed}, nil }); err != nil {
		t.Fatal(err)
	}
	targetsPath := filepath.Join(dir, "targets.json")
	if err := os.WriteFile(targetsPath, []byte(`{"targets":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runCommand(context.Background(), runOptions{dir: dir, findingsFile: defaultFindings, targetsFile: targetsPath}); err != nil {
		t.Fatal(err)
	}
	retained, err := loadFindings(path)
	if err != nil || len(retained) != 1 {
		t.Fatalf("scoped zero-target run pruned findings: %+v, %v", retained, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runCommand(ctx, runOptions{dir: dir, findingsFile: defaultFindings}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled empty whole-tree run = %v", err)
	}
	retained, err = loadFindings(path)
	if err != nil || len(retained) != 1 {
		t.Fatalf("cancelled empty whole-tree run changed findings: %+v, %v", retained, err)
	}
	var output bytes.Buffer
	if err := runCommand(context.Background(), runOptions{dir: dir, findingsFile: defaultFindings, output: &output}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "no targets\nsummary   0 targets: 0 measured, 0 cached, 0 skipped; 0 generated, 0 killed, 0 survived, 0 discarded; 0 attested, 0 open\n") {
		t.Fatalf("empty whole-tree output = %q", output.String())
	}
	got, err := loadFindings(path)
	if err != nil || len(got) != 0 {
		t.Fatalf("whole-tree empty discovery retained findings: %+v, %v", got, err)
	}
}

func TestInspectFindingsIncludesFullyAttestedDetachedRecord(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/empty\n\ngo 1.26.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "empty.go"), []byte("package empty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tree, err := gomutant.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	evidence := func(symbol string) gomutant.SubjectEvidence {
		return gomutant.SubjectEvidence{Symbol: symbol, MaximalClosure: "closure", Toolchain: "go", BuildConfig: "build",
			ObservationAssertion: "caller assertion", ObservationStrategy: "proof/v1", ObservationSubjectPackage: "p",
			ObservationSubjectSymbol: symbol, ObservationObservable: true, ObservationEvidence: "proof",
			RuntimeInputs: "manifest", RuntimeDigest: "digest"}
	}
	finding := gomutant.Finding{Symbol: "example.com/empty.Deleted", Labels: []string{"REQ-Z", "REQ-A"}, BodyHash: "body", OperatorSet: "go/2", OracleTimeout: "1m0s", Dirty: true,
		TargetEvidence: evidence("example.com/empty.Deleted"), OracleEvidence: []gomutant.SubjectEvidence{evidence("example.com/empty.TestDeleted")}, CandidateCount: 1, Generated: 1, Mutants: 1,
		Survivors: []gomutant.Survivor{{Position: "old.go:1:1", Operator: "zero return"}},
		Attested:  []gomutant.Attestation{{Position: "old.go:1:1", Operator: "zero return", Reason: "equivalent"}}}
	views, err := inspectFindings(context.Background(), tree, []gomutant.Finding{finding}, "REQ-A")
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].State != gomutant.FindingDetached || len(views[0].Open) != 0 || len(views[0].Attested) != 1 || views[0].Labels[0] != "REQ-A" {
		t.Fatalf("detached attested view = %+v", views)
	}
	views, err = inspectFindings(context.Background(), tree, []gomutant.Finding{finding}, "REQ-other")
	if err != nil || len(views) != 0 {
		t.Fatalf("label filter = %+v, %v", views, err)
	}
	var output bytes.Buffer
	if err := renderFindingsJSON(&output, views); err != nil {
		t.Fatal(err)
	}
	var decoded []findingView
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil || decoded == nil || len(decoded) != 0 {
		t.Fatalf("filtered-empty JSON = %q, %+v, %v", output.String(), decoded, err)
	}
}

func TestCobraCommandTree(t *testing.T) {
	root := newRootCommand()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Usage:") {
		t.Fatalf("root help missing usage:\n%s", output.String())
	}
	for _, command := range []string{"run", "discover", "findings", "attest", "ephemeral", "mcp"} {
		found := false
		for _, child := range root.Commands() {
			found = found || child.Name() == command
		}
		if !found {
			t.Fatalf("root command tree omits %q", command)
		}
	}
	root = newRootCommand()
	root.SetArgs([]string{"attest"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "--symbol") {
		t.Fatalf("missing attest flags = %v", err)
	}
	if err := Execute(nil); err == nil || !strings.Contains(err.Error(), "command is required") {
		t.Fatalf("empty invocation = %v", err)
	}
	if err := Execute([]string{"run", "-budget", "1"}); err == nil || !strings.Contains(err.Error(), "unknown shorthand") {
		t.Fatalf("single-dash long flag accepted: %v", err)
	}
}

func TestRenderRunStatus(t *testing.T) {
	var output bytes.Buffer
	renderPreparation(&output, gomutant.PreparationEvent{Stage: gomutant.PreparationLoading})
	renderPreparation(&output, gomutant.PreparationEvent{Stage: gomutant.PreparationResolving, Symbol: "p.F"})
	renderPreparation(&output, gomutant.PreparationEvent{Stage: gomutant.PreparationBaseline, Symbol: "p.F", Package: "example.com/p"})
	renderRunDecision(&output, gomutant.RunDecision{Symbol: "p.F", Action: "measure", Reason: "forced", Candidates: 3})
	renderRunDecision(&output, gomutant.RunDecision{Symbol: "p.G", Action: "cached"})
	renderRunSummary(&output, gomutant.RunSummary{Targets: 2, Measured: 1, Cached: 1, Generated: 3, Killed: 2, Survived: 1, Attested: 1, Open: 0})
	want := "prepare   loading\n" +
		"prepare   resolving p.F\n" +
		"prepare   baseline p.F example.com/p\n" +
		"measure   p.F  3 candidates (forced)\n" +
		"cached    p.G\n" +
		"summary   2 targets: 1 measured, 1 cached, 0 skipped; 3 generated, 2 killed, 1 survived, 0 discarded; 1 attested, 0 open\n"
	if output.String() != want {
		t.Fatalf("run status = %q, want %q", output.String(), want)
	}
}

func TestRunCommandReportsPreparationBeforeDecision(t *testing.T) {
	tmp := t.TempDir()
	targetsPath := filepath.Join(tmp, "targets.json")
	findingsPath := filepath.Join(tmp, "findings.json")
	if err := os.WriteFile(targetsPath, []byte(`{"targets":[{"symbol":"example.com/fixture/lib.BigLit","oracle":["example.com/fixture/lib.TestAdd"]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := runCommand(context.Background(), runOptions{
		dir: fixtureDir, targetsFile: targetsPath, findingsFile: findingsPath, budget: 1, jobs: 4, oracleTimeout: 2 * time.Minute, output: &output,
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(findingsPath)
	if err != nil {
		t.Fatal(err)
	}
	findings, err := gomutant.ParseFindings(data)
	if err != nil || len(findings) != 1 || findings[0].OracleTimeout != "2m0s" || findings[0].CandidateCount < findings[0].Generated ||
		findings[0].Generated != 1 || findings[0].Mutants != 0 || findings[0].Discarded != 1 {
		t.Fatalf("oracle timeout pin = %+v, %v", findings, err)
	}
	positions := []int{
		strings.Index(output.String(), "prepare   loading\n"),
		strings.Index(output.String(), "prepare   resolving example.com/fixture/lib.BigLit\n"),
		strings.Index(output.String(), "prepare   freshness example.com/fixture/lib.BigLit\n"),
		strings.Index(output.String(), "prepare   mutants example.com/fixture/lib.BigLit\n"),
		strings.Index(output.String(), "prepare   baseline example.com/fixture/lib.BigLit example.com/fixture/lib\n"),
		strings.Index(output.String(), "measure   example.com/fixture/lib.BigLit"),
	}
	for i, position := range positions {
		if position < 0 || i > 0 && position <= positions[i-1] {
			t.Fatalf("run progress positions = %v\n%s", positions, output.String())
		}
	}
	if !strings.Contains(output.String(), "measure   example.com/fixture/lib.BigLit  1 candidates") ||
		!strings.Contains(output.String(), "measured  example.com/fixture/lib.BigLit  1/") ||
		!strings.Contains(output.String(), "0 mutants, 0 killed, 1 discarded") {
		t.Fatalf("candidate counts missing from output:\n%s", output.String())
	}
}

func TestRunCommandCancellationLinearizesAtFindingsCommit(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":         "module example.com/cancel\n\ngo 1.26.5\n",
		"cancel.go":      "package cancel\nfunc Value() int { return 1 }\n",
		"cancel_test.go": "package cancel\nimport \"testing\"\nfunc TestValue(t *testing.T) { if Value() != 1 { t.Fail() } }\n",
		"targets.json":   `{"targets":[{"symbol":"example.com/cancel.Value","oracle":["example.com/cancel.TestValue"]}]}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	document, err := gomutant.Export(nil)
	if err != nil {
		t.Fatal(err)
	}
	docPath := filepath.Join(dir, "findings.json")
	if err := os.WriteFile(docPath, document, 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var output bytes.Buffer
	err = runCommand(ctx, runOptions{dir: dir, targetsFile: filepath.Join(dir, "targets.json"), findingsFile: docPath, budget: 1, output: &output})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled command = %v", err)
	}
	if output.String() != "" {
		t.Fatalf("cancelled command progress = %q", output.String())
	}
	got, err := os.ReadFile(docPath)
	if err != nil || !bytes.Equal(got, document) {
		t.Fatalf("findings changed on cancellation: %v\n%s", err, got)
	}
	if err := os.WriteFile(filepath.Join(dir, "targets.json"), []byte(`{"targets":[{"symbol":"example.com/cancel.Value","oracle":[],"oracleExplicit":true}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	err = runCommand(ctx, runOptions{
		dir: dir, targetsFile: filepath.Join(dir, "targets.json"), findingsFile: docPath,
		output: cancellingWriter{cancel: cancel},
	})
	if err != nil {
		t.Fatalf("post-commit output cancellation changed the result: %v", err)
	}
	got, err = os.ReadFile(docPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gomutant.ParseFindings(got); err != nil {
		t.Fatalf("post-commit document is invalid: %v\n%s", err, got)
	}
}

// TestInspectFindingsCarriesCandidateEvidence: a candidate-flagged record
// classifies unverifiable even with current-shaped subject evidence, and the
// view carries the candidate evidence for rendering (REQ-result-inspection).
func TestInspectFindingsCarriesCandidateEvidence(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/empty\n\ngo 1.26.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "empty.go"), []byte("package empty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tree, err := gomutant.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	evidence := gomutant.SubjectEvidence{Symbol: "example.com/empty.Gone", MaximalClosure: "closure", Toolchain: "go", BuildConfig: "build",
		RuntimeInputs: "manifest", RuntimeDigest: "digest"}
	finding := gomutant.Finding{Symbol: "example.com/empty.Gone", BodyHash: "body", OperatorSet: "go/2", OracleTimeout: "1m0s", Dirty: true,
		TargetEvidence: evidence, OracleEvidence: []gomutant.SubjectEvidence{evidence}, CandidateCount: 1, Generated: 1, Mutants: 1, Killed: 1,
		CandidateEvidence: []gomutant.CandidateEvidence{{Position: "gone.go:1:1", Operator: "return: zero", Reason: "panicked before observation finalization", Disposition: "killed"}}}
	views, err := inspectFindings(context.Background(), tree, []gomutant.Finding{finding}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || len(views[0].Candidates) != 1 {
		t.Fatalf("candidate evidence lost from the view: %+v", views)
	}
	if views[0].Candidates[0].Reason != "panicked before observation finalization" {
		t.Fatalf("candidate reason lost: %+v", views[0].Candidates[0])
	}
}
