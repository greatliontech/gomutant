package cmd

import (
	"strings"
	"testing"

	gomutant "github.com/greatliontech/gomutant"
)

// The CLI face renders a confirmation flip as its own loud line naming
// the mutant and the withdrawn killer — never the generic phase line
// with zeroed tallies (REQ-exec-run-status's confirmation-flip class).
func TestRenderExecutionEventFlipLine(t *testing.T) {
	var out strings.Builder
	renderExecutionEvent(&out, gomutant.ExecutionEvent{
		Phase: "confirmation-flip", Symbol: "example.com/m.Pager.flushBump",
		FlipPosition: "pager.go:695:44", FlipKiller: "TestUnitBumpPacking",
	})
	line := out.String()
	if !strings.Contains(line, "confirmation FLIP") ||
		!strings.Contains(line, "pager.go:695:44") ||
		!strings.Contains(line, "TestUnitBumpPacking") ||
		strings.Contains(line, "target 0/0") {
		t.Fatalf("flip line = %q, want the loud named demotion", line)
	}
}
