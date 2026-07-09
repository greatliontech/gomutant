package gitref

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
)

// TestChangedPaths pins the changed-surface listing (REQ-target-changed):
// tracked edits and untracked files both appear, tree-relative even when
// the tree is not the repo root, non-ASCII names unmangled; gitShow resolves
// ref content tree-relative and reports absence for new files.
func TestChangedPaths(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("config", "user.email", "t@example.invalid")
	git("config", "user.name", "t")
	// The module lives in a subdirectory: paths must stay tree-relative.
	sub := filepath.Join(repo, "svc")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(sub, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("tracked.go", "package svc\n")
	write("日本.go", "package svc\n")
	git("add", ".")
	git("commit", "-q", "-m", "init")
	write("tracked.go", "package svc\n\nfunc F() {}\n")
	write("日本.go", "package svc\n\nfunc G() {}\n")
	write("untracked.go", "package svc\n\nfunc H() {}\n")

	paths, err := ChangedPaths(sub, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	want := []string{"tracked.go", "untracked.go", "日本.go"}
	sort.Strings(want)
	if len(paths) != len(want) {
		t.Fatalf("paths = %q, want %q", paths, want)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("paths = %q, want %q", paths, want)
		}
	}

	// Reference content resolves tree-relative; a new file reads as absent.
	if b, ok := Show(sub, "HEAD", "tracked.go"); !ok || string(b) != "package svc\n" {
		t.Fatalf("gitShow tracked = %q ok=%v", b, ok)
	}
	if _, ok := Show(sub, "HEAD", "untracked.go"); ok {
		t.Fatal("a new file read as present at the ref")
	}
}
