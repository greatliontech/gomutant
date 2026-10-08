package gitcmd

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// A failing git carries its stderr into the error and names the
// command; a cancellation is answered as the context's own error, the
// group swept (REQ-exec-go-command-runner).
func TestOutputCarriesStderrAndNamesACancellation(t *testing.T) {
	dir := t.TempDir()
	_, err := Output(context.Background(), dir, "rev-parse", "--show-toplevel")
	if err == nil || !strings.Contains(err.Error(), "git rev-parse --show-toplevel:") || !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("a failing git = %v; want the command named with git's stderr", err)
	}
	cmd, err := Prepare(context.Background(), dir, []string{"PATH=" + os.Getenv("PATH"), "X=1"}, "status")
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Dir != dir || cmd.Args[0] != "git" || !strings.Contains(strings.Join(cmd.Env, "\n"), "X=1") {
		t.Fatalf("Prepare = dir %q args %v env %d entries; want the policy's command over the given environment", cmd.Dir, cmd.Args, len(cmd.Env))
	}
	var exit *exec.ExitError
	if _, err := Output(context.Background(), dir, "nosuchverb"); err == nil || !errors.As(err, &exit) {
		t.Fatalf("an unknown verb = %v; want the exit error carried", err)
	}
	// The shim as git — the last arm: every spawn after it meets the shim.
	if runtimeShim() == "" {
		t.Skip("spawns a shell shim as git")
	}
	shim := t.TempDir()
	if err := os.WriteFile(filepath.Join(shim, "git"), []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = Output(ctx, dir, "status")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a cancelled git = %v; want the context's own error", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("the cancelled git's group was not swept")
	}
}

// Hermetic closes every route from the host's git: the configuration
// scopes, the command-scope entries in every position and spelling,
// the config-file and template redirections, and the repository
// redirection — the rest of the environment kept.
func TestHermeticClosesTheHostsRoutes(t *testing.T) {
	env := []string{"PATH=/bin", "GIT_DIR=/elsewhere/.git", "GIT_CONFIG_GLOBAL=/host/gitconfig", "GIT_CONFIG=/host/config", "GIT_TEMPLATE_DIR=/host/templates", "GIT_CONFIG_COUNT=2", "GIT_CONFIG_KEY_0=core.hooksPath", "GIT_CONFIG_VALUE_0=/hooks", "GIT_CONFIG_KEY_1=core.fsmonitor", "GIT_CONFIG_VALUE_1=true", "GIT_CONFIG_PARAMETERS='core.hooksPath=/hooks'", "GIT_WORK_TREE=/elsewhere", "GIT_INDEX_FILE=/elsewhere/index", "GIT_OBJECT_DIRECTORY=/o", "GIT_ALTERNATE_OBJECT_DIRECTORIES=/a", "GIT_COMMON_DIR=/c", "GIT_NAMESPACE=n", "HOME=/home/x", "GIT_CONFIG_NOSYSTEM=0"}
	got := Hermetic(env)
	want := []string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "HOME=/home/x", "PATH=/bin"}
	// The setter orders the entries (gotool's one order); the set is
	// the contract here.
	sort.Strings(got)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("Hermetic = %q; want %q", got, want)
	}
}

func runtimeShim() string {
	if sh, err := exec.LookPath("sh"); err == nil {
		return sh
	}
	return ""
}
