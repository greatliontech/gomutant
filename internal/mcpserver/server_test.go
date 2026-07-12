package mcpserver

import (
	"context"
	"os"
	"path/filepath"
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
	return gomutant.Finding{Symbol: symbol, BodyHash: "body", OperatorSet: "go/2", Timeout: "1m0s", Dirty: true,
		TargetEvidence: evidence(symbol), OracleEvidence: []gomutant.SubjectEvidence{evidence("example.com/empty.TestOld")}}
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
	if _, _, err := s.toolRun(context.Background(), nil, runIn{TargetsJSON: `{"targets":[]}`}); err != nil {
		t.Fatal(err)
	}
	retained, err := s.loadFindings("")
	if err != nil || len(retained) != 1 {
		t.Fatalf("scoped zero-target run pruned findings: %+v, %v", retained, err)
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
		TargetsJSON: `{"stipulatorTargets":1,"targets":[{"symbol":"example.com/fixture/lib.Weak","witnesses":["example.com/fixture/lib.TestWeak"],"requirements":["REQ-weak"]}]}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Findings) != 1 || len(out.Findings[0].Open) == 0 || out.Findings[0].Labels[0] != "REQ-weak" {
		t.Fatalf("run = %+v", out)
	}
	if _, err := os.Stat(filepath.Join(s.dir, defaultFindings)); err != nil {
		t.Fatalf("findings document not written: %v", err)
	}

	_, fOut, err := s.toolFindings(ctx, nil, findingsIn{})
	if err != nil {
		t.Fatal(err)
	}
	if len(fOut.Findings) != 1 || fOut.Findings[0].State != gomutant.FindingCurrent || len(fOut.Findings[0].Open) != len(out.Findings[0].Open) {
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
		TargetsJSON: `{"stipulatorTargets":1,"targets":[{"symbol":"example.com/fixture/lib.Weak","witnesses":["example.com/fixture/lib.TestWeak"],"requirements":["REQ-weak"]}]}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out2.Findings[0].Cached || out2.Findings[0].Attested != 1 {
		t.Fatalf("rerun = %+v, want cached with the disposition intact", out2.Findings[0])
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
	}
	if !got["example.com/fixture/lib.Add"] || got["example.com/fixture/lib.TestAdd"] {
		t.Fatalf("discover = %d targets, Add=%v TestAdd=%v", len(out.Targets), got["example.com/fixture/lib.Add"], got["example.com/fixture/lib.TestAdd"])
	}
}

// TestMCPBuilds pins that the tool schemas derive cleanly: building the
// protocol server panics on an invalid schema, so construction is the test.
func TestMCPBuilds(t *testing.T) {
	if srv := New(".").MCP(); srv == nil {
		t.Fatal("no server")
	}
}
