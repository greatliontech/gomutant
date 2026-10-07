// Package fixtureguard keeps a tracked fixture tree read-only under a
// test suite: a suite's TestMain refuses a tree carrying untracked
// members before the run, snapshots it, runs the tests, and refuses a
// difference after — a member added, removed, or rewritten, a
// directory a member was created in or removed from — naming the
// members. Every test that plants a file inside a tracked fixture
// module copies the module first; the guard is what keeps that so: a
// residue in the tracked tree, or a member appearing and vanishing
// there, moves the observation brackets of every other package's tests
// reading the tree in place in the same run.
//
// The walk's scope is the work tree: the `.git` at its root — the
// repository's own store, which git itself rewrites under a commit
// (background maintenance, its lock files, the index) — is never a
// fixture member and is not descended into, so a maintenance run that
// overlaps the walk cannot fault it. A `.git` below the root is a
// residue the walk refuses: git tracks no `.git` path component, so no
// tracked tree holds one, and `git status` reports neither a nested
// repository's store nor a linked work tree's `.git` file, so the
// untracked-member refusal cannot see it; a test that read the fixture
// in place would resolve its repository root to the residue. A member
// vanishing under the walk anywhere else is a fault the guard names
// rather than a difference it judges.
package fixtureguard

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Snapshot is a fixture tree's members with the facts a rewrite moves:
// a regular file's size, content digest, modification time, and mode;
// a directory's modification time (moved by a member created in it or
// removed from it, so a transient the run cleaned up is still seen); a
// symbolic link's target. The tree's root is the "." member.
type Snapshot map[string]string

// Take walks dir and records every member of the work tree: the
// root's `.git` is skipped whole (a directory, or a linked work tree's
// file naming its store); a `.git` below the root is refused naming it
// — a residue, never a member (the package doc states both).
func Take(dir string) (Snapshot, error) {
	snap := Snapshot{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if d.Name() == ".git" {
			if rel != ".git" {
				return fmt.Errorf("a repository store below the guarded root: %s is a residue, never a fixture member", rel)
			}
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		stamp := info.ModTime().UTC().Format("2006-01-02T15:04:05.000000000Z")
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			snap[rel] = "link to " + target
		case info.IsDir():
			snap[rel] = "dir, " + stamp
		default:
			digest, err := digestOf(path)
			if err != nil {
				return err
			}
			snap[rel] = fmt.Sprintf("file %d bytes, sha256 %s, %s, mode %v", info.Size(), digest, stamp, info.Mode().Perm())
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return snap, nil
}

func digestOf(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil))[:12], nil
}

// Diff names every member the later snapshot added, removed, or
// changed, one row each, in path order; empty when the trees agree.
func (s Snapshot) Diff(later Snapshot) []string {
	var rows []string
	for path, before := range s {
		after, ok := later[path]
		switch {
		case !ok:
			rows = append(rows, path+": removed ("+before+")")
		case after != before:
			rows = append(rows, path+": changed ("+before+" -> "+after+")")
		}
	}
	for path, after := range later {
		if _, ok := s[path]; !ok {
			rows = append(rows, path+": added ("+after+")")
		}
	}
	sort.Strings(rows)
	return rows
}

// Untracked names the members of dir its git work tree does not track
// — untracked and ignored alike — the residue an earlier run may have
// left, which a snapshot taken over it would carry on both sides.
func Untracked(dir string) ([]string, error) {
	cmd := exec.Command("git", "-C", dir, "status", "--porcelain", "--ignored", "--untracked-files=all", "--", ".")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git status over %s: %v: %s", dir, err, strings.TrimSpace(string(out)))
	}
	var rows []string
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if strings.HasPrefix(line, "?? ") || strings.HasPrefix(line, "!! ") {
			rows = append(rows, line)
		}
	}
	return rows, nil
}

// Guard runs a suite (a TestMain's m.Run) with dir guarded: a tree
// carrying untracked members is refused before the run (a residue
// would sit in both snapshots); otherwise the tree is snapshotted, the
// suite runs, and a difference after fails the suite whatever the
// tests reported, naming every member that moved on w. The exit code
// is the suite's when the tree is unchanged. A dir outside a git work
// tree, or one the walk cannot read, is 1 naming the fault.
func Guard(w io.Writer, dir string, run func() int) int {
	untracked, err := Untracked(dir)
	if err != nil {
		fmt.Fprintf(w, "fixtureguard: %v\n", err)
		return 1
	}
	if len(untracked) > 0 {
		fmt.Fprintf(w, "fixtureguard: the tracked fixture tree %s carries untracked members before the suite - a residue of an earlier run; remove them:\n  %s\n", dir, strings.Join(untracked, "\n  "))
		return 1
	}
	before, err := Take(dir)
	if err != nil {
		fmt.Fprintf(w, "fixtureguard: %s: %v\n", dir, err)
		return 1
	}
	code := run()
	after, err := Take(dir)
	if err != nil {
		fmt.Fprintf(w, "fixtureguard: %s: %v\n", dir, err)
		return 1
	}
	if rows := before.Diff(after); len(rows) > 0 {
		fmt.Fprintf(w, "fixtureguard: the tracked fixture tree %s changed under the suite - a test in this run wrote into it instead of a copy:\n  %s\n", dir, strings.Join(rows, "\n  "))
		return 1
	}
	return code
}
