package gomutant

import (
	"context"
	"path/filepath"
)

// LineRange is a half-open run of 1-based line numbers [From, To) in
// the current content of a file.
type LineRange struct {
	From, To int
}

// Contains reports whether the 1-based line lies in the range.
func (r LineRange) Contains(line int) bool { return line >= r.From && line < r.To }

// ChangedSurface is what git reports changed against a ref: the
// tree-relative changed paths (tracked changes and untracked files)
// and, per Go path, the lines the working tree added since the ref.
// The shells read it through the git seam; the library stays git-free.
type ChangedSurface struct {
	Ref   string
	Paths []string
	Added map[string][]LineRange
}

// DeltaCut is a changed-ref campaign's survivor cut: the added-line
// surface beside the set of symbols the ref's changed surface names
// canonically changed (REQ-target-changed's projection — a formatting
// reflow changes no symbol). It is a run parameter, never a record
// fact: a survivor's place in it is derived at report time and
// persisted nowhere (REQ-exec-run-status's delta cut).
type DeltaCut struct {
	Ref     string
	Added   map[string][]LineRange
	Changed map[string]bool
}

// OnDelta reports whether the tree-relative file's 1-based line lies
// in the cut's added lines.
func (c DeltaCut) OnDelta(path string, line int) bool {
	for _, r := range c.Added[path] {
		if r.Contains(line) {
			return true
		}
	}
	return false
}

// DiscoverChangedSurfaceContext is changed-scope discovery over a
// surface the git seam read (DiscoverChangedContext) that also derives
// the run's delta cut from it: the cut's changed symbols are exactly
// the discovered targets' — the one canonical answer both the target
// set and the cut come from.
func (t *Tree) DiscoverChangedSurfaceContext(ctx context.Context, surface ChangedSurface, ref func(path string) ([]byte, bool)) ([]Target, []Residue, DeltaCut, error) {
	targets, residue, err := t.DiscoverChangedContext(ctx, surface.Paths, ref)
	if err != nil {
		return nil, nil, DeltaCut{}, err
	}
	cut := DeltaCut{Ref: surface.Ref, Added: surface.Added, Changed: make(map[string]bool, len(targets))}
	for _, tg := range targets {
		cut.Changed[tg.Symbol] = true
	}
	return targets, residue, cut, nil
}

// DeltaSurvivors is a record's open survivors split by the cut: the
// ones on the delta's added lines and the pre-existing remainder, in
// the record's survivor order.
type DeltaSurvivors struct {
	OnDelta   []Survivor
	Remainder []Survivor
	placed    map[int]bool
}

// IsOnDelta reports whether the i-th open survivor (Finding.Open order)
// lies on the delta.
func (d DeltaSurvivors) IsOnDelta(i int) bool { return d.placed[i] }

// CutSurvivorsContext splits f's open survivors by the cut. A survivor
// is placed only when its record can be: the mutated symbol is one the
// cut names canonically changed, and the record's body hash is the
// symbol's current one — a record measured against another body
// carries positions the current file's lines do not mean, and a
// symbol the ref's surface leaves canonically unchanged has no line of
// its own on the change however git's reflow counts it. Placement
// resolves the symbol's package directory through the tree and makes
// the survivor's base-name file tree-relative, symlink-aware. Whatever
// cannot be placed — the record, the package, the path, the position —
// is remainder: the cut never widens the delta it cannot place.
func (t *Tree) CutSurvivorsContext(ctx context.Context, f Finding, cut DeltaCut) (DeltaSurvivors, error) {
	open := f.Open()
	out := DeltaSurvivors{Remainder: open, placed: map[int]bool{}}
	if len(open) == 0 || len(cut.Added) == 0 || !cut.Changed[f.Symbol] {
		return out, nil
	}
	current, err := t.eng.BodyHashContext(ctx, f.Symbol)
	if err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		return out, nil
	}
	if current != f.BodyHash {
		return out, nil
	}
	_, pkgDir, err := t.eng.PackageContextContext(ctx, symbolPackage(f.Symbol))
	if err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		return out, nil
	}
	out.Remainder = nil
	for i, s := range open {
		file, line, _, ok := splitSurvivorPosition(s.Position)
		if !ok {
			out.Remainder = append(out.Remainder, s)
			continue
		}
		rel, ok := treeRelative(t.dir, filepath.Join(pkgDir, file))
		if ok && cut.OnDelta(rel, line) {
			out.OnDelta = append(out.OnDelta, s)
			out.placed[i] = true
		} else {
			out.Remainder = append(out.Remainder, s)
		}
	}
	return out, nil
}

// DeltaSummary is a changed-ref run's summary of the cut: the open
// survivors on the delta's added lines across the rendered records,
// beside the ref they were cut against.
type DeltaSummary struct {
	Ref  string `json:"ref"`
	Open int    `json:"open"`
}
