package mcpserver

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	gomutant "github.com/greatliontech/gomutant"
)

// The lifecycle verbs ride the protocol face: prune removes
// resolved-dead records echoing their dispositions, retarget rewrites
// symbol identity, both with check previews (REQ-mcp-lifecycle).
func TestToolPruneAndRetarget(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
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
	renamed.TargetEvidence.ObservationProof.Subject.Package = "example.com/old"
	// Seeded through the store: the dirty records land in the overlay,
	// the layer the verbs keep them in.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	seed, err := gomutant.OpenStore(filepath.Join(dir, gomutant.DefaultFindingsPath), dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.Update(context.Background(), func([]gomutant.Finding) ([]gomutant.Finding, error) {
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
	// A reviewed entry no record carries rides the response as stale.
	if err := os.WriteFile(gomutant.ExemptionsPathFor(filepath.Join(dir, gomutant.DefaultFindingsPath)), []byte(`{"version":1,"exemptions":[{"subject":"example.com/old.TestUnmeasured","reason":"r","rationale":"why"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, rOut, err := s.toolRetarget(ctx, nil, retargetIn{From: "example.com/old.", To: "example.com/life."})
	if err != nil || len(rOut.Rewritten) != 1 || rOut.Rewritten[0].To != "example.com/life.F" || rOut.Rewritten[0].Layer != gomutant.LayerLocal || !reflect.DeepEqual(rOut.StaleExemptions, []string{"example.com/old.TestUnmeasured"}) {
		t.Fatalf("retarget = %+v, %v", rOut, err)
	}
	// The shadow statement on the wire: a committed row renamed onto a
	// symbol the overlay holds.
	shadowed := seededFinding("example.com/old2.F")
	shadowed.TargetEvidence.ObservationProof.Subject.Package = "example.com/old2"
	shadowed.Dirty, shadowed.Commit = false, "abc"
	shadowed.TargetEvidence.RuntimeInputs, shadowed.OracleEvidence[0].RuntimeInputs = "eyJ2IjoxfQ", "eyJ2IjoxfQ"
	if err := seed.Update(context.Background(), func(prior []gomutant.Finding) ([]gomutant.Finding, error) { return append(prior, shadowed), nil }); err != nil {
		t.Fatal(err)
	}
	_, sOut, err := s.toolRetarget(ctx, nil, retargetIn{From: "example.com/old2.", To: "example.com/life."})
	if err != nil || len(sOut.Rewritten) != 1 || sOut.Rewritten[0].Layer != gomutant.LayerRepo || !sOut.Rewritten[0].Shadowed {
		t.Fatalf("shadowed retarget = %+v, %v", sOut, err)
	}
	// A touched row reaches the wire whole: a record outside the rename
	// whose killer carries the prefix.
	killed := seededFinding("example.com/life.G")
	killed.Killed, killed.Mutants, killed.CandidateCount, killed.Generated = 1, 1, 1, 1
	killed.Kills = []gomutant.Kill{{Position: "p.go:1:1", Operator: "zero return", Killer: "example.com/gone.TestHelper"}}
	killed.Operators = []gomutant.OperatorSummary{{Operator: "zero return", Generated: 1, Killed: 1}}
	if err := seed.Update(context.Background(), func(prior []gomutant.Finding) ([]gomutant.Finding, error) { return append(prior, killed), nil }); err != nil {
		t.Fatal(err)
	}
	_, tOut, err := s.toolRetarget(ctx, nil, retargetIn{From: "example.com/gone.", To: "example.com/moved."})
	if err != nil || tOut.Touched != 1 || len(tOut.TouchedRewrites) != 1 || tOut.TouchedRewrites[0] != (touchedOut{Record: "example.com/life.G", Layer: gomutant.LayerLocal, From: "example.com/gone.TestHelper", To: "example.com/moved.TestHelper"}) {
		t.Fatalf("touched retarget = %+v, %v", tOut, err)
	}
	// The stale roster caps like every row list, the remainder counted
	// (REQ-mcp-envelope).
	var entries []string
	for i := 0; i < 60; i++ {
		entries = append(entries, fmt.Sprintf(`{"subject":"example.com/old3.Test%02d","reason":"r","rationale":"why"}`, i))
	}
	if err := os.WriteFile(gomutant.ExemptionsPathFor(filepath.Join(dir, gomutant.DefaultFindingsPath)), []byte(`{"version":1,"exemptions":[`+strings.Join(entries, ",")+`]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, cOut, err := s.toolRetarget(ctx, nil, retargetIn{From: "example.com/old3.", To: "example.com/life.", Check: true})
	if err != nil || len(cOut.StaleExemptions) != 50 || cOut.OmittedStaleExemptions != 10 {
		t.Fatalf("stale roster = %d listed, %d omitted, %v", len(cOut.StaleExemptions), cOut.OmittedStaleExemptions, err)
	}

	_, pPreview, err := s.toolPrune(ctx, nil, pruneIn{Check: true})
	if err != nil || !pPreview.Check || len(pPreview.Removed) != 1 || pPreview.Removed[0].Symbol != "example.com/life.Gone" {
		t.Fatalf("prune preview = %+v, %v", pPreview, err)
	}
	_, pOut, err := s.toolPrune(ctx, nil, pruneIn{})
	// Kept counts records per layer: the committed life.F beside the
	// overlay's, and the touched life.G.
	if err != nil || len(pOut.Removed) != 1 || pOut.Kept != 3 {
		t.Fatalf("prune = %+v, %v", pOut, err)
	}
	if pOut.Removed[0].Layer != gomutant.LayerLocal {
		t.Fatalf("prune row layer = %q, want the overlay the dirty record sat in", pOut.Removed[0].Layer)
	}
	if len(pOut.Removed[0].Attested) != 1 || !strings.Contains(pOut.Removed[0].Attested[0].Reason, "equivalent by inspection") {
		t.Fatalf("prune response lost the disposition echo: %+v", pOut.Removed[0])
	}
	all, err := s.loadFindings("")
	if err != nil || len(all) != 2 || all[0].Symbol != "example.com/life.F" || all[1].Symbol != "example.com/life.G" {
		t.Fatalf("document after lifecycle verbs = %d records, %v", len(all), err)
	}
}

// The prune echo is never truncated - a removal echo is
// promote-then-delete, and truncation would silently drop dispositions
// - while retarget's echo caps with the remainder counted
// (REQ-mcp-lifecycle).
func TestToolLifecycleEchoBounds(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
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
		f.TargetEvidence.ObservationProof.Subject.Package = "example.com/old"
		seed = append(seed, f)
	}
	if err := gomutant.UpdateDocument(context.Background(), filepath.Join(dir, gomutant.DefaultFindingsPath), func([]gomutant.Finding) ([]gomutant.Finding, error) {
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

// The keepalive ping is the disconnect detector for in-flight
// campaigns: without it a client that died mid-run leaves the server
// measuring detached for the campaign's full duration
// (REQ-mcp-lifecycle).
func TestServerOptionsCarryKeepalive(t *testing.T) {
	if opts := serverOptions(); opts.KeepAlive != clientKeepAliveInterval || opts.KeepAlive <= 0 {
		t.Fatalf("server keepalive = %v, want the configured positive interval - a dead transport must cancel in-flight campaigns", opts.KeepAlive)
	}
}

// Machine-local input clauses sharing a top-level directory roll up on
// BOTH explain arms - the symbol row and the promotion triage grouping
// (REQ-mcp-explain).
func TestToolExplainRollsUpSameRootInputs(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/roll\n\ngo 1.26.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "r.go"), []byte("package roll\n\nfunc Value() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "r_test.go"), []byte("package roll\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) { if Value() != 1 { t.Fatal() } }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	s := New(dir)
	ctx := context.Background()

	entries := make([]string, 3)
	for i := range entries {
		entries[i] = fmt.Sprintf(`{"k":"abs","p":"/leaked/tmp%d/f","d":"0123456789abcdef0123456789abcdef"}`, i)
	}
	manifest := base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"paths":[` + strings.Join(entries, ",") + `]}`))
	f := seededFinding("example.com/roll.Value")
	f.Commit = "abc"
	f.TargetEvidence.RuntimeInputs = manifest
	f.OracleEvidence[0].RuntimeInputs = manifest
	if err := gomutant.UpdateDocument(context.Background(), filepath.Join(dir, gomutant.DefaultFindingsPath), func([]gomutant.Finding) ([]gomutant.Finding, error) {
		return []gomutant.Finding{f}, nil
	}); err != nil {
		t.Fatal(err)
	}

	_, out, err := s.toolExplain(ctx, nil, explainIn{Symbol: "example.com/roll.Value"})
	if err != nil {
		t.Fatal(err)
	}
	rolled := false
	for _, r := range out.LayerReasons {
		if r == "machine-local runtime inputs under /leaked (3 paths)" {
			rolled = true
		}
		if strings.HasPrefix(r, "machine-local runtime input /leaked") {
			t.Fatalf("per-path clause escaped the roll-up: %q", out.LayerReasons)
		}
	}
	if !rolled {
		t.Fatalf("no rolled clause on the symbol arm: %q", out.LayerReasons)
	}

	_, triage, err := s.toolExplain(ctx, nil, explainIn{})
	if err != nil {
		t.Fatal(err)
	}
	rolled = false
	for _, g := range triage.Promotion {
		if g.Reason == "machine-local runtime inputs under /leaked (3 paths)" {
			rolled = true
		}
	}
	if !rolled {
		t.Fatalf("no rolled group on the triage arm: %+v", triage.Promotion)
	}
}

// The campaign lock rides the MCP run face, and the short document
// operations stay available while a campaign holds it
// (REQ-exec-exclusivity).
func TestToolRunRefusesWhileCampaignLockHeldAndShortOpsProceed(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
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
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	s := New(dir)
	ctx := context.Background()
	release, err := gomutant.AcquireCampaignLock(gomutant.FindingsPathAt(s.dir, ""))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, _, err := s.toolRun(ctx, nil, runIn{Symbols: []string{"example.com/life.F"}}); err == nil || !strings.Contains(err.Error(), "already holds") {
		t.Fatalf("run under a held campaign lock = %v, want the fail-fast refusal", err)
	}
	// A lifecycle verb serializes under the document lock alone.
	if _, _, err := s.toolPrune(ctx, nil, pruneIn{Check: true}); err != nil {
		t.Fatalf("prune under a held campaign lock refused: %v", err)
	}
}

// The lifecycle wire rows are hand-copied projections of the library's
// records: a record filled in every field by reflection projects to a
// row carrying each value under the same name, so a field the library
// grows cannot miss the wire silently (REQ-mcp-lifecycle).
func TestLifecycleWireRowsProjectEveryRecordField(t *testing.T) {
	fill := func(v reflect.Value) {
		for i := 0; i < v.NumField(); i++ {
			f := v.Field(i)
			switch f.Kind() {
			case reflect.String:
				f.SetString(v.Type().Field(i).Name + "-value")
			case reflect.Bool:
				f.SetBool(true)
			case reflect.Int:
				f.SetInt(int64(i) + 1)
			case reflect.Slice:
				f.Set(reflect.MakeSlice(f.Type(), 1, 1))
			default:
				t.Fatalf("%s.%s: unhandled kind %s — extend the filler", v.Type().Name(), v.Type().Field(i).Name, f.Kind())
			}
		}
	}
	check := func(record, row reflect.Value) {
		t.Helper()
		for i := 0; i < record.NumField(); i++ {
			name := record.Type().Field(i).Name
			got := row.FieldByName(name)
			if !got.IsValid() {
				t.Errorf("%s.%s reaches no field of %s", record.Type().Name(), name, row.Type().Name())
				continue
			}
			if !reflect.DeepEqual(got.Interface(), record.Field(i).Interface()) {
				t.Errorf("%s.%s = %v on the wire, want %v", record.Type().Name(), name, got.Interface(), record.Field(i).Interface())
			}
		}
	}
	var pruned gomutant.PrunedRecord
	fill(reflect.ValueOf(&pruned).Elem())
	check(reflect.ValueOf(pruned), reflect.ValueOf(prunedRow(pruned)))
	var rewritten gomutant.RetargetedRecord
	fill(reflect.ValueOf(&rewritten).Elem())
	check(reflect.ValueOf(rewritten), reflect.ValueOf(rewrittenRow(rewritten)))
	var touched gomutant.TouchedRewrite
	fill(reflect.ValueOf(&touched).Elem())
	check(reflect.ValueOf(touched), reflect.ValueOf(touchedRow(touched)))
}
