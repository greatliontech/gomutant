package gomutant

import (
	"bytes"
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
	root, err := gitOutput(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return repositoryState{}
	}
	commit, err := gitOutput(dir, "rev-parse", "HEAD")
	if err != nil {
		return repositoryState{}
	}
	return repositoryState{
		root:      strings.TrimSpace(string(root)),
		commit:    strings.TrimSpace(string(commit)),
		available: true,
	}
}

func (s repositoryState) pathsDirty(selectedPaths []string, runtimeState runtimeinput.State) bool {
	if !s.available {
		return true
	}
	paths := append([]string(nil), selectedPaths...)
	if runtimeState.OK {
		runtimePaths, err := runtimeinput.Paths(runtimeState.Manifest, s.root)
		if err != nil {
			return true
		}
		paths = append(paths, runtimePaths...)
	}
	args := []string{"status", "--porcelain", "--untracked-files=all", "--ignored=matching", "--"}
	seen := map[string]bool{}
	for _, path := range paths {
		rel, err := filepath.Rel(s.root, path)
		if err != nil || !filepath.IsLocal(rel) {
			return true
		}
		if !seen[rel] {
			seen[rel] = true
			args = append(args, rel)
		}
	}
	if len(args) == 5 {
		return false
	}
	status, err := gitOutput(s.root, args...)
	return err != nil || len(bytes.TrimSpace(status)) > 0
}

func (s repositoryState) historicalPackageFiles(sourceFiles []string) []string {
	if !s.available {
		return nil
	}
	dirs := map[string]bool{}
	for _, source := range sourceFiles {
		rel, err := filepath.Rel(s.root, filepath.Dir(source))
		if err == nil && filepath.IsLocal(rel) {
			dirs[rel] = true
		}
	}
	seen := map[string]bool{}
	var paths []string
	for dir := range dirs {
		listed, err := gitOutput(s.root, "ls-tree", "-rz", "--name-only", "HEAD", "--", dir)
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
	return paths
}

func (s repositoryState) headMoved() bool {
	if !s.available {
		return false
	}
	head, err := gitOutput(s.root, "rev-parse", "HEAD")
	return err != nil || strings.TrimSpace(string(head)) != s.commit
}

func gitOutput(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	return cmd.Output()
}
