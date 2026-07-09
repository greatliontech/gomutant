package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"

	gomutant "github.com/greatliontech/gomutant"
)

// TestSaveFindingsMerge pins the document merge: a fresh finding replaces
// its symbol's record, untouched symbols persist (a scoped run must not
// drop the rest of the document), and skips write nothing.
func TestSaveFindingsMerge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "findings.json")
	prior := []gomutant.Finding{
		{Symbol: "p.Old", BodyHash: "h1", OperatorSet: "go/2", Toolchain: "tc", Mutants: 2, Killed: 2},
		{Symbol: "p.Stay", BodyHash: "h2", OperatorSet: "go/2", Toolchain: "tc", Mutants: 1, Killed: 1},
	}
	fresh := []gomutant.Finding{
		{Symbol: "p.Old", BodyHash: "h1b", OperatorSet: "go/2", Toolchain: "tc", Mutants: 3, Killed: 2,
			Survivors: []gomutant.Survivor{{Position: "f.go:1:1", Operator: "zero return"}}},
		{Symbol: "p.Skipped", Skipped: "no oracle"},
	}
	if err := saveFindings(path, prior, fresh); err != nil {
		t.Fatal(err)
	}
	got, err := loadFindings(path)
	if err != nil {
		t.Fatal(err)
	}
	bySym := map[string]gomutant.Finding{}
	for _, f := range got {
		bySym[f.Symbol] = f
	}
	if len(got) != 2 {
		t.Fatalf("document has %d findings, want 2: %+v", len(got), got)
	}
	if bySym["p.Old"].BodyHash != "h1b" || bySym["p.Old"].Mutants != 3 {
		t.Fatalf("fresh finding did not replace prior: %+v", bySym["p.Old"])
	}
	if bySym["p.Stay"].BodyHash != "h2" {
		t.Fatal("an untouched symbol was dropped by a scoped run")
	}
	if _, ok := bySym["p.Skipped"]; ok {
		t.Fatal("a skipped result was persisted")
	}
}

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

	paths, err := changedPaths(sub, "HEAD")
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
	if b, ok := gitShow(sub, "HEAD", "tracked.go"); !ok || string(b) != "package svc\n" {
		t.Fatalf("gitShow tracked = %q ok=%v", b, ok)
	}
	if _, ok := gitShow(sub, "HEAD", "untracked.go"); ok {
		t.Fatal("a new file read as present at the ref")
	}
}
