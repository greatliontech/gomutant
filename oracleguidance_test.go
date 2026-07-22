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
		name string
		attr oracleAttribution
		want []string
	}{
		{"no probe completed", oracleAttribution{firstErr: "package broke"}, []string{"attribution unavailable", "package broke"}},
		{"clean sweep", oracleAttribution{completed: 2}, []string{"no single oracle test reproduces", "mutant-execution induced"}},
		{"all unstable", oracleAttribution{completed: 2, unstable: oracle}, []string{"every oracle test's own run is unstable", "p.TestA, p.TestB"}},
		{"subset", oracleAttribution{completed: 2, unstable: []string{"p.TestB"}}, []string{"excluding p.TestB", "stable oracle: p.TestA"}},
	}
	for _, tc := range cases {
		g := buildOracleGuidance("q.F", "sealed", oracle, tc.attr)
		for _, want := range tc.want {
			if !strings.Contains(g.Suggestion, want) {
				t.Fatalf("%s: suggestion = %q, missing %q", tc.name, g.Suggestion, want)
			}
		}
	}
}
