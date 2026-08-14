package gomutant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The committed exemption record validates at the boundary: a missing
// file is an empty record, and a malformed one - wrong version, or an
// entry missing its subject, reason, or rationale - refuses instead of
// silently classifying without review (REQ-result-exemptions).
func TestLoadExemptionsValidation(t *testing.T) {
	dir := t.TempDir()
	if got, err := LoadExemptions(filepath.Join(dir, "absent.json")); err != nil || got != nil {
		t.Fatalf("missing record = %v, %v; want empty", got, err)
	}
	for name, content := range map[string]string{
		"wrong version":     `{"version":2,"exemptions":[]}`,
		"missing rationale": `{"version":1,"exemptions":[{"subject":"p.TestX","reason":"r"}]}`,
		"missing reason":    `{"version":1,"exemptions":[{"subject":"p.TestX","rationale":"why"}]}`,
		"missing subject":   `{"version":1,"exemptions":[{"reason":"r","rationale":"why"}]}`,
		"garbage":           `{`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "exemptions.json")
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadExemptions(path); err == nil {
				t.Fatalf("malformed record accepted: %s", content)
			}
		})
	}
	path := filepath.Join(dir, "exemptions.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"exemptions":[{"subject":"p.TestX","reason":"r","rationale":"why"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadExemptions(path)
	if err != nil || len(got) != 1 || got[0] != (Exemption{Subject: "p.TestX", Reason: "r", Rationale: "why"}) {
		t.Fatalf("valid record = %+v, %v", got, err)
	}
}

// A reviewed exemption lifts exactly the portable line's unverifiable
// clause - matched on the exact subject and reason, with the target's
// union evidence inheriting an oracle entry under the same reason -
// and a drifted reason or unnamed subject lifts nothing
// (REQ-result-exemptions).
func TestExemptionLiftsUnverifiableClauseExactly(t *testing.T) {
	dir := t.TempDir()
	f := Finding{
		Symbol: "example.com/m.F",
		Commit: "c0ffee", Dirty: false,
		TargetEvidence: SubjectEvidence{Symbol: "example.com/m.F", RuntimeUnverifiable: true, RuntimeReason: "runtime input not covered by observation bracket: go.mod"},
		OracleEvidence: []SubjectEvidence{{Symbol: "example.com/m.TestF", RuntimeUnverifiable: true, RuntimeReason: "runtime input not covered by observation bracket: go.mod"}},
	}
	record := []Exemption{{Subject: "example.com/m.TestF", Reason: "runtime input not covered by observation bracket: go.mod", Rationale: "repo-root walk; go.mod movement is subsumed by the build-config pin"}}
	if ok, reason := Committable(f, dir, record); !ok {
		t.Fatalf("exempted finding stays local: %s", reason)
	}
	if ok, reason := Committable(f, dir, nil); ok || !strings.Contains(reason, "runtime-unverifiable evidence") {
		t.Fatalf("unexempted finding = %v %q, want the unverifiable clause", ok, reason)
	}
	drifted := append([]Exemption(nil), record...)
	drifted[0].Reason = "runtime input not covered by observation bracket: go.sum"
	if ok, _ := Committable(f, dir, drifted); ok {
		t.Fatal("a drifted reason still lifted the clause")
	}
	other := []Exemption{{Subject: "example.com/m.TestOther", Reason: "runtime input not covered by observation bracket: go.mod", Rationale: "x"}}
	if ok, _ := Committable(f, dir, other); ok {
		t.Fatal("an unnamed subject's entry lifted the clause")
	}
	partial := Finding{
		Symbol:         f.Symbol,
		Commit:         "c0ffee",
		TargetEvidence: f.TargetEvidence,
		OracleEvidence: append(append([]SubjectEvidence{}, f.OracleEvidence...),
			SubjectEvidence{Symbol: "example.com/m.TestG", RuntimeUnverifiable: true, RuntimeReason: "some other instability"}),
	}
	if ok, _ := Committable(partial, dir, record); ok {
		t.Fatal("a finding with an unaccepted unverifiable subject still committed")
	}
}

// The store consumes the record beside its findings document as the
// live authority (REQ-result-exemptions): the same finding classifies
// repo with the committed entry present and local after its removal -
// revocation bites without touching the stamp.
func TestStoreLayerConsultsCommittedExemptionRecord(t *testing.T) {
	dir := t.TempDir()
	docPath := filepath.Join(dir, ".gomutant", "findings.json")
	if err := os.MkdirAll(filepath.Dir(docPath), 0o755); err != nil {
		t.Fatal(err)
	}
	f := Finding{
		Symbol:         "example.com/m.F",
		Commit:         "c0ffee",
		TargetEvidence: SubjectEvidence{Symbol: "example.com/m.F", RuntimeUnverifiable: true, RuntimeReason: "sealed reason"},
	}
	record := `{"version":1,"exemptions":[{"subject":"example.com/m.F","reason":"sealed reason","rationale":"reviewed"}]}`
	if err := os.WriteFile(ExemptionsPathFor(docPath), []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(docPath, dir)
	if err != nil {
		t.Fatal(err)
	}
	if layer, reason := store.Layer(f); layer != "repo" {
		t.Fatalf("exempted finding layered %s (%s), want repo", layer, reason)
	}
	if err := os.Remove(ExemptionsPathFor(docPath)); err != nil {
		t.Fatal(err)
	}
	store, err = OpenStore(docPath, dir)
	if err != nil {
		t.Fatal(err)
	}
	if layer, _ := store.Layer(f); layer != "local" {
		t.Fatalf("revoked exemption still layers repo")
	}
}

// End to end: the repo-root walk idiom seals a subpackage oracle's
// evidence as out-of-bracket; the reviewed exemption restores normal
// survivor bucketing and stamps the finding, while the unexempted run
// buckets unstable-oracle (REQ-result-exemptions,
// REQ-exec-survivor-evidence).
func TestExemptionRestoresSurvivorBuckets(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	build := func() *Tree {
		dir := t.TempDir()
		files := map[string]string{
			"go.mod":   "module example.com/envp\n\ngo 1.26.4\n",
			"sub/p.go": "package sub\n\nfunc F(x int) int {\n\tif x > 100 {\n\t\treturn x - 1\n\t}\n\treturn x\n}\n",
			"sub/p_test.go": `package sub

import (
	"os"
	"path/filepath"
	"testing"
)

func TestF(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for d := wd; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			break
		}
		if filepath.Dir(d) == d {
			t.Fatal("no go.mod above working directory")
		}
	}
	if F(5) != 5 {
		t.Fatal()
	}
}
`,
		}
		for name, content := range files {
			path := filepath.Join(dir, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		tree, err := Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		return tree
	}
	target := []Target{{Symbol: "example.com/envp/sub.F", Oracle: []string{"example.com/envp/sub.TestF"}, OracleExplicit: true}}
	sealedReason := "runtime input not covered by observation bracket: go.mod"
	record := []Exemption{{Subject: "example.com/envp/sub.TestF", Reason: sealedReason, Rationale: "repo-root walk; go.mod movement is subsumed by the build-config pin"}}

	bare, err := build().Run(context.Background(), target, Options{Budget: 2, OracleTimeout: 2 * time.Minute})
	if err != nil || len(bare) != 1 {
		t.Fatalf("bare measure = %+v, %v", bare, err)
	}
	if !bare[0].TargetEvidence.RuntimeUnverifiable || bare[0].TargetEvidence.RuntimeReason != sealedReason {
		t.Fatalf("fixture no longer seals as expected: %+v", bare[0].TargetEvidence)
	}
	if len(bare[0].Survivors) == 0 || bare[0].Survivors[0].Execution != "unstable-oracle" {
		t.Fatalf("unexempted survivors = %+v, want unstable-oracle", bare[0].Survivors)
	}
	if bare[0].Exempted != nil {
		t.Fatalf("unexempted finding carries a stamp: %+v", bare[0].Exempted)
	}

	exempted, err := build().Run(context.Background(), target, Options{Budget: 2, OracleTimeout: 2 * time.Minute, Exemptions: record})
	if err != nil || len(exempted) != 1 {
		t.Fatalf("exempted measure = %+v, %v", exempted, err)
	}
	if len(exempted[0].Survivors) == 0 {
		t.Fatal("no survivors to classify")
	}
	for _, s := range exempted[0].Survivors {
		if s.Execution == "unstable-oracle" {
			t.Fatalf("exempted survivor still bucketed unstable-oracle: %+v", s)
		}
	}
	if len(exempted[0].Exempted) == 0 || exempted[0].Exempted[0].Subject != record[0].Subject {
		t.Fatalf("finding not stamped with the accepted entries: %+v", exempted[0].Exempted)
	}
}

// The stamp is derived state: re-stamping under a record that no
// longer accepts the evidence clears it rather than carrying an
// acceptance past the record that justified it (REQ-result-exemptions).
func TestStampExemptionsClearsOnRevocation(t *testing.T) {
	f := Finding{
		TargetEvidence: SubjectEvidence{Symbol: "p.F", RuntimeUnverifiable: true, RuntimeReason: "r"},
		Exempted:       []Exemption{{Subject: "p.F", Reason: "r", Rationale: "old"}},
	}
	stampExemptions(&f, nil)
	if f.Exempted != nil {
		t.Fatalf("revoked stamp survived: %+v", f.Exempted)
	}
}

// A repo row whose exemption was revoked is not portable truth: the
// next reconciliation evicts it instead of stranding a row the layer
// contract forbids in the committed document
// (REQ-result-layers, REQ-result-exemptions).
func TestUpdateEvictsRevokedRepoRow(t *testing.T) {
	dir := t.TempDir()
	docPath := filepath.Join(dir, ".gomutant", "findings.json")
	if err := os.MkdirAll(filepath.Dir(docPath), 0o755); err != nil {
		t.Fatal(err)
	}
	f := storeFinding("example.com/m.F", func(f *Finding) {
		f.TargetEvidence.RuntimeUnverifiable = true
		f.TargetEvidence.RuntimeReason = "sealed reason"
		for i := range f.OracleEvidence {
			f.OracleEvidence[i].RuntimeUnverifiable = true
			f.OracleEvidence[i].RuntimeReason = "sealed reason"
		}
	})
	record := `{"version":1,"exemptions":[{"subject":"example.com/m.FTest","reason":"sealed reason","rationale":"reviewed"}]}`
	if err := os.WriteFile(ExemptionsPathFor(docPath), []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(docPath, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(context.Background(), func(prior []Finding) ([]Finding, error) {
		return []Finding{f}, nil
	}); err != nil {
		t.Fatal(err)
	}
	repo, _, err := store.Committability(context.Background())
	if err != nil || repo != 1 {
		t.Fatalf("exempted row not committed: repo=%d err=%v", repo, err)
	}
	if err := os.Remove(ExemptionsPathFor(docPath)); err != nil {
		t.Fatal(err)
	}
	store, err = OpenStore(docPath, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(context.Background(), func(prior []Finding) ([]Finding, error) {
		return []Finding{f}, nil
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "example.com/m.F") {
		t.Fatalf("revoked row stranded in the repo document:\n%s", data)
	}
}

// An exempted-unverifiable record never serves: reuse is untouched by
// the record, so every machine re-measures the accepted finding
// (REQ-result-exemptions).
func TestExemptedRecordStillNeverServes(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":        "module example.com/envp\n\ngo 1.26.4\n",
		"sub/p.go":      "package sub\n\nfunc F(x int) int {\n\tif x > 100 {\n\t\treturn x - 1\n\t}\n\treturn x\n}\n",
		"sub/p_test.go": "package sub\n\nimport (\n\t\"os\"\n\t\"path/filepath\"\n\t\"testing\"\n)\n\nfunc TestF(t *testing.T) {\n\tif _, err := os.Stat(filepath.Join(\"..\", \"go.mod\")); err != nil {\n\t\tt.Fatal(err)\n\t}\n\tif F(5) != 5 {\n\t\tt.Fatal()\n\t}\n}\n",
	}
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tree, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	target := []Target{{Symbol: "example.com/envp/sub.F", Oracle: []string{"example.com/envp/sub.TestF"}, OracleExplicit: true}}
	record := []Exemption{{Subject: "example.com/envp/sub.TestF", Reason: "runtime input not covered by observation bracket: go.mod", Rationale: "reviewed"}}
	first, err := tree.Run(context.Background(), target, Options{Budget: 1, OracleTimeout: 2 * time.Minute, Exemptions: record})
	if err != nil || len(first) != 1 {
		t.Fatalf("first = %+v, %v", first, err)
	}
	if !first[0].TargetEvidence.RuntimeUnverifiable {
		t.Fatalf("fixture no longer seals: %+v", first[0].TargetEvidence)
	}
	var decisions []string
	_, err = tree.Run(context.Background(), target, Options{
		Budget: 1, OracleTimeout: 2 * time.Minute, Exemptions: record, Prior: first,
		Decision: func(d RunDecision) { decisions = append(decisions, d.Action+" "+d.Reason) },
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range decisions {
		if strings.HasPrefix(d, "served") {
			t.Fatalf("exempted unverifiable record served: %q", d)
		}
	}
	if len(decisions) == 0 {
		t.Fatal("no decision observed")
	}
}

// The delta-path counts folds consult the record exactly as the fresh
// path does: an unverifiable-but-accepted record's re-executed
// survivors keep normal buckets (REQ-exec-survivor-evidence,
// REQ-result-exemptions).
func TestDeltaCountsFoldsConsultExemptions(t *testing.T) {
	rec := Finding{
		Symbol:         "example.com/m.F",
		TargetEvidence: SubjectEvidence{Symbol: "example.com/m.F", RuntimeUnverifiable: true, RuntimeReason: "r"},
		OracleEvidence: []SubjectEvidence{{Symbol: "example.com/m.TestF", RuntimeUnverifiable: true, RuntimeReason: "r"}},
	}
	record := []Exemption{{Subject: "example.com/m.TestF", Reason: "r", Rationale: "reviewed"}}
	if unstableForBuckets(&rec, record) {
		t.Fatal("accepted record still judged unstable")
	}
	if !unstableForBuckets(&rec, nil) {
		t.Fatal("unaccepted record judged stable")
	}
}
