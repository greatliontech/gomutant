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
	_, d1, err := tree.shapedCandidates(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	moved := base
	moved.Manual = &ManualSpec{File: "guard/guard.go", Edits: []ManualEdit{{Find: `if s == "" {`, Replace: `if len(s) < 0 {`}}}
	_, d2, err := tree.shapedCandidates(context.Background(), moved)
	if err != nil {
		t.Fatal(err)
	}
	if d1 == d2 {
		t.Fatal("a changed edit did not move the shape digest")
	}
	// A duplicated find refuses: position-stability is the contract.
	dup := base
	dup.Manual = &ManualSpec{File: "guard/guard.go", Edits: []ManualEdit{{Find: "return", Replace: "goto out; return"}}}
	if _, _, err := tree.shapedCandidates(context.Background(), dup); err == nil || !strings.Contains(err.Error(), "exactly once") {
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
	probes, digest, err := tree.shapedCandidates(context.Background(), Target{Symbol: "s",
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
	if _, _, err := tree.shapedCandidates(context.Background(), Target{Symbol: "s",
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
	probes, _, err := tree.shapedCandidates(context.Background(), Target{Symbol: "s",
		Structural: &StructuralSpec{Class: "interface-satisfaction", Type: "example.com/shaped/iface.Impl", Interface: "example.com/shaped/iface.Doer"},
		Oracle:     []string{"example.com/shaped/iface.TestSatisfies"}, OracleExplicit: true})
	if err != nil || len(probes) != 1 {
		t.Fatalf("probes=%d err=%v", len(probes), err)
	}
	src := string(probes[0].Replacements[0].Source)
	if !strings.Contains(src, "Do_gomutantStructuralProbe() int") || strings.Contains(src, "func (Impl) Do() int") {
		t.Fatalf("declaration not renamed: %s", src)
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
