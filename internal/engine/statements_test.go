package engine

import (
	"context"
	"strings"
	"testing"
)

func TestStatementCatalogContexts(t *testing.T) {
	tr := fixtureTree(t)
	for _, test := range []struct {
		symbol, operator string
		want             int
	}{
		{"StatementBlocks", "block: empty", 7},
		{"StatementBlocks", "statement: delete", 13},
		{"StatementKinds", "statement: delete", 6},
		{"StatementDropStores", "assignment: drop store", 6},
		{"StatementDropStores", "statement: delete", 4},
		{"StatementAssignmentBoundaries", "assignment: drop store", 3},
		{"StatementExcluded", "statement: delete", 1},
		{"StatementDeletionKinds", "statement: delete", 6},
	} {
		generation, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib."+test.symbol, 0)
		if err != nil {
			t.Fatal(err)
		}
		got := 0
		var positions []string
		for _, candidate := range generation.Candidates {
			if candidate.Operator == test.operator {
				got++
				positions = append(positions, candidate.Position)
			}
		}
		if got != test.want {
			t.Errorf("%s %s candidates = %d, want %d: %v", test.symbol, test.operator, got, test.want, positions)
		}
	}

	generation, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib.StatementExcluded", 0)
	if err != nil {
		t.Fatal(err)
	}
	deletions := 0
	for _, candidate := range generation.Candidates {
		if candidate.Operator == "assignment: drop store" {
			t.Errorf("short assignment emitted %s", candidate.Operator)
		}
		if candidate.Operator == "statement: delete" {
			deletions++
		}
	}
	if deletions != 1 {
		t.Errorf("StatementExcluded deletions = %d, want only the return statement", deletions)
	}
	generation, err = tr.CandidatesContext(context.Background(), "example.com/fixture/lib.StatementLabeledAssignment", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range generation.Candidates {
		if candidate.Operator == "assignment: drop store" {
			t.Error("labeled assignment emitted assignment: drop store")
		}
	}
}

func TestStatementSourcesRanksAndSpans(t *testing.T) {
	tr := fixtureTree(t)
	generation, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib.StatementDropStores", 0)
	if err != nil {
		t.Fatal(err)
	}
	foundMultiple, foundCompound := false, false
	for _, candidate := range generation.Candidates {
		if candidate.Operator != "assignment: drop store" || len(candidate.Replacements) != 1 {
			continue
		}
		source := string(candidate.Replacements[0].Source)
		if strings.Contains(source, "_, _ = next(), next()") {
			foundMultiple = true
		}
		if strings.Contains(source, "_ = next()") && !strings.Contains(source, "values[next()] += next()") {
			foundCompound = true
		}
	}
	if !foundMultiple || !foundCompound {
		t.Fatalf("drop-store sources: multiple=%v compound=%v", foundMultiple, foundCompound)
	}

	for _, test := range []struct {
		symbol, operator, span string
		family                 int
	}{
		{"StatementBlocks", "block: empty", "{\n\t\tsink()\n\t\tnumber++\n\t}", 29},
		{"StatementKinds", "statement: delete", "sink()", 30},
		{"StatementDropStores", "assignment: drop store", "left, right = next(), next()", 31},
	} {
		catalog, err := tr.candidateCatalog(context.Background(), "example.com/fixture/lib."+test.symbol)
		if err != nil {
			t.Fatal(err)
		}
		specs, err := catalog.enumerate(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, spec := range specs {
			if spec.operator == test.operator && string(catalog.text(spec.start, spec.end)) == test.span {
				found = true
				if spec.family != test.family || spec.variant != 1 {
					t.Errorf("%s rank = %d/%d, want %d/1", test.operator, spec.family, spec.variant, test.family)
				}
			}
		}
		if !found {
			t.Errorf("%s span %q missing", test.operator, test.span)
		}
	}
}
