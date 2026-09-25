package gomutant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A measurement whose oracle reads outside the tree lands unverifiable
// with the refusal's attribution on every evidence row — the
// operation, its logged name, and the producing process's own
// directory, the one gofresh names on the union every row's state is
// read from — and the guidance and the explain clause carry it, so a
// reader is sent to the package whose process made the read
// (REQ-result-record, REQ-exec-oracle-guidance).
func TestUnverifiableEvidenceCarriesTheRefusalsAttribution(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":      "module example.com/attr\n\ngo 1.26.4\n",
		"p/p.go":      "package p\n\nfunc F(v int) int {\n\tif v > 0 {\n\t\treturn v\n\t}\n\treturn -v\n}\n",
		"p/p_test.go": "package p\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestF(t *testing.T) {\n\tif _, err := os.ReadDir(\"/\"); err != nil {\n\t\tt.Fatal(err)\n\t}\n\tif F(1) != 1 {\n\t\tt.Fatal()\n\t}\n}\n",
	}
	for name, content := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tree, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var guidance []OracleGuidance
	findings, err := tree.Run(context.Background(), []Target{{Symbol: "example.com/attr/p.F"}}, Options{Budget: 1, OracleTimeout: 2 * time.Minute,
		Guidance: func(g OracleGuidance) { guidance = append(guidance, g) }})
	if err != nil || len(findings) != 1 {
		t.Fatalf("run = %+v, %v", findings, err)
	}
	target := findings[0].TargetEvidence
	if !target.RuntimeUnverifiable || !strings.HasPrefix(target.RuntimeReason, "external directory input: /") {
		t.Fatalf("target evidence = %+v, want the read of / refused", target)
	}
	// The row is the record's portable form: the process's directory
	// is tree-relative, so the record reads the same in every checkout.
	if !strings.HasPrefix(target.RuntimeAttribution, "open ") || !strings.Contains(target.RuntimeAttribution, `"/"`) || !strings.HasSuffix(target.RuntimeAttribution, ` in "p"`) {
		t.Fatalf("attribution = %q, want the open of / in the package's own tree-relative directory", target.RuntimeAttribution)
	}
	for _, row := range findings[0].OracleEvidence {
		if row.RuntimeAttribution != target.RuntimeAttribution {
			t.Fatalf("oracle row %s attribution = %q, want the union's %q", row.Symbol, row.RuntimeAttribution, target.RuntimeAttribution)
		}
	}
	if len(guidance) != 1 || guidance[0].Attribution != target.RuntimeAttribution {
		t.Fatalf("guidance = %+v, want the finding's attribution carried", guidance)
	}
	// The unstable test is named with its own solo run's attribution:
	// the read of / under that test alone, in the same directory.
	if own := guidance[0].UnstableAttributions["example.com/attr/p.TestF"]; own != target.RuntimeAttribution || !strings.Contains(guidance[0].Suggestion, "example.com/attr/p.TestF ("+own+")") {
		t.Fatalf("unstable attributions = %v, suggestion %q; want the test named with its own", guidance[0].UnstableAttributions, guidance[0].Suggestion)
	}
	clauses := CommittableReasons(findings[0], dir, nil)
	want := "runtime-unverifiable evidence for example.com/attr/p.F: " + target.RuntimeReason + "; attributed to " + target.RuntimeAttribution
	found := false
	for _, c := range clauses {
		if c == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("explain clauses = %q, want %q", clauses, want)
	}
}

// A row's attribution is the observation's own where gofresh set one
// (a classification refusal), else the one a resolved-target refusal
// carries in the reason itself as state, split by gofresh's one
// implementation, and none where the reason names no attributed
// refusal (REQ-result-record).
func TestEvidenceAttributionPrefersTheObservationsThenTheReasons(t *testing.T) {
	resolved := `external runtime input target: /w/pkg/link — recorded path "pkg/link" resolves to "/srv/x" outside the tree`
	for _, c := range []struct{ observed, reason, want string }{
		{`open "/" in "/w/pkg"`, "external directory input: /", `open "/" in "/w/pkg"`},
		{"", resolved, `recorded path "pkg/link" resolves to "/srv/x" outside the tree`},
		{`open "/" in "/w/pkg"`, resolved, `open "/" in "/w/pkg"`},
		{"", "runtime input observations diverged", ""},
		{"", "", ""},
	} {
		if got := evidenceAttribution(c.observed, c.reason); got != c.want {
			t.Fatalf("evidenceAttribution(%q, %q) = %q, want %q", c.observed, c.reason, got, c.want)
		}
	}
}

// Both stampers carry the evidence's attribution beside its state: the
// fresh attach and the spliced union's restamp read one runtimeEvidence
// (REQ-result-record).
func TestRuntimeEvidenceStampsTheAttribution(t *testing.T) {
	re := runtimeEvidence{attribution: `open "/" in "/w/pkg"`}
	re.state.Reason, re.state.Unverifiable = "external directory input: /", true
	stamped := withRuntimeState(SubjectEvidence{Symbol: "p.F"}, re)
	if stamped.RuntimeAttribution != re.attribution || stamped.RuntimeReason != re.state.Reason || !stamped.RuntimeUnverifiable {
		t.Fatalf("withRuntimeState = %+v", stamped)
	}
	if attached := evidenceFromFingerprint("p.F", stamped.Fingerprint, re); attached.RuntimeAttribution != re.attribution || attached.RuntimeReason != re.state.Reason {
		t.Fatalf("evidenceFromFingerprint = %+v", attached)
	}
}

// A whole-record reason — a divergence, a proof not re-established —
// names no attributed refusal: the stamp clears every row's
// attribution, so explain names none under it (REQ-exec-observation).
func TestWholeRecordStampClearsTheAttribution(t *testing.T) {
	f := Finding{Symbol: "p.A",
		TargetEvidence: SubjectEvidence{Symbol: "p.A", RuntimeUnverifiable: true, RuntimeReason: "external directory input: /", RuntimeAttribution: `open "/" in "p"`},
		OracleEvidence: []SubjectEvidence{{Symbol: "p.TestA", RuntimeUnverifiable: true, RuntimeReason: "external directory input: /", RuntimeAttribution: `open "/" in "p"`}}}
	stampUnverifiable(&f, "runtime input observations diverged from the served record's completed-process union")
	for _, row := range append([]SubjectEvidence{f.TargetEvidence}, f.OracleEvidence...) {
		if row.RuntimeAttribution != "" || !row.RuntimeUnverifiable || !strings.HasPrefix(row.RuntimeReason, "runtime input observations diverged") {
			t.Fatalf("row %s after the whole-record stamp = %+v", row.Symbol, row)
		}
	}
	for _, clause := range CommittableReasons(f, t.TempDir(), nil) {
		if strings.Contains(clause, "attributed to") {
			t.Fatalf("a whole-record reason named an attribution: %q", clause)
		}
	}
}
