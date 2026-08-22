package gomutant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A tree edit between load and generation refuses the touched target
// LOCALLY with the drift named — never a mutant spliced at stale
// offsets into moved bytes, never a campaign abort — while a sibling
// whose source held keeps its ordinary measurement
// (REQ-exec-quiescence's target-local drift refusal; the field
// failure was a mid-campaign edit generating an unparseable candidate
// that killed the whole run).
func TestRunSkipsTargetWhenSourceDriftsAfterLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	dir := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/drift\n\ngo 1.26\n")
	write("a/a.go", "package a\n\nfunc Double(v int) int {\n\treturn v * 2\n}\n")
	write("a/a_test.go", "package a\n\nimport \"testing\"\n\nfunc TestDouble(t *testing.T) {\n\tif Double(3) != 6 {\n\t\tt.Fatal(\"double\")\n\t}\n}\n")
	write("b/b.go", "package b\n\nfunc Triple(v int) int {\n\treturn v * 3\n}\n")
	write("b/b_test.go", "package b\n\nimport \"testing\"\n\nfunc TestTriple(t *testing.T) {\n\tif Triple(3) != 9 {\n\t\tt.Fatal(\"triple\")\n\t}\n}\n")

	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The mid-run edit: package a's source moves AFTER the load — a
	// same-length edit would be equally caught (the guard is a content
	// digest, not a size check).
	write("a/a.go", "package a\n\nfunc Double(v int) int {\n\treturn 2 * v\n}\n")

	var decisions []RunDecision
	_, err = tr.Run(context.Background(), []Target{
		{Symbol: "example.com/drift/a.Double"},
		{Symbol: "example.com/drift/b.Triple"},
	}, Options{Decision: func(d RunDecision) { decisions = append(decisions, d) }})
	// The contracted shape: the run fails OPERATIONALLY (a pipeline
	// never reads a partial campaign as success) with the drift named
	// and the completed remainder kept — never the old campaign abort
	// from a garbage candidate ("format candidate ... expected '}'").
	if err == nil {
		t.Fatal("a drift-refused run must fail operationally with the refused set named")
	}
	msg := err.Error()
	if !strings.Contains(msg, "source changed since load") || !strings.Contains(msg, "a/a.go") {
		t.Fatalf("refusal error = %q, want the named source drift", msg)
	}
	if !strings.Contains(msg, "1 completed target(s) kept") {
		t.Fatalf("refusal error = %q, want the completed sibling kept", msg)
	}
	if strings.Contains(msg, "format candidate") {
		t.Fatalf("the drift surfaced as a generator fault: %q", msg)
	}
	// The sibling's measurement completed before the operational
	// failure - its decision line is the ordinary measure.
	var siblingMeasured, driftRefused bool
	for _, d := range decisions {
		if d.Symbol == "example.com/drift/b.Triple" && d.Action == "measure" {
			siblingMeasured = true
		}
		if d.Symbol == "example.com/drift/a.Double" && strings.Contains(d.Reason, "source changed since load") {
			driftRefused = true
		}
	}
	if !siblingMeasured || !driftRefused {
		t.Fatalf("decisions = %+v, want the sibling measured and the drift refused", decisions)
	}
}

// A loaded file that VANISHES refuses once, in the deterministic
// decision order, with the file named — a checkout mid-run is drift
// (REQ-exec-quiescence), and the decision sequence stays exactly the
// preparation sequence's target order with one line per target
// (REQ-exec-run-status; the duplicate inline emit was a caught
// regression).
func TestRunRefusesVanishedTargetOnceInOrder(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	dir := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/vanish\n\ngo 1.26\n")
	write("a/a.go", "package a\n\nfunc Double(v int) int {\n\treturn v * 2\n}\n")
	write("a/a_test.go", "package a\n\nimport \"testing\"\n\nfunc TestDouble(t *testing.T) {\n\tif Double(3) != 6 {\n\t\tt.Fatal(\"double\")\n\t}\n}\n")
	write("b/b.go", "package b\n\nfunc Triple(v int) int {\n\treturn v * 3\n}\n")
	write("b/b_test.go", "package b\n\nimport \"testing\"\n\nfunc TestTriple(t *testing.T) {\n\tif Triple(3) != 9 {\n\t\tt.Fatal(\"triple\")\n\t}\n}\n")

	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "a", "a.go")); err != nil {
		t.Fatal(err)
	}

	var decisions []RunDecision
	_, err = tr.Run(context.Background(), []Target{
		{Symbol: "example.com/vanish/b.Triple"},
		{Symbol: "example.com/vanish/a.Double"},
	}, Options{Decision: func(d RunDecision) { decisions = append(decisions, d) }})
	if err == nil {
		t.Fatal("a vanished loaded file must fail the run operationally")
	}
	if !strings.Contains(err.Error(), "source vanished since load") || !strings.Contains(err.Error(), "a/a.go") {
		t.Fatalf("refusal error = %q, want the vanished file NAMED", err.Error())
	}
	var doubleLines int
	var order []string
	for _, d := range decisions {
		order = append(order, d.Symbol+":"+d.Action)
		if d.Symbol == "example.com/vanish/a.Double" {
			doubleLines++
			if !strings.Contains(d.Reason, "source vanished since load") {
				t.Fatalf("vanished decision reason = %q", d.Reason)
			}
		}
	}
	if doubleLines != 1 {
		t.Fatalf("vanished target decided %d times, want exactly once: %v", doubleLines, order)
	}
	if len(order) != 2 || order[0] != "example.com/vanish/b.Triple:measure" {
		t.Fatalf("decision order = %v, want the preparation sequence's target order", order)
	}
}
