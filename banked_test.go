package gomutant

import (
	"strings"
	"testing"
	"time"
)

// The banked state names each committed record's layer: the findings
// document's share and the machine-local share, in one sentence, so
// a reader checking the document reads the document's share alone
// there (REQ-exec-banked-summary).
func TestBankedStateNamesTheLayerOfEveryCommit(t *testing.T) {
	for _, tt := range []struct {
		committed, local int
		want             string
	}{
		{0, 0, "0 target(s) committed this run — none to the findings document"},
		{3, 0, "3 target(s) committed this run — 3 to the findings document, none machine-local"},
		{3, 3, "3 target(s) committed this run — every one machine-local (kept beside the findings document, none in it)"},
		{5, 2, "5 target(s) committed this run — 3 to the findings document, 2 machine-local (kept beside it)"},
	} {
		b := RunTallies{Committed: tt.committed, CommittedLocal: tt.local, Selected: 9}.Banked("command timeout", 3*time.Second)
		if b.Local != tt.local {
			t.Fatalf("Local = %d, want %d", b.Local, tt.local)
		}
		if got := b.Text(); !strings.Contains(got, tt.want) {
			t.Fatalf("Text() = %q, want it to carry %q", got, tt.want)
		}
	}
}
