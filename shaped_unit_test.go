package gomutant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Shaped-declaration validation refuses every malformed form before any
// resolution (REQ-target-structural, REQ-target-manual-recipes).
func TestShapedTargetValidationArms(t *testing.T) {
	oracle := []string{"example.com/m/p.TestX"}
	for name, tg := range map[string]Target{
		"both forms":         {Symbol: "s", Structural: &StructuralSpec{Class: "import-boundary", Packages: []string{"p"}, Forbidden: "f"}, Manual: &ManualSpec{File: "f.go", Edits: []ManualEdit{{Find: "a", Replace: "b"}}}, Oracle: oracle},
		"no oracle":          {Symbol: "s", Structural: &StructuralSpec{Class: "import-boundary", Packages: []string{"p"}, Forbidden: "f"}},
		"unknown class":      {Symbol: "s", Structural: &StructuralSpec{Class: "mystery"}, Oracle: oracle},
		"boundary no pkgs":   {Symbol: "s", Structural: &StructuralSpec{Class: "import-boundary", Forbidden: "f"}, Oracle: oracle},
		"boundary extras":    {Symbol: "s", Structural: &StructuralSpec{Class: "import-boundary", Packages: []string{"p"}, Forbidden: "f", Type: "T"}, Oracle: oracle},
		"satisfaction empty": {Symbol: "s", Structural: &StructuralSpec{Class: "interface-satisfaction", Type: "T"}, Oracle: oracle},
		"manual no edits":    {Symbol: "s", Manual: &ManualSpec{File: "f.go"}, Oracle: oracle},
		"manual empty find":  {Symbol: "s", Manual: &ManualSpec{File: "f.go", Edits: []ManualEdit{{Find: "", Replace: "b"}}}, Oracle: oracle},
		"manual no-op edit":  {Symbol: "s", Manual: &ManualSpec{File: "f.go", Edits: []ManualEdit{{Find: "a", Replace: "a"}}}, Oracle: oracle},
	} {
		if err := validateShapedTarget(tg); err == nil {
			t.Errorf("%s: malformed shaped target accepted", name)
		}
	}
	valid := Target{Symbol: "s", Structural: &StructuralSpec{Class: "import-boundary", Packages: []string{"p"}, Forbidden: "f"}, Oracle: oracle}
	if err := validateShapedTarget(valid); err != nil {
		t.Errorf("valid shaped target refused: %v", err)
	}
}

// The shape digest moves with the declared parameters and with the
// probed file content, so any moved input re-measures
// (REQ-result-stale via the shaped pin).
func TestShapeDigestMovesWithInputs(t *testing.T) {
	tmp := writeShapedFixture(t)
	tree, err := Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	base := Target{Symbol: "recipe:x",
		Manual: &ManualSpec{File: "guard/guard.go", Edits: []ManualEdit{{Find: `if s == "" {`, Replace: `if false {`}}},
		Oracle: []string{"example.com/shaped/guard.TestEmptyRefused"}, OracleExplicit: true}
	_, d1, _, err := tree.shapedCandidates(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	moved := base
	moved.Manual = &ManualSpec{File: "guard/guard.go", Edits: []ManualEdit{{Find: `if s == "" {`, Replace: `if len(s) < 0 {`}}}
	_, d2, _, err := tree.shapedCandidates(context.Background(), moved)
	if err != nil {
		t.Fatal(err)
	}
	if d1 == d2 {
		t.Fatal("a changed edit did not move the shape digest")
	}
	// A duplicated find refuses: position-stability is the contract.
	dup := base
	dup.Manual = &ManualSpec{File: "guard/guard.go", Edits: []ManualEdit{{Find: "return", Replace: "goto out; return"}}}
	if _, _, _, err := tree.shapedCandidates(context.Background(), dup); err == nil || !strings.Contains(err.Error(), "exactly once") {
		t.Fatalf("ambiguous find accepted: %v", err)
	}
}

// Import probes synthesize one blank-importing file per scoped package
// and refuse degenerate scopes (REQ-target-structural).
func TestImportProbesSynthesis(t *testing.T) {
	tmp := writeShapedFixture(t)
	tree, err := Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	probes, digest, _, err := tree.shapedCandidates(context.Background(), Target{Symbol: "s",
		Structural: &StructuralSpec{Class: "import-boundary", Packages: []string{"example.com/shaped/core"}, Forbidden: "example.com/shaped/forbidden"},
		Oracle:     []string{"example.com/shaped/arch.TestNoForbidden"}, OracleExplicit: true})
	if err != nil || len(probes) != 1 || digest == "" {
		t.Fatalf("probes=%d digest=%q err=%v", len(probes), digest, err)
	}
	src := string(probes[0].Replacements[0].Source)
	if !strings.Contains(src, "package core") || !strings.Contains(src, `_ "example.com/shaped/forbidden"`) {
		t.Fatalf("probe source: %s", src)
	}
	if !strings.HasSuffix(probes[0].Replacements[0].File, "zz_gomutant_structural_probe.go") {
		t.Fatalf("probe file: %s", probes[0].Replacements[0].File)
	}
	// The forbidden path being the scoped package itself refuses.
	if _, _, _, err := tree.shapedCandidates(context.Background(), Target{Symbol: "s",
		Structural: &StructuralSpec{Class: "import-boundary", Packages: []string{"example.com/shaped/core"}, Forbidden: "example.com/shaped/core"},
		Oracle:     []string{"example.com/shaped/arch.TestNoForbidden"}, OracleExplicit: true}); err == nil {
		t.Fatal("self-forbidding scope accepted")
	}
}

// Method probes rename exactly the asserted method's declaration; a
// satisfaction through embedding (no local declaration) refuses with
// the stated cause (REQ-target-structural).
func TestMethodProbesRewriteDeclaration(t *testing.T) {
	tmp := writeShapedFixture(t)
	tree, err := Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	probes, _, _, err := tree.shapedCandidates(context.Background(), Target{Symbol: "s",
		Structural: &StructuralSpec{Class: "interface-satisfaction", Type: "example.com/shaped/iface.Impl", Interface: "example.com/shaped/iface.Doer"},
		Oracle:     []string{"example.com/shaped/iface.TestSatisfies"}, OracleExplicit: true})
	if err != nil || len(probes) != 1 {
		t.Fatalf("probes=%d err=%v", len(probes), err)
	}
	src := string(probes[0].Replacements[0].Source)
	if !strings.Contains(src, "Do_gomutantStructuralProbe() int") || strings.Contains(src, "func (Impl) Do() int") {
		t.Fatalf("declaration not renamed: %s", src)
	}
	// Receiver discrimination: Decoy declares the same method name
	// BEFORE Impl, so a rewrite that stops matching by receiver renames
	// the wrong declaration — Decoy's must survive byte-intact.
	if !strings.Contains(src, "func (Decoy) Do() int") {
		t.Fatalf("the decoy's same-named method was renamed instead of the asserted type's: %s", src)
	}
}

// Lifecycle pruning keeps shaped findings: a shaped identity resolves
// to no declaration by design, so declaration absence is its normal
// state, never detachment (REQ-target-structural,
// REQ-target-manual-recipes).
func TestPruneKeepsShapedFindings(t *testing.T) {
	shapedFinding := lifecycleFinding("structural:boundary")
	shapedFinding.Shape = &TargetShape{Structural: &StructuralSpec{Class: "import-boundary", Packages: []string{"example.com/life"}, Forbidden: "example.com/other"}}
	shapedFinding.TargetEvidence = SubjectEvidence{}
	dead := lifecycleFinding("example.com/life.Gone")
	dead.TargetEvidence.Symbol = dead.Symbol
	tree, store := lifecycleModule(t, shapedFinding, dead)
	result, err := tree.PruneDetachedContext(context.Background(), store, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Removed) != 1 || result.Removed[0].Symbol != "example.com/life.Gone" || result.Kept != 1 {
		t.Fatalf("prune touched the shaped finding: %+v", result)
	}
	kept, err := store.Load(context.Background())
	if err != nil || len(kept) != 1 || kept[0].Symbol != "structural:boundary" {
		t.Fatalf("shaped finding not kept: %v %v", kept, err)
	}
}

// The whole-tree merge shed spares shaped findings exactly as pruning
// does: a shaped identity is never discovered, and a routine whole-tree
// run must not destroy the records (REQ-target-structural,
// REQ-target-manual-recipes).
func TestWholeTreeShedKeepsShapedFindings(t *testing.T) {
	shaped := lifecycleFinding("structural:kept")
	shaped.Shape = &TargetShape{Manual: &ManualSpec{File: "f.go", Edits: []ManualEdit{{Find: "a", Replace: "b"}}}}
	shaped.TargetEvidence = SubjectEvidence{}
	departed := lifecycleFinding("example.com/life.Gone")
	merged, _ := MergeWholeFindingsShed([]Finding{shaped, departed}, nil, []Target{{Symbol: "example.com/life.F"}})
	if len(merged) != 1 || merged[0].Symbol != "structural:kept" {
		t.Fatalf("whole-tree shed destroyed the shaped finding: %+v", merged)
	}
}

// The shaped serve matcher pins the property regime: a rapid-oracle
// record measured under other draws re-measures
// (REQ-exec-property-oracles, REQ-result-stale).
func TestShapedServeMatcherPinsRegime(t *testing.T) {
	prior := Finding{OperatorSet: shapedOperatorSet, OracleExplicit: true, OracleTimeout: "1m0s", PropertyRegime: ""}
	if ok, err := shapedEvidenceMatchesContext(context.Background(), prior, nil, shapedOperatorSet, "1m0s", 0, "rapid/v1"); err != nil || ok {
		t.Fatalf("regime mismatch served: ok=%v err=%v", ok, err)
	}
}

// A scratch-infrastructure fault (an out-of-tree replace directive the
// copy severs) is never a kill: the clean twin fails its oracle too, so
// the compile refusal is attributed to the scratch, not the probe
// (REQ-target-structural, REQ-exec-attribution).
func TestShapedScratchInfrastructureFaultNeverKills(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per shaped candidate")
	}
	root := t.TempDir()
	for name, content := range map[string]string{
		"outside/go.mod": "module example.com/outside\n\ngo 1.26\n",
		"outside/o.go":   "package outside\n\nfunc O() int { return 1 }\n",
		"mod/go.mod":     "module example.com/infra\n\ngo 1.26\n\nrequire example.com/outside v0.0.0\n\nreplace example.com/outside => ../outside\n",
		"mod/iface/iface.go": `package iface

import "example.com/outside"

type Doer interface {
	Do() int
}

type Impl struct{}

func (Impl) Do() int { return outside.O() }
`,
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
	tree, err := Load(filepath.Join(root, "mod"))
	if err != nil {
		t.Fatal(err)
	}
	target := Target{Symbol: "structural:infra",
		Structural: &StructuralSpec{Class: "interface-satisfaction", Type: "example.com/infra/iface.Impl", Interface: "example.com/infra/iface.Doer"},
		Oracle:     []string{"example.com/infra/iface.TestSatisfies"}, OracleExplicit: true}
	findings, err := tree.Run(context.Background(), []Target{target}, Options{OracleTimeout: 2 * time.Minute})
	if err == nil {
		if len(findings) == 1 && findings[0].Killed > 0 {
			t.Fatalf("scratch-infrastructure fault recorded a kill: %+v", findings[0])
		}
		return
	}
	if !strings.Contains(err.Error(), "scratch-infrastructure fault") && !strings.Contains(err.Error(), "does not build in the scratch tree") {
		t.Fatalf("fault not attributed to the scratch: %v", err)
	}
}

// Shape digests hash tree-relative identities, so the same content at
// two checkout roots derives the same pin — a shaped record travels
// exactly as its manifests do (REQ-result-stale, chunk-133 discipline).
func TestShapeDigestTravelsAcrossCheckoutRoots(t *testing.T) {
	targets := []Target{
		{Symbol: "recipe:x",
			Manual: &ManualSpec{File: "guard/guard.go", Edits: []ManualEdit{{Find: `if s == "" {`, Replace: `if false {`}}},
			Oracle: []string{"example.com/shaped/guard.TestEmptyRefused"}, OracleExplicit: true},
		{Symbol: "structural:link",
			Structural: &StructuralSpec{Class: "import-boundary", Packages: []string{"example.com/shaped/linkcore"}, Forbidden: "example.com/shaped/linkforbidden"},
			Oracle:     []string{"example.com/shaped/linkuser.TestUsesCore"}, OracleExplicit: true},
	}
	treeA, err := Load(writeShapedFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	treeB, err := Load(writeShapedFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, tg := range targets {
		_, da, _, err := treeA.shapedCandidates(context.Background(), tg)
		if err != nil {
			t.Fatal(err)
		}
		_, db, _, err := treeB.shapedCandidates(context.Background(), tg)
		if err != nil {
			t.Fatal(err)
		}
		if da != db {
			t.Errorf("%s: digest differs across checkout roots: %s vs %s", tg.Symbol, da, db)
		}
	}
}

// An external forbidden path — one the loaded tree does not carry —
// pins its linkage through the module-selection files, so a selection
// change re-measures conservatively; an in-tree linkage change moves
// the digest through the walked content itself
// (REQ-target-structural).
func TestShapeDigestPinsForbiddenLinkage(t *testing.T) {
	tmp := writeShapedFixture(t)
	tree, err := Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	external := Target{Symbol: "structural:no-strings",
		Structural: &StructuralSpec{Class: "import-boundary", Packages: []string{"example.com/shaped/linkcore"}, Forbidden: "strings"},
		Oracle:     []string{"example.com/shaped/linkuser.TestUsesCore"}, OracleExplicit: true}
	_, before, _, err := tree.shapedCandidates(context.Background(), external)
	if err != nil {
		t.Fatal(err)
	}
	appendFile := func(name, text string) {
		t.Helper()
		path := filepath.Join(tmp, name)
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(src, []byte(text)...), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	appendFile("go.mod", "\n// selection moved\n")
	_, after, _, err := tree.shapedCandidates(context.Background(), external)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Error("external forbidden linkage ignored a module-selection change")
	}

	inTree := Target{Symbol: "structural:no-linkforbidden",
		Structural: &StructuralSpec{Class: "import-boundary", Packages: []string{"example.com/shaped/linkcore"}, Forbidden: "example.com/shaped/linkforbidden"},
		Oracle:     []string{"example.com/shaped/linkuser.TestUsesCore"}, OracleExplicit: true}
	_, base, _, err := tree.shapedCandidates(context.Background(), inTree)
	if err != nil {
		t.Fatal(err)
	}
	// linkreg is reached transitively from linkforbidden: its content
	// rides the fold.
	appendFile("linkreg/reg.go", "\n// linkage moved\n")
	_, moved, _, err := tree.shapedCandidates(context.Background(), inTree)
	if err != nil {
		t.Fatal(err)
	}
	if base == moved {
		t.Error("in-tree forbidden linkage ignored a transitive content change")
	}
	// The embedded data linkforbidden compiles in is linked exactly as
	// its Go sources are: an edit to it moves the pin while every
	// function body stays byte-identical (the closure standard gofresh
	// sets — embedded files ride the closure).
	appendFile("linkforbidden/table.txt", "y")
	_, embedMoved, _, err := tree.shapedCandidates(context.Background(), inTree)
	if err != nil {
		t.Fatal(err)
	}
	if moved == embedMoved {
		t.Error("embedded data change ignored by the forbidden linkage fold")
	}
	// An untouched, unlinked package never moves the pin: the probed
	// package's own content stays outside the digest deliberately.
	appendFile("linkcore/core.go", "\n// probed content moved\n")
	_, probedMoved, _, err := tree.shapedCandidates(context.Background(), inTree)
	if err != nil {
		t.Fatal(err)
	}
	if embedMoved != probedMoved {
		t.Error("probed-package content moved the import-boundary digest — the deliberate exclusion regressed")
	}
}

// The two reaches no fold can pin refuse the derivation loudly: an
// in-tree path the load does not carry, and an out-of-tree reach
// while a local replace directive names source the selection files
// cannot content-pin (REQ-target-structural).
func TestShapeDigestRefusesUnpinnableForbiddenLinkage(t *testing.T) {
	tmp := writeShapedFixture(t)
	tree, err := Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	vanished := Target{Symbol: "structural:no-ghost",
		Structural: &StructuralSpec{Class: "import-boundary", Packages: []string{"example.com/shaped/linkcore"}, Forbidden: "example.com/shaped/ghost"},
		Oracle:     []string{"example.com/shaped/linkuser.TestUsesCore"}, OracleExplicit: true}
	if _, _, _, err := tree.shapedCandidates(context.Background(), vanished); err == nil || !strings.Contains(err.Error(), "not pinnable") {
		t.Errorf("vanished in-tree forbidden accepted: %v", err)
	}

	gomod := filepath.Join(tmp, "go.mod")
	src, err := os.ReadFile(gomod)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gomod, append(src, []byte("\nreplace example.com/other => ./othermod\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	external := Target{Symbol: "structural:no-strings",
		Structural: &StructuralSpec{Class: "import-boundary", Packages: []string{"example.com/shaped/linkcore"}, Forbidden: "strings"},
		Oracle:     []string{"example.com/shaped/linkuser.TestUsesCore"}, OracleExplicit: true}
	if _, _, _, err := tree.shapedCandidates(context.Background(), external); err == nil || !strings.Contains(err.Error(), "local replace") {
		t.Errorf("external forbidden under a local replace accepted: %v", err)
	}

	// The vendor twin: go.sum vouches for the module cache, never for
	// vendor/, so an external reach with a vendor manifest present
	// refuses on the same ground.
	if err := os.WriteFile(gomod, src, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "vendor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "vendor", "modules.txt"), []byte("# example.com/other v0.0.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := tree.shapedCandidates(context.Background(), external); err == nil || !strings.Contains(err.Error(), "vendor mode") {
		t.Errorf("external forbidden under vendor mode accepted: %v", err)
	}
}

// A workspace member above the tree root would hand the linkage fold
// absolute identities — folding them would silently root-key the
// digest and break record travel. The state is unreachable: Load
// itself refuses escaping go.work members, which this test pins (the
// digest derivation keeps its own escape refusal as fail-closed
// defense behind that guarantee).
func TestShapeDigestRefusesOutOfTreeLinkage(t *testing.T) {
	root := t.TempDir()
	for name, content := range map[string]string{
		"tree/go.work":         "go 1.26\n\nuse (\n\t.\n\t../sib\n)\n",
		"tree/go.mod":          "module example.com/tree\n\ngo 1.26\n",
		"tree/app/app.go":      "package app\n\nfunc App() int { return 1 }\n",
		"tree/app/app_test.go": "package app\n\nimport \"testing\"\n\nfunc TestApp(t *testing.T) {\n\tif App() != 1 {\n\t\tt.Fatal()\n\t}\n}\n",
		"sib/go.mod":           "module example.com/sib\n\ngo 1.26\n",
		"sib/pkg/pkg.go":       "package pkg\n\nfunc P() {}\n",
	} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Load(filepath.Join(root, "tree")); err == nil || !strings.Contains(err.Error(), "escapes the tree") {
		t.Errorf("out-of-tree workspace member loaded: %v", err)
	}
}
