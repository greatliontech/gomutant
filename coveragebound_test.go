package gomutant

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The summary's unreached set is exactly the no-oracle skips with an
// empty derivation under the declared selection: a stood-down derivation
// is a resolution failure, a measured target is not a skip, and a run
// under no declared selection states no bound (REQ-result-unreached-bound).
func TestSummaryStatesTheUnreachedBoundUnderTheSelection(t *testing.T) {
	findings := []Finding{
		{Symbol: "example.com/mod/wasm.B", Skipped: "no oracle", Unreached: true},
		{Symbol: "example.com/mod/wasm.A", Skipped: "no oracle", Unreached: true},
		{Symbol: "example.com/mod/host.C", Skipped: "no oracle — derivation stood down on: example.com/mod/host; resolve the closure, or pass an explicit oracle"},
		{Symbol: "example.com/mod/host.D", Generated: 2, Mutants: 2, Killed: 2},
	}
	sel := Selection{Tags: []string{"wasm", "js"}}
	summary := SummarizeRun(findings, sel)
	if summary.Selection != "tags:js,wasm" || strings.Join(summary.Unreached, ",") != "example.com/mod/wasm.A,example.com/mod/wasm.B" {
		t.Fatalf("summary = %+v, want the two unreached symbols sorted under tags:js,wasm", summary)
	}
	if summary.Skipped != 3 {
		t.Fatalf("skipped = %d, want 3 (the stood-down skip counts as skipped, never as unreached)", summary.Skipped)
	}
	if plain := SummarizeRun(findings, Selection{}); plain.Selection != "" || plain.Unreached != nil {
		t.Fatalf("no declared selection stated a bound: %+v", plain)
	}
	if bound := CoverageBoundOf(findings, sel, "run-1"); bound == nil || bound.Run != "run-1" || bound.Selection != "tags:js,wasm" || len(bound.Unreached) != 2 {
		t.Fatalf("bound = %+v", bound)
	}
	// A declared selection with nothing unreached yields an EMPTY bound —
	// the record that clears a standing row — never nil.
	if bound := CoverageBoundOf(findings[2:], sel, "run-1"); bound == nil || bound.Selection != "tags:js,wasm" || len(bound.Unreached) != 0 || bound.Unreached == nil {
		t.Fatalf("nothing unreached under a selection = %+v, want the empty bound", bound)
	}
	if bound := CoverageBoundOf(findings, Selection{}, "run-1"); bound != nil {
		t.Fatalf("no selection yet a bound: %+v", bound)
	}
	// The key spells tags and toolchain: sorted, deduplicated tags; each
	// toolchain its own leg; nothing declared, no key.
	for _, tc := range []struct {
		sel  Selection
		want string
	}{
		{Selection{Tags: []string{"b", "a", "b"}, Toolchain: "go1.27.0"}, "tags:a,b;toolchain:go1.27.0"},
		{Selection{Toolchain: "go1.28"}, "toolchain:go1.28"},
		{Selection{Tags: []string{"wasm"}}, "tags:wasm"},
		{Selection{}, ""},
	} {
		if key := SelectionKey(tc.sel); key != tc.want {
			t.Fatalf("selection key of %+v = %q, want %q", tc.sel, key, tc.want)
		}
	}
}

// The document carries its coverage bounds under version 12 — written
// present, read back — while a version-11 document reads with none and
// a version-12 document without the table refuses
// (REQ-result-unreached-bound, REQ-result-export).
func TestDocumentCarriesCoverageBoundsUnderVersion12(t *testing.T) {
	findings := []Finding{survivorFinding("example.com/mod/host.D")}
	bounds := []CoverageBound{{Selection: "wasm", Run: "r1", Unreached: []string{"example.com/mod/wasm.A"}}, {Selection: "js", Run: "r1", Unreached: []string{"example.com/mod/js.B"}}}
	data, _, err := renderDocument(findings, bounds)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"version": 12`) || !strings.Contains(string(data), `"coverageBounds"`) {
		t.Fatalf("document lacks the version-12 table:\n%s", data)
	}
	doc, err := ParseDocument(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.CoverageBounds) != 2 || doc.CoverageBounds[0].Selection != "js" || doc.CoverageBounds[1].Selection != "wasm" || doc.CoverageBounds[1].Unreached[0] != "example.com/mod/wasm.A" {
		t.Fatalf("bounds round-tripped as %+v, want both rows sorted by selection", doc.CoverageBounds)
	}
	if len(doc.Findings) != 1 {
		t.Fatalf("findings = %d", len(doc.Findings))
	}
	// An empty table is written present, never null.
	empty, _, err := renderDocument(findings, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(empty), `"coverageBounds": []`) {
		t.Fatalf("empty table not written present:\n%s", empty)
	}
	// A version-11 document reads with no bounds; a 12 without the
	// table refuses; a version ahead refuses ahead.
	var top map[string]json.RawMessage
	if err := json.Unmarshal(empty, &top); err != nil {
		t.Fatal(err)
	}
	delete(top, "coverageBounds")
	top["version"] = json.RawMessage("11")
	v11, _ := json.Marshal(top)
	if doc, err := ParseDocument(v11); err != nil || len(doc.CoverageBounds) != 0 || len(doc.Findings) != 1 {
		t.Fatalf("version 11 = %+v, %v; want the findings with no bounds", doc, err)
	}
	top["version"] = json.RawMessage("12")
	v12bare, _ := json.Marshal(top)
	if _, err := ParseDocument(v12bare); err == nil || !strings.Contains(err.Error(), "coverageBounds") {
		t.Fatalf("version 12 without the table = %v, want refused naming it", err)
	}
	top["version"] = json.RawMessage("13")
	ahead, _ := json.Marshal(top)
	var versionErr *DocumentVersionError
	if _, err := ParseDocument(ahead); !errors.As(err, &versionErr) || !errors.Is(err, ErrVersionAhead) {
		t.Fatalf("version 13 = %v, want refused ahead", err)
	}
	// A malformed row refuses.
	bad := strings.Replace(string(data), `"selection": "js"`, `"selection": ""`, 1)
	if _, err := ParseDocument([]byte(bad)); err == nil || !strings.Contains(err.Error(), "coverage bound") {
		t.Fatalf("empty selection = %v, want refused", err)
	}
}

// A recorded bound rides the next document write, replacing the row of
// its selection and leaving the other selections' rows, and reads back
// through the store (REQ-result-unreached-bound).
func TestStoreRecordsTheBoundOnTheNextWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gomutant", "findings.json")
	store, err := OpenStore(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	f := survivorFinding("example.com/mod/host.D")
	store.RecordCoverageBound(CoverageBound{Selection: "wasm", Run: "r1", Unreached: []string{"example.com/mod/wasm.A"}})
	store.RecordCoverageBound(CoverageBound{Selection: "js", Run: "r1", Unreached: []string{"example.com/mod/js.B"}})
	if err := store.Update(ctx, func([]Finding) ([]Finding, error) { return []Finding{f}, nil }); err != nil {
		t.Fatal(err)
	}
	bounds, err := store.CoverageBounds(ctx)
	if err != nil || len(bounds) != 2 || bounds[0].Selection != "js" || bounds[1].Selection != "wasm" {
		t.Fatalf("bounds after the first write = %+v, %v", bounds, err)
	}
	// A later run of one selection replaces its row alone; a write with
	// nothing recorded keeps every row.
	store.RecordCoverageBound(CoverageBound{Selection: "wasm", Run: "r2", Unreached: []string{"example.com/mod/wasm.A", "example.com/mod/wasm.C"}})
	if err := store.Update(ctx, func([]Finding) ([]Finding, error) { return []Finding{f}, nil }); err != nil {
		t.Fatal(err)
	}
	if err := store.Update(ctx, func([]Finding) ([]Finding, error) { return []Finding{f}, nil }); err != nil {
		t.Fatal(err)
	}
	bounds, err = store.CoverageBounds(ctx)
	if err != nil || len(bounds) != 2 || bounds[1].Run != "r2" || len(bounds[1].Unreached) != 2 || bounds[0].Run != "r1" {
		t.Fatalf("bounds after the replace = %+v, %v", bounds, err)
	}
	// An empty bound clears its selection's row — the leg reached
	// everything — and leaves the others.
	store.RecordCoverageBound(CoverageBound{Selection: "js", Run: "r3", Unreached: []string{}})
	if err := store.Update(ctx, func([]Finding) ([]Finding, error) { return []Finding{f}, nil }); err != nil {
		t.Fatal(err)
	}
	bounds, err = store.CoverageBounds(ctx)
	if err != nil || len(bounds) != 1 || bounds[0].Selection != "wasm" {
		t.Fatalf("bounds after the clearing run = %+v, %v; want the js row gone", bounds, err)
	}
	// A fresh store over the same document reads the same rows.
	again, err := OpenStore(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	if reread, err := again.CoverageBounds(ctx); err != nil || len(reread) != 1 || reread[0].Run != "r2" {
		t.Fatalf("reread bounds = %+v, %v; want the one surviving row", reread, err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"version": 12`) {
		t.Fatalf("document not at version 12:\n%s", data)
	}
}

// A tagged package with no test under the selection's leg — the
// browser-driven shape — discovers its function and reaches it through
// no oracle: the run's unreached set names it under the selection,
// while a tagged function beside a tagged test is measured like any
// other, and the selection-less run never discovers either
// (REQ-result-unreached-bound).
func TestTaggedLegWithoutAnOracleIsAStatedBound(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tmp := writeSelectionFixture(t)
	if err := os.MkdirAll(filepath.Join(tmp, "leg"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Two functions in the leg: the memoized derivation marks the second
	// exactly as the first, and the roster sorts.
	if err := os.WriteFile(filepath.Join(tmp, "leg", "dark.go"), []byte("//go:build seltag\n\npackage leg\n\nfunc Dark(x int) int { return x * 2 }\n\nfunc Also(x int) int { return x * 3 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	selected, err := LoadContextSelection(context.Background(), tmp, Selection{Tags: []string{"seltag"}})
	if err != nil {
		t.Fatal(err)
	}
	if selected.Selection().Tags[0] != "seltag" {
		t.Fatalf("tree selection = %+v", selected.Selection())
	}
	targets, err := selected.DiscoverContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	findings, err := selected.Run(context.Background(), targets, Options{OracleTimeout: 2 * time.Minute, RunID: "r-sel"})
	if err != nil {
		t.Fatal(err)
	}
	summary := SummarizeRun(findings, selected.Selection())
	if summary.Selection != "tags:seltag" || strings.Join(summary.Unreached, ",") != "example.com/sel/leg.Also,example.com/sel/leg.Dark" {
		t.Fatalf("summary = %+v, want leg.Also and leg.Dark unreached under tags:seltag, sorted", summary)
	}
	// An explicit empty oracle statement and a symbol in no loaded
	// package are not the leg's bound: neither is a derivation over a
	// resolved package.
	edge, err := selected.Run(context.Background(), []Target{
		{Symbol: "example.com/sel/leg.Dark", OracleExplicit: true},
		{Symbol: "example.com/sel/nowhere.Ghost"},
	}, Options{OracleTimeout: 2 * time.Minute, RunID: "r-edge"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range edge {
		if f.Skipped == "" || f.Unreached {
			t.Fatalf("%s = skipped %q unreached %v, want skipped and never the bound", f.Symbol, f.Skipped, f.Unreached)
		}
	}
	if b := CoverageBoundOf(edge, selected.Selection(), "r-edge"); b == nil || len(b.Unreached) != 0 {
		t.Fatalf("edge bound = %+v, want empty", b)
	}
	for _, f := range findings {
		if (f.Symbol == "example.com/sel/leg.Dark" || f.Symbol == "example.com/sel/leg.Also") && (!f.Unreached || f.Skipped != "no oracle") {
			t.Fatalf("%s = %+v, want the no-oracle skip marked unreached", f.Symbol, f)
		}
		if f.Symbol == "example.com/sel/pkg.Gated" && (f.Unreached || f.Skipped != "") {
			t.Fatalf("Gated beside the tagged oracle = skipped %q unreached %v, want measured", f.Skipped, f.Unreached)
		}
	}
	bound := CoverageBoundOf(findings, selected.Selection(), "r-sel")
	if bound == nil || bound.Run != "r-sel" {
		t.Fatalf("bound = %+v", bound)
	}
	plain, err := Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	plainTargets, err := plain.DiscoverContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range plainTargets {
		if target.Symbol == "example.com/sel/leg.Dark" || target.Symbol == "example.com/sel/leg.Also" || target.Symbol == "example.com/sel/pkg.Gated" {
			t.Fatalf("the selection-less load discovered the tagged symbol %s", target.Symbol)
		}
	}
}

// A derivation that stood down on packages is a resolution failure
// naming its packages, never the selection's coverage bound: under a
// declared selection the stood-down skip stays out of the unreached
// set and the run states no bound (REQ-result-unreached-bound).
func TestStoodDownDerivationIsNotACoverageBound(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list per test package")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":      "module example.com/darkmod\n\ngo 1.26\n",
		"a/a.go":      "package a\n\nfunc A() int { return 1 }\n",
		"c/c.go":      "package c\n\nimport (\n\t_ \"example.com/darkmod/a\"\n\t_ \"example.com/darkmod/missing\"\n)\n",
		"c/c_test.go": "package c\n\nimport \"testing\"\n\nfunc TestC(t *testing.T) {}\n",
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tr, err := LoadContextSelection(context.Background(), dir, Selection{Tags: []string{"seltag"}})
	if err != nil {
		t.Fatal(err)
	}
	findings, err := tr.Run(context.Background(), []Target{{Symbol: "example.com/darkmod/a.A"}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(findings[0].Skipped, "stood down on: example.com/darkmod/c") || findings[0].Unreached {
		t.Fatalf("stood-down skip = %q unreached %v, want the packages named and no bound", findings[0].Skipped, findings[0].Unreached)
	}
	if summary := SummarizeRun(findings, tr.Selection()); len(summary.Unreached) != 0 || summary.Skipped != 1 {
		t.Fatalf("summary = %+v, want the skip counted and nothing unreached", summary)
	}
	// The declared selection's bound exists — empty: the stood-down
	// skip is never in it, and a whole-tree run's record of it would
	// clear a standing row rather than restate one.
	if bound := CoverageBoundOf(findings, tr.Selection(), "r"); bound == nil || len(bound.Unreached) != 0 {
		t.Fatalf("stood-down derivation's bound = %+v, want empty", bound)
	}
}

// A caller editing records through the public document updater keeps
// the coverage-bounds table whole: the default writer carries the
// prior document's bounds (REQ-result-unreached-bound).
func TestUpdateDocumentKeepsTheCoverageBounds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gomutant", "findings.json")
	store, err := OpenStore(path, dir)
	if err != nil {
		t.Fatal(err)
	}
	store.RecordCoverageBound(CoverageBound{Selection: "tags:wasm", Run: "r1", Unreached: []string{"example.com/mod/wasm.A"}})
	if err := store.Update(context.Background(), func([]Finding) ([]Finding, error) { return []Finding{survivorFinding("example.com/mod/host.D")}, nil }); err != nil {
		t.Fatal(err)
	}
	if err := UpdateDocument(context.Background(), path, func(prior []Finding) ([]Finding, error) { return prior, nil }); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := ParseDocument(data)
	// The record itself may live in the overlay (committability is the
	// store's call); the claim here is the table.
	if err != nil || len(doc.CoverageBounds) != 1 || doc.CoverageBounds[0].Unreached[0] != "example.com/mod/wasm.A" {
		t.Fatalf("after UpdateDocument = %+v, %v; want the bound kept", doc, err)
	}
}
