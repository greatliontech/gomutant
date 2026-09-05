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

// A plan reaches the bracket-path preflight: an absent bracket path
// refuses before any target prepares, so the plan pays neither proof
// nor probe for a run that could not have measured
// (REQ-exec-preparation's loaded-set stage, REQ-exec-plan-only).
func TestRunPlanRefusesAbsentBracketPath(t *testing.T) {
	root, _ := stagedFixture(t)
	tree, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tree.Run(context.Background(), stagedTarget(), Options{Budget: 1, PlanOnly: true, BracketPaths: []string{"transient/per-test"}})
	if err == nil || !strings.Contains(err.Error(), "does not exist at run start") {
		t.Fatalf("plan with an absent bracket path: %v; want the preflight refusal", err)
	}
	// The executing run refuses before its observed union: the
	// preflight precedes the first proof attempt.
	unions := 0
	_, err = tree.Run(context.Background(), stagedTarget(), Options{Budget: 1, OracleTimeout: 2 * time.Minute, BracketPaths: []string{"transient/per-test"}, proofAttempt: func(string, int) { unions++ }})
	if err == nil || !strings.Contains(err.Error(), "does not exist at run start") || unions != 0 {
		t.Fatalf("run with an absent bracket path: %v after %d proof attempts; want the refusal before any", err, unions)
	}
}

// externalFixture is stagedFixture with the measured package importing
// a replace module outside the repository: a compile input the index
// snapshot can never vouch for.
func externalFixture(t *testing.T) (string, func(...string)) {
	t.Helper()
	root, runGit := stagedFixture(t)
	ext := filepath.Join(t.TempDir(), "ext")
	if err := os.MkdirAll(ext, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		filepath.Join(ext, "go.mod"):  "module example.com/ext\n\ngo 1.26.4\n",
		filepath.Join(ext, "ext.go"):  "package ext\n\nfunc V() int { return 0 }\n",
		filepath.Join(root, "go.mod"): "module example.com/staged\n\ngo 1.26.4\n\nrequire example.com/ext v0.0.0\n\nreplace example.com/ext => " + ext + "\n",
		filepath.Join(root, "p.go"):   "package staged\n\nimport \"example.com/ext\"\n\nfunc F(x int) int {\n\tif x > 100 {\n\t\treturn x - 1\n\t}\n\treturn x + ext.V()\n}\n",
	} {
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGit("add", "-A")
	runGit("commit", "-q", "-m", "external replace")
	return root, runGit
}

// A staged run refuses a target whose compile inputs include a file
// outside the repository at preparation, naming the input, before any
// proof or probe — a plan shows the same decision — while the worktree
// form measures it: the identity is not git's to vouch for and is not
// drift (REQ-result-staged, REQ-result-layers).
func TestStagedRunRefusesExternalInputAtPreparation(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	root, _ := externalFixture(t)
	tree, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	var decisions []RunDecision
	probes := 0
	opts := Options{Budget: 1, OracleTimeout: 2 * time.Minute, Staged: true, PlanOnly: true,
		Decision: func(d RunDecision) { decisions = append(decisions, d) }, proofAttempt: func(string, int) { probes++ }}
	if _, err := tree.Run(context.Background(), stagedTarget(), opts); err == nil {
		t.Fatal("staged plan over an external input reported no refused set")
	}
	if len(decisions) != 1 || decisions[0].Action != "skipped" || !strings.Contains(decisions[0].Reason, "measured input outside the repository: ") || !strings.Contains(decisions[0].Reason, "ext.go") || strings.Contains(decisions[0].Reason, "drift") {
		t.Fatalf("staged plan decisions = %+v; want one skip naming the external input, never drift", decisions)
	}
	if probes != 0 {
		t.Fatalf("the refused target built %d proof attempts; want none", probes)
	}
	opts.PlanOnly = false
	decisions = nil
	if _, err := tree.Run(context.Background(), stagedTarget(), opts); err == nil || !strings.Contains(err.Error(), "measured input outside the repository") {
		t.Fatalf("staged run over an external input: %v; want the refused set naming it", err)
	}
	if len(decisions) != 1 || decisions[0].Action != "skipped" || probes != 0 {
		t.Fatalf("staged run decisions = %+v with %d proof attempts; want the preparation refusal alone", decisions, probes)
	}
	worktree, err := tree.Run(context.Background(), stagedTarget(), Options{Budget: 1, OracleTimeout: 2 * time.Minute})
	if err != nil || len(worktree) != 1 || worktree[0].Skipped != "" {
		t.Fatalf("worktree run over an external input = %+v, %v; want a measurement", worktree, err)
	}
	if worktree[0].Dirty {
		t.Fatalf("worktree record stamped dirty over a clean tree: an identity outside the repository is not git-visible drift")
	}
	// Served or measured: a staged run that would serve the worktree
	// record refuses the target the same way, before the serve.
	decisions, probes = nil, 0
	opts.PlanOnly, opts.Prior = true, worktree
	if _, err := tree.Run(context.Background(), stagedTarget(), opts); err == nil {
		t.Fatal("staged plan with a prior over an external input reported no refused set")
	}
	if len(decisions) != 1 || decisions[0].Action != "skipped" || !strings.Contains(decisions[0].Reason, "measured input outside the repository") || probes != 0 {
		t.Fatalf("staged plan with a prior: decisions = %+v, %d proof attempts; want the refusal before the serve", decisions, probes)
	}
}

// The dirty judgment drops an identity physically outside the
// repository from its pathspec instead of answering dirty, keeps an
// alias of an in-repo file by its physical form, and keeps a spelling
// whose physical form cannot be established (REQ-result-layers).
func TestDirtyJudgmentPlacesIdentitiesByPhysicalForm(t *testing.T) {
	root, runGit := stagedFixture(t)
	repository := repositoryState{root: root, available: true}
	external := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(external, []byte("package outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty, causes, err := repository.pathsDirtyContext(context.Background(), []string{external, filepath.Join(root, "p.go")})
	if err != nil || dirty {
		t.Fatalf("clean tree with an external identity judged dirty (%v): %v", causes, err)
	}
	if specs, kind := repository.placement(root); kind != placedInRepo || len(specs) != 0 {
		t.Fatalf("placement(root) = %q, %v; want in-repo with nothing to ask git", specs, kind)
	}
	if specs, kind := repository.placement(external); kind != placedOutside || len(specs) != 0 {
		t.Fatalf("placement(external) = %q, %v; want outside with nothing to ask git", specs, kind)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Skip(err)
	}
	if specs, kind := repository.placement(filepath.Join(alias, "p.go")); kind != placedInRepo || !slices.Equal(specs, []string{"p.go"}) {
		t.Fatalf("placement(alias) = %q, %v; want the in-repo physical form", specs, kind)
	}
	missingOutside := filepath.Join(filepath.Dir(external), "gone", "x.go")
	if specs, kind := repository.placement(missingOutside); kind != placedOutsideMissing || len(specs) != 0 {
		t.Fatalf("placement(missing under an external ancestor) = %q, %v; want outside as missing", specs, kind)
	}
	if specs, kind := repository.placement(filepath.Join(alias, "gone", "x.go")); kind != placedInRepo || !slices.Equal(specs, []string{"gone"}) {
		t.Fatalf("placement(missing under an in-repo alias) = %q, %v; want the pathspec at the first unresolved component", specs, kind)
	}
	// The escape direction: an in-repo spelling through a tracked
	// symlink out of the tree is outside — git never saw its bytes —
	// while the link itself stays git's to judge: committed, the
	// identity is clean; repointed, the link's own entry is drift.
	escape := filepath.Join(root, "link")
	if err := os.Symlink(filepath.Dir(external), escape); err != nil {
		t.Skip(err)
	}
	runGit("add", "link")
	runGit("commit", "-q", "-m", "link out of the tree")
	escaped := filepath.Join(escape, "outside.go")
	if specs, kind := repository.placement(escaped); kind != placedOutside || !slices.Equal(specs, []string{"link"}) {
		t.Fatalf("placement(in-repo spelling of an external file) = %q, %v; want outside, the link asked about", specs, kind)
	}
	if dirty, causes, err := repository.pathsDirtyContext(context.Background(), []string{escaped}); err != nil || dirty {
		t.Fatalf("escaped identity through a committed link judged dirty (%v): %v", causes, err)
	}
	if err := os.Remove(escape); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), escape); err != nil {
		t.Fatal(err)
	}
	if dirty, causes, err := repository.pathsDirtyContext(context.Background(), []string{escaped}); err != nil || !dirty {
		t.Fatalf("repointed link judged clean (%v): %v", causes, err)
	}
	// A tracked in-repo symlink to a tracked in-repo file: the physical
	// form and the link's own entry are both asked about, so repointing
	// the link is drift even though the file it now names is clean.
	shim := filepath.Join(root, "shim.go")
	if err := os.Symlink("p.go", shim); err != nil {
		t.Fatal(err)
	}
	runGit("add", "shim.go")
	runGit("commit", "-q", "-m", "shim")
	if specs, kind := repository.placement(shim); kind != placedInRepo || !slices.Equal(specs, []string{"shim.go", "p.go"}) {
		t.Fatalf("placement(in-repo link to an in-repo file) = %q, %v; want the link and its physical form", specs, kind)
	}
	if dirty, causes, err := repository.pathsDirtyContext(context.Background(), []string{shim}); err != nil || dirty {
		t.Fatalf("committed in-repo link judged dirty (%v): %v", causes, err)
	}
	if err := os.Remove(shim); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("p_test.go", shim); err != nil {
		t.Fatal(err)
	}
	if dirty, causes, err := repository.pathsDirtyContext(context.Background(), []string{shim}); err != nil || !dirty {
		t.Fatalf("repointed in-repo link judged clean (%v): %v", causes, err)
	}
}

// A plan's decisions are the executing run's decisions: skipping the
// observed producer union changes nothing the plan reports
// (REQ-exec-plan-only).
func TestPlanDecisionsMatchExecution(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	root, _ := stagedFixture(t)
	tree, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	collect := func(plan bool) ([]RunDecision, int) {
		var decisions []RunDecision
		unions := 0
		if _, err := tree.Run(context.Background(), stagedTarget(), Options{Budget: 1, OracleTimeout: 2 * time.Minute, PlanOnly: plan,
			Decision: func(d RunDecision) { decisions = append(decisions, d) }, proofAttempt: func(_ string, attempt int) { unions++ }}); err != nil {
			t.Fatal(err)
		}
		return decisions, unions
	}
	planned, planUnions := collect(true)
	executed, runUnions := collect(false)
	if len(planned) != 1 || len(executed) != 1 || planned[0] != executed[0] {
		t.Fatalf("plan decision %+v != executing decision %+v", planned, executed)
	}
	if planUnions != 0 || runUnions == 0 {
		t.Fatalf("producer union built %d times by the plan and %d by the run; want none and some", planUnions, runUnions)
	}
}

func guardRecipe() Target {
	return Target{Symbol: "recipe:guard-empty-input",
		Manual: &ManualSpec{File: "guard/guard.go", Edits: []ManualEdit{{Find: `if s == "" {`, Replace: `if false {`}}},
		Oracle: []string{"example.com/shaped/guard.TestEmptyRefused"}, OracleExplicit: true}
}

// The shaped preparation path shares the plan's contract: its decision
// equals the executing run's and the plan builds no observed union
// (REQ-exec-plan-only).
func TestShapedPlanDecisionsMatchExecution(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per shaped candidate")
	}
	tmp := writeShapedFixture(t)
	gitInitCommit(t, tmp)
	tree, err := Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	collect := func(plan bool) ([]RunDecision, int) {
		var decisions []RunDecision
		unions := 0
		if _, err := tree.Run(context.Background(), []Target{guardRecipe()}, Options{OracleTimeout: 2 * time.Minute, PlanOnly: plan,
			Decision: func(d RunDecision) { decisions = append(decisions, d) }, proofAttempt: func(string, int) { unions++ }}); err != nil {
			t.Fatal(err)
		}
		return decisions, unions
	}
	planned, planUnions := collect(true)
	executed, runUnions := collect(false)
	if len(planned) != 1 || len(executed) != 1 || planned[0] != executed[0] {
		t.Fatalf("shaped plan decision %+v != executing decision %+v", planned, executed)
	}
	if planUnions != 0 || runUnions == 0 {
		t.Fatalf("producer union built %d times by the shaped plan and %d by the run; want none and some", planUnions, runUnions)
	}
}

// A shaped target whose recipe file's closure reaches a module outside
// the repository refuses at preparation under a staged run, exactly as
// a symbol target does (REQ-result-staged).
func TestStagedRunRefusesExternalInputForShapedTarget(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
	tmp := writeShapedFixture(t)
	ext := filepath.Join(t.TempDir(), "ext")
	if err := os.MkdirAll(ext, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		filepath.Join(ext, "go.mod"):            "module example.com/ext\n\ngo 1.26\n",
		filepath.Join(ext, "ext.go"):            "package ext\n\nfunc Empty() string { return \"\" }\n",
		filepath.Join(tmp, "go.mod"):            "module example.com/shaped\n\ngo 1.26\n\nrequire example.com/ext v0.0.0\n\nreplace example.com/ext => " + ext + "\n",
		filepath.Join(tmp, "guard", "guard.go"): "package guard\n\nimport (\n\t\"errors\"\n\n\t\"example.com/ext\"\n)\n\nfunc Parse(s string) (string, error) {\n\tif s == \"\" {\n\t\treturn ext.Empty(), errors.New(\"empty input\")\n\t}\n\treturn s, nil\n}\n",
	} {
		if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitInitCommit(t, tmp)
	tree, err := Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	var decisions []RunDecision
	probes := 0
	_, err = tree.Run(context.Background(), []Target{guardRecipe()}, Options{OracleTimeout: 2 * time.Minute, Staged: true, PlanOnly: true,
		Decision: func(d RunDecision) { decisions = append(decisions, d) }, proofAttempt: func(string, int) { probes++ }})
	if err == nil {
		t.Fatal("staged plan over an external input reported no refused set")
	}
	if len(decisions) != 1 || decisions[0].Action != "skipped" || !strings.Contains(decisions[0].Reason, "measured input outside the repository: ") || !strings.Contains(decisions[0].Reason, "ext.go") || probes != 0 {
		t.Fatalf("shaped staged plan decisions = %+v with %d proof attempts; want the preparation refusal naming the external input", decisions, probes)
	}
}

// The shaped preparation path preflights its oracle modules too: an
// absent bracket path refuses a shaped target's plan and its run
// before any proof (REQ-exec-preparation's loaded-set stage).
func TestShapedRunRefusesAbsentBracketPath(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
	tmp := writeShapedFixture(t)
	gitInitCommit(t, tmp)
	tree, err := Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	for _, plan := range []bool{true, false} {
		unions := 0
		_, err := tree.Run(context.Background(), []Target{guardRecipe()}, Options{OracleTimeout: 2 * time.Minute, PlanOnly: plan, BracketPaths: []string{"transient/per-test"}, proofAttempt: func(string, int) { unions++ }})
		if err == nil || !strings.Contains(err.Error(), "does not exist at run start") || unions != 0 {
			t.Fatalf("shaped target with an absent bracket path (plan=%v): %v after %d proof attempts; want the refusal before any", plan, err, unions)
		}
	}
}
