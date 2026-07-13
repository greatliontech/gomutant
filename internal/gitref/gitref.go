// Package gitref is the git seam both gomutant faces share: the changed
// surface vs a ref, and reference content, via the git binary — the library
// itself stays git-free; only the shells reach here.
package gitref

import (
	"bytes"
	"context"
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
	return ChangedPathsContext(context.Background(), dir, ref)
}

// ChangedPathsContext is ChangedPaths with caller-owned cancellation.
func ChangedPathsContext(ctx context.Context, dir, ref string) ([]string, error) {
	tracked, err := outputContext(ctx, dir, "-c", "core.quotepath=off", "diff", "--name-only", "--relative", ref)
	if err != nil {
		return nil, err
	}
	untracked, err := outputContext(ctx, dir, "-c", "core.quotepath=off", "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var paths []string
	for _, out := range [][]byte{tracked, untracked} {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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
	return ShowContext(context.Background(), dir, ref, path)
}

// ShowContext is Show with caller-owned cancellation.
func ShowContext(ctx context.Context, dir, ref, path string) ([]byte, bool) {
	out, err := outputContext(ctx, dir, "show", ref+":./"+path)
	if err != nil {
		return nil, false
	}
	return out, true
}

func output(dir string, args ...string) ([]byte, error) {
	return outputContext(context.Background(), dir, args...)
}

func outputContext(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
