package mcpserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gomutant "github.com/greatliontech/gomutant"
)

// The lifecycle verbs ride the protocol face: prune removes
// resolved-dead records echoing their dispositions, retarget rewrites
// symbol identity, both with check previews (REQ-mcp-lifecycle).
func TestToolPruneAndRetarget(t *testing.T) {
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
	s := New(dir)
	ctx := context.Background()

	dead := seededFinding("example.com/life.Gone")
	dead.Attested = []gomutant.Attestation{{Position: "p.go:1:1", Operator: "zero return", Reason: "equivalent by inspection"}}
	dead.Survivors = []gomutant.Survivor{{Position: "p.go:1:1", Operator: "zero return"}}
	dead.CandidateCount, dead.Generated, dead.Mutants = 1, 1, 1
	dead.Operators = []gomutant.OperatorSummary{{Operator: "zero return", Generated: 1, Survived: 1}}
	renamed := seededFinding("example.com/old.F")
	// The stored subject package must agree with the symbol - the
	// retarget's package-boundary gate audits the stored fact.
	renamed.TargetEvidence.ObservationSubjectPackage = "example.com/old"
	if err := gomutant.UpdateDocument(filepath.Join(dir, defaultFindings), func([]gomutant.Finding) ([]gomutant.Finding, error) {
		return []gomutant.Finding{dead, renamed}, nil
	}); err != nil {
		t.Fatal(err)
	}

	// Retarget first: the renamed record follows the rename; the dead
	// one is untouched (no prefix match).
	_, preview, err := s.toolRetarget(ctx, nil, retargetIn{From: "example.com/old.", To: "example.com/life.", Check: true})
	if err != nil || !preview.Check || len(preview.Rewritten) != 1 {
		t.Fatalf("retarget preview = %+v, %v", preview, err)
	}
	_, rOut, err := s.toolRetarget(ctx, nil, retargetIn{From: "example.com/old.", To: "example.com/life."})
	if err != nil || len(rOut.Rewritten) != 1 || rOut.Rewritten[0].To != "example.com/life.F" {
		t.Fatalf("retarget = %+v, %v", rOut, err)
	}

	_, pPreview, err := s.toolPrune(ctx, nil, pruneIn{Check: true})
	if err != nil || !pPreview.Check || len(pPreview.Removed) != 1 || pPreview.Removed[0].Symbol != "example.com/life.Gone" {
		t.Fatalf("prune preview = %+v, %v", pPreview, err)
	}
	_, pOut, err := s.toolPrune(ctx, nil, pruneIn{})
	if err != nil || len(pOut.Removed) != 1 || pOut.Kept != 1 {
		t.Fatalf("prune = %+v, %v", pOut, err)
	}
	if len(pOut.Removed[0].Attested) != 1 || !strings.Contains(pOut.Removed[0].Attested[0].Reason, "equivalent by inspection") {
		t.Fatalf("prune response lost the disposition echo: %+v", pOut.Removed[0])
	}
	all, err := s.loadFindings("")
	if err != nil || len(all) != 1 || all[0].Symbol != "example.com/life.F" {
		t.Fatalf("document after lifecycle verbs = %+v, %v", all, err)
	}
}

// The prune echo is never truncated - a removal echo is
// promote-then-delete, and truncation would silently drop dispositions
// - while retarget's echo caps with the remainder counted
// (REQ-mcp-lifecycle).
func TestToolLifecycleEchoBounds(t *testing.T) {
	dir := t.TempDir()
	var src strings.Builder
	src.WriteString("package life\n")
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&src, "\nfunc F%d() int { return 1 }\n", i)
	}
	files := map[string]string{
		"go.mod":    "module example.com/life\n\ngo 1.26.4\n",
		"p.go":      src.String(),
		"p_test.go": "package life\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) { if F0() != 1 { t.Fatal() } }\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := New(dir)
	ctx := context.Background()

	var seed []gomutant.Finding
	for i := 0; i < 60; i++ {
		dead := seededFinding(fmt.Sprintf("example.com/gone.G%d", i))
		dead.Attested = []gomutant.Attestation{{Position: "p.go:1:1", Operator: "zero return", Reason: fmt.Sprintf("equivalent by inspection %d", i)}}
		dead.Survivors = []gomutant.Survivor{{Position: "p.go:1:1", Operator: "zero return"}}
		dead.CandidateCount, dead.Generated, dead.Mutants = 1, 1, 1
		dead.Operators = []gomutant.OperatorSummary{{Operator: "zero return", Generated: 1, Survived: 1}}
		seed = append(seed, dead)
	}
	for i := 0; i < 60; i++ {
		f := seededFinding(fmt.Sprintf("example.com/old.F%d", i))
		f.TargetEvidence.ObservationSubjectPackage = "example.com/old"
		seed = append(seed, f)
	}
	if err := gomutant.UpdateDocument(filepath.Join(dir, defaultFindings), func([]gomutant.Finding) ([]gomutant.Finding, error) {
		return seed, nil
	}); err != nil {
		t.Fatal(err)
	}

	_, rOut, err := s.toolRetarget(ctx, nil, retargetIn{From: "example.com/old.", To: "example.com/life."})
	if err != nil || len(rOut.Rewritten) != 50 || rOut.OmittedRewritten != 10 {
		t.Fatalf("retarget echo = %d rows, omitted %d, %v; want the cap and the counted remainder", len(rOut.Rewritten), rOut.OmittedRewritten, err)
	}

	_, pOut, err := s.toolPrune(ctx, nil, pruneIn{})
	if err != nil || len(pOut.Removed) != 60 || pOut.Kept != 60 {
		t.Fatalf("prune echo = %d rows, kept %d, %v; want every removal echoed, uncapped", len(pOut.Removed), pOut.Kept, err)
	}
	for i, r := range pOut.Removed {
		if len(r.Attested) != 1 || r.Attested[0].Reason == "" {
			t.Fatalf("prune echo row %d lost its disposition: %+v", i, r)
		}
	}
}
