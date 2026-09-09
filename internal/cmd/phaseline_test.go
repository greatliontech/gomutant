package cmd

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gomutant"
)

// The cadence line names the phase in flight until the first decision,
// and carries the run tallies from then on (REQ-exec-run-status).
func TestProgressLineNamesThePhaseInFlight(t *testing.T) {
	var out bytes.Buffer
	rep := newRunReporter(&out, false, 0)
	rep.phase("loading")
	rep.progressLine()
	if !strings.HasPrefix(out.String(), "progress  loading, elapsed ") {
		t.Fatalf("phase line = %q", out.String())
	}
	out.Reset()
	rep.phase("baseline ^TestX$")
	rep.progressLine()
	if !strings.Contains(out.String(), "baseline ^TestX$") {
		t.Fatalf("phase line = %q", out.String())
	}
	out.Reset()
	rep.decision(gomutant.RunDecision{Symbol: "p.F", Action: "measure", Candidates: 3})
	rep.executing(gomutant.ExecutionEvent{Phase: "executing", CandidatesTotal: 3})
	rep.progressLine()
	if !strings.Contains(out.String(), "targets committed") || strings.Contains(out.String(), "baseline") {
		t.Fatalf("tallies line = %q", out.String())
	}
	var jsonl bytes.Buffer
	structured := newRunReporter(&jsonl, true, 0)
	structured.phase("loading")
	structured.progressLine()
	if !strings.Contains(jsonl.String(), `"phase":"loading"`) {
		t.Fatalf("structured phase line = %q", jsonl.String())
	}
}

// The epilogue ends the cadence before rendering: after it returns no
// progress line can follow the rows, however fast the cadence.
func TestEpilogueEndsTheCadenceBeforeTheRows(t *testing.T) {
	var out bytes.Buffer
	rep := newRunReporter(&syncWriter{w: &out}, false, 0)
	rep.phase("loading")
	rep.startCadence(time.Millisecond)
	time.Sleep(5 * time.Millisecond)
	rep.epilogue(func(w io.Writer) { fmt.Fprintln(w, "rows") })
	time.Sleep(20 * time.Millisecond)
	if !strings.HasSuffix(out.String(), "rows\n") {
		t.Fatalf("output after the epilogue = %q; want the rows last", out.String())
	}
}

// A one-call verb primes its phase before the cadence starts, so a
// tick before the loading line names the phase and never prints
// run-shaped tallies the verb does not have.
func TestPhasePrimedBeforeTheCadenceNeverPrintsTallies(t *testing.T) {
	var out bytes.Buffer
	rep := newRunReporter(&out, false, 0)
	rep.phase("loading")
	rep.progressLine()
	if strings.Contains(out.String(), "targets committed") || !strings.HasPrefix(out.String(), "progress  loading") {
		t.Fatalf("tick before the loading line = %q", out.String())
	}
}

// A payload-bearing analysis event renders on one line when its detail
// fits one, and as an indented block otherwise — a baseline's output is
// many lines and stays readable under its heading.
func TestRenderAnalysisIndentsMultiLineDetail(t *testing.T) {
	var out bytes.Buffer
	renderAnalysis(&out, gomutant.AnalysisEvent{Phase: "analysis-unavailable", Package: "example.com/p", Detail: "no analyzer"})
	if out.String() != "analysis  attributed reachability unavailable for example.com/p — no analyzer\n" {
		t.Fatalf("one-line detail = %q", out.String())
	}
	out.Reset()
	renderAnalysis(&out, gomutant.AnalysisEvent{Phase: "baseline-output", Package: "example.com/p", Detail: "TestX:\n--- FAIL: TestX\n    x_test.go:9: broke\n"})
	want := "analysis  oracle baseline output for example.com/p:\n          TestX:\n          --- FAIL: TestX\n              x_test.go:9: broke\n"
	if out.String() != want {
		t.Fatalf("multi-line detail = %q, want %q", out.String(), want)
	}
}

// The probing line: the announcement carries the upper-bound projection
// and the unpriced count, a tick the paid count only, and nothing
// priced renders no figure (REQ-exec-run-status).
func TestRenderProbingPhase(t *testing.T) {
	var out bytes.Buffer
	renderExecutionEvent(&out, gomutant.ExecutionEvent{Phase: "probing", TargetIndex: 2, TargetCount: 5, Symbol: "p.F", ProbesTotal: 4, EstimateProjected: "2m0s", ProbesUnpriced: 1}, "", "")
	renderExecutionEvent(&out, gomutant.ExecutionEvent{Phase: "probing", TargetIndex: 2, TargetCount: 5, Symbol: "p.F", ProbesDone: 3, ProbesTotal: 4}, "", "")
	renderExecutionEvent(&out, gomutant.ExecutionEvent{Phase: "probing", TargetIndex: 2, TargetCount: 5, Symbol: "p.F", ProbesTotal: 2, ProbesUnpriced: 2}, "", "")
	want := "probing   target 2/5 p.F  probes 0/4  (up to ~2m0s, 1 unpriced)\n" +
		"probing   target 2/5 p.F  probes 3/4\n" +
		"probing   target 2/5 p.F  probes 0/2  (2 unpriced)\n"
	if out.String() != want {
		t.Fatalf("probing lines = %q, want %q", out.String(), want)
	}
}
