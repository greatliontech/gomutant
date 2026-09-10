// Package gitref is the changed-surface and ref-content seam both
// gomutant faces share, via the git binary; the root package's
// provenance keeps its own git reads (the repository state a record
// pins), and every other package stays git-free.
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
// It is the changed surface's path list alone (ChangedSurfaceContext).
func ChangedPathsContext(ctx context.Context, dir, ref string) ([]string, error) {
	surface, err := ChangedSurfaceContext(ctx, dir, ref)
	if err != nil {
		return nil, err
	}
	return surface.Paths, nil
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

// ContentAt is the ref's content as the changed-surface discovery
// reads it — the one closure every face's changed-ref path hands
// Tree.DiscoverChangedSurfaceContext, written once.
func ContentAt(ctx context.Context, dir, ref string) func(path string) ([]byte, bool) {
	return func(path string) ([]byte, bool) { return ShowContext(ctx, dir, ref, path) }
}
