package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"
	"github.com/greatliontech/gomutant"
)

// The zero-target whole-tree reconcile states a demotion it made: the
// write re-judges the shaped records it keeps, and an exemption
// withdrawn since the last run moves one out of the committed document
// into the machine-local overlay — the promotion's twin
// (REQ-mcp-findings-doc).
func TestRunCommandStatesADemotionOnTheZeroTargetReconcile(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/empty\n\ngo 1.26.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "empty.go"), []byte("package empty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oracle := gomutant.SubjectEvidence{Symbol: "example.com/empty.TestBoundary", Fingerprint: gofresh.Fingerprint{MaximalClosure: "closure", TestVariantClosure: "tv", ObservationAssertion: "caller assertion", RuntimeInputs: "eyJ2IjoyfQ", RuntimeDigest: "digest", Guards: guard.Guards{Toolchain: "go", BuildConfig: "build"}, ObservationProof: gofresh.ObservationProof{Strategy: "proof/v1", Subject: gofresh.Subject{Package: "p", Symbol: "example.com/empty.TestBoundary"}, Observable: true, Evidence: "proof"}, ResultKind: gofresh.CodeResult}, RuntimeUnverifiable: true, RuntimeReason: "sealed reason"}
	shaped := gomutant.Finding{Symbol: "example.com/empty.Boundary", BodyHash: "body", OperatorSet: "go/2", OracleTimeout: "1m0s", Commit: "abc",
		Shape:          &gomutant.TargetShape{Structural: &gomutant.StructuralSpec{Class: "import-boundary", Packages: []string{"p"}, Forbidden: "q"}},
		OracleEvidence: []gomutant.SubjectEvidence{oracle}}
	path := gomutant.FindingsPathAt(dir, defaultFindings)
	// The exemption lands first, so the seeding write commits the record
	// to the repo document; the exemption is then withdrawn.
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	record := `{"version":1,"exemptions":[{"subject":"example.com/empty.TestBoundary","reason":"sealed reason","rationale":"reviewed"}]}`
	if err := os.WriteFile(gomutant.ExemptionsPathFor(path), []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}
	seed, err := gomutant.OpenStore(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	routing, err := seed.Update(context.Background(), func([]gomutant.Finding) ([]gomutant.Finding, error) { return []gomutant.Finding{shaped}, nil })
	if err != nil {
		t.Fatal(err)
	}
	if routing[shaped.Symbol].Layer != gomutant.LayerRepo {
		t.Fatalf("seeded record routed %+v, want repo under the exemption", routing[shaped.Symbol])
	}
	if err := os.Remove(gomutant.ExemptionsPathFor(path)); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := runCommand(context.Background(), runOptions{dir: dir, findingsFile: defaultFindings, output: &output}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "1 record(s) demoted to the machine-local overlay - findings document changed, commit it\n") {
		t.Fatalf("zero-target reconcile left its demotion unstated:\n%s", output.String())
	}
	if strings.Contains(output.String(), "promoted") {
		t.Fatalf("a demotion read as a promotion:\n%s", output.String())
	}
}
