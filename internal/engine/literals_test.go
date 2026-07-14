package engine

import (
	"context"
	"strings"
	"testing"
)

func TestScalarLiteralCatalog(t *testing.T) {
	tr := fixtureTree(t)
	for _, test := range []struct {
		symbol string
		want   map[string]int
	}{
		{"IntegerLiteralForms", map[string]int{"integer literal: magnitude +1": 7}},
		{"RuneLiteralForms", map[string]int{"rune literal: value +1": 6}},
		{"FloatLiteralForms", map[string]int{"float literal: value +1": 3}},
		{"ImaginaryLiteralForms", map[string]int{"imaginary literal: value +1": 2}},
		{"BooleanLiteralForms", map[string]int{"boolean literal: true -> false": 1, "boolean literal: false -> true": 1}},
		{"StringLiteralForms", map[string]int{"string literal: nonempty -> empty": 3, "string literal: empty -> nonempty": 2}},
	} {
		generation, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib."+test.symbol, 0)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]int{}
		for _, candidate := range generation.Candidates {
			if strings.Contains(candidate.Operator, "literal:") {
				got[candidate.Operator]++
			}
		}
		assertOperatorCounts(t, test.symbol, got, test.want)
	}

	for _, symbol := range []string{"ShadowedBooleanLiterals", "ShadowedBooleanSelector"} {
		generation, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib."+symbol, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, candidate := range generation.Candidates {
			if strings.HasPrefix(candidate.Operator, "boolean literal:") {
				t.Errorf("%s shadowed boolean emitted %s", symbol, candidate.Operator)
			}
		}
	}
}

func TestScalarLiteralMappingSourcesAndRanks(t *testing.T) {
	tr := fixtureTree(t)
	const source = "package lib\n\nfunc LiteralInteger() int          { return 0x0f }\nfunc LiteralRune() rune            { return '\\x61' }\nfunc LiteralFloat() float64        { return 1e2 }\nfunc LiteralImaginary() complex128 { return 2i }\nfunc LiteralTrue() bool            { return true }\nfunc LiteralFalse() bool           { return false }\nfunc LiteralNonempty() string      { return `value` }\nfunc LiteralEmpty() string         { return \"\" }\n"
	for _, test := range []struct {
		symbol, operator, old, replacement string
		family                             int
	}{
		{"LiteralInteger", "integer literal: magnitude +1", "return 0x0f", "return 16", 15},
		{"LiteralRune", "rune literal: value +1", "return '\\x61'", "return 'b'", 16},
		{"LiteralFloat", "float literal: value +1", "return 1e2", "return (1e2 + 1.0)", 17},
		{"LiteralImaginary", "imaginary literal: value +1", "return 2i", "return (2i + 1i)", 18},
		{"LiteralTrue", "boolean literal: true -> false", "return true", "return false", 19},
		{"LiteralFalse", "boolean literal: false -> true", "return false", "return true", 20},
		{"LiteralNonempty", "string literal: nonempty -> empty", "return `value`", "return \"\"", 21},
		{"LiteralEmpty", "string literal: empty -> nonempty", "return \"\"", "return \"mutant\"", 22},
	} {
		generation, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib."+test.symbol, 0)
		if err != nil {
			t.Fatal(err)
		}
		wantSource := strings.Replace(source, test.old, test.replacement, 1)
		found := false
		for _, candidate := range generation.Candidates {
			if candidate.Operator != test.operator {
				continue
			}
			found = true
			if len(candidate.Replacements) != 1 || string(candidate.Replacements[0].Source) != wantSource {
				t.Errorf("%s source = %q, want %q", test.operator, candidate.Replacements, wantSource)
			}
			if test.symbol == "LiteralInteger" && candidate.Position != "literal_mappings.go:3:45" {
				t.Errorf("integer literal position = %q, want literal_mappings.go:3:45", candidate.Position)
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
			if spec.operator == test.operator && (spec.family != test.family || spec.variant != 1 || string(catalog.text(spec.start, spec.end)) != strings.TrimPrefix(test.old, "return ")) {
				t.Errorf("%s rank/span = %d/%d %q", test.operator, spec.family, spec.variant, catalog.text(spec.start, spec.end))
			}
		}
	}
}

func TestIntegerLiteralArbitraryPrecisionAndLexicalSign(t *testing.T) {
	tr := fixtureTree(t)
	generation, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib.IntegerLiteralForms", 0)
	if err != nil {
		t.Fatal(err)
	}
	sources := make([]string, 0, len(generation.Candidates))
	for _, candidate := range generation.Candidates {
		if candidate.Operator == "integer literal: magnitude +1" && len(candidate.Replacements) == 1 {
			sources = append(sources, string(candidate.Replacements[0].Source))
		}
	}
	joined := strings.Join(sources, "\n")
	if !strings.Contains(joined, "const huge = 184467440737095516161") {
		t.Error("arbitrary-precision integer increment missing")
	}
	if !strings.Contains(joined, "const negative = -2") {
		t.Error("lexical mutation of -1 did not produce -2")
	}
}

func TestRuneLiteralByteEscapeUsesRuneValue(t *testing.T) {
	tr := fixtureTree(t)
	generation, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib.RuneLiteralForms", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range generation.Candidates {
		if candidate.Operator == "rune literal: value +1" && len(candidate.Replacements) == 1 && strings.Contains(string(candidate.Replacements[0].Source), "const byteEscape = 'Ā'") {
			return
		}
	}
	t.Fatal(`byte-escaped rune '\xff' did not increment from 255 to U+0100`)
}
