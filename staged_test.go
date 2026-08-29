package gomutant

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func stagedFixture(t *testing.T) (string, func(...string)) {
	t.Helper()
	root := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=gomutant", "GIT_AUTHOR_EMAIL=gomutant@example.invalid",
			"GIT_COMMITTER_NAME=gomutant", "GIT_COMMITTER_EMAIL=gomutant@example.invalid",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	files := map[string]string{
		"go.mod":    "module example.com/staged\n\ngo 1.26.4\n",
		"p.go":      "package staged\n\nfunc F(x int) int {\n\tif x > 100 {\n\t\treturn x - 1\n\t}\n\treturn x\n}\n",
		"p_test.go": "package staged\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) {\n\tif F(5) != 5 {\n\t\tt.Fatal()\n\t}\n}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGit("init", "-q")
	runGit("add", "-A")
	runGit("commit", "-q", "-m", "initial")
	return root, runGit
}

func stagedTarget() []Target {
	return []Target{{Symbol: "example.com/staged.F", Oracle: []string{"example.com/staged.TestF"}, OracleExplicit: true}}
}

// A staged-but-uncommitted change is the measured subject: the staged
// run records clean provenance carrying the index tree identity, while
// the worktree run stamps the same tree dirty (REQ-result-staged).
func TestStagedRunPinsIndexSnapshot(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	root, runGit := stagedFixture(t)
	// Stage a real edit: the guard boundary moves, and the staging is
	// the content under measurement.
	edited := "package staged\n\nfunc F(x int) int {\n\tif x > 99 {\n\t\treturn x - 1\n\t}\n\treturn x\n}\n"
	if err := os.WriteFile(filepath.Join(root, "p.go"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "p.go")
	tree, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	worktree, err := tree.Run(context.Background(), stagedTarget(), Options{Budget: 1, OracleTimeout: 2 * time.Minute})
	if err != nil || len(worktree) != 1 {
		t.Fatalf("worktree measure = %+v, %v", worktree, err)
	}
	if !worktree[0].Dirty || worktree[0].StagedTree != "" {
		t.Fatalf("worktree run = dirty %v stagedTree %q, want dirty with no snapshot identity", worktree[0].Dirty, worktree[0].StagedTree)
	}
	staged, err := tree.Run(context.Background(), stagedTarget(), Options{Budget: 1, OracleTimeout: 2 * time.Minute, Staged: true, Force: true})
	if err != nil || len(staged) != 1 {
		t.Fatalf("staged measure = %+v, %v", staged, err)
	}
	if staged[0].Dirty {
		t.Fatal("staged-but-uncommitted content stamped dirty under staged mode")
	}
	out, gerr := exec.Command("git", "-C", root, "write-tree").Output()
	if gerr != nil {
		t.Fatal(gerr)
	}
	if staged[0].StagedTree == "" || staged[0].StagedTree != strings.TrimSpace(string(out)) {
		t.Fatalf("stagedTree = %q, want the index tree %q", staged[0].StagedTree, strings.TrimSpace(string(out)))
	}
	if staged[0].Commit == "" {
		t.Fatal("staged record lost its commit provenance")
	}
}

// The pre-commit consumer loop the staged mode exists for, end to
// end: a staged run's record routes to the repo findings document
// (clean provenance on the index snapshot - not the machine-local
// overlay), the persisted document carries it, and a second identical
// staged run serves it - persistence plus later-run reuse, the field
// trigger this mode's issue doc named (REQ-result-staged,
// REQ-result-layers).
func TestStagedPreCommitLoopPersistsAndServes(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant, twice")
	}
	root, runGit := stagedFixture(t)
	edited := "package staged\n\nfunc F(x int) int {\n\tif x > 99 {\n\t\treturn x - 1\n\t}\n\treturn x\n}\n"
	if err := os.WriteFile(filepath.Join(root, "p.go"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "p.go")
	tree, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	first, err := tree.Run(context.Background(), stagedTarget(), Options{Budget: 1, OracleTimeout: 2 * time.Minute, Staged: true})
	if err != nil || len(first) != 1 {
		t.Fatalf("staged measure = %+v, %v", first, err)
	}
	store, err := OpenStore(filepath.Join(root, ".gomutant", "findings.json"), root)
	if err != nil {
		t.Fatal(err)
	}
	if layer, reason := store.Layer(first[0]); layer != "repo" {
		t.Fatalf("staged record routed %s (%s), want the repo document", layer, reason)
	}
	if err := store.Update(context.Background(), func([]Finding) ([]Finding, error) { return first, nil }); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, ".gomutant", "findings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "example.com/staged.F") {
		t.Fatalf("repo document does not carry the staged record: %s", raw)
	}
	prior, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := tree.Run(context.Background(), stagedTarget(), Options{Budget: 1, OracleTimeout: 2 * time.Minute, Staged: true, Prior: prior})
	if err != nil || len(second) != 1 {
		t.Fatalf("second staged run = %+v, %v", second, err)
	}
	if !second[0].Cached {
		t.Fatal("unchanged staged tree re-measured: the pre-commit loop has no reuse")
	}
}

// Unstaged drift over the measured package's inputs refuses the target
// with the drift named, and an untracked file in the package refuses
// the same way - the snapshot cannot vouch for either
// (REQ-result-staged).
func TestStagedRunRefusesUnstagedDrift(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	for name, tc := range map[string]struct {
		dirty func(t *testing.T, root string)
		cause string
	}{
		"unstaged edit": {
			dirty: func(t *testing.T, root string) {
				edited := "package staged\n\nfunc F(x int) int {\n\tif x > 98 {\n\t\treturn x - 1\n\t}\n\treturn x\n}\n"
				if err := os.WriteFile(filepath.Join(root, "p.go"), []byte(edited), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			cause: "worktree differs from the index: p.go",
		},
		"untracked file": {
			dirty: func(t *testing.T, root string) {
				if err := os.WriteFile(filepath.Join(root, "extra.go"), []byte("package staged\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			cause: "untracked: extra.go",
		},
	} {
		t.Run(name, func(t *testing.T) {
			root, _ := stagedFixture(t)
			tc.dirty(t, root)
			tree, err := Load(root)
			if err != nil {
				t.Fatal(err)
			}
			_, err = tree.Run(context.Background(), stagedTarget(), Options{Budget: 1, OracleTimeout: 2 * time.Minute, Staged: true})
			var drift *TreeDriftError
			if !errors.As(err, &drift) || len(drift.Drifted) == 0 {
				t.Fatalf("err = %v, want the staged drift refusal", err)
			}
			if !strings.Contains(drift.Drifted[0].Reason, "stage or stash") {
				t.Fatalf("drift reason = %q, want the unstaged-drift refusal", drift.Drifted[0].Reason)
			}
			// The refusal names the differing input and its class: an
			// unnamed refusal on a visually clean tree reads as a tool
			// fault (the drift may be an input plain `git status` never
			// shows).
			if !strings.Contains(drift.Drifted[0].Reason, tc.cause) {
				t.Fatalf("drift reason = %q, want the differing input named as %q", drift.Drifted[0].Reason, tc.cause)
			}
		})
	}
}

// Staged mode without a repository refuses outright: there is no
// snapshot to pin (REQ-result-staged).
func TestStagedRunRefusesWithoutRepository(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/norepo\n\ngo 1.26.4\n",
		"p.go":   "package norepo\n\nfunc F(x int) int { return x }\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tree, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tree.Run(context.Background(), nil, Options{Staged: true})
	if err == nil || !strings.Contains(err.Error(), "staged mode needs a git repository") {
		t.Fatalf("err = %v, want the no-repository refusal", err)
	}
}

// An index re-staged mid-run refuses the affected target: the recorded
// tree no longer names the measured content (REQ-result-staged).
func TestStagedRunRefusesMidRunRestaging(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	root, _ := stagedFixture(t)
	tree, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	restaged := false
	restage := func() {
		if restaged {
			return
		}
		restaged = true
		// A non-source file: the analysis view and the selected paths
		// are untouched, so only the snapshot identity moves.
		if err := os.WriteFile(filepath.Join(root, "NOTES.md"), []byte("mid-run\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("git", "-C", root, "add", "NOTES.md")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git add: %v: %s", err, out)
		}
	}
	_, err = tree.Run(context.Background(), stagedTarget(), Options{
		Budget: 1, OracleTimeout: 2 * time.Minute, Staged: true, Force: true,
		Executing: func(ExecutionEvent) { restage() },
	})
	var drift *TreeDriftError
	if !errors.As(err, &drift) || len(drift.Drifted) == 0 {
		t.Fatalf("err = %v, want the mid-run re-staging refusal", err)
	}
	if !strings.Contains(drift.Drifted[0].Reason, "re-staged mid-run") {
		t.Fatalf("drift reason = %q", drift.Drifted[0].Reason)
	}
}

// An unborn HEAD has no snapshot provenance: staged capture refuses
// (REQ-result-staged).
func TestStagedCaptureRefusesUnbornHead(t *testing.T) {
	root := t.TempDir()
	cmd := exec.Command("git", "-C", root, "init", "-q")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	_, err := captureRepositoryStateContext(context.Background(), root, true)
	if err == nil || !strings.Contains(err.Error(), "commit provenance") {
		t.Fatalf("err = %v, want the unborn-HEAD refusal", err)
	}
}

// An ignored file a measurement consumed is drift the snapshot cannot
// vouch for: the index never carries it (REQ-result-staged).
func TestStagedRunRefusesConsumedIgnoredFile(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	root, runGit := stagedFixture(t)
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("gen.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reader := "package staged\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestReadsGenerated(t *testing.T) {\n\tif _, err := os.ReadFile(\"gen.log\"); err != nil {\n\t\tt.Fatal(err)\n\t}\n\tif F(5) != 5 {\n\t\tt.Fatal()\n\t}\n}\n"
	if err := os.WriteFile(filepath.Join(root, "gen_test.go"), []byte(reader), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "-A")
	runGit("commit", "-q", "-m", "reader")
	if err := os.WriteFile(filepath.Join(root, "gen.log"), []byte("generated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tree, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tree.Run(context.Background(),
		[]Target{{Symbol: "example.com/staged.F", Oracle: []string{"example.com/staged.TestReadsGenerated"}, OracleExplicit: true}},
		Options{Budget: 1, OracleTimeout: 2 * time.Minute, Staged: true})
	var drift *TreeDriftError
	if !errors.As(err, &drift) || len(drift.Drifted) == 0 {
		t.Fatalf("err = %v, want the consumed-ignored-file refusal", err)
	}
}

// A staged record serves unchanged after the staging lands as a
// commit: the measurement pins are content-derived, so the committed
// tree is the measured tree (REQ-result-staged).
func TestStagedRecordServesAfterTheStagingCommits(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	root, runGit := stagedFixture(t)
	edited := "package staged\n\nfunc F(x int) int {\n\tif x > 97 {\n\t\treturn x - 1\n\t}\n\treturn x\n}\n"
	if err := os.WriteFile(filepath.Join(root, "p.go"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "p.go")
	tree, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	first, err := tree.Run(context.Background(), stagedTarget(), Options{Budget: 1, OracleTimeout: 2 * time.Minute, Staged: true})
	if err != nil || len(first) != 1 || first[0].Dirty {
		t.Fatalf("staged measure = %+v, %v", first, err)
	}
	runGit("commit", "-q", "-m", "the staging lands")
	var decisions []string
	_, err = tree.Run(context.Background(), stagedTarget(), Options{
		Budget: 1, OracleTimeout: 2 * time.Minute, Prior: first,
		Decision: func(d RunDecision) { decisions = append(decisions, d.Action) },
	})
	if err != nil {
		t.Fatal(err)
	}
	served := false
	for _, d := range decisions {
		if d == "cached" {
			served = true
		}
	}
	if !served {
		t.Fatalf("staged-born record did not serve after its commit: decisions %v", decisions)
	}
}

// A plan-only staged run surfaces a drift-refused cached target
// exactly as an executing run would - a successful plan silently
// dropping the target would misreport the budget decision
// (REQ-result-staged, REQ-exec-plan-only).
func TestStagedPlanSurfacesDriftRefusedServe(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	root, _ := stagedFixture(t)
	tree, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := tree.Run(context.Background(), stagedTarget(), Options{Budget: 1, OracleTimeout: 2 * time.Minute, Staged: true})
	if err != nil || len(prior) != 1 {
		t.Fatalf("prior = %+v, %v", prior, err)
	}
	// Pin-neutral unstaged drift on a selected path: a go.mod comment
	// moves no measurement pin, so the prior still serves - and the
	// cached serve's re-stamp refuses under the snapshot.
	mod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), append(mod, []byte("// unstaged note\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = tree.Run(context.Background(), stagedTarget(), Options{
		Budget: 1, OracleTimeout: 2 * time.Minute, Staged: true, PlanOnly: true, Prior: prior,
	})
	var drift *TreeDriftError
	if !errors.As(err, &drift) || len(drift.Drifted) == 0 {
		t.Fatalf("plan err = %v, want the drift refusal surfaced", err)
	}
}
