package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"
	"github.com/greatliontech/gomutant"
)

// The zero-target whole-tree reconcile states a demotion it made on
// the response — a standing committed record re-judged into the
// machine-local overlay once its exemption is withdrawn — as the
// promotion's twin (REQ-mcp-findings-doc).
func TestToolRunStatesADemotionOnTheZeroTargetReconcile(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/empty\n\ngo 1.26.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "empty.go"), []byte("package empty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oracle := gomutant.SubjectEvidence{Symbol: "example.com/empty.TestBoundary", Fingerprint: gofresh.Fingerprint{MaximalClosure: "closure", TestVariantClosure: "tv", ObservationAssertion: "caller assertion", RuntimeInputs: "eyJ2IjoxfQ", RuntimeDigest: "digest", Guards: guard.Guards{Toolchain: "go", BuildConfig: "build"}, ObservationProof: gofresh.ObservationProof{Strategy: "proof/v1", Subject: gofresh.Subject{Package: "p", Symbol: "example.com/empty.TestBoundary"}, Observable: true, Evidence: "proof"}, ResultKind: gofresh.CodeResult}, RuntimeUnverifiable: true, RuntimeReason: "sealed reason"}
	shaped := gomutant.Finding{Symbol: "example.com/empty.Boundary", BodyHash: "body", OperatorSet: "go/2", OracleTimeout: "1m0s", Commit: "abc",
		Shape:          &gomutant.TargetShape{Structural: &gomutant.StructuralSpec{Class: "import-boundary", Packages: []string{"p"}, Forbidden: "q"}},
		OracleEvidence: []gomutant.SubjectEvidence{oracle}}
	path := gomutant.FindingsPathAt(dir, "")
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
	_, out, err := New(dir).toolRun(context.Background(), nil, runIn{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Demoted != 1 || out.Promoted != 0 {
		t.Fatalf("zero-target reconcile demoted %d, promoted %d on the response; want 1 and 0", out.Demoted, out.Promoted)
	}
}
