package gomutant

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/greatliontech/gomutant/internal/engine"
)

// The observed union shares the decision set's gofresh views: one
// observation per module group serves the decision and the producer
// roles, the union's fingerprints carry the observation proof the
// decision's base fingerprints lack, and the strict builder reports a
// symbol's own fault first in symbol order.
//
//gofresh:pure
func TestObservedUnionSharesTheDecisionViews(t *testing.T) {
	tr := fixtureTree(t)
	ctx := context.Background()
	symbols := []string{"example.com/fixture/lib.Add", "example.com/fixture/lib.TestAdd", "example.com/fixture/counting.Value"}
	var observes atomic.Int64
	engines := tr.newSubjectEngines(func(phase, _, _ string) {
		if phase == "observe" {
			observes.Add(1)
		}
	}, false, 0)
	set, faults, err := tr.buildSubjectViews(ctx, symbols, tr.eng.PackageContextContext, engines)
	if err != nil || len(faults) != 0 {
		t.Fatalf("decision build: %v %v", faults, err)
	}
	if observes.Load() == 0 {
		t.Fatal("the decision build observed nothing — the pin below would be vacuous")
	}
	built := observes.Load()
	union, unionFaults, err := set.observed(ctx)
	if err != nil || len(unionFaults) != 0 {
		t.Fatalf("observed union: %v %v", unionFaults, err)
	}
	// The union constructs no view of its own: its proof siblings share
	// the decision views' one observation, and the only pass the proof
	// capture adds is the precise-analysis bracket's single closing
	// observation (REQ-fresh-coherent-view's comparison-only read) —
	// never a construction's agreement pair.
	if added := observes.Load() - built; added != int64(len(union.modules)) {
		t.Fatalf("the observed union ran %d observation pass(es) for %d module group(s) — one analysis-bracket close per group, never a construction pair", added, len(union.modules))
	}
	for _, symbol := range symbols {
		decision, ok := set.bySymbol[symbol]
		if !ok {
			t.Fatalf("decision set lacks %s", symbol)
		}
		observed, ok := union.bySymbol[symbol]
		if !ok {
			t.Fatalf("union lacks %s", symbol)
		}
		if observed.view == decision.view || observed.module == decision.module {
			t.Fatalf("%s: the union captured its proof on the decision view itself — the base view must stay base-only for the run-end validation", symbol)
		}
		if decision.fp.ObservationAssertion != "" || observed.fp.ObservationAssertion == "" {
			t.Fatalf("%s: decision fp assertion %q, observed fp assertion %q — want the proof on the union alone", symbol, decision.fp.ObservationAssertion, observed.fp.ObservationAssertion)
		}
		if observed.fp.MaximalClosure != decision.fp.MaximalClosure {
			t.Fatalf("%s: the union's closure %q differs from the decision's %q over one observation", symbol, observed.fp.MaximalClosure, decision.fp.MaximalClosure)
		}
	}
	if len(union.modules) != len(set.modules) {
		t.Fatalf("union modules %d, decision modules %d — one view per module group", len(union.modules), len(set.modules))
	}
	// A narrowing derives from the proof sibling and inherits its
	// captured proofs, so its validation takes the observed arm: with
	// no runtime evidence attached it refuses on exactly that — the
	// arm the decision parent must never take.
	narrowed, err := union.forTarget("example.com/fixture/lib.Add", []string{"example.com/fixture/lib.TestAdd"}, unionFaults)
	if err != nil {
		t.Fatal(err)
	}
	if narrowed.bySymbol["example.com/fixture/lib.Add"].fp.ObservationAssertion == "" {
		t.Fatal("narrowing lost the observation proof")
	}
	for _, module := range narrowed.modules {
		module.producer = true
	}
	if err := narrowed.validateProducers(ctx); err == nil || !strings.Contains(err.Error(), "no attached completed observation") {
		t.Fatalf("narrowing validation = %v, want the observed arm's missing-attachment refusal", err)
	}
	for _, module := range set.modules {
		module.producer = true
	}
	if err := set.validateProducers(ctx); err != nil {
		t.Fatalf("decision set validation took the observed arm: %v", err)
	}

	// The strict form: an unresolvable symbol's fault is the error, and
	// the first faulting symbol in the caller's order wins — every
	// time, not by map luck.
	for i := 0; i < 8; i++ {
		_, err = tr.newStrictObservedViews(ctx, []string{"example.com/fixture/lib.Add", "example.com/fixture/nope.Gone", "example.com/fixture/lib.NoSuch"}, tr.eng.PackageContextContext, engines)
		if err == nil || !strings.Contains(err.Error(), "nope.Gone") {
			t.Fatalf("strict build over missing symbols = %v, want the first faulting symbol named", err)
		}
	}
}

// A measured campaign observes each module group once for both roles:
// the decision build's observation pair plus the per-target sibling
// validation and the run-end validation — never a second construction
// for the producer union.
func TestCampaignObservesEachModuleGroupOnceForBothRoles(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	target := Target{Symbol: "example.com/fixture/counting.Value", Oracle: []string{"example.com/fixture/counting.TestCountingStrict"}}
	var observes atomic.Int64
	var mu sync.Mutex
	var phases []string
	_, err := tr.Run(context.Background(), []Target{target}, Options{AnalysisEvent: func(event AnalysisEvent) {
		phase := event.Phase
		if phase == "observe" {
			observes.Add(1)
		}
		mu.Lock()
		phases = append(phases, phase)
		mu.Unlock()
	}})
	if err != nil {
		t.Fatal(err)
	}
	// One module group: the decision build's agreement pair (2), the
	// proof capture's analysis-bracket close (1), the killer-scoped
	// baseline's and the sibling validation's comparison reads, the
	// run-end validation (1). A second construction for the producer
	// union would add its own pair on top.
	const want = 6
	if got := observes.Load(); got != want {
		t.Fatalf("campaign ran %d observation passes, want %d (decision pair + proof close + validations); phases: %v", got, want, phases)
	}
}

// A proof-capture fault routes to every symbol of the faulted module
// view — the cause on each, none of them in the union — so a target
// naming any of them reaches its bounded per-target retry with the
// cause (REQ-exec-quiescence's target-local evidence faults).
func TestObservedUnionRoutesACaptureFaultToTheModulesSymbols(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
	tmp := t.TempDir()
	if err := os.CopyFS(tmp, os.DirFS(fixtureDir)); err != nil {
		t.Fatal(err)
	}
	tr, err := Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	symbols := []string{"example.com/fixture/lib.Add", "example.com/fixture/lib.TestAdd"}
	set, faults, err := tr.buildSubjectViews(ctx, symbols, tr.eng.PackageContextContext, tr.newSubjectEngines(nil, false, 0))
	if err != nil || len(faults) != 0 {
		t.Fatalf("decision build: %v %v", faults, err)
	}
	// The tree moves under the proof capture: its analysis bracket's
	// closing observation refuses, and the refusal is the module's fault.
	if err := os.Remove(filepath.Join(tmp, "lib", "lib.go")); err != nil {
		t.Fatal(err)
	}
	union, unionFaults, err := set.observed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, symbol := range symbols {
		if _, ok := union.bySymbol[symbol]; ok {
			t.Fatalf("%s entered the union over a faulted proof capture", symbol)
		}
		if fault, ok := unionFaults[symbol]; !ok || fault == nil {
			t.Fatalf("%s carries no capture fault: %v", symbol, unionFaults)
		}
	}
	if _, err := union.forTarget(symbols[0], symbols[1:], unionFaults); err == nil || err != unionFaults[symbols[0]] {
		t.Fatalf("narrowing over the faulted module = %v, want the module's own capture fault", err)
	}
}

// firstFault promotes the first faulting symbol in the caller's order,
// never a map-iteration pick.
//
//gofresh:pure
func TestFirstFaultFollowsTheCallersOrder(t *testing.T) {
	faults := map[string]error{"b": errors.New("b faulted"), "c": errors.New("c faulted")}
	for i := 0; i < 16; i++ {
		if err := firstFault([]string{"a", "c", "b"}, faults); err == nil || err.Error() != "c faulted" {
			t.Fatalf("firstFault = %v, want c's (the first faulting symbol in the caller's order)", err)
		}
	}
	if err := firstFault([]string{"a"}, faults); err != nil {
		t.Fatalf("firstFault over no faulting symbol = %v, want nil", err)
	}
}

// The strict observed build promotes a proof-capture fault exactly as a
// construction fault: the per-target rebuild over a tree that moves
// between its construction and its proof capture reports the capture's
// refusal, never a union missing the symbols.
func TestStrictObservedBuildPromotesACaptureFault(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture tree")
	}
	tmp := t.TempDir()
	if err := os.CopyFS(tmp, os.DirFS(fixtureDir)); err != nil {
		t.Fatal(err)
	}
	tr, err := Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	prior := observedUnionHook
	observedUnionHook = func([]string) {
		if err := os.Remove(filepath.Join(tmp, "lib", "lib.go")); err != nil {
			t.Fatal(err)
		}
	}
	defer func() { observedUnionHook = prior }()
	union, err := tr.newStrictObservedViews(context.Background(), []string{"example.com/fixture/lib.Add", "example.com/fixture/lib.TestAdd"}, tr.eng.PackageContextContext, tr.newSubjectEngines(nil, false, 0))
	if err == nil {
		t.Fatalf("strict observed build over a tree that moved at the proof capture returned a union of %d symbols, want the capture fault", len(union.bySymbol))
	}
	// The capture's own refusal: the moved file's subject is gone from
	// the analysis scan — not a construction fault, which the hook's
	// placement makes unreachable here.
	if !strings.Contains(err.Error(), "not found in selected source") {
		t.Fatalf("strict observed build fault = %v, want the proof capture's refusal", err)
	}
}

// A supplementary view built beside a campaign's set carries the set's
// width in its evidence environment: a width-reading oracle the set
// lacks (the moved-pin attribution's case) is judged under the same
// environment as its siblings, never named moved because a standalone
// inspection's unbounded width crept in (REQ-exec-oracle-parallelism).
//
//gofresh:pure
func TestSupplementaryViewsCarryThePrebuiltSetsWidth(t *testing.T) {
	tr := fixtureTree(t)
	ctx := context.Background()
	set, faults, err := tr.buildSubjectViews(ctx, []string{"example.com/fixture/lib.Add"}, tr.eng.PackageContextContext, tr.newSubjectEngines(nil, false, 3))
	if err != nil || len(faults) != 0 {
		t.Fatalf("build: %v %v", faults, err)
	}
	views, err := tr.viewsFor(ctx, []string{"example.com/fixture/lib.Add", "example.com/fixture/lib.TestAdd"}, set, false)
	if err != nil {
		t.Fatal(err)
	}
	// The expectation is the evidence composition itself — the cap only
	// narrows, so under an ambient GOMAXPROCS at or below 3 (the
	// self-host check's own witness width, say) nothing is injected on
	// either side and the two compositions agree; under a wider ambient
	// the set's width is injected and the standalone's is not.
	want := strings.Join(engine.OracleEvidenceEnv(tr.eng.GoEnv(), 3), " ")
	for _, symbol := range []string{"example.com/fixture/lib.Add", "example.com/fixture/lib.TestAdd"} {
		if env := strings.Join(views[symbol].env, " "); env != want {
			t.Fatalf("%s judges under %q, want the set's evidence environment %q", symbol, env, want)
		}
	}
	standalone, err := tr.viewsFor(ctx, []string{"example.com/fixture/lib.TestAdd"}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if env, plain := strings.Join(standalone["example.com/fixture/lib.TestAdd"].env, " "), strings.Join(engine.OracleEvidenceEnv(tr.eng.GoEnv(), 0), " "); env != plain {
		t.Fatalf("a standalone inspection's view judges under %q, want the width-free environment %q", env, plain)
	}
	if ambient, ok := engineGOMAXPROCS(tr.eng.GoEnv()); !ok || ambient > 3 {
		if want == strings.Join(engine.OracleEvidenceEnv(tr.eng.GoEnv(), 0), " ") {
			t.Fatal("the set's width was not injected over a wider ambient — the pin would be vacuous")
		}
	}
}

// engineGOMAXPROCS reports the environment's last well-formed
// positive GOMAXPROCS, mirroring the engine's own reading.
func engineGOMAXPROCS(env []string) (int, bool) {
	value, ok := 0, false
	for _, kv := range env {
		if rest, found := strings.CutPrefix(kv, "GOMAXPROCS="); found {
			n := 0
			for _, r := range rest {
				if r < '0' || r > '9' {
					n = -1
					break
				}
				n = n*10 + int(r-'0')
			}
			value, ok = n, n > 0
		}
	}
	return value, ok
}
