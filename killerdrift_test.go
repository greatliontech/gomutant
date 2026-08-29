package gomutant

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gomutant/internal/engine"
)

func declaration(file, kind, name, receiver string, references ...string) gofresh.TestVariantDeclaration {
	return gofresh.TestVariantDeclaration{File: file, Kind: kind, Name: name, Receiver: receiver, Hash: "h-" + kind + "-" + name, Package: "p", References: references}
}

// The killer-drift license admits exactly the delta kinds whose observation
// routes through a reference chain: plain functions (never TestMain),
// methods of compartment-declared receiver types, consts, and types. The
// rejected kinds each reach unchanged tests without any reference
// (REQ-result-stale's killer-drift carve-out).
func TestKillerDriftAttributableClassifiesDeltaKinds(t *testing.T) {
	compartment := gofresh.TestVariantLedger{Declarations: []gofresh.TestVariantDeclaration{
		declaration("a_test.go", "type", "suite", ""),
		declaration("a_test.go", "type", "collide", ""),
	}}
	cases := []struct {
		name  string
		delta gofresh.TestVariantDelta
		want  bool
	}{
		{"changed plain func", gofresh.TestVariantDelta{Changed: []gofresh.TestVariantDeclarationChange{{Before: declaration("a_test.go", "func", "helper", ""), After: declaration("a_test.go", "func", "helper", "")}}}, true},
		{"removed plain func", gofresh.TestVariantDelta{Removed: []gofresh.TestVariantDeclaration{declaration("a_test.go", "func", "helper", "")}}, true},
		{"added const and type", gofresh.TestVariantDelta{Added: []gofresh.TestVariantDeclaration{declaration("a_test.go", "const", "limit", ""), declaration("a_test.go", "type", "extra", "")}}, true},
		{"changed TestMain", gofresh.TestVariantDelta{Changed: []gofresh.TestVariantDeclarationChange{{Before: declaration("a_test.go", "func", "TestMain", ""), After: declaration("a_test.go", "func", "TestMain", "")}}}, false},
		{"changed var", gofresh.TestVariantDelta{Changed: []gofresh.TestVariantDeclarationChange{{Before: declaration("a_test.go", "var", "state", ""), After: declaration("a_test.go", "var", "state", "")}}}, false},
		{"added init", gofresh.TestVariantDelta{Added: []gofresh.TestVariantDeclaration{declaration("a_test.go", "init", "init", "")}}, false},
		{"removed directive", gofresh.TestVariantDelta{Removed: []gofresh.TestVariantDeclaration{declaration("a_test.go", "directive", "go:linkname", "")}}, false},
		{"method of compartment type", gofresh.TestVariantDelta{Changed: []gofresh.TestVariantDeclarationChange{{Before: declaration("a_test.go", "method", "run", "*suite"), After: declaration("a_test.go", "method", "run", "*suite")}}}, true},
		{"method of generic compartment type", gofresh.TestVariantDelta{Added: []gofresh.TestVariantDeclaration{declaration("a_test.go", "method", "run", "suite[T]")}}, true},
		{"method of production type", gofresh.TestVariantDelta{Added: []gofresh.TestVariantDeclaration{declaration("a_test.go", "method", "String", "*Gadget")}}, false},
		{"method rides a name collision with the other variant's type", gofresh.TestVariantDelta{Added: []gofresh.TestVariantDeclaration{func() gofresh.TestVariantDeclaration {
			d := declaration("b_test.go", "method", "Error", "collide")
			d.Package = "p_test" // the same-named type below is package p
			return d
		}()}}, false},
		{"method recorded without its package clause", gofresh.TestVariantDelta{Removed: []gofresh.TestVariantDeclaration{func() gofresh.TestVariantDeclaration {
			d := declaration("a_test.go", "method", "run", "*suite")
			d.Package = ""
			return d
		}()}}, false},
		{"embedded header movement", gofresh.TestVariantDelta{HeaderChanges: []gofresh.TestVariantHeaderChange{{File: "fixture.go", Before: "a", After: "b", Embedded: true}}}, false},
		{"plain header movement", gofresh.TestVariantDelta{HeaderChanges: []gofresh.TestVariantHeaderChange{{File: "a_test.go", Before: "a", After: "b"}}}, true},
	}
	for _, tc := range cases {
		if got := killerDriftAttributable(tc.delta, compartment, compartment); got != tc.want {
			t.Errorf("%s: attributable = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The reference walk observes a delta declaration through direct reference,
// a transitive helper chain, and a receiver-type edge to its methods —
// reflection's only route to a compartment function — while an oracle whose
// walk reaches nothing in the delta stays unmoved, an oracle with no
// compartment declaration is unknown, and a served function entry with no
// reference list fails closed (REQ-result-stale's killer-drift carve-out).
func TestCompartmentReachObservesDeltaDeclarations(t *testing.T) {
	current := gofresh.TestVariantLedger{Declarations: []gofresh.TestVariantDeclaration{
		declaration("a_test.go", "func", "TestDirect", "", "TestDirect", "t", "helper"),
		declaration("a_test.go", "func", "TestChained", "", "TestChained", "t", "middle"),
		declaration("a_test.go", "func", "middle", "", "middle", "helper"),
		declaration("a_test.go", "func", "helper", "", "helper"),
		declaration("a_test.go", "func", "TestViaType", "", "TestViaType", "t", "suite"),
		declaration("a_test.go", "type", "suite", "", "suite", "int"),
		declaration("a_test.go", "method", "hook", "*suite", "hook", "suite"),
		declaration("a_test.go", "func", "TestQuiet", "", "TestQuiet", "t", "calm"),
		declaration("a_test.go", "func", "calm", "", "calm"),
		declaration("a_test.go", "func", "TestBare", ""),
		declaration("a_test.go", "func", "TestConst", "", "TestConst", "t", "limit"),
		declaration("a_test.go", "func", "TestBlankGoverned", "", "TestBlankGoverned", "t", "blankKind"),
		declaration("a_test.go", "const", "blankKind", "", "_", "blankKind"),
		declaration("a_test.go", "const", "_", "", "helperB"),
	}}
	delta := gofresh.TestVariantDelta{
		Changed: []gofresh.TestVariantDeclarationChange{
			{Before: declaration("a_test.go", "func", "helper", ""), After: current.Declarations[3]},
			{Before: declaration("a_test.go", "method", "hook", "*suite"), After: current.Declarations[6]},
		},
		// A removed const has no current entry and no fail-closed empty
		// reference list (that guard covers functions and methods only):
		// only the removed entry riding into the graph as a terminal
		// touched node makes its removal observable.
		Removed: []gofresh.TestVariantDeclaration{declaration("a_test.go", "const", "limit", "")},
	}
	// The blank-named const governor changed: its empty-listed sibling
	// blankKind carries the "_" edge gofresh's fold serves, so a test
	// referencing only blankKind still observes the governor's movement.
	delta.Changed = append(delta.Changed, gofresh.TestVariantDeclarationChange{
		Before: declaration("a_test.go", "const", "_", "", "helperA"),
		After:  current.Declarations[len(current.Declarations)-1],
	})
	reach := newCompartmentReach(current, delta)
	for name, want := range map[string]bool{
		"TestDirect":        true,  // references the changed helper by name
		"TestChained":       true,  // reaches it through middle
		"TestViaType":       true,  // reaches the changed method through its receiver type
		"TestConst":         true,  // references a removed const, visitable only as a removed node
		"TestBlankGoverned": true,  // reaches the changed blank governor through the folded "_" edge
		"TestQuiet":         false, // its chain touches nothing in the delta
	} {
		reached, known := reach.reaches(name)
		if !known || reached != want {
			t.Errorf("%s: reached=%v known=%v, want reached=%v known=true", name, reached, known, want)
		}
	}
	if _, known := reach.reaches("TestAbsent"); known {
		t.Errorf("TestAbsent: known=true, want unknown (no compartment declaration)")
	}
	if reached, known := reach.reaches("TestBare"); !known || !reached {
		t.Errorf("TestBare: reached=%v known=%v, want the empty reference list to fail closed as reached", reached, known)
	}
}

// An unchanged unconditional root — a package var's initializer, an init
// function, or TestMain — reaching a delta declaration runs changed code
// around every test without any oracle naming it, so the walk must surface
// it and the carve-out must refuse (REQ-result-stale's killer-drift
// carve-out).
func TestCompartmentReachFlagsUnconditionalRoots(t *testing.T) {
	current := gofresh.TestVariantLedger{Declarations: []gofresh.TestVariantDeclaration{
		declaration("a_test.go", "var", "_", "", "seed"),
		declaration("a_test.go", "func", "seed", "", "registry", "seed"),
		declaration("a_test.go", "var", "registry", "", "registry", "string"),
		declaration("a_test.go", "func", "TestKiller", "", "TestKiller", "registry", "t"),
	}}
	delta := gofresh.TestVariantDelta{Changed: []gofresh.TestVariantDeclarationChange{
		{Before: declaration("a_test.go", "func", "seed", ""), After: current.Declarations[1]},
	}}
	reach := newCompartmentReach(current, delta)
	if !reach.unconditionalRootReaches() {
		t.Fatalf("var initializer reaching the changed seed not surfaced")
	}
	// The oracle itself never reaches seed, so without the root check its
	// kill would falsely stand.
	if reached, known := reach.reaches("TestKiller"); !known || reached {
		t.Fatalf("TestKiller walk: reached=%v known=%v — the root, not the oracle, observes the delta", reached, known)
	}

	// A quiet delta no root reaches leaves the roots silent.
	quiet := gofresh.TestVariantDelta{Changed: []gofresh.TestVariantDeclarationChange{
		{Before: declaration("a_test.go", "func", "TestKiller", ""), After: current.Declarations[3]},
	}}
	if newCompartmentReach(current, quiet).unconditionalRootReaches() {
		t.Fatalf("roots flagged a delta they cannot reach")
	}

	// TestMain is a root exactly like initializers.
	withMain := gofresh.TestVariantLedger{Declarations: append(append([]gofresh.TestVariantDeclaration(nil), current.Declarations...),
		declaration("a_test.go", "func", "TestMain", "", "TestMain", "m", "seed"))}
	if !newCompartmentReach(withMain, delta).unconditionalRootReaches() {
		t.Fatalf("TestMain reaching the delta not surfaced")
	}
}

// A removed method is still observable through its receiver type: the walk
// visits the removed entry as a terminal node, so a dynamic type assertion
// that never wrote the method's name cannot silently keep a kill standing.
func TestCompartmentReachSeesRemovedMethodThroughReceiver(t *testing.T) {
	current := gofresh.TestVariantLedger{Declarations: []gofresh.TestVariantDeclaration{
		declaration("a_test.go", "func", "TestViaType", "", "TestViaType", "t", "suite"),
		declaration("a_test.go", "type", "suite", "", "suite", "int"),
	}}
	removed := declaration("a_test.go", "method", "hook", "*suite")
	delta := gofresh.TestVariantDelta{Removed: []gofresh.TestVariantDeclaration{removed}}
	reach := newCompartmentReach(current, delta)
	if reached, known := reach.reaches("TestViaType"); !known || !reached {
		t.Fatalf("removed method via receiver: reached=%v known=%v, want reached", reached, known)
	}
}

// driftRemeasureIndexes selects every survivor when anything moved or the
// derived set grew, every kill whose killer moved, every timeout or
// package-scope kill when anything moved (a purely grown set can only extend
// the recorded set's behavior, so those stand), and every candidate carrying
// recorded candidate evidence, while kills keyed to unmoved oracles and
// unflagged discards stand; a kill identity regeneration cannot re-identify
// refuses the serve (REQ-result-stale's killer-drift carve-out).
func TestDriftRemeasureIndexesSelectsMovedEvidence(t *testing.T) {
	runnable := []engine.Replacement{{File: "f.go", Source: []byte("x")}}
	generation := engine.Generation{CandidateCount: 6, Candidates: []engine.Candidate{
		{Symbol: "p.F", Operator: "op-a", Position: "f.go:1:1", Replacements: runnable}, // kill by unmoved
		{Symbol: "p.F", Operator: "op-a", Position: "f.go:2:2", Replacements: runnable}, // kill by moved
		{Symbol: "p.F", Operator: "op-a", Position: "f.go:3:3", Replacements: runnable}, // timeout kill
		{Symbol: "p.F", Operator: "op-a", Position: "f.go:4:4", Replacements: runnable}, // package kill
		{Symbol: "p.F", Operator: "op-a", Position: "f.go:5:5", Replacements: runnable}, // survivor
		{Symbol: "p.F", Operator: "op-a", Position: "f.go:6:6", Replacements: runnable}, // recorded discard
	}}
	rec := Finding{
		CandidateCount: 6, Generated: 6, Killed: 4, Mutants: 5, Discarded: 1,
		Kills: []Kill{
			{Position: "f.go:1:1", Operator: "op-a", Killer: "p.TestSteady"},
			{Position: "f.go:2:2", Operator: "op-a", Killer: "p.TestMoved"},
			{Position: "f.go:3:3", Operator: "op-a", Killer: TimeoutKiller},
			{Position: "f.go:4:4", Operator: "op-a", Killer: PackageKillerPrefix + "p)"},
		},
		Survivors: []Survivor{{Position: "f.go:5:5", Operator: "op-a"}},
	}
	remeasure, survivorScoped, stand, flagged, ok := driftRemeasureIndexes(generation, rec, []string{"p.TestMoved"}, nil)
	if !ok || stand != 1 || flagged != 0 {
		t.Fatalf("remeasure=%v stand=%d flagged=%d ok=%v, want ok with 1 standing kill", remeasure, stand, flagged, ok)
	}
	wantIndexes := map[int]bool{1: true, 2: true, 3: true, 4: true}
	if len(remeasure) != len(wantIndexes) {
		t.Fatalf("remeasure = %v, want %v", remeasure, wantIndexes)
	}
	for i := range wantIndexes {
		if !remeasure[i] {
			t.Fatalf("remeasure = %v, want %v", remeasure, wantIndexes)
		}
	}
	// The survivor's re-measure narrows to the added and moved tests —
	// its recorded passes on unmoved oracles stand like standing kills —
	// while moved-killer and set-wide kills keep the full oracle.
	if len(survivorScoped) != 1 || !survivorScoped[4] {
		t.Fatalf("survivorScoped = %v, want only the survivor narrowed", survivorScoped)
	}

	// Nothing moved: no oracle's behavior changed, so survivals stand
	// exactly like kills and nothing re-measures.
	remeasure, survivorScoped, stand, flagged, ok = driftRemeasureIndexes(generation, rec, nil, nil)
	if !ok || stand != 4 || flagged != 0 || len(remeasure) != 0 || len(survivorScoped) != 0 {
		t.Fatalf("no-movement remeasure=%v stand=%d ok=%v, want nothing re-measured", remeasure, stand, ok)
	}

	// The set grew with nothing moved: every kill stands — the set-wide
	// kills too, a purely grown oracle only extends the recorded set's
	// behavior — and every survivor re-measures (an added test may kill
	// it).
	remeasure, survivorScoped, stand, flagged, ok = driftRemeasureIndexes(generation, rec, nil, []string{"p.TestNew"})
	if !ok || stand != 4 || flagged != 0 || len(remeasure) != 1 || !remeasure[4] {
		t.Fatalf("grown-set remeasure=%v stand=%d ok=%v, want only the survivor re-measured", remeasure, stand, ok)
	}
	if len(survivorScoped) != 1 || !survivorScoped[4] {
		t.Fatalf("grown-set survivorScoped = %v, want the survivor narrowed to the added test", survivorScoped)
	}

	// Candidate evidence composes: the flagged kill re-executes even with
	// its killer unmoved, the flagged discard re-executes — the evidence
	// loop is its ONLY route into the re-measure set — and the standing
	// count drops accordingly.
	evidenced := rec
	evidenced.CandidateEvidence = []CandidateEvidence{
		{Position: "f.go:1:1", Operator: "op-a", Reason: "runtime inputs unverifiable", Disposition: "killed"},
		{Position: "f.go:6:6", Operator: "op-a", Reason: "mutant test process timed out", Disposition: "discarded"},
	}
	remeasure, survivorScoped, stand, flagged, ok = driftRemeasureIndexes(generation, evidenced, nil, nil)
	if !ok || stand != 3 || flagged != 2 || len(remeasure) != 2 || !remeasure[0] || !remeasure[5] {
		t.Fatalf("flagged remeasure=%v stand=%d flagged=%d ok=%v, want the flagged kill and discard re-executed", remeasure, stand, flagged, ok)
	}
	if len(survivorScoped) != 0 {
		t.Fatalf("flagged-only survivorScoped = %v, want none: flagged candidates keep the full oracle", survivorScoped)
	}

	// A flagged survivor under a grown set re-measures through its
	// evidence, never through the narrowing: its recorded passes are the
	// unverifiable evidence being re-established, so it keeps the full
	// current oracle while an unflagged survivor beside it narrows.
	flaggedSurvivor := rec
	flaggedSurvivor.CandidateEvidence = []CandidateEvidence{
		{Position: "f.go:5:5", Operator: "op-a", Reason: "runtime inputs unverifiable", Disposition: "survived"},
	}
	remeasure, survivorScoped, stand, flagged, ok = driftRemeasureIndexes(generation, flaggedSurvivor, nil, []string{"p.TestNew"})
	if !ok || stand != 4 || flagged != 1 || len(remeasure) != 1 || !remeasure[4] {
		t.Fatalf("flagged-survivor remeasure=%v stand=%d flagged=%d ok=%v, want the survivor re-measured through its evidence", remeasure, stand, flagged, ok)
	}
	if len(survivorScoped) != 0 {
		t.Fatalf("flagged-survivor survivorScoped = %v, want none: the flagged survivor keeps the full oracle", survivorScoped)
	}

	// A kill regeneration cannot re-identify refuses the serve.
	misplaced := rec
	misplaced.Kills = append([]Kill(nil), rec.Kills...)
	misplaced.Kills[0].Position = "f.go:9:9"
	if _, _, _, _, ok := driftRemeasureIndexes(generation, misplaced, nil, nil); ok {
		t.Fatalf("unidentifiable kill served under drift")
	}

	// A flagged identity regeneration cannot re-identify refuses too.
	strayEvidence := rec
	strayEvidence.CandidateEvidence = []CandidateEvidence{
		{Position: "f.go:9:9", Operator: "op-a", Reason: "runtime inputs unverifiable", Disposition: "killed"},
	}
	if _, _, _, _, ok := driftRemeasureIndexes(generation, strayEvidence, nil, nil); ok {
		t.Fatalf("unidentifiable flagged candidate served under drift")
	}
}

// driftFindingCounts rescoring: standing kills carry their recorded killer,
// a re-measured kill that survives moves to the open set, a re-measured
// survivor a test now kills records its fresh killer, counts conserve, and a
// newly killed attested survivor sheds its attestation
// (INV-RESULT-CANDIDATE-CONSERVATION, REQ-attest-survivor).
func TestDriftFindingCountsRescoresRemeasured(t *testing.T) {
	runnable := []engine.Replacement{{File: "f.go", Source: []byte("x")}}
	candidates := []engine.Candidate{
		{Symbol: "p.F", Operator: "op-a", Position: "f.go:1:1", Replacements: runnable}, // standing kill
		{Symbol: "p.F", Operator: "op-a", Position: "f.go:2:2", Replacements: runnable}, // moved kill -> survives
		{Symbol: "p.F", Operator: "op-b", Position: "f.go:3:3", Replacements: runnable}, // survivor -> killed, attested
		{Symbol: "p.F", Operator: "op-b", Position: "f.go:4:4", Replacements: runnable}, // survivor -> survives
		{Symbol: "p.F", Operator: "op-b", Position: "f.go:5:5", Replacements: runnable}, // recorded discard, stands
		{Symbol: "p.F", Operator: "op-b", Position: "f.go:6:6", Replacements: runnable}, // standing survivor, carries its bucket
	}
	rec := Finding{
		Symbol: "p.F", CandidateCount: 6, Generated: 6, Mutants: 5, Killed: 2, Discarded: 1,
		Operators: []OperatorSummary{
			{Operator: "op-a", Generated: 2, Killed: 2},
			{Operator: "op-b", Generated: 4, Survived: 3, Discarded: 1},
		},
		Kills: []Kill{
			{Position: "f.go:1:1", Operator: "op-a", Killer: "p.TestSteady"},
			{Position: "f.go:2:2", Operator: "op-a", Killer: "p.TestMoved"},
		},
		Survivors: []Survivor{
			{Position: "f.go:3:3", Operator: "op-b", Execution: "never-executed"},
			{Position: "f.go:4:4", Operator: "op-b", Execution: "executed-and-passed"},
			{Position: "f.go:6:6", Operator: "op-b", Execution: "never-executed"},
		},
		Attested: []Attestation{
			{Position: "f.go:3:3", Operator: "op-b", Reason: "wrongly judged"},
			{Position: "f.go:4:4", Operator: "op-b", Reason: "still equivalent"},
		},
	}
	remeasured := map[int]bool{1: true, 2: true, 3: true, 4: true}
	outcomes := []engine.MutantOutcome{0, engine.MutantSurvived, engine.MutantKilled, engine.MutantSurvived, engine.MutantDiscarded, 0}
	killers := []string{"", "", "p.TestMoved", "", "", ""}
	// Index 4 is a recorded discard re-executing through the
	// candidate-evidence composition; it discards again, conserving its
	// recorded disposition through a fresh execution.
	drifted, shed, err := driftFindingCounts(context.Background(), rec, candidates, remeasured, windowScores{outcomes: outcomes, killers: killers, memoryDecided: []bool{true}}, nil, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !drifted.OracleCeilingDecided {
		t.Fatal("drift dropped the window's memory-decided fact")
	}
	if drifted.Killed != 2 || drifted.Discarded != 1 || drifted.Mutants != 5 ||
		drifted.Generated != 6 || drifted.CandidateCount != 6 {
		t.Fatalf("drifted totals = %+v, want conservation across the rescoring", drifted)
	}
	wantOperators := []OperatorSummary{
		{Operator: "op-a", Generated: 2, Killed: 1, Survived: 1},
		{Operator: "op-b", Generated: 4, Killed: 1, Survived: 2, Discarded: 1},
	}
	if !slices.Equal(drifted.Operators, wantOperators) {
		t.Fatalf("drifted operators = %+v, want %+v", drifted.Operators, wantOperators)
	}
	wantKills := []Kill{
		{Position: "f.go:1:1", Operator: "op-a", Killer: "p.TestSteady"},
		{Position: "f.go:3:3", Operator: "op-b", Killer: "p.TestMoved"},
	}
	if !slices.Equal(drifted.Kills, wantKills) {
		t.Fatalf("drifted kills = %+v, want %+v", drifted.Kills, wantKills)
	}
	wantSurvivors := []Survivor{
		{Position: "f.go:2:2", Operator: "op-a"},
		{Position: "f.go:4:4", Operator: "op-b"},
		{Position: "f.go:6:6", Operator: "op-b", Execution: "never-executed"},
	}
	if !slices.Equal(drifted.Survivors, wantSurvivors) {
		t.Fatalf("drifted survivors = %+v, want the flipped kill, the re-measured survivor unbucketed, and the standing survivor with its recorded bucket", drifted.Survivors)
	}
	if len(drifted.Attested) != 1 || drifted.Attested[0].Position != "f.go:4:4" {
		t.Fatalf("drifted attestations = %+v, want only the still-surviving disposition", drifted.Attested)
	}
	if len(shed) != 1 || shed[0].Position != "f.go:3:3" || shed[0].Reason != "wrongly judged" {
		t.Fatalf("shed attestations = %+v, want the newly killed disposition", shed)
	}
}

// TestParseFindingsKillAttribution pins the persisted kill-attribution
// encoding (REQ-core-attributed-kills, REQ-result-record, REQ-result-export):
// a complete kill list round-trips and its absence is tolerated, while a
// partial list, a duplicate identity, an empty killer, or a kill naming a
// survivor is refused.
func TestParseFindingsKillAttribution(t *testing.T) {
	valid := `{"version":4,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/12","budget":0,"targetEvidence":{"symbol":"p.F","maximalClosure":"c","testVariantClosure":"tv","toolchain":"go","buildConfig":"b","observationAssertion":"caller assertion","observationStrategy":"gofresh/observation-rta@2","observationSubjectPackage":"p","observationSubjectSymbol":"F","observationObservable":true,"observationEvidence":"proof","runtimeInputs":"m","runtimeDigest":"d"},"oracleEvidence":[{"symbol":"p.TestF","maximalClosure":"tc","testVariantClosure":"tv","toolchain":"go","buildConfig":"b","observationAssertion":"caller assertion","observationStrategy":"gofresh/observation-rta@2","observationSubjectPackage":"p","observationSubjectSymbol":"TestF","observationObservable":true,"observationEvidence":"proof","runtimeInputs":"m","runtimeDigest":"d"}],"oracleExplicit":true,"oracleTimeout":"1m0s","dirty":true,"candidateCount":3,"generated":3,"mutants":3,"killed":2,"discarded":0,"operators":[{"operator":"op","generated":3,"discarded":0,"killed":2,"survived":1}],"kills":[{"position":"f.go:1:1","operator":"op","killer":"p.TestF"},{"position":"f.go:3:3","operator":"op","killer":"(timeout)"}],"survivors":[{"position":"f.go:2:2","operator":"op"}]}]}`
	findings, err := ParseFindings([]byte(valid))
	if err != nil || len(findings) != 1 || len(findings[0].Kills) != 2 ||
		findings[0].Kills[0].Killer != "p.TestF" {
		t.Fatalf("valid kill attribution refused: %v %+v", err, findings)
	}
	tolerated := strings.Replace(valid, `"kills":[{"position":"f.go:1:1","operator":"op","killer":"p.TestF"},{"position":"f.go:3:3","operator":"op","killer":"(timeout)"}],`, "", 1)
	if findings, err := ParseFindings([]byte(tolerated)); err != nil || len(findings[0].Kills) != 0 {
		t.Fatalf("attribution-free record refused: %v", err)
	}
	killEntry := `{"position":"f.go:1:1","operator":"op","killer":"p.TestF"}`
	for name, doc := range map[string]string{
		"partial list":       strings.Replace(valid, killEntry+",", "", 1),
		"duplicate identity": strings.Replace(valid, `{"position":"f.go:3:3","operator":"op","killer":"(timeout)"}`, killEntry, 1),
		"empty killer":       strings.Replace(valid, `"killer":"p.TestF"`, `"killer":""`, 1),
		"kill names a survivor": strings.Replace(valid,
			`"kills":[{"position":"f.go:1:1"`, `"kills":[{"position":"f.go:2:2"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseFindings([]byte(doc)); err == nil {
				t.Fatal("malformed kill attribution accepted")
			}
		})
	}
}

// TestRunServesDriftedKillsKeyedToUnmovedOracles pins the killer-drift
// carve-out end to end (REQ-result-stale, REQ-core-attributed-kills): a
// fresh measurement persists complete kill attribution; editing a helper
// only one oracle reaches serves the sibling's kills while re-measuring the
// moved oracle's kills and every survivor against the full current oracle; a
// delta reaching no oracle serves everything with nothing re-measured; and
// an initialization-bearing delta refuses the arm outright, the whole
// target re-measuring.
func TestRunServesDriftedKillsKeyedToUnmovedOracles(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the oracle per mutant")
	}
	dir := t.TempDir()
	const testSource = "package gated\n\nimport \"testing\"\n\nvar _ = prime()\n\nfunc prime() int {\n\treturn 1\n}\n\nfunc sink(v int) int {\n\treturn v\n}\n\nfunc TestSmall(t *testing.T) {\n\tif Gated(5) != 6 {\n\t\tt.Fail()\n\t}\n}\n\nfunc TestAux(t *testing.T) {\n\tif sink(Gated(200)) != 603 {\n\t\tt.Fail()\n\t}\n}\n"
	files := map[string]string{
		"go.mod":        "module example.com/drifted\n\ngo 1.26\n",
		"gated.go":      "package gated\n\nfunc Gated(x int) int {\n\ty := x + 1\n\tif y > 100 {\n\t\treturn y * 3\n\t}\n\treturn y\n}\n",
		"gated_test.go": testSource,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	target := Target{Symbol: "example.com/drifted.Gated"}
	first, err := tr.Run(ctx, []Target{target}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	f := first[0]
	if f.Killed == 0 || len(f.Survivors) == 0 || f.CompartmentLedger == nil {
		t.Fatalf("baseline fixture = %+v, want kills, survivors, and a ledger", f)
	}
	if len(f.Kills) != f.Killed {
		t.Fatalf("fresh measurement persisted %d attributions for %d kills", len(f.Kills), f.Killed)
	}
	auxKills, smallKills := 0, 0
	for _, kill := range f.Kills {
		switch kill.Killer {
		case "example.com/drifted.TestAux":
			auxKills++
		case "example.com/drifted.TestSmall":
			smallKills++
		case TimeoutKiller:
		default:
			t.Fatalf("kill %+v names an unexpected killer", kill)
		}
	}
	if auxKills == 0 || smallKills == 0 {
		t.Fatalf("fixture kills = %+v, want both oracles attributed so the drift split has teeth", f.Kills)
	}
	doc, err := Export(first)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := ParseFindings(doc)
	if err != nil {
		t.Fatal(err)
	}

	// Edit the helper only TestAux reaches: TestAux's kills and every
	// survivor re-measure, TestSmall's kills stand.
	if err := os.WriteFile(filepath.Join(dir, "gated_test.go"), []byte(strings.Replace(testSource, "return v\n", "w := v\n\treturn w\n", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	driftTree, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var decisions []RunDecision
	var dispatched []int
	scopesByCandidate := map[survivorKey][][]string{}
	driftedFindings, err := driftTree.Run(ctx, []Target{target}, Options{
		Prior:      prior,
		Decision:   func(d RunDecision) { decisions = append(decisions, d) },
		dispatched: func(_ string, mi int) { dispatched = append(dispatched, mi) },
		executedScope: func(position, operator string, scope []string) {
			key := survivorKey{position, operator}
			scopesByCandidate[key] = append(scopesByCandidate[key], scope)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	setWide := 0
	for _, kill := range prior[0].Kills {
		if kill.Killer == TimeoutKiller || strings.HasPrefix(kill.Killer, PackageKillerPrefix) {
			setWide++
		}
	}
	remeasured := len(prior[0].Survivors) + auxKills + setWide
	stand := prior[0].Killed - auxKills - setWide
	wantReason := fmt.Sprintf("served: %s stand on unmoved oracles; re-measuring %s against the current oracle (%s narrowed to the added and moved tests)",
		killNoun(stand), candidateNoun(remeasured), survivorNoun(len(prior[0].Survivors)))
	if len(decisions) != 1 || decisions[0].Action != "measure" || decisions[0].Reason != wantReason || decisions[0].Candidates != remeasured {
		t.Fatalf("drift decision = %+v, want %q over %d candidates", decisions, wantReason, remeasured)
	}
	if len(dispatched) != remeasured {
		t.Fatalf("dispatched %d candidates, want exactly the %d re-measured", len(dispatched), remeasured)
	}
	// The dual scope is executed per candidate — the executor reports the
	// scope off the very work value it runs: every survivor's executions
	// are the moved test alone, every re-measured kill's are the full
	// current oracle, and exactly the re-measured candidates execute.
	narrowScope := []string{"^(TestAux)$"}
	fullScope := []string{"^(TestAux|TestSmall)$"}
	if len(scopesByCandidate) != remeasured {
		t.Fatalf("executions observed for %d candidates, want the %d re-measured", len(scopesByCandidate), remeasured)
	}
	survivorIdentities := map[survivorKey]bool{}
	for _, survivor := range prior[0].Survivors {
		survivorIdentities[survivorKey{survivor.Position, survivor.Operator}] = true
	}
	for key, scopes := range scopesByCandidate {
		want := fullScope
		if survivorIdentities[key] {
			want = narrowScope
		}
		for _, scope := range scopes {
			if !slices.Equal(scope, want) {
				t.Fatalf("candidate %v ran scope %v, want %v (survivor=%v)", key, scope, want, survivorIdentities[key])
			}
		}
	}
	driftedF := driftedFindings[0]
	if driftedF.Cached || driftedF.TargetEvidence.RuntimeUnverifiable {
		t.Fatalf("drifted record = cached %v, unverifiable %v; want a verifiable measurement", driftedF.Cached, driftedF.TargetEvidence.RuntimeUnverifiable)
	}
	if driftedF.Generated != prior[0].Generated || driftedF.CandidateCount != prior[0].CandidateCount ||
		driftedF.Generated != driftedF.Mutants+driftedF.Discarded || driftedF.Mutants != driftedF.Killed+len(driftedF.Survivors) {
		t.Fatalf("drifted totals do not conserve: %+v", driftedF)
	}
	if len(driftedF.Kills) != driftedF.Killed {
		t.Fatalf("drifted record persisted %d attributions for %d kills", len(driftedF.Kills), driftedF.Killed)
	}

	// A record without kill attribution — persisted before the field
	// existed — never drift-serves: the whole target re-measures.
	stripped, err := ParseFindings(doc)
	if err != nil {
		t.Fatal(err)
	}
	stripped[0].Kills = nil
	var strippedDecisions []RunDecision
	var strippedDispatched []int
	if _, err := driftTree.Run(ctx, []Target{target}, Options{
		Prior:      stripped,
		Decision:   func(d RunDecision) { strippedDecisions = append(strippedDecisions, d) },
		dispatched: func(_ string, mi int) { strippedDispatched = append(strippedDispatched, mi) },
	}); err != nil {
		t.Fatal(err)
	}
	if len(strippedDecisions) != 1 || !strings.HasPrefix(strippedDecisions[0].Reason, "stale: ") || len(strippedDispatched) != stripped[0].Generated {
		t.Fatalf("attribution-free record = %+v dispatching %d, want a whole re-measure", strippedDecisions, len(strippedDispatched))
	}

	// The drifted record is current on its tree: a follow-up serves cached.
	driftedDoc, err := Export(driftedFindings)
	if err != nil {
		t.Fatal(err)
	}
	reparsed, err := ParseFindings(driftedDoc)
	if err != nil {
		t.Fatal(err)
	}
	var followDecisions []RunDecision
	followed, err := driftTree.Run(ctx, []Target{target}, Options{Prior: reparsed, Decision: func(d RunDecision) { followDecisions = append(followDecisions, d) }})
	if err != nil {
		t.Fatal(err)
	}
	if !followed[0].Cached || len(followDecisions) != 1 || followDecisions[0].Action != "cached" {
		t.Fatalf("drifted record did not serve cached on its own tree: %v %+v", followed[0].Cached, followDecisions)
	}

	// A delta reaching no recorded oracle — an added, unreferenced helper —
	// serves the whole record with nothing re-measured.
	if err := os.WriteFile(filepath.Join(dir, "gated_test.go"), []byte(strings.Replace(testSource, "return v\n", "w := v\n\treturn w\n", 1)+"\nfunc lonely() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lonelyTree, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var lonelyDecisions []RunDecision
	var lonelyDispatched []int
	lonelyFindings, err := lonelyTree.Run(ctx, []Target{target}, Options{
		Prior:      reparsed,
		Decision:   func(d RunDecision) { lonelyDecisions = append(lonelyDecisions, d) },
		dispatched: func(_ string, mi int) { lonelyDispatched = append(lonelyDispatched, mi) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(lonelyDecisions) != 1 || lonelyDecisions[0].Reason != "served: compartment delta reaches no recorded oracle; nothing re-measures" ||
		lonelyDecisions[0].Candidates != 0 || len(lonelyDispatched) != 0 {
		t.Fatalf("no-reach decision = %+v dispatching %d, want a full serve", lonelyDecisions, len(lonelyDispatched))
	}
	if lonelyFindings[0].Killed != driftedF.Killed || !slices.Equal(lonelyFindings[0].Survivors, driftedF.Survivors) {
		t.Fatalf("no-reach serve rescored or rebucketed: %+v vs %+v", lonelyFindings[0].Survivors, driftedF.Survivors)
	}
	if !slices.Equal(lonelyFindings[0].Kills, driftedF.Kills) {
		t.Fatalf("no-reach serve rewrote kill attributions: %+v vs %+v", lonelyFindings[0].Kills, driftedF.Kills)
	}

	// An initialization-bearing delta — an added package var — refuses the
	// arm: the whole target re-measures.
	if err := os.WriteFile(filepath.Join(dir, "gated_test.go"), []byte(strings.Replace(testSource, "return v\n", "w := v\n\treturn w\n", 1)+"\nfunc lonely() {}\n\nvar seed = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lonelyDoc, err := Export(lonelyFindings)
	if err != nil {
		t.Fatal(err)
	}
	lonelyPrior, err := ParseFindings(lonelyDoc)
	if err != nil {
		t.Fatal(err)
	}
	varTree, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var varDecisions []RunDecision
	var varDispatched []int
	if _, err := varTree.Run(ctx, []Target{target}, Options{
		Prior:      lonelyPrior,
		Decision:   func(d RunDecision) { varDecisions = append(varDecisions, d) },
		dispatched: func(_ string, mi int) { varDispatched = append(varDispatched, mi) },
	}); err != nil {
		t.Fatal(err)
	}
	if len(varDecisions) != 1 || !strings.HasPrefix(varDecisions[0].Reason, "stale: ") || len(varDispatched) != lonelyPrior[0].Generated {
		t.Fatalf("init-bearing delta = %+v dispatching %d, want a whole re-measure", varDecisions, len(varDispatched))
	}

	// An unchanged var initializer reaching an edited function — prime,
	// which no oracle references — runs changed code around every test:
	// the carve-out refuses and the whole target re-measures.
	if err := os.WriteFile(filepath.Join(dir, "gated_test.go"), []byte(strings.Replace(testSource, "func prime() int {\n\treturn 1\n}", "func prime() int {\n\treturn 2\n}", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	rootTree, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var rootDecisions []RunDecision
	var rootDispatched []int
	if _, err := rootTree.Run(ctx, []Target{target}, Options{
		Prior:      prior,
		Decision:   func(d RunDecision) { rootDecisions = append(rootDecisions, d) },
		dispatched: func(_ string, mi int) { rootDispatched = append(rootDispatched, mi) },
	}); err != nil {
		t.Fatal(err)
	}
	if len(rootDecisions) != 1 || !strings.HasPrefix(rootDecisions[0].Reason, "stale: ") || len(rootDispatched) != prior[0].Generated {
		t.Fatalf("unconditional-root delta = %+v dispatching %d, want a whole re-measure", rootDecisions, len(rootDispatched))
	}
}

// The generalized drift gate: a removed oracle identity stays the general
// rule's domain, a grown set under an explicit oracle on either side is the
// caller's selection, and incomplete kill attribution refuses — each before
// any evidence check. Candidate evidence no longer disqualifies at the gate;
// it composes downstream as re-executed flagged candidates
// (REQ-result-stale's killer-drift carve-out).
func TestEvidenceSetCoversKillerDriftEarlyGate(t *testing.T) {
	ledger := &CompartmentLedger{}
	base := Finding{
		OperatorSet: "go/12", OracleTimeout: "1m0s", CompartmentLedger: ledger,
		OracleEvidence: []SubjectEvidence{{Symbol: "p.TestA"}, {Symbol: "p.TestB"}},
	}
	ctx := context.Background()
	for name, tc := range map[string]struct {
		prior    Finding
		oracle   []*subjectView
		explicit bool
	}{
		"removed oracle identity": {prior: base, oracle: make([]*subjectView, 1)},
		"grown set under an explicit oracle": {prior: func() Finding { f := base; f.OracleExplicit = true; return f }(),
			oracle: make([]*subjectView, 3), explicit: true},
		"incomplete kill attribution": {prior: func() Finding {
			f := base
			f.Killed = 2
			f.Kills = []Kill{{Position: "f.go:1:1", Operator: "op", Killer: "p.TestA"}}
			return f
		}(), oracle: make([]*subjectView, 2)},
		"missing ledger": {prior: func() Finding { f := base; f.CompartmentLedger = nil; return f }(),
			oracle: make([]*subjectView, 2)},
		"duplicated current oracle": {prior: base, oracle: func() []*subjectView {
			a := &subjectView{symbol: "p.TestA"}
			return []*subjectView{a, a}
		}()},
		// Under a mutant that admits this row past the gate, the nil
		// TARGET view panics at the ledger read — a deterministic kill;
		// the clean pass proves the refusal precedes every view read.
		"ghost killer": {prior: func() Finding {
			f := base
			f.Killed = 1
			f.Kills = []Kill{{Position: "f.go:1:1", Operator: "op", Killer: "p.TestVanished"}}
			return f
		}(), oracle: []*subjectView{{symbol: "p.TestA"}, {symbol: "p.TestB"}}},
	} {
		// The head pins the explicit flags equal, so the grown-explicit
		// refusal is exercised with both sides explicit — the only state
		// the pin admits.
		moved, added, ok, err := evidenceSetCoversKillerDriftContext(ctx, tc.prior, nil, tc.oracle, tc.explicit, "go/12", "1m0s", 0, "")
		if err != nil || ok || moved != nil || added != nil {
			t.Fatalf("%s: drift gate = %v %v %v %v, want a refusal before any evidence check", name, moved, added, ok, err)
		}
	}
}

// TestRunServesGrownAndDriftedComposition pins the generalized carve-out end
// to end (REQ-result-stale): one round both ADDS a test and CHANGES a helper
// — the strengthen-loop's usual shape — while the record carries flagged
// candidate evidence. Kills keyed to the unmoved oracle stand; the moved
// oracle's kills, every survivor (the added test may kill one — and provably
// does), and the flagged candidate re-execute against the full current
// oracle; the added test's fresh kill lands attributed; the decision names
// the standing kills, the growth, and the flagged re-execution.
func TestRunServesGrownAndDriftedComposition(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the oracle per mutant")
	}
	dir := t.TempDir()
	const testSource = "package gated\n\nimport \"testing\"\n\nfunc sink(v int) int {\n\treturn v\n}\n\nfunc TestSmall(t *testing.T) {\n\tif Gated(5) != 6 {\n\t\tt.Fail()\n\t}\n}\n\nfunc TestAux(t *testing.T) {\n\tif sink(Gated(200)) != 603 {\n\t\tt.Fail()\n\t}\n}\n"
	files := map[string]string{
		"go.mod":        "module example.com/composed\n\ngo 1.26\n",
		"gated.go":      "package gated\n\nfunc Gated(x int) int {\n\ty := x + 1\n\tif y > 100 {\n\t\treturn y * 3\n\t}\n\treturn y\n}\n",
		"gated_test.go": testSource,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	target := Target{Symbol: "example.com/composed.Gated"}
	first, err := tr.Run(ctx, []Target{target}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	f := first[0]
	if f.Killed == 0 || len(f.Kills) != f.Killed || f.CompartmentLedger == nil {
		t.Fatalf("baseline fixture = %+v, want attributed kills and a ledger", f)
	}
	// The boundary mutant at y > 100 survives round one: neither test
	// crosses at y == 100 exactly, and only the added test's Gated(99)
	// discriminates > from >=.
	boundary := Survivor{}
	for _, s := range f.Survivors {
		if s.Operator == "relational boundary: > -> >=" {
			boundary = s
			break
		}
	}
	if boundary.Position == "" {
		t.Fatalf("fixture survivors = %+v, want the > -> >= boundary mutant open so growth has a provable kill", f.Survivors)
	}
	auxKills, smallKills, setWide := 0, 0, 0
	for _, kill := range f.Kills {
		switch kill.Killer {
		case "example.com/composed.TestAux":
			auxKills++
		case "example.com/composed.TestSmall":
			smallKills++
		default:
			setWide++
		}
	}
	if auxKills == 0 || smallKills == 0 {
		t.Fatalf("fixture kills = %+v, want both oracles attributed so the split has teeth", f.Kills)
	}
	doc, err := Export(first)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := ParseFindings(doc)
	if err != nil {
		t.Fatal(err)
	}
	// Flag one unmoved-killer kill with candidate evidence: the composition
	// must re-execute it rather than refuse the whole record.
	var flaggedKill Kill
	for _, kill := range prior[0].Kills {
		if kill.Killer == "example.com/composed.TestSmall" {
			flaggedKill = kill
			break
		}
	}
	prior[0].CandidateEvidence = []CandidateEvidence{{
		Position: flaggedKill.Position, Operator: flaggedKill.Operator,
		Reason: "mutant test process exited before observation finalization", Disposition: "killed",
	}}

	// The composed round: the helper only TestAux reaches changes, and
	// TestExtra is added.
	changed := strings.Replace(testSource, "return v\n", "w := v\n\treturn w\n", 1) +
		"\nfunc TestExtra(t *testing.T) {\n\tif Gated(99) != 100 {\n\t\tt.Fail()\n\t}\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "gated_test.go"), []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	composedTree, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var decisions []RunDecision
	var dispatched []int
	composed, err := composedTree.Run(ctx, []Target{target}, Options{
		Prior:      prior,
		Decision:   func(d RunDecision) { decisions = append(decisions, d) },
		dispatched: func(_ string, mi int) { dispatched = append(dispatched, mi) },
	})
	if err != nil {
		t.Fatal(err)
	}
	remeasured := len(prior[0].Survivors) + auxKills + setWide + 1 // + the flagged unmoved-killer kill
	stand := smallKills - 1
	wantReason := fmt.Sprintf(
		"served: %s stand on unmoved oracles; re-measuring %s against the current oracle (%s narrowed to the added and moved tests) (derived oracle grew by 1 test); 1 candidate re-executes flagged evidence",
		killNoun(stand), candidateNoun(remeasured), survivorNoun(len(prior[0].Survivors)))
	if len(decisions) != 1 || decisions[0].Action != "measure" || decisions[0].Reason != wantReason || decisions[0].Candidates != remeasured {
		t.Fatalf("composed decision = %+v, want %q over %d candidates", decisions, wantReason, remeasured)
	}
	if len(dispatched) != remeasured {
		t.Fatalf("dispatched %d candidates, want exactly the %d re-measured", len(dispatched), remeasured)
	}
	cf := composed[0]
	if cf.Cached {
		t.Fatal("composed serve marked cached; it measured")
	}
	if len(cf.Kills) != cf.Killed {
		t.Fatalf("composed record persisted %d attributions for %d kills", len(cf.Kills), cf.Killed)
	}
	// The added test's provable kill: the boundary survivor died to
	// TestExtra, attributed like any fresh kill.
	boundaryKilled := false
	for _, kill := range cf.Kills {
		if kill.Position == boundary.Position && kill.Operator == boundary.Operator {
			boundaryKilled = true
			if kill.Killer != "example.com/composed.TestExtra" {
				t.Fatalf("boundary kill attributed to %s, want the added test", kill.Killer)
			}
		}
	}
	if !boundaryKilled {
		t.Fatalf("the added test's provable kill did not land: kills=%+v survivors=%+v", cf.Kills, cf.Survivors)
	}
	// The flagged kill re-executed and re-killed; its evidence is replaced
	// by the fresh execution's.
	flaggedStillKilled := false
	for _, kill := range cf.Kills {
		if kill.Position == flaggedKill.Position && kill.Operator == flaggedKill.Operator {
			flaggedStillKilled = true
		}
	}
	if !flaggedStillKilled {
		t.Fatalf("the flagged kill vanished: %+v", cf.Kills)
	}
	for _, evidence := range cf.CandidateEvidence {
		if evidence.Reason == "mutant test process exited before observation finalization" {
			t.Fatalf("stale flagged evidence survived the re-execution: %+v", cf.CandidateEvidence)
		}
	}

	// A record whose oracle-evidence list lost a row (a hand edit, a bad
	// merge) must refuse: the absent oracle still exists and CHANGED in
	// this very delta — classifying it "added" would exempt it from the
	// walk and let recorded outcomes stand on a moved ground. The delta's
	// own Added list is the authority on what growth composed; the
	// record's evidence list is not an identity oracle. The tamper
	// relabels the dropped oracle's kills onto the surviving one so the
	// ghost-killer refusal cannot mask this guard — the dropped-row state
	// must refuse on the delta authority itself.
	tampered, err := ParseFindings(doc)
	if err != nil {
		t.Fatal(err)
	}
	kept := tampered[0].OracleEvidence[:0]
	for _, evidence := range tampered[0].OracleEvidence {
		if evidence.Symbol != "example.com/composed.TestAux" {
			kept = append(kept, evidence)
		}
	}
	if len(kept) != len(tampered[0].OracleEvidence)-1 {
		t.Fatal("setup: the tampered record did not drop exactly TestAux's evidence row")
	}
	tampered[0].OracleEvidence = kept
	relabeled := append([]Kill(nil), tampered[0].Kills...)
	for i := range relabeled {
		if relabeled[i].Killer == "example.com/composed.TestAux" {
			relabeled[i].Killer = "example.com/composed.TestSmall"
		}
	}
	tampered[0].Kills = relabeled
	tampered[0].CandidateEvidence = nil
	decisions = nil
	if _, err := composedTree.Run(ctx, []Target{target}, Options{
		Prior:    tampered,
		Decision: func(d RunDecision) { decisions = append(decisions, d) },
	}); err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 1 || strings.HasPrefix(decisions[0].Reason, "served:") {
		t.Fatalf("dropped-evidence-row decision = %+v, want a whole re-measure, never a serve", decisions)
	}

	// A same-length oracle SWAP — a killer renamed — is a removal even
	// though the set sizes match: the recorded killer's evidence names an
	// oracle the current set no longer carries, and serving its kills as
	// "unmoved" would let a vanished test's kills stand (the flattering
	// direction). The whole target re-measures instead.
	renamed := strings.Replace(changed, "func TestAux(", "func TestAuxRenamed(", 1)
	if err := os.WriteFile(filepath.Join(dir, "gated_test.go"), []byte(renamed), 0o644); err != nil {
		t.Fatal(err)
	}
	renamedTree, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	decisions = nil
	renamedFindings, err := renamedTree.Run(ctx, []Target{target}, Options{
		Prior:    composed,
		Decision: func(d RunDecision) { decisions = append(decisions, d) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 1 || decisions[0].Action != "measure" || strings.HasPrefix(decisions[0].Reason, "served:") {
		t.Fatalf("renamed-killer decision = %+v, want a whole re-measure, never a serve", decisions)
	}
	if decisions[0].Candidates != renamedFindings[0].Generated {
		t.Fatalf("renamed-killer re-measured %d of %d candidates, want the whole target", decisions[0].Candidates, renamedFindings[0].Generated)
	}
}

// TestRunDriftAddedOnlyServesKillsAndBucketsSurvivors pins the moved-empty,
// grown-set shape: a changed helper NO recorded oracle reaches plus an added
// test. Every kill stands — set-wide included — every survivor re-measures
// against the current oracle (the added test provably kills one), and the
// re-measured survivors' advisory buckets re-derive from the current probe
// (REQ-result-stale's killer-drift carve-out).
func TestRunDriftAddedOnlyServesKillsAndBucketsSurvivors(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the oracle per mutant")
	}
	dir := t.TempDir()
	const testSource = "package gated\n\nimport \"testing\"\n\nfunc spare(v int) int {\n\treturn v\n}\n\nfunc TestSmall(t *testing.T) {\n\tif Gated(5) != 6 {\n\t\tt.Fail()\n\t}\n}\n\nfunc TestAux(t *testing.T) {\n\tif Gated(200) != 603 {\n\t\tt.Fail()\n\t}\n}\n"
	files := map[string]string{
		"go.mod":        "module example.com/addedonly\n\ngo 1.26\n",
		"gated.go":      "package gated\n\nfunc Gated(x int) int {\n\ty := x + 1\n\tif y > 100 {\n\t\treturn y * 3\n\t}\n\treturn y\n}\n",
		"gated_test.go": testSource,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	target := Target{Symbol: "example.com/addedonly.Gated"}
	first, err := tr.Run(ctx, []Target{target}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	f := first[0]
	if f.Killed == 0 || len(f.Kills) != f.Killed || len(f.Survivors) == 0 {
		t.Fatalf("baseline fixture = %+v, want attributed kills and survivors", f)
	}
	doc, err := Export(first)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := ParseFindings(doc)
	if err != nil {
		t.Fatal(err)
	}
	// Change the helper neither test references, and add the
	// boundary-discriminating test.
	changed := strings.Replace(testSource, "return v\n", "w := v\n\treturn w\n", 1) +
		"\nfunc TestExtra(t *testing.T) {\n\tif Gated(99) != 100 {\n\t\tt.Fail()\n\t}\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "gated_test.go"), []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	grownTree, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var decisions []RunDecision
	grown, err := grownTree.Run(ctx, []Target{target}, Options{
		Prior:    prior,
		Decision: func(d RunDecision) { decisions = append(decisions, d) },
	})
	if err != nil {
		t.Fatal(err)
	}
	wantReason := fmt.Sprintf(
		"served: %s stand on unmoved oracles; re-measuring %s against the current oracle (%s narrowed to the added and moved tests) (derived oracle grew by 1 test)",
		killNoun(prior[0].Killed), candidateNoun(len(prior[0].Survivors)), survivorNoun(len(prior[0].Survivors)))
	if len(decisions) != 1 || decisions[0].Reason != wantReason {
		t.Fatalf("added-only decision = %+v, want %q", decisions, wantReason)
	}
	gf := grown[0]
	extraKilled := false
	for _, kill := range gf.Kills {
		if kill.Killer == "example.com/addedonly.TestExtra" {
			extraKilled = true
		}
	}
	if !extraKilled {
		t.Fatalf("the added test killed nothing: kills=%+v survivors=%+v", gf.Kills, gf.Survivors)
	}
	if gf.TargetEvidence.RuntimeUnverifiable {
		t.Fatalf("added-only serve landed non-reusable: %+v", gf.TargetEvidence)
	}
	for _, survivor := range gf.Survivors {
		if survivor.Execution == "" {
			t.Fatalf("re-measured survivor %s %s carries no advisory bucket — the grown re-measure must re-derive them", survivor.Position, survivor.Operator)
		}
	}
}

// TestRunDriftGrownFullyKilledRecordSaysSetGrew pins the decision
// wording on the empty re-measure set that is NOT a no-reach serve: a
// fully-killed record, a helper delta no oracle reaches, and an added test
// leave nothing to re-measure — yet the set grew, and the decision must say
// so rather than claim "reaches no recorded oracle"
// (REQ-result-stale's killer-drift carve-out).
func TestRunDriftGrownFullyKilledRecordSaysSetGrew(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the oracle per mutant")
	}
	dir := t.TempDir()
	const testSource = "package tiny\n\nimport \"testing\"\n\nfunc spare(v int) int {\n\treturn v\n}\n\nfunc TestOne(t *testing.T) {\n\tif Tiny(1) != 2 {\n\t\tt.Fail()\n\t}\n}\n\nfunc TestTwo(t *testing.T) {\n\tif Tiny(-3) != -2 {\n\t\tt.Fail()\n\t}\n}\n"
	files := map[string]string{
		"go.mod":       "module example.com/tinyfixture\n\ngo 1.26\n",
		"tiny.go":      "package tiny\n\nfunc Tiny(x int) int {\n\treturn x + 1\n}\n",
		"tiny_test.go": testSource,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	target := Target{Symbol: "example.com/tinyfixture.Tiny"}
	first, err := tr.Run(ctx, []Target{target}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first[0].Survivors) != 0 || first[0].Killed == 0 {
		t.Fatalf("fixture = %+v, want a fully-killed record so the re-measure set is empty", first[0])
	}
	doc, err := Export(first)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := ParseFindings(doc)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(testSource, "return v\n", "w := v\n\treturn w\n", 1) +
		"\nfunc TestMore(t *testing.T) {\n\tif Tiny(7) != 8 {\n\t\tt.Fail()\n\t}\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "tiny_test.go"), []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	grownTree, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var decisions []RunDecision
	if _, err := grownTree.Run(ctx, []Target{target}, Options{
		Prior:    prior,
		Decision: func(d RunDecision) { decisions = append(decisions, d) },
	}); err != nil {
		t.Fatal(err)
	}
	wantReason := fmt.Sprintf(
		"served: %s stand on unmoved oracles; re-measuring %s against the current oracle (derived oracle grew by 1 test)",
		killNoun(prior[0].Killed), candidateNoun(0))
	if len(decisions) != 1 || decisions[0].Reason != wantReason || decisions[0].Candidates != 0 {
		t.Fatalf("fully-killed grown decision = %+v, want %q with nothing re-measured", decisions, wantReason)
	}
}

// A confirmation flip during a killer-drift re-measure rides the
// drifted record end-to-end (REQ-exec-survivor-evidence's flipped-kill
// bucket through REQ-result-stale's killer-drift carve-out). The flaky
// oracle lives in a SEPARATE package: its marker plumbing makes its own
// compartment unverifiable, which classifies it as a moved oracle —
// drift's normal input — while the target package's compartment stays
// plainly valid, so the target-only strict-validity gate passes. A
// same-package flaky oracle cannot reach this path at all: its external
// effects stain the target's own compartment, and drift serving
// fail-closes on any non-valid target verdict.
func TestRunDriftRemeasureRecordsConfirmationFlip(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the oracle per mutant")
	}
	marker := filepath.Join(t.TempDir(), "marker")
	t.Setenv("GOMUTANT_FLAKY_MARKER", marker)
	dir := t.TempDir()
	const testSource = `package gated

import "testing"

func TestSmall(t *testing.T) {
	if Gated(5) != 6 {
		t.Fail()
	}
}

func TestAux(t *testing.T) {
	if Gated(200) != 603 {
		t.Fail()
	}
}
`
	files := map[string]string{
		"go.mod":        "module example.com/driftflip\n\ngo 1.26\n",
		"gated.go":      "package gated\n\nfunc Gated(x int) int {\n\ty := x + 1\n\tif y > 100 {\n\t\treturn y * 3\n\t}\n\treturn y\n}\n",
		"gated_test.go": testSource,
		"flakycheck/flaky_test.go": `package flakycheck

import (
	"fmt"
	"os"
	"testing"

	gated "example.com/driftflip"
)

// TestFlakyGate guards the small branch nondeterministically once
// armed: the first look on a mutant kills and leaves that mutant's
// marker, the second look passes. Unarmed it always passes. The Gated
// check runs FIRST so a baseline (healthy-tree) execution reads no
// environment at all: runtime-input pins derive from baseline runs,
// and an unconditionally read arming variable would ride the target's
// recorded inputs and stale the whole target when it changes. The
// marker is keyed by the pair of observations the oracles judge, so
// distinct mutants never consume each other's first look: a mutant
// TestAux kills has Gated(200) != 603 while a flip-capable one has
// == 603, so the doomed and flip-capable classes cannot collide, and
// two mutants sharing a key are behaviorally identical on every
// deciding probe — if one can flip, so can the other, and only the
// first needs to.
func TestFlakyGate(t *testing.T) {
	small := gated.Gated(5)
	if small == 6 {
		return
	}
	if os.Getenv("DRIFTFLIP_ARMED") == "" {
		return
	}
	base := os.Getenv("GOMUTANT_FLAKY_MARKER")
	if base == "" {
		t.Fatal("mutated without a marker path")
	}
	marker := fmt.Sprintf("%s-%d-%d", base, small, gated.Gated(200))
	if _, err := os.Stat(marker); err == nil {
		return // second look: the failure does not reproduce
	}
	if err := os.WriteFile(marker, []byte("seen"), 0o644); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		panic("first look interference")
	}()
	<-done
}
`,
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	target := Target{Symbol: "example.com/driftflip.Gated", Oracle: []string{
		"example.com/driftflip.TestSmall",
		"example.com/driftflip.TestAux",
		"example.com/driftflip/flakycheck.TestFlakyGate",
	}}
	first, err := tr.Run(ctx, []Target{target}, Options{Jobs: 2})
	if err != nil {
		t.Fatal(err)
	}
	if burned, err := filepath.Glob(marker + "-*"); err != nil || len(burned) != 0 {
		t.Fatalf("the flaky arm fired on the baseline run; the fixture premise is broken (%v, %v)", burned, err)
	}
	smallKills := 0
	for _, kill := range first[0].Kills {
		if kill.Killer == "example.com/driftflip.TestSmall" {
			smallKills++
		}
	}
	if smallKills == 0 {
		t.Fatalf("baseline kills = %+v, want TestSmall attributions to drift", first[0].Kills)
	}
	doc, err := Export(first)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := ParseFindings(doc)
	if err != nil {
		t.Fatal(err)
	}

	// Weaken TestSmall — its declaration changes (attributable killer
	// drift in the target's own compartment) and its guard drops — and
	// arm the flaky oracle, so a re-measured small-branch mutant is
	// killed in the window and withdrawn on serial confirmation.
	weakened := strings.Replace(testSource, "\tif Gated(5) != 6 {\n\t\tt.Fail()\n\t}\n", "\t_ = Gated(5)\n", 1)
	if weakened == testSource {
		t.Fatal("weakening edit did not apply")
	}
	if err := os.WriteFile(filepath.Join(dir, "gated_test.go"), []byte(weakened), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DRIFTFLIP_ARMED", "1")
	driftTree, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var decisions []RunDecision
	drifted, err := driftTree.Run(ctx, []Target{target}, Options{
		Jobs:     2,
		Prior:    prior,
		Decision: func(d RunDecision) { decisions = append(decisions, d) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 1 || !strings.Contains(decisions[0].Reason, "stand on unmoved oracles") {
		t.Fatalf("decision = %+v, want the drift-serving path (kills standing on unmoved oracles)", decisions)
	}
	flipped := 0
	for _, s := range drifted[0].Survivors {
		if s.Execution == "flipped-kill" {
			flipped++
			if s.WithdrawnKiller == "" {
				t.Fatalf("flipped drifted survivor carries no withdrawn killer: %+v", s)
			}
		}
		if s.WithdrawnKiller != "" && s.Execution != "flipped-kill" {
			t.Fatalf("withdrawn killer without the flipped-kill bucket: %+v", s)
		}
	}
	if flipped == 0 {
		t.Fatalf("no drifted survivor records the confirmation flip: %+v", drifted[0].Survivors)
	}
	if burned, err := filepath.Glob(marker + "-*"); err != nil || len(burned) == 0 {
		t.Fatalf("the window kill never happened (no marker written; %v)", err)
	}
}

// The per-candidate oracle scope binds at one seam: a drift-re-measured
// survivor executes (and confirms) under the narrowed added-and-moved
// groups, while flagged candidates and re-measured kills keep the full
// set — the swap covers the whole execution machinery because it all
// reads the item's groups (REQ-result-stale's survivor narrowing).
func TestScopedWorkSwapsGroupsForNarrowedSurvivors(t *testing.T) {
	full := []group{{pkgs: []string{"p"}, runRegex: "^TestA$|^TestB$"}}
	narrow := []group{{pkgs: []string{"p"}, runRegex: "^TestB$"}}
	w := work{groups: full, narrowGroups: narrow, driftSurvivors: map[int]bool{2: true}}
	if got := scopedWork(w, 2); len(got.groups) != 1 || got.groups[0].runRegex != "^TestB$" {
		t.Fatalf("survivor-scoped candidate kept the full groups: %+v", got.groups)
	}
	if got := scopedWork(w, 1); got.groups[0].runRegex != "^TestA$|^TestB$" {
		t.Fatalf("full-scope candidate lost the full groups: %+v", got.groups)
	}
	// No narrow groups (a non-drift item): the swap never fires even
	// for a marked index.
	bare := work{groups: full, driftSurvivors: map[int]bool{0: true}}
	if got := scopedWork(bare, 0); got.groups[0].runRegex != "^TestA$|^TestB$" {
		t.Fatalf("swap fired without narrow groups: %+v", got.groups)
	}
}

// TestRunDriftNarrowBaselineRefusesAddedTestFailingAlone pins the narrow
// scope's own baseline probe (REQ-exec-quiescence's baseline locality,
// REQ-result-stale's killer-drift carve-out): the survivor narrowing's
// timeout ground requires the narrow baseline and the narrow mutant run
// judged under one scope, so a grown set's added test that passes in the
// full suite's order but fails alone must refuse the target at the narrow
// baseline — with the failing test named — never proceed to mutants under
// an unprobed scope.
func TestRunDriftNarrowBaselineRefusesAddedTestFailingAlone(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the oracle baseline")
	}
	dir := t.TempDir()
	const testSource = "package gated\n\nimport \"testing\"\n\nvar primed bool\n\nfunc TestSmall(t *testing.T) {\n\tprimed = true\n\tif Gated(5) != 6 {\n\t\tt.Fail()\n\t}\n}\n"
	files := map[string]string{
		"go.mod":        "module example.com/narrowbase\n\ngo 1.26\n",
		"gated.go":      "package gated\n\nfunc Gated(x int) int {\n\ty := x + 1\n\tif y > 100 {\n\t\treturn y * 3\n\t}\n\treturn y\n}\n",
		"gated_test.go": testSource,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	target := Target{Symbol: "example.com/narrowbase.Gated"}
	first, err := tr.Run(ctx, []Target{target}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first[0].Survivors) == 0 {
		t.Fatalf("baseline fixture = %+v, want survivors so the narrowing arms", first[0])
	}
	doc, err := Export(first)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := ParseFindings(doc)
	if err != nil {
		t.Fatal(err)
	}

	// The added test passes after TestSmall primes the package but fails
	// alone: only the narrow scope's own baseline can observe the failure.
	if err := os.WriteFile(filepath.Join(dir, "gated_test.go"), []byte(testSource+"\nfunc TestBig(t *testing.T) {\n\tif !primed {\n\t\tt.Fatal(\"requires TestSmall first\")\n\t}\n\tif Gated(200) != 603 {\n\t\tt.Fail()\n\t}\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	grownTree, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var dispatched []int
	grownFindings, err := grownTree.Run(ctx, []Target{target}, Options{
		Prior:      prior,
		dispatched: func(_ string, mi int) { dispatched = append(dispatched, mi) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(grownFindings[0].Skipped, "oracle baseline does not pass") || !strings.Contains(grownFindings[0].Skipped, "TestBig") {
		t.Fatalf("finding = %+v, want the narrow baseline skip naming TestBig", grownFindings[0])
	}
	if len(dispatched) != 0 {
		t.Fatalf("dispatched %d mutants under an unprobed narrow scope", len(dispatched))
	}

	// The control round: a forced whole re-measure on the same tree
	// builds no narrow groups, so its full-scope baseline (TestSmall
	// priming TestBig in declaration order) passes and mutants dispatch —
	// witnessing that the narrow probe was the sole refuser above.
	var forcedDispatched []int
	forcedFindings, err := grownTree.Run(ctx, []Target{target}, Options{
		Prior:      prior,
		Force:      true,
		dispatched: func(_ string, mi int) { forcedDispatched = append(forcedDispatched, mi) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if forcedFindings[0].Skipped != "" || len(forcedDispatched) == 0 {
		t.Fatalf("forced whole re-measure = %q dispatching %d, want the full scope passing with mutants dispatched", forcedFindings[0].Skipped, len(forcedDispatched))
	}
}
