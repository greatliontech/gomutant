package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// gitModule writes a one-package module under git with one commit, then
// applies edits uncommitted so HEAD is the ref the delta is cut against.
func gitModule(t *testing.T, base, edited map[string]string) string {
	t.Helper()
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
	write := func(files map[string]string) {
		for name, content := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	write(base)
	git("init", "-q")
	git("config", "user.email", "t@example.invalid")
	git("config", "user.name", "t")
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	write(edited)
	return dir
}

// A changed-ref run cuts each result row's open survivors by the added
// lines on both faces — the structured row's deltaOpen list and the
// summary's delta object, the human row's on-delta count, per-survivor
// mark, and summary clause — and the findings faces re-cut the document
// under --changed (REQ-exec-run-status, REQ-result-inspection).
func TestRunAndFindingsCutSurvivorsByTheDelta(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test over a fixture module")
	}
	base := map[string]string{
		"go.mod":    "module example.com/dl\n\ngo 1.26.5\n",
		"p.go":      "package dl\n\nfunc Value(x int) int {\n\tif x > 10 {\n\t\treturn 1\n\t}\n\treturn 2\n}\n",
		"p_test.go": "package dl\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) {\n\tif Value(0) != 2 {\n\t\tt.Fatal(Value(0))\n\t}\n}\n",
	}
	// The edit inserts a guard at the top of the body: lines 4-6 are the delta.
	edited := map[string]string{
		"p.go": "package dl\n\nfunc Value(x int) int {\n\tif x < -10 {\n\t\treturn 3\n\t}\n\tif x > 10 {\n\t\treturn 1\n\t}\n\treturn 2\n}\n",
	}
	dir := gitModule(t, base, edited)
	docPath := filepath.Join(dir, "findings.json")
	var stream bytes.Buffer
	if err := runCommand(context.Background(), runOptions{dir: dir, changed: "HEAD", findingsFile: docPath, runID: "delta-run", jsonl: true, output: &stream}); err != nil {
		t.Fatal(err)
	}
	var result, summary map[string]any
	for _, line := range strings.Split(strings.TrimSpace(stream.String()), "\n") {
		var env map[string]any
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatalf("non-JSON line leaked: %q", line)
		}
		switch env["event"] {
		case "result":
			result = env
		case "summary":
			summary = env
		}
	}
	if result == nil || summary == nil {
		t.Fatalf("stream lacks a result or summary: %q", stream.String())
	}
	deltaOpen, _ := result["deltaOpen"].([]any)
	open, _ := result["open"].([]any)
	if len(open) == 0 || len(deltaOpen) == 0 || len(deltaOpen) >= len(open) {
		t.Fatalf("result row open %d, deltaOpen %d; want on-delta survivors as a strict subset of open: %v", len(open), len(deltaOpen), result)
	}
	for _, row := range deltaOpen {
		position := row.(map[string]any)["position"].(string)
		line := strings.Split(position, ":")[1]
		if line != "4" && line != "5" && line != "6" {
			t.Fatalf("on-delta survivor %s outside the added lines 4-6", position)
		}
	}
	delta, _ := summary["delta"].(map[string]any)
	if delta == nil || delta["ref"] != "HEAD" || int(delta["open"].(float64)) != len(deltaOpen) {
		t.Fatalf("summary delta = %v, want ref HEAD and %d open", delta, len(deltaOpen))
	}
	var human bytes.Buffer
	if err := runCommand(context.Background(), runOptions{dir: dir, changed: "HEAD", findingsFile: docPath, runID: "delta-two", output: &human}); err != nil {
		t.Fatal(err)
	}
	text := human.String()
	wantRow := " open (" + itoa(len(deltaOpen)) + " on the delta)"
	if !strings.Contains(text, wantRow) || strings.Count(text, "  [delta]") != len(deltaOpen) || !strings.Contains(text, "; "+itoa(len(deltaOpen))+" open on the delta of HEAD\n") {
		t.Fatalf("human face does not cut the row, mark the survivors, and state the summary clause: %q", text)
	}
	var findings bytes.Buffer
	if err := findingsCommand(context.Background(), findingsOptions{dir: dir, findingsFile: "findings.json", changed: "HEAD"}, &findings); err != nil {
		t.Fatal(err)
	}
	// The cut loads the tree but derives no freshness: the row's state
	// is the recorded one (REQ-result-inspection).
	if !strings.Contains(findings.String(), "recorded  example.com/dl.Value") || !strings.Contains(findings.String(), wantRow+", 0 attested") {
		t.Fatalf("findings human row does not carry the on-delta count under the recorded state: %q", findings.String())
	}
	var detail bytes.Buffer
	if err := findingsCommand(context.Background(), findingsOptions{dir: dir, findingsFile: "findings.json", changed: "HEAD", detail: true}, &detail); err != nil {
		t.Fatal(err)
	}
	if strings.Count(detail.String(), "  [delta]") != len(deltaOpen) || !strings.Contains(detail.String(), wantRow+", 0 attested") {
		t.Fatalf("findings detail face does not mark the on-delta survivors: %q", detail.String())
	}
	// A plan measures nothing and cuts nothing.
	var plan bytes.Buffer
	if err := runCommand(context.Background(), runOptions{dir: dir, changed: "HEAD", findingsFile: docPath, plan: true, output: &plan}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plan.String(), "on the delta") {
		t.Fatalf("a plan rendered a delta cut: %q", plan.String())
	}
	var doc bytes.Buffer
	if err := findingsCommand(context.Background(), findingsOptions{dir: dir, findingsFile: "findings.json", changed: "HEAD", json: true}, &doc); err != nil {
		t.Fatal(err)
	}
	var views []map[string]any
	if err := json.Unmarshal(doc.Bytes(), &views); err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0]["state"] != "recorded" || len(views[0]["deltaOpen"].([]any)) != len(deltaOpen) {
		t.Fatalf("findings JSON view deltaOpen = %v, want %d survivors", views[0]["deltaOpen"], len(deltaOpen))
	}
	var plain bytes.Buffer
	if err := findingsCommand(context.Background(), findingsOptions{dir: dir, findingsFile: "findings.json"}, &plain); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain.String(), "on the delta") {
		t.Fatalf("a findings inspection without a ref rendered a delta count: %q", plain.String())
	}
	// A reflow that leaves every body canonically unchanged targets
	// nothing on this face too: the ref's content must actually be
	// read for the comparison (REQ-target-changed).
	commit := exec.Command("git", "commit", "-q", "-am", "edited")
	commit.Dir = dir
	if out, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	src, err := os.ReadFile(filepath.Join(dir, "p.go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "p.go"), []byte(strings.Replace(string(src), "package dl\n", "package dl\n\n// reflowed\n\n", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	var reflowed bytes.Buffer
	if err := runCommand(context.Background(), runOptions{dir: dir, changed: "HEAD", findingsFile: "findings.json", output: &reflowed}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(reflowed.String(), "measure ") || strings.Contains(reflowed.String(), "candidates") {
		t.Fatalf("a reflow-only change measured a symbol on the CLI:\n%s", reflowed.String())
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
