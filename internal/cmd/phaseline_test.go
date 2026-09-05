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
