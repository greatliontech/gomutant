package mcpserver

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gomutant"
)

// The run tool cuts each row's open survivors by the delta on a
// changed-ref run and reports the cut in its summary; the findings tool
// re-cuts the document under changed (REQ-exec-run-status,
// REQ-result-inspection, REQ-mcp-envelope).
func TestToolsCutSurvivorsByTheDelta(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test over a fixture module")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/dl\n\ngo 1.26.5\n")
	write("p.go", "package dl\n\nfunc Value(x int) int {\n\tif x > 10 {\n\t\treturn 1\n\t}\n\treturn 2\n}\n")
	write("p_test.go", "package dl\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) {\n\tif Value(0) != 2 {\n\t\tt.Fatal(Value(0))\n\t}\n}\n")
	git("init", "-q")
	git("config", "user.email", "t@example.invalid")
	git("config", "user.name", "t")
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	write("p.go", "package dl\n\nfunc Value(x int) int {\n\tif x < -10 {\n\t\treturn 3\n\t}\n\tif x > 10 {\n\t\treturn 1\n\t}\n\treturn 2\n}\n")
	s := New(dir)
	_, run, err := s.toolRun(context.Background(), nil, runIn{Changed: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if len(run.Findings) != 1 || len(run.Findings[0].DeltaOpen) == 0 || len(run.Findings[0].DeltaOpen) >= len(run.Findings[0].Open) {
		t.Fatalf("run rows = %+v; want on-delta survivors as a strict subset of open", run.Findings)
	}
	for _, sv := range run.Findings[0].DeltaOpen {
		line := strings.Split(sv.Position, ":")[1]
		if line != "4" && line != "5" && line != "6" {
			t.Fatalf("on-delta survivor %s outside the added lines 4-6", sv.Position)
		}
	}
	if run.Summary.Delta == nil || run.Summary.Delta.Ref != "HEAD" || run.Summary.Delta.Open != len(run.Findings[0].DeltaOpen) {
		t.Fatalf("run summary delta = %+v, want ref HEAD and %d open", run.Summary.Delta, len(run.Findings[0].DeltaOpen))
	}
	_, findings, err := s.toolFindings(context.Background(), nil, findingsIn{Changed: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings.Summary) != 1 || findings.Summary[0].DeltaOpen == nil || *findings.Summary[0].DeltaOpen != run.Summary.Delta.Open {
		t.Fatalf("findings summary rows = %+v, want the same on-delta count", findings.Summary)
	}
	// The cut loads the tree but derives no freshness (REQ-result-inspection).
	if findings.Summary[0].State != gomutant.FindingRecorded {
		t.Fatalf("findings state under changed = %q, want recorded", findings.Summary[0].State)
	}
	_, detail, err := s.toolFindings(context.Background(), nil, findingsIn{Changed: "HEAD", Detail: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Findings) != 1 || len(detail.Findings[0].DeltaOpen) != run.Summary.Delta.Open {
		t.Fatalf("findings detail rows = %+v, want the on-delta survivors listed", detail.Findings)
	}
	_, plain, err := s.toolFindings(context.Background(), nil, findingsIn{})
	if err != nil {
		t.Fatal(err)
	}
	if plain.Summary[0].DeltaOpen != nil {
		t.Fatal("an inspection without a ref carried a delta count")
	}
	_, whole, err := s.toolRun(context.Background(), nil, runIn{})
	if err != nil {
		t.Fatal(err)
	}
	if whole.Summary.Delta != nil || len(whole.Findings[0].DeltaOpen) != 0 {
		t.Fatalf("a whole-tree run carried a delta cut: %+v", whole.Summary.Delta)
	}
}

// The on-delta list caps at the open bound with its remainder counted,
// and the summary total counts every row's survivors whole
// (REQ-mcp-envelope).
func TestCapRunFindingsCapsTheDeltaList(t *testing.T) {
	f := seededFinding("example.com/empty.Many")
	var many []gomutant.Survivor
	for i := 0; i < envelope.open+5; i++ {
		many = append(many, gomutant.Survivor{Position: "m.go:1:" + string(rune('a'+i%26)), Operator: "op"})
	}
	// One record past the row cap carries its own on-delta survivor: the
	// summary total counts it though no row lists it.
	findings := []gomutant.Finding{f}
	for i := 0; i < envelope.rows; i++ {
		findings = append(findings, seededFinding(fmt.Sprintf("example.com/empty.Row%02d", i)))
	}
	rows, omitted, deltaOpen, err := capRunFindings(findings, func(gomutant.Finding) (string, string) { return "repo", "" }, func(g gomutant.Finding) ([]gomutant.Survivor, error) {
		if g.Symbol == f.Symbol {
			return many, nil
		}
		if g.Symbol == fmt.Sprintf("example.com/empty.Row%02d", envelope.rows-1) {
			return []gomutant.Survivor{{Position: "last.go:1:1", Operator: "op"}}, nil
		}
		return nil, nil
	})
	if err != nil || omitted != 1 {
		t.Fatal(err, omitted)
	}
	if len(rows[0].DeltaOpen) != envelope.open || rows[0].OmittedDeltaOpen != 5 || deltaOpen != envelope.open+6 {
		t.Fatalf("delta list = %d rows, %d omitted, total %d; want %d, 5, %d (the capped row's survivor counted)", len(rows[0].DeltaOpen), rows[0].OmittedDeltaOpen, deltaOpen, envelope.open, envelope.open+6)
	}
}
