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

// A committed exemption record authored before Gofresh spelled
// in-module paths module-relative keeps covering its subject: the
// read-only findings verb judges under the re-keyed clause and leaves
// the file as it was, and the run — the first committing write —
// persists the re-key, says so, and demotes nothing
// (REQ-result-exemptions).
func TestRunCommandReKeysTheExemptionRecordAndSaysSo(t *testing.T) {
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
	path := gomutant.FindingsPathAt(dir, defaultFindings)
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
	// The record as a pre-292 checkout authored it: the escaping link
	// spelled by this checkout's absolute path.
	stale := `{"version":1,"exemptions":[{"subject":"example.com/empty.TestBoundary","reason":"external directory input: ` + dir + `/escape","rationale":"reviewed"}]}`
	if err := os.WriteFile(gomutant.ExemptionsPathFor(path), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	var listing bytes.Buffer
	if err := findingsCommand(context.Background(), findingsOptions{dir: dir, findingsFile: defaultFindings}, &listing); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(gomutant.ExemptionsPathFor(path)); string(got) != stale {
		t.Fatalf("the read-only findings verb rewrote the record:\n%s", got)
	}
	var output bytes.Buffer
	if err := runCommand(context.Background(), runOptions{dir: dir, findingsFile: defaultFindings, output: &output}); err != nil {
		t.Fatal(err)
	}
	want := `re-keyed 1 reviewed exemption clause(s) to the module-relative spelling: example.com/empty.TestBoundary "external directory input: ` + dir + `/escape" -> "external directory input: escape"` + "\n"
	if !strings.Contains(output.String(), want) {
		t.Fatalf("the run left its re-key unstated:\n%s", output.String())
	}
	if strings.Contains(output.String(), "demoted") {
		t.Fatalf("a re-keyed entry read as withdrawn:\n%s", output.String())
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
	if strings.Contains(string(got), dir) || !strings.Contains(string(got), `"external directory input: escape"`) {
		t.Fatalf("the run did not persist the re-key:\n%s", got)
	}
	// The prune verb as the first committing write prints the line too.
	if err := os.WriteFile(gomutant.ExemptionsPathFor(path), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	var pruned bytes.Buffer
	if err := pruneCommand(context.Background(), pruneOptions{dir: dir, findingsFile: defaultFindings}, &pruned); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pruned.String(), want) {
		t.Fatalf("the prune left its re-key unstated:\n%s", pruned.String())
	}
}

// The attest verb — a committing write — persists the re-key and says
// so on its epilogue (REQ-result-exemptions).
func TestAttestCommandReKeysTheExemptionRecordAndSaysSo(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/empty\n\ngo 1.26.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "empty.go"), []byte("package empty\n\nfunc F() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := gomutant.FindingsPathAt(dir, defaultFindings)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	stale := `{"version":1,"exemptions":[{"subject":"example.com/empty.TestF","reason":"external directory input: ` + dir + `/escape","rationale":"reviewed"}]}`
	if err := os.WriteFile(gomutant.ExemptionsPathFor(path), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	evidence := func(name string) gomutant.SubjectEvidence {
		return gomutant.SubjectEvidence{Symbol: name, Fingerprint: gofresh.Fingerprint{MaximalClosure: "closure", TestVariantClosure: "tv", ObservationAssertion: "caller assertion", RuntimeInputs: "eyJ2IjoyfQ", RuntimeDigest: "digest", Guards: guard.Guards{Toolchain: "go", BuildConfig: "build"}, ObservationProof: gofresh.ObservationProof{Strategy: "proof/v1", Subject: gofresh.Subject{Package: "example.com/empty", Symbol: strings.TrimPrefix(name, "example.com/empty.")}, Observable: true, Evidence: "proof"}, ResultKind: gofresh.CodeResult}, RuntimeUnverifiable: true, RuntimeReason: "external directory input: escape"}
	}
	record := gomutant.Finding{Symbol: "example.com/empty.F", BodyHash: "body", OperatorSet: "go/2", OracleTimeout: "1m0s", Commit: "abc",
		CandidateCount: 1, Generated: 1, Mutants: 1,
		TargetEvidence: evidence("example.com/empty.F"), OracleEvidence: []gomutant.SubjectEvidence{evidence("example.com/empty.TestF")},
		Operators: []gomutant.OperatorSummary{{Operator: "zero return", Generated: 1, Survived: 1}},
		Survivors: []gomutant.Survivor{{Position: "empty.go:1:1", Operator: "zero return"}}}
	seed, err := gomutant.OpenStore(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	// The reviewed shared observation must permit repository placement;
	// a legacy-manifest refusal would make the preservation check vacuous.
	if layer, reason := seed.Layer(record); layer != gomutant.LayerRepo {
		t.Fatalf("attestation seed layer=%s: %s", layer, reason)
	}
	if _, err := seed.Update(context.Background(), func([]gomutant.Finding) ([]gomutant.Finding, error) { return []gomutant.Finding{record}, nil }); err != nil {
		t.Fatal(err)
	}
	// The seed persisted the re-key; plant the stale spelling again for
	// the attest to re-key.
	if err := os.WriteFile(gomutant.ExemptionsPathFor(path), []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := attestCommand(context.Background(), attestOptions{dir: dir, findingsFile: defaultFindings, symbol: "example.com/empty.F", position: "empty.go:1:1", operator: "zero return", reason: "equivalent by inspection"}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `re-keyed 1 reviewed exemption clause(s) to the module-relative spelling: example.com/empty.TestF "external directory input: `+dir+`/escape" -> "external directory input: escape"`+"\n") {
		t.Fatalf("the attest left its re-key unstated:\n%s", output.String())
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
