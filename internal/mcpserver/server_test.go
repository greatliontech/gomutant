package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	gomutant "github.com/greatliontech/gomutant"
)

const fixtureDir = "../engine/testdata/fixturemod"

// serverAt copies the fixture tree into a temp dir so findings documents and
// git state never touch the shared fixtures, and roots a server there.
func serverAt(t *testing.T) *Server {
	t.Helper()
	tmp := t.TempDir()
	if err := os.CopyFS(tmp, os.DirFS(fixtureDir)); err != nil {
		t.Fatal(err)
	}
	return New(tmp)
}

func seededFinding(symbol string) gomutant.Finding {
	evidence := func(name string) gomutant.SubjectEvidence {
		return gomutant.SubjectEvidence{Symbol: name, MaximalClosure: "closure", Toolchain: "go", BuildConfig: "build", RuntimeInputs: "manifest", RuntimeDigest: "digest"}
	}
	return gomutant.Finding{Symbol: symbol, BodyHash: "body", OperatorSet: "go/2", OracleTimeout: "1m0s", Dirty: true,
		CandidateCount: 0, Generated: 0,
		TargetEvidence: evidence(symbol), OracleEvidence: []gomutant.SubjectEvidence{evidence("example.com/empty.TestOld")}}
}

func TestToolTimeoutInputsNameIndependentLimits(t *testing.T) {
	s := serverAt(t)
	if _, _, err := s.toolRun(context.Background(), nil, runIn{TimeoutSec: -1, OracleTimeoutSec: 60}); err == nil || !strings.Contains(err.Error(), "timeout_sec") {
		t.Fatalf("negative run command timeout = %v", err)
	}
	if _, _, err := s.toolEphemeral(context.Background(), nil, ephemeralIn{TimeoutSec: -1, OracleTimeoutSec: 60}); err == nil || !strings.Contains(err.Error(), "timeout_sec") {
		t.Fatalf("negative ephemeral command timeout = %v", err)
	}
}

func TestToolRunCommandTimeoutLeavesFindingsUntouched(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":       "module example.com/slow\n\ngo 1.26.4\n",
		"slow.go":      "package slow\nfunc Value() int { return 1 }\n",
		"slow_test.go": "package slow\nimport (\"testing\"; \"time\")\nfunc TestValue(t *testing.T) { time.Sleep(2*time.Second); if Value() != 1 { t.Fail() } }\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	document, err := gomutant.Export(nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, defaultFindings)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, document, 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(dir)
	_, _, err = s.toolRun(context.Background(), nil, runIn{
		TargetsJSON: `{"targets":[{"symbol":"example.com/slow.Value","oracle":["example.com/slow.TestValue"]}]}`,
		Budget:      1, TimeoutSec: 1, OracleTimeoutSec: 10,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("tool command timeout = %v, want context.DeadlineExceeded", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(document) {
		t.Fatalf("timed-out tool changed findings: %v\n%s", err, got)
	}
}

func TestToolRunCommandTimeoutPreservesOrdinaryErrors(t *testing.T) {
	s := serverAt(t)
	_, _, err := s.toolRun(context.Background(), nil, runIn{TargetsJSON: `{`, TimeoutSec: 10})
	if err == nil || errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "parse targets document") {
		t.Fatalf("timed command parse error = %v", err)
	}
}

func TestToolRunWholeTreePrunesWhenNoTargetsRemain(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/empty\n\ngo 1.26.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "empty.go"), []byte("package empty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, defaultFindings)
	if err := gomutant.UpdateDocument(path, func([]gomutant.Finding) ([]gomutant.Finding, error) {
		return []gomutant.Finding{seededFinding("example.com/empty.Old")}, nil
	}); err != nil {
		t.Fatal(err)
	}
	s := New(dir)
	if _, out, err := s.toolRun(context.Background(), nil, runIn{TargetsJSON: `{"targets":[]}`}); err != nil {
		t.Fatal(err)
	} else if want := []gomutant.PreparationEvent{{Stage: gomutant.PreparationLoading}}; len(out.Preparation) != len(want) || out.Preparation[0] != want[0] {
		t.Fatalf("empty run preparation = %+v, want %+v", out.Preparation, want)
	}
	retained, err := s.loadFindings("")
	if err != nil || len(retained) != 1 {
		t.Fatalf("scoped zero-target run pruned findings: %+v, %v", retained, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := s.toolRun(ctx, nil, runIn{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled empty whole-tree run = %v", err)
	}
	retained, err = s.loadFindings("")
	if err != nil || len(retained) != 1 {
		t.Fatalf("cancelled empty whole-tree run changed findings: %+v, %v", retained, err)
	}
	if _, _, err := s.toolRun(context.Background(), nil, runIn{}); err != nil {
		t.Fatal(err)
	}
	got, err := s.loadFindings("")
	if err != nil || len(got) != 0 {
		t.Fatalf("whole-tree empty discovery retained findings: %+v, %v", got, err)
	}
}

func TestToolRunWholeTreePrunesAlongsideCurrentMeasurement(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test for one mutant")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/current\n\ngo 1.26.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "current.go"), []byte("package current\n\nfunc Value() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "current_test.go"), []byte("package current\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) { if Value() != 1 { t.Fatal(Value()) } }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(dir)
	path := filepath.Join(dir, defaultFindings)
	if err := gomutant.UpdateDocument(path, func([]gomutant.Finding) ([]gomutant.Finding, error) {
		return []gomutant.Finding{seededFinding("example.com/current.Deleted")}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.toolRun(context.Background(), nil, runIn{Budget: 1}); err != nil {
		t.Fatal(err)
	}
	got, err := s.loadFindings("")
	if err != nil || len(got) != 1 || got[0].Symbol != "example.com/current.Value" {
		t.Fatalf("whole-tree reconciliation = %+v, %v", got, err)
	}
}

// TestToolRunFindingsAttest drives the measuring loop end to end over the
// protocol handlers (REQ-mcp-tools, REQ-mcp-findings-doc): a stipulator-form
// inline document measures with labels riding through, the findings document
// lands on disk, the findings tool groups by label, attest closes a
// survivor, and a rerun serves from cache with the disposition intact.
func TestToolRunFindingsAttest(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	s := serverAt(t)
	ctx := context.Background()

	_, out, err := s.toolRun(ctx, nil, runIn{
		TargetsJSON:      `{"surfaces":[{"id":"0f78123e19ecd70d242eb3a9d66d61b39aef6a22ce29e5c58a692e66813aabd9","backend":"go","symbol":"example.com/fixture/lib.Weak","requirementIds":["REQ-weak"],"bindings":[{"backend":"go","role":"BINDING_ROLE_TESTS","symbol":"example.com/fixture/lib.TestWeak"}]}],"format":"stipulator.binding-surfaces/v1"}`,
		OracleTimeoutSec: 120,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Findings) != 1 || len(out.Findings[0].Open) == 0 || out.Findings[0].Labels[0] != "REQ-weak" || out.Summary.Targets != 1 || out.Document == "" {
		t.Fatalf("run = %+v", out)
	}
	if len(out.Findings[0].Operators) == 0 {
		t.Fatal("run omitted operator summaries")
	}
	if finding := out.Findings[0]; finding.CandidateCount < finding.Generated || finding.Generated != finding.Mutants+finding.Discarded || finding.Generated == 0 {
		t.Fatalf("run candidate accounting = %+v", finding)
	}
	if len(out.Decisions) != 1 || out.Decisions[0].Action != "measure" || out.Decisions[0].Candidates != out.Findings[0].Generated || out.Summary.Measured != 1 || out.Summary.Targets != 1 {
		t.Fatalf("run status = decisions %+v, summary %+v", out.Decisions, out.Summary)
	}
	var stages []string
	for _, event := range out.Preparation {
		stages = append(stages, string(event.Stage))
	}
	if got, want := strings.Join(stages, ","), "loading,resolving,freshness,mutants,baseline"; got != want {
		t.Fatalf("run preparation = %s, want %s: %+v", got, want, out.Preparation)
	}
	if _, err := os.Stat(filepath.Join(s.dir, defaultFindings)); err != nil {
		t.Fatalf("findings document not written: %v", err)
	}
	persisted, err := s.loadFindings("")
	if err != nil || len(persisted) != 1 || persisted[0].OracleTimeout != "2m0s" {
		t.Fatalf("oracle timeout pin = %+v, %v", persisted, err)
	}

	_, fOut, err := s.toolFindings(ctx, nil, findingsIn{})
	if err != nil {
		t.Fatal(err)
	}
	if len(fOut.Findings) != 1 || fOut.Findings[0].State != gomutant.FindingCurrent || len(fOut.Findings[0].Open) != len(out.Findings[0].Open) ||
		fOut.Findings[0].CandidateCount != out.Findings[0].CandidateCount || fOut.Findings[0].Generated != out.Findings[0].Generated ||
		fOut.Findings[0].Mutants != out.Findings[0].Mutants || fOut.Findings[0].Discarded != out.Findings[0].Discarded {
		t.Fatalf("findings = %+v", fOut.Findings)
	}
	_, filtered, err := s.toolFindings(ctx, nil, findingsIn{Label: "REQ-other"})
	if err != nil || filtered.Findings == nil || len(filtered.Findings) != 0 {
		t.Fatalf("filtered-empty findings = %+v, %v", filtered, err)
	}

	sv := fOut.Findings[0].Open[0]
	_, aOut, err := s.toolAttest(ctx, nil, attestIn{
		Symbol: fOut.Findings[0].Symbol, Position: sv.Position, Operator: sv.Operator, Reason: "equivalent by inspection",
	})
	if err != nil {
		t.Fatal(err)
	}
	if aOut.Open != len(fOut.Findings[0].Open)-1 {
		t.Fatalf("attest left %d open, want %d", aOut.Open, len(fOut.Findings[0].Open)-1)
	}
	if _, _, err := s.toolAttest(ctx, nil, attestIn{Symbol: fOut.Findings[0].Symbol, Position: "nowhere:1:1", Operator: "x", Reason: "r"}); err == nil {
		t.Fatal("attested a non-survivor")
	}

	_, out2, err := s.toolRun(ctx, nil, runIn{
		TargetsJSON:      `{"surfaces":[{"id":"88f70f1253489fdbb40ad694ce44c2a43e63678b133980bcd7773ea5f73302eb","backend":"go","symbol":"example.com/fixture/lib.Weak","requirementIds":["REQ-current"],"bindings":[{"backend":"go","role":"BINDING_ROLE_TESTS","symbol":"example.com/fixture/lib.TestWeak"}]}],"format":"stipulator.binding-surfaces/v1"}`,
		OracleTimeoutSec: 120,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out2.Findings[0].Cached || out2.Findings[0].Attested != 1 {
		t.Fatalf("rerun = %+v, want cached with the disposition intact", out2.Findings[0])
	}
	if !slices.Equal(out2.Findings[0].Labels, []string{
		"REQ-current",
		"stipulator:surface:88f70f1253489fdbb40ad694ce44c2a43e63678b133980bcd7773ea5f73302eb",
	}) {
		t.Fatalf("cached finding labels = %v, want current surface labels", out2.Findings[0].Labels)
	}
	if len(out2.Decisions) != 1 || out2.Decisions[0].Action != "cached" || out2.Summary.Cached != 1 {
		t.Fatalf("cached run status = decisions %+v, summary %+v", out2.Decisions, out2.Summary)
	}
	stages = stages[:0]
	for _, event := range out2.Preparation {
		stages = append(stages, string(event.Stage))
	}
	if got, want := strings.Join(stages, ","), "loading,resolving,freshness"; got != want {
		t.Fatalf("cached preparation = %s, want %s: %+v", got, want, out2.Preparation)
	}

	// A scoped run of a different symbol never drops the rest of the
	// document (REQ-mcp-findings-doc).
	if _, _, err := s.toolRun(ctx, nil, runIn{
		TargetsJSON: `{"targets":[{"symbol":"example.com/fixture/lib.Add","oracle":["example.com/fixture/lib.TestAdd"]}]}`,
	}); err != nil {
		t.Fatal(err)
	}
	all, err := s.loadFindings("")
	if err != nil {
		t.Fatal(err)
	}
	syms := map[string]bool{}
	for _, f := range all {
		syms[f.Symbol] = true
	}
	if !syms["example.com/fixture/lib.Weak"] || !syms["example.com/fixture/lib.Add"] {
		t.Fatalf("scoped run dropped document entries: %v", syms)
	}
	if _, _, err := s.toolRun(ctx, nil, runIn{Symbols: []string{"example.com/fixture/lib.Add"}, Budget: 1}); err != nil {
		t.Fatal(err)
	}
	all, err = s.loadFindings("")
	if err != nil {
		t.Fatal(err)
	}
	syms = map[string]bool{}
	for _, finding := range all {
		syms[finding.Symbol] = true
	}
	if !syms["example.com/fixture/lib.Weak"] || !syms["example.com/fixture/lib.Add"] {
		t.Fatalf("filtered whole-tree run dropped document entries: %v", syms)
	}
}

func TestToolCandidateDiscardAccounting(t *testing.T) {
	if testing.Short() {
		t.Skip("runs an oracle baseline")
	}
	s := serverAt(t)
	_, out, err := s.toolRun(context.Background(), nil, runIn{
		TargetsJSON: `{"targets":[{"symbol":"example.com/fixture/lib.BigLit","oracle":["example.com/fixture/lib.TestAdd"]}]}`,
		Budget:      1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Findings) != 1 || out.Findings[0].CandidateCount < 1 || out.Findings[0].Generated != 1 || out.Findings[0].Mutants != 0 || out.Findings[0].Discarded != 1 ||
		len(out.Decisions) != 1 || out.Decisions[0].Candidates != 1 {
		t.Fatalf("run discard accounting = %+v", out)
	}
	_, inspected, err := s.toolFindings(context.Background(), nil, findingsIn{})
	if err != nil {
		t.Fatal(err)
	}
	if len(inspected.Findings) != 1 || inspected.Findings[0].CandidateCount != out.Findings[0].CandidateCount || inspected.Findings[0].Generated != 1 ||
		inspected.Findings[0].Mutants != 0 || inspected.Findings[0].Discarded != 1 {
		t.Fatalf("inspection discard accounting = %+v", inspected.Findings)
	}
}

// TestToolEphemeralEdits pins the agent's patch form (REQ-mcp-ephemeral-edits):
// an edit-stated mutation measures identically to a whole replacement, and
// the exactly-one-form rule is enforced.
func TestToolEphemeralEdits(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	s := serverAt(t)
	ctx := context.Background()

	_, res, err := s.toolEphemeral(ctx, nil, ephemeralIn{
		File:    "lib/lib.go",
		Edits:   []gomutant.Edit{{Old: "return a + b", New: "return a + b + 1"}},
		TestPkg: "example.com/fixture/lib",
		Run:     "^TestAdd$",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Killed || res.Killer != "example.com/fixture/lib.TestAdd" {
		t.Fatalf("edits mutant = %+v, want killed by TestAdd", res)
	}
	_, res, err = s.toolEphemeral(ctx, nil, ephemeralIn{
		BatchEdits: []gomutant.BatchEdit{
			{File: "lib/lib.go", OldString: "return a + b", NewString: "return a + b + manualDelta()"},
			{File: "lib/doc.go", OldString: "package lib", NewString: "package lib\n\nfunc manualDelta() int { return 1 }"},
		},
		TestPkg: "example.com/fixture/lib",
		Run:     "^TestAdd$",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Killed || len(res.Files) != 2 || res.Files[0] != "lib/doc.go" || res.Files[1] != "lib/lib.go" {
		t.Fatalf("batch mutant = %+v, want two-file attributed kill", res)
	}
	if _, _, err := s.toolEphemeral(ctx, nil, ephemeralIn{File: "lib/lib.go", TestPkg: "p", Run: "^T$"}); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("neither form refused: %v", err)
	}
	if _, _, err := s.toolEphemeral(ctx, nil, ephemeralIn{File: "lib/lib.go", Replacement: "x", Edits: []gomutant.Edit{{Old: "a", New: "b"}}, TestPkg: "p", Run: "^T$"}); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("both forms refused: %v", err)
	}
	if _, _, err := s.toolEphemeral(ctx, nil, ephemeralIn{File: "lib/lib.go", BatchEdits: []gomutant.BatchEdit{{File: "lib/lib.go", OldString: "a", NewString: "b"}}, TestPkg: "p", Run: "^T$"}); err == nil || !strings.Contains(err.Error(), "omit file") {
		t.Fatalf("batch with top-level file accepted: %v", err)
	}
	// The surface is dir-bound: a file escaping the tree would no-op in the
	// overlay and read as a survivor — refused instead.
	if _, _, err := s.toolEphemeral(ctx, nil, ephemeralIn{File: "../outside.go", Replacement: "x", TestPkg: "p", Run: "^T$"}); err == nil || !strings.Contains(err.Error(), "escapes the tree") {
		t.Fatalf("escaping file accepted: %v", err)
	}
	if _, _, err := s.toolEphemeral(ctx, nil, ephemeralIn{BatchEdits: []gomutant.BatchEdit{{File: "../outside.go", OldString: "a", NewString: "b"}}, TestPkg: "p", Run: "^T$"}); err == nil || !strings.Contains(err.Error(), "escapes the tree") {
		t.Fatalf("escaping batch file accepted: %v", err)
	}
	for _, file := range []string{"/outside.go", "C:/outside.go", `C:\outside.go`, `\\server\share.go`} {
		if _, _, err := s.toolEphemeral(ctx, nil, ephemeralIn{BatchEdits: []gomutant.BatchEdit{{File: file, OldString: "a", NewString: "b"}}, TestPkg: "p", Run: "^T$"}); err == nil || !strings.Contains(err.Error(), "escapes the tree") {
			t.Fatalf("platform-absolute batch file %q accepted: %v", file, err)
		}
	}
}

// TestToolDiscover pins discovery over the handler: the whole tree targets
// every declared body and no test symbol.
func TestToolDiscover(t *testing.T) {
	s := serverAt(t)
	_, out, err := s.toolDiscover(context.Background(), nil, discoverIn{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, tg := range out.Targets {
		got[tg.Symbol] = true
		if tg.OracleSet < 0 || tg.OracleSet >= len(out.OracleSets) {
			t.Fatalf("target oracle-set reference = %+v in %+v", tg, out.OracleSets)
		}
		oracle := out.OracleSets[tg.OracleSet].Oracle
		if tg.Symbol == "example.com/fixture/lib.Add" && (tg.OracleExplicit || len(oracle) == 0) {
			t.Fatalf("Add description = %+v", tg)
		}
	}
	if !got["example.com/fixture/lib.Add"] || got["example.com/fixture/lib.TestAdd"] {
		t.Fatalf("discover = %d targets, Add=%v TestAdd=%v", len(out.Targets), got["example.com/fixture/lib.Add"], got["example.com/fixture/lib.TestAdd"])
	}
	_, explicit, err := s.toolDiscover(context.Background(), nil, discoverIn{TargetsJSON: `{"targets":[{"symbol":"example.com/fixture/lib.Add","oracle":["example.com/fixture/lib.TestWeak","example.com/fixture/lib.TestAdd"],"labels":["z","a"]}]}`})
	if err != nil {
		t.Fatal(err)
	}
	if len(explicit.Targets) != 1 || len(explicit.OracleSets) != 1 || !explicit.Targets[0].OracleExplicit ||
		explicit.OracleSets[explicit.Targets[0].OracleSet].Oracle[0] != "example.com/fixture/lib.TestAdd" || explicit.Targets[0].Labels[0] != "a" {
		t.Fatalf("explicit discover = %+v", explicit)
	}
	_, stipulatorEmpty, err := s.toolDiscover(context.Background(), nil, discoverIn{TargetsJSON: `{"surfaces":[{"id":"7e1693c30271ffb09fdb6d8a42d84fe07ab2a7c51a7c1d1232caebe220fb6885","backend":"go","symbol":"example.com/fixture/lib.Add","requirementIds":["REQ-empty"],"bindings":[]}],"format":"stipulator.binding-surfaces/v1"}`})
	if err != nil {
		t.Fatal(err)
	}
	if len(stipulatorEmpty.Targets) != 1 || !stipulatorEmpty.Targets[0].OracleExplicit ||
		stipulatorEmpty.Targets[0].Skipped != "no oracle" ||
		len(stipulatorEmpty.OracleSets[stipulatorEmpty.Targets[0].OracleSet].Oracle) != 0 ||
		stipulatorEmpty.Targets[0].Labels[0] != "REQ-empty" {
		t.Fatalf("Stipulator explicit-empty discover = %+v", stipulatorEmpty)
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.OracleSets) >= len(out.Targets) || len(encoded) >= 100_000 {
		t.Fatalf("discovery was not compact: %d oracle sets, %d targets, %d bytes", len(out.OracleSets), len(out.Targets), len(encoded))
	}
	if _, _, err := s.toolDiscover(context.Background(), nil, discoverIn{TargetsJSON: `{"targets":[]}`, Changed: "HEAD"}); err == nil {
		t.Fatal("multiple discovery forms accepted")
	}
	for _, in := range []runIn{
		{TargetsPath: "targets.json", TargetsJSON: `{"targets":[]}`},
		{TargetsPath: "targets.json", Changed: "HEAD"},
		{TargetsJSON: `{"targets":[]}`, Changed: "HEAD"},
		{TargetsPath: "targets.json", TargetsJSON: `{"targets":[]}`, Changed: "HEAD"},
	} {
		if _, _, err := s.toolRun(context.Background(), nil, in); err == nil || !strings.Contains(err.Error(), "at most one") {
			t.Fatalf("run accepted multiple target forms %+v: %v", in, err)
		}
	}
	if _, _, err := s.toolRun(context.Background(), nil, runIn{TargetsJSON: `{"targets":[]}`, Findings: filepath.ToSlash(filepath.Join(t.TempDir(), "findings.json"))}); err != nil {
		t.Fatalf("run refused one target form: %v", err)
	}
	if _, _, err := s.toolRun(context.Background(), nil, runIn{
		TargetsJSON: `{"targets":[{"symbol":"example.com/fixture/lib.Add","oracle":[],"oracleExplicit":true}]}`,
		Symbols:     []string{"example.com/fixture/lib.Absent"},
	}); err == nil || !strings.Contains(err.Error(), "matched no targets") {
		t.Fatalf("run ignored symbol filter: %v", err)
	} else if !strings.Contains(err.Error(), "* stays within one slash component") {
		t.Fatalf("run filter error lacks pattern guidance: %v", err)
	} else if !strings.Contains(err.Error(), "** as a complete component crosses slash components") || !strings.Contains(err.Error(), "**/*emitConditions*") {
		t.Fatalf("run filter error lacks corrective globstar guidance: %v", err)
	}
	if _, _, err := New(filepath.Join(t.TempDir(), "missing")).toolRun(context.Background(), nil, runIn{}); err == nil {
		t.Fatal("run accepted an invalid tree")
	}
	_, filtered, err := s.toolDiscover(context.Background(), nil, discoverIn{
		Packages: []string{"example.com/fixture/methods"}, Symbols: []string{"example.com/fixture/methods.Counter.*"},
	})
	if err != nil || len(filtered.Targets) != 2 || filtered.Targets[0].Symbol != "example.com/fixture/methods.Counter.Inc" || filtered.Targets[1].Symbol != "example.com/fixture/methods.Counter.Value" {
		t.Fatalf("filtered discovery = %+v, %v", filtered, err)
	}
}

func TestCompactTargetDescriptionsDeduplicatesExactOracles(t *testing.T) {
	descriptions := []gomutant.TargetDescription{
		{Symbol: "p.A", Oracle: []string{"ab", "c"}},
		{Symbol: "p.B", Oracle: []string{"ab", "c"}, Labels: []string{"x"}},
		{Symbol: "p.C", Oracle: []string{"a", "bc"}, OracleExplicit: true},
		{Symbol: "p.D", Oracle: []string{}, OracleExplicit: true, Skipped: "no oracle"},
	}
	sets, targets := compactTargetDescriptions(descriptions)
	if len(sets) != 3 || len(targets) != 4 || targets[0].OracleSet != 0 || targets[1].OracleSet != 0 || targets[2].OracleSet != 1 || targets[3].OracleSet != 2 ||
		sets[0].ID != 0 || !slices.Equal(sets[0].Oracle, []string{"ab", "c"}) || sets[1].ID != 1 || !slices.Equal(sets[1].Oracle, []string{"a", "bc"}) ||
		sets[2].ID != 2 || len(sets[2].Oracle) != 0 {
		t.Fatalf("compact descriptions = sets %+v, targets %+v", sets, targets)
	}
	expanded := make([]gomutant.TargetDescription, len(targets))
	for i, target := range targets {
		expanded[i] = gomutant.TargetDescription{
			Symbol: target.Symbol, Oracle: sets[target.OracleSet].Oracle, Labels: target.Labels,
			OracleExplicit: target.OracleExplicit, Skipped: target.Skipped,
		}
	}
	if !reflect.DeepEqual(expanded, descriptions) {
		t.Fatalf("expanded descriptions = %+v, want %+v", expanded, descriptions)
	}
}

func TestDiscoverSchemaExplainsOracleReferences(t *testing.T) {
	for _, field := range []struct {
		typeOf  reflect.Type
		name    string
		phrases []string
	}{
		{reflect.TypeOf(discoverTarget{}), "OracleSet", []string{"references", "oracleSets", "id"}},
		{reflect.TypeOf(discoverOracleSet{}), "ID", []string{"referenced", "targets[].oracleSet"}},
		{reflect.TypeOf(discoverOut{}), "OracleSets", []string{"first-target", "oracle sets"}},
	} {
		structField, ok := field.typeOf.FieldByName(field.name)
		description := structField.Tag.Get("jsonschema")
		if !ok {
			t.Fatalf("%s.%s schema does not explain oracle references", field.typeOf.Name(), field.name)
		}
		for _, phrase := range field.phrases {
			if !strings.Contains(description, phrase) {
				t.Errorf("%s.%s schema %q lacks %q", field.typeOf.Name(), field.name, description, phrase)
			}
		}
	}
	for _, typeOf := range []reflect.Type{reflect.TypeOf(runIn{}), reflect.TypeOf(discoverIn{})} {
		field, ok := typeOf.FieldByName("Symbols")
		description := field.Tag.Get("jsonschema")
		if !ok || !strings.Contains(description, "** as a complete component crosses slash components") || !strings.Contains(description, "**/*emitConditions*") {
			t.Errorf("%s.Symbols schema lacks corrective globstar guidance: %q", typeOf.Name(), description)
		}
	}
}

func TestToolRunPropagatesUpdateFailure(t *testing.T) {
	s := serverAt(t)
	want := errors.New("update failed")
	s.updateDocument = func(context.Context, string, func([]gomutant.Finding) ([]gomutant.Finding, error)) error { return want }
	_, _, err := s.toolRun(context.Background(), nil, runIn{
		TargetsJSON: `{"targets":[{"symbol":"example.com/fixture/lib.Add","oracle":[],"oracleExplicit":true}]}`,
	})
	if !errors.Is(err, want) {
		t.Fatalf("update failure = %v, want %v", err, want)
	}
}

func TestToolRunCancellationAtUpdateLeavesDocumentUntouched(t *testing.T) {
	s := serverAt(t)
	ctx, cancel := context.WithCancel(context.Background())
	called := false
	s.updateDocument = func(_ context.Context, _ string, change func([]gomutant.Finding) ([]gomutant.Finding, error)) error {
		called = true
		cancel()
		_, err := change(nil)
		return err
	}
	_, _, err := s.toolRun(ctx, nil, runIn{
		TargetsJSON: `{"targets":[{"symbol":"example.com/fixture/lib.Add","oracle":[],"oracleExplicit":true}]}`,
	})
	if !called || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation at update = called %v, error %v", called, err)
	}
}

// TestMCPBuilds pins that the tool schemas derive cleanly: building the
// protocol server panics on an invalid schema, so construction is the test.
func TestMCPBuilds(t *testing.T) {
	if srv := New(".").MCP(); srv == nil {
		t.Fatal("no server")
	}
}
