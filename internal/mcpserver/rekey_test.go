package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"
	"github.com/greatliontech/gomutant"
)

// The structured face lists the exemption entries the run's first
// committing write re-keyed to the module-relative spelling, the
// record persisted and the subject still covered (REQ-result-exemptions).
func TestToolRunReKeysTheExemptionRecordAndListsIt(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/empty\n\ngo 1.26.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "empty.go"), []byte("package empty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oracle := gomutant.SubjectEvidence{Symbol: "example.com/empty.TestBoundary", Fingerprint: gofresh.Fingerprint{MaximalClosure: "closure", TestVariantClosure: "tv", ObservationAssertion: "caller assertion", RuntimeInputs: "eyJ2IjoyfQ", RuntimeDigest: "digest", Guards: guard.Guards{Toolchain: "go", BuildConfig: "build"}, ObservationProof: gofresh.ObservationProof{Strategy: "proof/v1", Subject: gofresh.Subject{Package: "p", Symbol: "example.com/empty.TestBoundary"}, Observable: true, Evidence: "proof"}, ResultKind: gofresh.CodeResult}, RuntimeUnverifiable: true, RuntimeReason: "external directory input: escape"}
	shaped := gomutant.Finding{Symbol: "example.com/empty.Boundary", BodyHash: "body", OperatorSet: "go/2", OracleTimeout: "1m0s", Commit: "abc",
		Shape:          &gomutant.TargetShape{Structural: &gomutant.StructuralSpec{Class: "import-boundary", Packages: []string{"p"}, Forbidden: "q"}},
		OracleEvidence: []gomutant.SubjectEvidence{oracle}}
	path := gomutant.FindingsPathAt(dir, "")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	current := `{"version":1,"exemptions":[{"subject":"example.com/empty.TestBoundary","reason":"external directory input: escape","rationale":"reviewed"}]}`
	if err := os.WriteFile(gomutant.ExemptionsPathFor(path), []byte(current), 0o644); err != nil {
		t.Fatal(err)
	}
	seed, err := gomutant.OpenStore(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	if layer, reason := seed.Layer(shaped); layer != gomutant.LayerRepo {
		t.Fatalf("seed layer=%s: %s", layer, reason)
	}
	if _, err := seed.Update(context.Background(), func([]gomutant.Finding) ([]gomutant.Finding, error) { return []gomutant.Finding{shaped}, nil }); err != nil {
		t.Fatal(err)
	}
	stale := `{"version":1,"exemptions":[{"subject":"example.com/empty.TestBoundary","reason":"external directory input: ` + dir + `/escape","rationale":"reviewed"}]}`
	if err := os.WriteFile(gomutant.ExemptionsPathFor(path), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(dir)
	// The read verbs judge under the re-keyed entries and write nothing.
	if _, _, err := s.toolFindings(context.Background(), nil, findingsIn{}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.toolDiscover(context.Background(), nil, discoverIn{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(gomutant.ExemptionsPathFor(path)); string(got) != stale {
		t.Fatalf("a read verb rewrote the record:\n%s", got)
	}
	_, out, err := s.toolRun(context.Background(), nil, runIn{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.ExemptionsRekeyed) != 1 || out.OmittedExemptionsRekeyed != 0 {
		t.Fatalf("re-keyed rows = %+v (omitted %d), want the one entry", out.ExemptionsRekeyed, out.OmittedExemptionsRekeyed)
	}
	row := out.ExemptionsRekeyed[0]
	if row.Subject != "example.com/empty.TestBoundary" || row.From != "external directory input: "+dir+"/escape" || row.To != "external directory input: escape" {
		t.Fatalf("re-keyed row = %+v", row)
	}
	if out.Demoted != 0 {
		t.Fatalf("a re-keyed entry read as withdrawn: demoted %d", out.Demoted)
	}
	if data, err := os.ReadFile(path); err != nil {
		t.Fatal(err)
	} else if records, err := gomutant.ParseFindings(data); err != nil || len(records) != 1 || records[0].Symbol != shaped.Symbol {
		t.Fatalf("re-key lost the committed record: %+v %v", records, err)
	}
	got, err := os.ReadFile(gomutant.ExemptionsPathFor(path))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), dir) {
		t.Fatalf("the run did not persist the re-key:\n%s", got)
	}
	// The prune tool as the first committing write lists the row too.
	if err := os.WriteFile(gomutant.ExemptionsPathFor(path), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	_, pruned, err := s.toolPrune(context.Background(), nil, pruneIn{})
	if err != nil || len(pruned.ExemptionsRekeyed) != 1 || pruned.ExemptionsRekeyed[0] != row {
		t.Fatalf("the prune's re-key rows = %+v, %v; want the one entry", pruned.ExemptionsRekeyed, err)
	}
}

// The attest tool — a committing write — persists the re-key and lists
// it on its response (REQ-result-exemptions).
func TestToolAttestReKeysTheExemptionRecordAndListsIt(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/empty\n\ngo 1.26.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "empty.go"), []byte("package empty\n\nfunc F() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := gomutant.FindingsPathAt(dir, "")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	stale := `{"version":1,"exemptions":[{"subject":"example.com/empty.TestF","reason":"external directory input: ` + dir + `/escape","rationale":"reviewed"}]}`
	if err := os.WriteFile(gomutant.ExemptionsPathFor(path), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	seed, err := gomutant.OpenStore(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	// The reviewed shared observation must permit repository placement;
	// a legacy-manifest refusal would make the preservation check vacuous.
	if layer, reason := seed.Layer(rekeyAttestFixture()); layer != gomutant.LayerRepo {
		t.Fatalf("attestation seed layer=%s: %s", layer, reason)
	}
	if _, err := seed.Update(context.Background(), func([]gomutant.Finding) ([]gomutant.Finding, error) {
		return []gomutant.Finding{rekeyAttestFixture()}, nil
	}); err != nil {
		t.Fatal(err)
	}
	// The seed persisted the re-key; the attest's own store finds the
	// record current and reports nothing — so plant the stale spelling
	// again for the attest to re-key.
	if err := os.WriteFile(gomutant.ExemptionsPathFor(path), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	_, out, err := New(dir).toolAttest(context.Background(), nil, attestIn{Symbol: "example.com/empty.F", Position: "empty.go:1:1", Operator: "zero return", Reason: "equivalent by inspection"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.ExemptionsRekeyed) != 1 || out.ExemptionsRekeyed[0].Subject != "example.com/empty.TestF" || out.ExemptionsRekeyed[0].To != "external directory input: escape" {
		t.Fatalf("attest re-key rows = %+v", out.ExemptionsRekeyed)
	}
	if got, _ := os.ReadFile(gomutant.ExemptionsPathFor(path)); strings.Contains(string(got), dir) {
		t.Fatalf("the attest left the record stale:\n%s", got)
	}
	if data, err := os.ReadFile(path); err != nil {
		t.Fatal(err)
	} else if records, err := gomutant.ParseFindings(data); err != nil || len(records) != 1 || len(records[0].Attested) != 1 {
		t.Fatalf("attest lost repository placement or its disposition: %+v %v", records, err)
	}
}

// rekeyAttestFixture is a measured record with one open survivor whose
// oracle's read sealed the finding (both rows carry the clause the
// stale entry re-keys to).
func rekeyAttestFixture() gomutant.Finding {
	evidence := func(name string) gomutant.SubjectEvidence {
		return gomutant.SubjectEvidence{Symbol: name, Fingerprint: gofresh.Fingerprint{MaximalClosure: "closure", TestVariantClosure: "tv", ObservationAssertion: "caller assertion", RuntimeInputs: "eyJ2IjoyfQ", RuntimeDigest: "digest", Guards: guard.Guards{Toolchain: "go", BuildConfig: "build"}, ObservationProof: gofresh.ObservationProof{Strategy: "proof/v1", Subject: gofresh.Subject{Package: "example.com/empty", Symbol: strings.TrimPrefix(name, "example.com/empty.")}, Observable: true, Evidence: "proof"}, ResultKind: gofresh.CodeResult}, RuntimeUnverifiable: true, RuntimeReason: "external directory input: escape"}
	}
	return gomutant.Finding{Symbol: "example.com/empty.F", BodyHash: "body", OperatorSet: "go/2", OracleTimeout: "1m0s", Commit: "abc",
		CandidateCount: 1, Generated: 1, Mutants: 1,
		TargetEvidence: evidence("example.com/empty.F"), OracleEvidence: []gomutant.SubjectEvidence{evidence("example.com/empty.TestF")},
		Operators: []gomutant.OperatorSummary{{Operator: "zero return", Generated: 1, Survived: 1}},
		Survivors: []gomutant.Survivor{{Position: "empty.go:1:1", Operator: "zero return"}}}
}
