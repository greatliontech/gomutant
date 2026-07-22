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
		Open:   []gomutant.Survivor{{Position: "f.go:1:1", Operator: "zero return"}},
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
