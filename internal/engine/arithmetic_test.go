package engine

import (
	"context"
	"strings"
	"testing"
)

func TestArithmeticCatalog(t *testing.T) {
	tr := fixtureTree(t)
	want := map[string]int{
		"arithmetic: + -> -": 4,
		"arithmetic: - -> +": 4,
		"arithmetic: * -> /": 4,
		"arithmetic: / -> *": 4,
		"arithmetic: % -> *": 2,
	}
	got := map[string]int{}
	for _, symbol := range []string{"ArithmeticDefined", "ArithmeticFloat", "ArithmeticComplex", "ArithmeticGeneric", "RemainderGeneric"} {
		generation, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib."+symbol, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, candidate := range generation.Candidates {
			if strings.HasPrefix(candidate.Operator, "arithmetic:") {
				got[candidate.Operator]++
			}
		}
	}
	for operator, count := range want {
		if got[operator] != count {
			t.Errorf("%s candidates = %d, want %d", operator, got[operator], count)
		}
	}
	for _, symbol := range []string{"MixedAddition", "StringAddition", "DefinedStringAddition"} {
		generation, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib."+symbol, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, candidate := range generation.Candidates {
			if strings.HasPrefix(candidate.Operator, "arithmetic:") {
				t.Errorf("%s emitted inapplicable %s", symbol, candidate.Operator)
			}
		}
	}
}

func TestArithmeticMappingSources(t *testing.T) {
	tr := fixtureTree(t)
	const source = "package lib\n\nfunc ArithmeticAdd(a, b int) int { return a + b }\nfunc ArithmeticSub(a, b int) int { return a - b }\nfunc ArithmeticMul(a, b int) int { return a * b }\nfunc ArithmeticDiv(a, b int) int { return a / b }\nfunc ArithmeticRem(a, b int) int { return a % b }\n"
	for _, test := range []struct {
		symbol, operator, old, replacement string
		variant                            int
	}{
		{"ArithmeticAdd", "arithmetic: + -> -", "return a + b", "return a - b", 1},
		{"ArithmeticSub", "arithmetic: - -> +", "return a - b", "return a + b", 2},
		{"ArithmeticMul", "arithmetic: * -> /", "return a * b", "return a / b", 3},
		{"ArithmeticDiv", "arithmetic: / -> *", "return a / b", "return a * b", 4},
		{"ArithmeticRem", "arithmetic: % -> *", "return a % b", "return a * b", 5},
	} {
		generation, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib."+test.symbol, 0)
		if err != nil {
			t.Fatal(err)
		}
		want := strings.Replace(source, test.old, test.replacement, 1)
		found := false
		for _, candidate := range generation.Candidates {
			if candidate.Operator == test.operator {
				found = true
				if len(candidate.Replacements) != 1 || string(candidate.Replacements[0].Source) != want {
					t.Errorf("%s source = %q, want %q", test.operator, candidate.Replacements, want)
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
			if spec.operator == test.operator && (spec.family != 5 || spec.variant != test.variant) {
				t.Errorf("%s rank = family %d variant %d, want 5/%d", test.operator, spec.family, spec.variant, test.variant)
			}
		}
	}
}

func TestArithmeticConstantAliasAndIntersectionDomains(t *testing.T) {
	tr := fixtureTree(t)
	for _, test := range []struct {
		symbol string
		want   map[string]int
	}{
		{"ArithmeticAlias", map[string]int{"arithmetic: + -> -": 1}},
		{"ArithmeticIntersected", map[string]int{"arithmetic: + -> -": 1, "arithmetic: - -> +": 1, "arithmetic: * -> /": 1, "arithmetic: / -> *": 1}},
		{"ArithmeticUntyped", map[string]int{"arithmetic: + -> -": 1}},
		{"ArithmeticIota", map[string]int{"arithmetic: + -> -": 1}},
		{"ArithmeticImaginary", map[string]int{"arithmetic: + -> -": 1}},
	} {
		generation, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib."+test.symbol, 0)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]int{}
		for _, candidate := range generation.Candidates {
			if strings.HasPrefix(candidate.Operator, "arithmetic:") {
				got[candidate.Operator]++
			}
		}
		for operator, count := range test.want {
			if got[operator] != count {
				t.Errorf("%s %s candidates = %d, want %d", test.symbol, operator, got[operator], count)
			}
		}
		if len(got) != len(test.want) {
			t.Errorf("%s arithmetic candidates = %v, want %v", test.symbol, got, test.want)
		}
		if test.symbol == "ArithmeticIota" {
			found := false
			for _, candidate := range generation.Candidates {
				if candidate.Operator == "arithmetic: + -> -" && len(candidate.Replacements) == 1 && strings.Contains(string(candidate.Replacements[0].Source), "const value = iota - 1") {
					found = true
				}
			}
			if !found {
				t.Error("iota subtraction source missing")
			}
		}
	}
}
