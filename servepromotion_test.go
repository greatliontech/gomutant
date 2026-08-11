package gomutant

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// promoteThroughServe drives one dirty-born record through a serve at a
// clean tree and returns the served finding and the repo document rows:
// the shared harness for the growth and drift promotion-shape pins.
// setup returns the repo, its git runner, and the file whose
// uncommitted edit makes the first measure dirty-born.
func promoteThroughServe(t *testing.T, target Target, setup func(t *testing.T) (string, func(...string), string), moveOracle func(tmp string, runGit func(...string))) (Finding, []Finding, string) {
	t.Helper()
	cacheDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheDir)
	tmp, runGit, dirtyFile := setup(t)
	dirtyUncommittedComment(t, dirtyFile)

	docPath := filepath.Join(tmp, ".gomutant", "findings.json")
	store, err := OpenStore(docPath, tmp)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	commit := func(finding Finding) error {
		return store.Update(ctx, func(current []Finding) ([]Finding, error) {
			return MergeFindings(current, []Finding{finding}), nil
		})
	}

	tr, err := Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	first, err := tr.Run(ctx, []Target{target}, Options{Budget: 1, Commit: commit})
	if err != nil || len(first) != 1 {
		t.Fatalf("dirty measure = %+v, %v", first, err)
	}
	if !first[0].Dirty || len(first[0].Survivors) == 0 {
		t.Fatalf("dirty-born record = dirty %v with %d survivors", first[0].Dirty, len(first[0].Survivors))
	}
	s0 := first[0].Survivors[0]
	if err := store.Update(ctx, func(all []Finding) ([]Finding, error) {
		for i := range all {
			if all[i].Symbol == target.Symbol {
				return all, all[i].Attest(s0.Position, s0.Operator, "equivalent by inspection")
			}
		}
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}

	runGit("add", "-A")
	runGit("commit", "-q", "-m", "content lands")
	moveOracle(tmp, runGit)
	cleanHead := gitHead(t, tmp)

	prior, err := store.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tr2, err := Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	second, err := tr2.Run(ctx, []Target{target}, Options{Budget: 1, Prior: prior, Commit: commit})
	if err != nil || len(second) != 1 {
		t.Fatalf("clean serve = %+v, %v", second, err)
	}
	raw, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatalf("repo document missing after the clean serve: %v", err)
	}
	parsed, err := ParseFindings(raw)
	if err != nil {
		t.Fatal(err)
	}
	return second[0], parsed, cleanHead
}

func assertPromotedShape(t *testing.T, served Finding, parsed []Finding, cleanHead, symbol string, wantAttested int) {
	t.Helper()
	if served.Dirty || served.Commit != cleanHead {
		t.Fatalf("served provenance = commit %q dirty %v, want clean at %q", served.Commit, served.Dirty, cleanHead)
	}
	for _, f := range parsed {
		if f.Symbol != symbol {
			continue
		}
		if f.Dirty || f.Commit != cleanHead {
			t.Fatalf("repo-document row = commit %q dirty %v, want the re-stamped clean provenance", f.Commit, f.Dirty)
		}
		if len(f.Attested) != wantAttested {
			t.Fatalf("repo-document row carries %d dispositions, want %d riding the promotion", len(f.Attested), wantAttested)
		}
		return
	}
	t.Fatalf("dirty-born record did not promote to the repo document: %+v", parsed)
}

// growthModuleRepo synthesizes a committed single-module git repo whose
// derived oracle observes only module-local inputs.
func growthModuleRepo(t *testing.T) (string, func(...string), string) {
	t.Helper()
	tmp := t.TempDir()
	files := map[string]string{
		"go.mod":       "module example.com/grow\n\ngo 1.26.4\n",
		"p.go":         "package grow\n\nfunc F(x int) int {\n\tif x > 100 {\n\t\treturn x - 1\n\t}\n\treturn x\n}\n",
		"doc.go":       "// Package grow exists for the growth promotion pin.\npackage grow\n",
		"grow_test.go": "package grow\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) {\n\tif F(5) != 5 {\n\t\tt.Fatal()\n\t}\n}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(tmp, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = tmp
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
	return tmp, runGit, filepath.Join(tmp, "doc.go")
}

// A dirty-born record serves through the oracle-growth carve-out at a
// clean tree: provenance re-stamps like a fresh measure's and the
// record - its disposition riding - reaches the repo findings document
// (REQ-result-stale's serve-provenance clause, REQ-result-layers).
func TestGrowthServePromotesDirtyBornRecordOnCleanTree(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	// The fixture is a dedicated module whose derived oracle observes
	// only module-local inputs: the shared fixture's derived oracle
	// includes tests reading external absolute inputs, and a record
	// carrying those identities is machine-local by the portable line
	// and dirty by the out-of-repo guard - it can never promote, which
	// is exactly the identities' contract, not this pin's subject.
	served, parsed, cleanHead := promoteThroughServe(t,
		Target{Symbol: "example.com/grow.F"},
		growthModuleRepo,
		func(tmp string, runGit func(...string)) {
			// An added derived sibling test is the growth delta; committed,
			// so the serve happens at a clean tree.
			path := filepath.Join(tmp, "grow2_test.go")
			if err := os.WriteFile(path, []byte("package grow\n\nimport \"testing\"\n\nfunc TestF2(t *testing.T) {\n\tF(1)\n}\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			runGit("add", "-A")
			runGit("commit", "-q", "-m", "growth test lands")
		})
	assertPromotedShape(t, served, parsed, cleanHead, "example.com/grow.F", 1)
}

// A dirty-born record serves through the killer-drift carve-out at a
// clean tree with the same promotion shape (REQ-result-stale's
// serve-provenance clause, REQ-result-layers).
func TestDriftServePromotesDirtyBornRecordOnCleanTree(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	served, parsed, cleanHead := promoteThroughServe(t,
		Target{Symbol: "example.com/fixture/lib.Weak", Oracle: []string{"example.com/fixture/lib.TestWeak"}, OracleExplicit: true},
		func(t *testing.T) (string, func(...string), string) {
			tmp, runGit := gitFixtureRepo(t)
			return tmp, runGit, filepath.Join(tmp, "lib", "doc.go")
		},
		func(tmp string, runGit func(...string)) {
			// Editing the oracle test's body moves killer content: the
			// drift serve re-measures the flagged candidates against the
			// current oracle; committed, so the serve is at a clean tree.
			path := filepath.Join(tmp, "lib", "lib_test.go")
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			moved := strings.Replace(string(src), `t.Fatal("small arm")`, `t.Fatal("small arm moved")`, 1)
			if moved == string(src) {
				t.Fatal("TestWeak edit anchor missing")
			}
			if err := os.WriteFile(path, []byte(moved), 0o644); err != nil {
				t.Fatal(err)
			}
			runGit("add", "-A")
			runGit("commit", "-q", "-m", "oracle body lands")
		})
	// The drift splice re-executes the attested survivor against the
	// current oracle; it survives again at its exact site, so the
	// disposition rides the promotion.
	assertPromotedShape(t, served, parsed, cleanHead, "example.com/fixture/lib.Weak", 1)
}
