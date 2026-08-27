package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/format"
	"go/token"
	"path/filepath"
	"sort"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/imports"
)

type sourceEdit struct {
	start, end  int
	replacement []byte
}

type candidateSpec struct {
	operator   string
	start, end token.Pos
	position   token.Pos
	// extentStart/extentEnd, when set, override start/end as the
	// EXECUTION extent the coverage probe intersects — for operators
	// whose ordering anchor (the whole statement) is wider than the
	// region the mutation touches (range-body suppression: the break
	// empties the BODY; a header-only execution must not read as
	// executing it). Zero values fall back to start/end. Ordering
	// never reads these.
	extentStart, extentEnd    token.Pos
	family, variant, index    int
	edits                     []sourceEdit
	preservesImportReferences bool
}

func (c candidateSpec) less(other candidateSpec) bool {
	if c.start != other.start {
		return c.start < other.start
	}
	if c.end != other.end {
		return c.end < other.end
	}
	if c.family != other.family {
		return c.family < other.family
	}
	if c.variant != other.variant {
		return c.variant < other.variant
	}
	return c.index < other.index
}

func (c candidateSpec) apply(source []byte) ([]byte, error) {
	return applySourceEdits(source, c.edits)
}

func orderCandidateSpecs(specs []candidateSpec) {
	sort.SliceStable(specs, func(i, j int) bool {
		return specs[i].less(specs[j])
	})
}

func candidatePositions(pkg *packages.Package, specs []candidateSpec) []string {
	positions := make([]string, len(specs))
	identities := map[string]int{}
	for i, spec := range specs {
		positionPos := spec.position
		if !positionPos.IsValid() {
			positionPos = spec.start
		}
		p := pkg.Fset.PositionFor(positionPos, false)
		position := fmt.Sprintf("%s:%d:%d", filepath.Base(p.Filename), p.Line, p.Column)
		identity := position + "|" + spec.operator
		identities[identity]++
		if identities[identity] > 1 {
			position += fmt.Sprintf("#%d", identities[identity])
		}
		positions[i] = position
	}
	return positions
}

// candidateExtents renders each spec's mutated-node range as
// "line:col-line:col" (half-open, go/token End semantics) — the
// coverage-intersection geometry candidatePositions' single anchor
// cannot carry.
func candidateExtents(pkg *packages.Package, specs []candidateSpec) []string {
	extents := make([]string, len(specs))
	for i, spec := range specs {
		start, end := spec.start, spec.end
		// Each side overrides independently: a half-set override is
		// likelier a narrowed side than an intent to discard both.
		if spec.extentStart.IsValid() {
			start = spec.extentStart
		}
		if spec.extentEnd.IsValid() {
			end = spec.extentEnd
		}
		if !start.IsValid() || !end.IsValid() {
			continue
		}
		s := pkg.Fset.PositionFor(start, false)
		e := pkg.Fset.PositionFor(end, false)
		extents[i] = fmt.Sprintf("%d:%d-%d:%d", s.Line, s.Column, e.Line, e.Column)
	}
	return extents
}

// candidateSites hashes each spec's site window into the attestation
// anchor's site component: the mutated byte range extended to full line
// bounds plus one full line above and below, from the original source.
// An attestation's equivalence reasoning is site-specific, and the
// window - not the bare snippet - keeps two same-shaped expressions
// with different neighbors apart, so a neighbor shifted into the old
// coordinates by an edit never inherits a disposition
// (REQ-attest-survivor).
func candidateSites(source []byte, specs []candidateSpec) []string {
	sites := make([]string, len(specs))
	for i, spec := range specs {
		start, end := len(source), 0
		for _, e := range spec.edits {
			if e.start < start {
				start = e.start
			}
			if e.end > end {
				end = e.end
			}
		}
		if start > end {
			start, end = 0, 0
		}
		sites[i] = siteHash(source, start, end)
	}
	return sites
}

func siteHash(source []byte, start, end int) string {
	clamp := func(v int) int {
		if v < 0 {
			return 0
		}
		if v > len(source) {
			return len(source)
		}
		return v
	}
	start, end = clamp(start), clamp(end)
	lineStart := func(off int) int {
		return bytes.LastIndexByte(source[:off], '\n') + 1
	}
	lineEnd := func(off int) int {
		i := bytes.IndexByte(source[off:], '\n')
		if i < 0 {
			return len(source)
		}
		return off + i + 1
	}
	ws := lineStart(start)
	if ws > 0 {
		ws = lineStart(ws - 1)
	}
	we := lineEnd(end)
	if we < len(source) {
		we = lineEnd(we)
	}
	sum := sha256.Sum256(source[ws:we])
	return hex.EncodeToString(sum[:8])
}

func applySourceEdits(source []byte, edits []sourceEdit) ([]byte, error) {
	if len(edits) == 0 {
		return nil, fmt.Errorf("candidate has no source edits")
	}
	edits = append([]sourceEdit(nil), edits...)
	sort.SliceStable(edits, func(i, j int) bool {
		if edits[i].start != edits[j].start {
			return edits[i].start < edits[j].start
		}
		return edits[i].end < edits[j].end
	})
	previousEnd := 0
	for i, edit := range edits {
		if edit.start < 0 || edit.end < edit.start || edit.end > len(source) {
			return nil, fmt.Errorf("edit %d range [%d,%d) outside source", i, edit.start, edit.end)
		}
		if i > 0 && edit.start < previousEnd {
			return nil, fmt.Errorf("edit %d overlaps its predecessor", i)
		}
		previousEnd = edit.end
	}
	mutated := append([]byte(nil), source...)
	for i := len(edits) - 1; i >= 0; i-- {
		edit := edits[i]
		next := make([]byte, 0, len(mutated)-(edit.end-edit.start)+len(edit.replacement))
		next = append(next, mutated[:edit.start]...)
		next = append(next, edit.replacement...)
		next = append(next, mutated[edit.end:]...)
		mutated = next
	}
	return mutated, nil
}

func (t *Tree) materializeCandidates(ctx context.Context, catalog *catalog, symbol string, specs []candidateSpec, positions, sites []string) ([]Candidate, error) {
	baseline, err := format.Source(catalog.source)
	if err != nil {
		return nil, fmt.Errorf("format baseline: %w", err)
	}
	processImports := t.importProcessor
	if processImports == nil {
		processImports = func(_ context.Context, filename string, source []byte) ([]byte, error) {
			return imports.Process(filename, source, nil)
		}
	}
	extents := candidateExtents(catalog.pkg, specs)
	renderedSeen := map[string]bool{}
	effectiveSeen := map[string]bool{string(baseline): true}
	candidates := make([]Candidate, 0, len(specs))
	for i, spec := range specs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		candidate := Candidate{Symbol: symbol, Operator: spec.operator, Position: positions[i], Site: sites[i], Extent: extents[i]}
		mutated, err := spec.apply(catalog.source)
		if err != nil {
			return nil, fmt.Errorf("candidate %s %s: %w", candidate.Position, candidate.Operator, err)
		}
		mutated, err = format.Source(mutated)
		if err != nil {
			return nil, fmt.Errorf("format candidate %s %s: %w", candidate.Position, candidate.Operator, err)
		}
		if renderedSeen[string(mutated)] {
			candidates = append(candidates, candidate)
			continue
		}
		renderedSeen[string(mutated)] = true
		if !spec.preservesImportReferences {
			mutated, err = processImports(ctx, catalog.path, mutated)
			if cancelErr := ctx.Err(); cancelErr != nil {
				return nil, cancelErr
			}
			if err != nil {
				return nil, fmt.Errorf("normalize candidate %s %s: %w", candidate.Position, candidate.Operator, err)
			}
			mutated, err = format.Source(mutated)
			if err != nil {
				return nil, fmt.Errorf("format normalized candidate %s %s: %w", candidate.Position, candidate.Operator, err)
			}
		}
		if effectiveSeen[string(mutated)] {
			candidates = append(candidates, candidate)
			continue
		}
		effectiveSeen[string(mutated)] = true
		candidate.Replacements = []Replacement{{File: catalog.path, Source: mutated}}
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}
