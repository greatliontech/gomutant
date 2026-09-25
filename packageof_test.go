package gomutant

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// dottedTree loads a module holding a package x and its dotted sibling
// x.go, the pair a string cut cannot tell apart, and a main package in
// a directory ending in .test, the spelling of a synthesized test main.
func dottedTree(t *testing.T) *Tree {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":                "module example.com/dotted\n\ngo 1.26.4\n",
		"x/x.go":                "package x\n\ntype T struct{}\n\nfunc (T) M() int {\n\treturn 1\n}\n\ntype test struct{}\n\nfunc (test) N() int {\n\treturn 2\n}\n",
		"x/x_test.go":           "package x\n\nimport \"testing\"\n\nfunc TestM(t *testing.T) { if (T{}).M() != 1 { t.Fatal() } }\n",
		"x.go/f.go":             "package xgo\n\nfunc F(v int) int {\n\tif v > 0 {\n\t\treturn v\n\t}\n\treturn -v\n}\n",
		"x.go/f_test.go":        "package xgo\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) { if F(1) != 1 { t.Fatal() } }\n\n// TestM links nothing of x: an explicit oracle for x.T.M that is cross-package\n// by package alone, so x stays outside x.go's test binary and derived oracle.\nfunc TestM(t *testing.T) {}\n",
		"cmd/tool.test/main.go": "package main\n\nfunc run() int { return 3 }\n\nfunc main() { _ = run() }\n",
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
	return tree
}

// A subject's package is the loaded package that owns it, so a method
// of x and a function of its dotted sibling x.go resolve apart where
// the string cut names one prefix for both; a method of a type named
// test is x's, never the synthesized test main's, while a real main
// package in a directory ending in .test is its own; a symbol under x's
// path with no loaded sibling beneath it is x's whatever its spelling;
// a symbol no loaded package's path prefixes falls back to the cut.
func TestPackageOfResolvesThroughTheLoadedPackages(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a fixture tree")
	}
	tree := dottedTree(t)
	for symbol, want := range map[string]string{
		"example.com/dotted/x.T.M":             "example.com/dotted/x",
		"example.com/dotted/x.TestM":           "example.com/dotted/x",
		"example.com/dotted/x.go.F":            "example.com/dotted/x.go",
		"example.com/dotted/x.go.TestF":        "example.com/dotted/x.go",
		"example.com/dotted/x.test.N":          "example.com/dotted/x",
		"example.com/dotted/cmd/tool.test.run": "example.com/dotted/cmd/tool.test",
		"example.com/dotted/x.zz.F":            "example.com/dotted/x",
		"example.com/dotted/gone.go.F":         "example.com/dotted/gone",
	} {
		if got := tree.PackageOf(symbol); got != want {
			t.Fatalf("PackageOf(%s) = %q, want %q", symbol, got, want)
		}
	}
}

// The delta cut places a dotted package's survivors by the package the
// loaded tree owns them under, never by a cut that names its sibling.
func TestCutSurvivorsResolvesADottedPackage(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a fixture tree")
	}
	tree := dottedTree(t)
	ctx := context.Background()
	current, err := tree.eng.BodyHashContext(ctx, "example.com/dotted/x.go.F")
	if err != nil {
		t.Fatal(err)
	}
	cut := DeltaCut{Ref: "HEAD", Added: map[string][]LineRange{"x.go/f.go": {{From: 4, To: 6}}}, Changed: map[string]bool{"example.com/dotted/x.go.F": true}}
	f := Finding{Symbol: "example.com/dotted/x.go.F", BodyHash: current, Survivors: []Survivor{
		{Position: "f.go:4:5", Operator: "condition: negate"},
		{Position: "f.go:7:2", Operator: "statement: delete"},
	}}
	split, err := tree.CutSurvivorsContext(ctx, f, cut)
	if err != nil {
		t.Fatal(err)
	}
	if len(split.OnDelta) != 1 || split.OnDelta[0].Position != "f.go:4:5" || len(split.Remainder) != 1 {
		t.Fatalf("cut = %+v, want the line-4 survivor on the delta and the line-7 one as remainder", split)
	}
}

// A pass is priced by the packages its subjects span as the loaded
// tree owns them: a method of x and a function of its dotted sibling
// x.go, with their tests, are five subjects in two packages, not one.
func TestPricedPassCountsPackagesAsTheTreeOwnsThem(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tree := dottedTree(t)
	var views []PreparationEvent
	_, err := tree.Run(context.Background(), []Target{{Symbol: "example.com/dotted/x.T.M"}, {Symbol: "example.com/dotted/x.go.F"}}, Options{Budget: 1, OracleTimeout: 2 * time.Minute,
		Progress: func(e PreparationEvent) {
			if e.Stage == PreparationViews {
				views = append(views, e)
			}
		}})
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].Subjects != 5 || views[0].Packages != 2 {
		t.Fatalf("priced views = %+v, want one pass over 5 subjects in 2 packages", views)
	}
}

// The attestation the run and the inspection grant a record follows
// the packages the tree owns, at every site that reads it: a target of
// x measured by a test of its dotted sibling x.go is cross-package, so
// it and a target measured by its own package's tests take two view
// modes — two priced view passes in the run, two view builds in the
// inspection — where a string cut would fold them into one.
func TestPackageProcessAttestationFollowsTheTreesPackages(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tree := dottedTree(t)
	ctx := context.Background()
	views := 0
	findings, err := tree.Run(ctx, []Target{
		{Symbol: "example.com/dotted/x.go.F"},
		{Symbol: "example.com/dotted/x.T.M", Oracle: []string{"example.com/dotted/x.go.TestM"}, OracleExplicit: true},
	}, Options{Budget: 1, OracleTimeout: 2 * time.Minute, Progress: func(e PreparationEvent) {
		if e.Stage == PreparationViews {
			views++
		}
	}})
	if err != nil || len(findings) != 2 {
		t.Fatalf("run = %+v, %v", findings, err)
	}
	if views != 2 {
		t.Fatalf("priced view passes = %d, want one per view mode (attested and cross-package)", views)
	}
	builds := 0
	if _, err := tree.InspectFindings(ctx, findings, func(stage string) {
		if strings.HasPrefix(stage, "building views for") {
			builds++
		}
	}); err != nil {
		t.Fatal(err)
	}
	if builds != 2 {
		t.Fatalf("inspection view builds = %d, want one per view mode", builds)
	}
}

// A prior record is judged under its own view mode at every site: the
// moved-pin attribution hands the run's set, built under the target's
// current attestation, to a record measured under a cross-package
// oracle, and the record's views are rebuilt under its own mode rather
// than served from that set (REQ-result-record).
func TestMovedPinAttributionReadsViewsUnderTheRecordsMode(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tree := dottedTree(t)
	ctx := context.Background()
	prior, err := tree.Run(ctx, []Target{{Symbol: "example.com/dotted/x.T.M", Oracle: []string{"example.com/dotted/x.go.TestM"}, OracleExplicit: true}}, Options{Budget: 1, OracleTimeout: 2 * time.Minute})
	if err != nil || len(prior) != 1 {
		t.Fatalf("prior run = %+v, %v", prior, err)
	}
	var supplementary [][]string
	old := seams.inspectionSupplementaryView
	seams.inspectionSupplementaryView = func(symbols []string) { supplementary = append(supplementary, slices.Clone(symbols)) }
	t.Cleanup(func() { seams.inspectionSupplementaryView = old })
	// The oracle selection moved to the derived, same-package oracle:
	// the run's set is attested, the record is not.
	findings, err := tree.Run(ctx, []Target{{Symbol: "example.com/dotted/x.T.M"}}, Options{Budget: 1, OracleTimeout: 2 * time.Minute, Prior: prior})
	if err != nil || len(findings) != 1 || findings[0].Cached {
		t.Fatalf("re-run = %+v, %v; want the record re-measured", findings, err)
	}
	rebuilt := false
	for _, symbols := range supplementary {
		if slices.Contains(symbols, "example.com/dotted/x.T.M") {
			rebuilt = true
		}
	}
	if !rebuilt {
		t.Fatalf("supplementary views = %v, want the target's view rebuilt under the record's mode, never served from the attested set", supplementary)
	}
}
