package gomutant

import (
	"slices"
	"testing"
)

// The pattern split mirrors the testing harness: top-level '|' opens an
// alternative, top-level '/' ends the first element, brackets,
// parentheses, and escapes hide both.
func TestRunPatternFirstElementsMirrorTheHarness(t *testing.T) {
	for run, want := range map[string][]string{
		"":                    {""},
		"^TestA$":             {"^TestA$"},
		"TestA/sub":           {"TestA"},
		"TestA/x|TestB":       {"TestA", "TestB"},
		"TestA|TestB/y|TestC": {"TestA", "TestB", "TestC"},
		"[/]x":                {"[/]x"},
		"(a/b)|c":             {"(a/b)", "c"},
		`a\/b/c`:              {`a\/b`},
		"[a|b]/c":             {"[a|b]"},
		"TestA/sub|":          {"TestA", ""},
	} {
		if got := runPatternFirstElements(run); !slices.Equal(got, want) {
			t.Errorf("runPatternFirstElements(%q) = %q, want %q", run, got, want)
		}
	}
}
