package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A targets-fed run reports each skip once - the decision line - and
// aggregates skip classes after the summary instead of repeating rows
// (REQ-exec-run-status's dedup arm).
func TestRunCommandReportsSkipsOnceWithClassSummary(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	targets := filepath.Join(t.TempDir(), "targets.json")
	doc := `{"targets":[{"symbol":"example.com/fixture/methods.Counter","oracle":["example.com/fixture/lib.TestAdd"]},{"symbol":"example.com/fixture/methods.Box","oracle":["example.com/fixture/lib.TestAdd"]}]}`
	if err := os.WriteFile(targets, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runCommand(context.Background(), runOptions{
		dir: fixtureDir, targetsFile: targets, findingsFile: filepath.Join(t.TempDir(), "findings.json"), output: &out,
	}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if got := strings.Count(text, "skipped   example.com/fixture/methods.Counter"); got != 1 {
		t.Fatalf("skip rows for the type symbol = %d, want exactly the decision line:\n%s", got, text)
	}
	if !strings.Contains(text, "2 x not a function - for mutation adequacy") {
		t.Fatalf("skip-class summary missing:\n%s", text)
	}

	// A single skip's decision line already said everything: no class
	// line at N=1.
	single := filepath.Join(t.TempDir(), "single.json")
	if err := os.WriteFile(single, []byte(`{"targets":[{"symbol":"example.com/fixture/methods.Counter","oracle":["example.com/fixture/lib.TestAdd"]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runCommand(context.Background(), runOptions{
		dir: fixtureDir, targetsFile: single, findingsFile: filepath.Join(t.TempDir(), "findings.json"), output: &out,
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "1 x not a function") {
		t.Fatalf("single skip repeated as a class line:\n%s", out.String())
	}
}

// An inline JSON document handed to --targets names the fix instead of
// a bare file-not-found.
func TestRunCommandNamesTheTargetsPathMistake(t *testing.T) {
	err := runCommand(context.Background(), runOptions{dir: fixtureDir, targetsFile: `{"targets":[]}`, findingsFile: "findings.json"})
	if err == nil || !strings.Contains(err.Error(), "looks like an inline JSON document") {
		t.Fatalf("inline-JSON --targets = %v, want the named mistake", err)
	}
}
