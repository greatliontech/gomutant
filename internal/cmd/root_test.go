package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gomutant "github.com/greatliontech/gomutant"
)

func testStore(t *testing.T, dir string) *gomutant.Store {
	t.Helper()
	store, err := gomutant.OpenStore(filepath.Join(dir, defaultFindings), dir)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// isolatedFixture copies the shared fixture tree into a temp git repository
// with one commit. A test asserting on the repo-layer findings document needs
// both commit provenance and evidence measured on a tree nothing else writes
// into: concurrent test packages plant input fixtures inside the shared
// tree's packages, which moves the observation bracket and routes the record
// to the machine-local overlay. The copy skips dot-prefixed scratch entries
// and tolerates files vanishing mid-walk — the planted fixtures are
// dot-prefixed and removed on their test's cleanup, so a plain CopyFS would
// re-import the same parallel-run race this helper exists to remove.
func isolatedFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	err := filepath.WalkDir(fixtureDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		rel, err := filepath.Rel(fixtureDir, path)
		if err != nil {
			return err
		}
		if rel != "." && strings.HasPrefix(filepath.Base(rel), ".") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dir, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"add", "."},
		{"-c", "user.name=fixture", "-c", "user.email=fixture@example.com", "commit", "--quiet", "--message", "fixture"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		// The helper must hold on any machine: neither config files
		// (signing, templates, hooks) nor exported GIT_* plumbing
		// (GIT_DIR, GIT_WORK_TREE, author overrides) may reach the
		// isolated repo.
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, "GIT_") {
				cmd.Env = append(cmd.Env, kv)
			}
		}
		cmd.Env = append(cmd.Env, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	return dir
}

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
	if got := cmd.Flags().Lookup("oracle-timeout"); got == nil || got.DefValue != "0s" {
		t.Fatalf("--oracle-timeout = %+v, want the derive-from-baseline default (0s)", got)
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
		return gomutant.SubjectEvidence{Symbol: symbol, MaximalClosure: "closure", TestVariantClosure: "tv", Toolchain: "go", BuildConfig: "build",
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
	got, err := loadFindings(filepath.Dir(filepath.Dir(path)), path)
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
		return gomutant.SubjectEvidence{Symbol: symbol, MaximalClosure: "closure", TestVariantClosure: "tv", Toolchain: "go", BuildConfig: "build",
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
	if err := runCommand(context.Background(), runOptions{dir: dir, findingsFile: defaultFindings, targetsFile: targetsPath, output: io.Discard}); err != nil {
		t.Fatal(err)
	}
	retained, err := loadFindings(dir, path)
	if err != nil || len(retained) != 1 {
		t.Fatalf("scoped zero-target run pruned findings: %+v, %v", retained, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runCommand(ctx, runOptions{dir: dir, findingsFile: defaultFindings, output: io.Discard}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled empty whole-tree run = %v", err)
	}
	retained, err = loadFindings(dir, path)
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
	got, err := loadFindings(filepath.Dir(filepath.Dir(path)), path)
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
		return gomutant.SubjectEvidence{Symbol: symbol, MaximalClosure: "closure", TestVariantClosure: "tv", Toolchain: "go", BuildConfig: "build",
			ObservationAssertion: "caller assertion", ObservationStrategy: "proof/v1", ObservationSubjectPackage: "p",
			ObservationSubjectSymbol: symbol, ObservationObservable: true, ObservationEvidence: "proof",
			RuntimeInputs: "manifest", RuntimeDigest: "digest"}
	}
	finding := gomutant.Finding{Symbol: "example.com/empty.Deleted", Labels: []string{"REQ-Z", "REQ-A"}, BodyHash: "body", OperatorSet: "go/2", OracleTimeout: "1m0s", Dirty: true,
		TargetEvidence: evidence("example.com/empty.Deleted"), OracleEvidence: []gomutant.SubjectEvidence{evidence("example.com/empty.TestDeleted")}, CandidateCount: 1, Generated: 1, Mutants: 1,
		Survivors: []gomutant.Survivor{{Position: "old.go:1:1", Operator: "zero return"}},
		Attested:  []gomutant.Attestation{{Position: "old.go:1:1", Operator: "zero return", Reason: "equivalent"}}}
	views, err := inspectFindings(context.Background(), tree, testStore(t, dir), []gomutant.Finding{finding}, "REQ-A", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].State != gomutant.FindingDetached || len(views[0].Open) != 0 || len(views[0].Attested) != 1 || views[0].Labels[0] != "REQ-A" {
		t.Fatalf("detached attested view = %+v", views)
	}
	if views[0].Layer != "local" || views[0].LayerReason == "" {
		t.Fatalf("dirty finding layer = %q (%q), want machine-local", views[0].Layer, views[0].LayerReason)
	}
	views, err = inspectFindings(context.Background(), tree, testStore(t, dir), []gomutant.Finding{finding}, "REQ-other", "", "")
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

// A plan over real targets renders its decisions and plan tallies with
// no zeroed run summary - the summary line would claim a measurement
// that never happened (execution.md's plan render clause).
func TestRunCommandPlanRendersDecisionsWithoutSummary(t *testing.T) {
	if testing.Short() {
		t.Skip("runs plan preparation over the fixture")
	}
	fixture := isolatedFixture(t)
	targetsPath := filepath.Join(t.TempDir(), "targets.json")
	if err := os.WriteFile(targetsPath, []byte(`{"targets":[{"symbol":"example.com/fixture/lib.Weak","oracle":["example.com/fixture/lib.TestWeak"],"oracleExplicit":true}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := runCommand(context.Background(), runOptions{
		dir: fixture, targetsFile: targetsPath, findingsFile: defaultFindings, budget: 1, plan: true, output: &output,
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "plan      1 measure") || !strings.Contains(output.String(), "plan only: no baselines probed, no mutants executed, nothing persisted") {
		t.Fatalf("plan output missing its own tallies: %q", output.String())
	}
	if strings.Contains(output.String(), "summary   ") {
		t.Fatalf("plan output carries a run summary: %q", output.String())
	}
	if _, err := os.Stat(findingsAt(fixture, defaultFindings)); !os.IsNotExist(err) {
		t.Fatalf("plan persisted a findings document: %v", err)
	}
}

// The confirming line drops the saturated candidates segment - the
// confirmations counter is the signal there - while executing lines
// keep it (display only; the event carries the tallies unchanged).
func TestRenderExecutionEventDropsSaturatedCandidatesWhileConfirming(t *testing.T) {
	var out bytes.Buffer
	renderExecutionEvent(&out, gomutant.ExecutionEvent{Phase: "executing", TargetIndex: 1, TargetCount: 2, Symbol: "p.F", CandidatesDone: 3, CandidatesTotal: 7}, "", "")
	renderExecutionEvent(&out, gomutant.ExecutionEvent{Phase: "confirming", TargetIndex: 1, TargetCount: 2, Symbol: "p.F", CandidatesDone: 7, CandidatesTotal: 7, ConfirmationsDone: 1, ConfirmationsTotal: 4}, "", "")
	want := "executing target 1/2 p.F  candidates 3/7\n" +
		"confirming target 1/2 p.F  confirmations 1/4\n"
	if out.String() != want {
		t.Fatalf("execution lines = %q, want %q", out.String(), want)
	}
}

func TestRunCommandReportsPreparationBeforeDecision(t *testing.T) {
	fixture := isolatedFixture(t)
	tmp := t.TempDir()
	targetsPath := filepath.Join(tmp, "targets.json")
	findingsPath := filepath.Join(tmp, "findings.json")
	if err := os.WriteFile(targetsPath, []byte(`{"targets":[{"symbol":"example.com/fixture/lib.BigLit","oracle":["example.com/fixture/lib.TestAdd"]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := runCommand(context.Background(), runOptions{
		dir: fixture, targetsFile: targetsPath, findingsFile: findingsPath, budget: 1, jobs: 4, oracleTimeout: 2 * time.Minute, output: &output,
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(findingsPath)
	if err != nil {
		t.Fatal(err)
	}
	findings, err := gomutant.ParseFindings(data)
	if len(findings) == 0 {
		// The record parses from the repo-layer document: the isolated
		// fixture keeps the evidence verifiable and the provenance clean, so
		// a record routed to the machine-local overlay is a failure, not
		// load noise — the rendered rows name the disqualifier.
		t.Fatalf("no findings in the repo document; run output:\n%s", output.String())
	}
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
	if strings.Contains(output.String(), "machine-local:") {
		t.Fatalf("committable record rendered a machine-local line:\n%s", output.String())
	}
}

// A measured record the store routes to the machine-local overlay names its
// disqualifier on the run face (REQ-result-layers): whether the artifact is
// safe to stage is answered by the tool, so a run that renders healthy counts
// never leaves the repo document silently missing the record.
func TestRunCommandStatesMachineLocalRouting(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":        "module example.com/local\n\ngo 1.26.5\n",
		"local.go":      "package local\ntype Kind int\nfunc Value() int { return 1 }\n",
		"local_test.go": "package local\nimport \"testing\"\nfunc TestValue(t *testing.T) { if Value() != 1 { t.Fail() } }\n",
		"targets.json":  `{"targets":[{"symbol":"example.com/local.Value","oracle":["example.com/local.TestValue"]},{"symbol":"example.com/local.Kind","oracle":["example.com/local.TestValue"]}]}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	findingsPath := filepath.Join(dir, "findings.json")
	var output bytes.Buffer
	if err := runCommand(context.Background(), runOptions{
		dir: dir, targetsFile: filepath.Join(dir, "targets.json"), findingsFile: findingsPath, budget: 1, output: &output,
	}); err != nil {
		t.Fatal(err)
	}
	// No git repository above the temp module: provenance is dirty, the
	// record stays machine-local, and the run face says so — once. The
	// type target skips, and a skipped row carries no record to state a
	// layer for.
	if !strings.Contains(output.String(), "machine-local: dirty worktree provenance") {
		t.Fatalf("run output missing the machine-local routing line:\n%s", output.String())
	}
	if got := strings.Count(output.String(), "machine-local:"); got != 1 {
		t.Fatalf("machine-local rows = %d, want exactly the measured record's:\n%s", got, output.String())
	}
	// The aggregate form: a run whose records all stayed machine-local
	// states it once at the end, so an unchanged repo document never
	// reads as a silent write failure.
	if !strings.Contains(output.String(), "1 record(s) machine-local only") {
		t.Fatalf("run output missing the aggregate machine-local line:\n%s", output.String())
	}
	// The analysis heartbeat: a measuring run's in-process analysis
	// stretches print a phase line (the first event immediately, then
	// time-gated), so the historically silent freshness and
	// producer-validation phases are visible on the run face.
	if !strings.Contains(output.String(), "analysis  ") {
		t.Fatalf("run output missing the analysis heartbeat:\n%s", output.String())
	}
	data, err := os.ReadFile(findingsPath)
	if err != nil {
		t.Fatal(err)
	}
	findings, err := gomutant.ParseFindings(data)
	if err != nil || len(findings) != 0 {
		t.Fatalf("repo document for a machine-local record = %+v, %v", findings, err)
	}
	// A cached serve of the still-local record states the layer too.
	output.Reset()
	if err := runCommand(context.Background(), runOptions{
		dir: dir, targetsFile: filepath.Join(dir, "targets.json"), findingsFile: findingsPath, budget: 1, output: &output,
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "cached    example.com/local.Value") ||
		!strings.Contains(output.String(), "machine-local: dirty worktree provenance") {
		t.Fatalf("cached serve missing the machine-local routing line:\n%s", output.String())
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

// shedCancellingWriter cancels its context the moment the streamed output
// contains an attestation-shed line, capturing everything written.
type shedCancellingWriter struct {
	buf    bytes.Buffer
	cancel context.CancelFunc
}

func (w *shedCancellingWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	if bytes.Contains(w.buf.Bytes(), []byte("attestation shed: ")) {
		w.cancel()
	}
	return len(p), nil
}

// A shed is reported by the commit that persists it: an aborted run keeps
// its completed targets' stripped records (REQ-exec-cancellation), so a
// shed buffered for the never-reached epilogue is dropped silently -
// REQ-attest-survivor demands it loudly in every mode, and the field
// failure was a stopped campaign wiping 49 dispositions without a line of
// output. The cancelling writer fires on the shed line itself, so the run
// ending in context.Canceled proves the line streamed from the commit,
// before any epilogue could have rendered it.
func TestRunCommandAbortAfterCommitReportsSheds(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":       "module example.com/shed\n\ngo 1.26.5\n",
		"shed.go":      "package shed\nfunc Value() int { return 1 }\n",
		"shed_test.go": "package shed\nimport \"testing\"\nfunc TestValue(t *testing.T) { Value() }\n",
		"targets.json": `{"targets":[{"symbol":"example.com/shed.Value","oracle":["example.com/shed.TestValue"]}]}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	document, err := gomutant.Export(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".gomutant"), 0o755); err != nil {
		t.Fatal(err)
	}
	docPath := filepath.Join(dir, ".gomutant", "findings.json")
	if err := os.WriteFile(docPath, document, 0o644); err != nil {
		t.Fatal(err)
	}
	// A committed git tree keeps the records on the repo layer, where the
	// prior document's attestations are the merge graft's input.
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "-A"},
		{"-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "-m", "fixture"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	// The assertion-free oracle leaves every mutant surviving.
	targetsFile := filepath.Join(dir, "targets.json")
	var first bytes.Buffer
	if err := runCommand(context.Background(), runOptions{dir: dir, targetsFile: targetsFile, findingsFile: docPath, oracleTimeout: time.Minute, output: &first}); err != nil {
		t.Fatalf("first run: %v\n%s", err, first.String())
	}
	raw, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatal(err)
	}
	findings, err := gomutant.ParseFindings(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || len(findings[0].Survivors) == 0 {
		t.Fatalf("first run left no survivor to attest: %+v", findings)
	}
	survivor := findings[0].Survivors[0]
	if err := findings[0].Attest(survivor.Position, survivor.Operator, "regression-harness equivalence"); err != nil {
		t.Fatal(err)
	}
	attested, err := gomutant.Export(findings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(docPath, attested, 0o644); err != nil {
		t.Fatal(err)
	}
	// Editing the target's body moves the mutation domain: the record
	// re-measures whole and the disposition sheds at the target's
	// incremental commit - where the cancel fires.
	if err := os.WriteFile(filepath.Join(dir, "shed.go"), []byte("package shed\nfunc Value() int { return 0 + 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writer := &shedCancellingWriter{cancel: cancel}
	err = runCommand(ctx, runOptions{dir: dir, targetsFile: targetsFile, findingsFile: docPath, oracleTimeout: time.Minute, output: writer})
	if !bytes.Contains(writer.buf.Bytes(), []byte("attestation shed: ")) {
		t.Fatalf("no shed line streamed; output:\n%s", writer.buf.String())
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run after the shed line = %v: the shed reached the output only at the epilogue, after the abort window\n%s", err, writer.buf.String())
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
	evidence := gomutant.SubjectEvidence{Symbol: "example.com/empty.Gone", MaximalClosure: "closure", TestVariantClosure: "tv", Toolchain: "go", BuildConfig: "build",
		RuntimeInputs: "manifest", RuntimeDigest: "digest"}
	finding := gomutant.Finding{Symbol: "example.com/empty.Gone", BodyHash: "body", OperatorSet: "go/2", OracleTimeout: "1m0s", Dirty: true,
		TargetEvidence: evidence, OracleEvidence: []gomutant.SubjectEvidence{evidence}, CandidateCount: 1, Generated: 1, Mutants: 1, Killed: 1,
		CandidateEvidence: []gomutant.CandidateEvidence{{Position: "gone.go:1:1", Operator: "return: zero", Reason: "panicked before observation finalization", Disposition: "killed"}}}
	views, err := inspectFindings(context.Background(), tree, testStore(t, dir), []gomutant.Finding{finding}, "", "", "")
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

// TestRunCommandPlanNeverPrunesEmptyWholeTree pins the plan clause's
// no-write guarantee on the zero-target whole-tree path: the executing
// run's empty-discovery reconciliation deliberately prunes, and a plan
// must not — a deleted target surveyed under --plan keeps its record.
func TestRunCommandPlanNeverPrunesEmptyWholeTree(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/empty\n\ngo 1.26.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "empty.go"), []byte("package empty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	evidence := func(symbol string) gomutant.SubjectEvidence {
		return gomutant.SubjectEvidence{Symbol: symbol, MaximalClosure: "closure", TestVariantClosure: "tv", Toolchain: "go", BuildConfig: "build",
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
	var output bytes.Buffer
	if err := runCommand(context.Background(), runOptions{dir: dir, findingsFile: defaultFindings, plan: true, output: &output}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "plan only: no baselines probed, no mutants executed, nothing persisted") {
		t.Fatalf("plan output = %q, want the plan line", output.String())
	}
	// A plan renders no zeroed run summary: a summary of zeros would
	// claim a measurement that never happened (REQ-exec-plan's render
	// clause in execution.md).
	if strings.Contains(output.String(), "summary   ") {
		t.Fatalf("plan output carries a run summary: %q", output.String())
	}
	retained, err := loadFindings(dir, path)
	if err != nil || len(retained) != 1 {
		t.Fatalf("plan-mode empty whole-tree pruned findings: %+v, %v", retained, err)
	}
}

// The cross-site shed happens at the incremental per-target commit
// merge - the final merge sees an already-stripped document - so the
// run surface must collect and print it from the commit phase; a
// silent drop here is the field bug's refusal disappearing from view
// (REQ-attest-survivor).
func TestRunCommandSurfacesCommitPhaseAttestationSheds(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS(filepath.Join("..", "engine", "testdata", "fixturemod"))); err != nil {
		t.Fatal(err)
	}
	twin := "package lib\n\nfunc Twin(a, b int) int {\n\tif a > b {\n\t\treturn a\n\t}\n\tif a > b {\n\t\treturn b\n\t}\n\treturn 0\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "lib", "twin.go"), []byte(twin), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lib", "twin_test.go"), []byte("package lib\n\nimport \"testing\"\n\nfunc TestTwin(t *testing.T) {\n\tTwin(1, 2)\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tr, err := gomutant.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	targets := []gomutant.Target{{Symbol: "example.com/fixture/lib.Twin", Oracle: []string{"example.com/fixture/lib.TestTwin"}}}
	first, err := tr.Run(context.Background(), targets, gomutant.Options{})
	if err != nil {
		t.Fatal(err)
	}
	rec := first[0]
	var attested *gomutant.Survivor
	for i, s := range rec.Survivors {
		if strings.HasPrefix(s.Position, "twin.go:4:") {
			attested = &rec.Survivors[i]
			break
		}
	}
	if attested == nil {
		t.Fatalf("no survivor at the first site: %+v", rec.Survivors)
	}
	if err := rec.Attest(attested.Position, attested.Operator, "boundary equivalent at the first site"); err != nil {
		t.Fatal(err)
	}
	path := findingsAt(dir, defaultFindings)
	if err := gomutant.UpdateDocument(path, func([]gomutant.Finding) ([]gomutant.Finding, error) {
		return []gomutant.Finding{rec}, nil
	}); err != nil {
		t.Fatal(err)
	}

	// Shift the same-shaped neighbor into the attested coordinates.
	shifted := strings.Replace(twin, "\tif a > b {\n\t\treturn a\n\t}\n", "", 1)
	if err := os.WriteFile(filepath.Join(dir, "lib", "twin.go"), []byte(shifted), 0o644); err != nil {
		t.Fatal(err)
	}
	targetsPath := filepath.Join(dir, "targets.json")
	if err := os.WriteFile(targetsPath, []byte(`{"targets":[{"symbol":"example.com/fixture/lib.Twin","oracle":["example.com/fixture/lib.TestTwin"]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := runCommand(context.Background(), runOptions{dir: dir, findingsFile: defaultFindings, targetsFile: targetsPath, output: &output}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "attestation shed: ") || !strings.Contains(output.String(), "site content changed") {
		t.Fatalf("commit-phase shed not surfaced:\n%s", output.String())
	}
}
