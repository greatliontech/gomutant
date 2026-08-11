package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gomutant "github.com/greatliontech/gomutant"
	"github.com/greatliontech/gomutant/internal/engine"
)

// The CLI lifecycle verbs remove resolved-dead records with the
// disposition echo and rewrite symbol identity across a rename, both
// with check previews (REQ-result-lifecycle).
func TestPruneAndRetargetCommands(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":    "module example.com/life\n\ngo 1.26.4\n",
		"p.go":      "package life\n\nfunc F() int { return 1 }\n",
		"p_test.go": "package life\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) { if F() != 1 { t.Fatal() } }\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	evidence := func(name string) gomutant.SubjectEvidence {
		// The recorded subject package must agree with the symbol - the
		// retarget's package-boundary gate audits the stored fact.
		return gomutant.SubjectEvidence{Symbol: name, MaximalClosure: "closure", TestVariantClosure: "tv", Toolchain: "go", BuildConfig: "build",
			ObservationAssertion: "caller assertion", ObservationStrategy: "proof/v1", ObservationSubjectPackage: name[:strings.LastIndex(name, ".")],
			ObservationSubjectSymbol: name[strings.LastIndex(name, ".")+1:], ObservationObservable: true, ObservationEvidence: "proof",
			RuntimeInputs: "manifest", RuntimeDigest: "digest"}
	}
	record := func(symbol string) gomutant.Finding {
		return gomutant.Finding{Symbol: symbol, BodyHash: "body", OperatorSet: engine.OperatorSet, OracleTimeout: "1m0s", Dirty: true,
			CandidateCount: 1, Generated: 1, Mutants: 1,
			TargetEvidence: evidence(symbol), OracleEvidence: []gomutant.SubjectEvidence{evidence(symbol + "Test")},
			Operators: []gomutant.OperatorSummary{{Operator: "zero return", Generated: 1, Survived: 1}},
			Survivors: []gomutant.Survivor{{Position: "p.go:1:1", Operator: "zero return"}},
			Attested:  []gomutant.Attestation{{Position: "p.go:1:1", Operator: "zero return", Reason: "equivalent by inspection"}}}
	}
	if err := gomutant.UpdateDocument(findingsAt(dir, defaultFindings), func([]gomutant.Finding) ([]gomutant.Finding, error) {
		return []gomutant.Finding{record("example.com/life.Gone"), record("example.com/old.F")}, nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	var retargetOut bytes.Buffer
	if err := retargetCommand(ctx, retargetOptions{dir: dir, findingsFile: defaultFindings, from: "example.com/old.", to: "example.com/life."}, &retargetOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(retargetOut.String(), "retargeted example.com/old.F -> example.com/life.F") {
		t.Fatalf("retarget output = %q", retargetOut.String())
	}

	var preview bytes.Buffer
	if err := pruneCommand(ctx, pruneOptions{dir: dir, findingsFile: defaultFindings, check: true}, &preview); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview.String(), "would prune     example.com/life.Gone") || !strings.Contains(preview.String(), "1 kept") {
		t.Fatalf("prune preview = %q", preview.String())
	}

	var pruned bytes.Buffer
	if err := pruneCommand(ctx, pruneOptions{dir: dir, findingsFile: defaultFindings}, &pruned); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pruned.String(), "pruned     example.com/life.Gone") ||
		!strings.Contains(pruned.String(), "attested p.go:1:1 zero return  (equivalent by inspection)") {
		t.Fatalf("prune output lost the disposition echo: %q", pruned.String())
	}
	after, err := loadFindings(dir, findingsAt(dir, defaultFindings))
	if err != nil || len(after) != 1 || after[0].Symbol != "example.com/life.F" {
		t.Fatalf("document after lifecycle commands = %+v, %v", after, err)
	}
}
