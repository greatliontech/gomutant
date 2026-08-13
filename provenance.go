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
	root, commit string
	available    bool
}

func captureRepositoryState(dir string) repositoryState {
	state, _ := captureRepositoryStateContext(context.Background(), dir)
	return state
}

func captureRepositoryStateContext(ctx context.Context, dir string) (repositoryState, error) {
	root, err := gitOutputContext(ctx, dir, "rev-parse", "--show-toplevel")
	if ctx.Err() != nil {
		return repositoryState{}, ctx.Err()
	}
	if err != nil {
		return repositoryState{}, nil
	}
	commit, err := gitOutputContext(ctx, dir, "rev-parse", "HEAD")
	if ctx.Err() != nil {
		return repositoryState{}, ctx.Err()
	}
	if err != nil {
		return repositoryState{}, nil
	}
	return repositoryState{
		root:      strings.TrimSpace(string(root)),
		commit:    strings.TrimSpace(string(commit)),
		available: true,
	}, nil
}

func (s repositoryState) pathsDirty(selectedPaths []string) bool {
	dirty, err := s.pathsDirtyContext(context.Background(), selectedPaths)
	return err != nil || dirty
}

// pathsDirtyContext judges the caller's already-materialized paths:
// runtime-input paths arrive resolved against their own subject's
// module directory by the provenance stamp - there is no correct
// single base to resolve a manifest against here.
func (s repositoryState) pathsDirtyContext(ctx context.Context, selectedPaths []string) (bool, error) {
	if !s.available {
		return true, nil
	}
	paths := append([]string(nil), selectedPaths...)
	args := []string{"status", "--porcelain", "--untracked-files=all", "--ignored=matching", "--"}
	seen := map[string]bool{}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		rel, err := filepath.Rel(s.root, path)
		if err != nil || !filepath.IsLocal(rel) {
			return true, nil
		}
		if !seen[rel] {
			seen[rel] = true
			args = append(args, rel)
		}
	}
	if len(args) == 5 {
		return false, nil
	}
	status, err := gitOutputContext(ctx, s.root, args...)
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	return err != nil || len(bytes.TrimSpace(status)) > 0, nil
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

func (s repositoryState) headMoved() bool {
	moved, _ := s.headMovedContext(context.Background())
	return moved
}

func (s repositoryState) headMovedContext(ctx context.Context) (bool, error) {
	if !s.available {
		return false, nil
	}
	head, err := gitOutputContext(ctx, s.root, "rev-parse", "HEAD")
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	return err != nil || strings.TrimSpace(string(head)) != s.commit, nil
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
func measurementResidue(ctx context.Context, s repositoryState, since time.Time) string {
	if !s.available {
		return ""
	}
	out, err := gitOutputContext(ctx, s.root, "-c", "core.quotepath=off", "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return ""
	}
	var fresh []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
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
