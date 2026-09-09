package cmd

import (
	"fmt"
	"path/filepath"

	"bytes"
	"context"
	"github.com/greatliontech/gomutant"
	"github.com/greatliontech/gomutant/internal/posturemod"
	"strings"
	"testing"
)

// The run's human report states each record's reuse posture beside its
// row and the summary's reuse line with the non-reusable roster: a
// target whose package graph shares mutated dynamic state measures and
// is stated not reusable with the freshness channel naming the
// variable and the stored observation beside it (discovery names the
// setter too, refused the same way), a clean one counts as reusable;
// the JSON face carries a posture object per record
// (REQ-result-run-posture).
//
//gofresh:pure
func TestRunReportStatesEachRecordsReusePosture(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	dir := posturemod.Write(t)
	var out bytes.Buffer
	if err := runCommand(context.Background(), runOptions{dir: dir, findingsFile: filepath.Join(dir, "findings.json"), budget: 4, output: &out}); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{
		"          reuse: unverifiable; freshness: ",
		"shares mutated dynamic state",
		"; analysis: no re-judgment lifts it",
		"reuse     1 reusable as they stand, 2 not\n",
		"; stored observation: subject reachability is not closed",
		"          example.com/posture/shared.Read  unverifiable; freshness: ",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("run report lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "clean.Add  ") && strings.Contains(text, "          example.com/posture/clean.Add  current") {
		t.Fatalf("a reusable record was listed as not reusable:\n%s", text)
	}
	var jsonOut bytes.Buffer
	if err := runCommand(context.Background(), runOptions{dir: dir, findingsFile: filepath.Join(dir, "findings.json"), budget: 4, output: &jsonOut, jsonl: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jsonOut.String(), `"posture"`) || !strings.Contains(jsonOut.String(), `"channel":"freshness"`) || !strings.Contains(jsonOut.String(), `"reusable":1`) {
		t.Fatalf("json face lacks the posture:\n%s", jsonOut.String())
	}
}

// The summary's roster caps with the remainder counted on the human
// face: the reuse line names the listed count when records are omitted.
//
//gofresh:pure
func TestReuseLineNamesTheOmittedRemainder(t *testing.T) {
	var summary gomutant.RunSummary
	postures := map[string]gomutant.RecordPosture{}
	for i := 0; i < gomutant.PostureCap+2; i++ {
		symbol := fmt.Sprintf("p.F%02d", i)
		postures[symbol] = gomutant.RecordPosture{Symbol: symbol, Measurement: "measured", Reuse: gomutant.FindingStale, Reasons: []gomutant.PostureReason{{Channel: gomutant.PostureFreshness, Reason: "moved"}}, Analysis: gomutant.AnalysisRemeasure}
	}
	postures["p.Ok"] = gomutant.RecordPosture{Symbol: "p.Ok", Measurement: "cached", Reuse: gomutant.FindingCurrent}
	summary.AddPostures(postures)
	var out bytes.Buffer
	renderReusePosture(&out, summary)
	text := out.String()
	if !strings.HasPrefix(text, fmt.Sprintf("reuse     1 reusable as they stand, %d not (%d listed)\n", gomutant.PostureCap+2, gomutant.PostureCap)) || strings.Count(text, "\n") != gomutant.PostureCap+1 {
		t.Fatalf("reuse lines:\n%s", text)
	}
}
