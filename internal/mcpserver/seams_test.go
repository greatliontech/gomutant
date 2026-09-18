package mcpserver

import (
	"os"
	"testing"

	"github.com/greatliontech/gomutant"
)

// productionSeams is the seams value the server reads, snapshotted at
// package init — after seams itself by the initialization order — so
// the pin judges the variable production reads and no sibling's
// leaked stub reaches it.
var productionSeams = seams

// The server reads no observer, the production pace, and stderr
// through the seams: a default that differs would make the server a
// different program in production than under test.
func TestServerSeamsDefaultToProduction(t *testing.T) {
	d := productionSeams
	if d.afterCommit != nil || d.afterFinalReplacement != nil || d.stretchObserver != nil || d.selectionObserver != nil {
		t.Fatal("an observer is installed by default")
	}
	if d.heartbeatInterval != gomutant.ProgressCadence {
		t.Fatalf("heartbeat pace = %s, want the progress cadence", d.heartbeatInterval)
	}
	// By descriptor, not identity: under `go test -json` the testing
	// package reassigns os.Stderr, so the file the init-time default
	// captured is no longer the variable's value.
	if f, ok := d.exitLogNotice.(*os.File); !ok || f.Fd() != 2 {
		t.Fatalf("the exit-log notice defaults to %T (fd %v), want the stderr descriptor", d.exitLogNotice, func() any {
			if ok {
				return f.Fd()
			}
			return "n/a"
		}())
	}
}
