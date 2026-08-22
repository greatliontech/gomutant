package gomutant

import "testing"

// The confirmation-flip event carries its whole payload — phase,
// symbol, mutant position, withdrawn killer — so a demoted kill is
// self-diagnosing on any advisory face (REQ-exec-run-status's
// confirmation-flip class; the false-survivor field report's watch
// instrumentation).
func TestReportConfirmationFlipCarriesThePayload(t *testing.T) {
	var got []ExecutionEvent
	reportConfirmationFlip(func(e ExecutionEvent) { got = append(got, e) },
		"example.com/m.Pager.flushBump", "pager.go:695:44", "TestUnitBumpPacking", 3, 9)
	if len(got) != 1 {
		t.Fatalf("events = %d, want exactly one", len(got))
	}
	e := got[0]
	if e.Phase != "confirmation-flip" || e.Symbol != "example.com/m.Pager.flushBump" ||
		e.FlipPosition != "pager.go:695:44" || e.FlipKiller != "TestUnitBumpPacking" ||
		e.TargetIndex != 3 || e.TargetCount != 9 {
		t.Fatalf("flip event = %+v, want the full payload", e)
	}
	// A nil callback is the no-listener case, never a panic.
	reportConfirmationFlip(nil, "s", "p", "k", 1, 1)
}
