package gomutant

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/greatliontech/gofresh"
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

// The stretch vocabulary: the one set of names both faces record and
// render for the stretch in flight — the CLI's cadenced progress line
// and its interruption line, the structured face's heartbeat — so a
// reader of either face reads the other's words (REQ-exec-run-status).
// Every stretch a face names is one of: the preparation before the
// load (StretchPreparation); a preparation event's stage under one
// lead, the load's own event included (StretchPreparing); the target
// selection; the inspection of prior findings at the record walk's
// stage (StretchInspecting); the zero-target reconcile; the final
// merge; the rendering; and the execution stretches
// (ExecutionEvent.Stretch).
const (
	// StretchPreparation is the stretch before the load's own event:
	// the preparation a verb runs once its inputs' shapes are refused
	// — the store, the document lock, the records, a probe's batch —
	// named from the cadence's start (REQ-exec-preparation).
	StretchPreparation = "preparing"
	// StretchSelecting is the target selection after the load.
	StretchSelecting = "selecting targets"
	// StretchReconciling is the zero-target whole-tree reconcile.
	StretchReconciling = "reconciling the document"
	// StretchMerging is the final merge into the document.
	StretchMerging = "merging findings"
	// StretchRendering is the response's or report's rendering.
	StretchRendering = "rendering the response"
)

// StretchInspecting names the inspection of prior findings at one of
// the record walk's stages — the read, the signpost's pass, the
// admission, the view build, a record's judgment — as the walk reports
// them.
func StretchInspecting(stage string) string { return "inspecting prior findings: " + stage }

// StretchPreparing names a preparation event's stretch by its stage
// under one lead both faces share; what the stage names (a symbol, a
// package, a budget) rides the event itself — the CLI's prepare line,
// the structured face's notification — never the stretch's name.
func StretchPreparing(e PreparationEvent) string { return "prepare " + string(e.Stage) }

// AnalysisEvent is one freshness-analysis event from the engine, as
// Options.AnalysisEvent delivers it: the engine's phase, the package for
// a per-package phase, the unit's position among the pass's units when
// the engine knows it (Index/Total, 1-based; zero when unknown), the
// memo class a "served" summary names with Index its package count,
// and the detail of a payload-bearing diagnostic. The position is what
// lets a reader tell a pass proving its third package of forty from
// one stalled on its first (REQ-exec-run-status).
type AnalysisEvent struct {
	Phase   string `json:"phase"`
	Package string `json:"package,omitempty"`
	Index   int    `json:"index,omitempty"`
	Total   int    `json:"total,omitempty"`
	Served  string `json:"served,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// Stretch names the stretch a keep-alive announces — the pass's unit
// with its position, under the analysis lead — and reports false for
// an event that names no stretch: a fact about an operation or a
// payload-bearing diagnostic (REQ-exec-run-status). Both faces' cadence
// surfaces read this one classification, and the per-unit phase set is
// the engine's own (gofresh.UnitPhases, read through Progress.IsUnit):
// a phase the engine adds names a stretch here without a change.
func (a AnalysisEvent) Stretch() (string, bool) {
	if a.Detail != "" || !(gofresh.Progress{Phase: a.Phase}).IsUnit() {
		return "", false
	}
	return StretchAnalysis(a), true
}

// analysisEventOf is the one projection of the engine's progress event
// onto the run's: every field the engine carries, nothing folded, so a
// consumer subscribing to the class reads what the engine said.
func analysisEventOf(p gofresh.Progress) AnalysisEvent {
	return AnalysisEvent{Phase: p.Phase, Package: p.Package, Index: p.Index, Total: p.Total, Served: p.Served, Detail: p.Detail}
}

// analysisVocabulary maps the engine's internal phase names to the
// operator vocabulary — an unexplained "analysis observe" was a field
// report's exact complaint. Unknown phases pass through raw so new
// engine vocabulary stays visible rather than silently renamed.
var analysisVocabulary = map[string]string{
	"analysis-unavailable": "attributed reachability unavailable for",
	"budget-exhausted":     "freshness-proof pass cut by the analysis budget",
	"cancelled":            "freshness analysis cancelled",
	"list":                 "listing package dependencies (gofresh analysis)",
	"typecheck":            "type-checking package graphs (gofresh analysis)",
	"load":                 "loading package graphs (gofresh analysis)",
	"hash":                 "folding package closures (gofresh analysis)",
	"observe":              "observing oracle runtime inputs (freshness evidence)",
	"runtime":              "validating runtime-input evidence (oracle freshness)",
	"prove":                "proving oracle closure freshness (gofresh hash proof)",
	"served":               "served from the persistent memo",
	"baseline-output":      "oracle baseline output for",
}

// Head renders the event's phase in the operator vocabulary with its
// package and, when the engine knows it, the unit's position among the
// pass's units — "(3/40)" — or, for a served summary, the memo class
// and the count of packages it served: the line's head, before any
// detail.
func (a AnalysisEvent) Head() string {
	phrase := a.Phase
	if v, ok := analysisVocabulary[a.Phase]; ok {
		phrase = v
	}
	if a.Phase == "served" && a.Served != "" {
		return phrase + ": " + a.Served + " for " + countNoun(a.Index, "package")
	}
	head := strings.TrimSpace(phrase + " " + a.Package)
	switch {
	case a.Total > 0 && a.Index > 0:
		head += " (" + strconv.Itoa(a.Index) + "/" + strconv.Itoa(a.Total) + ")"
	case a.Total > 0:
		// A pass that knows its unit count but not the position — the
		// typed load names its pattern count.
		head += " (of " + strconv.Itoa(a.Total) + ")"
	}
	return head
}

// StretchAnalysis names a freshness-analysis keep-alive's stretch — the
// event's head under the analysis lead, the position included — so a
// face's cadence line or heartbeat says which unit of which pass the
// process is on (REQ-exec-run-status).
func StretchAnalysis(e AnalysisEvent) string { return "analysis " + e.Head() }

// Text renders the event as one line: the head, then the detail after
// an em dash when there is one. A face rendering a many-line detail
// (a baseline's output) keeps Head and lays the detail out itself.
func (a AnalysisEvent) Text() string {
	if detail := strings.TrimSpace(a.Detail); detail != "" {
		return a.Head() + " — " + detail
	}
	return a.Head()
}
