package gomutant

import (
	"fmt"
	"sort"
	"strings"
)

// OracleGuidance attributes a measured finding's unverifiable runtime
// evidence under a package-derived oracle: each oracle test probed
// individually, the unstable ones named so the caller can narrow to an
// explicit oracle instead of bisecting by hand
// (REQ-exec-oracle-guidance).
type OracleGuidance struct {
	// Symbol is the measured target whose evidence is unverifiable.
	Symbol string
	// UnstableTests are the oracle tests whose own solo probe produced
	// unverifiable runtime evidence; empty when no single test
	// reproduces the instability.
	UnstableTests []string
	// Reason is the finding's unverifiable runtime reason.
	Reason string
	// Suggestion is the one-line next action.
	Suggestion string
}

// oracleAttribution is one probed oracle set's result, memoizable
// across the targets that share the set.
type oracleAttribution struct {
	unstable  []string
	completed int
	firstErr  string
	// probedPaths unions the completed solo probes' observed
	// module-local input paths; an input the finding observed that no
	// solo probe did was reached only under mutant execution.
	probedPaths map[string]bool
}

// mutantOnlyInputs names the finding's observed input paths no
// completed solo probe reached, sorted - the reads only mutant
// execution provoked (REQ-exec-oracle-guidance).
func mutantOnlyInputs(findingPaths []string, probed map[string]bool) []string {
	var out []string
	for _, path := range findingPaths {
		if !probed[path] {
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out
}

func buildOracleGuidance(symbol, reason string, oracle []string, attr oracleAttribution, mutantOnly []string) OracleGuidance {
	unstable := attr.unstable
	g := OracleGuidance{Symbol: symbol, UnstableTests: unstable, Reason: reason}
	if attr.completed == 0 {
		// No probe completed: a sweep that never ran proves nothing, so
		// the report says so instead of claiming reproducibility.
		g.Suggestion = "attribution unavailable: no oracle-test probe completed"
		if attr.firstErr != "" {
			g.Suggestion = "attribution unavailable: " + attr.firstErr
		}
		return g
	}
	if len(unstable) == 0 {
		g.Suggestion = "no single oracle test reproduces the instability in its own run (mutant-execution induced); stabilize the input named in the reason or accept a machine-local record"
		if len(mutantOnly) > 0 {
			// The narrowest place to look: inputs the finding observed
			// that no solo probe reached (REQ-exec-oracle-guidance).
			g.Suggestion += "; inputs observed only under mutant execution: " + cappedNameList(mutantOnly, "inputs")
		}
		return g
	}
	stable := make([]string, 0, len(oracle))
	unstableSet := make(map[string]bool, len(unstable))
	for _, u := range unstable {
		unstableSet[u] = true
	}
	for _, o := range oracle {
		if !unstableSet[o] {
			stable = append(stable, o)
		}
	}
	if len(stable) == 0 {
		g.Suggestion = fmt.Sprintf("every oracle test's own run is unstable (%s); stabilize the input or give this target an explicit oracle", strings.Join(unstable, ", "))
		return g
	}
	g.Suggestion = fmt.Sprintf("rerun with an explicit oracle excluding %s if it does not vouch for this target (stable oracle: %s)",
		strings.Join(unstable, ", "), strings.Join(stable, ", "))
	return g
}
