package gomutant

import (
	"context"
	"errors"
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

// The target inputs are read at their enumerated places: a second
// source refuses right after the bounds, the document's parse follows
// the exclusivity — behind the bounds' signs, ahead of the
// declarations — and the changed ref's surface is read only once the
// tree root is known to exist (REQ-exec-preparation).
func TestPrepareCampaignReadsTheTargetInputsInOrder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gomutant", "findings.json")
	malformed := filepath.Join(dir, "targets.json")
	if err := os.WriteFile(malformed, []byte(`{"nope":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, err := PrepareCampaign(ctx, CampaignInputs{FindingsPath: path, ModuleDir: dir, TargetSources: []string{"--targets", "--changed"}, Targets: TargetInputs{TargetsPath: malformed}})
	if err == nil || !strings.Contains(err.Error(), "--targets and --changed were given") {
		t.Fatalf("two sources = %v, want the exclusivity refusal ahead of the document's", err)
	}
	_, err = PrepareCampaign(ctx, CampaignInputs{FindingsPath: path, ModuleDir: dir, Budget: -1, Targets: TargetInputs{TargetsPath: malformed}})
	if err == nil || !strings.Contains(err.Error(), "budget must be non-negative") {
		t.Fatalf("a negative budget beside a malformed document = %v, want the bounds refused first", err)
	}
	for name, in := range map[string]CampaignInputs{
		"a malformed scratch namespace": {FindingsPath: path, ModuleDir: dir, ScratchNamespaces: []string{"no-colon"}, Targets: TargetInputs{TargetsPath: malformed}},
		"a malformed tag":               {FindingsPath: path, ModuleDir: dir, Selection: Selection{Tags: []string{"not a tag!"}}, Targets: TargetInputs{TargetsPath: malformed}},
	} {
		if _, err := PrepareCampaign(ctx, in); err == nil || !strings.Contains(err.Error(), "parse targets document") {
			t.Fatalf("a malformed document beside %s = %v, want the document refused first", name, err)
		}
	}
	// The confined form: an escaping spelling refuses where the document
	// is refused, before the root is resolved.
	_, err = PrepareCampaign(ctx, CampaignInputs{FindingsPath: path, ModuleDir: dir, Targets: TargetInputs{TargetsPath: "../escape.json", TargetsRoot: dir}})
	if err == nil || !strings.Contains(err.Error(), "escapes the tree") {
		t.Fatalf("an escaping confined path = %v, want the confinement refusal", err)
	}
	read := false
	reader := func(context.Context) (*ChangedSelection, error) {
		read = true
		return &ChangedSelection{Ref: func(string) ([]byte, bool) { return nil, false }}, nil
	}
	_, err = PrepareCampaign(ctx, CampaignInputs{FindingsPath: path, ModuleDir: filepath.Join(dir, "nonexistent"), Targets: TargetInputs{Changed: reader}})
	if err == nil || !strings.Contains(err.Error(), "tree root") || read {
		t.Fatalf("a changed reader under a missing root = %v (read %v), want the root refused and the reader never run", err, read)
	}
	// A root that is a file is refused as such.
	if _, err := PrepareCampaign(ctx, CampaignInputs{FindingsPath: path, ModuleDir: malformed}); err == nil || !strings.Contains(err.Error(), "is not a directory") {
		t.Fatalf("a file as the tree root = %v, want the not-a-directory refusal", err)
	}
	// A refused surface read strands nothing: it precedes the lock.
	refusing := func(context.Context) (*ChangedSelection, error) { return nil, errors.New("no such ref") }
	if _, err := PrepareCampaign(ctx, CampaignInputs{FindingsPath: path, ModuleDir: dir, Targets: TargetInputs{Changed: refusing}}); err == nil || !strings.Contains(err.Error(), "no such ref") {
		t.Fatalf("a refusing surface read = %v", err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("a refused surface read minted the campaign directory: %v", err)
	}
	// Discovery's preparation keeps the same order without a lock: the
	// sources' exclusivity, the document, the root, then the surface.
	read = false
	if _, err := PrepareSelection(ctx, filepath.Join(dir, "nonexistent"), nil, TargetInputs{Changed: reader}, nil, nil); err == nil || !strings.Contains(err.Error(), "tree root") || read {
		t.Fatalf("discovery's reader under a missing root = %v (read %v), want the root refused first", err, read)
	}
	if _, err := PrepareSelection(ctx, dir, []string{"a", "b"}, TargetInputs{TargetsPath: malformed}, nil, nil); err == nil || !strings.Contains(err.Error(), "a and b were given") {
		t.Fatalf("discovery's two sources = %v, want the exclusivity refusal first", err)
	}
	if _, err := ConfineToTree("targets_path", dir, ".."); err == nil {
		t.Fatal("the literal .. escaped the tree")
	}
	prepared, err := PrepareCampaign(ctx, CampaignInputs{FindingsPath: path, ModuleDir: dir, Plan: true, Targets: TargetInputs{Changed: reader}, Packages: []string{"p"}, CutChanged: true})
	if err != nil || !read || prepared.Request.Changed == nil || !prepared.Request.Cut || len(prepared.Request.Packages) != 1 {
		t.Fatalf("prepared request = %+v, %v (read %v); want the surface read and the filters carried", prepared, err, read)
	}
	if _, err := os.Stat(path + ".campaign"); !os.IsNotExist(err) {
		t.Fatalf("a refused preparation left the campaign lock: %v", err)
	}
}
