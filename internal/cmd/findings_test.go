package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gomutant "github.com/greatliontech/gomutant"
)

// The findings verb's tail states the document's coverage bounds per
// declared selection after the rows (REQ-result-unreached-bound).
func TestFindingsTailStatesTheDocumentCoverageBounds(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/empty\n\ngo 1.26.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "empty.go"), []byte("package empty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, defaultFindings)
	store, err := gomutant.OpenStore(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	store.RecordCoverageBound(gomutant.CoverageBound{Selection: "tags:wasm", Run: "r1", Unreached: []string{"example.com/empty/leg.Dark"}})
	if err := store.Update(context.Background(), func([]gomutant.Finding) ([]gomutant.Finding, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := findingsCommand(context.Background(), findingsOptions{dir: dir, findingsFile: defaultFindings}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no findings\nunreached under selection tags:wasm: 1 target no oracle of the selection's leg reaches — example.com/empty/leg.Dark\n") {
		t.Fatalf("findings tail = %q, want the bound after the rows", out.String())
	}
}

// Under a declared selection the plan's human face states the bound
// and persists nothing; a whole-tree run records the bound in the
// document; a scoped run states its bound and records none, the
// standing row untouched (REQ-result-unreached-bound, REQ-exec-plan).
func TestRunRecordsTheBoundForWholeTreeRunsOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":        "module example.com/sel\n\ngo 1.26\n",
		"pkg/f.go":      "package pkg\n\nfunc F(x int) int {\n\tif x > 100 {\n\t\treturn x - 1\n\t}\n\treturn x + 1\n}\n",
		"pkg/f_test.go": "package pkg\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) {\n\tif F(1) != 2 {\n\t\tt.Fatal()\n\t}\n\tif F(101) != 100 {\n\t\tt.Fatal()\n\t}\n}\n",
		"leg/dark.go":   "//go:build seltag\n\npackage leg\n\nfunc Dark(x int) int { return x * 2 }\n",
		"leg/dark2.go":  "//go:build seltag\n\npackage leg\n\nfunc Darker(x int) int { return x * 3 }\n",
		".gitignore":    ".gomutant/\n",
	} {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bounds := func() []gomutant.CoverageBound {
		t.Helper()
		store, err := gomutant.OpenStore(filepath.Join(dir, defaultFindings), dir)
		if err != nil {
			t.Fatal(err)
		}
		out, err := store.CoverageBounds(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	var plan bytes.Buffer
	if err := runCommand(context.Background(), runOptions{dir: dir, findingsFile: defaultFindings, plan: true, tags: []string{"seltag"}, output: &plan}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.String(), "unreached under selection tags:seltag: 2 targets no oracle of the selection's leg reaches — example.com/sel/leg.Dark, example.com/sel/leg.Darker") {
		t.Fatalf("plan face = %q, want the bound stated", plan.String())
	}
	if got := bounds(); len(got) != 0 {
		t.Fatalf("a plan persisted a bound: %+v", got)
	}
	var whole bytes.Buffer
	if err := runCommand(context.Background(), runOptions{dir: dir, findingsFile: defaultFindings, tags: []string{"seltag"}, output: &whole, runID: "r-whole"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(whole.String(), "unreached under selection tags:seltag: 2 targets") {
		t.Fatalf("whole-tree face = %q", whole.String())
	}
	if got := bounds(); len(got) != 1 || got[0].Selection != "tags:seltag" || got[0].Run != "r-whole" || len(got[0].Unreached) != 2 {
		t.Fatalf("whole-tree run recorded %+v, want the two-symbol bound", got)
	}
	// A scoped run over the leg alone states its bound and records none.
	var scoped bytes.Buffer
	if err := runCommand(context.Background(), runOptions{dir: dir, findingsFile: defaultFindings, tags: []string{"seltag"}, packages: []string{"example.com/sel/leg"}, output: &scoped, runID: "r-scoped"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(scoped.String(), "unreached under selection tags:seltag: 2 targets") {
		t.Fatalf("scoped face = %q, want the bound stated", scoped.String())
	}
	if got := bounds(); len(got) != 1 || got[0].Run != "r-whole" {
		t.Fatalf("a scoped run touched the row: %+v", got)
	}
	// A whole-tree run whose leg gained an oracle clears the row.
	if err := os.WriteFile(filepath.Join(dir, "leg", "dark_test.go"), []byte("//go:build seltag\n\npackage leg\n\nimport \"testing\"\n\nfunc TestDark(t *testing.T) {\n\tif Dark(2) != 4 || Darker(2) != 6 {\n\t\tt.Fatal()\n\t}\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var cleared bytes.Buffer
	if err := runCommand(context.Background(), runOptions{dir: dir, findingsFile: defaultFindings, tags: []string{"seltag"}, output: &cleared, runID: "r-clear"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cleared.String(), "unreached under") {
		t.Fatalf("a reached leg still stated a bound: %q", cleared.String())
	}
	if got := bounds(); len(got) != 0 {
		t.Fatalf("the clearing run left the row: %+v", got)
	}
}

// A whole-tree run that discovers no targets is still a whole-tree run
// of its selection: its reconcile clears the selection's standing row
// (REQ-result-unreached-bound).
func TestZeroTargetWholeTreeRunClearsTheBound(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":     "module example.com/bare\n\ngo 1.26\n",
		"bare.go":    "package bare\n\ntype T struct{}\n",
		".gitignore": ".gomutant/\n",
	} {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, defaultFindings)
	store, err := gomutant.OpenStore(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	store.RecordCoverageBound(gomutant.CoverageBound{Selection: "tags:seltag", Run: "r-old", Unreached: []string{"example.com/bare/leg.Gone"}})
	if err := store.Update(context.Background(), func([]gomutant.Finding) ([]gomutant.Finding, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runCommand(context.Background(), runOptions{dir: dir, findingsFile: defaultFindings, tags: []string{"seltag"}, output: &out, runID: "r-zero"}); err != nil {
		t.Fatal(err)
	}
	after, err := gomutant.OpenStore(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	if bounds, err := after.CoverageBounds(context.Background()); err != nil || len(bounds) != 0 {
		t.Fatalf("zero-target whole-tree run left the row: %+v, %v", bounds, err)
	}
}
