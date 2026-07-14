package engine

import (
	"context"
	"strings"
	"testing"
)

func TestBitwiseCatalog(t *testing.T) {
	tr := fixtureTree(t)
	want := map[string]int{
		"bitwise: & -> |":  4,
		"bitwise: | -> &":  3,
		"bitwise: ^ -> &":  3,
		"bitwise: &^ -> &": 3,
		"shift: << -> >>":  3,
		"shift: >> -> <<":  3,
	}
	got := map[string]int{}
	for _, symbol := range []string{"BitwiseDefined", "BitwiseGeneric", "BitwiseConstants", "BitwiseAlias", "ShiftDefined", "ShiftGeneric", "ShiftConstants"} {
		generation, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib."+symbol, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, candidate := range generation.Candidates {
			if strings.HasPrefix(candidate.Operator, "bitwise:") || strings.HasPrefix(candidate.Operator, "shift:") {
				got[candidate.Operator]++
			}
		}
	}
	for operator, count := range want {
		if got[operator] != count {
			t.Errorf("%s candidates = %d, want %d", operator, got[operator], count)
		}
	}
	if len(got) != len(want) {
		t.Errorf("bitwise operators = %v, want %v", got, want)
	}
}

func TestBitwiseMappingSourcesAndRanks(t *testing.T) {
	tr := fixtureTree(t)
	const source = "package lib\n\nfunc BitwiseAnd(a, b int) int   { return a & b }\nfunc BitwiseOr(a, b int) int    { return a | b }\nfunc BitwiseXor(a, b int) int   { return a ^ b }\nfunc BitwiseClear(a, b int) int { return a &^ b }\nfunc ShiftLeft(a, b uint) uint  { return a << b }\nfunc ShiftRight(a, b uint) uint { return a >> b }\n"
	for _, test := range []struct {
		symbol, operator, old, replacement string
		family, variant                    int
	}{
		{"BitwiseAnd", "bitwise: & -> |", "return a & b", "return a | b", 6, 1},
		{"BitwiseOr", "bitwise: | -> &", "return a | b", "return a & b", 6, 2},
		{"BitwiseXor", "bitwise: ^ -> &", "return a ^ b", "return a & b", 6, 3},
		{"BitwiseClear", "bitwise: &^ -> &", "return a &^ b", "return a & b", 6, 4},
		{"ShiftLeft", "shift: << -> >>", "return a << b", "return a >> b", 7, 1},
		{"ShiftRight", "shift: >> -> <<", "return a >> b", "return a << b", 7, 2},
	} {
		generation, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib."+test.symbol, 0)
		if err != nil {
			t.Fatal(err)
		}
		wantSource := strings.Replace(source, test.old, test.replacement, 1)
		found := false
		for _, candidate := range generation.Candidates {
			if candidate.Operator == test.operator {
				found = true
				if len(candidate.Replacements) != 1 || string(candidate.Replacements[0].Source) != wantSource {
					t.Errorf("%s source = %q, want %q", test.operator, candidate.Replacements, wantSource)
				}
			}
		}
		if !found {
			t.Errorf("%s did not emit %s", test.symbol, test.operator)
		}
		catalog, err := tr.candidateCatalog(context.Background(), "example.com/fixture/lib."+test.symbol)
		if err != nil {
			t.Fatal(err)
		}
		specs, err := catalog.enumerate(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, spec := range specs {
			if spec.operator == test.operator && (spec.family != test.family || spec.variant != test.variant) {
				t.Errorf("%s rank = %d/%d, want %d/%d", test.operator, spec.family, spec.variant, test.family, test.variant)
			}
		}
	}
}
