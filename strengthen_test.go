package gomutant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The pando field scenario, verbatim: a survivor recorded under a
// weak oracle test MUST flip to killed when that existing test is
// strengthened IN PLACE (alongside unrelated additions) and the
// target re-measures. A
// surviving record that outlives the assertion that kills its mutant
// is a false verdict served as truth (REQ-result-stale,
// REQ-exec-attribution).
func TestRunSurvivorFlipsWhenExistingOracleTestStrengthens(t *testing.T) {
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
	write("go.mod", "module example.com/flip\n\ngo 1.26\n")
	write("lib/lib.go", `package lib

// Bump adds an offset to a base.
func Bump(base, i int) int {
	return base + i
}
`)
	// The weak oracle: an offset of zero cannot distinguish + from -,
	// so the arithmetic swap at the return survives run A.
	write("lib/lib_test.go", `package lib

import "testing"

func TestBump(t *testing.T) {
	if Bump(2, 0) != 2 {
		t.Fatal("bump")
	}
}
`)

	ctx := context.Background()
	trA, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	target := Target{Symbol: "example.com/flip/lib.Bump"}
	first, err := trA.Run(ctx, []Target{target}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 {
		t.Fatalf("run A findings = %d, want 1", len(first))
	}
	survived := false
	for _, s := range first[0].Survivors {
		if s.Operator == "arithmetic: + -> -" {
			survived = true
		}
	}
	if !survived {
		t.Fatalf("run A did not record the weak-oracle survivor: %+v", first[0].Survivors)
	}

	// The strengthen-in-place: the SAME test gains the assertion that
	// discriminates the swap, and an unrelated sibling is added —
	// exactly the field edit shape.
	write("lib/lib_test.go", `package lib

import "testing"

func TestBump(t *testing.T) {
	if Bump(2, 0) != 2 {
		t.Fatal("bump")
	}
	if Bump(2, 3) != 5 {
		t.Fatal("bump offset")
	}
}

func TestBumpUnrelated(t *testing.T) {
	if Bump(0, 0) != 0 {
		t.Fatal("zero")
	}
}
`)

	// A fresh load, as a fresh CLI invocation would: the prior record
	// rides in exactly as the findings document does.
	trB, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var decisionsB []RunDecision
	second, err := trB.Run(ctx, []Target{target}, Options{Prior: first, Decision: func(d RunDecision) { decisionsB = append(decisionsB, d) }})
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 {
		t.Fatalf("run B findings = %d, want 1", len(second))
	}
	// The decision class is pinned so a degenerate always-re-measure
	// (or a wholesale cache) cannot green this net: the edit steers
	// the killer-drift carve-out in its growth flavour — survivors
	// re-measure against the CURRENT oracle.
	if second[0].Cached {
		t.Fatalf("run B served a cached record across a strengthened oracle: %+v", second[0])
	}
	if len(decisionsB) != 1 || !strings.Contains(decisionsB[0].Reason, "re-measuring") || !strings.Contains(decisionsB[0].Reason, "derived oracle grew") {
		t.Fatalf("run B decision = %+v, want the growth-flavoured killer-drift re-measure", decisionsB)
	}
	for _, s := range second[0].Survivors {
		if s.Operator == "arithmetic: + -> -" {
			t.Fatalf("the strengthened oracle's mutant still recorded as a survivor - a false verdict: %+v (killed=%d mutants=%d cached=%v)",
				s, second[0].Killed, second[0].Mutants, second[0].Cached)
		}
	}
}

// The in-place-only arm: the SAME oracle test strengthened with NO
// added sibling — the field ask (a) verbatim. The body edit alone
// must invalidate the record and the survivor must flip
// (REQ-result-stale; the killer-drift carve-out without its growth
// clause).
func TestRunSurvivorFlipsOnInPlaceOracleEditAlone(t *testing.T) {
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
	write("go.mod", "module example.com/flip2\n\ngo 1.26\n")
	write("lib/lib.go", "package lib\n\nfunc Bump(base, i int) int {\n\treturn base + i\n}\n")
	write("lib/lib_test.go", "package lib\n\nimport \"testing\"\n\nfunc TestBump(t *testing.T) {\n\tif Bump(2, 0) != 2 {\n\t\tt.Fatal(\"bump\")\n\t}\n}\n")

	ctx := context.Background()
	trA, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	target := Target{Symbol: "example.com/flip2/lib.Bump"}
	first, err := trA.Run(ctx, []Target{target}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	survived := false
	for _, s := range first[0].Survivors {
		if s.Operator == "arithmetic: + -> -" {
			survived = true
		}
	}
	if !survived {
		t.Fatalf("run A did not record the weak-oracle survivor: %+v", first[0].Survivors)
	}

	write("lib/lib_test.go", "package lib\n\nimport \"testing\"\n\nfunc TestBump(t *testing.T) {\n\tif Bump(2, 0) != 2 {\n\t\tt.Fatal(\"bump\")\n\t}\n\tif Bump(2, 3) != 5 {\n\t\tt.Fatal(\"bump offset\")\n\t}\n}\n")

	trB, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var decisionsB []RunDecision
	second, err := trB.Run(ctx, []Target{target}, Options{Prior: first, Decision: func(d RunDecision) { decisionsB = append(decisionsB, d) }})
	if err != nil {
		t.Fatal(err)
	}
	if second[0].Cached {
		t.Fatalf("run B served a cached record across the in-place edit: %+v", second[0])
	}
	// The in-place edit takes the killer-drift carve-out WITHOUT its
	// growth clause - pinned so this arm cannot drift into the
	// growth-flavoured neighbour it exists to distinguish.
	if len(decisionsB) != 1 || !strings.Contains(decisionsB[0].Reason, "re-measuring") || strings.Contains(decisionsB[0].Reason, "derived oracle grew") {
		t.Fatalf("run B decision = %+v, want the growth-free killer-drift re-measure", decisionsB)
	}
	for _, s := range second[0].Survivors {
		if s.Operator == "arithmetic: + -> -" {
			t.Fatalf("the in-place-strengthened oracle's mutant still recorded as a survivor: %+v", s)
		}
	}
}

// The stale reason names ALL THREE edit classes: a strengthened-in-place
// oracle test surfaces as "modified:" beside the added and removed
// identities — stopping at "added:" hides exactly the edit a caller who
// just wrote kill-tests needs to see the tool noticing (the pando field
// report; REQ-result-inspection's naming arm).
func TestStaleReasonNamesModifiedOracleTests(t *testing.T) {
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
	write("go.mod", "module example.com/named\n\ngo 1.26\n")
	write("lib/lib.go", "package lib\n\nfunc Bump(base, i int) int {\n\treturn base + i\n}\n")
	write("lib/lib_test.go", "package lib\n\nimport \"testing\"\n\nfunc TestBump(t *testing.T) {\n\tif Bump(2, 0) != 2 {\n\t\tt.Fatal(\"bump\")\n\t}\n}\n\nfunc TestGone(t *testing.T) {\n\tif Bump(1, 0) != 1 {\n\t\tt.Fatal(\"gone\")\n\t}\n}\n")

	ctx := context.Background()
	trA, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	target := Target{Symbol: "example.com/named/lib.Bump"}
	first, err := trA.Run(ctx, []Target{target}, Options{})
	if err != nil {
		t.Fatal(err)
	}

	// One edit, all three classes: TestBump strengthened IN PLACE,
	// TestNew added, TestGone removed — the removal forecloses the
	// growth/drift carve-outs, so the plain derived-oracle-delta reason
	// is the decision's, exactly the field trace's shape.
	write("lib/lib_test.go", "package lib\n\nimport \"testing\"\n\nfunc TestBump(t *testing.T) {\n\tif Bump(2, 0) != 2 {\n\t\tt.Fatal(\"bump\")\n\t}\n\tif Bump(2, 3) != 5 {\n\t\tt.Fatal(\"bump offset\")\n\t}\n}\n\nfunc TestNew(t *testing.T) {\n\tif Bump(0, 1) != 1 {\n\t\tt.Fatal(\"new\")\n\t}\n}\n")

	trB, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var decisions []RunDecision
	if _, err := trB.Run(ctx, []Target{target}, Options{Prior: first, Decision: func(d RunDecision) { decisions = append(decisions, d) }}); err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 1 {
		t.Fatalf("decisions = %+v, want one", decisions)
	}
	reason := decisions[0].Reason
	for _, want := range []string{"added: example.com/named/lib.TestNew", "removed: example.com/named/lib.TestGone", "modified: example.com/named/lib.TestBump"} {
		if !strings.Contains(reason, want) {
			t.Fatalf("stale reason %q misses %q", reason, want)
		}
	}
}

// The modified arm stays silent when no test BODY changed: a target
// edit plus oracle identity churn (add/remove) moves every oracle's
// closure and the shared compartment, but the per-declaration ledger
// diff attributes no body edit, so no "modified:" list is named — a
// list there would point at untouched tests
// (REQ-result-inspection's naming arm).
func TestStaleReasonSkipsModifiedListWhenTargetMoved(t *testing.T) {
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
	write("go.mod", "module example.com/gated\n\ngo 1.26\n")
	write("lib/lib.go", "package lib\n\nfunc Bump(base, i int) int {\n\treturn base + i\n}\n")
	write("lib/lib_test.go", "package lib\n\nimport \"testing\"\n\nfunc TestBump(t *testing.T) {\n\tif Bump(2, 0) != 2 {\n\t\tt.Fatal(\"bump\")\n\t}\n}\n\nfunc TestGone(t *testing.T) {\n\tif Bump(1, 0) != 1 {\n\t\tt.Fatal(\"gone\")\n\t}\n}\n")

	ctx := context.Background()
	trA, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	target := Target{Symbol: "example.com/gated/lib.Bump"}
	first, err := trA.Run(ctx, []Target{target}, Options{})
	if err != nil {
		t.Fatal(err)
	}

	// A target edit plus identity churn, with TestBump's body
	// byte-identical — no declaration changed, so no modified list.
	write("lib/lib.go", "package lib\n\nfunc Bump(base, i int) int {\n\tsum := base + i\n\treturn sum\n}\n")
	write("lib/lib_test.go", "package lib\n\nimport \"testing\"\n\nfunc TestBump(t *testing.T) {\n\tif Bump(2, 0) != 2 {\n\t\tt.Fatal(\"bump\")\n\t}\n}\n\nfunc TestNew(t *testing.T) {\n\tif Bump(0, 1) != 1 {\n\t\tt.Fatal(\"new\")\n\t}\n}\n")

	trB, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var decisions []RunDecision
	if _, err := trB.Run(ctx, []Target{target}, Options{Prior: first, Decision: func(d RunDecision) { decisions = append(decisions, d) }}); err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 1 {
		t.Fatalf("decisions = %+v, want one", decisions)
	}
	// The positive first — without it, a fixture that stopped producing
	// an identity delta would green this test while pinning nothing.
	if !strings.Contains(decisions[0].Reason, "derived oracle changed") {
		t.Fatalf("the identity delta did not fire, the arm was never reached: %q", decisions[0].Reason)
	}
	if strings.Contains(decisions[0].Reason, "modified:") {
		t.Fatalf("stale reason names a modified list although no test body changed: %q", decisions[0].Reason)
	}
}
