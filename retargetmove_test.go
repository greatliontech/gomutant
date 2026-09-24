package gomutant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A package move re-measures its records: a retarget rewrites identity
// only, and the package closure gofresh digests is salted with the
// subject identity, which names the import path, so a moved package's
// stored evidence proves nothing about the moved code — the record
// judges stale until it is re-measured (REQ-result-lifecycle).
// Measured here on a real tree: a campaign, the package directory
// moved with its package clause and file names kept — the import path
// the one thing that changes — its record retargeted, the inspection.
func TestRetargetAfterAPackageMoveJudgesTheRecordStale(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test over a fixture module")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	write := func(files map[string]string) {
		for name, content := range files {
			path := filepath.Join(dir, name)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	source := func(dir string) map[string]string {
		return map[string]string{
			"go.mod":           "module example.com/mv\n\ngo 1.26\n",
			dir + "/a.go":      "package a\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n",
			dir + "/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal()\n\t}\n}\n",
		}
	}
	write(source("a"))
	ctx := context.Background()
	tree, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(filepath.Join(dir, ".gomutant", "findings.json"), dir)
	if err != nil {
		t.Fatal(err)
	}
	ledger := NewRunLedger(store, nil, "run-1", false)
	findings, err := tree.Run(ctx, []Target{{Symbol: "example.com/mv/a.Add"}}, Options{Budget: 1, Jobs: 1, Commit: ledger.Commit(ctx), Layer: ledger.Layer})
	if err != nil || len(findings) != 1 || findings[0].Skipped != "" {
		t.Fatalf("campaign: %+v, %v", findings, err)
	}
	// Measured on the tree it was made on, the record is current.
	before, err := tree.InspectFindings(ctx, findings, nil)
	if err != nil || before[0].State != FindingCurrent {
		t.Fatalf("before the move: %+v, %v", before, err)
	}
	// The move: the directory alone, the files and their package clause
	// byte for byte the same under a new import path.
	if err := os.RemoveAll(filepath.Join(dir, "a")); err != nil {
		t.Fatal(err)
	}
	write(source("b"))
	moved, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	result, err := moved.RetargetContext(ctx, store, "example.com/mv/a.", "example.com/mv/b.", false)
	if err != nil || len(result.Rewritten) != 1 {
		t.Fatalf("retarget: %+v, %v", result, err)
	}
	after, err := store.Load(ctx)
	if err != nil || len(after) != 1 || after[0].Symbol != "example.com/mv/b.Add" {
		t.Fatalf("after the retarget: %+v, %v", after, err)
	}
	inspections, err := moved.InspectFindings(ctx, after, nil)
	if err != nil {
		t.Fatal(err)
	}
	if inspections[0].State != FindingStale {
		t.Fatalf("the moved record judges %s (%s), want stale: the closure identity names the import path", inspections[0].State, inspections[0].Reason)
	}
	if !strings.Contains(inspections[0].Reason, "closure") && !strings.Contains(inspections[0].Reason, "identity") {
		t.Fatalf("stale reason %q names neither the closure nor the identity", inspections[0].Reason)
	}
}
