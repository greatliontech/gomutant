package gomutant

import (
	"strings"
	"testing"
)

// Every guidance arm speaks only for what its probes proved: a sweep
// that never ran claims nothing, a clean sweep names the mutant-induced
// class, an all-unstable set points at stabilization
// (REQ-exec-oracle-guidance).
func TestBuildOracleGuidanceArms(t *testing.T) {
	oracle := []string{"p.TestA", "p.TestB"}
	cases := []struct {
		name       string
		attr       oracleAttribution
		mutantOnly []string
		want       []string
	}{
		{"no probe completed", oracleAttribution{firstErr: "package broke"}, nil, []string{"attribution unavailable", "package broke"}},
		{"clean sweep", oracleAttribution{completed: 2}, nil, []string{"no single oracle test reproduces", "mutant-execution induced"}},
		{"clean sweep names mutant-only inputs", oracleAttribution{completed: 2}, []string{"testdata/a.json", "testdata/b.json"}, []string{"inputs observed only under mutant execution: testdata/a.json, testdata/b.json"}},
		{"all unstable", oracleAttribution{completed: 2, unstable: oracle}, nil, []string{"every oracle test's own run is unstable", "p.TestA, p.TestB"}},
		{"subset", oracleAttribution{completed: 2, unstable: []string{"p.TestB"}}, nil, []string{"excluding p.TestB", "stable oracle: p.TestA"}},
	}
	if got := mutantOnlyInputs([]string{"b.txt", "a.txt", "probed.txt"}, map[string]bool{"probed.txt": true}); len(got) != 2 || got[0] != "a.txt" || got[1] != "b.txt" {
		t.Fatalf("mutant-only inputs = %v", got)
	}
	for _, tc := range cases {
		g := buildOracleGuidance("q.F", "sealed", oracle, tc.attr, tc.mutantOnly)
		for _, want := range tc.want {
			if !strings.Contains(g.Suggestion, want) {
				t.Fatalf("%s: suggestion = %q, missing %q", tc.name, g.Suggestion, want)
			}
		}
	}
}
