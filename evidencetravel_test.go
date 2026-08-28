package gomutant

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh/runtimeinput"
)

// copyFixtureModule clones the fixture module into dst — a second
// checkout: identical content, a different root, fresh file times.
func copyFixtureModule(t *testing.T, dst string) {
	t.Helper()
	err := filepath.WalkDir(fixtureDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(fixtureDir, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, content, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// The chunk's driving pin: a findings document measured in one
// checkout serves in a second checkout of the same content. The
// oracle reads its module's own bytes at run time, so the record
// carries runtime-input evidence; that evidence persists
// module-relative with content-bound digests, so the clone's
// different root and fresh modification times are invisible to it
// (REQ-inputs-relative-identities, REQ-inputs-observation-class,
// REQ-result-stale — the field fault: a 415-record committed document
// once served only on the checkout that produced it).
func TestRunServesAcrossCheckoutRoots(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	ctx := context.Background()
	rootA := filepath.Join(t.TempDir(), "producer")
	rootB := filepath.Join(t.TempDir(), "clone")
	copyFixtureModule(t, rootA)
	copyFixtureModule(t, rootB)

	targets := []Target{{Symbol: "example.com/fixture/diskread.D", Oracle: []string{"example.com/fixture/diskread.TestDiskVerdict"}, OracleExplicit: true}}
	treeA, err := Load(rootA)
	if err != nil {
		t.Fatal(err)
	}
	first, err := treeA.Run(ctx, targets, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].Cached || first[0].Skipped != "" {
		t.Fatalf("producer measurement = %+v", first)
	}

	// The travel precondition, asserted directly: every persisted
	// manifest's tree-local identities are module-relative — they
	// materialize under the clone's root, not the producer's.
	treeLocal := 0
	for _, evidence := range append([]SubjectEvidence{first[0].TargetEvidence}, first[0].OracleEvidence...) {
		if evidence.RuntimeInputs == "" {
			continue
		}
		paths, err := runtimeinput.Paths(evidence.RuntimeInputs, rootB)
		if err != nil {
			t.Fatalf("%s: recorded manifest unreadable: %v", evidence.Symbol, err)
		}
		for _, p := range paths {
			if strings.HasPrefix(p, rootA+string(filepath.Separator)) {
				t.Fatalf("%s: persisted identity pinned to the producing checkout: %s", evidence.Symbol, p)
			}
			if strings.HasPrefix(p, rootB+string(filepath.Separator)) {
				treeLocal++
			}
		}
	}
	if treeLocal == 0 {
		t.Fatal("the fixture carried no tree-local runtime input - the travel precondition went vacuous")
	}

	treeB, err := Load(rootB)
	if err != nil {
		t.Fatal(err)
	}
	var decisions []RunDecision
	second, err := treeB.Run(ctx, targets, Options{Prior: first, Decision: func(decision RunDecision) {
		decisions = append(decisions, decision)
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || !second[0].Cached {
		t.Fatalf("clone run = %+v, want the producer's record served", second[0])
	}
	if len(decisions) != 1 || decisions[0].Action != "cached" {
		t.Fatalf("clone decisions = %+v, want one cached serve", decisions)
	}
}
