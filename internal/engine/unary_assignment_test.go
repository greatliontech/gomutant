package engine

import (
	"context"
	"strings"
	"testing"
)

func TestUnaryAssignmentCatalog(t *testing.T) {
	tr := fixtureTree(t)
	want := map[string]int{
		"unary: + -> -": 4, "unary: - -> +": 4,
		"unary: ! -> identity": 4, "unary: ^ -> identity": 4,
	}
	got := map[string]int{}
	for _, symbol := range []string{"UnaryDefined", "UnaryGenericNumeric", "UnaryGenericInteger", "UnaryGenericBoolean", "UnaryConstants", "UnaryAliases"} {
		generation, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib."+symbol, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, candidate := range generation.Candidates {
			if strings.HasPrefix(candidate.Operator, "unary:") {
				got[candidate.Operator]++
			}
		}
	}
	assertOperatorCounts(t, "unary", got, want)
	generation, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib.UnaryExcluded", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range generation.Candidates {
		if strings.HasPrefix(candidate.Operator, "unary:") {
			t.Errorf("excluded unary site emitted %s", candidate.Operator)
		}
	}

	got = map[string]int{}
	for _, symbol := range []string{"CompoundDefined", "CompoundGeneric", "CompoundAliases", "CompoundMixedAddition", "CompoundShiftIncompatible", "CompoundShiftLarge"} {
		generation, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib."+symbol, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, candidate := range generation.Candidates {
			if strings.HasPrefix(candidate.Operator, "compound ") {
				got[candidate.Operator]++
			}
		}
	}
	want = map[string]int{
		"compound arithmetic: += -> -=": 3, "compound arithmetic: -= -> +=": 3,
		"compound arithmetic: *= -> /=": 3, "compound arithmetic: /= -> *=": 3,
		"compound arithmetic: %= -> *=": 3,
		"compound bitwise: &= -> |=":    3, "compound bitwise: |= -> &=": 3,
		"compound bitwise: ^= -> &=": 3, "compound bitwise: &^= -> &=": 3,
		"compound shift: <<= -> >>=": 5, "compound shift: >>= -> <<=": 3,
		"compound store: += -> =": 4, "compound store: -= -> =": 3,
		"compound store: *= -> =": 3, "compound store: /= -> =": 3,
		"compound store: %= -> =": 3, "compound store: &= -> =": 3,
		"compound store: |= -> =": 3, "compound store: ^= -> =": 3,
		"compound store: &^= -> =": 3, "compound store: <<= -> =": 3,
		"compound store: >>= -> =": 3,
	}
	assertOperatorCounts(t, "compound", got, want)
}

func assertOperatorCounts(t *testing.T, family string, got, want map[string]int) {
	t.Helper()
	for operator, count := range want {
		if got[operator] != count {
			t.Errorf("%s candidates = %d, want %d", operator, got[operator], count)
		}
	}
	if len(got) != len(want) {
		t.Errorf("%s operators = %v, want %v", family, got, want)
	}
}

func TestUnaryAssignmentMappingSourcesAndRanks(t *testing.T) {
	tr := fixtureTree(t)
	tests := []struct {
		symbol, operator, old, replacement string
		family, variant                    int
	}{
		{"UnaryPlus", "unary: + -> -", "return +value", "return -value", 8, 1},
		{"UnaryMinus", "unary: - -> +", "return -value", "return +value", 8, 2},
		{"UnaryNot", "unary: ! -> identity", "return !value", "return value", 8, 3},
		{"UnaryXor", "unary: ^ -> identity", "return ^value", "return value", 8, 4},
		{"CompoundAdd", "compound arithmetic: += -> -=", "value += right", "value -= right", 9, 1},
		{"CompoundSub", "compound arithmetic: -= -> +=", "value -= right", "value += right", 9, 2},
		{"CompoundMul", "compound arithmetic: *= -> /=", "value *= right", "value /= right", 9, 3},
		{"CompoundDiv", "compound arithmetic: /= -> *=", "value /= right", "value *= right", 9, 4},
		{"CompoundRem", "compound arithmetic: %= -> *=", "value %= right", "value *= right", 9, 5},
		{"CompoundAnd", "compound bitwise: &= -> |=", "value &= right", "value |= right", 10, 1},
		{"CompoundOr", "compound bitwise: |= -> &=", "value |= right", "value &= right", 10, 2},
		{"CompoundXor", "compound bitwise: ^= -> &=", "value ^= right", "value &= right", 10, 3},
		{"CompoundClear", "compound bitwise: &^= -> &=", "value &^= right", "value &= right", 10, 4},
		{"CompoundShiftLeft", "compound shift: <<= -> >>=", "value <<= right", "value >>= right", 11, 1},
		{"CompoundShiftRight", "compound shift: >>= -> <<=", "value >>= right", "value <<= right", 11, 2},
		{"CompoundAdd", "compound store: += -> =", "value += right", "value = right", 12, 1},
		{"CompoundSub", "compound store: -= -> =", "value -= right", "value = right", 12, 2},
		{"CompoundMul", "compound store: *= -> =", "value *= right", "value = right", 12, 3},
		{"CompoundDiv", "compound store: /= -> =", "value /= right", "value = right", 12, 4},
		{"CompoundRem", "compound store: %= -> =", "value %= right", "value = right", 12, 5},
		{"CompoundAnd", "compound store: &= -> =", "value &= right", "value = right", 12, 6},
		{"CompoundOr", "compound store: |= -> =", "value |= right", "value = right", 12, 7},
		{"CompoundXor", "compound store: ^= -> =", "value ^= right", "value = right", 12, 8},
		{"CompoundClear", "compound store: &^= -> =", "value &^= right", "value = right", 12, 9},
		{"CompoundShiftLeft", "compound store: <<= -> =", "value <<= right", "value = right", 12, 10},
		{"CompoundShiftRight", "compound store: >>= -> =", "value >>= right", "value = right", 12, 11},
		{"Increment", "increment/decrement: ++ -> --", "value++", "value--", 13, 1},
		{"Decrement", "increment/decrement: -- -> ++", "value--", "value++", 13, 2},
	}
	for _, test := range tests {
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
			if spec.operator != test.operator {
				continue
			}
			found = true
			if spec.family != test.family || spec.variant != test.variant {
				t.Errorf("%s rank = %d/%d, want %d/%d", test.operator, spec.family, spec.variant, test.family, test.variant)
			}
			if strings.HasPrefix(test.operator, "unary:") {
				wantSpan := strings.TrimPrefix(test.old, "return ")
				if span := string(catalog.text(spec.start, spec.end)); span != wantSpan {
					t.Errorf("%s ordering span = %q, want %q", test.operator, span, wantSpan)
				}
			}
			generation, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib."+test.symbol, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, candidate := range generation.Candidates {
				if candidate.Operator != test.operator {
					continue
				}
				if len(candidate.Replacements) != 1 || !strings.Contains(string(candidate.Replacements[0].Source), test.replacement) || strings.Contains(string(candidate.Replacements[0].Source), test.old) {
					t.Errorf("%s source = %q", test.operator, candidate.Replacements)
				}
				if test.symbol == "UnaryPlus" && candidate.Position != "unary_assignment_mappings.go:3:41" {
					t.Errorf("unary position = %q, want operator at unary_assignment_mappings.go:3:41", candidate.Position)
				}
			}
		}
		if !found {
			t.Errorf("%s did not emit %s", test.symbol, test.operator)
		}
	}
}

func TestCompoundAssignmentPlaces(t *testing.T) {
	tr := fixtureTree(t)
	got := map[string]int{}
	for _, symbol := range []string{"CompoundPlaces", "CompoundHolder.add"} {
		generation, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib."+symbol, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, candidate := range generation.Candidates {
			if candidate.Operator == "compound arithmetic: += -> -=" || candidate.Operator == "compound store: += -> =" {
				got[candidate.Operator]++
			}
		}
	}
	for _, operator := range []string{"compound arithmetic: += -> -=", "compound store: += -> ="} {
		if got[operator] != 4 {
			t.Errorf("%s selector/index/dereference/method candidates = %d, want 4", operator, got[operator])
		}
	}
}
