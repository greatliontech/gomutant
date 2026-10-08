// Package gitref is the changed-surface and ref-content seam both
// gomutant faces share, over the one git runner (internal/gitcmd); the
// root package's provenance, the fixture guard and the fixture package
// read git through the same runner, and every other package stays
// git-free.
package gitref

import (
	"context"

	"github.com/greatliontech/gomutant/internal/gitcmd"
)

// ShowContext reads a tree-relative path's content at ref; ok=false
// when the path did not exist there (a new file reads as all changed).
// The ./ form resolves against the command's directory, so it stays
// correct when the tree is not the repo root.
func ShowContext(ctx context.Context, dir, ref, path string) ([]byte, bool) {
	out, err := gitcmd.Output(ctx, dir, "show", ref+":./"+path)
	if err != nil {
		return nil, false
	}
	return out, true
}

// ContentAt is the ref's content as the changed-surface discovery
// reads it — the one closure every face's changed-ref path hands
// Tree.DiscoverChangedSurfaceContext, written once.
func ContentAt(ctx context.Context, dir, ref string) func(path string) ([]byte, bool) {
	return func(path string) ([]byte, bool) { return ShowContext(ctx, dir, ref, path) }
}
