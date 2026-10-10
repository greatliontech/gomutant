package gomutant

import (
	"strings"
	"time"
)

// coverageKey identifies the advisory full-pattern coverage probe.
func coverageKey(g group, coverPkg string) string {
	return g.pkgs[0] + "\x00" + g.runRegex + "\x00" + coverPkg + "\x00" + strings.Join(g.flags, "\x00")
}

// executesCandidate is shared by dispatch and pricing. Reuse gates decide
// which candidates require fresh execution; coverage never makes that choice.
func executesCandidate(w work, mi int) bool {
	switch {
	case w.serve != nil && !w.flagged[mi]:
		return false
	case w.extend != nil && mi < w.extendFrom:
		return false
	case w.drift != nil && !w.driftRemeasure[mi]:
		return false
	}
	_, runnable := w.candidates[mi].Mutant()
	return runnable
}

func executingIndexes(w work) []int {
	var out []int
	for mi := range w.candidates {
		if executesCandidate(w, mi) {
			out = append(out, mi)
		}
	}
	return out
}

func stepBudget(g group, opts runOptions) time.Duration {
	if opts.groupBudget != nil {
		return opts.groupBudget(g)
	}
	return opts.OracleTimeout
}
