package fixtureguard

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A committed git work tree holding the fixture members the tests
// plant, every stamp fixed so a touch is a move.
func committedTree(t *testing.T, stamp time.Time, members map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range members {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "-m", "fixture"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Name() == ".git" {
			return filepath.SkipDir
		}
		return os.Chtimes(path, stamp, stamp)
	}); err != nil {
		t.Fatal(err)
	}
	return dir
}

var stamp = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// The guard's judgment over a tree: an unchanged tree diffs to nothing;
// a member added, removed, rewritten (size), rewritten in place (the
// same size, time, and mode — the content digest alone), touched (time
// alone), or re-moded is named as such; a link is judged by its
// target; a directory by its time, so a member created in it moves it.
func TestDiffNamesEveryMovedMember(t *testing.T) {
	dir := committedTree(t, stamp, map[string]string{
		"lib/keep.go": "package lib\n", "lib/grow.go": "package lib\n", "lib/swap.go": "package lib\n",
		"lib/touch.go": "package lib\n", "lib/mode.go": "package lib\n", "lib/gone.go": "package lib\n",
		"other/keep.go": "package other\n",
	})
	if err := os.Symlink("keep.go", filepath.Join(dir, "lib", "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(dir, "lib"), stamp, stamp); err != nil {
		t.Fatal(err)
	}
	before, err := Take(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := before.Diff(before); len(got) != 0 {
		t.Fatalf("an unchanged tree diffs to %v", got)
	}
	write := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("lib/grow.go", "package lib\n\nvar X = 1\n")
	write("lib/swap.go", "package lob\n")
	if err := os.Chtimes(filepath.Join(dir, "lib", "swap.go"), stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(dir, "lib", "touch.go"), stamp.Add(time.Second), stamp.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, "lib", "mode.go"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "lib", "gone.go")); err != nil {
		t.Fatal(err)
	}
	write("lib/.planted-input", "A")
	if err := os.Remove(filepath.Join(dir, "lib", "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("grow.go", filepath.Join(dir, "lib", "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(dir, "lib"), stamp.Add(2*time.Second), stamp.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	after, err := Take(dir)
	if err != nil {
		t.Fatal(err)
	}
	rows := before.Diff(after)
	want := []string{
		"lib/.planted-input: added (file 1 bytes, ",
		"lib/gone.go: removed (file 12 bytes, sha256 6d5de2f89b37, 2026-01-02T03:04:05.000000000Z, mode -rw-r--r--)",
		"lib/grow.go: changed (file 12 bytes, sha256 6d5de2f89b37, 2026-01-02T03:04:05.000000000Z, mode -rw-r--r-- -> file 23 bytes, ",
		"lib/link: changed (link to keep.go -> link to grow.go)",
		"lib/mode.go: changed (file 12 bytes, sha256 6d5de2f89b37, 2026-01-02T03:04:05.000000000Z, mode -rw-r--r-- -> file 12 bytes, sha256 6d5de2f89b37, 2026-01-02T03:04:05.000000000Z, mode -rw-------)",
		"lib/swap.go: changed (file 12 bytes, sha256 6d5de2f89b37, 2026-01-02T03:04:05.000000000Z, mode -rw-r--r-- -> file 12 bytes, sha256 9341c35c9ea9, 2026-01-02T03:04:05.000000000Z, mode -rw-r--r--)",
		"lib/touch.go: changed (file 12 bytes, sha256 6d5de2f89b37, 2026-01-02T03:04:05.000000000Z, mode -rw-r--r-- -> file 12 bytes, sha256 6d5de2f89b37, 2026-01-02T03:04:06.000000000Z, mode -rw-r--r--)",
		"lib: changed (dir, 2026-01-02T03:04:05.000000000Z -> dir, 2026-01-02T03:04:07.000000000Z)",
	}
	if len(rows) != len(want) {
		t.Fatalf("diff rows = %q, want %d rows", rows, len(want))
	}
	for i, prefix := range want {
		if !strings.HasPrefix(rows[i], prefix) {
			t.Fatalf("row %d = %q, want prefix %q", i, rows[i], prefix)
		}
	}
}

// Guard answers the suite's own code over an unchanged tree; 1 over a
// tree the suite changed — whatever the suite answered — naming the
// member on the writer, a member the suite created and removed
// included (its directory moved); 1 without running the suite over a
// tree carrying an untracked member before it (an earlier run's
// residue); 1 outside a git work tree.
func TestGuardRefusesAResidueWhateverTheSuiteAnswered(t *testing.T) {
	dir := committedTree(t, stamp, map[string]string{"lib/lib.go": "package lib\n"})
	var out bytes.Buffer
	if code := Guard(&out, dir, func() int { return 3 }); code != 3 || out.Len() != 0 {
		t.Fatalf("an unchanged tree = %d, %q; want the suite's 3 and silence", code, out.String())
	}
	planted := filepath.Join(dir, "lib", ".planted-input")
	out.Reset()
	transient := func() int {
		if err := os.WriteFile(planted, []byte("A"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(planted); err != nil {
			t.Fatal(err)
		}
		return 0
	}
	if code := Guard(&out, dir, transient); code != 1 || !strings.Contains(out.String(), "changed under the suite") || !strings.Contains(out.String(), "lib: changed (dir, 2026-01-02T03:04:05.000000000Z -> dir, ") {
		t.Fatalf("a transient = %d, %q; want 1 naming the moved directory", code, out.String())
	}
	if err := os.Chtimes(filepath.Join(dir, "lib"), stamp, stamp); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := Guard(&out, dir, func() int { os.WriteFile(planted, []byte("A"), 0o644); return 0 }); code != 1 || !strings.Contains(out.String(), "lib/.planted-input: added (file 1 bytes") {
		t.Fatalf("a residue = %d, %q; want 1 naming the member", code, out.String())
	}
	// The residue stands: the next guarded run refuses before running.
	out.Reset()
	ran := false
	if code := Guard(&out, dir, func() int { ran = true; return 0 }); code != 1 || ran || !strings.Contains(out.String(), "untracked members before the suite") || !strings.Contains(out.String(), "?? lib/.planted-input") {
		t.Fatalf("a dirty start = %d, ran %v, %q; want 1 naming the residue, the suite not run", code, ran, out.String())
	}
	out.Reset()
	if code := Guard(&out, t.TempDir(), func() int { return 0 }); code != 1 || !strings.Contains(out.String(), "git status over") {
		t.Fatalf("outside a work tree = %d, %q; want 1 naming git", code, out.String())
	}
}
