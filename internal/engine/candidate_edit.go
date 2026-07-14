package engine

import (
	"context"
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
	operator                  string
	start, end                token.Pos
	position                  token.Pos
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
		p := pkg.Fset.Position(positionPos)
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

func (t *Tree) materializeCandidates(ctx context.Context, catalog *catalog, symbol string, specs []candidateSpec, positions []string) ([]Candidate, error) {
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
	renderedSeen := map[string]bool{}
	effectiveSeen := map[string]bool{string(baseline): true}
	candidates := make([]Candidate, 0, len(specs))
	for i, spec := range specs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		candidate := Candidate{Symbol: symbol, Operator: spec.operator, Position: positions[i]}
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
