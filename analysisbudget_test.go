package gomutant

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/greatliontech/gofresh"
)

// The analysis event's head carries the unit's position when the engine
// knows it, and a served summary its memo class and package count — the
// words both faces' cadence lines and heartbeats read
// (REQ-exec-run-status).
func TestAnalysisEventHeadCarriesTheUnitPosition(t *testing.T) {
	for _, row := range []struct {
		event AnalysisEvent
		want  string
	}{
		{AnalysisEvent{Phase: "prove", Package: "example.com/p", Index: 3, Total: 40}, "proving oracle closure freshness (gofresh hash proof) example.com/p (3/40)"},
		{AnalysisEvent{Phase: "typecheck", Total: 3}, "type-checking package graphs (gofresh analysis) (of 3)"},
		{AnalysisEvent{Phase: "load", Package: "example.com/p"}, "loading package graphs (gofresh analysis) example.com/p"},
		{AnalysisEvent{Phase: "served", Served: "observability proof", Index: 12}, "served from the persistent memo: observability proof for 12 packages"},
		{AnalysisEvent{Phase: "served", Served: "effect scan", Index: 1}, "served from the persistent memo: effect scan for 1 package"},
		{AnalysisEvent{Phase: "budget-exhausted", Detail: "analysis budget 5m0s exhausted: 3 of 40 subjects unproven"}, "freshness-proof pass cut by the analysis budget"},
		{AnalysisEvent{Phase: "novel", Package: "p", Index: 2, Total: 5}, "novel p (2/5)"},
	} {
		if got := row.event.Head(); got != row.want {
			t.Errorf("Head(%+v) = %q, want %q", row.event, got, row.want)
		}
	}
	cut := AnalysisEvent{Phase: "budget-exhausted", Detail: "analysis budget 5m0s exhausted: 3 of 40 subjects unproven"}
	if got, want := cut.Text(), "freshness-proof pass cut by the analysis budget — analysis budget 5m0s exhausted: 3 of 40 subjects unproven"; got != want {
		t.Errorf("Text = %q, want %q", got, want)
	}
	if got, want := StretchAnalysis(AnalysisEvent{Phase: "prove", Package: "example.com/p", Index: 3, Total: 40}), "analysis proving oracle closure freshness (gofresh hash proof) example.com/p (3/40)"; got != want {
		t.Errorf("StretchAnalysis = %q, want %q", got, want)
	}
}

// A keep-alive names a stretch only for the engine's per-unit phases:
// a fact about an operation (a served summary, a cancellation, a budget
// cut) and a payload-bearing diagnostic name none, so neither face's
// cadence surface reads a finished operation as work in flight
// (REQ-exec-run-status).
func TestAnalysisEventStretchIsThePerUnitPhases(t *testing.T) {
	// The set is the engine's — gofresh.UnitPhases, pinned here by its
	// literal so a phase the engine adds or drops is a conscious move
	// on this face too.
	unit := []string{"list", "typecheck", "load", "hash", "observe", "runtime", "prove"}
	if got := gofresh.UnitPhases(); !reflect.DeepEqual(got, unit) {
		t.Fatalf("gofresh.UnitPhases() = %v, want %v", got, unit)
	}
	for _, phase := range unit {
		label, ok := (AnalysisEvent{Phase: phase, Package: "p", Index: 1, Total: 2}).Stretch()
		if !ok || label != StretchAnalysis(AnalysisEvent{Phase: phase, Package: "p", Index: 1, Total: 2}) {
			t.Errorf("%s: Stretch() = %q, %v; want the analysis stretch", phase, label, ok)
		}
	}
	for _, event := range []AnalysisEvent{
		{Phase: "served", Served: "observability proof", Index: 12},
		{Phase: "cancelled", Detail: "1 observability proof slice and 1 scan persisted this operation; a rerun serves them"},
		{Phase: "budget-exhausted", Detail: "analysis budget 1s exhausted: 1 subject unproven"},
		{Phase: "analysis-unavailable", Package: "p", Detail: "F: no analyzer"},
		{Phase: "toolchain-unaudited", Detail: "go1.99 unlisted"},
		{Phase: "baseline-output", Package: "p", Detail: "--- FAIL"},
		{Phase: "prove", Package: "p", Detail: "a diagnostic on a unit phase"},
		{Phase: "novel"},
	} {
		if label, ok := event.Stretch(); ok {
			t.Errorf("%+v: Stretch() = %q, true; want no stretch", event, label)
		}
	}
	if got, want := (AnalysisEvent{Phase: "served"}).Head(), "served from the persistent memo"; got != want {
		t.Errorf("served head without a class = %q, want %q", got, want)
	}
}

// The engine's progress event projects onto the run's event field for
// field: a consumer subscribing to the class reads what the engine said.
func TestAnalysisEventOfKeepsEveryEngineField(t *testing.T) {
	p := gofresh.Progress{Phase: "prove", Package: "example.com/p", Index: 3, Total: 40, Served: "", Detail: "d"}
	if got, want := analysisEventOf(p), (AnalysisEvent{Phase: "prove", Package: "example.com/p", Index: 3, Total: 40, Detail: "d"}); got != want {
		t.Fatalf("analysisEventOf = %+v, want %+v", got, want)
	}
	if got := analysisEventOf(gofresh.Progress{Phase: "served", Served: "observability proof", Index: 2}); got.Served != "observability proof" || got.Index != 2 {
		t.Fatalf("served projection = %+v", got)
	}
}

// The two freshness-proof passes are priced before they are paid: the
// decision-view build and the observed union each announce the subjects
// and packages they cover, after the targets' freshness events and
// before any target's mutants (REQ-exec-run-status).
func TestFreshnessProofPassesArePricedBeforeTheyArePaid(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	var mu sync.Mutex
	var events []PreparationEvent
	target := Target{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestAdd"}}
	if _, err := tr.Run(context.Background(), []Target{target}, Options{Budget: 1, Progress: func(e PreparationEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, e)
	}}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	stages := make([]string, 0, len(events))
	var views, proofs PreparationEvent
	for _, e := range events {
		stages = append(stages, string(e.Stage))
		switch e.Stage {
		case PreparationViews:
			views = e
		case PreparationProofs:
			proofs = e
		}
	}
	if views.Subjects != 2 || views.Packages != 1 || proofs.Subjects != 2 || proofs.Packages != 1 {
		t.Fatalf("priced passes: views %+v, proofs %+v (stages %v); want the target and its oracle over one package on both", views, proofs, stages)
	}
	at := func(stage PreparationStage) int { return slices.Index(stages, string(stage)) }
	if !(at(PreparationFreshness) < at(PreparationViews) && at(PreparationViews) < at(PreparationProofs) && at(PreparationProofs) < at(PreparationMutants)) {
		t.Fatalf("stage order %v; want freshness < views < proofs < mutants", stages)
	}
	if got, want := views.Text(), "views 2 subjects over 1 package"; got != want {
		t.Fatalf("views line = %q, want %q", got, want)
	}
	if got, want := proofs.Text(), "proofs 2 subjects over 1 package"; got != want {
		t.Fatalf("proofs line = %q, want %q", got, want)
	}
	// A mixed run prices one build per mode present, cross-package
	// first: a target with an oracle in another package beside a
	// same-package target.
	mixed := []Target{
		{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestAdd"}},
		{Symbol: "example.com/fixture/counting.Value", Oracle: []string{"example.com/fixture/lib.TestAdd"}},
	}
	var pricedMu sync.Mutex
	var priced []PreparationEvent
	if _, err := fixtureTree(t).Run(context.Background(), mixed, Options{Budget: 1, Progress: func(e PreparationEvent) {
		pricedMu.Lock()
		defer pricedMu.Unlock()
		if e.Stage == PreparationViews {
			priced = append(priced, e)
		}
	}}); err != nil {
		t.Fatal(err)
	}
	want := []PreparationEvent{{Stage: PreparationViews, Subjects: 2, Packages: 2}, {Stage: PreparationViews, Subjects: 2, Packages: 1}}
	if !slices.Equal(priced, want) {
		t.Fatalf("mixed run's views events = %+v, want the cross-package mode's first, then the same-package mode's: %+v", priced, want)
	}
}

// A proof pass the analysis budget cuts lands the subjects it left
// unproven with an unavailable proof naming the budget, the target's
// measurement stands, the cut is reported once on the analysis
// channel, and a subject whose reuse rests on the proof — an
// I/O-dependent closure whose completed observation the proof would
// have vouched for — re-executes before reuse: a second run measures
// again where the proven record served (REQ-exec-analysis-budget).
func TestAnalysisBudgetCutLandsAnUnavailableProof(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	fixture := filepath.Join(t.TempDir(), "fixture.txt")
	if err := os.WriteFile(fixture, []byte("stable"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOMUTANT_EXTERNAL_FIXTURE", fixture)
	target := Target{Symbol: "example.com/fixture/extinput.Flag", Oracle: []string{"example.com/fixture/extinput.TestFlag"}}
	var postures []RecordPosture
	posture := func(p RecordPosture) { postures = append(postures, p) }
	proven, err := fixtureTree(t).Run(context.Background(), []Target{target}, Options{Budget: 1, BracketPaths: []string{fixture}, Posture: posture})
	if err != nil || len(proven) != 1 || !proven[0].TargetEvidence.ObservationObservable {
		t.Fatalf("unbudgeted run = %+v, %v; want a proven, bound record", proven, err)
	}
	var mu sync.Mutex
	var cuts []AnalysisEvent
	findings, err := fixtureTree(t).Run(context.Background(), []Target{target}, Options{Budget: 1, BracketPaths: []string{fixture}, AnalysisBudget: time.Nanosecond, Posture: posture, AnalysisEvent: func(e AnalysisEvent) {
		if e.Phase == "budget-exhausted" {
			mu.Lock()
			defer mu.Unlock()
			cuts = append(cuts, e)
		}
	}})
	if err != nil || len(findings) != 1 {
		t.Fatalf("budgeted run = %+v, %v; want the target measured, never refused or stalled", findings, err)
	}
	f := findings[0]
	if f.Mutants == 0 {
		t.Fatalf("the cut pass measured nothing: %+v", f)
	}
	const reason = "observation analysis unavailable: analysis budget 1ns exhausted: "
	for _, e := range append([]SubjectEvidence{f.TargetEvidence}, f.OracleEvidence...) {
		if e.ObservationObservable || !strings.Contains(e.ObservationReason, reason) {
			t.Fatalf("evidence for %s = observable %v, reason %q; want an unavailable proof naming the budget", e.Symbol, e.ObservationObservable, e.ObservationReason)
		}
	}
	mu.Lock()
	if len(cuts) == 0 || !strings.HasPrefix(cuts[0].Detail, "analysis budget 1ns exhausted: ") {
		t.Fatalf("budget-exhausted events = %+v; want the cut reported on the analysis channel", cuts)
	}
	mu.Unlock()
	// The discriminator is the fixture's own shape: its oracle reaches
	// file I/O, so the record never reads current, but the TARGET's
	// reuse rests on its proof — proven, the target's evidence stands
	// (its run's posture names the oracle alone and no stored
	// observation); cut, the target's reuse is refused on the unproven
	// I/O dependence and the stored observation names the budget, and
	// a second run re-measures under that refusal where the proven
	// record's kills were served.
	channels := func(p RecordPosture) (target, stored bool) {
		for _, r := range p.Reasons {
			target = target || (r.Channel == PostureFreshness && strings.HasPrefix(r.Reason, targetReasonPrefix))
			stored = stored || (r.Channel == PostureStoredObservation && strings.Contains(r.Reason, "analysis budget 1ns exhausted"))
		}
		return target, stored
	}
	if len(postures) != 2 {
		t.Fatalf("postures = %+v", postures)
	}
	if target, stored := channels(postures[0]); target || stored {
		t.Fatalf("proven record's posture = %+v; want neither a target refusal nor a stored-observation reason", postures[0])
	}
	if target, stored := channels(postures[1]); !target || !stored {
		t.Fatalf("budget-cut record's posture = %+v; want the target's reuse refused and the stored observation naming the budget", postures[1])
	}
	second := func(prior []Finding) RunDecision {
		var decisions []RunDecision
		if _, err := fixtureTree(t).Run(context.Background(), []Target{target}, Options{Budget: 1, BracketPaths: []string{fixture}, Prior: prior, Decision: func(d RunDecision) { decisions = append(decisions, d) }}); err != nil {
			t.Fatal(err)
		}
		if len(decisions) != 1 {
			t.Fatalf("decisions %+v", decisions)
		}
		return decisions[0]
	}
	if d := second(proven); !strings.HasPrefix(d.Reason, "served:") {
		t.Fatalf("second run over the proven record = %+v; want its kills served — the discriminating arm for the cut record", d)
	}
	if d := second(findings); !strings.HasPrefix(d.Reason, "unverifiable:") {
		t.Fatalf("second run over the budget-cut record = %+v; want a fresh measurement under the refusal, never a serve on an unavailable proof", d)
	}
	// A provably pure subject's reuse rests on no proof: cut, it serves
	// as it would have.
	pure := Target{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestAdd"}}
	cutPure, err := fixtureTree(t).Run(context.Background(), []Target{pure}, Options{Budget: 1, AnalysisBudget: time.Nanosecond})
	if err != nil || len(cutPure) != 1 || cutPure[0].TargetEvidence.ObservationObservable {
		t.Fatalf("budgeted run over a pure subject = %+v, %v; want its proof cut like any other", cutPure, err)
	}
	var decisions []RunDecision
	if _, err := fixtureTree(t).Run(context.Background(), []Target{pure}, Options{Budget: 1, Prior: cutPure, Decision: func(d RunDecision) { decisions = append(decisions, d) }}); err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 1 || decisions[0].Action != "cached" {
		t.Fatalf("second run over a budget-cut pure record = %+v; want a serve — the proof confers nothing a pure closure needs", decisions)
	}
}

// A producer validation whose proof re-establishment the budget cut —
// the engine's analysis-unavailable verdict — stamps the finding's
// evidence unverifiable under that reason and commits it; every other
// validation failure remains the target's refusal
// (REQ-exec-analysis-budget).
func TestValidationCutByTheBudgetStampsUnverifiableEvidence(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	target := Target{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestAdd"}}
	const cut = "observation proof for example.com/fixture/lib.Add: observation analysis unavailable: analysis budget 1ns exhausted: context deadline exceeded"
	unavailable := func(context.Context, *subjectViewSet) error {
		return fmt.Errorf("%w: %s", gofresh.ErrAnalysisUnavailable, cut)
	}
	seams.validateProducers = unavailable
	defer func() { seams.validateProducers = nil }()
	findings, err := fixtureTree(t).Run(context.Background(), []Target{target}, Options{Budget: 1})
	if err != nil || len(findings) != 1 {
		t.Fatalf("run under a cut validation = %+v, %v; want the finding committed", findings, err)
	}
	f := findings[0]
	if f.Mutants == 0 {
		t.Fatalf("the finding carries no measurement: %+v", f)
	}
	for _, e := range append([]SubjectEvidence{f.TargetEvidence}, f.OracleEvidence...) {
		if !e.RuntimeUnverifiable || !strings.Contains(e.RuntimeReason, cut) {
			t.Fatalf("evidence for %s = unverifiable %v, reason %q; want the cut stamped", e.Symbol, e.RuntimeUnverifiable, e.RuntimeReason)
		}
	}
	for _, sv := range f.Survivors {
		if sv.Execution != "unstable-oracle" && sv.Execution != "flipped-kill" {
			t.Fatalf("fresh survivor %s %s bucketed %q under the stamp; want unstable-oracle", sv.Position, sv.Operator, sv.Execution)
		}
	}
	seams.validateProducers = func(context.Context, *subjectViewSet) error {
		return errors.New("gofresh: view changed: a source moved")
	}
	_, err = fixtureTree(t).Run(context.Background(), []Target{target}, Options{Budget: 1})
	var drift *TreeDriftError
	if !errors.As(err, &drift) || len(drift.Drifted) != 1 || !strings.Contains(drift.Drifted[0].Reason, "a source moved") {
		t.Fatalf("run under a failed validation = %v; want the target refused with the failure", err)
	}
	// The stamp lands on every validated commit path — a served record
	// re-executing its candidate-local evidence, a killer-drift splice
	// re-measuring against the current oracle, and a budget extension —
	// and the record's exemptions are derived after it, so an exemption
	// naming the cut is honoured on those paths as on a fresh measure.
	seams.validateProducers = unavailable
	stamped := func(name string, prior []Finding, targets []Target, opts Options, exempted bool) Finding {
		t.Helper()
		var decisions []RunDecision
		opts.Prior, opts.Decision = prior, func(d RunDecision) { decisions = append(decisions, d) }
		exempt := func(symbol string) Exemption {
			return Exemption{Subject: symbol, Reason: "gofresh: observation analysis unavailable during validation: " + cut, Rationale: "the cut is the run's, not the subject's"}
		}
		if exempted {
			opts.Exemptions = []Exemption{exempt(targets[0].Symbol), exempt(targets[0].Oracle[0])}
		}
		out, err := fixtureTree(t).Run(context.Background(), targets, opts)
		if err != nil || len(out) != 1 || len(decisions) != 1 {
			t.Fatalf("%s: run = %+v, %v, decisions %+v", name, out, err, decisions)
		}
		for _, e := range append([]SubjectEvidence{out[0].TargetEvidence}, out[0].OracleEvidence...) {
			if !e.RuntimeUnverifiable || !strings.Contains(e.RuntimeReason, cut) {
				t.Fatalf("%s: evidence for %s = unverifiable %v, reason %q; want the cut stamped (decision %+v)", name, e.Symbol, e.RuntimeUnverifiable, e.RuntimeReason, decisions[0])
			}
		}
		if exempted && len(out[0].Exempted) == 0 {
			t.Fatalf("%s: the exemption naming the cut was not derived after the stamp: %+v (decision %+v)", name, out[0], decisions[0])
		}
		if !exempted {
			// Uncovered, the record-wide stamp buckets every survivor
			// unstable-oracle — on a splice as on a fresh measure — so
			// no survivor commits without an execution bucket
			// (REQ-exec-survivor-evidence).
			if len(out[0].Exempted) != 0 || len(out[0].Survivors) == 0 {
				t.Fatalf("%s: exempted %+v, survivors %d; want no exemption and survivors to bucket", name, out[0].Exempted, len(out[0].Survivors))
			}
			for _, sv := range out[0].Survivors {
				if sv.Execution != "unstable-oracle" && sv.Execution != "flipped-kill" {
					t.Fatalf("%s: survivor %s %s bucketed %q under the uncovered stamp; want unstable-oracle", name, sv.Position, sv.Operator, sv.Execution)
				}
			}
		}
		t.Logf("%s: %s", name, decisions[0].Reason)
		return out[0]
	}
	seams.validateProducers = nil
	weak := Target{Symbol: "example.com/fixture/lib.Weak", Oracle: []string{"example.com/fixture/lib.TestWeak"}}
	capped, err := fixtureTree(t).Run(context.Background(), []Target{weak}, Options{Budget: 1})
	if err != nil || len(capped) != 1 {
		t.Fatalf("capped run = %+v, %v", capped, err)
	}
	fixture := filepath.Join(t.TempDir(), "fixture.txt")
	if err := os.WriteFile(fixture, []byte("stable"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOMUTANT_EXTERNAL_FIXTURE", fixture)
	// Loose keeps survivors under the I/O-reading oracle, so its record
	// re-measures them against the current oracle — the drift splice.
	loose := Target{Symbol: "example.com/fixture/extinput.Loose", Oracle: []string{"example.com/fixture/extinput.TestFlag"}}
	proven, err := fixtureTree(t).Run(context.Background(), []Target{loose}, Options{Budget: 4, BracketPaths: []string{fixture}})
	if err != nil || len(proven) != 1 || len(proven[0].Survivors) == 0 {
		t.Fatalf("proven run = %+v, %v; want survivors to re-measure", proven, err)
	}
	// Mixed's zero-return mutant is candidate-local (its test panics)
	// and its branch's mutants survive, so the record serves with the
	// flagged candidate re-executed and survivors to bucket.
	local := Target{Symbol: "example.com/fixture/candlocal.Mixed", Oracle: []string{"example.com/fixture/candlocal.TestMixed"}}
	candidateLocal, err := fixtureTree(t).Run(context.Background(), []Target{local}, Options{})
	if err != nil || len(candidateLocal) != 1 || len(candidateLocal[0].CandidateEvidence) == 0 || len(candidateLocal[0].Survivors) == 0 {
		t.Fatalf("candidate-local run = %+v, %v; want a record whose panicking candidate is flagged for re-execution beside survivors", candidateLocal, err)
	}
	seams.validateProducers = unavailable
	stamped("extended", capped, []Target{weak}, Options{Budget: 3}, true)
	stamped("drift", proven, []Target{loose}, Options{Budget: 4, BracketPaths: []string{fixture}}, true)
	stamped("served", candidateLocal, []Target{local}, Options{}, true)
	stamped("extended, uncovered", capped, []Target{weak}, Options{Budget: 3}, false)
	stamped("drift, uncovered", proven, []Target{loose}, Options{Budget: 4, BracketPaths: []string{fixture}}, false)
	stamped("served, uncovered", candidateLocal, []Target{local}, Options{}, false)
}

// validateProducers holds an analysis-unavailable verdict across the
// set's modules: the engine's ordering guarantee is per view, so a
// sibling module's drift must still refuse whichever module answered
// first (REQ-exec-analysis-budget).
func TestValidateProducersHoldsUnavailableAcrossModules(t *testing.T) {
	unavailable := func(context.Context) error {
		return fmt.Errorf("%w: observation proof for a.F", gofresh.ErrAnalysisUnavailable)
	}
	drift := func(context.Context) error { return errors.New("gofresh: view changed: a source moved") }
	set := func(validates ...func(context.Context) error) *subjectViewSet {
		s := &subjectViewSet{}
		for _, v := range validates {
			s.modules = append(s.modules, &moduleSubjectView{producer: true, validate: v})
		}
		return s
	}
	if err := set(unavailable, drift).validateProducers(context.Background()); err == nil || errors.Is(err, gofresh.ErrAnalysisUnavailable) {
		t.Fatalf("unavailable then drift = %v; want the drift", err)
	}
	if err := set(drift, unavailable).validateProducers(context.Background()); err == nil || errors.Is(err, gofresh.ErrAnalysisUnavailable) {
		t.Fatalf("drift then unavailable = %v; want the drift", err)
	}
	if err := set(unavailable, unavailable).validateProducers(context.Background()); !errors.Is(err, gofresh.ErrAnalysisUnavailable) {
		t.Fatalf("two unavailable = %v; want the held verdict", err)
	}
	if err := set(func(context.Context) error { return nil }, unavailable).validateProducers(context.Background()); !errors.Is(err, gofresh.ErrAnalysisUnavailable) {
		t.Fatalf("clean then unavailable = %v; want the held verdict", err)
	}
	if err := set().validateProducers(context.Background()); err != nil {
		t.Fatalf("no producers = %v", err)
	}
}

// A negative analysis budget is refused with the run's other bounds,
// before any load, on the preparation and the library entry alike.
func TestAnalysisBudgetRefusedWhenNegative(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a fixture module")
	}
	ctx := context.Background()
	_, err := PrepareCampaign(ctx, CampaignInputs{FindingsPath: filepath.Join(t.TempDir(), "findings.json"), ModuleDir: fixtureDir, AnalysisBudget: -time.Second})
	if err == nil || !strings.Contains(err.Error(), "analysis budget must be non-negative") {
		t.Fatalf("preparation with a negative analysis budget = %v", err)
	}
	_, err = fixtureTree(t).Run(ctx, []Target{{Symbol: "example.com/fixture/lib.Add"}}, Options{AnalysisBudget: -time.Second})
	if err == nil || !strings.Contains(err.Error(), "analysis budget must be non-negative") {
		t.Fatalf("run with a negative analysis budget = %v", err)
	}
}
