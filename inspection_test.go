package gomutant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The one inspection walk: the recorded facts answer without a tree,
// the state filter runs after the judgment (a recorded row never
// matches a judged state), the layer is judged per row and counted, the
// cut splits each row's open survivors exactly when one was given, and
// the rows come back sorted by symbol (REQ-result-inspection).
func TestInspectDocumentIsTheOneWalk(t *testing.T) {
	root := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":      "module example.com/ins\n\ngo 1.26.4\n",
		"ins.go":      "package ins\n\nfunc Value(x int) int {\n\tif x > 10 {\n\t\treturn 1\n\t}\n\treturn 2\n}\n",
		"ins_test.go": "package ins\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) { if Value(0) != 2 { t.Fatal() } }\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tree, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(filepath.Join(root, ".gomutant", "findings.json"), root)
	if err != nil {
		t.Fatal(err)
	}
	survivor := func(pos string) Survivor {
		return Survivor{Position: pos, Operator: "zero return", Site: "aaaa1111aaaa1111"}
	}
	ctx := context.Background()
	// The cut places survivors only over the body the record measured.
	body, err := tree.eng.BodyHashContext(ctx, "example.com/ins.Value")
	if err != nil {
		t.Fatal(err)
	}
	records := []Finding{
		storeFinding("example.com/ins.Zed", func(f *Finding) { f.Dirty = true }),
		storeFinding("example.com/ins.Value", func(f *Finding) {
			f.BodyHash = body
			f.Killed = 0
			f.Survivors = []Survivor{survivor("ins.go:4:2"), survivor("ins.go:7:2")}
			f.Operators = []OperatorSummary{{Operator: "zero return", Generated: 2, Survived: 2}}
			f.Generated, f.Mutants, f.CandidateCount = 2, 2, 2
		}),
	}
	plain, err := InspectDocument(ctx, nil, store, records, InspectionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plain.Rows) != 2 || plain.Rows[0].Finding.Symbol != "example.com/ins.Value" || plain.Rows[1].Finding.Symbol != "example.com/ins.Zed" {
		t.Fatalf("rows = %+v, want both, sorted by symbol", plain.Rows)
	}
	if plain.Repo != 1 || plain.Local != 1 || plain.Rows[1].Layer != "local" || plain.Rows[1].LayerReason == "" || plain.Rows[0].Delta != nil {
		t.Fatalf("plain walk: repo %d local %d rows %+v", plain.Repo, plain.Local, plain.Rows)
	}
	if plain.Rows[0].Inspection.State != FindingRecorded {
		t.Fatalf("an unjudged walk reported %s, want the recorded state", plain.Rows[0].Inspection.State)
	}
	// A state filter without a judgment keeps nothing: recorded rows
	// never match a judged state.
	filtered, err := InspectDocument(ctx, nil, store, records, InspectionRequest{State: string(FindingStale)})
	if err != nil || len(filtered.Rows) != 0 || filtered.Repo != 0 || filtered.Local != 0 {
		t.Fatalf("state filter over recorded rows = %+v, %v; want nothing kept and nothing counted", filtered, err)
	}
	// A judged walk derives every row's state against the tree — the
	// synthetic evidence never proves reuse — and a state filter keeps a
	// row by that judged state.
	judged, err := InspectDocument(ctx, tree, store, records, InspectionRequest{Judge: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(judged.Rows) != 2 || judged.Rows[0].Inspection.State == FindingRecorded {
		t.Fatalf("judged walk = %+v, want a judged state on every row", judged.Rows)
	}
	kept, err := InspectDocument(ctx, tree, store, records, InspectionRequest{Judge: true, State: string(judged.Rows[0].Inspection.State)})
	if err != nil || len(kept.Rows) != 1 || kept.Rows[0].Finding.Symbol != "example.com/ins.Value" {
		t.Fatalf("judged state filter = %+v, %v; want the one row in that state", kept.Rows, err)
	}
	// Judging or cutting without a tree is refused, never dereferenced.
	if _, err := InspectDocument(ctx, nil, store, records, InspectionRequest{Judge: true}); err == nil {
		t.Fatal("a judged walk without a tree was not refused")
	}
	// The cut splits every row's open survivors by the delta's lines.
	cut := &DeltaCut{Ref: "HEAD", Added: map[string][]LineRange{"ins.go": {{From: 4, To: 5}}}, Changed: map[string]bool{"example.com/ins.Value": true}}
	split, err := InspectDocument(ctx, tree, store, records, InspectionRequest{Cut: cut})
	if err != nil {
		t.Fatal(err)
	}
	if split.Rows[0].Delta == nil || len(split.Rows[0].Delta.OnDelta) != 1 || split.Rows[0].Delta.OnDelta[0].Position != "ins.go:4:2" || split.Rows[1].Delta == nil {
		t.Fatalf("cut rows = %+v, want the survivor on line 4 alone on the delta and every row split", split.Rows)
	}
	if rest := split.Rows[0].Delta.Remainder; len(rest) != 1 || rest[0].Position != "ins.go:7:2" {
		t.Fatalf("cut remainder = %+v, want the survivor on line 7", rest)
	}
}

// The inspection's inputs are refused at the request: a state that is
// not a judged state, and the zero-row note names the input that
// emptied the roster (REQ-exec-preparation, REQ-mcp-envelope).
func TestInspectionInputsAndNotes(t *testing.T) {
	for _, ok := range []string{"", "current", "stale", "unverifiable", "detached"} {
		if err := ValidateFindingState(ok); err != nil {
			t.Fatalf("state %q refused: %v", ok, err)
		}
	}
	if err := ValidateFindingState("recorded"); err == nil || !strings.Contains(err.Error(), `unknown state "recorded"`) {
		t.Fatalf("an unjudged state accepted: %v", err)
	}
	cases := []struct {
		recorded, matched int
		filter            RecordFilter
		state, want       string
	}{
		{0, 0, RecordFilter{}, "", "no findings recorded at doc - run measures the tree first"},
		{3, 0, RecordFilter{Label: "L", Symbol: "S"}, "", "the label/symbol filters matched none of 3 recorded finding(s); drop them to list the document"},
		{3, 0, RecordFilter{Run: "r"}, "", "the run filter matched none of 3 recorded finding(s); drop it to list the document"},
		{3, 2, RecordFilter{Run: "r"}, "stale", "state=stale matched none of the 2 finding(s) the other filters kept; drop it to list them"},
		{3, 2, RecordFilter{}, "", ""},
	}
	for _, c := range cases {
		if got := NoRecordsNote("doc", c.recorded, c.matched, c.filter, c.state); got != c.want {
			t.Fatalf("note(%d,%d,%+v,%q) = %q, want %q", c.recorded, c.matched, c.filter, c.state, got, c.want)
		}
	}
}
