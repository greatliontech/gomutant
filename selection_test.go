package gomutant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeSelectionFixture(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":            "module example.com/sel\n\ngo 1.26\n",
		"pkg/f.go":          "package pkg\n\nfunc F(x int) int {\n\tif x > 100 {\n\t\treturn x - 1\n\t}\n\treturn x + 1\n}\n",
		"pkg/gated.go":      "//go:build seltag\n\npackage pkg\n\nfunc Gated(x int) int { return x + 2 }\n",
		"pkg/gated_test.go": "//go:build seltag\n\npackage pkg\n\nimport \"testing\"\n\nfunc TestGatedOracle(t *testing.T) {\n\tif F(1) != 2 {\n\t\tt.Fatal()\n\t}\n\tif F(101) != 100 {\n\t\tt.Fatal()\n\t}\n\tif Gated(1) != 3 {\n\t\tt.Fatal()\n\t}\n}\n",
	} {
		path := filepath.Join(tmp, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return tmp
}

// A declared build selection makes a tag-gated symbol and its tag-gated
// oracle visible end to end — discovered, resolvable, runnable, and
// measured — exactly as untagged ones, while the selection-less load
// keeps today's boundary: the gated arm does not exist
// (REQ-target-selection).
func TestSelectionMakesTagGatedOracleMeasurable(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tmp := writeSelectionFixture(t)

	discovered := func(tree *Tree) map[string]bool {
		t.Helper()
		targets, err := tree.DiscoverContext(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, target := range targets {
			out[target.Symbol] = true
		}
		return out
	}

	plain, err := Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if syms := discovered(plain); syms["example.com/sel/pkg.Gated"] {
		t.Fatal("selection-less load discovered the tag-gated symbol")
	}

	selected, err := LoadContextSelection(context.Background(), tmp, Selection{Tags: []string{"seltag"}})
	if err != nil {
		t.Fatal(err)
	}
	syms := discovered(selected)
	if !syms["example.com/sel/pkg.Gated"] {
		t.Fatal("selection did not surface the tag-gated symbol")
	}

	// The gated oracle measures a mutant of the UNTAGGED symbol: the
	// selection reaches the oracle spawn, not just discovery — a spawn
	// reading the ambient environment would compile the test binary
	// without the gated file and the oracle could never run.
	targets := []Target{{Symbol: "example.com/sel/pkg.F", Oracle: []string{"example.com/sel/pkg.TestGatedOracle"}, OracleExplicit: true}}
	findings, err := selected.Run(context.Background(), targets, Options{OracleTimeout: 2 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Skipped != "" {
		t.Fatalf("tag-gated oracle did not measure: %+v", findings)
	}
	if findings[0].Mutants == 0 || findings[0].Killed == 0 {
		t.Fatalf("tag-gated oracle produced no kill evidence: %+v", findings[0])
	}

	// The same target under the selection-less tree refuses: the oracle
	// does not exist there, and the refusal names oracle validation,
	// never a silent pass.
	plainFindings, err := plain.Run(context.Background(), targets, Options{OracleTimeout: 2 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if len(plainFindings) != 1 || !strings.Contains(plainFindings[0].Skipped, "oracle validation failed") {
		t.Fatalf("selection-less run did not refuse the gated oracle: %+v", plainFindings)
	}

	// The measurement pins carry the selection: the same selection
	// serves the prior finding, a different selection re-measures —
	// pins captured under an ambient environment would serve across
	// selections, the forbidden cross-selection verdict.
	reloaded, err := LoadContextSelection(context.Background(), tmp, Selection{Tags: []string{"seltag"}})
	if err != nil {
		t.Fatal(err)
	}
	served, err := reloaded.Run(context.Background(), targets, Options{OracleTimeout: 2 * time.Minute, Prior: findings})
	if err != nil {
		t.Fatal(err)
	}
	if len(served) != 1 || !served[0].Cached {
		t.Fatalf("same selection did not serve the prior finding: %+v", served)
	}
	widened, err := LoadContextSelection(context.Background(), tmp, Selection{Tags: []string{"seltag", "othertag"}})
	if err != nil {
		t.Fatal(err)
	}
	remeasured, err := widened.Run(context.Background(), targets, Options{OracleTimeout: 2 * time.Minute, Prior: findings})
	if err != nil {
		t.Fatal(err)
	}
	if len(remeasured) != 1 || remeasured[0].Cached || remeasured[0].Mutants == 0 {
		t.Fatalf("a different selection served across: %+v", remeasured)
	}
}
