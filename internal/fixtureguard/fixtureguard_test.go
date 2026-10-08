package fixtureguard

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gomutant/internal/gitfixture"
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
	if err := gitfixture.Init(dir); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "fixture"}} {
		cmd, err := gitfixture.Command(dir, args...)
		if err != nil {
			t.Fatal(err)
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	// The members are the walk's own rows, so the store stays untouched.
	members, err := Take(dir)
	if err != nil {
		t.Fatal(err)
	}
	for rel := range members {
		if err := os.Chtimes(filepath.Join(dir, rel), stamp, stamp); err != nil {
			t.Fatal(err)
		}
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

// The repository's own store is outside the walk: nothing under .git
// is a member, and a .git the walk cannot read — an object store git
// is maintaining, a lock file appearing under it — never faults the
// snapshot, which is the race the runner's auto-maintenance (git 2.29
// and later) exposed: its maintenance.lock vanished between readdir
// and lstat. A store below the root is a residue the walk refuses,
// naming it, and the guard refuses before the run — `git status`
// reports neither a nested repository nor a linked work tree's file.
func TestGitStoreIsOutsideTheWalk(t *testing.T) {
	dir := committedTree(t, stamp, map[string]string{"lib/keep.go": "package lib\n", "other/keep.go": "package other\n"})
	quiet, err := Take(dir)
	if err != nil {
		t.Fatal(err)
	}
	for member := range quiet {
		if member == ".git" || strings.HasPrefix(member, ".git/") {
			t.Fatalf("the store is a member: %q", member)
		}
	}
	lock := filepath.Join(dir, ".git", "objects", "maintenance.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	objects := filepath.Join(dir, ".git", "objects")
	if err := os.Chmod(objects, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(objects, 0o755) })
	busy, err := Take(dir)
	if err != nil {
		t.Fatalf("a busy store faulted the walk: %v", err)
	}
	if rows := quiet.Diff(busy); len(rows) != 0 {
		t.Fatalf("the store's churn moved the snapshot: %v", rows)
	}
	if err := os.Chmod(objects, 0o755); err != nil {
		t.Fatal(err)
	}
	// A linked work tree's .git file in a tracked directory, and a
	// repository initialised in another: each refused naming it, the
	// suite not run (git status reports neither).
	for _, residue := range []struct{ rel, plant string }{{"lib/.git", "file"}, {"other/.git", "repository"}} {
		path := filepath.Join(dir, filepath.FromSlash(residue.rel))
		if residue.plant == "file" {
			if err := os.WriteFile(path, []byte("gitdir: elsewhere\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		} else if err := gitfixture.Init(filepath.Dir(path)); err != nil {
			t.Fatal(err)
		}
		if _, err := Take(dir); err == nil || !strings.Contains(err.Error(), "a repository store below the guarded root: "+residue.rel+" is a residue") {
			t.Fatalf("a nested %s %s: Take err = %v; want the residue named", residue.plant, residue.rel, err)
		}
		var out bytes.Buffer
		ran := false
		if code := Guard(&out, dir, func() int { ran = true; return 0 }); code != 1 || ran || !strings.Contains(out.String(), residue.rel+" is a residue") {
			t.Fatalf("a nested %s: guard = %d, ran %v, %q; want 1 naming it, the suite not run", residue.plant, code, ran, out.String())
		}
		if err := os.RemoveAll(path); err != nil {
			t.Fatal(err)
		}
	}
}

// A fixture repository is hermetic to the host's git: the developer's
// global and the system configuration — a hook path whose post-commit
// hook writes into the work tree (git runs a hook from the work tree's
// root), the shape of a writer racing the temporary tree's removal —
// never reach it, and its own configuration carries the
// no-maintenance rule and the identity.
func TestFixtureRepositoriesAreHermetic(t *testing.T) {
	hooks := t.TempDir()
	hook := filepath.Join(hooks, "post-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch hooked\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The same configuration planted as the global and as the system
	// one: each half of the hermetic environment is witnessed alone.
	for _, scope := range []string{"GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM"} {
		file := filepath.Join(t.TempDir(), "gitconfig")
		if err := os.WriteFile(file, []byte("[core]\n\thooksPath = "+hooks+"\n[maintenance]\n\tauto = true\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv(scope, file)
	}
	// The command scope (a wrapping process's exported entries) and the
	// repository redirection a hook exports: neither reaches the fixture.
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.hooksPath")
	t.Setenv("GIT_CONFIG_VALUE_0", hooks)
	foreign := filepath.Join(t.TempDir(), "foreign.git")
	t.Setenv("GIT_DIR", foreign)
	t.Setenv("GIT_WORK_TREE", t.TempDir())
	// GIT_CONFIG redirects every `git config` write; a template
	// directory seeds every `git init` with its hooks.
	redirected := filepath.Join(t.TempDir(), "config")
	t.Setenv("GIT_CONFIG", redirected)
	templates := t.TempDir()
	if err := os.MkdirAll(filepath.Join(templates, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(templates, "hooks", "post-commit"), []byte("#!/bin/sh\ntouch templated\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_TEMPLATE_DIR", templates)
	for name, build := range map[string]func() string{
		"committedTree":      func() string { return committedTree(t, stamp, map[string]string{"lib/keep.go": "package lib\n"}) },
		"gitfixture.Changed": func() string { return gitfixture.Changed(t) },
	} {
		dir := build()
		if _, err := os.Stat(filepath.Join(dir, "hooked")); err == nil {
			t.Fatalf("%s: the host's post-commit hook ran in the fixture", name)
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
			t.Fatalf("%s: the fixture's own repository is missing — the host's GIT_DIR redirected it: %v", name, err)
		}
		if _, err := os.Stat(foreign); err == nil {
			t.Fatalf("%s: the fixture wrote into the host's GIT_DIR", name)
		}
		if _, err := os.Stat(filepath.Join(dir, "templated")); err == nil {
			t.Fatalf("%s: the host's template hook ran in the fixture", name)
		}
		if _, err := os.Stat(redirected); err == nil {
			t.Fatalf("%s: the fixture's configuration was written to the host's GIT_CONFIG", name)
		}
		// The repository's OWN file carries the configuration — read
		// through the file, never through a `git config` the host
		// could redirect.
		for key, want := range map[string]string{"maintenance.auto": "false", "gc.auto": "0", "user.name": "t"} {
			cmd, err := gitfixture.Command(dir, "config", "--file", filepath.Join(dir, ".git", "config"), "--get", key)
			if err != nil {
				t.Fatal(err)
			}
			out, err := cmd.Output()
			if err != nil || strings.TrimSpace(string(out)) != want {
				t.Fatalf("%s: config %s = %q, %v; want %q", name, key, out, err, want)
			}
		}
	}
}
