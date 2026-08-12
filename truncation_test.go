package gomutant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A run whose pipeline ends early with its error lost must fail loudly
// naming the unfinished targets - a truncated campaign reported as
// complete is indistinguishable from success unless the operator diffs
// the findings document against the roster (REQ-exec-completion). The
// targets finished before the truncation keep their committed results.
func TestTruncatedPipelineFailsLoudlyNamingUnfinishedTargets(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":    "module example.com/trunc\n\ngo 1.26.4\n",
		"p.go":      "package trunc\n\nfunc A(x int) int {\n\tif x > 100 {\n\t\treturn x - 1\n\t}\n\treturn x\n}\n\nfunc B(x int) int {\n\tif x > 100 {\n\t\treturn x - 1\n\t}\n\treturn x\n}\n",
		"p_test.go": "package trunc\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {\n\tif A(5) != 5 {\n\t\tt.Fatal()\n\t}\n}\n\nfunc TestB(t *testing.T) {\n\tif B(5) != 5 {\n\t\tt.Fatal()\n\t}\n}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tree, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	runTruncateAfterItems = 1
	t.Cleanup(func() { runTruncateAfterItems = 0 })
	targets := []Target{
		{Symbol: "example.com/trunc.A", Oracle: []string{"example.com/trunc.TestA"}, OracleExplicit: true},
		{Symbol: "example.com/trunc.B", Oracle: []string{"example.com/trunc.TestB"}, OracleExplicit: true},
	}
	var committedSyms []string
	_, err = tree.Run(context.Background(), targets, Options{Budget: 1, OracleTimeout: 2 * time.Minute, Commit: func(f Finding) error {
		committedSyms = append(committedSyms, f.Symbol)
		return nil
	}})
	if err == nil || !strings.Contains(err.Error(), "campaign truncated") || !strings.Contains(err.Error(), "example.com/trunc.B") {
		t.Fatalf("truncated run = %v, want the loud truncation naming the unfinished target", err)
	}
	// The target finished before the truncation kept its incremental
	// commit, exactly as the error promises.
	if len(committedSyms) != 1 || committedSyms[0] != "example.com/trunc.A" {
		t.Fatalf("commits before truncation = %q, want the finished target's", committedSyms)
	}
}
