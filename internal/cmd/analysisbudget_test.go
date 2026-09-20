package cmd

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/gomutant"
)

// The analysis budget defaults unbounded on the CLI face, like the
// oracle timeout defaults to derivation (REQ-exec-analysis-budget).
func TestAnalysisBudgetFlagDefaultsUnbounded(t *testing.T) {
	cmd := newRunCommand()
	if got := cmd.Flags().Lookup("analysis-budget"); got == nil || got.DefValue != "0s" {
		t.Fatalf("--analysis-budget = %+v, want the unbounded default (0s)", got)
	}
}

// The cadence line names the freshness-analysis stretch the engine
// last announced: before the first decision as the phase line, after
// it beside the tallies with its age — until the preparation sequence
// moves on. A payload-bearing diagnostic names no stretch
// (REQ-exec-run-status).
func TestProgressLineNamesTheAnalysisStretch(t *testing.T) {
	var out bytes.Buffer
	rep := newRunReporter(&out, false, 0)
	t0 := time.Now()
	rep.now = func() time.Time { return t0 }
	rep.phase(gomutant.StretchPreparation)
	rep.analysis(gomutant.AnalysisEvent{Phase: "prove", Package: "example.com/p", Index: 3, Total: 40})
	rep.progressLine()
	if !strings.HasPrefix(out.String(), "progress  analysis proving oracle closure freshness (gofresh hash proof) example.com/p (3/40), elapsed ") {
		t.Fatalf("phase line = %q", out.String())
	}
	// A fact about an operation names no stretch: the phase line keeps
	// the keep-alive's.
	out.Reset()
	rep.analysis(gomutant.AnalysisEvent{Phase: "served", Served: "observability proof", Index: 12})
	rep.progressLine()
	if !strings.HasPrefix(out.String(), "progress  analysis proving oracle closure freshness (gofresh hash proof) example.com/p (3/40), elapsed ") {
		t.Fatalf("phase line after a served fact = %q; want the keep-alive's stretch kept", out.String())
	}
	out.Reset()
	rep.decision(gomutant.RunDecision{Symbol: "p.F", Action: "measure", Candidates: 3})
	rep.executing(gomutant.ExecutionEvent{Phase: "executing", CandidatesTotal: 3})
	rep.now = func() time.Time { return t0.Add(4 * time.Minute) }
	rep.analysis(gomutant.AnalysisEvent{Phase: "prove", Package: "example.com/p", Index: 4, Total: 40})
	rep.now = func() time.Time { return t0.Add(4*time.Minute + 7*time.Second) }
	rep.progressLine()
	line := out.String()
	if !strings.Contains(line, "targets committed") || !strings.HasSuffix(line, ", analysis proving oracle closure freshness (gofresh hash proof) example.com/p (4/40) (7s ago)\n") {
		t.Fatalf("tallies line = %q; want the analysis stretch with its age", line)
	}
	out.Reset()
	rep.now = func() time.Time { return t0.Add(5 * time.Minute) }
	rep.preparation(gomutant.PreparationEvent{Stage: gomutant.PreparationMutants, Symbol: "p.G"})
	rep.progressLine()
	if strings.Contains(out.String(), "analysis") {
		t.Fatalf("tallies line after the sequence moved on = %q; want no analysis stretch", out.String())
	}
	out.Reset()
	rep.now = func() time.Time { return t0.Add(6 * time.Minute) }
	rep.analysis(gomutant.AnalysisEvent{Phase: "budget-exhausted", Detail: "analysis budget 1s exhausted: 1 of 2 subjects unproven"})
	rep.analysis(gomutant.AnalysisEvent{Phase: "served", Served: "observability proof", Index: 12})
	rep.progressLine()
	if strings.Contains(out.String(), "analysis") {
		t.Fatalf("tallies line after a diagnostic and a served fact = %q; want no analysis stretch (neither names one)", out.String())
	}
	// A keep-alive at the very instant of the sequence's latest event
	// is not after it; a decision or a commit after a keep-alive moves
	// the sequence on and clears the stretch.
	out.Reset()
	rep.now = func() time.Time { return t0.Add(7 * time.Minute) }
	rep.decision(gomutant.RunDecision{Symbol: "p.H", Action: "measure"})
	rep.analysis(gomutant.AnalysisEvent{Phase: "prove", Package: "example.com/p", Index: 5, Total: 40})
	rep.progressLine()
	if strings.Contains(out.String(), "analysis") {
		t.Fatalf("tallies line with a keep-alive at the decision's instant = %q; want no analysis stretch", out.String())
	}
	out.Reset()
	rep.now = func() time.Time { return t0.Add(8 * time.Minute) }
	rep.analysis(gomutant.AnalysisEvent{Phase: "prove", Package: "example.com/p", Index: 6, Total: 40})
	rep.now = func() time.Time { return t0.Add(9 * time.Minute) }
	rep.decision(gomutant.RunDecision{Symbol: "p.I", Action: "measure"})
	rep.progressLine()
	if strings.Contains(out.String(), "analysis") {
		t.Fatalf("tallies line after a decision cleared the stretch = %q", out.String())
	}
	out.Reset()
	rep.now = func() time.Time { return t0.Add(10 * time.Minute) }
	rep.analysis(gomutant.AnalysisEvent{Phase: "prove", Package: "example.com/p", Index: 7, Total: 40})
	rep.now = func() time.Time { return t0.Add(11 * time.Minute) }
	rep.bankedFinding(gomutant.Finding{Symbol: "p.I"})
	rep.progressLine()
	if strings.Contains(out.String(), "analysis") {
		t.Fatalf("tallies line after a commit cleared the stretch = %q", out.String())
	}
	var jsonl bytes.Buffer
	structured := newRunReporter(&jsonl, true, 0)
	structured.now = func() time.Time { return t0 }
	structured.decision(gomutant.RunDecision{Symbol: "p.F", Action: "measure"})
	structured.now = func() time.Time { return t0.Add(time.Second) }
	structured.analysis(gomutant.AnalysisEvent{Phase: "prove", Package: "example.com/p", Index: 3, Total: 40})
	structured.now = func() time.Time { return t0.Add(10 * time.Second) }
	structured.progressLine()
	if !strings.Contains(jsonl.String(), `"analysis":"analysis proving oracle closure freshness (gofresh hash proof) example.com/p (3/40)"`) || !strings.Contains(jsonl.String(), `"analysisAge":"9s"`) {
		t.Fatalf("structured progress record = %q; want the analysis stretch and its age under their own keys", jsonl.String())
	}
}

// The structured face's analysis record is the event itself: the unit's
// position, the served memo class, and the detail each under its own
// key, absent when the event does not carry them (REQ-exec-run-status).
func TestAnalysisRecordCarriesThePosition(t *testing.T) {
	var out bytes.Buffer
	rep := newRunReporter(&out, true, 0)
	rep.emit("analysis", gomutant.AnalysisEvent{Phase: "prove", Package: "example.com/p", Index: 3, Total: 40})
	rep.emit("analysis", gomutant.AnalysisEvent{Phase: "served", Served: "observability proof", Index: 12})
	rep.emit("analysis", gomutant.AnalysisEvent{Phase: "budget-exhausted", Detail: "analysis budget 1s exhausted: 1 subject unproven"})
	want := "{\"event\":\"analysis\",\"index\":3,\"package\":\"example.com/p\",\"phase\":\"prove\",\"total\":40}\n" +
		"{\"event\":\"analysis\",\"index\":12,\"phase\":\"served\",\"served\":\"observability proof\"}\n" +
		"{\"detail\":\"analysis budget 1s exhausted: 1 subject unproven\",\"event\":\"analysis\",\"phase\":\"budget-exhausted\"}\n"
	if out.String() != want {
		t.Fatalf("analysis records =\n%s\nwant\n%s", out.String(), want)
	}
}

// The run verb wires every freshness-analysis keep-alive into the
// cadence line's stretch — the assignment itself is exercised end to
// end over a measured run, or dropping it leaves the reporter's own
// pins green (REQ-exec-run-status).
func TestRunWiresAnalysisEventsIntoTheStretch(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	dir := isolatedFixture(t)
	targets := filepath.Join(dir, "targets.json")
	if err := os.WriteFile(targets, []byte(`{"targets":[{"symbol":"example.com/fixture/lib.Add","oracle":["example.com/fixture/lib.TestAdd"]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	labels := observeStretches(t)
	if err := runCommand(context.Background(), runOptions{dir: dir, targetsFile: targets, budget: 1, output: io.Discard}); err != nil {
		t.Fatal(err)
	}
	for _, label := range labels() {
		if strings.HasPrefix(label, "analysis ") {
			return
		}
	}
	t.Fatalf("stretches = %q; want a freshness-analysis keep-alive's stretch among them", labels())
}
