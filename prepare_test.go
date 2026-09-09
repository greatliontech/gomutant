package gomutant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

// The harness environment is judged in preparation, before the lock:
// a silenced harness refuses with no campaign lock created and the
// findings document untouched.
func TestPrepareCampaignRefusesASilencedHarnessBeforeTheLock(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GODEBUG", "gotestjsonbuildtext=1")
	path := filepath.Join(dir, ".gomutant", "findings.json")
	_, err := PrepareCampaign(context.Background(), CampaignInputs{FindingsPath: path, ModuleDir: dir})
	if err == nil || !strings.Contains(err.Error(), "gotestjsonbuildtext=1") {
		t.Fatalf("preparation under a silenced harness = %v, want the refusal naming the setting", err)
	}
	if _, err := os.Stat(path + ".campaign"); !os.IsNotExist(err) {
		t.Fatalf("a refused preparation left the campaign lock: %v", err)
	}
}

// The selection's shape is a preparation refusal through the same
// composition the harness arm reads: a malformed tag refuses before
// the lock.
func TestPrepareCampaignRefusesAMalformedSelectionBeforeTheLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gomutant", "findings.json")
	_, err := PrepareCampaign(context.Background(), CampaignInputs{FindingsPath: path, ModuleDir: dir, Selection: Selection{Tags: []string{"not a tag!"}}})
	if err == nil || !strings.Contains(err.Error(), "not a tag!") {
		t.Fatalf("preparation under a malformed selection = %v, want the refusal naming the tag", err)
	}
	// With the declarations, ahead of the tree root's existence: a
	// nonexistent root and a malformed tag refuse for the tag.
	_, err = PrepareCampaign(context.Background(), CampaignInputs{FindingsPath: path, ModuleDir: filepath.Join(dir, "nonexistent"), Selection: Selection{Tags: []string{"not a tag!"}}})
	if err == nil || !strings.Contains(err.Error(), "not a tag!") {
		t.Fatalf("a malformed selection under a missing root = %v, want the tag refused first", err)
	}
	// The toolchain half of the declaration's shape, at the same
	// stage: ahead of the tree root's existence.
	_, err = PrepareCampaign(context.Background(), CampaignInputs{FindingsPath: path, ModuleDir: filepath.Join(dir, "nonexistent"), Selection: Selection{Toolchain: "not a toolchain!"}})
	if err == nil || !strings.Contains(err.Error(), "not a toolchain!") {
		t.Fatalf("a malformed toolchain under a missing root = %v, want the toolchain refused first", err)
	}
	if _, err := os.Stat(path + ".campaign"); !os.IsNotExist(err) {
		t.Fatalf("a refused preparation left the campaign lock: %v", err)
	}
}
