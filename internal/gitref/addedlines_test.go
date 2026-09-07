package gitref

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/greatliontech/gomutant"
)

type repo struct {
	t   *testing.T
	dir string
	sub string
}

func newRepo(t *testing.T) repo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	root := t.TempDir()
	sub := filepath.Join(root, "svc")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	r := repo{t: t, dir: root, sub: sub}
	r.git("init", "-q")
	r.git("config", "user.email", "t@example.invalid")
	r.git("config", "user.name", "t")
	return r
}

func (r repo) git(args ...string) {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	if out, err := cmd.CombinedOutput(); err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func (r repo) write(rel, content string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.sub, rel), []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// The changed surface is git's: modified and inserted lines in tracked
// Go files, whole untracked Go files, nothing for deletions, unchanged
// files, or non-Go paths (which still appear among the changed paths),
// tree-relative under a subdirectory module (REQ-target-changed's
// delta geometry).
func TestChangedSurface(t *testing.T) {
	r := newRepo(t)
	r.write("edit.go", "package svc\n\nfunc F() int {\n\treturn 1\n}\n\nfunc G() {}\n")
	r.write("same.go", "package svc\n")
	r.write("gone.go", "package svc\n\nfunc Gone() {}\n")
	r.write("notes.md", "one\n")
	r.git("add", ".")
	r.git("commit", "-q", "-m", "init")
	// Line 4 modified, two lines inserted after line 6 (new lines 7-8),
	// G's body untouched; a file deleted; an untracked Go file of three
	// lines without a final newline; an untracked non-Go file.
	r.write("edit.go", "package svc\n\nfunc F() int {\n\treturn 2\n}\n\nfunc E() {}\n\nfunc G() {}\n")
	if err := os.Remove(filepath.Join(r.sub, "gone.go")); err != nil {
		t.Fatal(err)
	}
	r.write("new.go", "package svc\n\nfunc New() {}")
	r.write("notes.md", "one\ntwo\n")
	r.write("data.bin", strings.Repeat("x", 1<<20))

	surface, err := ChangedSurface(r.sub, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	paths := append([]string(nil), surface.Paths...)
	sort.Strings(paths)
	if want := []string{"data.bin", "edit.go", "gone.go", "new.go", "notes.md"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("changed paths = %v, want %v", paths, want)
	}
	want := map[string][]gomutant.LineRange{
		"edit.go": {{From: 4, To: 5}, {From: 7, To: 9}},
		"new.go":  {{From: 1, To: 4}},
	}
	if !reflect.DeepEqual(surface.Added, want) {
		t.Fatalf("added lines = %v, want %v", surface.Added, want)
	}
	if !surface.Added["edit.go"][1].Contains(8) || surface.Added["edit.go"][1].Contains(9) || surface.Added["edit.go"][0].Contains(3) {
		t.Fatal("range containment is not the half-open [From, To)")
	}
	if surface.Ref != "HEAD" {
		t.Fatalf("surface ref = %q", surface.Ref)
	}
}

// An added content line that itself starts with `+++ ` never re-keys
// the file: headers are read only between a `diff --git` line and the
// file's first hunk (the parser refused a widened cut when it did).
func TestChangedSurfaceContentCannotRekeyTheFile(t *testing.T) {
	r := newRepo(t)
	r.write("doc.go", "package svc\n\n// l1\n// l2\n// l3\n")
	r.write("edit.go", "package svc\n\nfunc F() {}\n")
	r.git("add", ".")
	r.git("commit", "-q", "-m", "init")
	r.write("doc.go", "package svc\n\n// l1\n++ edit.go\n// l2\n// l3\n// tail\n")
	surface, err := ChangedSurface(r.sub, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]gomutant.LineRange{"doc.go": {{From: 4, To: 5}, {From: 7, To: 8}}}
	if !reflect.DeepEqual(surface.Added, want) {
		t.Fatalf("added lines = %v, want %v (edit.go untouched)", surface.Added, want)
	}
}

// The operator's diff configuration cannot change the parsed grammar:
// mnemonic or absent prefixes and an external diff driver are all
// overridden (the parse silently found nothing under them before).
func TestChangedSurfaceIgnoresDiffConfiguration(t *testing.T) {
	r := newRepo(t)
	r.write("b.go", "package svc\n\nfunc B() {}\n")
	r.git("add", ".")
	r.git("commit", "-q", "-m", "init")
	r.write("b.go", "package svc\n\nfunc B() {}\n\nfunc C() {}\n")
	want := map[string][]gomutant.LineRange{"b.go": {{From: 4, To: 6}}}
	for _, cfg := range [][]string{
		{"diff.mnemonicPrefix", "true"},
		{"diff.noprefix", "true"},
		{"diff.external", "/bin/false"},
		{"diff.context", "3"},
	} {
		r.git("config", cfg[0], cfg[1])
		surface, err := ChangedSurface(r.sub, "HEAD")
		if err != nil {
			t.Fatalf("%s: %v", cfg[0], err)
		}
		if !reflect.DeepEqual(surface.Added, want) {
			t.Fatalf("%s: added lines = %v, want %v", cfg[0], surface.Added, want)
		}
		r.git("config", "--unset", cfg[0])
	}
}

// Hunk headers with and without counts, deletions, and malformed
// shapes; a header outside a file block and a `+++` outside a header
// are ignored or refused, never keyed.
func TestParseUnifiedAdded(t *testing.T) {
	for _, tc := range []struct {
		header      string
		from, count int
		bad         bool
	}{
		{"@@ -1,3 +4,2 @@", 4, 2, false},
		{"@@ -1 +7 @@ func F()", 7, 1, false},
		{"@@ -5,2 +4,0 @@", 4, 0, false},
		{"@@ -1,3 +x,2 @@", 0, 0, true},
		{"@@ garbage", 0, 0, true},
	} {
		from, count, err := parseHunkAdded(tc.header)
		if tc.bad {
			if err == nil {
				t.Errorf("%q parsed as %d,%d", tc.header, from, count)
			}
			continue
		}
		if err != nil || from != tc.from || count != tc.count {
			t.Errorf("%q = %d,%d,%v; want %d,%d", tc.header, from, count, err, tc.from, tc.count)
		}
	}
	diff := "diff --git a/x.go b/x.go\n--- a/x.go\n+++ b/x.go\n@@ -1 +1,2 @@\n+++ y.go\n+more\n@@ -5 +6 @@\n+z\ndiff --git a/gone.go b/gone.go\n--- a/gone.go\n+++ /dev/null\n@@ -1,3 +0,0 @@\n-a\n-b\n-c\ndiff --git a/skip.go b/skip.go\n--- a/skip.go\n+++ b/skip.go\n@@ -1 +1 @@\n+s\n"
	added := map[string][]gomutant.LineRange{}
	if err := parseUnifiedAdded(strings.NewReader(diff), func(p string) bool { return p != "skip.go" }, added); err != nil {
		t.Fatal(err)
	}
	want := map[string][]gomutant.LineRange{"x.go": {{From: 1, To: 3}, {From: 6, To: 7}}}
	if !reflect.DeepEqual(added, want) {
		t.Fatalf("parsed = %v, want %v", added, want)
	}
	if err := parseUnifiedAdded(strings.NewReader("diff --git a/w b/w\n--- a/w\n+++ w/b/w\n@@ -1 +1 @@\n+q\n"), func(string) bool { return true }, map[string][]gomutant.LineRange{}); err == nil {
		t.Fatal("an unexpected header prefix was accepted")
	}
}

// A renamed file is wholly new to changed-scope discovery (its path has
// no content at the ref), so its added lines are the whole file, never
// the hunks against the old path (git's rename pairing is off).
func TestChangedSurfaceReadsARenamedFileWhole(t *testing.T) {
	r := newRepo(t)
	r.write("old.go", "package svc\n\nfunc F() {}\n")
	r.git("add", ".")
	r.git("commit", "-q", "-m", "init")
	r.git("mv", "svc/old.go", "svc/new.go")
	r.write("new.go", "package svc\n\nfunc F() {}\n\nfunc G() {}\n")
	r.git("add", ".")
	surface, err := ChangedSurface(r.sub, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]gomutant.LineRange{"new.go": {{From: 1, To: 6}}}
	if !reflect.DeepEqual(surface.Added, want) {
		t.Fatalf("added lines = %v, want %v", surface.Added, want)
	}
}
