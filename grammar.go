package gomutant

import (
	"fmt"
	"strings"
)

// The run's four advisory grammars — decisions, execution events,
// analysis events, and the banked epilogue (BankedState.Text) — render
// once, here, beside PreparationEvent.Text: each face keeps only its
// prefix and column policy, so the two faces can never tell one event
// two ways (REQ-exec-run-status; REQ-mcp-envelope).

// Text renders the decision as its label (the action) and the rest of
// the line: the symbol, the candidate count on a measure alone (a
// served decision's reason names any re-execution itself), and the
// reason in parentheses when one is given.
func (d RunDecision) Text() (label, rest string) {
	rest = d.Symbol
	if d.Action == "measure" {
		rest += fmt.Sprintf("  %d candidates", d.Candidates)
	}
	if d.Reason != "" {
		rest += " (" + d.Reason + ")"
	}
	return d.Action, rest
}

// Text renders the execution event as its label (the phase word) and
// the rest of the line, every payload folded flat: the probe phase's
// priced announcement and its batch ticks, the window cost model, the
// audit's summary and each audit flip with its killer, a confirmation
// flip with its withdrawn killer, and the window line with its
// position and tallies. selectionNote follows the symbol on the lines
// that place a target, and suffix ends the window line alone — a
// face's own annotations (a resumed run's denominator, the
// confirmation mode). A candidate tick renders nothing: ok is false,
// the cadence progress carries the pace.
func (e ExecutionEvent) Text(selectionNote, suffix string) (label, rest string, ok bool) {
	switch e.Phase {
	case "tick":
		return "", "", false
	case "probing":
		rest = fmt.Sprintf("target %d/%d %s%s  probes %d/%d", e.TargetIndex, e.TargetCount, e.Symbol, selectionNote, e.ProbesDone, e.ProbesTotal)
		if e.ProbesDone == 0 {
			// The projection is an upper bound (each batch at its
			// group's whole baseline); nothing priced renders no figure.
			if e.EstimateProjected != "" {
				rest += fmt.Sprintf("  (up to ~%s", e.EstimateProjected)
				if e.ProbesUnpriced > 0 {
					rest += fmt.Sprintf(", %d unpriced", e.ProbesUnpriced)
				}
				rest += ")"
			} else if e.ProbesUnpriced > 0 {
				rest += fmt.Sprintf("  (%d unpriced)", e.ProbesUnpriced)
			}
		}
		return "probing", rest, true
	case "estimate":
		rest = fmt.Sprintf("target %d/%d %s%s", e.TargetIndex, e.TargetCount, e.Symbol, selectionNote)
		if e.EstimateProjected != "" {
			rest += fmt.Sprintf("  window ~%s", e.EstimateProjected)
		}
		rest += fmt.Sprintf("  (%d narrowed, %d full", e.EstimateNarrowed, e.EstimateFull)
		if e.EstimateUnknown > 0 {
			rest += fmt.Sprintf(", %d unpriced", e.EstimateUnknown)
		}
		rest += ")"
		if e.EstimateAudit != "" {
			rest += fmt.Sprintf("  audit ~%s", e.EstimateAudit)
		}
		return "estimate", rest, true
	case "audit-flip":
		return "audit", fmt.Sprintf("FLIP: %s %s - narrowed survivor killed by %s under the full oracle; verdict re-scored", e.Symbol, e.FlipPosition, e.FlipKiller), true
	case "audit":
		return "audit", fmt.Sprintf("%d narrowed survivor(s) re-scored under the full oracle, %d disagreed", e.AuditedNarrowed, e.AuditDisagreed), true
	case "confirmation-flip":
		return "confirmation", fmt.Sprintf("FLIP: %s %s - provisional kill by %s re-scored survivor on serial re-run", e.Symbol, e.FlipPosition, e.FlipKiller), true
	}
	rest = fmt.Sprintf("target %d/%d %s%s", e.TargetIndex, e.TargetCount, e.Symbol, selectionNote)
	// A confirming window's candidate tally is saturated by
	// construction — the confirmations counter is the signal — so the
	// line drops the dead segment (the event carries the tallies).
	if e.Phase != "confirming" {
		rest += fmt.Sprintf("  candidates %d/%d", e.CandidatesDone, e.CandidatesTotal)
	}
	if e.ConfirmationsTotal > 0 {
		rest += fmt.Sprintf("  confirmations %d/%d", e.ConfirmationsDone, e.ConfirmationsTotal)
	}
	return e.Phase, rest + suffix, true
}

// Stretch names the stretch of work an execution event opens, for a
// heartbeat that says what is still running: the probe pass with its
// batch position, the window's estimate, its execution, its serial
// confirmation — and each candidate tick advances the executing
// stretch's done tally, so a face whose cadence is the heartbeat still
// reads monotonic per-candidate progress (REQ-exec-run-status). The
// audit's summary and the flips name no stretch (empty).
func (e ExecutionEvent) Stretch() string {
	switch e.Phase {
	case "tick":
		return fmt.Sprintf("executing mutants %s %d/%d", e.Symbol, e.CandidatesDone, e.CandidatesTotal)
	case "probing":
		return fmt.Sprintf("probing %d/%d %s", e.ProbesDone, e.ProbesTotal, e.Symbol)
	case "estimate":
		return "estimating " + e.Symbol
	case "executing":
		return "executing mutants " + e.Symbol
	case "confirming":
		return "confirming " + e.Symbol
	}
	return ""
}

// AnalysisEvent is a payload-bearing analysis event from the freshness
// engine, as Options.AnalysisEvent delivers it: the engine's phase,
// the package, and the detail.
type AnalysisEvent struct {
	Phase   string
	Package string
	Detail  string
}

// analysisEventSink adapts the caller's event callback to the engine's
// three-string seam; nil stays nil.
func analysisEventSink(fn func(AnalysisEvent)) func(phase, pkg, detail string) {
	if fn == nil {
		return nil
	}
	return func(phase, pkg, detail string) { fn(AnalysisEvent{Phase: phase, Package: pkg, Detail: detail}) }
}

// analysisVocabulary maps the engine's internal phase names to the
// operator vocabulary — an unexplained "analysis observe" was a field
// report's exact complaint. Unknown phases pass through raw so new
// engine vocabulary stays visible rather than silently renamed.
var analysisVocabulary = map[string]string{
	"analysis-unavailable": "attributed reachability unavailable for",
	"load":                 "loading package graphs (gofresh analysis)",
	"observe":              "observing oracle runtime inputs (freshness evidence)",
	"runtime":              "validating runtime-input evidence (oracle freshness)",
	"prove":                "proving oracle closure freshness (gofresh hash proof)",
	"baseline-output":      "oracle baseline output for",
}

// Head renders the event's phase in the operator vocabulary with its
// package: the line's head, before any detail.
func (a AnalysisEvent) Head() string {
	phrase := a.Phase
	if v, ok := analysisVocabulary[a.Phase]; ok {
		phrase = v
	}
	return strings.TrimSpace(phrase + " " + a.Package)
}

// Text renders the event as one line: the head, then the detail after
// an em dash when there is one. A face rendering a many-line detail
// (a baseline's output) keeps Head and lays the detail out itself.
func (a AnalysisEvent) Text() string {
	if detail := strings.TrimSpace(a.Detail); detail != "" {
		return a.Head() + " — " + detail
	}
	return a.Head()
}
