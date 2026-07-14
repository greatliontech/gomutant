package engine

import (
	"context"
	"strings"
	"testing"
)

func TestReturnSubstitutionCatalog(t *testing.T) {
	tr := fixtureTree(t)
	for _, test := range []struct {
		symbol string
		want   map[string]int
	}{
		{"ReturnBoolean", map[string]int{"return: false": 1, "return: true": 1}},
		{"ReturnNumber", map[string]int{"return: zero": 1}},
		{"ReturnString", map[string]int{"return: zero": 1}},
		{"ReturnPointer", map[string]int{"return: nil": 1}},
		{"ReturnDefined", map[string]int{"return: false": 1, "return: true": 1, "return: zero": 4, "return: nil": 1}},
		{"ReturnAliases", map[string]int{"return: zero": 1, "return: nil": 1}},
		{"ReturnInstantiatedAlias", map[string]int{"return: nil": 1}},
		{"ReturnNilDomains", map[string]int{"return: nil": 7}},
		{"ReturnDefinedNilDomains", map[string]int{"return: nil": 6}},
		{"ReturnMultiple", map[string]int{"return: false": 1, "return: true": 1, "return: zero": 1, "return: nil": 1}},
		{"ReturnDeclaredInterface", map[string]int{"return: nil": 1}},
		{"ReturnNested", map[string]int{"return: false": 1, "return: true": 1, "return: zero": 1}},
		{"ReturnDeepNested", map[string]int{"return: false": 1, "return: true": 1, "return: zero": 2}},
		{"ReturnGrouped", map[string]int{"return: zero": 2}},
		{"ReturnSingleCall", map[string]int{"return: zero": 1}},
		{"ReturnParenthesized", map[string]int{"return: zero": 1}},
		{"ReturnReceiver.Value", map[string]int{"return: false": 1, "return: true": 1}},
	} {
		generation, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib."+test.symbol, 0)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]int{}
		for _, candidate := range generation.Candidates {
			if strings.HasPrefix(candidate.Operator, "return:") {
				got[candidate.Operator]++
			}
		}
		assertOperatorCounts(t, test.symbol, got, test.want)
	}
	for _, symbol := range []string{"ReturnArray", "ReturnStruct", "ReturnTypeParameter", "ReturnBare", "ReturnTuple"} {
		generation, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib."+symbol, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, candidate := range generation.Candidates {
			if strings.HasPrefix(candidate.Operator, "return:") {
				t.Errorf("%s emitted excluded %s", symbol, candidate.Operator)
			}
		}
	}
}

func TestReturnSubstitutionSourcesRanksAndSiblings(t *testing.T) {
	tr := fixtureTree(t)
	const source = "package lib\n\nfunc ReturnBoolean(value bool) bool { return value }\nfunc ReturnNumber(value int) int    { return value }\nfunc ReturnString(value string) string {\n\treturn value\n}\nfunc ReturnPointer(value *int) *int { return value }\n"
	for _, test := range []struct {
		symbol, operator, old, replacement string
		family                             int
	}{
		{"ReturnBoolean", "return: false", "return value", "return false", 32},
		{"ReturnBoolean", "return: true", "return value", "return true", 33},
		{"ReturnNumber", "return: zero", "func ReturnNumber(value int) int    { return value }", "func ReturnNumber(value int) int    { return 0 }", 34},
		{"ReturnString", "return: zero", "\treturn value\n", "\treturn \"\"\n", 34},
		{"ReturnPointer", "return: nil", "func ReturnPointer(value *int) *int { return value }", "func ReturnPointer(value *int) *int { return nil }", 35},
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
				if test.symbol == "ReturnBoolean" && test.operator == "return: false" && candidate.Position != "return_mappings.go:3:46" {
					t.Errorf("return position = %q, want return_mappings.go:3:46", candidate.Position)
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
			if spec.operator == test.operator && (spec.family != test.family || spec.variant != 1 || string(catalog.text(spec.start, spec.end)) != "value") {
				t.Errorf("%s rank/span = %d/%d %q", test.operator, spec.family, spec.variant, catalog.text(spec.start, spec.end))
			}
		}
	}

	generation, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib.ReturnMultiple", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range generation.Candidates {
		if candidate.Operator == "return: zero" && (len(candidate.Replacements) != 1 || !strings.Contains(string(candidate.Replacements[0].Source), "return flag, 0, pointer")) {
			t.Errorf("return sibling expressions changed: %q", candidate.Replacements)
		}
	}

	generation, err = tr.CandidatesContext(context.Background(), "example.com/fixture/lib.ReturnParenthesized", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range generation.Candidates {
		if candidate.Operator == "return: zero" {
			if len(candidate.Replacements) != 1 || !strings.Contains(string(candidate.Replacements[0].Source), "return 0") {
				t.Errorf("parenthesized return source = %q", candidate.Replacements)
			}
			catalog, err := tr.candidateCatalog(context.Background(), "example.com/fixture/lib.ReturnParenthesized")
			if err != nil {
				t.Fatal(err)
			}
			specs, err := catalog.enumerate(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			for _, spec := range specs {
				if spec.operator == "return: zero" && string(catalog.text(spec.start, spec.end)) != "(value)" {
					t.Errorf("parenthesized return span = %q", catalog.text(spec.start, spec.end))
				}
			}
		}
	}
}

func TestReturnSubstitutionPrunesOrphanedImport(t *testing.T) {
	tr := fixtureTree(t)
	generation, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib.ReturnImportedError", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range generation.Candidates {
		if candidate.Operator == "return: nil" && len(candidate.Replacements) == 1 {
			if got, want := string(candidate.Replacements[0].Source), "package lib\n\nfunc ReturnImportedError() error { return nil }\n"; got != want {
				t.Fatalf("import-pruned return source = %q, want %q", got, want)
			}
			return
		}
	}
	t.Fatal("return: nil candidate missing")
}
