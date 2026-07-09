// Package gitref is the git seam both gomutant faces share: the changed
// surface vs a ref, and reference content, via the git binary — the library
// itself stays git-free; only the shells reach here.
package gitref

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// ChangedPaths lists tree-relative paths differing from ref: tracked changes
// (--relative keeps them tree-relative when the tree is not the repo root)
// plus untracked files — a brand-new uncommitted file is part of the changed
// surface, never silently absent (REQ-target-changed). quotepath is off so a
// non-ASCII path arrives as bytes, not an escaped quoted string.
func ChangedPaths(dir, ref string) ([]string, error) {
	tracked, err := output(dir, "-c", "core.quotepath=off", "diff", "--name-only", "--relative", ref)
	if err != nil {
		return nil, err
	}
	untracked, err := output(dir, "-c", "core.quotepath=off", "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var paths []string
	for _, out := range [][]byte{tracked, untracked} {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if line != "" && !seen[line] {
				seen[line] = true
				paths = append(paths, line)
			}
		}
	}
	return paths, nil
}

// Show reads a tree-relative path's content at ref; ok=false when the path
// did not exist there (a new file reads as all changed). The ./ form
// resolves against the command's directory, so it stays correct when the
// tree is not the repo root.
func Show(dir, ref, path string) ([]byte, bool) {
	out, err := output(dir, "show", ref+":./"+path)
	if err != nil {
		return nil, false
	}
	return out, true
}

func output(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
