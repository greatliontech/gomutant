package mcpserver

import (
	"encoding/json"
	"testing"

	"github.com/greatliontech/gomutant"
)

// The inline analysis row is the event itself: the unit's position and
// the served memo class ride under their own keys, absent when unknown,
// and the notification carries the same head (REQ-exec-run-status).
func TestAnalysisRowCarriesThePosition(t *testing.T) {
	event := gomutant.AnalysisEvent{Phase: "prove", Package: "example.com/p", Index: 3, Total: 40}
	row, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(row), `{"phase":"prove","package":"example.com/p","index":3,"total":40}`; got != want {
		t.Fatalf("inline row = %s, want %s", got, want)
	}
	served, _ := json.Marshal(gomutant.AnalysisEvent{Phase: "served", Served: "observability proof", Index: 12})
	if got, want := string(served), `{"phase":"served","index":12,"served":"observability proof"}`; got != want {
		t.Fatalf("served row = %s, want %s", got, want)
	}
	if got, want := analysisEventMessage(event), "analysis proving oracle closure freshness (gofresh hash proof) example.com/p (3/40)"; got != want {
		t.Fatalf("keep-alive message = %q, want %q", got, want)
	}
}

// A freshness-analysis keep-alive names the heartbeat's stretch in the
// CLI cadence line's words; a fact about an operation (a served summary)
// and a payload-bearing diagnostic leave the stretch where it was
// (REQ-exec-run-status).
func TestAnalysisKeepAliveNamesTheHeartbeatStretch(t *testing.T) {
	streams := newRunStreams(&runOut{}, nil)
	event := gomutant.AnalysisEvent{Phase: "prove", Package: "example.com/p", Index: 3, Total: 40}
	streams.analysis(event)
	if got, want := streams.lastPhase.v.Load().(string), gomutant.StretchAnalysis(event); got != want {
		t.Fatalf("heartbeat stretch after a keep-alive = %q, want %q", got, want)
	}
	for _, fact := range []gomutant.AnalysisEvent{
		{Phase: "served", Served: "observability proof", Index: 12},
		{Phase: "budget-exhausted", Detail: "analysis budget 1s exhausted: 1 subject unproven"},
	} {
		streams.analysis(fact)
		if got, want := streams.lastPhase.v.Load().(string), gomutant.StretchAnalysis(event); got != want {
			t.Fatalf("heartbeat stretch after %+v = %q, want the keep-alive's %q", fact, got, want)
		}
	}
}
