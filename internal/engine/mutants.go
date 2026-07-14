package engine

import (
	"context"
	"fmt"
)

// Replacement is one original file and its complete overlaid content.
type Replacement struct {
	File   string
	Source []byte
}

// Mutant is one mutation represented by every file replacement that must be
// visible atomically during its overlay run.
type Mutant struct {
	Symbol       string
	Operator     string
	Position     string
	Replacements []Replacement
}

// Candidate is one selected operator application before no-op and duplicate
// discards. An empty replacement set means the candidate was discarded before
// execution; its identity remains available for accounting.
type Candidate struct {
	Symbol       string
	Operator     string
	Position     string
	Replacements []Replacement
}

// Mutant returns the runnable mutation represented by c. Pre-execution
// discards have no replacement and return false.
func (c Candidate) Mutant() (Mutant, bool) {
	if len(c.Replacements) == 0 {
		return Mutant{}, false
	}
	return Mutant{Symbol: c.Symbol, Operator: c.Operator, Position: c.Position, Replacements: c.Replacements}, true
}

// Generation is the complete candidate count and the budget-selected prefix.
type Generation struct {
	CandidateCount int
	Candidates     []Candidate
}

// OperatorSet identifies the mutant-generation basis; finding records pin it,
// so changing candidate generation re-stales every prior record.
const OperatorSet = "go/10"

type importProcessor func(context.Context, string, []byte) ([]byte, error)

// Mutants returns the runnable subset of the selected candidate prefix.
func (t *Tree) Mutants(symbol string, budget int) ([]Mutant, error) {
	return t.MutantsContext(context.Background(), symbol, budget)
}

// MutantsContext is Mutants with cooperative cancellation.
func (t *Tree) MutantsContext(ctx context.Context, symbol string, budget int) ([]Mutant, error) {
	generation, err := t.CandidatesContext(ctx, symbol, budget)
	if err != nil {
		return nil, err
	}
	mutants := make([]Mutant, 0, len(generation.Candidates))
	for _, candidate := range generation.Candidates {
		if mutant, ok := candidate.Mutant(); ok {
			mutants = append(mutants, mutant)
		}
	}
	return mutants, nil
}

// CandidatesContext enumerates every applicable site before selecting and
// materializing the requested deterministic prefix.
func (t *Tree) CandidatesContext(ctx context.Context, symbol string, budget int) (Generation, error) {
	catalog, err := t.candidateCatalog(ctx, symbol)
	if err != nil {
		return Generation{}, err
	}
	specs, err := catalog.enumerate(ctx)
	if err != nil {
		return Generation{}, err
	}
	orderCandidateSpecs(specs)
	positions := candidatePositions(catalog.pkg, specs)
	selected := specs
	if budget > 0 && budget < len(selected) {
		selected = selected[:budget]
		positions = positions[:budget]
	}
	candidates, err := t.materializeCandidates(ctx, catalog, symbol, selected, positions)
	if err != nil {
		return Generation{}, fmt.Errorf("generate %s: %w", symbol, err)
	}
	return Generation{CandidateCount: len(specs), Candidates: candidates}, nil
}
