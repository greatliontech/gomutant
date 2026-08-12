package gomutant

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A fully committed tree whose oracle uses a temp directory stamps
// CLEAN: the oracle-scratch identities are swept before the stamp and
// recorded as missing, so they are exactly as recorded - digest-stable
// evidence vouches for them, and a nonexistent tool-owned scratch path
// is not git-visible drift (REQ-result-layers, REQ-exec-oracle-scratch).
// Field regression: the fail-closed unresolvable arm kept swept scratch
// identities for the dirty walk, marking every finding on a clean tree
// dirty and making repo-layer promotion unreachable.
func TestCleanTreeWithTempDirOracleStampsClean(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	root := t.TempDir()
	files := map[string]string{
		"go.mod":     "module example.com/clean\n\ngo 1.26.4\n",
		".gitignore": ".gomutant/\n",
		"p.go":       "package clean\n\nfunc F(x int) int {\n\tif x > 100 {\n\t\treturn x - 1\n\t}\n\treturn x\n}\n",
		"p_test.go":  "package clean\n\nimport (\n\t\"os\"\n\t\"path/filepath\"\n\t\"testing\"\n)\n\nfunc TestF(t *testing.T) {\n\td, err := os.MkdirTemp(\"\", \"clean-oracle-*\")\n\tif err != nil {\n\t\tt.Fatal(err)\n\t}\n\tdefer os.RemoveAll(d)\n\tf := filepath.Join(d, \"data\")\n\tif err := os.WriteFile(f, []byte(\"x\"), 0o644); err != nil {\n\t\tt.Fatal(err)\n\t}\n\tif _, err := os.ReadFile(f); err != nil {\n\t\tt.Fatal(err)\n\t}\n\tif F(5) != 5 {\n\t\tt.Fatal()\n\t}\n}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=gomutant", "GIT_AUTHOR_EMAIL=gomutant@example.invalid",
			"GIT_COMMITTER_NAME=gomutant", "GIT_COMMITTER_EMAIL=gomutant@example.invalid",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	runGit("init", "-q")
	runGit("add", "-A")
	runGit("commit", "-q", "-m", "base")

	tree, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	findings, err := tree.Run(context.Background(), []Target{{Symbol: "example.com/clean.F", Oracle: []string{"example.com/clean.TestF"}, OracleExplicit: true}}, Options{Budget: 1, OracleTimeout: 2 * time.Minute})
	if err != nil || len(findings) != 1 {
		t.Fatalf("measure = %+v, %v", findings, err)
	}
	if findings[0].Dirty {
		t.Fatal("a fully committed tree with a temp-dir-using oracle stamped dirty - repo-layer promotion is unreachable")
	}
	// The record lands in the findings DOCUMENT and attest can find it
	// (the field incident's downstream: rows silently absent, attest
	// failing "no finding").
	store, err := OpenStore(filepath.Join(root, ".gomutant", "findings.json"), root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(context.Background(), func(prior []Finding) ([]Finding, error) {
		merged, _ := MergeFindingsShedAgainst(prior, findings, nil)
		return merged, nil
	}); err != nil {
		t.Fatal(err)
	}
	committed, err := store.Load(context.Background())
	if err != nil || len(committed) != 1 || committed[0].Symbol != "example.com/clean.F" {
		t.Fatalf("document after commit = %+v, %v; want the measured record present", committed, err)
	}
	// The record stays machine-local through the pre-existing
	// bracket-uncoverable clause (the scratch-namespace declaration
	// surface is its own roadmap item) - but never through the
	// dirty-worktree clause, the regression's signature.
	_, reasons := store.LayerReasons(committed[0])
	for _, r := range reasons {
		if strings.Contains(r, "dirty worktree provenance") {
			t.Fatalf("clean tree carries the dirty-worktree clause: %q", reasons)
		}
	}

	// The measured record is attestable - the field incident's
	// downstream was attest failing "no finding" for symbols the
	// summary reported measured.
	if len(committed[0].Survivors) > 0 {
		if err := store.Update(context.Background(), func(all []Finding) ([]Finding, error) {
			for i := range all {
				if all[i].Symbol == "example.com/clean.F" {
					if err := all[i].Attest(committed[0].Survivors[0].Position, committed[0].Survivors[0].Operator, "equivalent by inspection"); err != nil {
						return nil, err
					}
					return all, nil
				}
			}
			return nil, fmt.Errorf("no finding for example.com/clean.F")
		}); err != nil {
			t.Fatalf("attest on a measured record: %v", err)
		}
	}
}
