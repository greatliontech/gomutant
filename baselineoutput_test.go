package gomutant

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A target skipped on a failing oracle baseline names the failing tests
// on its decision and carries the oracle's own output as a
// payload-bearing analysis event beside it (REQ-exec-run-status).
func TestCampaignBaselineFailureCarriesTheOracleOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("probes the failing fixture package")
	}
	tr := fixtureTree(t)
	var decisions []RunDecision
	var analyses []string
	target := Target{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/failing.TestAlwaysFails"}, OracleExplicit: true}
	if _, err := tr.Run(context.Background(), []Target{target}, Options{Budget: 1, OracleTimeout: time.Minute,
		Decision: func(d RunDecision) { decisions = append(decisions, d) },
		AnalysisEvent: func(event AnalysisEvent) {
			analyses = append(analyses, event.Phase+" "+event.Package+" "+event.Detail)
		},
	}); err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 1 || decisions[0].Action != "skipped" || !strings.Contains(decisions[0].Reason, "does not pass") || !strings.Contains(decisions[0].Reason, "TestAlwaysFails") {
		t.Fatalf("decisions = %+v; want the baseline skip naming the failing test", decisions)
	}
	joined := strings.Join(analyses, "\n")
	if !strings.Contains(joined, "baseline-output example.com/fixture/failing TestAlwaysFails:") || !strings.Contains(joined, "by design") {
		t.Fatalf("analysis events = %q; want the oracle's output beside the skip", analyses)
	}
}

// A baseline whose result drifts between the discovery and measurement
// runs skips the target with the drift named, and carries the failing
// run's output the same way (REQ-exec-run-status).
func TestCampaignBaselineDriftCarriesTheOracleOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("probes the fixture's unstable test")
	}
	marker := filepath.Join(t.TempDir(), "unstable")
	t.Setenv("GOMUTANT_UNSTABLE_RESULT", marker)
	tr := fixtureTree(t)
	var decisions []RunDecision
	var analyses []string
	target := Target{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestUnstableBaselineResult"}, OracleExplicit: true}
	if _, err := tr.Run(context.Background(), []Target{target}, Options{Budget: 1, OracleTimeout: time.Minute,
		Decision: func(d RunDecision) { decisions = append(decisions, d) },
		AnalysisEvent: func(event AnalysisEvent) {
			analyses = append(analyses, event.Phase+" "+event.Package+" "+event.Detail)
		},
	}); err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 1 || decisions[0].Action != "skipped" || !strings.Contains(decisions[0].Reason, "changed between discovery and measurement") {
		t.Fatalf("decisions = %+v; want the drift skip", decisions)
	}
	joined := strings.Join(analyses, "\n")
	if !strings.Contains(joined, "baseline-output example.com/fixture/lib TestUnstableBaselineResult:") {
		t.Fatalf("analysis events = %q; want the drifted run's output beside the skip", analyses)
	}
}
