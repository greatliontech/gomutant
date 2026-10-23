package gomutant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPillarM1_CoexistingSelectionRecordsAndCrossSelectionVerdicts(t *testing.T) {
	defaultFinding := survivorFinding("example.com/mod/host.D")
	defaultFinding.Selection = ""
	defaultFinding.Attested = nil
	defaultFinding.Survivors = []Survivor{
		{Position: "a.go:1:1", Operator: "zero return", Site: "site-1"},
	}

	integrationFinding := survivorFinding("example.com/mod/host.D")
	integrationFinding.Selection = "tags:integration"
	integrationFinding.Attested = nil
	integrationFinding.Survivors = nil
	integrationFinding.Killed = 1
	integrationFinding.Operators = []OperatorSummary{
		{Operator: "zero return", Generated: 1, Discarded: 0, Killed: 1, Survived: 0},
	}
	integrationFinding.Kills = []Kill{
		{Position: "a.go:1:1", Operator: "zero return", Killer: "example.com/mod/host.TestIntegration"},
	}

	merged, sheds := MergeFindings([]Finding{defaultFinding}, []Finding{integrationFinding}, nil)
	if len(sheds) != 0 {
		t.Fatalf("unexpected sheds: %+v", sheds)
	}
	if len(merged) != 2 {
		t.Fatalf("expected 2 coexisting selection records, got %d", len(merged))
	}

	data, err := Export(merged, nil)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}
	parsed, err := ParseFindings(data)
	if err != nil {
		t.Fatalf("ParseFindings failed: %v", err)
	}
	if len(parsed) != 2 {
		t.Fatalf("expected 2 parsed records, got %d", len(parsed))
	}
	if parsed[0].Selection != "" || parsed[1].Selection != "tags:integration" {
		t.Fatalf("unexpected selection ordering: %q, %q", parsed[0].Selection, parsed[1].Selection)
	}
	others := parsed[0].Survivors[0].OtherSelections()
	if len(others) != 1 || others[0].Selection != "tags:integration" || others[0].Disposition != "killed" || others[0].Killer != "example.com/mod/host.TestIntegration" {
		t.Fatalf("expected cross-selection kill verdict on default survivor, got %+v (raw %q)", others, parsed[0].Survivors[0].OtherSelection)
	}
}

func TestPillarM1_AttestReplaceAndWithdraw(t *testing.T) {
	f := survivorFinding("example.com/mod/host.D")
	f.Attested = nil

	// Initial attestation
	all, updated, err := AttestFindingWithOptions([]Finding{f}, f.Symbol, "a.go:1:1", "zero return", "initial equivalence reason", AttestOptions{})
	if err != nil {
		t.Fatalf("initial attest failed: %v", err)
	}
	if len(updated.Open()) != 0 || len(updated.Attested) != 1 {
		t.Fatalf("unexpected state after attest: open=%d attested=%d", len(updated.Open()), len(updated.Attested))
	}

	// Replace attestation (--reattest)
	all, updated, err = AttestFindingWithOptions(all, f.Symbol, "a.go:1:1", "zero return", "refined equivalence reason", AttestOptions{Reattest: true})
	if err != nil {
		t.Fatalf("reattest failed: %v", err)
	}
	if len(updated.Attested) != 1 || updated.Attested[0].Reason != "refined equivalence reason" {
		t.Fatalf("unexpected state after reattest: %+v", updated.Attested)
	}

	// Withdraw attestation (--withdraw)
	all, updated, err = AttestFindingWithOptions(all, f.Symbol, "a.go:1:1", "zero return", "withdrawing after test review", AttestOptions{Withdraw: true})
	if err != nil {
		t.Fatalf("withdraw failed: %v", err)
	}
	if len(updated.Open()) != 1 || len(updated.Attested) != 0 || len(updated.Withdrawn) != 1 {
		t.Fatalf("unexpected state after withdraw: open=%d attested=%d withdrawn=%d", len(updated.Open()), len(updated.Attested), len(updated.Withdrawn))
	}
	if updated.Withdrawn[0].Reason != "withdrawing after test review" || updated.Withdrawn[0].PriorReason != "refined equivalence reason" {
		t.Fatalf("unexpected withdrawn record: %+v", updated.Withdrawn[0])
	}

	// Round-trip through Export/ParseFindings
	data, err := Export(all, nil)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}
	parsed, err := ParseFindings(data)
	if err != nil {
		t.Fatalf("ParseFindings failed: %v", err)
	}
	if len(parsed[0].Withdrawn) != 1 || parsed[0].Withdrawn[0].PriorReason != "refined equivalence reason" {
		t.Fatalf("withdrawn record lost on round-trip: %+v", parsed[0].Withdrawn)
	}
}

func TestPillarM1_InternedTableDuplicateKeysRejected(t *testing.T) {
	f := survivorFinding("example.com/mod/host.D")
	data, err := Export([]Finding{f}, nil)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}
	// Inject duplicate key into runtimeInputsTable entry
	dupRuntime := strings.Replace(string(data), `"manifest":`, `"manifest": "eyJ2Ijo0fQ", "manifest":`, 1)
	if _, err := ParseFindings([]byte(dupRuntime)); err == nil {
		t.Fatalf("expected duplicate key in runtimeInputsTable to be rejected")
	}
	// Inject duplicate key into evidenceTable entry
	dupEvidence := strings.Replace(string(data), `"runtimeInputs":`, `"runtimeInputs": "8973f9e8974b183f68b3c526", "runtimeInputs":`, 1)
	if _, err := ParseFindings([]byte(dupEvidence)); err == nil {
		t.Fatalf("expected duplicate key in evidenceTable to be rejected")
	}
}

func TestPillarM1_CachePrunePreservesLocalAttestationsByDefault(t *testing.T) {
	stateHome := t.TempDir()
	cacheHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("XDG_CACHE_HOME", cacheHome)

	modDir := t.TempDir()
	findingsPath := filepath.Join(modDir, ".gomutant", "findings.json")
	store, err := OpenStore(findingsPath, modDir)
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}

	// Create an attested finding that routes to the machine-local overlay (dirty: true)
	attestedLocal := survivorFinding("example.com/mod/host.AttestedLocal")
	attestedLocal.Dirty = true

	unattestedLocal := survivorFinding("example.com/mod/host.UnattestedLocal")
	unattestedLocal.Dirty = true
	unattestedLocal.Attested = nil

	if _, err := store.Update(context.Background(), func([]Finding) ([]Finding, error) {
		return []Finding{attestedLocal, unattestedLocal}, nil
	}); err != nil {
		t.Fatalf("store.Update failed: %v", err)
	}

	// Write a disposable baseline bank entry in the tree's cache dir
	_, treeDir, err := machineLocalDir(modDir)
	if err != nil {
		t.Fatalf("machineLocalDir failed: %v", err)
	}
	baselinePath := filepath.Join(treeDir, "baselines.json")
	if err := os.WriteFile(baselinePath, []byte(`{"version":2,"entries":[{"key":"k1","rawNs":1000,"passed":["TestA"],"evidence":[]}]}`), 0o644); err != nil {
		t.Fatalf("write baselines.json failed: %v", err)
	}

	// Default cache prune: reclaims baselines, preserves BOTH local overlay records!
	res, err := PruneCacheContext(context.Background(), modDir, CachePruneOptions{})
	if err != nil {
		t.Fatalf("PruneCacheContext default failed: %v", err)
	}
	if res.RemovedBaselines != 1 || res.RemovedLocalRecords != 0 || res.RetainedLocalRecords != 2 || res.RetainedAttestedRecords != 1 {
		t.Fatalf("unexpected default cache prune result: %+v", res)
	}

	// IncludeLocalRecords without IncludeAttested: removes unattested overlay record, preserves attested overlay record!
	res, err = PruneCacheContext(context.Background(), modDir, CachePruneOptions{IncludeLocalRecords: true})
	if err != nil {
		t.Fatalf("PruneCacheContext IncludeLocalRecords failed: %v", err)
	}
	if res.RemovedLocalRecords != 1 || res.RemovedAttestedRecords != 0 || res.RetainedLocalRecords != 1 || res.RetainedAttestedRecords != 1 {
		t.Fatalf("unexpected IncludeLocalRecords result: %+v", res)
	}

	// Verify store.Load still serves the attested local record!
	loaded, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("store.Load failed: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Symbol != attestedLocal.Symbol || len(loaded[0].Attested) != 1 {
		t.Fatalf("expected attested local record preserved, got %+v", loaded)
	}
}
