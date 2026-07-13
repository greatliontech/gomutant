package gomutant

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const fixtureDir = "internal/engine/testdata/fixturemod"

func fixtureTree(t *testing.T) *Tree {
	t.Helper()
	tr, err := Load(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

// TestDiscover pins whole-tree discovery (REQ-target-producers): every
// non-test, non-generated function and method is a target with the default
// oracle; test functions and generated symbols are not.
func TestDiscover(t *testing.T) {
	tr := fixtureTree(t)
	targets := tr.Discover()
	got := map[string]bool{}
	for _, tg := range targets {
		got[tg.Symbol] = true
		if len(tg.Oracle) != 0 || len(tg.Labels) != 0 {
			t.Fatalf("discovered target carries oracle/labels: %+v", tg)
		}
	}
	for _, want := range []string{
		"example.com/fixture/lib.Add",
		"example.com/fixture/methods.Counter.Inc",
		"example.com/fixture/methods.Box.Get",
	} {
		if !got[want] {
			t.Errorf("discovery missed %s", want)
		}
	}
	for _, absent := range []string{
		"example.com/fixture/lib.TestAdd", // a test, never a target
		"example.com/fixture/genp.G.M",    // generated
	} {
		if got[absent] {
			t.Errorf("discovery targeted %s", absent)
		}
	}
}

func TestFilterTargets(t *testing.T) {
	tr := fixtureTree(t)
	targets := tr.Discover()
	selected, err := tr.FilterTargets(targets,
		[]string{"example.com/fixture/{lib,dot.x}"},
		[]string{"{example.com/fixture/lib.Add,example.com/fixture/dot.x.F}"},
	)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(selected))
	for _, target := range selected {
		got = append(got, target.Symbol)
	}
	want := []string{"example.com/fixture/dot.x.F", "example.com/fixture/lib.Add"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filtered targets = %v, want %v", got, want)
	}
	if _, err := tr.FilterTargets(targets, []string{"["}, nil); err == nil || !strings.Contains(err.Error(), "invalid package filter") {
		t.Fatalf("invalid filter = %v", err)
	}
	if _, err := tr.FilterTargets(targets, nil, []string{"["}); err == nil || !strings.Contains(err.Error(), "invalid symbol filter") {
		t.Fatalf("invalid symbol filter = %v", err)
	}
	symbolOnly, err := tr.FilterTargets(targets, nil, []string{"example.com/fixture/lib.Add"})
	if err != nil || len(symbolOnly) != 1 || symbolOnly[0].Symbol != "example.com/fixture/lib.Add" {
		t.Fatalf("symbol-only selection = %+v, %v", symbolOnly, err)
	}
	packageOnly, err := tr.FilterTargets([]Target{
		{Symbol: "example.com/fixture/lib.Add"},
		{Symbol: "example.com/fixture/methods.Counter.Inc"},
		{Symbol: "example.com/fixture/lib.Weak"},
	}, []string{"example.com/fixture/lib"}, nil)
	if err != nil || len(packageOnly) != 2 || packageOnly[0].Symbol != "example.com/fixture/lib.Add" || packageOnly[1].Symbol != "example.com/fixture/lib.Weak" {
		t.Fatalf("package-only selection = %+v, %v", packageOnly, err)
	}
	invalid := []Target{{Symbol: "not/a/loaded.Symbol"}, {Symbol: "example.com/fixture/lib.Add"}}
	selected, err = tr.FilterTargets(invalid, nil, []string{"example.com/fixture/lib.Add"})
	if err != nil || len(selected) != 1 || selected[0].Symbol != "example.com/fixture/lib.Add" {
		t.Fatalf("excluded invalid target = %+v, %v", selected, err)
	}
	if _, err := tr.FilterTargets(invalid[:1], []string{"**"}, nil); err == nil || !strings.Contains(err.Error(), "no loaded package") {
		t.Fatalf("selected invalid target = %v", err)
	}
	if _, err := tr.FilterTargets(targets, nil, []string{"example.com/fixture/lib.Absent"}); err == nil || !strings.Contains(err.Error(), "matched no targets") {
		t.Fatalf("empty selection = %v", err)
	}
	copy, err := tr.FilterTargets(targets, nil, nil)
	if err != nil || !reflect.DeepEqual(copy, targets) {
		t.Fatalf("unfiltered copy = %v, %v", copy, err)
	}
	copy[0].Symbol = "changed"
	if targets[0].Symbol == "changed" {
		t.Fatal("unfiltered selection aliases the target slice")
	}
}

func TestSelfHostTargetsResolve(t *testing.T) {
	data, err := os.ReadFile("testdata/self-host-targets.json")
	if err != nil {
		t.Fatal(err)
	}
	targets, err := LoadTargets(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 6 {
		t.Fatalf("self-host target count = %d, want 6", len(targets))
	}
	want := map[string]string{
		"github.com/greatliontech/gomutant.Tree.FilterTargets":               "github.com/greatliontech/gomutant.TestFilterTargets",
		"github.com/greatliontech/gomutant.SummarizeRun":                     "github.com/greatliontech/gomutant.TestSummarizeRun",
		"github.com/greatliontech/gomutant.reportPreparation":                "github.com/greatliontech/gomutant.TestRunDecisionsAndCancellation",
		"github.com/greatliontech/gomutant/internal/cmd.renderRunDecision":   "github.com/greatliontech/gomutant/internal/cmd.TestRenderRunStatus",
		"github.com/greatliontech/gomutant/internal/cmd.renderRunSummary":    "github.com/greatliontech/gomutant/internal/cmd.TestRenderRunStatus",
		"github.com/greatliontech/gomutant/internal/engine.Tree.PackagePath": "github.com/greatliontech/gomutant.TestFilterTargets",
	}
	for _, target := range targets {
		oracle, ok := want[target.Symbol]
		if !ok || len(target.Oracle) != 1 || target.Oracle[0] != oracle {
			t.Fatalf("unexpected self-host target: %+v", target)
		}
		delete(want, target.Symbol)
	}
	if len(want) != 0 {
		t.Fatalf("missing self-host targets: %v", want)
	}
	tree, err := Load(".")
	if err != nil {
		t.Fatal(err)
	}
	descriptions, err := tree.DescribeTargets(targets)
	if err != nil {
		t.Fatal(err)
	}
	for _, description := range descriptions {
		if !description.OracleExplicit || len(description.Oracle) != 1 || description.Skipped != "" {
			t.Fatalf("self-host target is not directly measurable: %+v", description)
		}
	}
}

// TestDiscoverChanged pins changed-scope discovery and the residue report
// (REQ-target-changed): only changed bodies target; every changed-but-
// untargeted path carries its engine-level reason.
func TestDiscoverChanged(t *testing.T) {
	tr := fixtureTree(t)
	libSrc, err := os.ReadFile(filepath.Join(fixtureDir, "lib", "lib.go"))
	if err != nil {
		t.Fatal(err)
	}
	dotSrc, err := os.ReadFile(filepath.Join(fixtureDir, "dot", "dot.go"))
	if err != nil {
		t.Fatal(err)
	}
	refs := map[string][]byte{
		// Add's body differed at the reference: only Add targets.
		"lib/lib.go": []byte(strings.Replace(string(libSrc), "return a + b", "return a - b", 1)),
		// Formatting-only churn: same canonical bodies.
		"lib/opsites.go": mustRead(t, filepath.Join(fixtureDir, "lib", "opsites.go"), "\t", "    "),
		// The reference had one more function than the working file: a
		// deleted symbol, nothing left to mutate.
		"dot/dot.go": append(append([]byte{}, dotSrc...), []byte("\nfunc Gone() int { return 9 }\n")...),
		// A loaded Go file declaring no function body.
		"lib/doc.go": nil,
		// genp/gen.go and the test file: reference content irrelevant.
		"genp/gen.go":     nil,
		"lib/lib_test.go": nil,
		"README.md":       nil,
	}
	paths := []string{"lib/lib.go", "lib/opsites.go", "genp/gen.go", "lib/lib_test.go", "README.md", "dot.x/dotx.go", "dot/dot.go", "lib/doc.go", "lib/removed.go"}
	targets, residue := tr.DiscoverChanged(paths, func(p string) ([]byte, bool) {
		b, ok := refs[p]
		if p == "dot.x/dotx.go" {
			return nil, false // absent at the reference: a new file, all changed
		}
		return b, ok && b != nil
	})

	syms := map[string]bool{}
	for _, tg := range targets {
		syms[tg.Symbol] = true
	}
	if !syms["example.com/fixture/lib.Add"] {
		t.Errorf("changed body not targeted: %v", targets)
	}
	if syms["example.com/fixture/lib.Weak"] {
		t.Error("unchanged body targeted")
	}
	if !syms["example.com/fixture/dot.x.F"] {
		t.Error("new file's symbol not targeted")
	}

	reasons := map[string]string{}
	for _, r := range residue {
		reasons[r.Path] = r.Reason
	}
	for path, want := range map[string]string{
		"lib/opsites.go":  "formatting-only churn",
		"genp/gen.go":     "generated",
		"lib/lib_test.go": "test file",
		"README.md":       "not a Go source file",
		"dot/dot.go":      "only deleted symbols",
		"lib/doc.go":      "no function body declared",
		"lib/removed.go":  "not in the loaded packages",
	} {
		if !strings.Contains(reasons[path], want) {
			t.Errorf("residue[%s] = %q, want reason containing %q", path, reasons[path], want)
		}
	}
	if _, ok := reasons["lib/lib.go"]; ok {
		t.Error("a targeting file also reported as residue")
	}
}

func mustRead(t *testing.T, path, old, new string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return []byte(strings.ReplaceAll(string(b), old, new))
}

// TestParseTargets pins the config-file producer (REQ-target-producers):
// one JSON document onto the same model, a symbol-less entry refused.
func TestParseTargets(t *testing.T) {
	doc := `{"targets": [
		{"symbol": "example.com/p.F"},
		{"symbol": "example.com/p.G", "oracle": ["example.com/p.TestG"], "labels": ["REQ-x"]}
	]}`
	targets, err := ParseTargets([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	want := []Target{
		{Symbol: "example.com/p.F"},
		{Symbol: "example.com/p.G", Oracle: []string{"example.com/p.TestG"}, Labels: []string{"REQ-x"}},
	}
	if !reflect.DeepEqual(targets, want) {
		t.Fatalf("targets = %+v", targets)
	}
	if _, err := ParseTargets([]byte(`{"targets": [{"oracle": ["x"]}]}`)); err == nil {
		t.Fatal("symbol-less target accepted")
	}
	if _, err := ParseTargets([]byte(`not json`)); err == nil {
		t.Fatal("malformed document accepted")
	}
}

func TestDescribeTargetsResolvesEffectiveOracles(t *testing.T) {
	tr := fixtureTree(t)
	descriptions, err := tr.DescribeTargets([]Target{
		{Symbol: "example.com/fixture/lib.Weak", Labels: []string{"z", "a"}},
		{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestWeak", "example.com/fixture/lib.TestAdd"}},
		{Symbol: "example.com/fixture/lib.Guarded", OracleExplicit: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(descriptions) != 3 || descriptions[0].Symbol != "example.com/fixture/lib.Add" || !descriptions[0].OracleExplicit || len(descriptions[0].Oracle) != 2 || descriptions[0].Oracle[0] != "example.com/fixture/lib.TestAdd" {
		t.Fatalf("explicit description = %+v", descriptions)
	}
	if descriptions[1].Symbol != "example.com/fixture/lib.Guarded" || !descriptions[1].OracleExplicit || len(descriptions[1].Oracle) != 0 || descriptions[1].Skipped != "no oracle" {
		t.Fatalf("explicit-empty description = %+v", descriptions[1])
	}
	weak := descriptions[2]
	if weak.OracleExplicit || len(weak.Oracle) == 0 || len(weak.Labels) != 2 || weak.Labels[0] != "a" {
		t.Fatalf("derived description = %+v", weak)
	}
	if _, err := tr.DescribeTargets([]Target{{Symbol: "example.com/fixture/lib.Add"}, {Symbol: "example.com/fixture/lib.Add"}}); err == nil {
		t.Fatal("duplicate target accepted")
	}
	if _, err := tr.DescribeTargets([]Target{{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestDeleted"}}}); err == nil {
		t.Fatal("missing oracle accepted")
	}
	nonFunction, err := tr.DescribeTargets([]Target{{Symbol: "example.com/fixture/lib.I", Oracle: []string{"example.com/fixture/lib.TestAdd"}}})
	if err != nil || len(nonFunction) != 1 || nonFunction[0].Skipped != "not a function" {
		t.Fatalf("non-function description = %+v, %v", nonFunction, err)
	}
	if _, err := tr.DescribeTargets([]Target{{Symbol: "example.com/fixture/lib.Deleted", Oracle: []string{"example.com/fixture/lib.TestAdd"}}}); err == nil {
		t.Fatal("missing target accepted")
	}
}

// TestResolveOracle pins oracle resolution (REQ-target-oracle,
// REQ-target-default): explicit oracles pass through untouched; an empty
// oracle derives the tests of the symbol's own package, both variants.
func TestResolveOracle(t *testing.T) {
	tr := fixtureTree(t)
	explicit := Target{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/plain.TestPlain"}}
	if got := tr.resolveOracle(explicit); !reflect.DeepEqual(got, explicit.Oracle) {
		t.Fatalf("explicit oracle rewritten: %v", got)
	}
	// An explicitly empty oracle derives nothing: nothing vouches, so
	// nothing is measured (REQ-target-default).
	if got := tr.resolveOracle(Target{Symbol: "example.com/fixture/lib.Add", OracleExplicit: true}); len(got) != 0 {
		t.Fatalf("explicitly empty oracle inherited a default: %v", got)
	}
	got := tr.resolveOracle(Target{Symbol: "example.com/fixture/lib.Add"})
	want := map[string]bool{
		"example.com/fixture/lib.TestAdd": true, // in-package
		"example.com/fixture/lib.TestExt": true, // external variant
		"example.com/fixture/lib.FuzzAdd": true, // fuzz seed corpus runs in go test
	}
	found := 0
	for _, sym := range got {
		if want[sym] {
			found++
		}
		// TestMain is the harness; Testhelper never runs (lowercase
		// continuation, non-harness signature): neither can kill, so
		// admitting either would derive an oracle that executes nothing.
		if strings.Contains(sym, "TestMain") || strings.Contains(sym, "Testhelper") {
			t.Fatalf("non-runnable %s in a derived oracle: %v", sym, got)
		}
	}
	if found != len(want) {
		t.Fatalf("derived oracle = %v, want all of %v", got, want)
	}
	// A Test-named function with a signature go test rejects can never run:
	// the derived oracle stays empty rather than executing nothing.
	if got := tr.resolveOracle(Target{Symbol: "example.com/fixture/badtest.B"}); len(got) != 0 {
		t.Fatalf("malformed test signature entered a derived oracle: %v", got)
	}
}

// TestPkgRuns pins per-package oracle scoping (REQ-exec-oracle-run): tests
// group by package, each package running exactly its own oracle pattern.
func TestPkgRuns(t *testing.T) {
	runs := pkgRuns([]string{
		"example.com/a.TestX",
		"example.com/b.TestX",
		"example.com/a.TestY",
	})
	want := []pkgRun{
		{pkg: "example.com/a", runRegex: "^(TestX|TestY)$"},
		{pkg: "example.com/b", runRegex: "^(TestX)$"},
	}
	if !reflect.DeepEqual(runs, want) {
		t.Fatalf("pkgRuns = %+v, want %+v", runs, want)
	}
}
