package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gomutant"
)

// A run names its identity first on the human face and as a note on
// the structured stream, carries it in the structured summary, and
// stamps it on the result rows it measured (REQ-exec-run-status).
func TestRunCommandNamesItsIdentityAndStampsItsRows(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test over a fixture module")
	}
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":       "module example.com/jp\n\ngo 1.26.5\n",
		"p.go":         "package jp\nfunc Value() int { return 1 }\n",
		"p_test.go":    "package jp\nimport \"testing\"\nfunc TestValue(t *testing.T) { if Value() != 1 { t.Fail() } }\n",
		"targets.json": `{"targets":[{"symbol":"example.com/jp.Value","oracle":["example.com/jp.TestValue"]}]}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	docPath := filepath.Join(dir, "findings.json")
	var human bytes.Buffer
	if err := runCommand(context.Background(), runOptions{dir: dir, targetsFile: filepath.Join(dir, "targets.json"), findingsFile: docPath, budget: 1, runID: "run-one", output: &human}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(human.String(), "run       run-one\n") {
		t.Fatalf("human face did not lead with the run identity: %q", human.String())
	}
	var stream bytes.Buffer
	if err := runCommand(context.Background(), runOptions{dir: dir, targetsFile: filepath.Join(dir, "targets.json"), findingsFile: docPath, budget: 1, runID: "run-two", jsonl: true, output: &stream}); err != nil {
		t.Fatal(err)
	}
	var note, summary, result map[string]any
	for _, line := range strings.Split(strings.TrimSpace(stream.String()), "\n") {
		var env map[string]any
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatalf("non-JSON line leaked: %q", line)
		}
		switch env["event"] {
		case "note":
			if note == nil {
				note = env
			}
		case "summary":
			summary = env
		case "result":
			result = env
		}
	}
	if note == nil || note["text"] != "run       run-two" {
		t.Fatalf("structured stream's first note = %v, want the run identity", note)
	}
	if summary == nil || summary["run"] != "run-two" {
		t.Fatalf("structured summary = %v, want run-two", summary)
	}
	// The second run served the first's record: the row names the
	// measuring run, not the serving one.
	if result == nil || result["cached"] != true || result["run"] != "run-one" {
		t.Fatalf("served result row = %v, want cached with run-one", result)
	}
	// The human cached row names the measuring run the same way.
	var third bytes.Buffer
	if err := runCommand(context.Background(), runOptions{dir: dir, targetsFile: filepath.Join(dir, "targets.json"), findingsFile: docPath, budget: 1, runID: "run-three", output: &third}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(third.String(), "cached    example.com/jp.Value  ") || !strings.Contains(third.String(), " open  [run run-one]\n") {
		t.Fatalf("human cached row does not name the measuring run: %q", third.String())
	}
}

// The findings faces scope to one run's measured set and name each
// record's run beside its counts (REQ-result-inspection).
func TestFindingsCommandFiltersByRun(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/empty\n\ngo 1.26.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "empty.go"), []byte("package empty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	one := seededLocalFinding("example.com/empty.One")
	one.Run = "run-one"
	two := seededLocalFinding("example.com/empty.Two")
	two.Run = "run-two"
	old := seededLocalFinding("example.com/empty.Old")
	if err := gomutant.UpdateDocument(context.Background(), gomutant.FindingsPathAt(dir, defaultFindings), func([]gomutant.Finding) ([]gomutant.Finding, error) {
		return []gomutant.Finding{one, two, old}, nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var all bytes.Buffer
	if err := findingsCommand(ctx, findingsOptions{dir: dir, findingsFile: defaultFindings}, &all); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(all.String(), "example.com/empty.One  [machine-local]  1 open, 0 attested  [run run-one]\n") ||
		!strings.Contains(all.String(), "example.com/empty.Old  [machine-local]  1 open, 0 attested\n") {
		t.Fatalf("summary rows do not name the run (and only when recorded): %q", all.String())
	}
	var filtered bytes.Buffer
	if err := findingsCommand(ctx, findingsOptions{dir: dir, findingsFile: defaultFindings, run: "run-two"}, &filtered); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(filtered.String(), "example.com/empty.Two") || strings.Contains(filtered.String(), "empty.One") || strings.Contains(filtered.String(), "empty.Old") {
		t.Fatalf("run filter did not scope to run-two: %q", filtered.String())
	}
	var doc bytes.Buffer
	if err := findingsCommand(ctx, findingsOptions{dir: dir, findingsFile: defaultFindings, run: "run-one", json: true}, &doc); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(doc.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["run"] != "run-one" {
		t.Fatalf("JSON view under the run filter = %v, want the one run-one record carrying run", rows)
	}
}
