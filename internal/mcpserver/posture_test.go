package mcpserver

import (
	"context"
	"github.com/greatliontech/gomutant/internal/posturemod"
	"strings"
	"testing"

	"github.com/greatliontech/gomutant"
)

// The run tool's rows carry each record's reuse posture and the
// summary counts the reusable records and lists the rest with their
// refusing channel, so a consumer reading counts and layer alone never
// takes a committed record for reusable evidence (REQ-result-run-posture).
//
//gofresh:pure
func TestRunToolStatesEachRecordsReusePosture(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	dir := posturemod.Write(t)
	s := New(dir)
	_, run, err := s.toolRun(context.Background(), nil, runIn{Budget: 4})
	if err != nil {
		t.Fatal(err)
	}
	// Discovery names Read, Set, and Add; the two sharing the hook are
	// refused, in symbol order.
	if run.Summary.Reusable != 1 || len(run.Summary.NotReusable) != 2 || run.Summary.NotReusable[0].Symbol != "example.com/posture/shared.Read" || run.Summary.NotReusable[1].Symbol != "example.com/posture/shared.Set" {
		t.Fatalf("summary posture = %+v", run.Summary)
	}
	rows := map[string]findingOut{}
	for _, row := range run.Findings {
		rows[row.Symbol] = row
	}
	shared, clean := rows["example.com/posture/shared.Read"], rows["example.com/posture/clean.Add"]
	if shared.Reuse != string(gomutant.FindingUnverifiable) || len(shared.Reasons) == 0 || shared.Reasons[0].Channel != gomutant.PostureFreshness ||
		!strings.Contains(shared.Reasons[0].Reason, "shares mutated dynamic state") || shared.Analysis != gomutant.AnalysisSource ||
		len(shared.Reasons) != 2 || shared.Reasons[1].Channel != gomutant.PostureStoredObservation || !strings.Contains(shared.Reasons[1].Reason, "reachability is not closed") {
		t.Fatalf("shared row = %+v", shared)
	}
	if clean.Reuse != string(gomutant.FindingCurrent) || len(clean.Reasons) != 0 || clean.Analysis != "" {
		t.Fatalf("clean row = %+v", clean)
	}
}
