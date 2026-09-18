package cmd

import (
	"testing"
	"time"

	"github.com/greatliontech/gomutant"
)

// productionSeams is the seams value the commands read, snapshotted at
// package init — after seams itself by the initialization order — so
// the pin judges the variable production reads and no sibling's
// leaked stub reaches it.
var productionSeams = seams

// The commands read no observer and the production paces through the
// seams: a default that differs would make the CLI a different program
// in production than under test.
func TestCommandSeamsDefaultToProduction(t *testing.T) {
	d := productionSeams
	if d.afterFinalReplacement != nil {
		t.Fatal("an observer is installed by default")
	}
	if d.progressInterval != gomutant.ProgressCadence || d.sigtermDrainDeadline != 5*time.Second || d.postCommitRenderBound != gomutant.PostCommitRenderBound {
		t.Fatalf("paces = %s / %s / render bound %s, want the progress cadence, the five-second SIGTERM drain, and the library's render bound", d.progressInterval, d.sigtermDrainDeadline, d.postCommitRenderBound)
	}
}
