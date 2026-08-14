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

// driftRemeasureIndexes selects every survivor, every kill whose killer
// moved, and every timeout or package-scope kill when anything moved, while
// kills keyed to unmoved oracles stand; a kill identity regeneration cannot
// re-identify refuses the serve (REQ-result-stale's killer-drift carve-out).
func TestDriftRemeasureIndexesSelectsMovedEvidence(t *testing.T) {
	runnable := []engine.Replacement{{File: "f.go", Source: []byte("x")}}
	generation := engine.Generation{CandidateCount: 5, Candidates: []engine.Candidate{
		{Symbol: "p.F", Operator: "op-a", Position: "f.go:1:1", Replacements: runnable}, // kill by unmoved
		{Symbol: "p.F", Operator: "op-a", Position: "f.go:2:2", Replacements: runnable}, // kill by moved
		{Symbol: "p.F", Operator: "op-a", Position: "f.go:3:3", Replacements: runnable}, // timeout kill
		{Symbol: "p.F", Operator: "op-a", Position: "f.go:4:4", Replacements: runnable}, // package kill
		{Symbol: "p.F", Operator: "op-a", Position: "f.go:5:5", Replacements: runnable}, // survivor
	}}
	rec := Finding{
		CandidateCount: 5, Generated: 5, Killed: 4, Mutants: 5,
		Kills: []Kill{
			{Position: "f.go:1:1", Operator: "op-a", Killer: "p.TestSteady"},
			{Position: "f.go:2:2", Operator: "op-a", Killer: "p.TestMoved"},
			{Position: "f.go:3:3", Operator: "op-a", Killer: TimeoutKiller},
			{Position: "f.go:4:4", Operator: "op-a", Killer: PackageKillerPrefix + "p)"},
		},
		Survivors: []Survivor{{Position: "f.go:5:5", Operator: "op-a"}},
	}
	remeasure, stand, ok := driftRemeasureIndexes(generation, rec, []string{"p.TestMoved"})
	if !ok || stand != 1 {
		t.Fatalf("remeasure=%v stand=%d ok=%v, want ok with 1 standing kill", remeasure, stand, ok)
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

	// Nothing moved: no oracle's behavior changed, so survivals stand
	// exactly like kills and nothing re-measures.
	remeasure, stand, ok = driftRemeasureIndexes(generation, rec, nil)
	if !ok || stand != 4 || len(remeasure) != 0 {
		t.Fatalf("no-movement remeasure=%v stand=%d ok=%v, want nothing re-measured", remeasure, stand, ok)
	}

	// A kill regeneration cannot re-identify refuses the serve.
	misplaced := rec
	misplaced.Kills = append([]Kill(nil), rec.Kills...)
	misplaced.Kills[0].Position = "f.go:9:9"
	if _, _, ok := driftRemeasureIndexes(generation, misplaced, nil); ok {
		t.Fatalf("unidentifiable kill served under drift")
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
	remeasured := map[int]bool{1: true, 2: true, 3: true}
	outcomes := []engine.MutantOutcome{0, engine.MutantSurvived, engine.MutantKilled, engine.MutantSurvived, 0, 0}
	killers := []string{"", "", "p.TestMoved", "", "", ""}
	drifted, shed, err := driftFindingCounts(context.Background(), rec, candidates, remeasured, outcomes, killers, nil, nil)
	if err != nil {
		t.Fatal(err)
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
	driftedFindings, err := driftTree.Run(ctx, []Target{target}, Options{
		Prior:      prior,
		Decision:   func(d RunDecision) { decisions = append(decisions, d) },
		dispatched: func(_ string, mi int) { dispatched = append(dispatched, mi) },
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
	wantReason := fmt.Sprintf("served: %s stand on unmoved oracles; re-measuring %s against the current oracle", killNoun(stand), candidateNoun(remeasured))
	if len(decisions) != 1 || decisions[0].Action != "measure" || decisions[0].Reason != wantReason || decisions[0].Candidates != remeasured {
		t.Fatalf("drift decision = %+v, want %q over %d candidates", decisions, wantReason, remeasured)
	}
	if len(dispatched) != remeasured {
		t.Fatalf("dispatched %d candidates, want exactly the %d re-measured", len(dispatched), remeasured)
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
