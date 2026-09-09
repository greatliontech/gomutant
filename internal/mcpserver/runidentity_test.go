package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/greatliontech/gomutant"
)

// The findings tool scopes to one run's measured set and names each
// record's run on its summary row; the run tool reports its identity
// in the summary and on its rows (REQ-result-inspection,
// REQ-exec-run-status).
func TestToolsCarryTheRunIdentity(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/empty\n\ngo 1.26.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "empty.go"), []byte("package empty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	one := seededFinding("example.com/empty.One")
	one.Run = "run-one"
	two := seededFinding("example.com/empty.Two")
	two.Run = "run-two"
	if err := gomutant.UpdateDocument(filepath.Join(dir, gomutant.DefaultFindingsPath), func([]gomutant.Finding) ([]gomutant.Finding, error) {
		return []gomutant.Finding{one, two, seededFinding("example.com/empty.Old")}, nil
	}); err != nil {
		t.Fatal(err)
	}
	s := New(dir)
	_, all, err := s.toolFindings(context.Background(), nil, findingsIn{})
	if err != nil {
		t.Fatal(err)
	}
	runs := map[string]string{}
	for _, row := range all.Summary {
		runs[row.Symbol] = row.Run
	}
	if runs["example.com/empty.One"] != "run-one" || runs["example.com/empty.Old"] != "" {
		t.Fatalf("summary rows' runs = %v, want run-one on One and none on Old", runs)
	}
	_, filtered, err := s.toolFindings(context.Background(), nil, findingsIn{Run: "run-two", Detail: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered.Findings) != 1 || filtered.Findings[0].Symbol != "example.com/empty.Two" || filtered.Findings[0].Run != "run-two" {
		t.Fatalf("run filter = %+v, want the run-two record alone", filtered.Findings)
	}
	_, run, err := s.toolRun(context.Background(), nil, runIn{TargetsJSON: `{"targets":[]}`})
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Summary.Run) != 16 {
		t.Fatalf("run summary identity = %q, want a minted 16-character identity", run.Summary.Run)
	}
}

// A measuring MCP run reports its identity in the summary and stamps
// its rows with it; a rerun serves with the measuring run's identity on
// the row and its own in the summary (REQ-exec-run-status).
func TestToolRunReportsAndStampsItsIdentity(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test for one mutant")
	}
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
	s := New(dir)
	_, first, err := s.toolRun(context.Background(), nil, runIn{Budget: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Summary.Run) != 16 || len(first.Findings) != 1 || first.Findings[0].Run != first.Summary.Run {
		t.Fatalf("measuring run summary run %q, rows %+v; want the rows stamped with the summary's identity", first.Summary.Run, first.Findings)
	}
	_, second, err := s.toolRun(context.Background(), nil, runIn{Budget: 1})
	if err != nil {
		t.Fatal(err)
	}
	if second.Summary.Run == first.Summary.Run || len(second.Findings) != 1 || !second.Findings[0].Cached || second.Findings[0].Run != first.Summary.Run {
		t.Fatalf("serving run summary run %q, rows %+v; want a fresh identity and the row keeping %s", second.Summary.Run, second.Findings, first.Summary.Run)
	}
}
