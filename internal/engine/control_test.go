package engine

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestControlCatalog(t *testing.T) {
	tr := fixtureTree(t)
	assertOperators := func(symbol string, want []string) Generation {
		t.Helper()
		generation, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib."+symbol, 0)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, candidate := range generation.Candidates {
			if strings.HasPrefix(candidate.Operator, "condition:") || strings.HasPrefix(candidate.Operator, "loop control:") || strings.HasPrefix(candidate.Operator, "range body:") {
				got = append(got, candidate.Operator)
			}
		}
		if !slices.Equal(got, want) {
			t.Fatalf("%s control operators = %v, want %v", symbol, got, want)
		}
		return generation
	}

	ifCondition := assertOperators("IfCondition", []string{"condition: negate", "condition: force true", "condition: force false"})
	assertOperators("ForCondition", []string{"condition: negate", "condition: force false"})
	assertOperators("Forever", []string{"condition: force false", "loop control: break -> continue"})
	assertOperators("MissingCondition", []string{"condition: force false", "loop control: break -> continue"})
	assertOperators("SwitchTag", nil)

	const conditionsSource = "package lib\n\nfunc IfCondition(n int) bool {\n\tif n > 0 {\n\t\treturn true\n\t}\n\treturn false\n}\n\nfunc ForCondition(n int) int {\n\tfor n < 1 {\n\t\treturn 1\n\t}\n\treturn n\n}\n\nfunc Forever() int {\n\tn := 0\n\tfor {\n\t\tn++\n\t\tbreak\n\t}\n\treturn n\n}\n\nfunc MissingCondition() {\n\tfor i := 0; ; i++ {\n\t\tbreak\n\t}\n}\n\nfunc SwitchTag(n int) {\n\tswitch n > 0 {\n\tcase true:\n\t}\n}\n"
	conditionSources := map[string]string{
		"condition: negate":      strings.Replace(conditionsSource, "if n > 0", "if !(n > 0)", 1),
		"condition: force true":  strings.Replace(conditionsSource, "if n > 0", "if true", 1),
		"condition: force false": strings.Replace(conditionsSource, "if n > 0", "if false", 1),
	}
	for _, candidate := range ifCondition.Candidates {
		want, ok := conditionSources[candidate.Operator]
		if !ok {
			continue
		}
		if len(candidate.Replacements) != 1 || string(candidate.Replacements[0].Source) != want {
			t.Errorf("%s source = %q, want %q", candidate.Operator, candidate.Replacements, want)
		}
	}
	forCondition := assertOperators("ForCondition", []string{"condition: negate", "condition: force false"})
	for operator, replacement := range map[string]string{
		"condition: negate":      "for !(n < 1)",
		"condition: force false": "for false",
	} {
		found := false
		for _, candidate := range forCondition.Candidates {
			if candidate.Operator != operator {
				continue
			}
			found = true
			want := strings.Replace(conditionsSource, "for n < 1", replacement, 1)
			if len(candidate.Replacements) != 1 || string(candidate.Replacements[0].Source) != want {
				t.Errorf("%s source = %q, want %q", operator, candidate.Replacements, want)
			}
		}
		if !found {
			t.Errorf("ForCondition did not emit %s", operator)
		}
	}
	for _, test := range []struct {
		symbol, operator, old, replacement, position string
	}{
		{"Forever", "condition: force false", "for {", "for false {", "control_conditions.go:19:2"},
		{"MissingCondition", "condition: force false", "for i := 0; ; i++ {", "for i := 0; false; i++ {", "control_conditions.go:27:2"},
	} {
		generation := assertOperators(test.symbol, map[string][]string{
			"Forever":          {"condition: force false", "loop control: break -> continue"},
			"MissingCondition": {"condition: force false", "loop control: break -> continue"},
		}[test.symbol])
		found := false
		for _, candidate := range generation.Candidates {
			if candidate.Operator != test.operator {
				continue
			}
			found = true
			want := strings.Replace(conditionsSource, test.old, test.replacement, 1)
			if len(candidate.Replacements) != 1 || string(candidate.Replacements[0].Source) != want {
				t.Errorf("%s source = %q, want %q", test.symbol, candidate.Replacements, want)
			}
			if candidate.Position != test.position {
				t.Errorf("%s position = %s, want %s", test.symbol, candidate.Position, test.position)
			}
		}
		if !found {
			t.Errorf("%s force-false candidate missing", test.symbol)
		}
	}
}

func TestLoopControlLegality(t *testing.T) {
	tr := fixtureTree(t)
	for _, test := range []struct {
		symbol, operator string
		want             int
	}{
		{"BreakLoop", "loop control: break -> continue", 1},
		{"BreakSwitch", "loop control: break -> continue", 0},
		{"BreakSwitchInLoop", "loop control: break -> continue", 2},
		{"BreakSelect", "loop control: break -> continue", 0},
		{"BreakSelectInLoop", "loop control: break -> continue", 2},
		{"BreakLabeledLoop", "loop control: break -> continue", 1},
		{"BreakLabeledSwitch", "loop control: break -> continue", 0},
		{"ContinueLoop", "loop control: continue -> break", 1},
		{"ContinueLabeled", "loop control: continue -> break", 1},
		{"BreakAcrossFuncBoundary", "loop control: break -> continue", 1},
	} {
		generation, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib."+test.symbol, 0)
		if err != nil {
			t.Fatal(err)
		}
		got := 0
		for _, candidate := range generation.Candidates {
			if candidate.Operator == test.operator {
				got++
			}
		}
		if got != test.want {
			t.Errorf("%s %s candidates = %d, want %d", test.symbol, test.operator, got, test.want)
		}
	}

	const branchSource = "package lib\n\nfunc BreakMapping() {\n\tfor {\n\t\tbreak\n\t}\n}\n\nfunc ContinueMapping(n int) {\n\tfor n > 0 {\n\t\tn--\n\t\tcontinue\n\t}\n}\n"
	for _, test := range []struct {
		symbol, operator, old, replacement string
	}{
		{"BreakMapping", "loop control: break -> continue", "\t\tbreak\n", "\t\tcontinue\n"},
		{"ContinueMapping", "loop control: continue -> break", "\t\tcontinue\n", "\t\tbreak\n"},
	} {
		generation, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib."+test.symbol, 0)
		if err != nil {
			t.Fatal(err)
		}
		want := strings.Replace(branchSource, test.old, test.replacement, 1)
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
	}
}

func TestConditionlessForLexingAndImportClassification(t *testing.T) {
	tr := fixtureTree(t)
	const source = "package lib\n\nfunc SemicolonStringCondition() {\n\tfor value := \";\"; ; value = \"\" {\n\t\t_ = value\n\t\tbreak\n\t}\n}\n\nfunc SemicolonCommentCondition() {\n\tfor value := 0; ; /* ; */ value++ {\n\t\tbreak\n\t}\n}\n\nfunc ConditionlessOnly() {\n\tfor {\n\t\treturn\n\t}\n}\n"
	for _, test := range []struct {
		symbol, old, replacement string
	}{
		{"SemicolonStringCondition", `for value := ";"; ; value = "" {`, `for value := ";"; false; value = "" {`},
		{"SemicolonCommentCondition", "for value := 0; ; /* ; */ value++ {", "for value := 0; false; /* ; */ value++ {"},
	} {
		generation, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib."+test.symbol, 0)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, candidate := range generation.Candidates {
			if candidate.Operator != "condition: force false" {
				continue
			}
			found = true
			want := strings.Replace(source, test.old, test.replacement, 1)
			if len(candidate.Replacements) != 1 || string(candidate.Replacements[0].Source) != want {
				t.Errorf("%s source = %q, want %q", test.symbol, candidate.Replacements, want)
			}
		}
		if !found {
			t.Errorf("%s force-false candidate missing", test.symbol)
		}
	}

	processed := 0
	tr.importProcessor = func(context.Context, string, []byte) ([]byte, error) {
		processed++
		return nil, nil
	}
	if _, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib.ConditionlessOnly", 0); err != nil {
		t.Fatal(err)
	}
	if processed != 0 {
		t.Fatalf("conditionless insertion processed imports %d times", processed)
	}
}

func TestControlOrderingSpans(t *testing.T) {
	tr := fixtureTree(t)
	for _, test := range []struct {
		symbol, operator, span string
	}{
		{"BreakMapping", "loop control: break -> continue", "break"},
		{"Forever", "condition: force false", "for"},
		{"RangeOnce", "range body: prepend break", "for _, value := range values {\n\t\ttotal += value\n\t}"},
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
			if spec.operator != test.operator {
				continue
			}
			found = true
			if got := string(catalog.text(spec.start, spec.end)); got != test.span {
				t.Errorf("%s ordering span = %q, want %q", test.operator, got, test.span)
			}
		}
		if !found {
			t.Errorf("%s ordering spec missing", test.operator)
		}
	}
}

func TestConditionlessForIgnoresNestedSemicolons(t *testing.T) {
	tr := fixtureTree(t)
	generation, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib.SemicolonNestedCondition", 0)
	if err != nil {
		t.Fatal(err)
	}
	want := "package lib\n\nfunc SemicolonNestedCondition() {\n\tfor function := func() { println() }; false; {\n\t\t_ = function\n\t\tbreak\n\t}\n}\n"
	found := false
	for _, candidate := range generation.Candidates {
		if candidate.Operator != "condition: force false" {
			continue
		}
		found = true
		if len(candidate.Replacements) != 1 || string(candidate.Replacements[0].Source) != want {
			t.Errorf("nested-semicolon source = %q, want %q", candidate.Replacements, want)
		}
	}
	if !found {
		t.Fatal("nested-semicolon force-false candidate missing")
	}
}

func TestBooleanOperandAndRangeSources(t *testing.T) {
	tr := fixtureTree(t)
	const mappingSource = "package lib\n\nfunc MappingEQ(a, b int) bool   { return a == b }\nfunc MappingNEQ(a, b int) bool  { return a != b }\nfunc MappingLT(a, b int) bool   { return a < b }\nfunc MappingLE(a, b int) bool   { return a <= b }\nfunc MappingGT(a, b int) bool   { return a > b }\nfunc MappingGE(a, b int) bool   { return a >= b }\nfunc MappingAND(a, b bool) bool { return a && b }\nfunc MappingOR(a, b bool) bool  { return a || b }\n"
	for _, test := range []struct {
		symbol, operator, old, replacement string
	}{
		{"MappingAND", "boolean operand: -> true", "return a && b", "return true && b"},
		{"MappingAND", "boolean operand: -> true", "return a && b", "return a && true"},
		{"MappingOR", "boolean operand: -> false", "return a || b", "return false || b"},
		{"MappingOR", "boolean operand: -> false", "return a || b", "return a || false"},
	} {
		generation, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib."+test.symbol, 0)
		if err != nil {
			t.Fatal(err)
		}
		want := strings.Replace(mappingSource, test.old, test.replacement, 1)
		found := false
		for _, candidate := range generation.Candidates {
			if candidate.Operator == test.operator && len(candidate.Replacements) == 1 && string(candidate.Replacements[0].Source) == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s did not render %s", test.symbol, test.replacement)
		}
	}

	for _, symbol := range []string{"LogicalDefined", "LogicalGeneric"} {
		generation, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib."+symbol, 0)
		if err != nil {
			t.Fatal(err)
		}
		counts := map[string]int{}
		for _, candidate := range generation.Candidates {
			if strings.HasPrefix(candidate.Operator, "boolean operand:") {
				counts[candidate.Operator]++
			}
		}
		if counts["boolean operand: -> true"] != 2 || counts["boolean operand: -> false"] != 4 {
			t.Errorf("%s operand candidates = %v", symbol, counts)
		}
	}

	const rangeSource = "package lib\n\nfunc RangeOnce(values []int) int {\n\ttotal := 0\n\tfor _, value := range values {\n\t\ttotal += value\n\t}\n\treturn total\n}\n\nfunc RangeEmpty(values []int) {\n\tfor range values {\n\t}\n}\n"
	for _, symbol := range []string{"RangeOnce", "RangeEmpty"} {
		generation, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib."+symbol, 0)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, candidate := range generation.Candidates {
			if candidate.Operator != "range body: prepend break" {
				continue
			}
			found = true
			old := "for _, value := range values {\n\t\ttotal += value"
			replacement := "for _, value := range values {\n\t\tbreak\n\t\ttotal += value"
			if symbol == "RangeEmpty" {
				old = "for range values {"
				replacement = "for range values {\n\t\tbreak"
			}
			want := strings.Replace(rangeSource, old, replacement, 1)
			if len(candidate.Replacements) != 1 || string(candidate.Replacements[0].Source) != want {
				t.Errorf("%s range source = %q, want %q", symbol, candidate.Replacements, want)
			}
			wantPosition := map[string]string{"RangeOnce": "control_range.go:5:18", "RangeEmpty": "control_range.go:12:6"}[symbol]
			if candidate.Position != wantPosition {
				t.Errorf("%s range position = %s, want %s", symbol, candidate.Position, wantPosition)
			}
		}
		if !found {
			t.Errorf("%s range suppression missing", symbol)
		}
	}
}
