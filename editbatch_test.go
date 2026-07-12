package gomutant

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareEditBatch(t *testing.T) {
	root := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.go", "package p\nfunc A() int { return 1 }\n")
	write("sub/b.go", "package p\nfunc B() int { return 2 }\n")

	got, err := prepareEditBatch(root, []BatchEdit{
		{File: "sub/b.go", OldString: "return 2", NewString: "return 0"},
		{File: "a.go", OldString: "return 1", NewString: "return -1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].File != "a.go" || !strings.Contains(string(got[0].Source), "return -1") || got[1].File != "sub/b.go" || !strings.Contains(string(got[1].Source), "return 0") {
		t.Fatalf("replacements = %+v", got)
	}
	for _, file := range got {
		onDisk, err := os.ReadFile(file.Abs)
		if err != nil {
			t.Fatal(err)
		}
		if string(onDisk) == string(file.Source) {
			t.Fatalf("prepare changed worktree file %s", file.File)
		}
	}
	write("noop.go", "ab")
	got, err = prepareEditBatch(root, []BatchEdit{
		{File: "noop.go", OldString: "a", NewString: ""},
		{File: "noop.go", OldString: "b", NewString: "ab"},
		{File: "a.go", OldString: "return 1", NewString: "return 3"},
	})
	if err != nil || len(got) != 1 || got[0].File != "a.go" {
		t.Fatalf("effective replacements with one no-op file = %+v, %v", got, err)
	}
	write("empty.go", "x")
	got, err = prepareEditBatch(root, []BatchEdit{{File: "empty.go", OldString: "x", NewString: ""}})
	if err != nil || len(got) != 1 || got[0].Source == nil || len(got[0].Source) != 0 {
		t.Fatalf("empty replacement = %+v, %v", got, err)
	}
}

func TestPrepareEditBatchRejectsInvalidEdits(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("alpha beta alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		edits []BatchEdit
		want  string
	}{
		{name: "empty batch", want: "empty"},
		{name: "empty match", edits: []BatchEdit{{File: "a.go", NewString: "x"}}, want: "empty match"},
		{name: "identical", edits: []BatchEdit{{File: "a.go", OldString: "beta", NewString: "beta"}}, want: "byte-identical"},
		{name: "missing", edits: []BatchEdit{{File: "a.go", OldString: "gamma", NewString: "x"}}, want: "matches nothing"},
		{name: "ambiguous", edits: []BatchEdit{{File: "a.go", OldString: "alpha", NewString: "x"}}, want: "ambiguous"},
		{name: "overlap", edits: []BatchEdit{{File: "a.go", OldString: "alpha beta", NewString: "x"}, {File: "a.go", OldString: "beta alpha", NewString: "y"}}, want: "overlaps"},
		{name: "introduced match", edits: []BatchEdit{{File: "a.go", OldString: "beta", NewString: "gamma"}, {File: "a.go", OldString: "gamma", NewString: "x"}}, want: "matches nothing"},
		{name: "escape", edits: []BatchEdit{{File: "../a.go", OldString: "beta", NewString: "x"}}, want: "invalid tree-relative"},
		{name: "non canonical", edits: []BatchEdit{{File: "./a.go", OldString: "beta", NewString: "x"}}, want: "invalid tree-relative"},
		{name: "missing file", edits: []BatchEdit{{File: "missing.go", OldString: "beta", NewString: "x"}}, want: "resolve file"},
		{name: "batch no-op", edits: []BatchEdit{{File: "a.go", OldString: "alpha beta", NewString: ""}, {File: "a.go", OldString: " alpha", NewString: "alpha beta alpha"}}, want: "changes no files"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := prepareEditBatch(root, tt.edits)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestPrepareEditBatchRejectsSymlinkEscapeAndAlias(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outside, []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape.go")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := prepareEditBatch(root, []BatchEdit{{File: "escape.go", OldString: "outside", NewString: "changed"}}); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("symlink escape accepted: %v", err)
	}

	real := filepath.Join(root, "real.go")
	if err := os.WriteFile(real, []byte("real\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(root, "alias.go")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err := prepareEditBatch(root, []BatchEdit{
		{File: "real.go", OldString: "real", NewString: "one"},
		{File: "alias.go", OldString: "real", NewString: "two"},
	})
	if err == nil || !strings.Contains(err.Error(), "resolve to the same file") {
		t.Fatalf("file alias accepted: %v", err)
	}
}
