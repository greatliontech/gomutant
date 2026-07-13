package gomutant

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/greatliontech/gofresh/runtimeinput"
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

func (s repositoryState) pathsDirty(selectedPaths []string, runtimeState runtimeinput.State) bool {
	dirty, err := s.pathsDirtyContext(context.Background(), selectedPaths, runtimeState)
	return err != nil || dirty
}

func (s repositoryState) pathsDirtyContext(ctx context.Context, selectedPaths []string, runtimeState runtimeinput.State) (bool, error) {
	if !s.available {
		return true, nil
	}
	paths := append([]string(nil), selectedPaths...)
	if runtimeState.OK {
		runtimePaths, err := runtimeinput.Paths(runtimeState.Manifest, s.root)
		if err != nil {
			return true, nil
		}
		if err := ctx.Err(); err != nil {
			return false, err
		}
		paths = append(paths, runtimePaths...)
	}
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
