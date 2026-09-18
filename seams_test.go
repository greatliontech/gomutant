package gomutant

import (
	"reflect"
	"testing"
	"time"

	"github.com/greatliontech/gomutant/internal/engine"
)

// productionSeams is the seams value production reads, snapshotted at
// package init — after seams itself by the initialization order — so
// the pin judges the variable the run reads, not the constructor, and
// no sibling's leaked stub reaches it.
var productionSeams = seams

// Production reads the engine's own functions and zero knobs through
// the seams: a default that is not the engine's would make the run a
// different program in production than under test.
func TestSeamsDefaultToTheEngine(t *testing.T) {
	d := productionSeams
	// A func value's code pointer identifies a top-level function
	// (two fields sharing one engine function share it by design).
	same := func(name string, got, want any) {
		t.Helper()
		if reflect.ValueOf(got).Pointer() != reflect.ValueOf(want).Pointer() {
			t.Fatalf("%s defaults to a function other than the engine's", name)
		}
	}
	same("coveredPositions", d.coveredPositions, engine.CoveredPositions)
	same("testProbe", d.testProbe, engine.TestProbeEnv)
	same("runMutantEvidence", d.runMutantEvidence, engine.RunMutantEvidenceEnv)
	same("runMutantObserved", d.runMutantObserved, engine.RunMutantObservedEnv)
	same("runMutantShaped", d.runMutantShaped, engine.RunMutantBaselineDirEnv)
	same("baselineProbe", d.baselineProbe, engine.TestProbeObservedEnv)
	same("advisoryProbe", d.advisoryProbe, engine.TestProbeObservedEnv)
	same("killGround", d.killGround, engine.TestProbeObservedEnv)
	if d.probeGateInstalled != nil || d.subjectViewBuild != nil || d.observedUnion != nil || d.inspectionSupplementaryView != nil {
		t.Fatal("an observer is installed by default")
	}
	if d.windowCandidates != 0 || d.waitPreparedBeforePick || d.truncateAfterItems != 0 || d.truncateErr != nil {
		t.Fatalf("a knob is set by default: %+v", d)
	}
	if d.ephemeralBudgetFloor != 60*time.Second {
		t.Fatalf("ephemeral budget floor = %s, want the retired 60s", d.ephemeralBudgetFloor)
	}
}
