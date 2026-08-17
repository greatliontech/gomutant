package gomutant

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeShapedFixture(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":         "module example.com/shaped\n\ngo 1.26\n",
		"core/core.go":   "package core\n\nfunc Core() int { return 1 }\n",
		"forbidden/f.go": "package forbidden\n\nfunc F() {}\n",
		// The toothy oracle: parses the core package's source at RUNTIME
		// and fails on any import of the forbidden path — the
		// analyzer-shaped oracle class the structural probe exists to
		// check. It reads from disk, which is exactly why the probe must
		// exist in a scratch tree rather than a build overlay.
		"arch/arch_test.go": `package arch

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestNoForbidden(t *testing.T) {
	pkgs, err := parser.ParseDir(token.NewFileSet(), "../core", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, pkg := range pkgs {
		for file, f := range pkg.Files {
			for _, imp := range f.Imports {
				if strings.Contains(imp.Path.Value, "example.com/shaped/forbidden") {
					t.Fatalf("%s imports the forbidden package", file)
				}
			}
		}
	}
}

func TestVacuous(t *testing.T) {}
`,
		// Interface-satisfaction subjects: the oracle's teeth are the
		// compiler's own.
		"iface/iface.go": "package iface\n\ntype Doer interface {\n\tDo() int\n}\n\ntype Impl struct{}\n\nfunc (Impl) Do() int { return 1 }\n",
		"iface/iface_test.go": `package iface

import "testing"

var _ Doer = Impl{}

func TestSatisfies(t *testing.T) {
	var d Doer = Impl{}
	if d.Do() != 1 {
		t.Fatal()
	}
}
`,
		// Manual-recipe subject: a parser guard the recipe removes.
		"guard/guard.go":      "package guard\n\nimport \"errors\"\n\nfunc Parse(s string) (string, error) {\n\tif s == \"\" {\n\t\treturn \"\", errors.New(\"empty input\")\n\t}\n\treturn s, nil\n}\n",
		"guard/guard_test.go": "package guard\n\nimport \"testing\"\n\nfunc TestEmptyRefused(t *testing.T) {\n\tif _, err := Parse(\"\"); err == nil {\n\t\tt.Fatal(\"empty input accepted\")\n\t}\n}\n",
	} {
		path := filepath.Join(tmp, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return tmp
}

// The import-boundary structural class end to end: the probe overlays a
// blank import of the forbidden path on disk in a scratch tree, a
// toothy analyzer oracle kills it, a vacuous oracle survives it, and an
// unchanged shape with fresh oracle evidence serves wholesale
// (REQ-target-structural, REQ-result-stale).
func TestStructuralImportBoundaryChecksOracleTeeth(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per shaped candidate")
	}
	tmp := writeShapedFixture(t)
	tree, err := Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	spec := &StructuralSpec{Class: "import-boundary", Packages: []string{"example.com/shaped/core"}, Forbidden: "example.com/shaped/forbidden"}
	toothy := Target{Symbol: "structural:core-no-forbidden", Structural: spec,
		Oracle: []string{"example.com/shaped/arch.TestNoForbidden"}, OracleExplicit: true}
	vacuous := Target{Symbol: "structural:core-no-forbidden-vacuous", Structural: spec,
		Oracle: []string{"example.com/shaped/arch.TestVacuous"}, OracleExplicit: true, Labels: []string{"expected-vacuous"}}
	// The analyzer oracle reads the scoped package's sources at
	// runtime: declaring them as a bracket path is the caller's
	// assertion that surface is mutation-free for the run — without it
	// the oracle's evidence is honestly unverifiable and the finding
	// can never serve.
	findings, err := tree.Run(context.Background(), []Target{toothy, vacuous}, Options{OracleTimeout: 2 * time.Minute, BracketPaths: []string{"core"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 {
		t.Fatalf("findings = %d, want 2", len(findings))
	}
	if f := findings[0]; f.Skipped != "" || f.Mutants != 1 || f.Killed != 1 || f.Shape == nil {
		t.Fatalf("toothy oracle finding: %+v", f)
	}
	if f := findings[1]; f.Skipped != "" || f.Mutants != 1 || f.Killed != 0 || len(f.Survivors) != 1 {
		t.Fatalf("vacuous oracle did not surface a survivor: %+v", f)
	}
	if findings[1].Survivors[0].Operator != "structural: import-boundary" {
		t.Fatalf("survivor operator = %q", findings[1].Survivors[0].Operator)
	}

	// An analyzer oracle reaching go/parser is not observation-closed
	// (unsafe reachable), so its evidence honestly never validates and
	// the finding re-measures every run — the serve/re-measure pins are
	// witnessed on the manual recipe, whose plain oracle is
	// observation-closed.
	if findings[0].OracleEvidence[0].ObservationObservable {
		t.Fatalf("fixture assumption moved: the parser oracle became observation-closed; move the serve arm back here: %+v", findings[0].OracleEvidence[0])
	}
	rerun, err := tree.Run(context.Background(), []Target{toothy}, Options{OracleTimeout: 2 * time.Minute, BracketPaths: []string{"core"}, Prior: findings})
	if err != nil {
		t.Fatal(err)
	}
	if rerun[0].Cached {
		t.Fatal("unverifiable-evidence prior served")
	}
}

// The interface-satisfaction class: each broken method must refuse the
// oracle — here through the compiler itself, the natural teeth of a
// static satisfaction assertion (REQ-target-structural).
func TestStructuralInterfaceSatisfactionKillsThroughCompiler(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per shaped candidate")
	}
	tmp := writeShapedFixture(t)
	tree, err := Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	target := Target{Symbol: "structural:impl-satisfies-doer",
		Structural: &StructuralSpec{Class: "interface-satisfaction", Type: "example.com/shaped/iface.Impl", Interface: "example.com/shaped/iface.Doer"},
		Oracle:     []string{"example.com/shaped/iface.TestSatisfies"}, OracleExplicit: true}
	findings, err := tree.Run(context.Background(), []Target{target}, Options{OracleTimeout: 2 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	f := findings[0]
	if f.Skipped != "" || f.Mutants != 1 || f.Killed != 1 {
		t.Fatalf("satisfaction probe not killed: %+v", f)
	}
	if len(f.Kills) != 1 || !strings.HasPrefix(f.Kills[0].Killer, "compile:") {
		t.Fatalf("kill not attributed to the compiler: %+v", f.Kills)
	}
}

// The manual recipe class: a caller-declared edit drops a parser guard,
// and the oracle proves malformed input still fails closed — the
// break-observe-restore cycle owned by the harness, with the caller's
// intent riding the label (REQ-target-manual-recipes).
func TestManualRecipeChecksGuardAdequacy(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per shaped candidate")
	}
	tmp := writeShapedFixture(t)
	gitInitCommit(t, tmp)
	tree, err := Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	target := Target{Symbol: "recipe:guard-empty-input",
		Manual: &ManualSpec{File: "guard/guard.go", Edits: []ManualEdit{{
			Find:    `if s == "" {`,
			Replace: `if false {`,
		}}},
		Oracle: []string{"example.com/shaped/guard.TestEmptyRefused"}, OracleExplicit: true,
		Labels: []string{"attacks:empty-input-guard"}}
	findings, err := tree.Run(context.Background(), []Target{target}, Options{OracleTimeout: 2 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	f := findings[0]
	if f.Skipped != "" || f.Mutants != 1 || f.Killed != 1 {
		t.Fatalf("guard recipe not killed: %+v", f)
	}
	if len(f.Labels) != 1 || f.Labels[0] != "attacks:empty-input-guard" {
		t.Fatalf("recipe label lost: %+v", f.Labels)
	}
	// The probed recipe file is committed and clean: the finding stamps
	// clean provenance — a relative probed-file path would force-stamp
	// dirty and strand the record machine-local (REQ-result-layers).
	if f.Dirty {
		t.Fatalf("clean committed tree stamped dirty provenance: %+v", f)
	}

	// Wholesale serve on unchanged shape and oracle pins; a changed
	// edit is a moved shape digest and re-measures (REQ-result-stale).
	served, err := tree.Run(context.Background(), []Target{target}, Options{OracleTimeout: 2 * time.Minute, Prior: findings})
	if err != nil {
		t.Fatal(err)
	}
	if !served[0].Cached {
		t.Fatalf("unchanged recipe did not serve: %+v", served[0])
	}
	moved := target
	moved.Manual = &ManualSpec{File: "guard/guard.go", Edits: []ManualEdit{{Find: `if s == "" {`, Replace: `if len(s) > 1_000_000 {`}}}
	remeasured, err := tree.Run(context.Background(), []Target{moved}, Options{OracleTimeout: 2 * time.Minute, Prior: findings})
	if err != nil {
		t.Fatal(err)
	}
	if remeasured[0].Cached {
		t.Fatal("a moved recipe served the prior finding")
	}
}

func gitInitCommit(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "-A"},
		{"-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "-m", "fixture"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

// A workspace tree's clean differential twin anchors at the clean
// scratch: GOWORK pointing into the mutant scratch would make the twin
// build mutant content, refusing valid compile kills
// (REQ-target-structural, REQ-exec-attribution).
func TestShapedWorkspaceCompileKill(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per shaped candidate")
	}
	root := t.TempDir()
	for name, content := range map[string]string{
		"go.work":             "go 1.26\n\nuse ./mod\n",
		"mod/go.mod":          "module example.com/ws\n\ngo 1.26\n",
		"mod/iface/iface.go":  "package iface\n\ntype Doer interface {\n\tDo() int\n}\n\ntype Impl struct{}\n\nfunc (Impl) Do() int { return 1 }\n",
		"mod/iface/iface_test.go": "package iface\n\nimport \"testing\"\n\nvar _ Doer = Impl{}\n\nfunc TestSatisfies(t *testing.T) {\n\tvar d Doer = Impl{}\n\tif d.Do() != 1 {\n\t\tt.Fatal()\n\t}\n}\n",
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tree, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	target := Target{Symbol: "structural:ws-satisfies",
		Structural: &StructuralSpec{Class: "interface-satisfaction", Type: "example.com/ws/iface.Impl", Interface: "example.com/ws/iface.Doer"},
		Oracle:     []string{"example.com/ws/iface.TestSatisfies"}, OracleExplicit: true}
	findings, err := tree.Run(context.Background(), []Target{target}, Options{OracleTimeout: 2 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if f := findings[0]; f.Skipped != "" || f.Killed != 1 {
		t.Fatalf("workspace satisfaction probe not killed: %+v", f)
	}
}
