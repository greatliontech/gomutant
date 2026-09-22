package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"
	gomutant "github.com/greatliontech/gomutant"
	"github.com/greatliontech/gomutant/internal/engine"
)

// The CLI lifecycle verbs remove resolved-dead records with the
// disposition echo and rewrite symbol identity across a rename, both
// with check previews (REQ-result-lifecycle).
func TestPruneAndRetargetCommands(t *testing.T) {
	if testing.Short() {
		t.Skip("measured heavy under the fast tier (in-process)")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":    "module example.com/life\n\ngo 1.26.4\n",
		"p.go":      "package life\n\nfunc F() int { return 1 }\n\nfunc G() int { return 2 }\n",
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
		return gomutant.SubjectEvidence{Symbol: name, Fingerprint: gofresh.Fingerprint{MaximalClosure: "closure", TestVariantClosure: "tv", ObservationAssertion: "caller assertion", RuntimeInputs: "manifest", RuntimeDigest: "digest", Guards: guard.Guards{Toolchain: "go", BuildConfig: "build"}, ObservationProof: gofresh.ObservationProof{Strategy: "proof/v1", Subject: gofresh.Subject{Package: name[:strings.LastIndex(name, ".")], Symbol: name[strings.LastIndex(name, ".")+1:]}, Observable: true, Evidence: "proof"}, ResultKind: gofresh.CodeResult}}
	}
	record := func(symbol string) gomutant.Finding {
		return gomutant.Finding{Symbol: symbol, BodyHash: "body", OperatorSet: engine.OperatorSet, OracleTimeout: "1m0s", Dirty: true,
			CandidateCount: 1, Generated: 1, Mutants: 1,
			TargetEvidence: evidence(symbol), OracleEvidence: []gomutant.SubjectEvidence{evidence(symbol + "Test")},
			Operators: []gomutant.OperatorSummary{{Operator: "zero return", Generated: 1, Survived: 1}},
			Survivors: []gomutant.Survivor{{Position: "p.go:1:1", Operator: "zero return"}},
			Attested:  []gomutant.Attestation{{Position: "p.go:1:1", Operator: "zero return", Reason: "equivalent by inspection"}}}
	}
	// Seeded through the store: the dirty records land in the overlay,
	// the layer the verbs keep them in.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	seed, err := gomutant.OpenStore(gomutant.FindingsPathAt(dir, defaultFindings), dir)
	if err != nil {
		t.Fatal(err)
	}
	// A committable variant lands in the document: its rows carry no
	// marker.
	committed := func(symbol string) gomutant.Finding {
		f := record(symbol)
		f.Dirty, f.Commit = false, "abc"
		f.TargetEvidence.RuntimeInputs, f.OracleEvidence[0].RuntimeInputs = "eyJ2IjoxfQ", "eyJ2IjoxfQ"
		return f
	}
	if err := seed.Update(context.Background(), func([]gomutant.Finding) ([]gomutant.Finding, error) {
		return []gomutant.Finding{record("example.com/life.Gone"), record("example.com/old.F"), committed("example.com/life.Gone2"), committed("example.com/old.G")}, nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	var retargetOut bytes.Buffer
	if err := retargetCommand(ctx, retargetOptions{dir: dir, findingsFile: defaultFindings, from: "example.com/old.", to: "example.com/life."}, &retargetOut); err != nil {
		t.Fatal(err)
	}
	// The dirty record sat in the overlay: the row carries the
	// machine-local marker (REQ-result-layers).
	if !strings.Contains(retargetOut.String(), "retargeted example.com/old.F -> example.com/life.F  [machine-local]\n") || !strings.Contains(retargetOut.String(), "retargeted example.com/old.G -> example.com/life.G\n") {
		t.Fatalf("retarget output = %q", retargetOut.String())
	}

	var preview bytes.Buffer
	if err := pruneCommand(ctx, pruneOptions{dir: dir, findingsFile: defaultFindings, check: true}, &preview); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(preview.String(), "would prune     example.com/life.Gone  [machine-local]\n") || !strings.Contains(preview.String(), "would prune     example.com/life.Gone2\n") || !strings.Contains(preview.String(), "2 kept") {
		t.Fatalf("prune preview = %q", preview.String())
	}

	var pruned bytes.Buffer
	if err := pruneCommand(ctx, pruneOptions{dir: dir, findingsFile: defaultFindings}, &pruned); err != nil {
		t.Fatal(err)
	}
	// The dirty record sat in the overlay: the row carries the
	// machine-local marker (REQ-result-layers).
	if !strings.Contains(pruned.String(), "pruned     example.com/life.Gone  [machine-local]\n") || !strings.Contains(pruned.String(), "pruned     example.com/life.Gone2\n") ||
		!strings.Contains(pruned.String(), "attested p.go:1:1 zero return  (equivalent by inspection)") {
		t.Fatalf("prune output lost the disposition echo: %q", pruned.String())
	}
	// The shadow statement and the stale-exemption note ride the rows
	// (REQ-result-lifecycle).
	var shadow bytes.Buffer
	renderRetarget(&shadow, gomutant.RetargetResult{Rewritten: []gomutant.RetargetedRecord{{From: "a.F", To: "b.F", Layer: gomutant.LayerRepo, Shadowed: true}, {From: "a.G", To: "b.G", Layer: gomutant.LayerRepo}}, StaleExemptions: []string{"a.TestX"}})
	if got := shadow.String(); !strings.Contains(got, "retargeted a.F -> b.F  (a machine-local record holds the new symbol and shadows this row)\n") || !strings.Contains(got, "retargeted a.G -> b.G\n") || !strings.Contains(got, "note: 1 reviewed exemption subject(s) under the old prefix that no record carries - no rewrite reaches them; rewrite or delete them by hand: a.TestX\n") {
		t.Fatalf("retarget rows = %q", got)
	}
	// The note is bounded like every roster: the first twenty listed, the
	// remainder counted.
	var many []string
	for i := 0; i < 23; i++ {
		many = append(many, fmt.Sprintf("a.Test%02d", i))
	}
	var bounded bytes.Buffer
	renderRetarget(&bounded, gomutant.RetargetResult{StaleExemptions: many})
	if got := bounded.String(); !strings.Contains(got, "note: 23 reviewed exemption subject(s)") || !strings.Contains(got, "a.Test19 (+3 more)\n") || strings.Contains(got, "a.Test20") {
		t.Fatalf("bounded note = %q", got)
	}
	after, err := loadFindings(dir, gomutant.FindingsPathAt(dir, defaultFindings))
	if err != nil || len(after) != 2 || after[0].Symbol != "example.com/life.F" || after[1].Symbol != "example.com/life.G" {
		t.Fatalf("document after lifecycle commands = %+v, %v", after, err)
	}
}

// The campaign lock rides the run face: a held lock refuses the run
// fail-fast naming the holder (REQ-exec-exclusivity).
func TestRunRefusesWhileCampaignLockHeld(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test over a fixture module")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":    "module example.com/locked\n\ngo 1.26.4\n",
		"p.go":      "package locked\n\nfunc F() int { return 1 }\n",
		"p_test.go": "package locked\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) { if F() != 1 { t.Fatal() } }\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	release, err := gomutant.AcquireCampaignLock(gomutant.FindingsPathAt(dir, defaultFindings))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	err = runCommand(context.Background(), runOptions{dir: dir, findingsFile: defaultFindings, budget: 1})
	if err == nil || !strings.Contains(err.Error(), "already holds") {
		t.Fatalf("run under a held campaign lock = %v, want the fail-fast refusal", err)
	}
	// A plan persists nothing, so it needs no lock: it proceeds beside
	// the held campaign instead of refusing.
	if err := runCommand(context.Background(), runOptions{dir: dir, findingsFile: defaultFindings, budget: 1, plan: true}); err != nil {
		t.Fatalf("plan under a held campaign lock = %v, want it to proceed", err)
	}
}
