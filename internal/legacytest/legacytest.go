// Package legacytest plants machine-local overlay entries written as
// documents of an older, unreadable version, for the face tests that
// pin how preserved legacy entries are named. It installs a complete
// machine-local record through the store to learn each entry's path,
// then rewrites the file as a version-behind document carrying an
// attested disposition.
package legacytest

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gomutant"
)

// Plant installs one legacy entry per version under the store for
// findingsPath, each for its own symbol, and returns the entry paths in
// the order of versions. XDG_CACHE_HOME must be isolated by the caller.
func Plant(t *testing.T, dir, findingsPath string, versions ...int) []string {
	t.Helper()
	store, err := gomutant.OpenStore(findingsPath, dir)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for i, version := range versions {
		symbol := fmt.Sprintf("example.com/empty.Legacy%d", i)
		before := entries(t)
		if err := store.Update(context.Background(), func(current []gomutant.Finding) ([]gomutant.Finding, error) {
			return append(append([]gomutant.Finding(nil), current...), record(symbol)), nil
		}); err != nil {
			t.Fatal(err)
		}
		var entry string
		for path := range entries(t) {
			if !before[path] {
				entry = path
			}
		}
		if entry == "" {
			t.Fatalf("no overlay entry installed for %s", symbol)
		}
		content := fmt.Sprintf(`{"version": %d, "findings": [{"symbol": %q, "attested": [{"position": "a.go:1:1", "operator": "x", "reason": "kept"}]}]}`, version, symbol)
		if err := os.WriteFile(entry, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, entry)
	}
	return paths
}

func entries(t *testing.T) map[string]bool {
	t.Helper()
	found := map[string]bool{}
	root := filepath.Join(os.Getenv("XDG_CACHE_HOME"), "gomutant", "repos")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".json") && filepath.Base(filepath.Dir(path)) == "findings" {
			found[path] = true
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return found
}

// record is a complete machine-local (dirty) finding.
func record(symbol string) gomutant.Finding {
	evidence := func(sym string) gomutant.SubjectEvidence {
		return gomutant.SubjectEvidence{Symbol: sym, MaximalClosure: "closure", TestVariantClosure: "tv", Toolchain: "go", BuildConfig: "build",
			ObservationAssertion: "caller assertion", ObservationStrategy: "proof/v1", ObservationSubjectPackage: "p",
			ObservationSubjectSymbol: sym, ObservationObservable: true, ObservationEvidence: "proof",
			RuntimeInputs: "manifest", RuntimeDigest: "digest"}
	}
	return gomutant.Finding{Symbol: symbol, BodyHash: "body", OperatorSet: "go/2", OracleTimeout: "1m0s", Dirty: true,
		CandidateCount: 1, Generated: 1, Mutants: 1,
		TargetEvidence: evidence(symbol),
		OracleEvidence: []gomutant.SubjectEvidence{evidence(symbol + "Test")},
		Operators:      []gomutant.OperatorSummary{{Operator: "zero return", Generated: 1, Survived: 1}},
		Survivors:      []gomutant.Survivor{{Position: "old.go:1:1", Operator: "zero return"}}}
}
