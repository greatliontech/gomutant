package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/greatliontech/gofresh/shapecorpus"
)

// TestLanguageShapeCanaries runs the fleet's shared shape corpus
// (gofresh/shapecorpus) through candidate generation: each entry must
// load, enumerate, and generate AT LEAST ONE candidate — a frontend
// that silently skips an unrecognized shape (zero candidates, no
// error: the likeliest breakage for an AST walker) is exactly as red
// as one that errors. Runs under the CI matrix's next-rc leg like
// every test; the inline-interface parse failure cost one field
// session already.
func TestLanguageShapeCanaries(t *testing.T) {
	for _, entry := range shapecorpus.Entries() {
		t.Run(entry.Name, func(t *testing.T) {
			dir := t.TempDir()
			for file, content := range entry.TestFiles() {
				if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			tr, err := Load(dir)
			if err != nil {
				t.Errorf("canary load: %v", err)
				return
			}
			// Candidate generation is per-symbol: enumerating only the
			// Subject wrapper would never walk the body that carries the
			// entry's shape, so both symbols are exercised.
			symbols := []string{"example.com/shape.Subject"}
			if entry.ShapeSymbol != "Subject" {
				symbols = append(symbols, "example.com/shape."+entry.ShapeSymbol)
			}
			for _, symbol := range symbols {
				generation, err := tr.CandidatesContext(context.Background(), symbol, 0)
				if err != nil {
					t.Errorf("canary candidate generation over %s: %v", symbol, err)
					continue
				}
				if len(generation.Candidates) == 0 {
					t.Errorf("canary generated zero candidates over %s - a silently skipped shape reads exactly like a covered one", symbol)
				}
			}
		})
	}
}
