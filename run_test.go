package gomutant

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/gomutant/internal/engine"
)

// TestRunEndToEnd pins the orchestration against the fixture tree: a
// pinned-down body kills everything, an untested branch survives with its
// labels echoed (REQ-target-labels), a prior finding with matching pins is
// served from cache (REQ-result-stale), an attested survivor carries across
// a cached serve and a pin-matching re-measure (REQ-attest-survivor), a
// budget request beyond a capped finding re-measures, and the document
// round-trips (REQ-result-export).
func TestRunEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	ctx := context.Background()
	targets := []Target{
		{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestAdd"}},
		{Symbol: "example.com/fixture/lib.Weak", Oracle: []string{"example.com/fixture/lib.TestWeak"}, Labels: []string{"REQ-weak"}},
		{Symbol: "example.com/fixture/lib.I", Oracle: []string{"example.com/fixture/lib.TestAdd"}},
	}

	var firstDecisions []RunDecision
	first, err := tr.Run(ctx, targets, Options{Decision: func(decision RunDecision) {
		firstDecisions = append(firstDecisions, decision)
	}})
	if err != nil {
		t.Fatal(err)
	}
	add, weak, iface := first[0], first[1], first[2]
	if add.Cached || add.Mutants == 0 || add.Killed != add.Mutants || len(add.Survivors) != 0 {
		t.Fatalf("Add = %+v, want all mutants killed fresh", add)
	}
	if len(add.Operators) == 0 {
		t.Fatal("Add finding omitted operator summaries")
	}
	if add.BodyHash == "" || add.TargetEvidence.Toolchain == "" || add.OperatorSet == "" || len(add.OracleEvidence) != 1 {
		t.Fatalf("Add pins incomplete: %+v", add)
	}
	if len(weak.Survivors) == 0 || weak.Labels[0] != "REQ-weak" {
		t.Fatalf("Weak = %+v, want survivors with labels echoed", weak)
	}
	if iface.Skipped != "not a function" {
		t.Fatalf("interface target = %+v, want skipped as not a function", iface)
	}
	if len(firstDecisions) != 3 || firstDecisions[0].Reason != "no-prior" || firstDecisions[1].Reason != "no-prior" || firstDecisions[2].Action != "skipped" || firstDecisions[2].Reason != "not a function" {
		t.Fatalf("first decisions = %+v", firstDecisions)
	}
	if len(weak.Open()) != len(weak.Survivors) {
		t.Fatalf("open != survivors before any attestation")
	}

	// Attest one survivor; the export/parse round trip preserves it.
	s0 := weak.Survivors[0]
	if err := weak.Attest(s0.Position, s0.Operator, "equivalent by inspection"); err != nil {
		t.Fatal(err)
	}
	if err := weak.Attest("nowhere:1:1", "no-op", "x"); err == nil {
		t.Fatal("attested a mutant that is not a survivor")
	}
	if len(weak.Open()) != len(weak.Survivors)-1 {
		t.Fatal("attestation did not close the finding")
	}
	doc, err := Export([]Finding{add, weak, iface})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(doc), "example.com/fixture/lib.I") {
		t.Fatal("a skipped result was exported")
	}
	prior, err := ParseFindings(doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(prior) != 2 {
		t.Fatalf("document findings = %d, want 2", len(prior))
	}

	// Second run under the same pins: served from cache, attestation intact.
	second, err := tr.Run(ctx, targets[:2], Options{Prior: prior})
	if err != nil {
		t.Fatal(err)
	}
	if !second[0].Cached || !second[1].Cached {
		t.Fatalf("unchanged pins re-measured: %+v %+v", second[0].Cached, second[1].Cached)
	}
	if len(second[1].Attested) != 1 || second[1].Attested[0].Reason != "equivalent by inspection" {
		t.Fatalf("attestation lost across cache: %+v", second[1].Attested)
	}

	// A moved pin re-measures instead of serving the cache, and sheds the
	// attestation: every source-evidence version's equivalences are re-judged
	// (REQ-result-stale, REQ-attest-survivor).
	tampered := append([]Finding(nil), prior...)
	for i := range tampered {
		tampered[i].TargetEvidence.MaximalClosure = "not-the-current-closure"
	}
	var movedDecisions []RunDecision
	moved, err := tr.Run(ctx, targets[:2], Options{Prior: tampered, Decision: func(decision RunDecision) {
		movedDecisions = append(movedDecisions, decision)
	}})
	if err != nil {
		t.Fatal(err)
	}
	if moved[0].Cached || moved[1].Cached {
		t.Fatal("a moved pin served from cache")
	}
	if len(moved[1].Attested) != 0 {
		t.Fatalf("attestation survived a pin move: %+v", moved[1].Attested)
	}
	if len(movedDecisions) != 2 || movedDecisions[0].Reason != "stale" || movedDecisions[1].Reason != "stale" {
		t.Fatalf("moved decisions = %+v", movedDecisions)
	}

	// A capped prior finding never answers a larger request: budget 1 is
	// re-measured fresh under budget 1, then a budget-2 request re-measures
	// again rather than serving the capped record (REQ-result-stale).
	capped, err := tr.Run(ctx, targets[:1], Options{Budget: 1})
	if err != nil {
		t.Fatal(err)
	}
	if capped[0].Cached || capped[0].Budget != 1 || capped[0].Mutants+capped[0].Discarded != 1 {
		t.Fatalf("budget-1 run = %+v", capped[0])
	}
	cappedDoc, err := Export(capped)
	if err != nil {
		t.Fatal(err)
	}
	cappedPrior, err := ParseFindings(cappedDoc)
	if err != nil {
		t.Fatal(err)
	}
	var widerDecisions []RunDecision
	wider, err := tr.Run(ctx, targets[:1], Options{Budget: 2, Prior: cappedPrior, Decision: func(decision RunDecision) {
		widerDecisions = append(widerDecisions, decision)
	}})
	if err != nil {
		t.Fatal(err)
	}
	if wider[0].Cached {
		t.Fatal("a capped finding answered a larger budget request")
	}
	if len(widerDecisions) != 1 || widerDecisions[0].Reason != "budget" {
		t.Fatalf("wider decisions = %+v", widerDecisions)
	}
	// And the same capped request is served from the capped record.
	same, err := tr.Run(ctx, targets[:1], Options{Budget: 1, Prior: cappedPrior})
	if err != nil {
		t.Fatal(err)
	}
	if !same[0].Cached {
		t.Fatal("a covering capped finding was re-measured")
	}
}

func TestSummarizeRun(t *testing.T) {
	findings := []Finding{
		{Symbol: "p.Measured", Generated: 4, Mutants: 3, Killed: 2, Discarded: 1,
			Survivors: []Survivor{{Position: "p.go:1:1", Operator: "x"}}},
		{Symbol: "p.Cached", Cached: true, Generated: 2, Mutants: 2, Killed: 1,
			Survivors: []Survivor{{Position: "p.go:2:1", Operator: "x"}},
			Attested:  []Attestation{{Position: "p.go:2:1", Operator: "x", Reason: "same"}}},
		{Symbol: "p.Skipped", Skipped: "no oracle"},
	}
	want := RunSummary{Targets: 3, Measured: 1, Cached: 1, Skipped: 1, Generated: 6, Discarded: 1, Killed: 3, Survived: 2, Attested: 1, Open: 1}
	if got := SummarizeRun(findings); got != want {
		t.Fatalf("summary = %+v, want %+v", got, want)
	}
}

func TestRunConservesCandidateDiscards(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	oracle := []string{"example.com/fixture/lib.TestAdd"}
	findings, err := tr.Run(context.Background(), []Target{
		{Symbol: "example.com/fixture/lib.BigLit", Oracle: oracle},
		{Symbol: "example.com/fixture/lib.Dup", Oracle: oracle},
		{Symbol: "example.com/fixture/lib.Idx", Oracle: oracle},
	}, Options{Jobs: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 3 {
		t.Fatalf("findings = %d", len(findings))
	}
	for _, finding := range findings {
		if finding.Generated != finding.Mutants+finding.Discarded || finding.Mutants != finding.Killed+len(finding.Survivors) || finding.Generated != finding.CandidateCount {
			t.Fatalf("%s counts do not reconcile: %+v", finding.Symbol, finding)
		}
		generated, discarded := 0, 0
		for _, summary := range finding.Operators {
			generated += summary.Generated
			discarded += summary.Discarded
		}
		if generated != finding.Generated || discarded != finding.Discarded {
			t.Fatalf("%s operator totals do not reconcile: %+v", finding.Symbol, finding.Operators)
		}
	}
	if big := findings[0]; big.Generated < 1 || big.Discarded < 1 || big.Mutants != big.Generated-big.Discarded {
		t.Fatalf("no-op candidate was not conserved: %+v", big)
	}
	if dup := findings[1]; dup.Discarded < 1 {
		t.Fatalf("duplicate candidate was not conserved: %+v", dup)
	}
	if idx := findings[2]; idx.Discarded < 1 {
		t.Fatalf("compile-rejected candidate was not conserved: %+v", idx)
	}
}

func TestRunDecisionsAndCancellation(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	target := Target{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestAdd"}}
	type runStatus struct {
		preparation []PreparationEvent
		decisions   []RunDecision
		timeline    []string
	}
	collect := func(ctx context.Context, opts Options) ([]Finding, runStatus, error) {
		var status runStatus
		opts.Progress = func(event PreparationEvent) {
			status.preparation = append(status.preparation, event)
			status.timeline = append(status.timeline, "prepare")
		}
		var decisions []RunDecision
		opts.Decision = func(decision RunDecision) {
			decisions = append(decisions, decision)
			status.timeline = append(status.timeline, "decision")
		}
		findings, err := tr.Run(ctx, []Target{target}, opts)
		status.decisions = decisions
		return findings, status, err
	}
	first, firstStatus, err := collect(context.Background(), Options{Budget: 1, Jobs: 1})
	if err != nil {
		t.Fatal(err)
	}
	decisions := firstStatus.decisions
	if want := (RunDecision{Symbol: target.Symbol, Action: "measure", Reason: "no-prior", Candidates: 1}); len(decisions) != 1 || decisions[0] != want {
		t.Fatalf("first decisions = %+v, want %+v", decisions, want)
	}
	wantPreparation := []PreparationEvent{
		{Stage: PreparationResolving, Symbol: target.Symbol},
		{Stage: PreparationFreshness, Symbol: target.Symbol},
		{Stage: PreparationMutants, Symbol: target.Symbol},
		{Stage: PreparationBaseline, Symbol: target.Symbol, Package: "example.com/fixture/lib"},
	}
	if !slices.Equal(firstStatus.preparation, wantPreparation) || !slices.Equal(firstStatus.timeline, []string{"prepare", "prepare", "prepare", "prepare", "decision"}) {
		t.Fatalf("first status = preparation %+v, timeline %v", firstStatus.preparation, firstStatus.timeline)
	}
	_, cachedStatus, err := collect(context.Background(), Options{Budget: 1, Prior: first})
	if err != nil || len(cachedStatus.decisions) != 1 || cachedStatus.decisions[0].Action != "cached" {
		t.Fatalf("cached status = %+v, %v", cachedStatus, err)
	}
	if want := wantPreparation[:2]; !slices.Equal(cachedStatus.preparation, want) || !slices.Equal(cachedStatus.timeline, []string{"prepare", "prepare", "decision"}) {
		t.Fatalf("cached preparation = %+v, timeline %v", cachedStatus.preparation, cachedStatus.timeline)
	}
	_, forcedStatus, err := collect(context.Background(), Options{Budget: 1, Prior: first, Force: true, Jobs: 4})
	if err != nil || len(forcedStatus.decisions) != 1 || forcedStatus.decisions[0].Reason != "forced" {
		t.Fatalf("forced status = %+v, %v", forcedStatus, err)
	}
	if !slices.Equal(forcedStatus.preparation, firstStatus.preparation) {
		t.Fatalf("worker count changed preparation: jobs 1 %+v, jobs 4 %+v", firstStatus.preparation, forcedStatus.preparation)
	}
	mutableTargets := []Target{{Symbol: target.Symbol, Oracle: []string{"example.com/fixture/lib.TestAdd"}}}
	mutablePrior := append([]Finding(nil), first...)
	snapshotted, err := tr.Run(context.Background(), mutableTargets, Options{
		Budget: 1,
		Prior:  mutablePrior,
		Progress: func(PreparationEvent) {
			mutableTargets[0].Symbol = "example.com/fixture/lib.Missing"
			mutableTargets[0].Oracle[0] = "example.com/fixture/lib.TestMissing"
			mutablePrior[0].TargetEvidence.MaximalClosure = "moved"
		},
	})
	if err != nil || len(snapshotted) != 1 || !snapshotted[0].Cached || snapshotted[0].Symbol != target.Symbol {
		t.Fatalf("callback mutated snapshotted inputs: findings %+v, error %v", snapshotted, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	findings, cancelledStatus, err := collect(ctx, Options{Budget: 1})
	if !errors.Is(err, context.Canceled) || findings != nil || len(cancelledStatus.preparation) != 0 || len(cancelledStatus.decisions) != 0 {
		t.Fatalf("cancelled run = findings %+v, status %+v, error %v", findings, cancelledStatus, err)
	}
}

func TestRunCancellationAtBatchedFreshness(t *testing.T) {
	tr := fixtureTree(t)
	ctx, cancel := context.WithCancel(context.Background())
	var preparation []PreparationEvent
	findings, err := tr.Run(ctx, []Target{{
		Symbol: "example.com/fixture/lib.Add",
		Oracle: []string{"example.com/fixture/lib.TestAdd"},
	}}, Options{
		Budget: 1,
		Progress: func(event PreparationEvent) {
			preparation = append(preparation, event)
			if event.Stage == PreparationFreshness {
				cancel()
			}
		},
	})
	want := []PreparationEvent{
		{Stage: PreparationResolving, Symbol: "example.com/fixture/lib.Add"},
		{Stage: PreparationFreshness, Symbol: "example.com/fixture/lib.Add"},
	}
	if !errors.Is(err, context.Canceled) || findings != nil || !slices.Equal(preparation, want) {
		t.Fatalf("cancelled freshness = findings %+v, preparation %+v, error %v", findings, preparation, err)
	}
}

func TestRunCancellationAtMutantPreparation(t *testing.T) {
	tr := fixtureTree(t)
	ctx, cancel := context.WithCancel(context.Background())
	var preparation []PreparationEvent
	var decisions []RunDecision
	findings, err := tr.Run(ctx, []Target{{
		Symbol: "example.com/fixture/lib.Add",
		Oracle: []string{"example.com/fixture/lib.TestAdd"},
	}}, Options{
		Budget:   1,
		Decision: func(decision RunDecision) { decisions = append(decisions, decision) },
		Progress: func(event PreparationEvent) {
			preparation = append(preparation, event)
			if event.Stage == PreparationMutants {
				cancel()
			}
		},
	})
	want := []PreparationEvent{
		{Stage: PreparationResolving, Symbol: "example.com/fixture/lib.Add"},
		{Stage: PreparationFreshness, Symbol: "example.com/fixture/lib.Add"},
		{Stage: PreparationMutants, Symbol: "example.com/fixture/lib.Add"},
	}
	if !errors.Is(err, context.Canceled) || findings != nil || len(decisions) != 0 || !slices.Equal(preparation, want) {
		t.Fatalf("cancelled mutants = findings %+v, preparation %+v, decisions %+v, error %v", findings, preparation, decisions, err)
	}
}

func TestRunCancellationDuringDecisionsPublishesNoFindings(t *testing.T) {
	if testing.Short() {
		t.Skip("runs oracle baseline")
	}
	tr := fixtureTree(t)
	ctx, cancel := context.WithCancel(context.Background())
	var decisions []RunDecision
	findings, err := tr.Run(ctx, []Target{
		{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestAdd"}},
		{Symbol: "example.com/fixture/lib.Weak", Oracle: []string{"example.com/fixture/lib.TestAdd"}},
	}, Options{
		Budget: 1,
		Decision: func(decision RunDecision) {
			decisions = append(decisions, decision)
			cancel()
		},
	})
	if !errors.Is(err, context.Canceled) || findings != nil || len(decisions) != 1 {
		t.Fatalf("cancelled decisions = findings %+v, decisions %+v, error %v", findings, decisions, err)
	}
}

func TestRunCancellationBeforeAggregationPublishesNoFindings(t *testing.T) {
	if testing.Short() {
		t.Skip("runs one mutant")
	}
	tr := fixtureTree(t)
	ctx, cancel := context.WithCancel(context.Background())
	aggregated := 0
	findings, err := tr.Run(ctx, []Target{{
		Symbol: "example.com/fixture/lib.Add",
		Oracle: []string{"example.com/fixture/lib.TestAdd"},
	}}, Options{Budget: 1, afterExecution: cancel, aggregate: func() { aggregated++ }})
	if !errors.Is(err, context.Canceled) || findings != nil || aggregated != 0 {
		t.Fatalf("cancelled aggregation = findings %+v, aggregation calls %d, error %v", findings, aggregated, err)
	}
}

func TestRunValidatesBatchedProducerBeforeFindings(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tmp := t.TempDir()
	if err := os.CopyFS(tmp, os.DirFS(fixtureDir)); err != nil {
		t.Fatal(err)
	}
	tr, err := Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	drift := filepath.Join(tmp, "lib", "doc.go")
	original, err := os.ReadFile(drift)
	if err != nil {
		t.Fatal(err)
	}
	findings, err := tr.Run(context.Background(), []Target{{
		Symbol: "example.com/fixture/lib.Add",
		Oracle: []string{"example.com/fixture/lib.TestAdd"},
	}}, Options{
		Budget: 1,
		Decision: func(RunDecision) {
			if writeErr := os.WriteFile(drift, append(original, []byte("\n// drift\n")...), 0o644); writeErr != nil {
				t.Fatal(writeErr)
			}
		},
	})
	if err == nil || !strings.Contains(err.Error(), "analysis view changed") || findings != nil {
		t.Fatalf("producer drift = findings %+v, error %v", findings, err)
	}
}

func TestRunValidatesEveryProducerModule(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tmp := t.TempDir()
	if err := os.CopyFS(tmp, os.DirFS("internal/engine/testdata/workspacemod")); err != nil {
		t.Fatal(err)
	}
	tr, err := Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	drift := filepath.Join(tmp, "sub", "sub.go")
	original, err := os.ReadFile(drift)
	if err != nil {
		t.Fatal(err)
	}
	findings, err := tr.Run(context.Background(), []Target{{
		Symbol: "example.com/ws.Root",
		Oracle: []string{"example.com/ws/sub.TestNested"},
	}}, Options{
		Budget: 1,
		Decision: func(RunDecision) {
			if writeErr := os.WriteFile(drift, append(original, []byte("\n// oracle drift\n")...), 0o644); writeErr != nil {
				t.Fatal(writeErr)
			}
		},
	})
	if err == nil || !strings.Contains(err.Error(), "analysis view changed") || findings != nil {
		t.Fatalf("oracle-module drift = findings %+v, error %v", findings, err)
	}
}

func TestRunValidatesAfterMutantProcesses(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tmp := t.TempDir()
	if err := os.CopyFS(tmp, os.DirFS(fixtureDir)); err != nil {
		t.Fatal(err)
	}
	drift := filepath.Join(tmp, "lib", "doc.go")
	t.Setenv("GOMUTANT_DRIFT_SOURCE", drift)
	tr, err := Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	findings, err := tr.Run(context.Background(), []Target{{
		Symbol: "example.com/fixture/lib.Add",
		Oracle: []string{"example.com/fixture/lib.TestDriftSource"},
	}}, Options{Budget: 1})
	if err == nil || !strings.Contains(err.Error(), "analysis view changed") || findings != nil {
		t.Fatalf("post-mutant drift = findings %+v, error %v", findings, err)
	}
}

func TestRunValidatesZeroMutantProducer(t *testing.T) {
	if testing.Short() {
		t.Skip("constructs freshness views")
	}
	tmp := t.TempDir()
	if err := os.CopyFS(tmp, os.DirFS(fixtureDir)); err != nil {
		t.Fatal(err)
	}
	tr, err := Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	drift := filepath.Join(tmp, "lib", "doc.go")
	original, err := os.ReadFile(drift)
	if err != nil {
		t.Fatal(err)
	}
	findings, err := tr.Run(context.Background(), []Target{{
		Symbol: "example.com/fixture/lib.F",
		Oracle: []string{"example.com/fixture/lib.TestVacuous"},
	}}, Options{
		Decision: func(RunDecision) {
			if writeErr := os.WriteFile(drift, append(original, []byte("\n// zero-mutant drift\n")...), 0o644); writeErr != nil {
				t.Fatal(writeErr)
			}
		},
	})
	if err == nil || !strings.Contains(err.Error(), "analysis view changed") || findings != nil {
		t.Fatalf("zero-mutant drift = findings %+v, error %v", findings, err)
	}
}

func TestSnapshotRunInputsPreservesEmptySlices(t *testing.T) {
	target := snapshotTargets([]Target{{Oracle: []string{}, Labels: []string{}}})[0]
	if target.Oracle == nil || target.Labels == nil {
		t.Fatalf("target snapshot lost non-nil empties: %+v", target)
	}
	finding := snapshotFindings([]Finding{{
		Labels:         []string{},
		OracleEvidence: []SubjectEvidence{},
		Operators:      []OperatorSummary{},
		Survivors:      []Survivor{},
		Attested:       []Attestation{},
	}})[0]
	if finding.Labels == nil || finding.OracleEvidence == nil || finding.Operators == nil || finding.Survivors == nil || finding.Attested == nil {
		t.Fatalf("finding snapshot lost non-nil empties: %+v", finding)
	}
}

func TestRunReportsSharedBaselineOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	targets := []Target{
		{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestAdd"}},
		{Symbol: "example.com/fixture/lib.Weak", Oracle: []string{"example.com/fixture/lib.TestAdd"}},
	}
	var preparation []PreparationEvent
	if _, err := tr.Run(context.Background(), targets, Options{
		Budget:   1,
		Progress: func(event PreparationEvent) { preparation = append(preparation, event) },
	}); err != nil {
		t.Fatal(err)
	}
	var baselines []PreparationEvent
	for _, event := range preparation {
		if event.Stage == PreparationBaseline {
			baselines = append(baselines, event)
		}
	}
	if want := []PreparationEvent{{Stage: PreparationBaseline, Symbol: targets[0].Symbol, Package: "example.com/fixture/lib"}}; !slices.Equal(baselines, want) {
		t.Fatalf("baseline preparation = %+v, want %+v", baselines, want)
	}
	wantStages := []PreparationEvent{
		{Stage: PreparationResolving, Symbol: targets[0].Symbol},
		{Stage: PreparationFreshness, Symbol: targets[0].Symbol},
		{Stage: PreparationResolving, Symbol: targets[1].Symbol},
		{Stage: PreparationFreshness, Symbol: targets[1].Symbol},
		{Stage: PreparationMutants, Symbol: targets[0].Symbol},
		{Stage: PreparationBaseline, Symbol: targets[0].Symbol, Package: "example.com/fixture/lib"},
		{Stage: PreparationMutants, Symbol: targets[1].Symbol},
	}
	if !slices.Equal(preparation, wantStages) {
		t.Fatalf("batched preparation = %+v, want %+v", preparation, wantStages)
	}
}

func TestRunRapidClassificationIncludesLaterTargets(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	t.Setenv("GOMUTANT_REQUIRE_RAPID_FLAG", "1")
	tree := fixtureTree(t)
	targets := []Target{
		{Symbol: "example.com/fixture/plain.Ok", Oracle: []string{"example.com/fixture/plain.TestPlain"}},
		{Symbol: "example.com/fixture/extprop.Ok", Oracle: []string{"example.com/fixture/extprop.TestExtProp"}},
	}
	findings, err := tree.Run(context.Background(), targets, Options{Budget: 1, Jobs: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 || findings[0].Mutants != 1 || findings[1].Mutants != 1 {
		t.Fatalf("findings = %+v", findings)
	}
}

func TestRunRemeasuresGeneratedFixtureEvidence(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	target := Target{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestGeneratedFixture"}}
	first, err := tr.Run(context.Background(), []Target{target}, Options{Budget: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || !first[0].TargetEvidence.RuntimeUnverifiable || first[0].TargetEvidence.RuntimeReason == "" {
		t.Fatalf("generated-fixture finding = %+v", first)
	}
	data, err := Export(first)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := ParseFindings(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(prior) != 1 || !prior[0].TargetEvidence.RuntimeUnverifiable || prior[0].TargetEvidence.RuntimeReason != first[0].TargetEvidence.RuntimeReason ||
		len(prior[0].OracleEvidence) != 1 || !prior[0].OracleEvidence[0].RuntimeUnverifiable || prior[0].OracleEvidence[0].RuntimeReason != first[0].OracleEvidence[0].RuntimeReason {
		t.Fatalf("round-tripped generated-fixture finding = %+v", prior)
	}
	var decisions []RunDecision
	second, err := tr.Run(context.Background(), []Target{target}, Options{
		Budget: 1,
		Prior:  prior,
		Decision: func(decision RunDecision) {
			decisions = append(decisions, decision)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || len(decisions) != 1 || decisions[0].Action != "measure" || decisions[0].Reason != "stale" || second[0].Cached {
		t.Fatalf("remeasure = findings %+v, decisions %+v", second, decisions)
	}
}

func TestMergeFindingObservationsMakesMovementNonReusable(t *testing.T) {
	root := t.TempDir()
	stable := filepath.Join(root, "stable")
	moving := filepath.Join(root, "moving")
	if err := os.WriteFile(stable, []byte("stable"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(moving, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := os.Environ()
	stableState, err := runtimeinput.FromTestLogEnv([]byte("open "+stable+"\n"), root, root, env)
	if err != nil {
		t.Fatal(err)
	}
	movingState, err := runtimeinput.FromTestLogEnv([]byte("open "+moving+"\n"), root, root, env)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(moving, []byte("after"), 0o644); err != nil {
		t.Fatal(err)
	}
	merged, err := mergeFindingObservations(root, env, stableState, movingState)
	if err != nil || !merged.OK || !merged.Unverifiable || !strings.Contains(merged.Reason, "could not be merged for reuse") {
		t.Fatalf("moved observation = %+v, %v", merged, err)
	}
	paths, err := runtimeinput.Paths(merged.Manifest, root)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(paths, stable) {
		t.Fatalf("runtime paths = %v, missing stable input %s", paths, stable)
	}
}

// TestRunUnionsEveryProcessObservation pins REQ-exec-observation end to end:
// distinct mutants read distinct files before the oracle kills them, and both
// identities must survive in the finding-wide runtime manifest.
func TestRunUnionsEveryProcessObservation(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	tg := Target{Symbol: "example.com/fixture/lib.PickInput", Oracle: []string{"example.com/fixture/lib.TestPickInput"}}
	findings, err := tr.Run(context.Background(), []Target{tg}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Mutants != 2 || findings[0].Killed != 2 {
		t.Fatalf("PickInput finding = %+v, want two killed mutants", findings)
	}
	paths, err := runtimeinput.Paths(findings[0].TargetEvidence.RuntimeInputs, tr.dir)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, path := range paths {
		seen[filepath.Base(path)] = true
	}
	for _, name := range []string{"input-0.txt", "input-1.txt", "input-2.txt"} {
		if !seen[name] {
			t.Fatalf("runtime paths = %v, missing %s", paths, name)
		}
	}
}

// TestAttestationShedsAcrossSourceDrift pins REQ-attest-survivor: even when
// the mutated body is unchanged, moved subject evidence requires every
// equivalence to be judged afresh.
func TestAttestationShedsAcrossSourceDrift(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
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
	targets := []Target{{Symbol: "example.com/fixture/lib.Weak", Oracle: []string{"example.com/fixture/lib.TestWeak"}}}
	first, err := tr.Run(ctx, targets, Options{})
	if err != nil {
		t.Fatal(err)
	}
	weak := first[0]
	if len(weak.Survivors) == 0 {
		t.Fatal("no survivors to attest")
	}
	s0 := weak.Survivors[0]
	if err := weak.Attest(s0.Position, s0.Operator, "equivalent"); err != nil {
		t.Fatal(err)
	}
	doc, err := Export([]Finding{weak})
	if err != nil {
		t.Fatal(err)
	}
	prior, err := ParseFindings(doc)
	if err != nil {
		t.Fatal(err)
	}

	// Shift the declaration down one line without touching its body. The
	// maximal source closure still moves, so the prior disposition is judged
	// afresh rather than inferred to be location-only.
	libPath := filepath.Join(tmp, "lib", "lib.go")
	src, err := os.ReadFile(libPath)
	if err != nil {
		t.Fatal(err)
	}
	shifted := strings.Replace(string(src), "func Weak(", "// shifted by an edit above the body\nfunc Weak(", 1)
	if shifted == string(src) {
		t.Fatal("fixture edit failed")
	}
	if err := os.WriteFile(libPath, []byte(shifted), 0o644); err != nil {
		t.Fatal(err)
	}

	tr2, err := Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	// Force the re-measure so the new evidence is produced and compared.
	moved, err := tr2.Run(ctx, targets, Options{Force: true, Prior: prior})
	if err != nil {
		t.Fatal(err)
	}
	got := moved[0]
	if got.Cached {
		t.Fatal("forced run served from cache")
	}
	if len(got.Attested) != 0 {
		t.Fatalf("attestation = %+v, want shed after closure drift", got.Attested)
	}
	if len(got.Open()) != len(got.Survivors) {
		t.Fatalf("open = %d of %d survivors after disposition shedding", len(got.Open()), len(got.Survivors))
	}
}

// TestRunDuplicateTargetRefused pins the finding-key collision guard
// (REQ-result-record keys by symbol): two targets naming one symbol are
// refused up front rather than one silently shadowing the other.
func TestRunDuplicateTargetRefused(t *testing.T) {
	tr := fixtureTree(t)
	_, err := tr.Run(context.Background(), []Target{
		{Symbol: "example.com/fixture/lib.Add"},
		{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestAdd"}},
	}, Options{})
	if err == nil || !strings.Contains(err.Error(), "duplicate target symbol") {
		t.Fatalf("duplicate targets accepted: %v", err)
	}
}

func TestRunRejectsNegativeBudget(t *testing.T) {
	tr := fixtureTree(t)
	_, err := tr.Run(context.Background(), []Target{{Symbol: "example.com/fixture/lib.Add"}}, Options{Budget: -1})
	if err == nil || !strings.Contains(err.Error(), "budget must be non-negative") {
		t.Fatalf("negative budget accepted: %v", err)
	}
}

// TestRunRejectsAmbiguousOracle pins the orchestration guard from
// REQ-target-oracle: same-named in-package and external tests cannot be mapped
// back from one displayed test event, so the run must stop before mutation.
func TestRunRejectsAmbiguousOracle(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":           "module example.com/ambiguous\n\ngo 1.26\n",
		"p.go":             "package ambiguous\n\nfunc F() int { return 1 }\n",
		"internal_test.go": "package ambiguous\n\nimport \"testing\"\nfunc TestSame(t *testing.T) {}\n",
		"external_test.go": "package ambiguous_test\n\nimport \"testing\"\nfunc TestSame(t *testing.T) {}\n",
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tree, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tree.Run(context.Background(), []Target{{
		Symbol: "example.com/ambiguous.F",
		Oracle: []string{"example.com/ambiguous.TestSame"},
	}}, Options{Budget: 1})
	if err == nil || !strings.Contains(err.Error(), "ambiguous across test package variants") {
		t.Fatalf("ambiguous oracle run = %v", err)
	}
}

// TestRunNoOracle pins the no-oracle skip: a target in a test-less package
// derives an empty oracle and is reported, never measured, never dropped.
func TestRunNoOracle(t *testing.T) {
	tr := fixtureTree(t)
	symbol := "example.com/fixture/methods.Counter.Inc"
	var preparation []PreparationEvent
	var decisions []RunDecision
	fs, err := tr.Run(context.Background(), []Target{{Symbol: symbol}}, Options{
		Progress: func(event PreparationEvent) { preparation = append(preparation, event) },
		Decision: func(decision RunDecision) { decisions = append(decisions, decision) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if fs[0].Skipped != "no oracle" {
		t.Fatalf("finding = %+v, want skipped with no oracle", fs[0])
	}
	if want := []PreparationEvent{{Stage: PreparationResolving, Symbol: symbol}}; !slices.Equal(preparation, want) || len(decisions) != 1 || decisions[0].Action != "skipped" {
		t.Fatalf("no-oracle status = preparation %+v, decisions %+v", preparation, decisions)
	}
}

func TestRunRejectsFailingOracleBaseline(t *testing.T) {
	tr := fixtureTree(t)
	var preparation []PreparationEvent
	var decisions []RunDecision
	findings, err := tr.Run(context.Background(), []Target{{
		Symbol: "example.com/fixture/lib.Add",
		Oracle: []string{"example.com/fixture/failing.TestAlwaysFails"},
	}}, Options{
		Budget:   1,
		Progress: func(event PreparationEvent) { preparation = append(preparation, event) },
		Decision: func(decision RunDecision) { decisions = append(decisions, decision) },
	})
	if err == nil || !strings.Contains(err.Error(), "oracle baseline does not pass") || findings != nil {
		t.Fatalf("failing oracle baseline = findings %+v, error %v", findings, err)
	}
	if len(preparation) != 4 || preparation[3].Stage != PreparationBaseline || len(decisions) != 0 {
		t.Fatalf("failing baseline status = preparation %+v, decisions %+v", preparation, decisions)
	}
}

// TestParseFindingsVersionAndTolerance pins the document boundary
// (REQ-result-export, REQ-result-tolerant): an unknown version is refused;
// an unknown field within a known version is discarded.
func TestParseFindingsVersionAndTolerance(t *testing.T) {
	if _, err := ParseFindings([]byte(`{"version": 99, "findings": []}`)); err == nil {
		t.Fatal("unknown version accepted")
	}
	fs, err := ParseFindings([]byte(`{"version":1,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":0,"targetEvidence":{"symbol":"p.F","maximalClosure":"c","toolchain":"go","buildConfig":"b","runtimeInputs":"m","runtimeDigest":"d"},"oracleEvidence":[{"symbol":"p.TestF","maximalClosure":"tc","toolchain":"go","buildConfig":"b","runtimeInputs":"m","runtimeDigest":"d"}],"oracleExplicit":true,"oracleTimeout":"1m0s","dirty":true,"candidateCount":0,"generated":0,"mutants":0,"killed":0,"discarded":0,"operators":[],"futureField":{"nested":true}}]}`))
	if err != nil || len(fs) != 1 || fs[0].Symbol != "p.F" {
		t.Fatalf("tolerant parse failed: %v %+v", err, fs)
	}
	for name, doc := range map[string]string{
		"null budget":                    `{"version":1,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":null,"targetEvidence":{},"oracleEvidence":[],"oracleTimeout":"1m0s","mutants":1,"killed":1}]}`,
		"null dirty":                     `{"version":1,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":1,"targetEvidence":{},"oracleEvidence":[],"oracleTimeout":"1m0s","dirty":null,"mutants":1,"killed":1}]}`,
		"duplicate budget":               `{"version":1,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":1,"budget":0,"targetEvidence":{},"oracleEvidence":[],"oracleTimeout":"1m0s","mutants":1,"killed":1}]}`,
		"duplicate version":              `{"version":1,"version":99,"findings":[]}`,
		"missing survivors":              `{"version":1,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":1,"targetEvidence":{},"oracleEvidence":[],"oracleTimeout":"1m0s","mutants":1,"killed":0}]}`,
		"empty attestation reason":       `{"version":1,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":1,"targetEvidence":{},"oracleEvidence":[],"oracleTimeout":"1m0s","mutants":1,"killed":0,"survivors":[{"position":"f.go:1:1","operator":"op"}],"attested":[{"position":"f.go:1:1","operator":"op","reason":""}]}]}`,
		"duplicate nested evidence":      `{"version":1,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":1,"targetEvidence":{"symbol":"p.F","symbol":"p.G"},"oracleEvidence":[],"oracleTimeout":"1m0s","mutants":0,"killed":0}]}`,
		"inflated budget":                `{"version":1,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":2,"targetEvidence":{},"oracleEvidence":[],"oracleTimeout":"1m0s","mutants":1,"killed":1}]}`,
		"colliding attestation identity": `{"version":1,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":1,"targetEvidence":{},"oracleEvidence":[],"oracleTimeout":"1m0s","mutants":1,"killed":0,"survivors":[{"position":"a|b.go:1:1","operator":"zero return"}],"attested":[{"position":"a","operator":"b.go:1:1|zero return","reason":"not the survivor"}]}]}`,
		"duplicate symbols":              `{"version":1,"findings":[{"symbol":"p.F","mutants":0,"killed":0},{"symbol":"p.F","mutants":0,"killed":0}]}`,
		"duplicate oracle symbols":       `{"version":1,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":0,"targetEvidence":{},"oracleEvidence":[{"symbol":"p.TestF"},{"symbol":"p.TestF"}],"oracleTimeout":"1m0s","dirty":true,"mutants":0,"killed":0}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseFindings([]byte(doc)); err == nil {
				t.Fatal("malformed known field accepted")
			}
		})
	}
	nonGit := `{"version":1,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":0,"targetEvidence":{"symbol":"p.F","maximalClosure":"c","toolchain":"go","buildConfig":"b","runtimeInputs":"m","runtimeDigest":"d"},"oracleEvidence":[{"symbol":"p.TestF","maximalClosure":"tc","toolchain":"go","buildConfig":"b","runtimeInputs":"m","runtimeDigest":"d"}],"oracleExplicit":true,"oracleTimeout":"1m0s","dirty":true,"candidateCount":0,"generated":0,"mutants":0,"killed":0,"discarded":0,"operators":[]}]}`
	nonGitFindings, err := ParseFindings([]byte(nonGit))
	if err != nil || len(nonGitFindings) != 1 {
		t.Fatalf("non-Git provenance rejected: %v %+v", err, nonGitFindings)
	}
	for _, field := range []string{`,"candidateCount":0`, `,"generated":0`, `,"discarded":0`} {
		if _, err := ParseFindings([]byte(strings.Replace(nonGit, field, "", 1))); err == nil {
			t.Fatalf("finding without required count %s accepted", field)
		}
	}
	for name, malformed := range map[string]string{
		"generated equation":  strings.Replace(nonGit, `"generated":0`, `"generated":1`, 1),
		"negative candidates": strings.Replace(nonGit, `"candidateCount":0`, `"candidateCount":-1`, 1),
		"budget relation": strings.Replace(
			strings.Replace(
				strings.Replace(
					strings.Replace(
						strings.Replace(nonGit, `"budget":0`, `"budget":2`, 1),
						`"candidateCount":0`, `"candidateCount":2`, 1),
					`"generated":0`, `"generated":1`, 1),
				`"mutants":0,"killed":0`, `"mutants":1,"killed":1`, 1),
			`"operators":[]`, `"operators":[{"operator":"op","generated":1,"discarded":0,"killed":1,"survived":0}]`, 1),
	} {
		if _, err := ParseFindings([]byte(malformed)); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	legacyTimeout := strings.Replace(nonGit, `"oracleTimeout":"1m0s"`, `"timeout":"1m0s"`, 1)
	if _, err := ParseFindings([]byte(legacyTimeout)); err == nil {
		t.Fatal("legacy ambiguous timeout field accepted")
	}
	withoutOracleMode := strings.Replace(nonGit, `,"oracleExplicit":true`, "", 1)
	if _, err := ParseFindings([]byte(withoutOracleMode)); err == nil {
		t.Fatal("finding without oracle selection mode accepted")
	}
	withoutOperators := strings.Replace(nonGit, `,"operators":[]`, "", 1)
	if _, err := ParseFindings([]byte(withoutOperators)); err == nil {
		t.Fatal("finding without operator summaries accepted")
	}
	badOperators := strings.Replace(nonGit, `"operators":[]`, `"operators":[{"operator":"zero return","generated":1,"discarded":0,"killed":0,"survived":0}]`, 1)
	if _, err := ParseFindings([]byte(badOperators)); err == nil {
		t.Fatal("operator summary inconsistent with totals accepted")
	}
	nullOperators := strings.Replace(nonGit, `"operators":[]`, `"operators":null`, 1)
	if _, err := ParseFindings([]byte(nullOperators)); err == nil {
		t.Fatal("null operator summaries accepted")
	}
	expectInvalidExport := func(name string, finding Finding) {
		t.Helper()
		if _, err := Export([]Finding{finding}); err == nil {
			t.Fatalf("%s operator summaries accepted", name)
		}
	}
	base := nonGitFindings[0]
	base.CandidateCount, base.Generated, base.Mutants, base.Killed = 2, 2, 2, 2
	base.Operators = []OperatorSummary{{Operator: "z", Generated: 1, Killed: 1}, {Operator: "a", Generated: 1, Killed: 1}}
	expectInvalidExport("unsorted", base)
	base.Operators = []OperatorSummary{{Operator: "a", Generated: 1, Killed: 1}, {Operator: "a", Generated: 1, Killed: 1}}
	expectInvalidExport("duplicate", base)
	base.CandidateCount, base.Generated, base.Mutants, base.Killed = 1, 1, 1, 0
	base.Survivors = []Survivor{{Position: "f.go:1:1", Operator: "b"}}
	base.Operators = []OperatorSummary{{Operator: "a", Generated: 1, Survived: 1}}
	expectInvalidExport("survivor mismatch", base)
	base.CandidateCount, base.Generated, base.Mutants, base.Killed, base.Discarded, base.Survivors = 0, 0, 0, 0, 0, nil
	base.Operators = []OperatorSummary{{Operator: "a"}}
	expectInvalidExport("zero generated", base)
	base.CandidateCount, base.Generated = int(^uint(0)>>1), int(^uint(0)>>1)
	base.Mutants, base.Killed, base.Discarded = int(^uint(0)>>1), int(^uint(0)>>1), 1
	base.Operators = []OperatorSummary{{Operator: "a", Generated: int(^uint(0) >> 1), Killed: int(^uint(0) >> 1)}}
	expectInvalidExport("overflow", base)
	base.CandidateCount, base.Generated, base.Mutants, base.Killed, base.Discarded = 1, 1, 1, 1, 0
	base.Operators = []OperatorSummary{{Operator: "a", Generated: 1, Discarded: -1, Killed: 1, Survived: 1}}
	expectInvalidExport("negative", base)
	invalidExport := nonGitFindings[0]
	invalidExport.Dirty = false
	if _, err := Export([]Finding{invalidExport}); err == nil {
		t.Fatal("export emitted commitless clean provenance")
	}
	digestAt := strings.LastIndex(nonGit, `"runtimeDigest":"d"`)
	if digestAt < 0 {
		t.Fatal("runtime digest fixture missing")
	}
	mismatchedRuntime := nonGit[:digestAt] + `"runtimeDigest":"other"` + nonGit[digestAt+len(`"runtimeDigest":"d"`):]
	if _, err := ParseFindings([]byte(mismatchedRuntime)); err == nil {
		t.Fatal("per-subject runtime evidence mismatch accepted")
	}
	partialRuntime := nonGit[:digestAt-1] + nonGit[digestAt+len(`"runtimeDigest":"d"`):]
	if _, err := ParseFindings([]byte(partialRuntime)); err == nil {
		t.Fatal("partial runtime evidence accepted")
	}
	impossibleRuntime := strings.ReplaceAll(nonGit, `"runtimeDigest":"d"`, `"runtimeUnverifiable":true,"runtimeDigest":"d"`)
	if _, err := ParseFindings([]byte(impossibleRuntime)); err == nil {
		t.Fatal("impossible runtime disposition accepted")
	}
	wrongTarget := strings.Replace(nonGit, `"targetEvidence":{"symbol":"p.F"`, `"targetEvidence":{"symbol":"p.G"`, 1)
	if _, err := ParseFindings([]byte(wrongTarget)); err == nil {
		t.Fatal("mismatched target evidence accepted")
	}
	emptyOracle := `{"version":1,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":0,"targetEvidence":{"symbol":"p.F","maximalClosure":"c","toolchain":"go","buildConfig":"b","runtimeInputs":"m","runtimeDigest":"d"},"oracleEvidence":[],"oracleExplicit":true,"oracleTimeout":"1m0s","dirty":true,"candidateCount":0,"generated":0,"mutants":0,"killed":0,"discarded":0,"operators":[]}]}`
	if _, err := ParseFindings([]byte(emptyOracle)); err == nil {
		t.Fatal("empty oracle evidence accepted")
	}
	withoutDirty := strings.Replace(nonGit, `,"dirty":true`, "", 1)
	if _, err := ParseFindings([]byte(withoutDirty)); err == nil {
		t.Fatal("missing commit without dirty provenance accepted")
	}
	committedWithoutDirty := strings.Replace(withoutDirty, `"oracleTimeout":"1m0s"`, `"oracleTimeout":"1m0s","commit":"abc"`, 1)
	if _, err := ParseFindings([]byte(committedWithoutDirty)); err == nil {
		t.Fatal("committed finding without explicit dirty provenance accepted")
	}
	legacy := `{"version":1,"findings":[{"symbol":"p.F","mutants":1,"killed":0,"survivors":[{"position":"f.go:1:1","operator":"op"}],"attested":[{"position":"f.go:1:1","operator":"op","reason":"legacy"}]}]}`
	if _, err := ParseFindings([]byte(legacy)); err == nil {
		t.Fatal("legacy finding accepted")
	}
	emptyPins := `{"version":1,"findings":[{"symbol":"p.F","bodyHash":"","operatorSet":"","budget":1,"targetEvidence":{"symbol":"","maximalClosure":"","toolchain":"","buildConfig":"","runtimeInputs":"","runtimeDigest":""},"oracleEvidence":[],"oracleTimeout":"","dirty":true,"mutants":1,"killed":0,"survivors":[{"position":"f.go:1:1","operator":"op"}],"attested":[{"position":"f.go:1:1","operator":"op","reason":"unsupported"}]}]}`
	if _, err := ParseFindings([]byte(emptyPins)); err == nil {
		t.Fatal("empty required pins accepted")
	}
}

func TestSummarizeOperators(t *testing.T) {
	mutants := []engine.Candidate{{Operator: "zero return"}, {Operator: "swap"}, {Operator: "zero return"}, {Operator: "swap"}}
	outcomes := []engine.MutantOutcome{engine.MutantKilled, engine.MutantSurvived, engine.MutantDiscarded, engine.MutantKilled}
	got := summarizeOperators(mutants, outcomes)
	if len(got) != 2 || got[0] != (OperatorSummary{Operator: "swap", Generated: 2, Killed: 1, Survived: 1}) ||
		got[1] != (OperatorSummary{Operator: "zero return", Generated: 2, Discarded: 1, Killed: 1}) {
		t.Fatalf("operator summaries = %+v", got)
	}
}
