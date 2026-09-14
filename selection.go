package gomutant

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// ChangedSelection is the git seam's one answer to a changed ref: the
// ref's surface and the content reader at the ref, always together —
// discovery over the surface reads the ref's content.
type ChangedSelection struct {
	Surface ChangedSurface
	Ref     func(path string) ([]byte, bool)
}

// TargetInputs are a request's target sources as a face received them:
// a targets document by path (the face's own resolution — the MCP face
// confines it to the tree first) or inline, and a changed ref through
// the git seam's reader the face binds to its directory. At most one
// is given; the preparation refuses two (ValidateTargetSources) and
// then reads the one given at its enumerated place
// (REQ-exec-preparation): the document's parse right after the
// exclusivity, the ref's surface after the tree root is known to
// exist — the read runs in it.
type TargetInputs struct {
	TargetsPath string
	// TargetsRoot, when set, confines TargetsPath to the tree: a
	// tree-relative spelling that cannot escape (ConfineToTree), resolved
	// under the root before it is read — the served face's rule
	// (REQ-mcp-envelope), refused where the document is refused.
	TargetsRoot string
	TargetsJSON []byte
	// Changed reads the ref's surface; nil when no ref was given.
	Changed func(ctx context.Context) (*ChangedSelection, error)
}

// ConfineToTree admits a tree-relative path that cannot escape the root
// — no absolute form, no drive, no backslash, clean, not "." and not
// under ".." — and returns it resolved under the root; name spells the
// input in the refusal.
func ConfineToTree(name, root, p string) (string, error) {
	drive := len(p) >= 2 && p[1] == ':' && ((p[0] >= 'a' && p[0] <= 'z') || (p[0] >= 'A' && p[0] <= 'Z'))
	if strings.Contains(p, `\`) || path.IsAbs(p) || drive || path.Clean(p) != p || p == "." || p == ".." || strings.HasPrefix(p, "../") {
		return "", fmt.Errorf("%s %q escapes the tree", name, p)
	}
	return filepath.Join(root, filepath.FromSlash(p)), nil
}

// parseTargets reads and parses the document given, if one was.
func (in TargetInputs) parseTargets(ctx context.Context) (targets []Target, given bool, err error) {
	switch {
	case in.TargetsPath != "":
		location := in.TargetsPath
		if in.TargetsRoot != "" {
			if location, err = ConfineToTree("targets_path", in.TargetsRoot, in.TargetsPath); err != nil {
				return nil, true, err
			}
		}
		data, err := readFileContext(ctx, location)
		if err != nil {
			return nil, true, err
		}
		targets, err = LoadTargetsContext(ctx, data)
		return targets, true, err
	case in.TargetsJSON != nil:
		targets, err = LoadTargetsContext(ctx, in.TargetsJSON)
		return targets, true, err
	}
	return nil, false, nil
}

// readChanged reads the changed ref's surface, if a ref was given.
func (in TargetInputs) readChanged(ctx context.Context) (*ChangedSelection, error) {
	if in.Changed == nil {
		return nil, nil
	}
	return in.Changed(ctx)
}

// PrepareSelection is the preparation of a verb that takes no campaign
// lock (discovery): the refusals its inputs decide, in the enumerated
// order — the sources' exclusivity (sources in the face's spelling),
// the document's parse, the tree root's existence, then the ref's
// surface read in that root — into the request the dispatch resolves
// after the load (REQ-exec-preparation). A discovery renders no cut.
func PrepareSelection(ctx context.Context, root string, sources []string, in TargetInputs, packages, symbols []string) (SelectionRequest, error) {
	request := SelectionRequest{Packages: packages, Symbols: symbols}
	if err := ValidateTargetSources(sources); err != nil {
		return request, err
	}
	var err error
	if request.Targets, request.TargetsGiven, err = in.parseTargets(ctx); err != nil {
		return request, err
	}
	if err := treeRootExists(root); err != nil {
		return request, err
	}
	request.Changed, err = in.readChanged(ctx)
	return request, err
}

// SelectionRequest is a resolved target-source request: the parsed
// targets document when one was given (an empty document included),
// the changed ref's selection when a ref was, and the filters in the
// face's own spellings. Cut asks a changed-ref selection for its
// survivor cut — the geometry a face cuts open survivors by, data
// derived from the surface; a plan renders no cut (REQ-exec-plan-only's
// rows are decisions, not cut rows).
type SelectionRequest struct {
	Targets      []Target
	TargetsGiven bool
	Changed      *ChangedSelection
	Cut          bool
	Packages     []string
	Symbols      []string
}

// TargetSelection is a resolved selection: the targets after the
// filters, the changed-scope residue, the survivor cut when asked for,
// and whether the selection is the whole tree — no source
// and no filter — the form whose final write reconciles the document
// (REQ-result-hygiene).
type TargetSelection struct {
	Targets   []Target
	Residue   []Residue
	Cut       *DeltaCut
	WholeTree bool
}

// SelectTargets is the one target-source dispatch every face's run and
// discover verbs share: the targets document, the changed ref's surface
// (the target set and the cut from the one surface, REQ-exec-run-status),
// or the whole tree, then the filter walk — whose empty-selection
// discrimination lives in the library, so a face's zero-target note
// names the true emptier (REQ-target-filtering, REQ-mcp-envelope).
func (t *Tree) SelectTargets(ctx context.Context, req SelectionRequest) (TargetSelection, error) {
	var sel TargetSelection
	var err error
	switch {
	case req.TargetsGiven:
		sel.Targets = req.Targets
	case req.Changed != nil:
		if req.Changed.Ref == nil {
			return sel, errors.New("gomutant: changed selection without its content reader")
		}
		var delta DeltaCut
		if sel.Targets, sel.Residue, delta, err = t.DiscoverChangedSurfaceContext(ctx, req.Changed.Surface, req.Changed.Ref); err != nil {
			return sel, err
		}
		if req.Cut {
			sel.Cut = &delta
		}
	default:
		if sel.Targets, err = t.DiscoverContext(ctx); err != nil {
			return sel, err
		}
		sel.WholeTree = true
	}
	if sel.Targets, err = t.FilterTargets(ctx, sel.Targets, req.Packages, req.Symbols); err != nil {
		return sel, err
	}
	if len(req.Packages) != 0 || len(req.Symbols) != 0 {
		sel.WholeTree = false
	}
	return sel, nil
}
