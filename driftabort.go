package gomutant

import (
	"fmt"
	"strings"
)

// TargetDrift is one target refused because the tree changed under
// measurement: its producer evidence no longer validated after execution
// (REQ-exec-quiescence).
type TargetDrift struct {
	Symbol string
	Reason string
}

// TreeDriftError reports a run whose tree changed under measurement.
// Refusal is target-local: the drifted targets are refused with the
// drift named, while every completed target's finding re-validated and
// was already committed incrementally, so a partial campaign keeps its
// sound results and the caller re-runs only the refused set.
type TreeDriftError struct {
	Drifted   []TargetDrift
	Completed int
	// Transient carries the global validation failure when no surviving
	// target's own evidence reflected it (a transient move): the campaign
	// reports the drift rather than reading as clean.
	Transient string
}

func (e *TreeDriftError) Error() string {
	var b strings.Builder
	b.WriteString("tree changed under measurement: ")
	if len(e.Drifted) == 0 {
		fmt.Fprintf(&b, "%s; every measured target's producer evidence re-validated, served records were validated at serve time; re-run to confirm", e.Transient)
		return b.String()
	}
	first := e.Drifted[0]
	fmt.Fprintf(&b, "%s: %s", first.Symbol, first.Reason)
	if len(e.Drifted) > 1 {
		fmt.Fprintf(&b, " (and %d more targets)", len(e.Drifted)-1)
	}
	fmt.Fprintf(&b, "; %d completed target(s) kept in the findings document; re-run to measure the refused set", e.Completed)
	return b.String()
}

// completedFindings filters a run's findings to those not refused by
// drift, preserving order.
func completedFindings(findings []Finding, drifted []TargetDrift) []Finding {
	refused := make(map[string]bool, len(drifted))
	for _, d := range drifted {
		refused[d.Symbol] = true
	}
	out := make([]Finding, 0, len(findings))
	for _, f := range findings {
		if refused[f.Symbol] {
			continue
		}
		out = append(out, f)
	}
	return out
}
