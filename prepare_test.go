package gomutant

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The preparation carries the exemption record beside the findings
// document into the run (REQ-result-exemptions): a record with one
// entry reaches the prepared value from the store's own read.
func TestPrepareCampaignCarriesTheExemptionRecord(t *testing.T) {
	root := t.TempDir()
	doc := filepath.Join(root, ".gomutant", "findings.json")
	if err := os.MkdirAll(filepath.Dir(doc), 0o755); err != nil {
		t.Fatal(err)
	}
	record := `{"version":1,"exemptions":[{"subject":"example.com/p.F","reason":"runtime input: clock","rationale":"reviewed: the clock read is the subject"}]}`
	if err := os.WriteFile(ExemptionsPathFor(doc), []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}
	prepared, err := PrepareCampaign(context.Background(), CampaignInputs{FindingsPath: doc, ModuleDir: root, Plan: true})
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.ReleaseCampaign()
	if len(prepared.Exemptions) != 1 || prepared.Exemptions[0].Subject != "example.com/p.F" || prepared.Exemptions[0].Reason != "runtime input: clock" {
		t.Fatalf("prepared exemptions = %+v; want the record's one entry", prepared.Exemptions)
	}
}
