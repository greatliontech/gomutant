package cmd

import (
	"bytes"
	"strings"
	"testing"

	gomutant "github.com/greatliontech/gomutant"
)

// The cause leads: an unreusable record's reason renders before its open
// survivors, and the trailing line counts the layers
// (REQ-result-inspection, REQ-result-layers).
func TestRenderFindingViewsLeadsWithTheCause(t *testing.T) {
	var out bytes.Buffer
	renderFindingViews(&out, []findingView{{
		Symbol: "p.F", State: gomutant.FindingUnverifiable,
		Reason: "oracle p.TestF: observation bracket moved: /dev/pts/3",
		Layer:  "local", LayerReason: "runtime-unverifiable evidence for p.TestF",
		Open: []gomutant.Survivor{{Position: "f.go:1:1", Operator: "zero return"}},
	}})
	text := out.String()
	cause := strings.Index(text, "cause: oracle p.TestF")
	survivor := strings.Index(text, "survivor f.go:1:1")
	if cause < 0 || survivor < 0 || cause > survivor {
		t.Fatalf("cause does not lead the survivors:\n%s", text)
	}
	if !strings.Contains(text, "machine-local: runtime-unverifiable evidence") || !strings.Contains(text, "0 repo-committable, 1 machine-local") {
		t.Fatalf("layer surface missing:\n%s", text)
	}
}

// The default findings render is one summary row per record - state,
// symbol, layer, open and attested counts, the cause when the record
// cannot serve - with the lists behind --detail
// (REQ-result-inspection).
func TestRenderFindingSummariesIsOneRowPerRecord(t *testing.T) {
	var out bytes.Buffer
	renderFindingSummaries(&out, []findingView{
		{
			Symbol: "p.F", State: gomutant.FindingStale,
			Reason: "oracle p.TestF: subject identity changed",
			Layer:  "local", LayerReason: "runtime-unverifiable evidence",
			Open: []gomutant.Survivor{{Position: "f.go:1:1", Operator: "zero return"}},
		},
		{Symbol: "p.G", State: gomutant.FindingCurrent, Layer: "repo",
			Attested: []gomutant.Attestation{{Position: "g.go:1:1", Operator: "op", Reason: "equivalent"}}},
	})
	text := out.String()
	if !strings.Contains(text, "stale  p.F  [machine-local]  1 open, 0 attested  (oracle p.TestF: subject identity changed)") {
		t.Fatalf("summary row missing state, layer, counts, or cause:\n%s", text)
	}
	if !strings.Contains(text, "current  p.G  [repo]  0 open, 1 attested") {
		t.Fatalf("current row missing:\n%s", text)
	}
	if strings.Contains(text, "survivor f.go:1:1") || strings.Contains(text, "attested g.go:1:1") {
		t.Fatalf("summary leaked detail lists:\n%s", text)
	}
	if !strings.Contains(text, "1 repo-committable, 1 machine-local; --detail for survivors and dispositions") {
		t.Fatalf("layer totals or detail pointer missing:\n%s", text)
	}
}
