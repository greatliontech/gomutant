package gomutant

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/greatliontech/gofresh/runtimeinput"
)

func TestRepositoryContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if state, err := captureRepositoryStateContext(ctx, t.TempDir()); !errors.Is(err, context.Canceled) || state.available {
		t.Fatalf("cancelled capture = %+v, %v", state, err)
	}
	repository := repositoryState{root: t.TempDir(), commit: "commit", available: true}
	if _, err := repository.pathsDirtyContext(ctx, []string{"source.go"}, runtimeinput.State{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled dirty check = %v", err)
	}
	if _, err := repository.historicalPackageFilesContext(ctx, []string{"source.go"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled history check = %v", err)
	}
	if _, err := repository.headMovedContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled head check = %v", err)
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
	if !repository.available || repository.commit == "" {
		t.Fatalf("repository state = %+v", repository)
	}
	if repository.pathsDirty([]string{goMod, source}, runtimeinput.State{}) {
		t.Fatal("clean selected inputs reported dirty")
	}
	if err := os.WriteFile(filepath.Join(root, "unrelated.txt"), []byte("untracked"), 0o644); err != nil {
		t.Fatal(err)
	}
	if repository.pathsDirty([]string{goMod, source}, runtimeinput.State{}) {
		t.Fatal("unrelated untracked file dirtied selected inputs")
	}
	selected := append([]string{goMod, source}, repository.historicalPackageFiles([]string{source})...)
	if err := os.Remove(extraSource); err != nil {
		t.Fatal(err)
	}
	if !repository.pathsDirty(selected, runtimeinput.State{}) {
		t.Fatal("deleted tracked package input reported clean")
	}
	if err := os.WriteFile(extraSource, []byte("package provenance\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("package provenance\n\nvar changed = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !repository.pathsDirty([]string{goMod, source}, runtimeinput.State{}) {
		t.Fatal("modified selected source reported clean")
	}
	if err := os.WriteFile(source, []byte("package provenance\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(root, "input.txt")
	if err := os.WriteFile(input, []byte("runtime"), 0o644); err != nil {
		t.Fatal(err)
	}
	state, err := runtimeinput.FromTestLog([]byte("open input.txt\n"), root, root, runtimeinput.WithCompletedProcess("test"), runtimeinput.WithBracket(testBracket(t, root)))
	if err != nil {
		t.Fatal(err)
	}
	if !repository.pathsDirty([]string{goMod, source}, state.State) {
		t.Fatal("untracked selected runtime input reported clean")
	}
}
