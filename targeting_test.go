package gomutant

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

const fixtureDir = "internal/engine/testdata/fixturemod"

type cancelAfterChecks struct {
	context.Context
	remaining int
}

type cancelInFunctionContext struct {
	context.Context
	function string
}

func (c cancelInFunctionContext) Err() error {
	callers := make([]uintptr, 32)
	for frames := runtime.CallersFrames(callers[:runtime.Callers(2, callers)]); ; {
		frame, more := frames.Next()
		if strings.Contains(frame.Function, c.function) {
			return context.Canceled
		}
		if !more {
			return nil
		}
	}
}

func (c *cancelAfterChecks) Err() error {
	if c.remaining == 0 {
		return context.Canceled
	}
	c.remaining--
	return nil
}

func fixtureTree(t *testing.T) *Tree {
	t.Helper()
	tr, err := Load(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func TestTargetPreparationContextCancellation(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if tree, err := LoadContext(cancelled, fixtureDir); !errors.Is(err, context.Canceled) || tree != nil {
		t.Fatalf("cancelled load = tree %v, error %v", tree, err)
	}

	tree := fixtureTree(t)
	ctx, cancel := context.WithCancel(context.Background())
	surface, err := tree.eng.SurfaceContext(ctx, []string{"lib/lib.go"}, func(string) ([]byte, bool) {
		cancel()
		return []byte("package lib"), true
	})
	if !errors.Is(err, context.Canceled) || surface != nil {
		t.Fatalf("cancelled changed surface = surface %v, error %v", surface, err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	targets, residue, err := tree.DiscoverChangedContext(ctx, []string{"lib/lib.go"}, func(string) ([]byte, bool) {
		cancel()
		return []byte("package lib"), true
	})
	if !errors.Is(err, context.Canceled) || targets != nil || residue != nil {
		t.Fatalf("cancelled changed discovery = targets %v, residue %v, error %v", targets, residue, err)
	}
	if targets, err := tree.DiscoverContext(cancelled); !errors.Is(err, context.Canceled) || targets != nil {
		t.Fatalf("cancelled discovery = targets %v, error %v", targets, err)
	}
	if selected, err := tree.FilterTargetsContext(cancelled, []Target{{Symbol: "missing"}}, nil, nil); !errors.Is(err, context.Canceled) || selected != nil {
		t.Fatalf("cancelled filtering = targets %v, error %v", selected, err)
	}
	filterCtx := &cancelAfterChecks{Context: context.Background(), remaining: 2}
	if selected, err := tree.FilterTargetsContext(filterCtx, []Target{{Symbol: "example.com/fixture/lib.Add"}}, []string{"example.com/*"}, nil); !errors.Is(err, context.Canceled) || selected != nil {
		t.Fatalf("mid-filter cancellation = targets %v, error %v", selected, err)
	}
	compileCtx := &cancelAfterChecks{Context: context.Background(), remaining: 2}
	if _, err := tree.FilterTargetsContext(compileCtx, nil, []string{"example.com/*", "["}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("filter compilation cancellation lost precedence: %v", err)
	}
	if pkg, err := tree.eng.PackagePathContext(cancelled, "missing"); !errors.Is(err, context.Canceled) || pkg != "" {
		t.Fatalf("cancelled package path = %q, %v", pkg, err)
	}
	packageCtx := &cancelAfterChecks{Context: context.Background(), remaining: 1}
	if pkg, err := tree.eng.PackagePathContext(packageCtx, "example.com/fixture/lib.Add"); !errors.Is(err, context.Canceled) {
		t.Fatalf("post-resolution package cancellation = %q, %v", pkg, err)
	}
	describeCtx := cancelInFunctionContext{Context: context.Background(), function: "BodyHashContext"}
	if descriptions, err := tree.DescribeTargetsContext(describeCtx, []Target{{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestAdd"}}}); !errors.Is(err, context.Canceled) || descriptions != nil {
		t.Fatalf("body-hash cancellation = descriptions %+v, error %v", descriptions, err)
	}
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
	want := map[string][]string{
		"github.com/greatliontech/gomutant.Tree.FilterTargetsContext": {
			"github.com/greatliontech/gomutant.TestFilterTargets",
			"github.com/greatliontech/gomutant.TestTargetPreparationContextCancellation",
		},
		"github.com/greatliontech/gomutant.SummarizeRun":                   {"github.com/greatliontech/gomutant.TestSummarizeRun"},
		"github.com/greatliontech/gomutant.reportPreparation":              {"github.com/greatliontech/gomutant.TestRunDecisionsAndCancellation"},
		"github.com/greatliontech/gomutant/internal/cmd.renderRunDecision": {"github.com/greatliontech/gomutant/internal/cmd.TestRenderRunStatus"},
		"github.com/greatliontech/gomutant/internal/cmd.renderRunSummary":  {"github.com/greatliontech/gomutant/internal/cmd.TestRenderRunStatus"},
		"github.com/greatliontech/gomutant/internal/engine.Tree.PackagePathContext": {
			"github.com/greatliontech/gomutant.TestFilterTargets",
			"github.com/greatliontech/gomutant.TestTargetPreparationContextCancellation",
		},
	}
	for _, target := range targets {
		oracle, ok := want[target.Symbol]
		if !ok || !reflect.DeepEqual(target.Oracle, oracle) {
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
		if !description.OracleExplicit || len(description.Oracle) == 0 || description.Skipped != "" {
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
// one strict JSON document onto the same model.
func TestParseTargets(t *testing.T) {
	doc := `{"targets": [
		{"symbol": "example.com/p.F"},
		{"symbol": "example.com/p.G", "oracle": ["example.com/p.TestG"], "labels": ["REQ-x"]},
		{"symbol": "example.com/p.H", "oracle": [], "oracleExplicit": true}
	]}` + "  \n\t"
	targets, err := ParseTargets([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	want := []Target{
		{Symbol: "example.com/p.F"},
		{Symbol: "example.com/p.G", Oracle: []string{"example.com/p.TestG"}, Labels: []string{"REQ-x"}},
		{Symbol: "example.com/p.H", Oracle: []string{}, OracleExplicit: true},
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
	for name, malformed := range map[string]string{
		"missing targets":      `{}`,
		"null targets":         `{"targets":null}`,
		"unknown top field":    `{"targets":[],"extra":true}`,
		"wrong-case targets":   `{"Targets":[]}`,
		"duplicate targets":    `{"targets":[],"targets":[]}`,
		"null target":          `{"targets":[null]}`,
		"misspelled oracle":    `{"targets":[{"symbol":"p.F","orcale":["p.TestF"]}]}`,
		"unknown target field": `{"targets":[{"symbol":"p.F","extra":true}]}`,
		"wrong-case oracle":    `{"targets":[{"symbol":"p.F","Oracle":[]}]}`,
		"case-fold duplicate":  `{"targets":[{"symbol":"p.F","oracle":[],"Oracle":["p.TestF"]}]}`,
		"duplicate symbol":     `{"targets":[{"symbol":"p.F","symbol":"p.G"}]}`,
		"null symbol":          `{"targets":[{"symbol":null}]}`,
		"null oracle":          `{"targets":[{"symbol":"p.F","oracle":null}]}`,
		"null oracle element":  `{"targets":[{"symbol":"p.F","oracle":[null]}]}`,
		"null labels":          `{"targets":[{"symbol":"p.F","labels":null}]}`,
		"null labels element":  `{"targets":[{"symbol":"p.F","labels":[null]}]}`,
		"null oracle explicit": `{"targets":[{"symbol":"p.F","oracleExplicit":null}]}`,
		"trailing document":    `{"targets":[]} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseTargets([]byte(malformed)); err == nil {
				t.Fatalf("malformed target document accepted: %s", malformed)
			}
		})
	}
	invalidUTF8 := []byte(`{"targets":[{"symbol":"p.F"}]}`)
	invalidUTF8[len(invalidUTF8)-5] = 0xff
	if _, err := ParseTargets(invalidUTF8); err == nil {
		t.Fatal("invalid UTF-8 target document accepted")
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
