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

	"github.com/greatliontech/gofresh/runtimeinput"
)

func TestRepositoryContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if state, err := captureRepositoryStateContext(ctx, t.TempDir(), false); !errors.Is(err, context.Canceled) || state.available {
		t.Fatalf("cancelled capture = %+v, %v", state, err)
	}
	repository := repositoryState{root: t.TempDir(), available: true}
	if _, _, err := repository.pathsDirtyContext(ctx, []string{"source.go"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled dirty check = %v", err)
	}
	if _, err := repository.historicalPackageFilesContext(ctx, []string{"source.go"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled history check = %v", err)
	}
	if _, err := repository.currentCommitContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled commit read = %v", err)
	}
}

func TestModuleSelectionPathsIncludeNestedModuleMetadata(t *testing.T) {
	root := t.TempDir()
	module := filepath.Join(root, "nested")
	if err := os.Mkdir(module, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{
		"go.mod":    "module example.com/nested\n",
		"source.go": "package nested\n",
	} {
		if err := os.WriteFile(filepath.Join(module, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	paths := withModuleSelectionPaths([]string{filepath.Join(module, "source.go")})
	want := map[string]bool{
		filepath.Join(module, "go.mod"):                false,
		filepath.Join(module, "go.sum"):                false,
		filepath.Join(module, "vendor", "modules.txt"): false,
	}
	for _, path := range paths {
		if _, ok := want[path]; ok {
			want[path] = true
		}
	}
	for path, found := range want {
		if !found {
			t.Fatalf("module selection paths = %v, missing %s", paths, path)
		}
	}
}

func TestRepositoryStateTracksOnlySelectedInputs(t *testing.T) {
	root := t.TempDir()
	goMod := filepath.Join(root, "go.mod")
	source := filepath.Join(root, "source.go")
	extraSource := filepath.Join(root, "extra.go")
	if err := os.WriteFile(goMod, []byte("module example.com/provenance\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("package provenance\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(extraSource, []byte("package provenance\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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
	runGit("init", "-q")
	runGit("add", "go.mod", "source.go", "extra.go")
	runGit("commit", "-q", "-m", "fixture")

	repository := captureRepositoryState(root)
	if !repository.available {
		t.Fatalf("repository state = %+v", repository)
	}
	if commit, err := repository.currentCommitContext(context.Background()); err != nil || commit == "" {
		t.Fatalf("stamp-time commit = %q, %v", commit, err)
	}
	if repository.pathsDirty([]string{goMod, source}) {
		t.Fatal("clean selected inputs reported dirty")
	}
	if err := os.WriteFile(filepath.Join(root, "unrelated.txt"), []byte("untracked"), 0o644); err != nil {
		t.Fatal(err)
	}
	if repository.pathsDirty([]string{goMod, source}) {
		t.Fatal("unrelated untracked file dirtied selected inputs")
	}
	selected := append([]string{goMod, source}, repository.historicalPackageFiles([]string{source})...)
	if err := os.Remove(extraSource); err != nil {
		t.Fatal(err)
	}
	if !repository.pathsDirty(selected) {
		t.Fatal("deleted tracked package input reported clean")
	}
	if err := os.WriteFile(extraSource, []byte("package provenance\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("package provenance\n\nvar changed = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if dirty, causes, err := repository.pathsDirtyContext(context.Background(), []string{goMod, source}); err != nil || !dirty ||
		len(causes) != 1 || !strings.Contains(causes[0], "worktree differs from the index") || !strings.Contains(causes[0], "source.go") {
		t.Fatalf("modified selected source: dirty=%v causes=%v err=%v, want the divergence named", dirty, causes, err)
	}
	// Staged-but-uncommitted is still git-visible drift for a WORKTREE
	// run: the measured bytes are in no commit, so the judgment stays
	// dirty with the index-only class named — only a STAGED run's
	// snapshot vouches for the index.
	runGit("add", "source.go")
	if dirty, causes, err := repository.pathsDirtyContext(context.Background(), []string{goMod, source}); err != nil || !dirty ||
		len(causes) != 1 || !strings.Contains(causes[0], "index differs from HEAD") || !strings.Contains(causes[0], "source.go") {
		t.Fatalf("staged-but-uncommitted source: dirty=%v causes=%v err=%v, want the index-only class named", dirty, causes, err)
	}
	if err := os.WriteFile(source, []byte("package provenance\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "source.go")
	input := filepath.Join(root, "input.txt")
	if err := os.WriteFile(input, []byte("runtime"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Runtime-input paths arrive already materialized against their own
	// subject's module - the provenance stamp resolves manifests, this
	// walk only judges paths.
	if dirty, causes, err := repository.pathsDirtyContext(context.Background(), []string{goMod, source, input}); err != nil || !dirty ||
		len(causes) != 1 || !strings.Contains(causes[0], "untracked") || !strings.Contains(causes[0], "input.txt") {
		t.Fatalf("untracked selected runtime input: dirty=%v causes=%v err=%v, want the untracked class named", dirty, causes, err)
	}
	// A measured input outside the repository names itself.
	if dirty, causes, _ := repository.pathsDirtyContext(context.Background(), []string{filepath.Join(os.TempDir(), "elsewhere.txt")}); !dirty ||
		len(causes) != 1 || !strings.Contains(causes[0], "outside the repository") {
		t.Fatalf("external input: dirty=%v causes=%v, want the outside-the-repository cause", dirty, causes)
	}
}

// TestStampServedProvenanceCoversEvidenceRuntimeInputs: the served
// re-stamp derives runtime-input paths from the record's own subject
// evidence — a dirty manifest path keeps the record machine-local even
// when every source file is clean — and an unreadable manifest stamps
// dirty, fail-closed (REQ-result-stale, REQ-result-layers).
func TestStampServedProvenanceCoversEvidenceRuntimeInputs(t *testing.T) {
	root := t.TempDir()
	// The module sits BELOW the repository root, the package in a
	// subdirectory of the module, and the runtime input outside the
	// package: manifest identities are module-relative, so resolving
	// them against the git toplevel instead of the subject's module
	// directory materializes a path that does not exist — and the
	// historical package-file sweep must not cover the input either, or
	// the evidence-derived paths are unobservable and the case is
	// vacuous.
	moduleDir := filepath.Join(root, "m")
	goMod := filepath.Join(moduleDir, "go.mod")
	source := filepath.Join(moduleDir, "pkg", "source.go")
	input := filepath.Join(moduleDir, "data", "input.txt")
	for path, content := range map[string]string{
		goMod:  "module example.com/provenance\n\ngo 1.26\n",
		source: "package provenance\n",
		input:  "runtime\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
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
	runGit("init", "-q")
	runGit("add", "-A")
	runGit("commit", "-q", "-m", "fixture")
	repository := captureRepositoryState(root)
	if !repository.available {
		t.Fatalf("repository state = %+v", repository)
	}
	if commit, err := repository.currentCommitContext(context.Background()); err != nil || commit == "" {
		t.Fatalf("stamp-time commit = %q, %v", commit, err)
	}
	observed, err := runtimeinput.FromTestLog([]byte("open data/input.txt\n"), moduleDir, moduleDir, runtimeinput.WithCompletedProcess("test"), runtimeinput.WithBracket(testBracket(t, moduleDir)))
	if err != nil {
		t.Fatal(err)
	}
	tree := &Tree{dir: root}
	const symbol = "example.com/provenance.F"
	view := &subjectView{symbol: symbol, moduleDir: moduleDir, sourceFiles: []string{source}}
	ctx := context.Background()

	clean := Finding{Commit: "stale", Dirty: true, TargetEvidence: SubjectEvidence{Symbol: symbol, RuntimeInputs: observed.State.Manifest, RuntimeDigest: observed.State.Digest}}
	if _, err := tree.stampProvenance(ctx, repository, view, nil, nil, &clean); err != nil {
		t.Fatal(err)
	}
	head, err := repository.currentCommitContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if clean.Dirty || clean.Commit != head {
		t.Fatalf("clean re-stamp = commit %q dirty %v, want the current HEAD and clean", clean.Commit, clean.Dirty)
	}

	if err := os.WriteFile(input, []byte("runtime moved\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirtied := Finding{TargetEvidence: SubjectEvidence{Symbol: symbol, RuntimeInputs: observed.State.Manifest, RuntimeDigest: observed.State.Digest}}
	if _, err := tree.stampProvenance(ctx, repository, view, nil, nil, &dirtied); err != nil {
		t.Fatal(err)
	}
	if !dirtied.Dirty {
		t.Fatal("a moved evidence runtime input re-stamped clean - the served stamp ignored the record's manifest paths or resolved them against the wrong base")
	}

	unreadable := Finding{TargetEvidence: SubjectEvidence{Symbol: symbol, RuntimeInputs: "not-a-manifest"}}
	if _, err := tree.stampProvenance(ctx, repository, view, nil, nil, &unreadable); err != nil {
		t.Fatal(err)
	}
	if !unreadable.Dirty {
		t.Fatal("an unreadable evidence manifest re-stamped clean, want fail-closed dirty")
	}
	// A STAGED run reaching the same terminal arm refuses instead of
	// persisting a dirty record — staged records never persist dirty
	// (REQ-result-staged) — with the evidence fault named.
	stagedRepo := repository
	stagedRepo.staged = true
	unreadableStaged := Finding{TargetEvidence: SubjectEvidence{Symbol: symbol, RuntimeInputs: "not-a-manifest"}}
	if reason, err := tree.stampProvenance(ctx, stagedRepo, view, nil, nil, &unreadableStaged); err != nil ||
		!strings.Contains(reason, "unreadable") || !strings.Contains(reason, symbol) {
		t.Fatalf("staged unreadable manifest: reason=%q err=%v, want the staged refusal naming the fault", reason, err)
	}

	unknown := Finding{TargetEvidence: SubjectEvidence{Symbol: "example.com/provenance.Ghost", RuntimeInputs: observed.State.Manifest}}
	if _, err := tree.stampProvenance(ctx, repository, view, nil, nil, &unknown); err != nil {
		t.Fatal(err)
	}
	if !unknown.Dirty {
		t.Fatal("evidence naming a subject with no view re-stamped clean, want fail-closed dirty")
	}

}

// TestStampJudgesAliasFormIdentitiesByPhysicalPath: the runtime-input
// recorder keeps the literal path a run opened, so an in-repo input
// reached through a symlinked tree path records as an absolute identity
// outside the repository's physical root. The stamp judges the physical
// form git sees - alias-form drift stamps dirty, a clean alias-form
// input stamps clean - and an identity whose physical location cannot
// be established counts as in-repo, fail-closed, never silently
// external (REQ-result-layers).
func TestStampJudgesAliasFormIdentitiesByPhysicalPath(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "real")
	moduleDir := filepath.Join(root, "m")
	goMod := filepath.Join(moduleDir, "go.mod")
	source := filepath.Join(moduleDir, "pkg", "source.go")
	input := filepath.Join(moduleDir, "data", "input.txt")
	for path, content := range map[string]string{
		goMod:  "module example.com/provenance\n\ngo 1.26\n",
		source: "package provenance\n",
		input:  "runtime\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
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
	runGit("init", "-q")
	runGit("add", "-A")
	runGit("commit", "-q", "-m", "fixture")
	repository := captureRepositoryState(root)
	if !repository.available {
		t.Fatalf("repository state = %+v", repository)
	}
	if commit, err := repository.currentCommitContext(context.Background()); err != nil || commit == "" {
		t.Fatalf("stamp-time commit = %q, %v", commit, err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	aliasInput := filepath.Join(alias, "m", "data", "input.txt")
	observed, err := runtimeinput.FromTestLog([]byte("open "+aliasInput+"\n"), moduleDir, moduleDir, runtimeinput.WithCompletedProcess("test"), runtimeinput.WithBracket(testBracket(t, moduleDir)))
	if err != nil {
		t.Fatal(err)
	}
	tree := &Tree{dir: root}
	const symbol = "example.com/provenance.F"
	view := &subjectView{symbol: symbol, moduleDir: moduleDir, sourceFiles: []string{source}}
	ctx := context.Background()

	clean := Finding{TargetEvidence: SubjectEvidence{Symbol: symbol, RuntimeInputs: observed.State.Manifest, RuntimeDigest: observed.State.Digest}}
	if _, err := tree.stampProvenance(ctx, repository, view, nil, nil, &clean); err != nil {
		t.Fatal(err)
	}
	if clean.Dirty {
		t.Fatal("alias-form identity of a clean tracked input stamped dirty")
	}

	// Drift on an INTERMEDIATE tracked component: a tracked in-module
	// symlink the recorded path traverses is retargeted to dangle
	// (uncommitted). The reconstructed pathspec must anchor at the
	// first unresolved component - git never matches an index entry
	// shallower than a pathspec, so a leaf pathspec would miss the
	// symlink's own drift and stamp falsely clean.
	link := filepath.Join(moduleDir, "link")
	if err := os.Symlink("data", link); err != nil {
		t.Fatal(err)
	}
	runGit("add", "-A")
	runGit("commit", "-q", "-m", "tracked link")
	repository = captureRepositoryState(root)
	aliasLinkInput := filepath.Join(alias, "m", "link", "input.txt")
	linkObserved, err := runtimeinput.FromTestLog([]byte("open "+aliasLinkInput+"\n"), moduleDir, moduleDir, runtimeinput.WithCompletedProcess("test"), runtimeinput.WithBracket(testBracket(t, moduleDir)))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing", link); err != nil {
		t.Fatal(err)
	}
	linked := Finding{TargetEvidence: SubjectEvidence{Symbol: symbol,
		RuntimeInputs: linkObserved.State.Manifest, RuntimeDigest: linkObserved.State.Digest,
		RuntimeUnverifiable: linkObserved.State.Unverifiable, RuntimeReason: linkObserved.State.Reason}}
	if _, err := tree.stampProvenance(ctx, repository, view, nil, nil, &linked); err != nil {
		t.Fatal(err)
	}
	if !linked.Dirty {
		t.Fatal("a retargeted tracked intermediate symlink stamped clean - the pathspec must anchor at the first unresolved component")
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	// Restoring the link returns the tree to the committed state - no
	// new commit needed; later legs run against the same HEAD.
	if err := os.Symlink("data", link); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(input, []byte("runtime moved\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirtied := Finding{TargetEvidence: SubjectEvidence{Symbol: symbol, RuntimeInputs: observed.State.Manifest, RuntimeDigest: observed.State.Digest}}
	if _, err := tree.stampProvenance(ctx, repository, view, nil, nil, &dirtied); err != nil {
		t.Fatal(err)
	}
	if !dirtied.Dirty {
		t.Fatal("alias-form in-repo drift escaped the dirty stamp - the identity was judged silently external")
	}

	// A tracked file deleted before measurement records missing through
	// the live alias: the digest is stable (missing then, missing now)
	// but git reports the uncommitted deletion - the deepest resolvable
	// ancestor lies in-repo, the path reconstructs, and the dirty walk
	// sees it. A digest vouch alone would falsely stamp clean.
	if err := os.Remove(input); err != nil {
		t.Fatal(err)
	}
	deletedObserved, err := runtimeinput.FromTestLog([]byte("open "+aliasInput+"\n"), moduleDir, moduleDir, runtimeinput.WithCompletedProcess("test"), runtimeinput.WithBracket(testBracket(t, moduleDir)))
	if err != nil {
		t.Fatal(err)
	}
	deleted := Finding{TargetEvidence: SubjectEvidence{Symbol: symbol,
		RuntimeInputs: deletedObserved.State.Manifest, RuntimeDigest: deletedObserved.State.Digest,
		RuntimeUnverifiable: deletedObserved.State.Unverifiable, RuntimeReason: deletedObserved.State.Reason}}
	if _, err := tree.stampProvenance(ctx, repository, view, nil, nil, &deleted); err != nil {
		t.Fatal(err)
	}
	if !deleted.Dirty {
		t.Fatal("a tracked deletion behind a live alias stamped clean - the in-repo ancestor must reconstruct the path for the dirty walk")
	}
	if err := os.WriteFile(input, []byte("runtime moved\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// With the alias gone the identity's physical location is
	// unknowable: it counts as in-repo and the non-local walk arm
	// stamps dirty, fail-closed.
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	unresolvable := Finding{TargetEvidence: SubjectEvidence{Symbol: symbol, RuntimeInputs: observed.State.Manifest, RuntimeDigest: observed.State.Digest}}
	if _, err := tree.stampProvenance(ctx, repository, view, nil, nil, &unresolvable); err != nil {
		t.Fatal(err)
	}
	if !unresolvable.Dirty {
		t.Fatal("an unresolvable identity stamped clean, want fail-closed dirty")
	}
}

// A drift refusal whose residue is an untracked file written after the
// run began names the measurement provenance - a mutant or oracle
// process wrote into the tree - while pre-existing untracked files and
// clean trees add nothing (REQ-exec-quiescence's residue sentence).
func TestMeasurementResidueNamesFreshUntrackedFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "source.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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
	runGit("init", "-q")
	runGit("add", "source.go")
	runGit("commit", "-q", "-m", "fixture")
	// A pre-existing untracked file: present before the run began.
	stale := filepath.Join(root, "pre-existing.txt")
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	repository := captureRepositoryState(root)
	if !repository.available {
		t.Fatalf("repository state = %+v", repository)
	}
	since := time.Now().Add(-time.Minute)
	if residue := measurementResidue(context.Background(), repository, since, nil); residue != "" {
		t.Fatalf("pre-existing untracked file named as residue: %q", residue)
	}
	if err := os.WriteFile(filepath.Join(root, "written-under-measurement.db"), []byte("residue"), 0o644); err != nil {
		t.Fatal(err)
	}
	residue := measurementResidue(context.Background(), repository, since, nil)
	if !strings.Contains(residue, "written-under-measurement.db") || !strings.Contains(residue, "can write into the tree") {
		t.Fatalf("residue = %q, want the fresh untracked file named with its provenance", residue)
	}
	// The caller's declared own writes are the harness's, never residue:
	// with the fresh file declared, nothing remains to attribute.
	if residue := measurementResidue(context.Background(), repository, since, []string{filepath.Join(root, "written-under-measurement.db")}); residue != "" {
		t.Fatalf("declared own write attributed as residue: %q", residue)
	}
	// Unavailable repository state degrades to the bare reason.
	if residue := measurementResidue(context.Background(), repositoryState{}, since, nil); residue != "" {
		t.Fatalf("unavailable repository produced residue: %q", residue)
	}
}

// Index entries flagged skip-worktree or assume-unchanged opt out of
// git's own change tracking, so the porcelain judges them false-clean
// while the measurement read divergent bytes; either flag on a
// selected path makes the clean judgment unsupported and stamps dirty
// (REQ-result-staged sharpens the stake: the staged clean stamp
// asserts equality with a named tree).
func TestPathsDirtyDetectsTrackingOptOutFlags(t *testing.T) {
	root, runGit := stagedFixture(t)
	path := filepath.Join(root, "p.go")
	state, err := captureRepositoryStateContext(context.Background(), root, false)
	if err != nil || !state.available {
		t.Fatalf("repository state = %+v, %v", state, err)
	}
	if dirty := state.pathsDirty([]string{path}); dirty {
		t.Fatal("clean unflagged tree judged dirty")
	}
	// The probe is scoped to the selected paths: a tracking-opt-out
	// flag hiding divergence on an unselected sibling never dirties
	// this target's judgment.
	runGit("update-index", "--skip-worktree", "p_test.go")
	if err := os.WriteFile(filepath.Join(root, "p_test.go"), []byte("package staged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if dirty := state.pathsDirty([]string{path}); dirty {
		t.Fatal("unselected flagged sibling dirtied a scoped judgment")
	}
	runGit("update-index", "--no-skip-worktree", "p_test.go")
	runGit("checkout", "--", "p_test.go")
	runGit("update-index", "--skip-worktree", "p.go")
	if err := os.WriteFile(path, []byte("package staged\n\nfunc F(x int) int { return x + 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if dirty := state.pathsDirty([]string{path}); !dirty {
		t.Fatal("skip-worktree divergence judged clean")
	}
	runGit("update-index", "--no-skip-worktree", "p.go")
	runGit("update-index", "--assume-unchanged", "p.go")
	if dirty := state.pathsDirty([]string{path}); !dirty {
		t.Fatal("assume-unchanged divergence judged clean")
	}
	// Staged mode shares the probe: the flag makes the staged clean
	// stamp's equality assertion with the recorded index tree
	// unsupported, so the flagged path judges dirty there too.
	stagedState, err := captureRepositoryStateContext(context.Background(), root, true)
	if err != nil || !stagedState.available {
		t.Fatalf("staged repository state = %+v, %v", stagedState, err)
	}
	if dirty := stagedState.pathsDirty([]string{path}); !dirty {
		t.Fatal("staged mode judged a tracking-opt-out flag clean")
	}
}

// pathsDirty is the tests' verdict-only view of pathsDirtyContext.
func (s repositoryState) pathsDirty(selectedPaths []string) bool {
	dirty, _, err := s.pathsDirtyContext(context.Background(), selectedPaths)
	return err != nil || dirty
}

// cappedJoin caps long cause lists counted, never silently truncated.
func TestCappedJoinCountsRemainder(t *testing.T) {
	items := []string{"a", "b", "c", "d"}
	if got := cappedJoin(items, 8); got != "a; b; c; d" {
		t.Fatalf("under-cap join = %q", got)
	}
	if got := cappedJoin(items, 2); got != "a; b; and 2 more" {
		t.Fatalf("capped join = %q, want the remainder counted", got)
	}
}
