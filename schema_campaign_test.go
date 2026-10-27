package gomutant

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/greatliontech/gomutant/internal/engine"
)

func TestCampaignWindowMergesPackageSchemasAndCommitsVerifiableFindings(t *testing.T) {
	if testing.Short() {
		t.Skip("runs campaign execution")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/schemacampaign\n\ngo 1.26.4\n",
		"a.go": `package schemacampaign

func Add(x, y int) int {
	return x + y
}

func Sub(x, y int) int {
	return x - y
}
`,
		"b.go": `package schemacampaign

func Mul(x, y int) int {
	return x * y
}
`,
		"pkg_test.go": `package schemacampaign

import (
	"os"
	"testing"
)

func TestAll(t *testing.T) {
	if got := os.Getenv("GOMUTANT_ACTIVE_MUTANT"); got != "" {
		t.Fatalf("GOMUTANT_ACTIVE_MUTANT leaked into test: %q", got)
	}
	if Add(2, 3) != 5 {
		t.Fatal("Add(2, 3) != 5")
	}
	if Sub(7, 4) != 3 {
		t.Fatal("Sub(7, 4) != 3")
	}
	if Mul(3, 5) != 15 {
		t.Fatal("Mul(3, 5) != 15")
	}
}
`,
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
	targets := []Target{
		{Symbol: "example.com/schemacampaign.Add", Oracle: []string{"example.com/schemacampaign.TestAll"}, OracleExplicit: true},
		{Symbol: "example.com/schemacampaign.Sub", Oracle: []string{"example.com/schemacampaign.TestAll"}, OracleExplicit: true},
		{Symbol: "example.com/schemacampaign.Mul", Oracle: []string{"example.com/schemacampaign.TestAll"}, OracleExplicit: true},
	}
	for _, tSym := range targets {
		ms, err := tree.eng.Mutants(tSym.Symbol, 0)
		if err != nil || len(ms) == 0 {
			t.Fatalf("Mutants(%s) = %d, %v", tSym.Symbol, len(ms), err)
		}
		schemaCount := 0
		for _, m := range ms {
			if m.Operator == "statement: delete" {
				if m.SchemaMutantID != "" {
					t.Fatalf("uncompilable mutant %s %s should be filtered from schema, got id=%q",
						m.Symbol, m.Position, m.SchemaMutantID)
				}
				continue
			}
			if m.SchemaMutantID == "" || m.SchemaKey == "" || len(m.SchemaReplacements) == 0 {
				t.Fatalf("mutant %s %s (%s) missing schema metadata: id=%q key=%q repls=%d",
					m.Symbol, m.Position, m.Operator, m.SchemaMutantID, m.SchemaKey, len(m.SchemaReplacements))
			}
			schemaCount++
		}
		if schemaCount == 0 {
			t.Fatalf("expected schema-eligible mutants for %s", tSym.Symbol)
		}
	}

	var (
		mu              sync.Mutex
		compileCount    int
		nonCompileOvCnt int
	)
	restore := engine.ObserveGoCommandsForTest(func(cmd *exec.Cmd) {
		args := cmd.Args[1:]
		if len(args) == 0 || args[0] != "test" || !slices.Contains(args, "-overlay") {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if slices.Contains(args, "-c") {
			compileCount++
		} else {
			nonCompileOvCnt++
		}
	})
	defer restore()

	findings, err := tree.Run(context.Background(), targets, Options{
		Jobs:          2,
		OracleTimeout: time.Minute,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	mu.Lock()
	gotCompile, gotFallback := compileCount, nonCompileOvCnt
	mu.Unlock()
	if gotCompile != 1 || gotFallback != 3 {
		t.Fatalf("go test -c -overlay count = %d (want 1 coalesced package binary), fallback go test -overlay count = %d (want 3 for the 3 filtered uncompilable mutants)",
			gotCompile, gotFallback)
	}
	if len(findings) != 3 {
		t.Fatalf("len(findings) = %d, want 3", len(findings))
	}
	for _, f := range findings {
		if f.Skipped != "" {
			t.Fatalf("finding %s skipped: %s", f.Symbol, f.Skipped)
		}
		if f.Killed == 0 || f.Killed != f.Mutants {
			t.Fatalf("finding %s: Killed=%d Mutants=%d, want all mutants killed", f.Symbol, f.Killed, f.Mutants)
		}
		if f.TargetEvidence.RuntimeUnverifiable {
			t.Fatalf("finding %s TargetEvidence.RuntimeUnverifiable = true: %+v", f.Symbol, f.TargetEvidence)
		}
		if len(f.OracleEvidence) != 1 || f.OracleEvidence[0].RuntimeUnverifiable {
			t.Fatalf("finding %s OracleEvidence unverifiable: %+v", f.Symbol, f.OracleEvidence)
		}
	}
}
