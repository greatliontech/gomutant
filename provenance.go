package gomutant

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func withModuleSelectionPaths(sourceFiles []string) []string {
	paths := append([]string(nil), sourceFiles...)
	seen := map[string]bool{}
	for _, source := range sourceFiles {
		dir := filepath.Dir(source)
		for {
			mod := filepath.Join(dir, "go.mod")
			if _, err := os.Stat(mod); err == nil {
				if !seen[mod] {
					seen[mod] = true
					paths = append(paths, mod, filepath.Join(dir, "go.sum"), filepath.Join(dir, "vendor", "modules.txt"))
				}
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return paths
}

type repositoryState struct {
	root      string
	available bool
	// staged marks the run as measuring the index snapshot
	// (REQ-result-staged): the dirty judgment narrows to drift the
	// snapshot cannot vouch for - worktree content diverging from the
	// index, untracked files, ignored files - while staged-but-
	// uncommitted changes are exactly the measured content and count
	// clean. stagedTree is the index's own tree identity
	// (`git write-tree`): the tree the eventual commit will carry when
	// the staging lands as reviewed.
	staged     bool
	stagedTree string
}

func captureRepositoryState(dir string) repositoryState {
	state, _ := captureRepositoryStateContext(context.Background(), dir, false)
	return state
}

func captureRepositoryStateContext(ctx context.Context, dir string, staged bool) (repositoryState, error) {
	root, err := gitOutputContext(ctx, dir, "rev-parse", "--show-toplevel")
	if ctx.Err() != nil {
		return repositoryState{}, ctx.Err()
	}
	if err != nil {
		if staged {
			return repositoryState{}, fmt.Errorf("gomutant: staged mode needs a git repository: %v", err)
		}
		return repositoryState{}, nil
	}
	// The rev-parse is an availability probe: capture commits are read
	// at stamp time (currentCommitContext), never served from a
	// run-start snapshot, so only the probe's success is kept.
	if _, err := gitOutputContext(ctx, dir, "rev-parse", "HEAD"); err != nil {
		if ctx.Err() != nil {
			return repositoryState{}, ctx.Err()
		}
		if staged {
			return repositoryState{}, fmt.Errorf("gomutant: staged mode needs commit provenance: %v", err)
		}
		return repositoryState{}, nil
	}
	if err := ctx.Err(); err != nil {
		return repositoryState{}, err
	}
	state := repositoryState{
		root:      strings.TrimSpace(string(root)),
		available: true,
	}
	if staged {
		// write-tree refuses an unmerged index - exactly the states a
		// snapshot run cannot pin - and materializes the tree identity
		// the eventual commit will carry.
		tree, err := gitOutputContext(ctx, state.root, "write-tree")
		if ctx.Err() != nil {
			return repositoryState{}, ctx.Err()
		}
		if err != nil {
			return repositoryState{}, fmt.Errorf("gomutant: staged mode cannot pin the index snapshot: %v", err)
		}
		state.staged = true
		state.stagedTree = strings.TrimSpace(string(tree))
	}
	return state, nil
}

// pathsDirtyContext judges the caller's already-materialized paths:
// runtime-input paths arrive resolved against their own subject's
// module directory by the provenance stamp - there is no correct
// single base to resolve a manifest against here. A dirty judgment
// names its evidence: every arm that answers true reports the paths
// (and their divergence class) that decided it, so a refusal built on
// the judgment can name what differs instead of asserting bare drift.
func (s repositoryState) pathsDirtyContext(ctx context.Context, selectedPaths []string) (bool, []string, error) {
	if !s.available {
		return true, []string{"no repository state available for the dirty judgment"}, nil
	}
	seen := map[string]bool{}
	var pathspec []string
	for _, path := range selectedPaths {
		if err := ctx.Err(); err != nil {
			return false, nil, err
		}
		rel, err := filepath.Rel(s.root, path)
		if err != nil || !filepath.IsLocal(rel) {
			return true, []string{"measured input outside the repository: " + path}, nil
		}
		if !seen[rel] {
			seen[rel] = true
			pathspec = append(pathspec, rel)
		}
	}
	if len(pathspec) == 0 {
		return false, nil, nil
	}
	status, err := gitOutputContext(ctx, s.root, append([]string{"-c", "core.quotepath=off", "status", "--porcelain", "--untracked-files=all", "--ignored=matching", "--"}, pathspec...)...)
	if ctx.Err() != nil {
		return false, nil, ctx.Err()
	}
	if err != nil {
		return true, []string{"git status unavailable over the measured inputs"}, nil
	}
	// The porcelain omits index entries flagged skip-worktree or
	// assume-unchanged - an operator opt-out of git's own change
	// tracking - so a divergent worktree file under either flag would
	// judge false-clean; a selected path carrying a flag makes the
	// clean judgment unsupported and stamps dirty (REQ-result-staged
	// sharpens the stake: the staged clean stamp asserts equality with
	// a named tree). ls-files -v tags: uppercase S is skip-worktree,
	// any lowercase tag is assume-unchanged.
	flagged, err := gitOutputContext(ctx, s.root, append([]string{"ls-files", "-v", "--"}, pathspec...)...)
	if ctx.Err() != nil {
		return false, nil, ctx.Err()
	}
	if err != nil {
		return true, []string{"git ls-files unavailable over the measured inputs"}, nil
	}
	var causes []string
	for _, line := range bytes.Split(flagged, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		if tag := line[0]; tag == 'S' || (tag >= 'a' && tag <= 'z') {
			causes = append(causes, "change tracking opted out (skip-worktree or assume-unchanged): "+string(bytes.TrimSpace(line[1:])))
		}
	}
	if len(causes) != 0 {
		return true, causes, nil
	}
	// statusCause classifies one porcelain line; the second return is
	// false exactly for index-only lines (the X column alone), the one
	// class the staged snapshot itself vouches for — both arms below
	// share this single classification. A rename pair names the
	// worktree-side file (the drifting one), not the arrow pair.
	statusCause := func(line []byte) (string, bool) {
		x, y := line[0], line[1]
		name := string(bytes.TrimSpace(line[2:]))
		if _, dest, renamed := strings.Cut(name, " -> "); renamed {
			name = dest
		}
		switch {
		case x == '?':
			return "untracked: " + name, true
		case x == '!':
			return "ignored: " + name, true
		case y != ' ':
			return "worktree differs from the index: " + name, true
		default:
			return "index differs from HEAD: " + name, false
		}
	}
	// The snapshot vouches for staged content: only worktree-vs-index
	// divergence (the Y column), untracked files, and ignored files are
	// drift the index cannot cover; an index-vs-HEAD change (the X
	// column alone) IS the measured snapshot (REQ-result-staged). A
	// non-staged judgment counts every line, index-only included.
	for _, line := range bytes.Split(status, []byte("\n")) {
		if len(line) < 3 {
			continue
		}
		cause, beyondIndex := statusCause(line)
		if beyondIndex || !s.staged {
			causes = append(causes, cause)
		}
	}
	return len(causes) != 0, causes, nil
}

// snapshotMovedContext reports whether the index snapshot a staged run
// pinned at start still stands: a re-staging mid-run means the
// measured content and the recorded tree identity have diverged
// (REQ-result-staged). A non-staged state never reports movement here.
func (s repositoryState) snapshotMovedContext(ctx context.Context) (bool, error) {
	if !s.staged {
		return false, nil
	}
	tree, err := gitOutputContext(ctx, s.root, "write-tree")
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	return err != nil || strings.TrimSpace(string(tree)) != s.stagedTree, nil
}

func (s repositoryState) historicalPackageFiles(sourceFiles []string) []string {
	paths, _ := s.historicalPackageFilesContext(context.Background(), sourceFiles)
	return paths
}

func (s repositoryState) historicalPackageFilesContext(ctx context.Context, sourceFiles []string) ([]string, error) {
	if !s.available {
		return nil, nil
	}
	dirs := map[string]bool{}
	for _, source := range sourceFiles {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(s.root, filepath.Dir(source))
		if err == nil && filepath.IsLocal(rel) {
			dirs[rel] = true
		}
	}
	seen := map[string]bool{}
	var paths []string
	for dir := range dirs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		listed, err := gitOutputContext(ctx, s.root, "ls-tree", "-rz", "--name-only", "HEAD", "--", dir)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			continue
		}
		for _, raw := range bytes.Split(listed, []byte{0}) {
			if len(raw) == 0 {
				continue
			}
			rel := filepath.FromSlash(string(raw))
			if filepath.Dir(rel) != dir {
				continue
			}
			path := filepath.Join(s.root, rel)
			if !seen[path] {
				seen[path] = true
				paths = append(paths, path)
			}
		}
	}
	return paths, nil
}

// currentCommitContext reads the commit HEAD names now — the capture
// commit a finding stamps is the repository state its just-validated
// evidence is true of, so it is read at stamp time, never served from
// the run-start snapshot: ref motion between measurements changes later
// stamps and discards nothing (REQ-exec-quiescence). An unavailable
// repository stamps no commit, exactly as at capture; a mid-run git
// failure surfaces as an error the stamp resolves to the same
// no-commit-provenance posture (Commit empty, Dirty true), fail-safe
// and target-local, never a campaign abort.
func (s repositoryState) currentCommitContext(ctx context.Context) (string, error) {
	if !s.available {
		return "", nil
	}
	head, err := gitOutputContext(ctx, s.root, "rev-parse", "HEAD")
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", fmt.Errorf("gomutant: commit provenance unavailable at stamp time: %v", err)
	}
	return strings.TrimSpace(string(head)), nil
}

func gitOutput(dir string, args ...string) ([]byte, error) {
	return gitOutputContext(context.Background(), dir, args...)
}

func gitOutputContext(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	return cmd.Output()
}

// measurementResidue names untracked files that appeared in the
// repository after the run began. A mutant of filesystem-writing code
// (or its oracle) can create files inside the tree during measurement;
// the resulting drift refusal is then the run's own residue, not
// external-actor drift, and the decision line says so instead of
// reading as operator error (REQ-exec-quiescence's residue sentence).
// Empty when nothing fresh is untracked, when the repository is
// unavailable, or when the listing fails - the generic drift reason
// then stands alone, never blocked by this diagnostic.
func measurementResidue(ctx context.Context, s repositoryState, since time.Time, ownWrites []string) string {
	if !s.available {
		return ""
	}
	out, err := gitOutputContext(ctx, s.root, "-c", "core.quotepath=off", "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return ""
	}
	// The caller's declared run artifacts (its findings document, the
	// locks) are the run's own writes, never a mutant's or oracle's —
	// attributing them as measurement residue would make every
	// default-layout run read as self-inflicted drift. Both the literal
	// absolute form and the physical form are keyed: s.root is git's
	// physical path while a caller's path may ride a symlinked worktree
	// alias, and an alias-blind match would re-attribute the findings
	// document (the store's own Abs+EvalSymlinks discipline).
	own := make(map[string]bool, 2*len(ownWrites))
	for _, w := range ownWrites {
		abs, aerr := filepath.Abs(w)
		if aerr != nil {
			continue
		}
		own[filepath.Clean(abs)] = true
		if resolved, rerr := filepath.EvalSymlinks(abs); rerr == nil {
			own[filepath.Clean(resolved)] = true
		}
		if dir, derr := filepath.EvalSymlinks(filepath.Dir(abs)); derr == nil {
			// Unconditional: covers a not-yet-written first-run doc, a
			// broken link, and a findings doc that is itself a symlink
			// (git lists the physical link path, which full resolution
			// would step through).
			own[filepath.Join(dir, filepath.Base(abs))] = true
		}
	}
	var fresh []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" || own[filepath.Clean(filepath.Join(s.root, line))] {
			continue
		}
		info, statErr := os.Lstat(filepath.Join(s.root, line))
		if statErr != nil || info.ModTime().Before(since) {
			continue
		}
		fresh = append(fresh, line)
	}
	if len(fresh) == 0 {
		return ""
	}
	sort.Strings(fresh)
	residue := fresh[0]
	if len(fresh) > 1 {
		residue = fmt.Sprintf("%s (and %d more)", fresh[0], len(fresh)-1)
	}
	return fmt.Sprintf("; untracked %s appeared during measurement - a mutant or oracle process can write into the tree, and the refused target re-measures once the residue is removed", residue)
}
