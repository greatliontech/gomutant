package gomutant

import (
	"strings"
	"testing"
)

// The four grammars render once: every execution-event class, every
// decision shape, and the analysis event have one text, a candidate
// tick none, and the heartbeat's stretch follows the event's phase —
// the confirming phase included (REQ-exec-run-status).
func TestGrammarsRenderOnceForEveryClass(t *testing.T) {
	base := ExecutionEvent{TargetIndex: 2, TargetCount: 5, Symbol: "a.F", CandidatesDone: 3, CandidatesTotal: 9}
	rows := []struct {
		event   ExecutionEvent
		label   string
		want    []string
		stretch string
	}{
		{ExecutionEvent{Phase: "tick", Symbol: "a.F", CandidatesDone: 3, CandidatesTotal: 9}, "", nil, "executing mutants a.F 3/9"},
		{with(base, func(e *ExecutionEvent) {
			e.Phase = "probing"
			e.ProbesTotal = 4
			e.EstimateProjected = "2m"
			e.ProbesUnpriced = 1
		}), "probing", []string{"target 2/5 a.F", "probes 0/4", "up to ~2m", "1 unpriced"}, "probing 0/4 a.F"},
		{with(base, func(e *ExecutionEvent) {
			e.Phase = "estimate"
			e.EstimateProjected = "3m"
			e.EstimateNarrowed = 4
			e.EstimateFull = 5
			e.EstimateUnknown = 1
			e.EstimateAudit = "9s"
		}), "estimate", []string{"window ~3m", "4 narrowed, 5 full, 1 unpriced", "audit ~9s"}, "estimating a.F"},
		{with(base, func(e *ExecutionEvent) { e.Phase = "audit-flip"; e.FlipPosition = "f.go:1:1"; e.FlipKiller = "a.TestF" }), "audit", []string{"FLIP: a.F f.go:1:1", "killed by a.TestF under the full oracle"}, ""},
		{with(base, func(e *ExecutionEvent) { e.Phase = "audit"; e.AuditedNarrowed = 3; e.AuditDisagreed = 1 }), "audit", []string{"3 narrowed survivor(s)", "1 disagreed"}, ""},
		{with(base, func(e *ExecutionEvent) {
			e.Phase = "confirmation-flip"
			e.FlipPosition = "f.go:2:2"
			e.FlipKiller = "a.TestG"
		}), "confirmation", []string{"FLIP: a.F f.go:2:2", "provisional kill by a.TestG"}, ""},
		{with(base, func(e *ExecutionEvent) { e.Phase = "executing" }), "executing", []string{"target 2/5 a.F", "candidates 3/9"}, "executing mutants a.F"},
		{with(base, func(e *ExecutionEvent) { e.Phase = "confirming"; e.ConfirmationsDone = 1; e.ConfirmationsTotal = 2 }), "confirming", []string{"target 2/5 a.F", "confirmations 1/2"}, "confirming a.F"},
	}
	for _, row := range rows {
		label, rest, ok := row.event.Text(" (of 7)", " mode=x")
		if row.label == "" {
			if ok {
				t.Fatalf("%s rendered %q %q", row.event.Phase, label, rest)
			}
		} else if !ok || label != row.label {
			t.Fatalf("%s label = %q (ok %v), want %q", row.event.Phase, label, ok, row.label)
		}
		for _, want := range row.want {
			if !strings.Contains(rest, want) {
				t.Fatalf("%s text %q lacks %q", row.event.Phase, rest, want)
			}
		}
		if window := row.event.Phase == "executing" || row.event.Phase == "confirming"; ok && strings.HasSuffix(rest, " mode=x") != window {
			t.Fatalf("%s text %q: the face's suffix rides the window line alone", row.event.Phase, rest)
		}
		if ok && row.event.Phase != "audit" && row.event.Phase != "audit-flip" && row.event.Phase != "confirmation-flip" && !strings.Contains(rest, "a.F (of 7)") {
			t.Fatalf("%s text %q lacks the face's selection note after the symbol", row.event.Phase, rest)
		}
		if row.event.Phase == "confirming" && strings.Contains(rest, "candidates") {
			t.Fatalf("a confirming line carries the saturated candidate tally: %q", rest)
		}
		if got := row.event.Stretch(); got != row.stretch {
			t.Fatalf("%s stretch = %q, want %q", row.event.Phase, got, row.stretch)
		}
	}
	for _, row := range []struct {
		decision RunDecision
		label    string
		rest     string
	}{
		{RunDecision{Action: "measure", Symbol: "a.F", Candidates: 4, Reason: "no-prior"}, "measure", "a.F  4 candidates (no-prior)"},
		{RunDecision{Action: "cached", Symbol: "a.F", Candidates: 2, Reason: "served: re-executing 2"}, "cached", "a.F (served: re-executing 2)"},
		{RunDecision{Action: "skipped", Symbol: "a.F"}, "skipped", "a.F"},
	} {
		if label, rest := row.decision.Text(); label != row.label || rest != row.rest {
			t.Fatalf("decision %+v = %q %q, want %q %q", row.decision, label, rest, row.label, row.rest)
		}
	}
	if got := (AnalysisEvent{Phase: "observe", Package: "a", Detail: "  x  "}).Text(); got != "observing oracle runtime inputs (freshness evidence) a — x" {
		t.Fatalf("analysis text = %q", got)
	}
	if got := (AnalysisEvent{Phase: "novel", Package: "a"}).Text(); got != "novel a" {
		t.Fatalf("unknown phase passes through raw: %q", got)
	}
}

func with(e ExecutionEvent, f func(*ExecutionEvent)) ExecutionEvent {
	f(&e)
	return e
}
