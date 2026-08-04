package gomutant

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/gomutant/internal/engine"
)

// TestRunEndToEnd pins the orchestration against the fixture tree: a
// pinned-down body kills everything, an untested branch survives with its
// labels echoed (REQ-target-labels), a prior finding with matching pins is
// served from cache (REQ-result-stale), an attested survivor carries across
// a cached serve and a pin-matching re-measure (REQ-attest-survivor), a
// budget request beyond a capped finding measures only the unmeasured suffix
// (REQ-mut-budget), and the document round-trips (REQ-result-export).
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
	first, err := tr.Run(ctx, []Target{targets[0], targets[2]}, Options{Decision: func(decision RunDecision) {
		firstDecisions = append(firstDecisions, decision)
	}})
	if err != nil {
		t.Fatal(err)
	}
	add, iface := first[0], first[1]
	wantAddSurvivors := []Survivor{
		{Position: "lib.go:24:2", Operator: "statement: delete", Execution: "executed-and-passed"},
		{Position: "lib.go:24:5", Operator: "condition: force false", Execution: "executed-and-passed"},
		{Position: "lib.go:24:12", Operator: "block: empty", Execution: "executed-and-passed"},
		{Position: "lib.go:25:3", Operator: "statement: delete", Execution: "executed-and-passed"},
	}
	if add.Cached || add.Mutants != 11 || add.Killed != 7 || add.Discarded != 1 || !slices.Equal(add.Survivors, wantAddSurvivors) {
		t.Fatalf("Add = %+v, want exact go/12 outcomes %+v", add, wantAddSurvivors)
	}
	if len(add.Operators) == 0 {
		t.Fatal("Add finding omitted operator summaries")
	}
	if add.BodyHash == "" || add.TargetEvidence.Toolchain == "" || add.OperatorSet == "" || len(add.OracleEvidence) != 1 {
		t.Fatalf("Add pins incomplete: %+v", add)
	}
	if !strings.HasPrefix(iface.Skipped, "not a function - ") {
		t.Fatalf("interface target = %+v, want skipped as not a function", iface)
	}
	if len(firstDecisions) != 2 || firstDecisions[0].Reason != "no-prior" || firstDecisions[1].Action != "skipped" || !strings.HasPrefix(firstDecisions[1].Reason, "not a function - ") {
		t.Fatalf("first decisions = %+v", firstDecisions)
	}

	// The export/parse round trip omits skipped targets.
	doc, err := Export([]Finding{add, iface})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(doc), "example.com/fixture/lib.I") {
		t.Fatal("a skipped result was exported")
	}
	measured, err := ParseFindings(doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(measured) != 1 {
		t.Fatalf("document findings = %d, want 1", len(measured))
	}

	// Use a discard-free measurement for cache behavior: a launched compiler
	// rejection would carry candidate evidence, which serves through the
	// re-execution splice rather than the plain cached path pinned here.
	cacheable, err := tr.Run(ctx, targets[1:2], Options{Budget: 1})
	if err != nil {
		t.Fatal(err)
	}
	if cacheable[0].Discarded != 0 || len(cacheable[0].Survivors) == 0 || !slices.Equal(cacheable[0].Labels, []string{"REQ-weak"}) {
		t.Fatalf("cache fixture = %+v, want a discard-free survivor", cacheable[0])
	}
	if len(cacheable[0].Open()) != len(cacheable[0].Survivors) {
		t.Fatal("open != survivors before any attestation")
	}
	cacheSurvivor := cacheable[0].Survivors[0]
	if err := cacheable[0].Attest(cacheSurvivor.Position, cacheSurvivor.Operator, "equivalent by inspection"); err != nil {
		t.Fatal(err)
	}
	if err := cacheable[0].Attest("nowhere:1:1", "no-op", "x"); err == nil {
		t.Fatal("attested a mutant that is not a survivor")
	}
	if len(cacheable[0].Open()) != len(cacheable[0].Survivors)-1 {
		t.Fatal("attestation did not close the finding")
	}
	cacheDoc, err := Export(cacheable)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := ParseFindings(cacheDoc)
	if err != nil {
		t.Fatal(err)
	}

	// Second run under the same pins: served from cache, attestation intact.
	second, err := tr.Run(ctx, targets[1:2], Options{Budget: 1, Prior: prior})
	if err != nil {
		t.Fatal(err)
	}
	if !second[0].Cached {
		t.Fatal("unchanged pins re-measured")
	}
	if len(second[0].Attested) != 1 || second[0].Attested[0].Reason != "equivalent by inspection" {
		t.Fatalf("attestation lost across cache: %+v", second[0].Attested)
	}

	// A moved pin re-measures instead of serving the cache, and sheds the
	// attestation: every source-evidence version's equivalences are re-judged
	// (REQ-result-stale, REQ-attest-survivor).
	tampered := append([]Finding(nil), prior...)
	tampered[0].TargetEvidence.MaximalClosure = "not-the-current-closure"
	var movedDecisions []RunDecision
	moved, err := tr.Run(ctx, targets[1:2], Options{Budget: 1, Prior: tampered, Decision: func(decision RunDecision) {
		movedDecisions = append(movedDecisions, decision)
	}})
	if err != nil {
		t.Fatal(err)
	}
	if moved[0].Cached {
		t.Fatal("a moved pin served from cache")
	}
	if len(moved[0].Attested) != 0 {
		t.Fatalf("attestation survived a pin move: %+v", moved[0].Attested)
	}
	if len(movedDecisions) != 1 || !strings.HasPrefix(movedDecisions[0].Reason, "stale: ") ||
		!strings.Contains(movedDecisions[0].Reason, "target") {
		t.Fatalf("moved decisions = %+v; want the moved pin attributed to its subject", movedDecisions)
	}

	// An unverifiable prior is not stale: the decision reason carries the
	// inspection's own class (REQ-result-stale).
	sealed := append([]Finding(nil), prior...)
	sealed[0].TargetEvidence.RuntimeUnverifiable = true
	sealed[0].TargetEvidence.RuntimeReason = "manual input"
	var sealedDecisions []RunDecision
	if _, err := tr.Run(ctx, targets[1:2], Options{Budget: 1, Prior: sealed, Decision: func(decision RunDecision) {
		sealedDecisions = append(sealedDecisions, decision)
	}}); err != nil {
		t.Fatal(err)
	}
	if len(sealedDecisions) != 1 || !strings.HasPrefix(sealedDecisions[0].Reason, "unverifiable: ") {
		t.Fatalf("sealed decisions = %+v; want the inspection's class, not an assumed stale", sealedDecisions)
	}

	// A capped prior finding never answers a larger request without
	// measurement: budget 1 is re-measured fresh under budget 1, then a
	// budget-2 request serves the recorded prefix and measures only the one
	// unmeasured candidate (REQ-mut-budget, REQ-result-stale's
	// budget-extension carve-out).
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
		t.Fatal("an extended finding reported itself cached")
	}
	wantWider := RunDecision{Symbol: targets[0].Symbol, Action: "measure", Reason: "served: prefix of 1 candidate stands; measuring 1 more", Candidates: 1}
	if len(widerDecisions) != 1 || widerDecisions[0] != wantWider {
		t.Fatalf("wider decisions = %+v, want %+v", widerDecisions, wantWider)
	}
	if wider[0].Budget != 2 || wider[0].Generated != 2 || wider[0].CandidateCount != capped[0].CandidateCount ||
		wider[0].Generated != wider[0].Mutants+wider[0].Discarded || wider[0].Mutants != wider[0].Killed+len(wider[0].Survivors) {
		t.Fatalf("extended counts = %+v, want the merged truth conserved", wider[0])
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

func TestRunAccountsForComparisonFamilies(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	targets := []Target{
		{Symbol: "example.com/fixture/lib.Boundary", Oracle: []string{"example.com/fixture/lib.TestBoundary"}},
		{Symbol: "example.com/fixture/lib.EqualityLogical", Oracle: []string{"example.com/fixture/lib.TestEqualityLogical"}},
	}
	findings, err := tr.Run(context.Background(), targets, Options{Jobs: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 {
		t.Fatalf("comparison finding = %+v", findings)
	}
	for _, finding := range findings {
		if finding.Generated != finding.CandidateCount || finding.Generated != finding.Mutants+finding.Discarded {
			t.Fatalf("comparison finding = %+v", finding)
		}
	}
	operators := map[string]OperatorSummary{}
	for _, finding := range findings {
		for _, summary := range finding.Operators {
			operators[summary.Operator] = summary
		}
	}
	for _, operator := range []string{"relational boundary: < -> <=", "relational negation: < -> >="} {
		summary, ok := operators[operator]
		if !ok || summary.Generated != 1 || summary.Killed != 1 || summary.Discarded != 0 || summary.Survived != 0 {
			t.Errorf("%s summary = %+v", operator, summary)
		}
	}
	for _, operator := range []string{"equality: == -> !=", "logical: && -> ||"} {
		summary, ok := operators[operator]
		if !ok || summary.Generated != 1 || summary.Killed != 1 || summary.Discarded != 0 || summary.Survived != 0 {
			t.Errorf("%s summary = %+v", operator, summary)
		}
	}
	if summary := operators["boolean operand: -> true"]; summary.Generated != 2 || summary.Killed != 2 || summary.Discarded != 0 || summary.Survived != 0 {
		t.Errorf("boolean operand summary = %+v", summary)
	}
	oldBasis := findings[0]
	oldBasis.OperatorSet = "go/5"
	if fresh, err := tr.Fresh(oldBasis, targets[0], 0); err != nil || fresh {
		t.Fatalf("go/5 finding under current basis = fresh %v, err %v", fresh, err)
	}
}

func TestRunAccountsForControlFamilies(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	oracle := []string{"example.com/fixture/lib.TestControlOutcomes"}
	targets := []Target{
		{Symbol: "example.com/fixture/lib.IfCondition", Oracle: oracle},
		{Symbol: "example.com/fixture/lib.ForCondition", Oracle: oracle},
		{Symbol: "example.com/fixture/lib.ConditionlessOutcome", Oracle: oracle},
		{Symbol: "example.com/fixture/lib.RangeOnce", Oracle: oracle},
		{Symbol: "example.com/fixture/lib.BreakValue", Oracle: oracle},
		{Symbol: "example.com/fixture/lib.ContinueValue", Oracle: oracle},
		{Symbol: "example.com/fixture/lib.LogicalDefined", Oracle: oracle},
		{Symbol: "example.com/fixture/lib.LogicalGeneric", Oracle: oracle},
	}
	findings, err := tr.Run(context.Background(), targets, Options{Jobs: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != len(targets) {
		t.Fatalf("control findings = %d, want %d", len(findings), len(targets))
	}
	operators := map[string]OperatorSummary{}
	for _, finding := range findings {
		if finding.Generated != finding.CandidateCount || finding.Generated != finding.Mutants+finding.Discarded {
			t.Fatalf("control finding = %+v", finding)
		}
		for _, summary := range finding.Operators {
			total := operators[summary.Operator]
			total.Operator = summary.Operator
			total.Generated += summary.Generated
			total.Discarded += summary.Discarded
			total.Killed += summary.Killed
			total.Survived += summary.Survived
			operators[summary.Operator] = total
		}
	}
	for operator, want := range map[string]OperatorSummary{
		"condition: negate":               {Generated: 4, Killed: 4},
		"condition: force true":           {Generated: 3, Killed: 2, Survived: 1},
		"condition: force false":          {Generated: 5, Killed: 5},
		"range body: prepend break":       {Generated: 3, Killed: 2, Survived: 1},
		"loop control: break -> continue": {Generated: 1, Killed: 1},
		"loop control: continue -> break": {Generated: 1, Killed: 1},
		"boolean operand: -> true":        {Generated: 4, Survived: 4},
		"boolean operand: -> false":       {Generated: 8, Survived: 8},
	} {
		summary := operators[operator]
		if summary.Generated != want.Generated || summary.Killed != want.Killed || summary.Discarded != want.Discarded || summary.Survived != want.Survived {
			t.Errorf("%s summary = %+v, want %+v", operator, summary, want)
		}
	}
	oldBasis := findings[0]
	oldBasis.OperatorSet = "go/6"
	if fresh, err := tr.Fresh(oldBasis, targets[0], 0); err != nil || fresh {
		t.Fatalf("go/6 finding under go/7 = fresh %v, err %v", fresh, err)
	}
}

func TestRunAccountsForArithmeticFamilies(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	oracle := []string{"example.com/fixture/lib.TestVacuous"}
	targets := []Target{
		{Symbol: "example.com/fixture/lib.ArithmeticDefined", Oracle: oracle},
		{Symbol: "example.com/fixture/lib.ArithmeticFloat", Oracle: oracle},
		{Symbol: "example.com/fixture/lib.ArithmeticComplex", Oracle: oracle},
		{Symbol: "example.com/fixture/lib.ArithmeticGeneric", Oracle: oracle},
		{Symbol: "example.com/fixture/lib.RemainderGeneric", Oracle: oracle},
		{Symbol: "example.com/fixture/lib.ArithmeticMulZero", Oracle: oracle},
		{Symbol: "example.com/fixture/lib.ArithmeticAlias", Oracle: oracle},
		{Symbol: "example.com/fixture/lib.ArithmeticIntersected", Oracle: oracle},
		{Symbol: "example.com/fixture/lib.ArithmeticUntyped", Oracle: oracle},
		{Symbol: "example.com/fixture/lib.ArithmeticIota", Oracle: oracle},
		{Symbol: "example.com/fixture/lib.ArithmeticImaginary", Oracle: oracle},
	}
	findings, err := tr.Run(context.Background(), targets, Options{Jobs: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != len(targets) {
		t.Fatalf("arithmetic findings = %d, want %d", len(findings), len(targets))
	}
	operators := map[string]OperatorSummary{}
	for _, finding := range findings {
		if finding.Generated != finding.CandidateCount || finding.Generated != finding.Mutants+finding.Discarded {
			t.Fatalf("arithmetic finding = %+v", finding)
		}
		for _, summary := range finding.Operators {
			total := operators[summary.Operator]
			total.Operator = summary.Operator
			total.Generated += summary.Generated
			total.Discarded += summary.Discarded
			total.Killed += summary.Killed
			total.Survived += summary.Survived
			operators[summary.Operator] = total
		}
	}
	for operator, want := range map[string]OperatorSummary{
		"arithmetic: + -> -": {Generated: 9, Survived: 9},
		"arithmetic: - -> +": {Generated: 5, Survived: 5},
		"arithmetic: * -> /": {Generated: 6, Discarded: 1, Survived: 5},
		"arithmetic: / -> *": {Generated: 5, Survived: 5},
		"arithmetic: % -> *": {Generated: 2, Survived: 2},
	} {
		summary := operators[operator]
		if summary.Generated != want.Generated || summary.Killed != want.Killed || summary.Discarded != want.Discarded || summary.Survived != want.Survived {
			t.Errorf("%s summary = %+v, want %+v", operator, summary, want)
		}
	}
	oldBasis := findings[0]
	oldBasis.OperatorSet = "go/6"
	if fresh, err := tr.Fresh(oldBasis, targets[0], 0); err != nil || fresh {
		t.Fatalf("go/6 finding under go/7 = fresh %v, err %v", fresh, err)
	}
}

func TestRunAccountsForBitwiseFamilies(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	oracle := []string{"example.com/fixture/lib.TestVacuous"}
	targets := []Target{
		{Symbol: "example.com/fixture/lib.BitwiseDefined", Oracle: oracle},
		{Symbol: "example.com/fixture/lib.BitwiseGeneric", Oracle: oracle},
		{Symbol: "example.com/fixture/lib.BitwiseConstants", Oracle: oracle},
		{Symbol: "example.com/fixture/lib.BitwiseAlias", Oracle: oracle},
		{Symbol: "example.com/fixture/lib.ShiftDefined", Oracle: oracle},
		{Symbol: "example.com/fixture/lib.ShiftGeneric", Oracle: oracle},
		{Symbol: "example.com/fixture/lib.ShiftConstants", Oracle: oracle},
		{Symbol: "example.com/fixture/lib.BitwiseDuplicate", Oracle: oracle},
		{Symbol: "example.com/fixture/lib.ShiftOverflow", Oracle: oracle},
	}
	findings, err := tr.Run(context.Background(), targets, Options{Jobs: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != len(targets) {
		t.Fatalf("bitwise findings = %d, want %d", len(findings), len(targets))
	}
	operators := map[string]OperatorSummary{}
	for _, finding := range findings {
		if finding.Generated != finding.CandidateCount || finding.Generated != finding.Mutants+finding.Discarded {
			t.Fatalf("bitwise finding = %+v", finding)
		}
		for _, summary := range finding.Operators {
			total := operators[summary.Operator]
			total.Operator = summary.Operator
			total.Generated += summary.Generated
			total.Discarded += summary.Discarded
			total.Killed += summary.Killed
			total.Survived += summary.Survived
			operators[summary.Operator] = total
		}
	}
	for operator, want := range map[string]OperatorSummary{
		"bitwise: & -> |":  {Generated: 4, Survived: 4},
		"bitwise: | -> &":  {Generated: 3, Survived: 3},
		"bitwise: ^ -> &":  {Generated: 4, Discarded: 1, Survived: 3},
		"bitwise: &^ -> &": {Generated: 3, Survived: 3},
		"shift: << -> >>":  {Generated: 3, Survived: 3},
		"shift: >> -> <<":  {Generated: 4, Discarded: 1, Survived: 3},
	} {
		summary := operators[operator]
		if summary.Generated != want.Generated || summary.Killed != want.Killed || summary.Discarded != want.Discarded || summary.Survived != want.Survived {
			t.Errorf("%s summary = %+v, want %+v", operator, summary, want)
		}
	}
	oldBasis := findings[0]
	oldBasis.OperatorSet = "go/7"
	if fresh, err := tr.Fresh(oldBasis, targets[0], 0); err != nil || fresh {
		t.Fatalf("go/7 finding under go/8 = fresh %v, err %v", fresh, err)
	}
}

func TestRunAccountsForUnaryAssignmentFamilies(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	oracle := []string{"example.com/fixture/lib.TestVacuous"}
	symbols := []string{
		"UnaryPlus", "UnaryMinus", "UnaryNot", "UnaryXor",
		"CompoundAdd", "CompoundSub", "CompoundMul", "CompoundDiv", "CompoundRem",
		"CompoundAnd", "CompoundOr", "CompoundXor", "CompoundClear", "CompoundShiftLeft", "CompoundShiftRight",
		"Increment", "Decrement", "UnaryOverflow", "CompoundDivideByZero",
	}
	targets := make([]Target, 0, len(symbols))
	for _, symbol := range symbols {
		targets = append(targets, Target{Symbol: "example.com/fixture/lib." + symbol, Oracle: oracle})
	}
	findings, err := tr.Run(context.Background(), targets, Options{Jobs: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != len(targets) {
		t.Fatalf("unary/assignment findings = %d, want %d", len(findings), len(targets))
	}
	operators := map[string]OperatorSummary{}
	for _, finding := range findings {
		if finding.Generated != finding.CandidateCount || finding.Generated != finding.Mutants+finding.Discarded {
			t.Fatalf("unary/assignment finding = %+v", finding)
		}
		for _, summary := range finding.Operators {
			total := operators[summary.Operator]
			total.Operator = summary.Operator
			total.Generated += summary.Generated
			total.Discarded += summary.Discarded
			total.Killed += summary.Killed
			total.Survived += summary.Survived
			operators[summary.Operator] = total
		}
	}
	want := map[string]OperatorSummary{
		"unary: + -> -": {Generated: 1, Survived: 1}, "unary: - -> +": {Generated: 2, Discarded: 1, Survived: 1},
		"unary: ! -> identity": {Generated: 1, Survived: 1}, "unary: ^ -> identity": {Generated: 1, Survived: 1},
		"compound arithmetic: += -> -=": {Generated: 1, Survived: 1}, "compound arithmetic: -= -> +=": {Generated: 1, Survived: 1},
		"compound arithmetic: *= -> /=": {Generated: 2, Discarded: 1, Survived: 1}, "compound arithmetic: /= -> *=": {Generated: 1, Survived: 1},
		"compound arithmetic: %= -> *=": {Generated: 1, Survived: 1},
		"compound bitwise: &= -> |=":    {Generated: 1, Survived: 1}, "compound bitwise: |= -> &=": {Generated: 1, Survived: 1},
		"compound bitwise: ^= -> &=": {Generated: 1, Survived: 1}, "compound bitwise: &^= -> &=": {Generated: 1, Survived: 1},
		"compound shift: <<= -> >>=": {Generated: 1, Survived: 1}, "compound shift: >>= -> <<=": {Generated: 1, Survived: 1},
		"increment/decrement: ++ -> --": {Generated: 1, Survived: 1}, "increment/decrement: -- -> ++": {Generated: 1, Survived: 1},
	}
	for _, operator := range []string{"+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=", "&^=", "<<=", ">>="} {
		generated := 1
		if operator == "*=" {
			generated = 2
		}
		want["compound store: "+operator+" -> ="] = OperatorSummary{Generated: generated, Survived: generated}
	}
	for operator, expected := range want {
		summary := operators[operator]
		if summary.Generated != expected.Generated || summary.Killed != expected.Killed || summary.Discarded != expected.Discarded || summary.Survived != expected.Survived {
			t.Errorf("%s summary = %+v, want %+v", operator, summary, expected)
		}
	}
	oldBasis := findings[0]
	oldBasis.OperatorSet = "go/8"
	if fresh, err := tr.Fresh(oldBasis, targets[0], 0); err != nil || fresh {
		t.Fatalf("go/8 finding under go/9 = fresh %v, err %v", fresh, err)
	}
}

func TestRunAccountsForScalarLiteralFamilies(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	oracle := []string{"example.com/fixture/lib.TestVacuous"}
	symbols := []string{
		"LiteralInteger", "LiteralRune", "LiteralFloat", "LiteralImaginary",
		"LiteralTrue", "LiteralFalse", "LiteralNonempty", "LiteralEmpty",
		"IntegerLiteralOverflow", "IntegerLiteralDuplicate", "RuneLiteralDuplicate",
		"FloatLiteralDuplicate", "ImaginaryLiteralCases", "BooleanLiteralCases", "StringLiteralDuplicate",
	}
	targets := make([]Target, 0, len(symbols))
	for _, symbol := range symbols {
		targets = append(targets, Target{Symbol: "example.com/fixture/lib." + symbol, Oracle: oracle})
	}
	findings, err := tr.Run(context.Background(), targets, Options{Jobs: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != len(targets) {
		t.Fatalf("scalar literal findings = %d, want %d", len(findings), len(targets))
	}
	operators := map[string]OperatorSummary{}
	for _, finding := range findings {
		if finding.Generated != finding.CandidateCount || finding.Generated != finding.Mutants+finding.Discarded {
			t.Fatalf("scalar literal finding = %+v", finding)
		}
		for _, summary := range finding.Operators {
			total := operators[summary.Operator]
			total.Operator = summary.Operator
			total.Generated += summary.Generated
			total.Discarded += summary.Discarded
			total.Killed += summary.Killed
			total.Survived += summary.Survived
			operators[summary.Operator] = total
		}
	}
	for operator, want := range map[string]OperatorSummary{
		"integer literal: magnitude +1":     {Generated: 4, Discarded: 2, Survived: 2},
		"rune literal: value +1":            {Generated: 3, Discarded: 1, Survived: 2},
		"float literal: value +1":           {Generated: 3, Discarded: 1, Survived: 2},
		"imaginary literal: value +1":       {Generated: 3, Survived: 3},
		"boolean literal: true -> false":    {Generated: 2, Survived: 2},
		"boolean literal: false -> true":    {Generated: 2, Survived: 2},
		"string literal: nonempty -> empty": {Generated: 2, Discarded: 1, Survived: 1},
		"string literal: empty -> nonempty": {Generated: 2, Discarded: 1, Survived: 1},
	} {
		summary := operators[operator]
		if summary.Generated != want.Generated || summary.Killed != want.Killed || summary.Discarded != want.Discarded || summary.Survived != want.Survived {
			t.Errorf("%s summary = %+v, want %+v", operator, summary, want)
		}
	}
	oldBasis := findings[0]
	oldBasis.OperatorSet = "go/9"
	if fresh, err := tr.Fresh(oldBasis, targets[0], 0); err != nil || fresh {
		t.Fatalf("go/9 finding under go/10 = fresh %v, err %v", fresh, err)
	}
}

func TestRunAccountsForReturnSubstitutions(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	oracle := []string{"example.com/fixture/lib.TestVacuous"}
	symbols := []string{
		"ReturnBoolean", "ReturnNumber", "ReturnString", "ReturnPointer",
		"ReturnDefined", "ReturnAliases", "ReturnNilDomains", "ReturnDefinedNilDomains", "ReturnDeclaredInterface",
		"ReturnFalseLiteral", "ReturnTrueLiteral", "ReturnZeroLiteral", "ReturnEmptyLiteral", "ReturnNilLiteral",
	}
	targets := make([]Target, 0, len(symbols))
	for _, symbol := range symbols {
		targets = append(targets, Target{Symbol: "example.com/fixture/lib." + symbol, Oracle: oracle})
	}
	findings, err := tr.Run(context.Background(), targets, Options{Jobs: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != len(targets) {
		t.Fatalf("return findings = %d, want %d", len(findings), len(targets))
	}
	operators := map[string]OperatorSummary{}
	for _, finding := range findings {
		if finding.Generated != finding.CandidateCount || finding.Generated != finding.Mutants+finding.Discarded {
			t.Fatalf("return finding = %+v", finding)
		}
		for _, summary := range finding.Operators {
			total := operators[summary.Operator]
			total.Operator = summary.Operator
			total.Generated += summary.Generated
			total.Discarded += summary.Discarded
			total.Killed += summary.Killed
			total.Survived += summary.Survived
			operators[summary.Operator] = total
		}
	}
	for operator, want := range map[string]OperatorSummary{
		"return: false": {Generated: 4, Discarded: 2, Survived: 2},
		"return: true":  {Generated: 4, Discarded: 2, Survived: 2},
		"return: zero":  {Generated: 9, Discarded: 2, Survived: 7},
		"return: nil":   {Generated: 18, Discarded: 1, Survived: 17},
	} {
		summary := operators[operator]
		if summary.Generated != want.Generated || summary.Killed != want.Killed || summary.Discarded != want.Discarded || summary.Survived != want.Survived {
			t.Errorf("%s summary = %+v, want %+v", operator, summary, want)
		}
	}
	oldBasis := findings[0]
	oldBasis.OperatorSet = "go/10"
	if fresh, err := tr.Fresh(oldBasis, targets[0], 0); err != nil || fresh {
		t.Fatalf("go/10 finding under go/11 = fresh %v, err %v", fresh, err)
	}
}

func TestRunAccountsForStatementFamilies(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	oracle := []string{"example.com/fixture/lib.TestVacuous"}
	symbols := []string{"StatementBlocks", "StatementKinds", "StatementDropStores", "StatementExcluded"}
	targets := make([]Target, 0, len(symbols))
	for _, symbol := range symbols {
		targets = append(targets, Target{Symbol: "example.com/fixture/lib." + symbol, Oracle: oracle})
	}
	findings, err := tr.Run(context.Background(), targets, Options{Jobs: 2})
	if err != nil {
		t.Fatal(err)
	}
	operators := map[string]OperatorSummary{}
	for _, finding := range findings {
		if finding.Generated != finding.CandidateCount || finding.Generated != finding.Mutants+finding.Discarded {
			t.Fatalf("statement finding = %+v", finding)
		}
		if finding.TargetEvidence.RuntimeUnverifiable && !strings.Contains(finding.TargetEvidence.RuntimeReason, "failed to build") {
			t.Fatalf("pre-execution statement-family discard added incomplete process evidence without a launched compiler rejection: %+v", finding.TargetEvidence)
		}
		for _, summary := range finding.Operators {
			total := operators[summary.Operator]
			total.Operator = summary.Operator
			total.Generated += summary.Generated
			total.Discarded += summary.Discarded
			total.Killed += summary.Killed
			total.Survived += summary.Survived
			operators[summary.Operator] = total
		}
	}
	for operator, want := range map[string]OperatorSummary{
		"block: empty":           {Generated: 8, Discarded: 4, Survived: 4},
		"statement: delete":      {Generated: 24, Discarded: 4, Survived: 20},
		"assignment: drop store": {Generated: 7, Discarded: 1, Survived: 6},
	} {
		summary := operators[operator]
		if summary.Generated != want.Generated || summary.Killed != want.Killed || summary.Discarded != want.Discarded || summary.Survived != want.Survived {
			t.Errorf("%s summary = %+v, want %+v", operator, summary, want)
		}
	}
	oldBasis := findings[0]
	oldBasis.OperatorSet = "go/11"
	if fresh, err := tr.Fresh(oldBasis, targets[0], 0); err != nil || fresh {
		t.Fatalf("go/11 finding under go/12 = fresh %v, err %v", fresh, err)
	}
}

func TestRunDecisionsAndCancellation(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	reportPreparation(nil, PreparationEvent{Stage: PreparationLoading})
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
	if err != nil || len(cachedStatus.decisions) != 1 || cachedStatus.decisions[0].Action != "cached" ||
		!strings.Contains(cachedStatus.decisions[0].Reason, "served: body, oracle closure, and runtime inputs unchanged") {
		t.Fatalf("cached status = %+v, %v; want the served reason naming the held pins", cachedStatus, err)
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
	if err == nil || !strings.Contains(err.Error(), "analysis view changed") || len(findings) != 0 {
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
	if err == nil || !strings.Contains(err.Error(), "analysis view changed") || len(findings) != 0 {
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
	}}, Options{Budget: 2})
	if err == nil || !strings.Contains(err.Error(), "analysis view changed") || len(findings) != 0 {
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
	if err == nil || !strings.Contains(err.Error(), "analysis view changed") || len(findings) != 0 {
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
	var lifecycle []string
	if _, err := tr.Run(context.Background(), targets, Options{
		Budget: 1,
		Progress: func(event PreparationEvent) {
			preparation = append(preparation, event)
			if event.Stage == PreparationBaseline {
				lifecycle = append(lifecycle, "baseline:"+event.Symbol)
			}
		},
		producer: func(symbol string) { lifecycle = append(lifecycle, "capture:"+symbol) },
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
	wantLifecycle := []string{"capture:" + targets[0].Symbol, "capture:" + targets[1].Symbol, "baseline:" + targets[0].Symbol}
	if !slices.Equal(lifecycle, wantLifecycle) {
		t.Fatalf("shared-baseline lifecycle = %v, want %v", lifecycle, wantLifecycle)
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
	findings, err := tree.Run(context.Background(), targets, Options{Budget: 2, Jobs: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 || findings[0].Mutants != 1 || findings[1].Mutants != 1 {
		t.Fatalf("findings = %+v", findings)
	}
	// An external-test-only production subject is unrootable in its test
	// binary's program, so its observability analysis records a subject-local
	// unavailable disposition. The proof confers nothing — Observable=false
	// blocks any runtime-input lift at check time — while the runtime evidence
	// itself stays verifiable: closure-level unverifiability is the maximal
	// scan's independent verdict, so forcing the runtime pin here would only
	// re-measure a finding whose every checked input still proves.
	if findings[1].TargetEvidence.RuntimeUnverifiable {
		t.Fatalf("unavailable proof forced the runtime pin unverifiable: %+v", findings[1].TargetEvidence)
	}
	if findings[1].TargetEvidence.ObservationStrategy != gofresh.ObservationRTA || findings[1].TargetEvidence.ObservationObservable ||
		!strings.Contains(findings[1].TargetEvidence.ObservationReason, "observation analysis unavailable") {
		t.Fatalf("external-test-only observation proof = %+v", findings[1].TargetEvidence)
	}
	if _, err := Export(findings); err != nil {
		t.Fatalf("exporting unavailable observation proof: %v", err)
	}
}

// TestRunRemeasuresGeneratedFixtureEvidence pins the finding-wide arm of
// REQ-exec-observation against the candidate-local carve-out: the
// generated-fixture oracle's completed observations are content-unverifiable
// (their manifests cover generated per-run paths that cannot be re-proven),
// and a COMPLETED observation stays in the finding-wide union — candidate
// evidence flags only a process that could not prove its evidence sound — so
// the subject evidence is explicitly unverifiable, carries no candidate
// flags, and the record remeasures rather than serves.
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
		t.Fatalf("generated-fixture finding = %+v, want finding-wide unverifiable completed evidence", first)
	}
	if len(first[0].CandidateEvidence) != 0 {
		t.Fatalf("generated-fixture candidate evidence = %+v, want none: the processes proved their logs complete", first[0].CandidateEvidence)
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
	if len(second) != 1 || len(decisions) != 1 || decisions[0].Action != "measure" || !strings.HasPrefix(decisions[0].Reason, "unverifiable: ") || second[0].Cached {
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
	stableState, err := runtimeinput.FromTestLogEnv([]byte("open "+stable+"\n"), root, root, env, runtimeinput.WithCompletedProcess("stable"), runtimeinput.WithBracket(testBracket(t, root)))
	if err != nil {
		t.Fatal(err)
	}
	movingState, err := runtimeinput.FromTestLogEnv([]byte("open "+moving+"\n"), root, root, env, runtimeinput.WithCompletedProcess("moving"), runtimeinput.WithBracket(testBracket(t, root)))
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

// TestRunCompileDiscardIsCandidateLocalEvidence pins the candidate-evidence
// carve-out (REQ-result-record, REQ-result-stale): a launched compiler
// rejection is an incomplete process that measured exactly one candidate, so
// its unverifiability attaches to that candidate — never to the finding's
// subject evidence — and the record still refuses coverage without the
// flagged re-execution.
func TestRunCompileDiscardCarriesNoCandidateEvidence(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	target := Target{Symbol: "example.com/fixture/plain.Ok", Oracle: []string{"example.com/fixture/plain.TestPlain"}}
	findings, err := tr.Run(context.Background(), []Target{target}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Discarded == 0 {
		t.Fatalf("compile-discard finding = %+v", findings)
	}
	evidence := findings[0].TargetEvidence
	if evidence.RuntimeUnverifiable || evidence.RuntimeReason != "" {
		t.Fatalf("compile-discard runtime evidence = %+v, want the completed-process union verifiable", evidence)
	}
	// No test process started for the rejected candidates: no runtime
	// exposure exists to prove complete, so the discard is covered by the
	// toolchain and build-configuration pins and carries no candidate
	// evidence (candidate evidence term, REQ-result-stale).
	if flagged := findings[0].CandidateEvidence; len(flagged) != 0 {
		t.Fatalf("compile-discard finding carries candidate evidence: %+v", flagged)
	}
	// The record is coverable as it stands - no splice, no probe, no
	// doomed re-compile per serve.
	if ok, err := tr.Fresh(findings[0], target, 0); err != nil || !ok {
		t.Fatalf("compile-discard finding coverable without execution = %v, %v; want covered", ok, err)
	}
	var decisions []RunDecision
	served, err := tr.Run(context.Background(), []Target{target}, Options{
		Prior:    findings,
		Decision: func(d RunDecision) { decisions = append(decisions, d) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(served) != 1 || !served[0].Cached {
		t.Fatalf("compile-discard record did not serve: %+v", served)
	}
	for _, decision := range decisions {
		if strings.Contains(decision.Reason, "re-executing") {
			t.Fatalf("serve still splices a deterministic compile rejection: %+v", decision)
		}
	}
	if _, err := Export(findings); err != nil {
		t.Fatalf("exporting compile-discard finding: %v", err)
	}
}

// A record persisted before the compile-rejection carve-out carries the
// old-style candidate evidence: its one remaining splice re-executes the
// rejection, produces no fresh evidence, and the persisted spliced record
// serves fully thereafter (REQ-result-stale's self-heal sentence).
func TestRunSelfHealsLegacyCompileRejectionEvidence(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	ctx := context.Background()
	targets := []Target{{Symbol: "example.com/fixture/plain.Ok", Oracle: []string{"example.com/fixture/plain.TestPlain"}}}
	first, err := tr.Run(ctx, targets, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].Discarded == 0 {
		t.Fatalf("seed finding = %+v", first)
	}
	// Regress the record to the pre-carve-out shape: flag one discarded
	// candidate with the legacy incomplete-process reason.
	position, operator := discardedCandidateIdentity(t, tr, targets[0].Symbol, first[0])
	legacy := first[0]
	legacy.CandidateEvidence = []CandidateEvidence{{
		Position:    position,
		Operator:    operator,
		Reason:      "mutant test process did not start because the mutant failed to build",
		Disposition: "discarded",
	}}

	var decisions []RunDecision
	second, err := tr.Run(ctx, targets, Options{
		Prior:    []Finding{legacy},
		Decision: func(d RunDecision) { decisions = append(decisions, d) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || !second[0].Cached {
		t.Fatalf("legacy record did not serve via splice: %+v", second)
	}
	spliced := false
	for _, decision := range decisions {
		if strings.Contains(decision.Reason, "re-executing") {
			spliced = true
		}
	}
	if !spliced {
		t.Fatalf("legacy record served without its one remaining splice: %+v", decisions)
	}
	// The splice's fresh execution produces no evidence for the rejection,
	// so the record the caller persists is evidence-free - the tax ends
	// here (REQ-result-stale's self-heal sentence).
	if len(second[0].CandidateEvidence) != 0 {
		t.Fatalf("spliced record still carries candidate evidence: %+v", second[0].CandidateEvidence)
	}
	if !reflect.DeepEqual(summarizeFinding(first[0]), summarizeFinding(second[0])) {
		t.Fatalf("self-heal changed the measurement:\n first %+v\n second %+v", summarizeFinding(first[0]), summarizeFinding(second[0]))
	}
	var healedDecisions []RunDecision
	third, err := tr.Run(ctx, targets, Options{
		Prior:    second,
		Decision: func(d RunDecision) { healedDecisions = append(healedDecisions, d) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(third) != 1 || !third[0].Cached {
		t.Fatalf("healed record did not serve: %+v", third)
	}
	for _, decision := range healedDecisions {
		if strings.Contains(decision.Reason, "re-executing") {
			t.Fatalf("healed record still splices: %+v", decision)
		}
	}
}

// discardedCandidateIdentity picks a candidate the fixture provably
// compile-rejects: an operator whose every generated candidate was discarded,
// then that operator's first candidate in the regenerated set.
func discardedCandidateIdentity(t *testing.T, tr *Tree, symbol string, rec Finding) (position, operator string) {
	t.Helper()
	generation, err := tr.eng.CandidatesContext(context.Background(), symbol, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, summary := range rec.Operators {
		if summary.Discarded == 0 || summary.Killed != 0 || summary.Survived != 0 || summary.Discarded != summary.Generated {
			continue
		}
		// The discard must be a compile rejection, not a generation-time
		// duplicate: only a runnable candidate can be spliced.
		for _, candidate := range generation.Candidates {
			if candidate.Operator != summary.Operator {
				continue
			}
			if _, runnable := candidate.Mutant(); runnable {
				return candidate.Position, candidate.Operator
			}
		}
	}
	t.Fatalf("fixture has no runnable fully-discarded candidate: %+v", rec.Operators)
	return "", ""
}

type findingSummary struct {
	killed, mutants, discarded, generated, candidateCount int
	survivors                                             []Survivor
}

func summarizeFinding(f Finding) findingSummary {
	survivors := append([]Survivor(nil), f.Survivors...)
	sort.Slice(survivors, func(i, j int) bool {
		if survivors[i].Position != survivors[j].Position {
			return survivors[i].Position < survivors[j].Position
		}
		return survivors[i].Operator < survivors[j].Operator
	})
	for i := range survivors {
		survivors[i].Execution = ""
	}
	return findingSummary{killed: f.Killed, mutants: f.Mutants, discarded: f.Discarded, generated: f.Generated, candidateCount: f.CandidateCount, survivors: survivors}
}

// An oracle group that runs contributes its completed observation even when a
// sibling group's build rejects the mutant: the union keeps the ran process's
// runtime evidence verifiable instead of poisoning it with a no-process state
// (candidate evidence term: "an oracle group that did run contributes its
// completed observation to the union as usual").
func TestRunCompileRejectionKeepsSiblingGroupObservations(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	findings, err := tr.Run(context.Background(), []Target{{
		Symbol: "example.com/fixture/lib.Idx",
		Oracle: []string{"example.com/fixture/lib.TestAdd", "example.com/fixture/plain.TestPlain"},
	}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Discarded == 0 {
		t.Fatalf("cross-package compile-discard finding = %+v", findings)
	}
	if flagged := findings[0].CandidateEvidence; len(flagged) != 0 {
		t.Fatalf("candidate evidence = %+v, want none", flagged)
	}
	evidence := findings[0].TargetEvidence
	if evidence.RuntimeUnverifiable {
		t.Fatalf("sibling group's completed observation poisoned the union: %+v", evidence)
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
	fs, err := ParseFindings([]byte(`{"version":3,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":0,"targetEvidence":{"symbol":"p.F","maximalClosure":"c","testVariantClosure":"tv","toolchain":"go","buildConfig":"b","observationAssertion":"caller assertion","observationStrategy":"gofresh/observation-rta@2","observationSubjectPackage":"p","observationSubjectSymbol":"F","observationObservable":true,"observationEvidence":"proof","runtimeInputs":"m","runtimeDigest":"d"},"oracleEvidence":[{"symbol":"p.TestF","maximalClosure":"tc","testVariantClosure":"tv","toolchain":"go","buildConfig":"b","observationAssertion":"caller assertion","observationStrategy":"gofresh/observation-rta@2","observationSubjectPackage":"p","observationSubjectSymbol":"TestF","observationObservable":true,"observationEvidence":"proof","runtimeInputs":"m","runtimeDigest":"d"}],"oracleExplicit":true,"oracleTimeout":"1m0s","dirty":true,"candidateCount":0,"generated":0,"mutants":0,"killed":0,"discarded":0,"operators":[],"futureField":{"nested":true}}]}`))
	if err != nil || len(fs) != 1 || fs[0].Symbol != "p.F" {
		t.Fatalf("tolerant parse failed: %v %+v", err, fs)
	}
	for name, doc := range map[string]string{
		"null budget":                    `{"version":3,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":null,"targetEvidence":{},"oracleEvidence":[],"oracleTimeout":"1m0s","mutants":1,"killed":1}]}`,
		"null dirty":                     `{"version":3,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":1,"targetEvidence":{},"oracleEvidence":[],"oracleTimeout":"1m0s","dirty":null,"mutants":1,"killed":1}]}`,
		"duplicate budget":               `{"version":3,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":1,"budget":0,"targetEvidence":{},"oracleEvidence":[],"oracleTimeout":"1m0s","mutants":1,"killed":1}]}`,
		"duplicate version":              `{"version":3,"version":99,"findings":[]}`,
		"missing survivors":              `{"version":3,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":1,"targetEvidence":{},"oracleEvidence":[],"oracleTimeout":"1m0s","mutants":1,"killed":0}]}`,
		"empty attestation reason":       `{"version":3,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":1,"targetEvidence":{},"oracleEvidence":[],"oracleTimeout":"1m0s","mutants":1,"killed":0,"survivors":[{"position":"f.go:1:1","operator":"op"}],"attested":[{"position":"f.go:1:1","operator":"op","reason":""}]}]}`,
		"duplicate nested evidence":      `{"version":3,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":1,"targetEvidence":{"symbol":"p.F","symbol":"p.G"},"oracleEvidence":[],"oracleTimeout":"1m0s","mutants":0,"killed":0}]}`,
		"inflated budget":                `{"version":3,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":2,"targetEvidence":{},"oracleEvidence":[],"oracleTimeout":"1m0s","mutants":1,"killed":1}]}`,
		"colliding attestation identity": `{"version":3,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":1,"targetEvidence":{},"oracleEvidence":[],"oracleTimeout":"1m0s","mutants":1,"killed":0,"survivors":[{"position":"a|b.go:1:1","operator":"zero return"}],"attested":[{"position":"a","operator":"b.go:1:1|zero return","reason":"not the survivor"}]}]}`,
		"duplicate symbols":              `{"version":3,"findings":[{"symbol":"p.F","mutants":0,"killed":0},{"symbol":"p.F","mutants":0,"killed":0}]}`,
		"duplicate oracle symbols":       `{"version":3,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":0,"targetEvidence":{},"oracleEvidence":[{"symbol":"p.TestF"},{"symbol":"p.TestF"}],"oracleTimeout":"1m0s","dirty":true,"mutants":0,"killed":0}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseFindings([]byte(doc)); err == nil {
				t.Fatal("malformed known field accepted")
			}
		})
	}
	nonGit := `{"version":3,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":0,"targetEvidence":{"symbol":"p.F","maximalClosure":"c","testVariantClosure":"tv","toolchain":"go","buildConfig":"b","observationAssertion":"caller assertion","observationStrategy":"gofresh/observation-rta@2","observationSubjectPackage":"p","observationSubjectSymbol":"F","observationObservable":true,"observationEvidence":"proof","runtimeInputs":"m","runtimeDigest":"d"},"oracleEvidence":[{"symbol":"p.TestF","maximalClosure":"tc","testVariantClosure":"tv","toolchain":"go","buildConfig":"b","observationAssertion":"caller assertion","observationStrategy":"gofresh/observation-rta@2","observationSubjectPackage":"p","observationSubjectSymbol":"TestF","observationObservable":true,"observationEvidence":"proof","runtimeInputs":"m","runtimeDigest":"d"}],"oracleExplicit":true,"oracleTimeout":"1m0s","dirty":true,"candidateCount":0,"generated":0,"mutants":0,"killed":0,"discarded":0,"operators":[]}]}`
	nonGitFindings, err := ParseFindings([]byte(nonGit))
	if err != nil || len(nonGitFindings) != 1 {
		t.Fatalf("non-Git provenance rejected: %v %+v", err, nonGitFindings)
	}
	for _, field := range []string{`,"candidateCount":0`, `,"generated":0`, `,"discarded":0`} {
		if _, err := ParseFindings([]byte(strings.Replace(nonGit, field, "", 1))); err == nil {
			t.Fatalf("finding without required count %s accepted", field)
		}
	}
	for _, field := range []string{`,"observationAssertion":"caller assertion"`, `,"observationEvidence":"proof"`} {
		if _, err := ParseFindings([]byte(strings.Replace(nonGit, field, "", 1))); err == nil {
			t.Fatalf("finding without required observation evidence %s accepted", field)
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
	emptyOracle := `{"version":3,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":0,"targetEvidence":{"symbol":"p.F","maximalClosure":"c","testVariantClosure":"tv","toolchain":"go","buildConfig":"b","runtimeInputs":"m","runtimeDigest":"d"},"oracleEvidence":[],"oracleExplicit":true,"oracleTimeout":"1m0s","dirty":true,"candidateCount":0,"generated":0,"mutants":0,"killed":0,"discarded":0,"operators":[]}]}`
	if _, err := ParseFindings([]byte(emptyOracle)); err == nil {
		t.Fatal("empty oracle evidence accepted")
	}
	// The compartment pin is required and never legitimately empty: gofresh
	// defines a non-empty identity even for a package with no test files, so
	// an empty or absent value is a record that could never serve.
	missingCompartment := strings.Replace(nonGit, `"testVariantClosure":"tv",`, "", 1)
	if _, err := ParseFindings([]byte(missingCompartment)); err == nil {
		t.Fatal("missing test-variant compartment pin accepted")
	}
	emptyCompartment := strings.Replace(nonGit, `"testVariantClosure":"tv"`, `"testVariantClosure":""`, 1)
	if _, err := ParseFindings([]byte(emptyCompartment)); err == nil {
		t.Fatal("empty test-variant compartment pin accepted")
	}
	withoutDirty := strings.Replace(nonGit, `,"dirty":true`, "", 1)
	if _, err := ParseFindings([]byte(withoutDirty)); err == nil {
		t.Fatal("missing commit without dirty provenance accepted")
	}
	committedWithoutDirty := strings.Replace(withoutDirty, `"oracleTimeout":"1m0s"`, `"oracleTimeout":"1m0s","commit":"abc"`, 1)
	if _, err := ParseFindings([]byte(committedWithoutDirty)); err == nil {
		t.Fatal("committed finding without explicit dirty provenance accepted")
	}
	legacy := `{"version":3,"findings":[{"symbol":"p.F","mutants":1,"killed":0,"survivors":[{"position":"f.go:1:1","operator":"op"}],"attested":[{"position":"f.go:1:1","operator":"op","reason":"legacy"}]}]}`
	if _, err := ParseFindings([]byte(legacy)); err == nil {
		t.Fatal("legacy finding accepted")
	}
	emptyPins := `{"version":3,"findings":[{"symbol":"p.F","bodyHash":"","operatorSet":"","budget":1,"targetEvidence":{"symbol":"","maximalClosure":"","toolchain":"","buildConfig":"","runtimeInputs":"","runtimeDigest":""},"oracleEvidence":[],"oracleTimeout":"","dirty":true,"mutants":1,"killed":0,"survivors":[{"position":"f.go:1:1","operator":"op"}],"attested":[{"position":"f.go:1:1","operator":"op","reason":"unsupported"}]}]}`
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

// TestRunPanickedMutantIsCandidateLocalAndServes pins the candidate-evidence
// serve path end to end (REQ-exec-observation, REQ-result-stale): one
// mutant's test process panics before observation finalization, its
// incompleteness attaches to that candidate alone while the siblings'
// completed union stays verifiable, and a second run serves the record while
// re-executing exactly the flagged candidates under a fresh passing baseline
// probe — counted through the run-decision event.
func TestRunPanickedMutantIsCandidateLocalAndServes(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	target := Target{Symbol: "example.com/fixture/candlocal.Value", Oracle: []string{"example.com/fixture/candlocal.TestValue"}}
	first, err := tr.Run(context.Background(), []Target{target}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	f := first[0]
	if f.TargetEvidence.RuntimeUnverifiable || len(f.OracleEvidence) != 1 || f.OracleEvidence[0].RuntimeUnverifiable {
		t.Fatalf("sibling evidence = %+v, want the completed-process union verifiable", f.TargetEvidence)
	}
	panicked := 0
	for _, candidate := range f.CandidateEvidence {
		if candidate.Operator == "return: zero" && strings.Contains(candidate.Reason, "panicked before observation finalization") && candidate.Disposition == "killed" {
			panicked++
		}
	}
	if panicked != 1 {
		t.Fatalf("candidate evidence = %+v, want the panicking zero-return kill flagged once", f.CandidateEvidence)
	}
	if f.Generated != f.Mutants+f.Discarded || f.Mutants != f.Killed+len(f.Survivors) || f.Generated != f.CandidateCount {
		t.Fatalf("first-run counts do not reconcile: %+v", f)
	}
	doc, err := Export(first)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := ParseFindings(doc)
	if err != nil {
		t.Fatal(err)
	}

	// Every pin matches, yet the record covers the target only through the
	// flagged re-execution, so it is not fresh without measurement
	// (REQ-result-stale).
	if ok, err := tr.Fresh(prior[0], target, 0); err != nil || ok {
		t.Fatalf("candidate-local record coverable without execution = %v, %v", ok, err)
	}

	var decisions []RunDecision
	var preparation []PreparationEvent
	second, err := tr.Run(context.Background(), []Target{target}, Options{
		Prior:    prior,
		Decision: func(decision RunDecision) { decisions = append(decisions, decision) },
		Progress: func(event PreparationEvent) { preparation = append(preparation, event) },
	})
	if err != nil {
		t.Fatal(err)
	}
	want := RunDecision{Symbol: target.Symbol, Action: "cached",
		Reason:     fmt.Sprintf("served: pins unchanged; re-executing %s", candidateNoun(len(f.CandidateEvidence))),
		Candidates: len(f.CandidateEvidence)}
	if len(decisions) != 1 || decisions[0] != want {
		t.Fatalf("serve decision = %+v, want %+v (exactly the flagged candidates re-executed)", decisions, want)
	}
	probed := false
	for _, event := range preparation {
		if event.Stage == PreparationBaseline {
			probed = true
		}
	}
	if !probed {
		t.Fatal("serve path launched no current baseline probe")
	}
	s := second[0]
	if !s.Cached {
		t.Fatal("candidate-local record was not served")
	}
	if s.Mutants != f.Mutants || s.Killed != f.Killed || s.Discarded != f.Discarded || s.Generated != f.Generated || s.CandidateCount != f.CandidateCount {
		t.Fatalf("spliced counts = %+v, want conserved against %+v", s, f)
	}
	if s.TargetEvidence.RuntimeUnverifiable {
		t.Fatalf("spliced evidence = %+v, want the served union intact", s.TargetEvidence)
	}
	if len(s.CandidateEvidence) != len(f.CandidateEvidence) {
		t.Fatalf("re-executed candidate evidence = %+v, want the deterministic incompleteness re-flagged", s.CandidateEvidence)
	}
	if _, err := Export(second); err != nil {
		t.Fatalf("exporting spliced finding: %v", err)
	}
}

// TestCompletedObservationUnionIsCandidateGranular pins the union rule
// (REQ-exec-observation): a candidate whose process cannot prove its log
// complete is excluded from the completed-process union and returned as that
// candidate's explicit evidence, while an incomplete BASELINE observation is
// always finding-wide — the union itself becomes unverifiable and no
// candidate is flagged for it.
func TestCompletedObservationUnionIsCandidateGranular(t *testing.T) {
	root := t.TempDir()
	env := os.Environ()
	ctx := context.Background()
	completedBaseline, err := runtimeinput.FromTestLogEnv([]byte("# test log\n"), root, root, env, runtimeinput.WithCompletedProcess("baseline"), runtimeinput.WithBracket(testBracket(t, root)))
	if err != nil {
		t.Fatal(err)
	}
	completedCandidate, err := runtimeinput.FromTestLogEnv([]byte("# test log\n"), root, root, env, runtimeinput.WithCompletedProcess("candidate"), runtimeinput.WithBracket(testBracket(t, root)))
	if err != nil {
		t.Fatal(err)
	}
	incompleteCandidate, err := runtimeinput.IncompleteEnv(root, "incomplete-candidate", "mutant test process panicked before observation finalization", env)
	if err != nil {
		t.Fatal(err)
	}
	runnable := []engine.Replacement{{File: "f.go", Source: []byte("x")}}
	candidates := []engine.Candidate{
		{Symbol: "p.F", Operator: "op-a", Position: "f.go:1:1", Replacements: runnable},
		{Symbol: "p.F", Operator: "op-b", Position: "f.go:2:2", Replacements: runnable},
		{Symbol: "p.F", Operator: "op-c", Position: "f.go:3:3"}, // pre-execution discard: never launched, never flagged
	}
	outcomes := []engine.MutantOutcome{engine.MutantSurvived, engine.MutantKilled, engine.MutantDiscarded}
	observations := []runtimeinput.Observation{completedCandidate, incompleteCandidate, {}}
	incompletes := []string{"", "mutant test process panicked before observation finalization", ""}
	union, evidence, err := completedObservationUnion(ctx, root, env, []runtimeinput.Observation{completedBaseline}, candidates, outcomes, observations, incompletes, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !union.OK || union.Unverifiable {
		t.Fatalf("completed-only union = %+v, want verifiable evidence with the incomplete process excluded", union)
	}
	want := CandidateEvidence{Position: "f.go:2:2", Operator: "op-b", Reason: "mutant test process panicked before observation finalization", Disposition: "killed"}
	if len(evidence) != 1 || evidence[0] != want {
		t.Fatalf("candidate evidence = %+v, want %+v", evidence, want)
	}

	incompleteBaseline, err := runtimeinput.IncompleteEnv(root, "incomplete-baseline", "baseline test process produced no runtime-input log", env)
	if err != nil {
		t.Fatal(err)
	}
	union, evidence, err = completedObservationUnion(ctx, root, env, []runtimeinput.Observation{incompleteBaseline}, candidates[:1], outcomes[:1], observations[:1], incompletes[:1], nil)
	if err != nil {
		t.Fatal(err)
	}
	if !union.OK || !union.Unverifiable || !strings.Contains(union.Reason, "produced no runtime-input log") || len(evidence) != 0 {
		t.Fatalf("incomplete-baseline union = %+v, evidence %+v, want finding-wide unverifiability with no candidate flagged", union, evidence)
	}
}

// TestParseFindingsCandidateEvidence pins the persisted candidate-evidence
// encoding (REQ-result-record, REQ-result-export): a well-formed flagged
// candidate round-trips, while malformed identity, disposition,
// survivor-contradicting, or count-exceeding evidence is refused.
func TestParseFindingsCandidateEvidence(t *testing.T) {
	valid := `{"version":3,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/12","budget":0,"targetEvidence":{"symbol":"p.F","maximalClosure":"c","testVariantClosure":"tv","toolchain":"go","buildConfig":"b","observationAssertion":"caller assertion","observationStrategy":"gofresh/observation-rta@2","observationSubjectPackage":"p","observationSubjectSymbol":"F","observationObservable":true,"observationEvidence":"proof","runtimeInputs":"m","runtimeDigest":"d"},"oracleEvidence":[{"symbol":"p.TestF","maximalClosure":"tc","testVariantClosure":"tv","toolchain":"go","buildConfig":"b","observationAssertion":"caller assertion","observationStrategy":"gofresh/observation-rta@2","observationSubjectPackage":"p","observationSubjectSymbol":"TestF","observationObservable":true,"observationEvidence":"proof","runtimeInputs":"m","runtimeDigest":"d"}],"oracleExplicit":true,"oracleTimeout":"1m0s","dirty":true,"candidateCount":2,"generated":2,"mutants":2,"killed":1,"discarded":0,"operators":[{"operator":"op","generated":2,"discarded":0,"killed":1,"survived":1}],"survivors":[{"position":"f.go:2:2","operator":"op"}],"candidateEvidence":[{"position":"f.go:1:1","operator":"op","reason":"mutant test process panicked before observation finalization","disposition":"killed"}]}]}`
	findings, err := ParseFindings([]byte(valid))
	if err != nil || len(findings) != 1 || len(findings[0].CandidateEvidence) != 1 ||
		findings[0].CandidateEvidence[0].Disposition != "killed" {
		t.Fatalf("valid candidate evidence refused: %v %+v", err, findings)
	}
	entry := `{"position":"f.go:1:1","operator":"op","reason":"mutant test process panicked before observation finalization","disposition":"killed"}`
	for name, doc := range map[string]string{
		"invalid disposition": strings.Replace(valid, `"disposition":"killed"`, `"disposition":"vanished"`, 1),
		"missing reason":      strings.Replace(valid, `"reason":"mutant test process panicked before observation finalization",`, "", 1),
		"empty reason":        strings.Replace(valid, `"reason":"mutant test process panicked before observation finalization"`, `"reason":""`, 1),
		"duplicate identity":  strings.Replace(valid, entry, entry+","+entry, 1),
		"survivor contradiction": strings.Replace(valid,
			`"candidateEvidence":[{"position":"f.go:1:1"`, `"candidateEvidence":[{"position":"f.go:2:2"`, 1),
		"phantom survivor": strings.Replace(valid, `"disposition":"killed"`, `"disposition":"survived"`, 1),
		"kill count excess": strings.Replace(valid, entry,
			entry+`,{"position":"f.go:3:3","operator":"op","reason":"mutant test process timed out","disposition":"killed"}`, 1),
		"discard count excess": strings.Replace(valid, `"disposition":"killed"}]`, `"disposition":"discarded"}]`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseFindings([]byte(doc)); err == nil {
				t.Fatal("malformed candidate evidence accepted")
			}
		})
	}
}

// TestSpliceFindingCountsConservesChangedOutcomes pins splice accounting
// under INV-RESULT-CANDIDATE-CONSERVATION: each flagged candidate's fresh
// outcome replaces its recorded disposition per operator and in the totals, a
// flagged kill that now survives opens a survivor, a flagged survivor that
// now dies sheds its attestation (REQ-attest-survivor), and covered
// candidates keep their recorded outcomes.
func TestSpliceFindingCountsConservesChangedOutcomes(t *testing.T) {
	runnable := []engine.Replacement{{File: "f.go", Source: []byte("x")}}
	candidates := []engine.Candidate{
		{Symbol: "p.F", Operator: "op-a", Position: "f.go:1:1", Replacements: runnable}, // covered kill
		{Symbol: "p.F", Operator: "op-a", Position: "f.go:2:2", Replacements: runnable}, // flagged kill -> survivor
		{Symbol: "p.F", Operator: "op-b", Position: "f.go:3:3", Replacements: runnable}, // flagged survivor -> kill
		{Symbol: "p.F", Operator: "op-b", Position: "f.go:4:4", Replacements: runnable}, // covered survivor
		{Symbol: "p.F", Operator: "op-c", Position: "f.go:5:5", Replacements: runnable}, // flagged discard -> discard
	}
	rec := Finding{
		Symbol: "p.F", CandidateCount: 5, Generated: 5, Mutants: 4, Killed: 2, Discarded: 1,
		Operators: []OperatorSummary{
			{Operator: "op-a", Generated: 2, Killed: 2},
			{Operator: "op-b", Generated: 2, Survived: 2},
			{Operator: "op-c", Generated: 1, Discarded: 1},
		},
		Survivors: []Survivor{{Position: "f.go:3:3", Operator: "op-b", Execution: "never-executed"}, {Position: "f.go:4:4", Operator: "op-b", Execution: "executed-and-passed"}},
		Attested: []Attestation{
			{Position: "f.go:3:3", Operator: "op-b", Reason: "was equivalent"},
			{Position: "f.go:4:4", Operator: "op-b", Reason: "still equivalent"},
		},
		CandidateEvidence: []CandidateEvidence{
			{Position: "f.go:2:2", Operator: "op-a", Reason: "mutant test process panicked before observation finalization", Disposition: "killed"},
			{Position: "f.go:3:3", Operator: "op-b", Reason: "test process produced no runtime-input log", Disposition: "survived"},
			{Position: "f.go:5:5", Operator: "op-c", Reason: "mutant test process did not start because the mutant failed to build", Disposition: "discarded"},
		},
	}
	flagged := map[int]bool{1: true, 2: true, 4: true}
	outcomes := []engine.MutantOutcome{0, engine.MutantSurvived, engine.MutantKilled, 0, engine.MutantDiscarded}
	fresh := []CandidateEvidence{{Position: "f.go:5:5", Operator: "op-c", Reason: "mutant test process did not start because the mutant failed to build", Disposition: "discarded"}}
	spliced, err := spliceFindingCounts(context.Background(), rec, candidates, flagged, outcomes, fresh)
	if err != nil {
		t.Fatal(err)
	}
	if spliced.Generated != 5 || spliced.CandidateCount != 5 || spliced.Mutants != 4 || spliced.Killed != 2 || spliced.Discarded != 1 {
		t.Fatalf("spliced totals = %+v, want conservation across swapped outcomes", spliced)
	}
	wantOperators := []OperatorSummary{
		{Operator: "op-a", Generated: 2, Killed: 1, Survived: 1},
		{Operator: "op-b", Generated: 2, Killed: 1, Survived: 1},
		{Operator: "op-c", Generated: 1, Discarded: 1},
	}
	if !slices.Equal(spliced.Operators, wantOperators) {
		t.Fatalf("spliced operators = %+v, want %+v", spliced.Operators, wantOperators)
	}
	// The covered survivor carries its recorded advisory bucket verbatim; the
	// flagged kill flipping into survival has no recorded bucket and stays
	// unbucketed (REQ-exec-survivor-evidence).
	wantSurvivors := []Survivor{{Position: "f.go:2:2", Operator: "op-a"}, {Position: "f.go:4:4", Operator: "op-b", Execution: "executed-and-passed"}}
	if !slices.Equal(spliced.Survivors, wantSurvivors) {
		t.Fatalf("spliced survivors = %+v, want %+v", spliced.Survivors, wantSurvivors)
	}
	if len(spliced.Attested) != 1 || spliced.Attested[0].Position != "f.go:4:4" {
		t.Fatalf("spliced attestations = %+v, want the dead survivor's disposition shed", spliced.Attested)
	}
	if !slices.Equal(spliced.CandidateEvidence, fresh) {
		t.Fatalf("spliced candidate evidence = %+v, want the fresh flags only", spliced.CandidateEvidence)
	}
	for _, summary := range spliced.Operators {
		if summary.Generated != summary.Discarded+summary.Killed+summary.Survived {
			t.Fatalf("operator summary does not conserve: %+v", summary)
		}
	}
	if spliced.Generated != spliced.Mutants+spliced.Discarded || spliced.Mutants != spliced.Killed+len(spliced.Survivors) {
		t.Fatalf("finding totals do not conserve: %+v", spliced)
	}
}

// TestServeMatchingBatchesEvidenceChecksPerView pins the serve path's
// observation economy: a warm cached serve validates the record's target and
// oracle evidence through one CheckObservedBatch per analysis view — one
// runtime-input window shared across the view's subjects, observable as
// exactly two runtime observation passes (the window's open and close) —
// where a per-subject walk would pay one window per subject.
func TestServeMatchingBatchesEvidenceChecksPerView(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":   "module example.com/batch\n\ngo 1.26.5\n",
		"batch.go": "package batch\nfunc Value(x int) int {\n\tif x > 100 {\n\t\treturn x * 3\n\t}\n\treturn x + 1\n}\n",
		"batch_test.go": "package batch\n\nimport \"testing\"\n\n" +
			"func TestSmall(t *testing.T) {\n\tif Value(1) != 2 {\n\t\tt.Fail()\n\t}\n}\n\n" +
			"func TestBig(t *testing.T) {\n\tif Value(200) != 600 {\n\t\tt.Fail()\n\t}\n}\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	targets := []Target{{Symbol: "example.com/batch.Value"}}
	prior, err := tr.Run(context.Background(), targets, Options{Budget: 1})
	if err != nil || len(prior) != 1 || len(prior[0].OracleEvidence) != 2 {
		t.Fatalf("seed measurement = %+v, %v; want a two-test derived oracle", prior, err)
	}
	warm, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var runtimePasses atomic.Int64
	var decisions []RunDecision
	served, err := warm.Run(context.Background(), targets, Options{
		Budget: 1, Prior: prior,
		Decision: func(d RunDecision) { decisions = append(decisions, d) },
		AnalysisProgress: func(phase, _ string) {
			if phase == "runtime" {
				runtimePasses.Add(1)
			}
		},
	})
	if err != nil || len(served) != 1 || !served[0].Cached {
		t.Fatalf("warm serve = %+v, %v; decisions %+v", served, err, decisions)
	}
	// Target plus two oracle tests share one view: one batched check, one
	// window, two passes. Three per-subject windows would show six.
	if got := runtimePasses.Load(); got != 2 {
		t.Fatalf("warm serve paid %d runtime observation passes, want 2 (one batched window)", got)
	}
}

// TestGrowthGateRefusesPinMovedBehindCompartmentVerdict pins the growth
// gate's plain-validity bar (REQ-result-stale): gofresh orders the
// compartment comparison before the environment tiers, so a moved pin can
// hide behind the stale "test variants" verdict — the gate must refuse a
// record whose toolchain pin moved even though the compartment delta is
// inert, and accept the same record with its pins intact.
func TestGrowthGateRefusesPinMovedBehindCompartmentVerdict(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":  "module example.com/growthgate\n\ngo 1.26.5\n",
		"gate.go": "package growthgate\nfunc Value(x int) int {\n\tif x > 100 {\n\t\treturn x * 3\n\t}\n\treturn x + 1\n}\n",
		"gate_test.go": "package growthgate\n\nimport \"testing\"\n\n" +
			"func TestSmall(t *testing.T) {\n\tif Value(1) != 2 {\n\t\tt.Fail()\n\t}\n}\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	targets := []Target{{Symbol: "example.com/growthgate.Value"}}
	prior, err := tr.Run(context.Background(), targets, Options{Budget: 1})
	if err != nil || len(prior) != 1 || prior[0].CompartmentLedger == nil {
		t.Fatalf("seed measurement = %+v, %v", prior, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "more_test.go"),
		[]byte("package growthgate\n\nimport \"testing\"\n\nfunc TestMore(t *testing.T) {\n\tif Value(200) != 600 {\n\t\tt.Fail()\n\t}\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	grown, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	views, err := grown.newSubjectViews(context.Background(), []string{
		"example.com/growthgate.Value", "example.com/growthgate.TestSmall", "example.com/growthgate.TestMore",
	})
	if err != nil {
		t.Fatal(err)
	}
	target := views.bySymbol["example.com/growthgate.Value"]
	oracle := []*subjectView{
		views.bySymbol["example.com/growthgate.TestSmall"],
		views.bySymbol["example.com/growthgate.TestMore"],
	}
	added, ok, err := evidenceSetCoversGrowthContext(context.Background(), prior[0], target, oracle, false, engine.OperatorSet, prior[0].OracleTimeout)
	if err != nil || !ok || len(added) != 1 || added[0] != "example.com/growthgate.TestMore" {
		t.Fatalf("intact pins refused the growth gate: added=%v ok=%v err=%v", added, ok, err)
	}
	tampered := prior[0]
	tampered.TargetEvidence.Toolchain = "go0.0-never"
	if _, ok, err := evidenceSetCoversGrowthContext(context.Background(), tampered, target, oracle, false, engine.OperatorSet, prior[0].OracleTimeout); err != nil || ok {
		t.Fatalf("a moved toolchain hid behind the compartment verdict: ok=%v err=%v", ok, err)
	}
	// Growth is a derived-oracle claim on both sides: an explicit request
	// supersetting the recorded derived set is the caller's selection,
	// never derived growth.
	if _, ok, err := evidenceSetCoversGrowthContext(context.Background(), prior[0], target, oracle, true, engine.OperatorSet, prior[0].OracleTimeout); err != nil || ok {
		t.Fatalf("an explicit request rode the derived-growth carve-out: ok=%v err=%v", ok, err)
	}
}

// TestRunServesGrownOracleMeasuringOnlySurvivors pins the oracle-growth
// carve-out end to end (REQ-result-stale): a sibling test added beside the
// oracle grows the derived set under an inert compartment delta, so the
// prior record serves — kills and discards stand — while only the recorded
// survivors re-execute against the added test alone. Newly killed survivors
// move to killed (shedding their attestations with the contradiction
// reported), still-surviving ones keep their survival and recorded buckets,
// the grown record carries the current tree's evidence and ledger — proven
// by a follow-up run serving it cached — and a non-inert compartment delta
// refuses the arm outright.
func TestRunServesGrownOracleMeasuringOnlySurvivors(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":        "module example.com/grown\n\ngo 1.26\n",
		"gated.go":      "package gated\n\nfunc Gated(x int) int {\n\ty := x + 1\n\tif y > 100 {\n\t\treturn y * 3\n\t}\n\treturn y\n}\n",
		"gated_test.go": "package gated\n\nimport \"testing\"\n\nfunc TestSmall(t *testing.T) {\n\tif Gated(5) != 6 {\n\t\tt.Fail()\n\t}\n}\n",
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
	target := Target{Symbol: "example.com/grown.Gated"}
	first, err := tr.Run(ctx, []Target{target}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	f := first[0]
	if len(f.Survivors) < 2 || f.Killed == 0 || f.CompartmentLedger == nil {
		t.Fatalf("baseline fixture = %+v, want survivors, kills, and a recorded ledger", f)
	}
	// Attest one survivor the added test will kill — an arithmetic mutant
	// inside the never-executed y>100 branch — and one it will not: the
	// > -> >= guard flip is behavior-identical for both 6 and 201.
	var doomed, keeper *Survivor
	for i, survivor := range f.Survivors {
		switch {
		case strings.Contains(survivor.Operator, "> -> >="):
			keeper = &f.Survivors[i]
		case doomed == nil && strings.HasPrefix(survivor.Operator, "arithmetic:"):
			doomed = &f.Survivors[i]
		}
	}
	if doomed == nil || keeper == nil {
		t.Fatalf("fixture survivors lack the two classes: %+v", f.Survivors)
	}
	if err := first[0].Attest(doomed.Position, doomed.Operator, "judged equivalent, wrongly"); err != nil {
		t.Fatal(err)
	}
	if err := first[0].Attest(keeper.Position, keeper.Operator, "still equivalent"); err != nil {
		t.Fatal(err)
	}
	doc, err := Export(first)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := ParseFindings(doc)
	if err != nil {
		t.Fatal(err)
	}

	// A sibling test exercising the large-x branch: the derived oracle grows
	// by one, the compartment delta is an inert added test function.
	if err := os.WriteFile(filepath.Join(dir, "gated_test.go"), []byte(files["gated_test.go"]+"\nfunc TestBig(t *testing.T) {\n\tif Gated(200) != 603 {\n\t\tt.Fail()\n\t}\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	grownTree, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var decisions []RunDecision
	var dispatched []int
	var contradictions []AttestationContradiction
	grownFindings, err := grownTree.Run(ctx, []Target{target}, Options{
		Prior:         prior,
		Decision:      func(d RunDecision) { decisions = append(decisions, d) },
		Contradiction: func(c AttestationContradiction) { contradictions = append(contradictions, c) },
		dispatched:    func(_ string, mi int) { dispatched = append(dispatched, mi) },
	})
	if err != nil {
		t.Fatal(err)
	}
	survivorCount := len(prior[0].Survivors)
	if survivorCount < 2 {
		t.Fatalf("fixture yielded %d survivors, want several so the plural literal below has teeth", survivorCount)
	}
	wantReason := fmt.Sprintf("served: derived oracle grew by 1 test; re-measuring %d survivors against them", survivorCount)
	if len(decisions) != 1 || decisions[0].Action != "measure" || decisions[0].Reason != wantReason || decisions[0].Candidates != survivorCount {
		t.Fatalf("growth decision = %+v, want %q over %d survivors", decisions, wantReason, survivorCount)
	}
	if len(dispatched) != survivorCount {
		t.Fatalf("dispatched %d candidates, want exactly the %d recorded survivors", len(dispatched), survivorCount)
	}
	grown := grownFindings[0]
	if grown.Cached || grown.TargetEvidence.RuntimeUnverifiable {
		t.Fatalf("grown record = cached %v, unverifiable %v; want a verifiable measurement", grown.Cached, grown.TargetEvidence.RuntimeUnverifiable)
	}
	if grown.Killed <= prior[0].Killed || len(grown.Survivors) >= survivorCount {
		t.Fatalf("growth measured nothing: killed %d->%d, survivors %d->%d", prior[0].Killed, grown.Killed, survivorCount, len(grown.Survivors))
	}
	if grown.Generated != prior[0].Generated || grown.CandidateCount != prior[0].CandidateCount ||
		grown.Generated != grown.Mutants+grown.Discarded || grown.Mutants != grown.Killed+len(grown.Survivors) {
		t.Fatalf("grown totals do not conserve: %+v", grown)
	}
	oracleSymbols := make([]string, 0, len(grown.OracleEvidence))
	for _, evidence := range grown.OracleEvidence {
		oracleSymbols = append(oracleSymbols, evidence.Symbol)
	}
	if !slices.Equal(oracleSymbols, []string{"example.com/grown.TestBig", "example.com/grown.TestSmall"}) {
		t.Fatalf("grown oracle evidence = %v, want both tests recorded", oracleSymbols)
	}
	if len(grown.Attested) != 1 || grown.Attested[0].Position != keeper.Position {
		t.Fatalf("grown attestations = %+v, want only the still-surviving disposition", grown.Attested)
	}
	if len(contradictions) != 1 || contradictions[0].Position != doomed.Position ||
		contradictions[0].Killer != "example.com/grown.TestBig" || contradictions[0].Reason != "judged equivalent, wrongly" {
		t.Fatalf("contradictions = %+v, want the doomed attestation shed with its killer named", contradictions)
	}

	// A covering but smaller budget request grows the same way: regeneration
	// runs at the record's own budget, so a serviceable exhaustive record is
	// never destroyed by the request shape, and the grown record keeps its
	// recorded budget.
	var cappedDecisions []RunDecision
	cappedGrown, err := grownTree.Run(ctx, []Target{target}, Options{
		Budget:   1,
		Prior:    prior,
		Decision: func(d RunDecision) { cappedDecisions = append(cappedDecisions, d) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cappedDecisions) != 1 || cappedDecisions[0].Reason != wantReason {
		t.Fatalf("covering-but-smaller budget decision = %+v, want the growth serve %q", cappedDecisions, wantReason)
	}
	if cappedGrown[0].Budget != prior[0].Budget || cappedGrown[0].Generated != prior[0].Generated {
		t.Fatalf("capped growth rewrote the record's selection: budget %d, generated %d", cappedGrown[0].Budget, cappedGrown[0].Generated)
	}

	// The grown record is current on the grown tree: a follow-up run serves
	// it without measurement.
	grownDoc, err := Export(grownFindings)
	if err != nil {
		t.Fatal(err)
	}
	reparsed, err := ParseFindings(grownDoc)
	if err != nil {
		t.Fatal(err)
	}
	var followDecisions []RunDecision
	followed, err := grownTree.Run(ctx, []Target{target}, Options{Prior: reparsed, Decision: func(d RunDecision) { followDecisions = append(followDecisions, d) }})
	if err != nil {
		t.Fatal(err)
	}
	if !followed[0].Cached || len(followDecisions) != 1 || followDecisions[0].Action != "cached" {
		t.Fatalf("grown record did not serve cached on its own tree: %+v, %+v", followed[0].Cached, followDecisions)
	}

	// A non-inert compartment delta — the added test plus an edited existing
	// one — refuses the arm: the whole target re-measures.
	if err := os.WriteFile(filepath.Join(dir, "gated_test.go"), []byte(strings.Replace(files["gated_test.go"], "t.Fail()", "t.Fail() // edited", 1)+"\nfunc TestBig(t *testing.T) {\n\tif Gated(200) != 603 {\n\t\tt.Fail()\n\t}\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	editedTree, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	var editedDecisions []RunDecision
	var editedDispatched []int
	edited, err := editedTree.Run(ctx, []Target{target}, Options{
		Prior:      prior,
		Decision:   func(d RunDecision) { editedDecisions = append(editedDecisions, d) },
		dispatched: func(_ string, mi int) { editedDispatched = append(editedDispatched, mi) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if edited[0].Cached || len(editedDecisions) != 1 || !strings.HasPrefix(editedDecisions[0].Reason, "stale: ") ||
		len(editedDispatched) != prior[0].Generated {
		t.Fatalf("non-inert delta = %+v dispatching %d, want a whole re-measure", editedDecisions, len(editedDispatched))
	}
}

// TestRunExtensionDivergenceStampsAndAttributes pins the extension's
// fail-closed divergence path end to end (REQ-result-stale's fail-closed
// bound, REQ-exec-oracle-guidance, REQ-exec-survivor-evidence): a suffix
// mutant flips a guard so the oracle deterministically reads an input the
// capped record never pinned; the spliced record is preserved but stamped
// non-reusable, suffix survivors classify unstable-oracle, and the
// oracle-instability attribution fires for the partial measurement exactly
// as it would for a whole one.
func TestRunExtensionDivergenceStampsAndAttributes(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":        "module example.com/gated\n\ngo 1.26.4\n",
		"gated.go":      "package gated\n\nfunc Gated(x int) int {\n\ty := x + 1\n\tif y > 100 {\n\t\treturn y * 1000\n\t}\n\treturn y\n}\n",
		"gated_test.go": "package gated\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestGated(t *testing.T) {\n\tbase, _ := os.ReadFile(\"baseline.txt\")\n\tif len(base) == 0 {\n\t\tt.Fail()\n\t\treturn\n\t}\n\tgot := Gated(5)\n\tif got > 100 {\n\t\tdata, _ := os.ReadFile(\"extra.txt\")\n\t\tif len(data) == 0 {\n\t\t\tt.Fail()\n\t\t\treturn\n\t\t}\n\t}\n\tif got != 6 {\n\t\tt.Fail()\n\t}\n}\n",
		"baseline.txt":  "pinned bytes\n",
		"extra.txt":     "unpinned bytes\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tr, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	target := Target{Symbol: "example.com/gated.Gated"}

	capped, err := tr.Run(ctx, []Target{target}, Options{Budget: 1})
	if err != nil {
		t.Fatal(err)
	}
	if capped[0].TargetEvidence.RuntimeUnverifiable || capped[0].Generated != 1 || capped[0].CandidateCount < 2 {
		t.Fatalf("capped fixture = %+v, want one clean measured candidate ahead of the guard mutants", capped[0])
	}
	for _, oracle := range capped[0].OracleEvidence {
		if oracle.RuntimeUnverifiable {
			t.Fatalf("capped oracle evidence unverifiable at capture: %+v", oracle)
		}
	}
	inspection, ierr := tr.InspectFinding(capped[0])
	if ierr != nil || inspection.State != FindingCurrent {
		t.Fatalf("capped inspection = %+v, %v\noracle evidence: %+v", inspection, ierr, capped[0].OracleEvidence)
	}
	doc, err := Export(capped)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := ParseFindings(doc)
	if err != nil {
		t.Fatal(err)
	}

	var guided []OracleGuidance
	var decisions []RunDecision
	extended, err := tr.Run(ctx, []Target{target}, Options{
		Prior:    prior,
		Guidance: func(g OracleGuidance) { guided = append(guided, g) },
		Decision: func(d RunDecision) { decisions = append(decisions, d) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 1 || decisions[0].Action != "measure" || !strings.HasPrefix(decisions[0].Reason, "served: prefix of 1 candidate stands") {
		t.Fatalf("extension decision = %+v, want the served-prefix measure", decisions)
	}
	f := extended[0]
	if !f.TargetEvidence.RuntimeUnverifiable {
		t.Fatalf("suffix guard-flip read an unpinned input yet the extension stayed reusable: %+v", f.TargetEvidence)
	}
	prefixSurvivors := len(prior[0].Survivors)
	if len(f.Survivors) <= prefixSurvivors {
		t.Fatalf("fixture yielded no suffix survivors, the stamp assertion would be vacuous: %+v", f.Survivors)
	}
	for _, survivor := range f.Survivors[prefixSurvivors:] {
		if survivor.Execution != "unstable-oracle" {
			t.Fatalf("suffix survivor of a stamped extension = %+v, want unstable-oracle", f.Survivors)
		}
	}
	if len(guided) == 0 || guided[0].Symbol != target.Symbol {
		t.Fatalf("guidance = %+v, want the partial measurement's oracle-instability attribution", guided)
	}
}

// TestEvidenceSetCoversGrowthRefusesIneligibleRecords pins the growth
// gate's pre-checks (REQ-result-stale's growth carve-out): an explicit
// oracle, a missing compartment ledger, a moved scalar pin, a non-grown
// set, or candidate evidence each refuse before any evidence is consulted.
func TestEvidenceSetCoversGrowthRefusesIneligibleRecords(t *testing.T) {
	ledger := &CompartmentLedger{}
	base := Finding{
		OperatorSet: "go/12", OracleTimeout: "1m0s", CompartmentLedger: ledger,
		OracleEvidence: []SubjectEvidence{{Symbol: "p.TestA"}},
	}
	oracle := make([]*subjectView, 2)
	ctx := context.Background()
	for name, prior := range map[string]Finding{
		"explicit oracle": func() Finding { f := base; f.OracleExplicit = true; return f }(),
		"missing ledger":  func() Finding { f := base; f.CompartmentLedger = nil; return f }(),
		"operator set":    func() Finding { f := base; f.OperatorSet = "go/1"; return f }(),
		"oracle timeout":  func() Finding { f := base; f.OracleTimeout = "2m0s"; return f }(),
		"candidate evidence": func() Finding {
			f := base
			f.CandidateEvidence = []CandidateEvidence{{Position: "f.go:1:1", Operator: "op", Reason: "r", Disposition: "survived"}}
			return f
		}(),
	} {
		added, ok, err := evidenceSetCoversGrowthContext(ctx, prior, nil, oracle, false, "go/12", "1m0s")
		if err != nil || ok || added != nil {
			t.Fatalf("%s: growth gate = %v %v %v, want a refusal before any evidence check", name, added, ok, err)
		}
	}
	// Not strictly grown: recorded set size equals the current one.
	equal := base
	if added, ok, err := evidenceSetCoversGrowthContext(ctx, equal, nil, oracle[:1], false, "go/12", "1m0s"); err != nil || ok || added != nil {
		t.Fatalf("non-grown set = %v %v %v, want a refusal", added, ok, err)
	}
}

// TestGrowFindingCountsReplacesSurvivorOutcomes pins the growth splice
// accounting under INV-RESULT-CANDIDATE-CONSERVATION: every non-survivor
// keeps its recorded disposition, each re-executed survivor's fresh outcome
// replaces its survival per operator and in the totals, still-surviving
// candidates carry their recorded buckets (unstable-oracle under a
// divergence stamp), and a newly killed attested survivor's attestation is
// shed and returned while a still-surviving one's carries.
func TestGrowFindingCountsReplacesSurvivorOutcomes(t *testing.T) {
	runnable := []engine.Replacement{{File: "f.go", Source: []byte("x")}}
	candidates := []engine.Candidate{
		{Symbol: "p.F", Operator: "op-a", Position: "f.go:1:1", Replacements: runnable}, // recorded kill, stands
		{Symbol: "p.F", Operator: "op-a", Position: "f.go:2:2", Replacements: runnable}, // survivor -> killed by added test
		{Symbol: "p.F", Operator: "op-b", Position: "f.go:3:3", Replacements: runnable}, // survivor -> still surviving
		{Symbol: "p.F", Operator: "op-b", Position: "f.go:4:4", Replacements: runnable}, // recorded discard, stands
	}
	rec := Finding{
		Symbol: "p.F", CandidateCount: 4, Generated: 4, Mutants: 3, Killed: 1, Discarded: 1,
		Operators: []OperatorSummary{
			{Operator: "op-a", Generated: 2, Killed: 1, Survived: 1},
			{Operator: "op-b", Generated: 2, Discarded: 1, Survived: 1},
		},
		Survivors: []Survivor{
			{Position: "f.go:2:2", Operator: "op-a", Execution: "never-executed"},
			{Position: "f.go:3:3", Operator: "op-b", Execution: "executed-and-passed"},
		},
		Attested: []Attestation{
			{Position: "f.go:2:2", Operator: "op-a", Reason: "wrongly judged"},
			{Position: "f.go:3:3", Operator: "op-b", Reason: "still equivalent"},
		},
	}
	survivors := map[int]bool{1: true, 2: true}
	outcomes := []engine.MutantOutcome{0, engine.MutantKilled, engine.MutantSurvived, 0}
	grown, shed, err := growFindingCounts(context.Background(), rec, candidates, survivors, outcomes, nil)
	if err != nil {
		t.Fatal(err)
	}
	if grown.Killed != 2 || grown.Discarded != 1 || grown.Mutants != 3 ||
		grown.Generated != 4 || grown.CandidateCount != 4 {
		t.Fatalf("grown totals = %+v, want the killed survivor rescored", grown)
	}
	wantOperators := []OperatorSummary{
		{Operator: "op-a", Generated: 2, Killed: 2},
		{Operator: "op-b", Generated: 2, Discarded: 1, Survived: 1},
	}
	if !slices.Equal(grown.Operators, wantOperators) {
		t.Fatalf("grown operators = %+v, want %+v", grown.Operators, wantOperators)
	}
	if len(grown.Survivors) != 1 || grown.Survivors[0].Position != "f.go:3:3" || grown.Survivors[0].Execution != "executed-and-passed" {
		t.Fatalf("grown survivors = %+v, want the still-surviving candidate with its recorded bucket", grown.Survivors)
	}
	if len(grown.Attested) != 1 || grown.Attested[0].Position != "f.go:3:3" {
		t.Fatalf("grown attestations = %+v, want only the still-surviving disposition", grown.Attested)
	}
	if len(shed) != 1 || shed[0].Position != "f.go:2:2" || shed[0].Reason != "wrongly judged" {
		t.Fatalf("shed attestations = %+v, want the newly killed disposition", shed)
	}

	// A divergence-stamped record classifies its re-executed survivors
	// unstable rather than carrying buckets measured under a stable run.
	stamped := rec
	stamped.TargetEvidence = SubjectEvidence{RuntimeUnverifiable: true}
	grownStamped, _, err := growFindingCounts(context.Background(), stamped, candidates, survivors, outcomes, nil)
	if err != nil {
		t.Fatal(err)
	}
	if grownStamped.Survivors[0].Execution != "unstable-oracle" {
		t.Fatalf("stamped grown survivor = %+v, want unstable-oracle", grownStamped.Survivors)
	}
}

// TestGrownSurvivorIndexesFallsBackOnMismatch pins the growth serve's
// regeneration bound (REQ-result-stale): the complete count and selection
// length unchanged, identities unique, every recorded survivor re-identified
// and runnable — any mismatch refuses so the whole target re-measures.
func TestGrownSurvivorIndexesFallsBackOnMismatch(t *testing.T) {
	runnable := []engine.Replacement{{File: "a.go", Source: []byte("x")}}
	generation := engine.Generation{
		CandidateCount: 2,
		Candidates: []engine.Candidate{
			{Position: "a.go:1:1", Operator: "op-a", Replacements: runnable},
			{Position: "a.go:2:1", Operator: "op-b", Replacements: runnable},
		},
	}
	rec := Finding{
		CandidateCount: 2, Generated: 2,
		Survivors: []Survivor{{Position: "a.go:2:1", Operator: "op-b"}},
	}
	survivors, ok := grownSurvivorIndexes(generation, rec)
	if !ok || len(survivors) != 1 || !survivors[1] {
		t.Fatalf("matching regeneration = %v %v, want survivor index 1", survivors, ok)
	}
	drifted := generation
	drifted.CandidateCount = 3
	if _, ok := grownSurvivorIndexes(drifted, rec); ok {
		t.Fatal("candidate-count drift accepted")
	}
	shrunk := generation
	shrunk.Candidates = shrunk.Candidates[:1]
	if _, ok := grownSurvivorIndexes(shrunk, rec); ok {
		t.Fatal("selection-length drift accepted")
	}
	duplicated := generation
	duplicated.Candidates = []engine.Candidate{generation.Candidates[1], generation.Candidates[1]}
	if _, ok := grownSurvivorIndexes(duplicated, rec); ok {
		t.Fatal("duplicate identity accepted")
	}
	missing := rec
	missing.Survivors = []Survivor{{Position: "a.go:9:9", Operator: "op-b"}}
	if _, ok := grownSurvivorIndexes(generation, missing); ok {
		t.Fatal("unidentifiable survivor accepted")
	}
	unrunnable := generation
	unrunnable.Candidates = []engine.Candidate{generation.Candidates[0], {Position: "a.go:2:1", Operator: "op-b"}}
	if _, ok := grownSurvivorIndexes(unrunnable, rec); ok {
		t.Fatal("unrunnable survivor accepted")
	}
}

// TestSpliceCountsStampReExecutedSurvivorsUnderUnverifiableEvidence pins the
// divergence-stamp boundary of both splices at the counts layer
// (REQ-exec-survivor-evidence): under an unverifiable spliced record only the
// re-measured survivors classify unstable-oracle; carried survivors keep
// their recorded buckets verbatim.
func TestSpliceCountsStampReExecutedSurvivorsUnderUnverifiableEvidence(t *testing.T) {
	candidates := []engine.Candidate{
		{Position: "f.go:1:1", Operator: "op-a"},
		{Position: "f.go:2:2", Operator: "op-a"},
	}
	stamped := SubjectEvidence{RuntimeUnverifiable: true}

	rec := Finding{
		CandidateCount: 2, Generated: 2, Mutants: 2, TargetEvidence: stamped,
		Operators: []OperatorSummary{{Operator: "op-a", Generated: 2, Survived: 2}},
		Survivors: []Survivor{
			{Position: "f.go:1:1", Operator: "op-a", Execution: "executed-and-passed"},
			{Position: "f.go:2:2", Operator: "op-a", Execution: "executed-and-passed"},
		},
	}
	outcomes := []engine.MutantOutcome{0, engine.MutantSurvived}
	spliced, err := spliceFindingCounts(context.Background(), rec, candidates, map[int]bool{1: true}, outcomes, nil)
	if err != nil {
		t.Fatal(err)
	}
	if spliced.Survivors[0].Execution != "executed-and-passed" || spliced.Survivors[1].Execution != "unstable-oracle" {
		t.Fatalf("spliced survivors = %+v, want the carried bucket kept and the re-executed one unstable", spliced.Survivors)
	}

	capped := Finding{
		CandidateCount: 2, Generated: 1, Mutants: 1, TargetEvidence: stamped,
		Operators: []OperatorSummary{{Operator: "op-a", Generated: 1, Survived: 1}},
		Survivors: []Survivor{{Position: "f.go:1:1", Operator: "op-a", Execution: "never-executed"}},
	}
	extended, err := extendFindingCounts(context.Background(), capped, candidates, 1, outcomes, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if extended.Survivors[0].Execution != "never-executed" || extended.Survivors[1].Execution != "unstable-oracle" {
		t.Fatalf("extended survivors = %+v, want the carried bucket kept and the suffix one unstable", extended.Survivors)
	}
}

// TestSplicedUnionDivergenceIsNonReusable: REQ-result-stale's union-divergence
// bound — a fresh completed union that does not equal the served record's
// persisted union, in manifest, digest, or verifiability, marks the splice
// diverged; only the equal union keeps the serve reusable.
func TestSplicedUnionDivergenceIsNonReusable(t *testing.T) {
	prior := SubjectEvidence{RuntimeInputs: "manifest-a", RuntimeDigest: "digest-a"}
	equal := runtimeinput.State{OK: true, Manifest: "manifest-a", Digest: "digest-a"}
	if splicedUnionDiverged(equal, prior) {
		t.Fatal("equal union reported diverged")
	}
	for name, state := range map[string]runtimeinput.State{
		"manifest":     {OK: true, Manifest: "manifest-b", Digest: "digest-a"},
		"digest":       {OK: true, Manifest: "manifest-a", Digest: "digest-b"},
		"unverifiable": {OK: true, Manifest: "manifest-a", Digest: "digest-a", Unverifiable: true},
	} {
		if !splicedUnionDiverged(state, prior) {
			t.Fatalf("%s divergence reported equal", name)
		}
	}
}

// TestFlaggedCandidateIndexesFallsBackOnMismatch: REQ-result-stale's
// regeneration-mismatch bound — a regeneration that cannot re-identify the
// record's candidates refuses the serve so the target remeasures whole.
func TestFlaggedCandidateIndexesFallsBackOnMismatch(t *testing.T) {
	runnable := []engine.Replacement{{}}
	generation := engine.Generation{
		CandidateCount: 2,
		Candidates: []engine.Candidate{
			{Position: "a.go:1:1", Operator: "return: zero", Replacements: runnable},
			{Position: "a.go:2:1", Operator: "return: zero", Replacements: runnable},
		},
	}
	rec := Finding{
		CandidateCount: 2,
		Generated:      2,
		CandidateEvidence: []CandidateEvidence{
			{Position: "a.go:1:1", Operator: "return: zero", Reason: "panicked", Disposition: "killed"},
		},
	}
	flagged, ok := flaggedCandidateIndexes(generation, rec)
	if !ok || len(flagged) != 1 || !flagged[0] {
		t.Fatalf("matching regeneration = %v %v, want index 0 flagged", flagged, ok)
	}
	drifted := generation
	drifted.CandidateCount = 3
	if _, ok := flaggedCandidateIndexes(drifted, rec); ok {
		t.Fatal("candidate-count drift accepted")
	}
	missing := rec
	missing.CandidateEvidence = []CandidateEvidence{{Position: "a.go:9:9", Operator: "return: zero", Reason: "panicked", Disposition: "killed"}}
	if _, ok := flaggedCandidateIndexes(generation, missing); ok {
		t.Fatal("unidentifiable flagged position accepted")
	}
	shrunk := generation
	shrunk.Candidates = shrunk.Candidates[:1]
	if _, ok := flaggedCandidateIndexes(shrunk, rec); ok {
		t.Fatal("generated-count drift accepted")
	}
	duplicated := generation
	duplicated.Candidates = []engine.Candidate{generation.Candidates[0], generation.Candidates[0]}
	if _, ok := flaggedCandidateIndexes(duplicated, rec); ok {
		t.Fatal("duplicate candidate identity accepted")
	}
	lostSurvivor := rec
	lostSurvivor.Survivors = []Survivor{{Position: "a.go:9:9", Operator: "return: zero"}}
	if _, ok := flaggedCandidateIndexes(generation, lostSurvivor); ok {
		t.Fatal("unidentifiable survivor accepted")
	}
	unrunnable := generation
	unrunnable.Candidates = []engine.Candidate{
		{Position: "a.go:1:1", Operator: "return: zero"},
		generation.Candidates[1],
	}
	if _, ok := flaggedCandidateIndexes(unrunnable, rec); ok {
		t.Fatal("unrunnable flagged candidate accepted")
	}
}

// TestApplySplicedUnionMarksDivergedEvidenceNonReusable: the effect arm of
// REQ-result-stale's union-divergence bound — an equal union leaves the served
// record's evidence untouched, while a diverged union stamps every subject's
// evidence with an explicit unverifiable state so the spliced finding is
// preserved but never reusable.
func TestApplySplicedUnionMarksDivergedEvidenceNonReusable(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/empty\n\ngo 1.26.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "empty.go"), []byte("package empty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "data.txt"), []byte("observed"), 0o644); err != nil {
		t.Fatal(err)
	}
	tree, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	env := os.Environ()
	ctx := context.Background()
	recorded, err := runtimeinput.FromTestLogEnv([]byte("# test log\n"), root, root, env, runtimeinput.WithCompletedProcess("baseline"), runtimeinput.WithBracket(testBracket(t, root)))
	if err != nil {
		t.Fatal(err)
	}
	recordedState, err := runtimeinput.CompletedState(recorded)
	if err != nil {
		t.Fatal(err)
	}
	evidence := SubjectEvidence{Symbol: "example.com/empty.Gone", RuntimeInputs: recordedState.Manifest, RuntimeDigest: recordedState.Digest}
	rec := Finding{TargetEvidence: evidence, OracleEvidence: []SubjectEvidence{evidence}}

	_, same, err := tree.applySplicedUnion(ctx, env, rec, recorded)
	if err != nil {
		t.Fatal(err)
	}
	if same.TargetEvidence.RuntimeUnverifiable || same.OracleEvidence[0].RuntimeUnverifiable {
		t.Fatalf("equal union marked evidence unverifiable: %+v", same.TargetEvidence)
	}
	if same.TargetEvidence.RuntimeInputs != recordedState.Manifest || same.TargetEvidence.RuntimeDigest != recordedState.Digest {
		t.Fatalf("equal union rewrote pinned evidence: %+v", same.TargetEvidence)
	}

	fresh, err := runtimeinput.FromTestLogEnv([]byte("open data.txt\n"), root, root, env, runtimeinput.WithCompletedProcess("baseline"), runtimeinput.WithBracket(testBracket(t, root)))
	if err != nil {
		t.Fatal(err)
	}
	_, marked, err := tree.applySplicedUnion(ctx, env, rec, fresh)
	if err != nil {
		t.Fatal(err)
	}
	if !marked.TargetEvidence.RuntimeUnverifiable || !marked.OracleEvidence[0].RuntimeUnverifiable {
		t.Fatalf("diverged union left evidence reusable: %+v", marked.TargetEvidence)
	}
	if marked.TargetEvidence.RuntimeReason == "" {
		t.Fatal("diverged union carries no reason")
	}
}

// TestRunCancellationKeepsCommittedFindings pins the incremental commit
// boundary (REQ-exec-cancellation): every finished target's finding — a
// measured one after its post-execution validation, a cached serve once its
// pins are proven — is delivered to Options.Commit and persisted under the
// document lock, so a run cancelled after the first target finished keeps
// that finding while the unfinished target leaves nothing.
func TestRunCancellationKeepsCommittedFindings(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	ctx := context.Background()
	add := Target{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestAdd"}}
	weak := Target{Symbol: "example.com/fixture/lib.Weak", Oracle: []string{"example.com/fixture/lib.TestWeak"}}

	var committed []Finding
	first, err := tr.Run(ctx, []Target{add}, Options{Budget: 1, Commit: func(f Finding) error {
		committed = append(committed, f)
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(committed) != 1 || committed[0].Symbol != add.Symbol || committed[0].Cached ||
		committed[0].Mutants != first[0].Mutants || committed[0].Killed != first[0].Killed {
		t.Fatalf("measured-target commits = %+v, want the finished finding once", committed)
	}
	doc, err := Export(first)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := ParseFindings(doc)
	if err != nil {
		t.Fatal(err)
	}

	// Two targets, the first served from cache: its finding commits before
	// the ordered decisions are delivered, the Decision callback cancels, and
	// the run aborts before the second target measures.
	docPath := filepath.Join(t.TempDir(), "findings.json")
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	committed = nil
	findings, err := tr.Run(runCtx, []Target{add, weak}, Options{
		Budget: 1, Prior: prior,
		Decision: func(RunDecision) { cancel() },
		Commit: func(f Finding) error {
			committed = append(committed, f)
			return UpdateDocumentContext(ctx, docPath, func(current []Finding) ([]Finding, error) {
				return MergeFindings(current, []Finding{f}), nil
			})
		},
	})
	if !errors.Is(err, context.Canceled) || findings != nil {
		t.Fatalf("cancelled run = findings %v, error %v", findings, err)
	}
	if len(committed) != 1 || committed[0].Symbol != add.Symbol || !committed[0].Cached {
		t.Fatalf("commits before cancellation = %+v, want the cached serve alone", committed)
	}
	data, err := os.ReadFile(docPath)
	if err != nil {
		t.Fatal(err)
	}
	persisted, err := ParseFindings(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 1 || persisted[0].Symbol != add.Symbol {
		t.Fatalf("persisted findings after cancellation = %+v, want the finished target alone", persisted)
	}
	for _, finding := range persisted {
		if finding.Symbol == weak.Symbol {
			t.Fatal("an unfinished target was persisted")
		}
	}
}

// TestCommitFindingRefusesMovedHead pins the incremental-commit HEAD guard
// (REQ-exec-cancellation): a finding commits only while the capture commit
// still names repository HEAD, mirroring the run's final check.
func TestCommitFindingRefusesMovedHead(t *testing.T) {
	root := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=gomutant", "GIT_AUTHOR_EMAIL=gomutant@example.invalid",
			"GIT_COMMITTER_NAME=gomutant", "GIT_COMMITTER_EMAIL=gomutant@example.invalid",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	path := filepath.Join(root, "file.txt")
	if err := os.WriteFile(path, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("init", "-q")
	runGit("add", "file.txt")
	runGit("commit", "-q", "-m", "one")
	repository, err := captureRepositoryStateContext(context.Background(), root)
	if err != nil || !repository.available {
		t.Fatalf("repository state = %+v, %v", repository, err)
	}
	calls := 0
	commit := func(Finding) error { calls++; return nil }
	if err := commitFinding(context.Background(), repository, commit, Finding{Symbol: "p.F"}); err != nil || calls != 1 {
		t.Fatalf("commit at unmoved HEAD = %v, calls %d", err, calls)
	}
	if err := os.WriteFile(path, []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "file.txt")
	runGit("commit", "-q", "-m", "two")
	if err := commitFinding(context.Background(), repository, commit, Finding{Symbol: "p.F"}); err == nil || !strings.Contains(err.Error(), "HEAD moved") || calls != 1 {
		t.Fatalf("commit past moved HEAD = %v, calls %d, want a refusal without delivery", err, calls)
	}
	if err := commitFinding(context.Background(), repository, nil, Finding{}); err != nil {
		t.Fatalf("nil commit callback = %v", err)
	}
}

// TestMergeFindingsGraftsConcurrentAttestation: replacement never sheds a
// disposition the document holds for a survivor the replacement still reports
// — an attestation added between a run's snapshot or incremental commit and
// its merge rides survivor identity onto the fresh record; a survivor absent
// from the fresh record still sheds its attestation.
func TestMergeFindingsGraftsConcurrentAttestation(t *testing.T) {
	prior := Finding{Symbol: "p.F",
		Survivors: []Survivor{{Position: "f.go:1:1", Operator: "op"}},
		Attested:  []Attestation{{Position: "f.go:1:1", Operator: "op", Reason: "equivalent"}}}
	fresh := Finding{Symbol: "p.F",
		Survivors: []Survivor{{Position: "f.go:1:1", Operator: "op"}, {Position: "f.go:2:2", Operator: "op"}}}
	merged := MergeFindings([]Finding{prior}, []Finding{fresh})
	if len(merged) != 1 || len(merged[0].Attested) != 1 || merged[0].Attested[0].Reason != "equivalent" {
		t.Fatalf("concurrent attestation clobbered: %+v", merged[0].Attested)
	}
	shed := Finding{Symbol: "p.F", Survivors: []Survivor{{Position: "f.go:2:2", Operator: "op"}}}
	merged = MergeFindings([]Finding{prior}, []Finding{shed})
	if len(merged[0].Attested) != 0 {
		t.Fatalf("dead survivor's attestation retained: %+v", merged[0].Attested)
	}
	kept := Finding{Symbol: "p.F",
		Survivors: []Survivor{{Position: "f.go:1:1", Operator: "op"}},
		Attested:  []Attestation{{Position: "f.go:1:1", Operator: "op", Reason: "fresher"}}}
	merged = MergeFindings([]Finding{prior}, []Finding{kept})
	if len(merged[0].Attested) != 1 || merged[0].Attested[0].Reason != "fresher" {
		t.Fatalf("fresh attestation not preferred: %+v", merged[0].Attested)
	}
}

// TestRunReportsAnalysisProgress: the advisory freshness-analysis keep-alive
// events reach Options.AnalysisProgress, with the view-observation phase
// present — the wiring an MCP server forwards as progress notifications.
func TestRunReportsAnalysisProgress(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	var mu sync.Mutex
	phases := map[string]int{}
	target := Target{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestAdd"}}
	if _, err := tr.Run(context.Background(), []Target{target}, Options{AnalysisProgress: func(phase, pkg string) {
		mu.Lock()
		phases[phase]++
		mu.Unlock()
	}}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if phases["observe"] == 0 {
		t.Fatalf("analysis progress phases = %v, want view observations reported", phases)
	}
}

// A caller-declared bracket path extends observation coverage to an
// external fixed input the oracle legitimately reads: without the
// declaration the absolute out-of-module read seals the evidence,
// with it the value binds and the finding stays verifiable
// (REQ-exec-observation).
func TestRunCallerDeclaredBracketPathBindsExternalInput(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	external := t.TempDir()
	fixture := filepath.Join(external, "fixture.txt")
	if err := os.WriteFile(fixture, []byte("stable"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOMUTANT_EXTERNAL_FIXTURE", fixture)
	tr := fixtureTree(t)
	target := Target{Symbol: "example.com/fixture/extinput.Flag", Oracle: []string{"example.com/fixture/extinput.TestFlag"}}

	sealed, err := tr.Run(context.Background(), []Target{target}, Options{Budget: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(sealed) != 1 || !sealed[0].TargetEvidence.RuntimeUnverifiable {
		t.Fatalf("undeclared external read = %+v, want sealed unverifiable evidence", sealed[0].TargetEvidence)
	}

	tr2 := fixtureTree(t)
	bound, err := tr2.Run(context.Background(), []Target{target}, Options{Budget: 1, BracketPaths: []string{fixture}})
	if err != nil {
		t.Fatal(err)
	}
	if len(bound) != 1 || bound[0].TargetEvidence.RuntimeUnverifiable {
		t.Fatalf("declared external read = %+v (%s), want bound verifiable evidence", bound[0].TargetEvidence, bound[0].TargetEvidence.RuntimeReason)
	}
}

// Bracket-path declarations the bracket cannot honor refuse loudly at
// run start: an absolute external directory would seal every
// observation, and a tool-excluded path would be silently uncovered
// (REQ-exec-observation).
func TestRunRefusesUnhonorableBracketPaths(t *testing.T) {
	tr := fixtureTree(t)
	target := Target{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestAdd"}}
	if _, err := tr.Run(context.Background(), []Target{target}, Options{BracketPaths: []string{t.TempDir()}}); err == nil ||
		!strings.Contains(err.Error(), "absolute directory the observation bracket cannot walk") {
		t.Fatalf("absolute-directory declaration = %v, want a loud refusal", err)
	}
	if _, err := tr.Run(context.Background(), []Target{target}, Options{BracketPaths: []string{".gomutant/targets.json"}}); err == nil ||
		!strings.Contains(err.Error(), "tool-excluded") {
		t.Fatalf("tool-excluded declaration = %v, want a loud refusal", err)
	}
}

// A package-derived oracle whose merged evidence lands unverifiable is
// attributed test by test: the unstable test is named with a narrowing
// suggestion listing the stable remainder (REQ-exec-oracle-guidance).
func TestRunAttributesOracleInstability(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	var guidance []OracleGuidance
	fs, err := tr.Run(context.Background(), []Target{{Symbol: "example.com/fixture/unstableoracle.Value"}}, Options{
		Budget:   1,
		Guidance: func(g OracleGuidance) { guidance = append(guidance, g) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 || !fs[0].TargetEvidence.RuntimeUnverifiable {
		t.Fatalf("unstable-oracle finding = %+v, want unverifiable union evidence", fs[0].TargetEvidence)
	}
	if len(guidance) != 1 {
		t.Fatalf("guidance = %+v, want one attribution", guidance)
	}
	g := guidance[0]
	if g.Symbol != "example.com/fixture/unstableoracle.Value" || g.Reason == "" {
		t.Fatalf("guidance identity = %+v", g)
	}
	if len(g.UnstableTests) != 1 || g.UnstableTests[0] != "example.com/fixture/unstableoracle.TestUnstable" {
		t.Fatalf("unstable attribution = %+v, want exactly TestUnstable", g.UnstableTests)
	}
	if !strings.Contains(g.Suggestion, "excluding example.com/fixture/unstableoracle.TestUnstable") ||
		!strings.Contains(g.Suggestion, "stable oracle: example.com/fixture/unstableoracle.TestStable") {
		t.Fatalf("suggestion = %q", g.Suggestion)
	}

	// An explicit oracle gets no attribution: the caller already chose
	// the tests (REQ-exec-oracle-guidance).
	guidance = nil
	explicit, err := tr.Run(context.Background(), []Target{{
		Symbol: "example.com/fixture/unstableoracle.Value",
		Oracle: []string{"example.com/fixture/unstableoracle.TestUnstable"},
	}}, Options{Budget: 1, Guidance: func(g OracleGuidance) { guidance = append(guidance, g) }})
	if err != nil {
		t.Fatal(err)
	}
	if !explicit[0].TargetEvidence.RuntimeUnverifiable || len(guidance) != 0 {
		t.Fatalf("explicit-oracle run = unverifiable %v, guidance %+v; want unverifiable with no attribution", explicit[0].TargetEvidence.RuntimeUnverifiable, guidance)
	}
}

// Survivor execution evidence buckets why each survivor lived
// (REQ-exec-survivor-evidence): a coverage-gap survivor reads
// never-executed, a survivor the oracle runs through and passes reads
// executed-and-passed, and unverifiable runtime evidence buckets every
// survivor unstable-oracle without probing.
func TestRunBucketsSurvivorExecution(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)

	weak, err := tr.Run(context.Background(), []Target{{Symbol: "example.com/fixture/lib.Weak", Oracle: []string{"example.com/fixture/lib.TestWeak"}}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	buckets := map[string]int{}
	for _, s := range weak[0].Survivors {
		buckets[s.Execution]++
	}
	if buckets["never-executed"] == 0 {
		t.Fatalf("Weak survivors = %+v; want the untested branch bucketed never-executed", weak[0].Survivors)
	}
	if buckets[""] != 0 {
		t.Fatalf("Weak survivors carry empty buckets: %+v", weak[0].Survivors)
	}

	add, err := tr.Run(context.Background(), []Target{{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestAdd"}}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	executed := 0
	for _, s := range add[0].Survivors {
		if s.Execution == "executed-and-passed" {
			executed++
		}
	}
	if executed == 0 {
		t.Fatalf("Add survivors = %+v; want covered survivors bucketed executed-and-passed", add[0].Survivors)
	}

	unstable, err := tr.Run(context.Background(), []Target{{Symbol: "example.com/fixture/unstableoracle.Weakly"}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !unstable[0].TargetEvidence.RuntimeUnverifiable || len(unstable[0].Survivors) == 0 {
		t.Fatalf("unstable-oracle run = evidence %+v, survivors %d; fixture assumption broken", unstable[0].TargetEvidence, len(unstable[0].Survivors))
	}
	for _, s := range unstable[0].Survivors {
		if s.Execution != "unstable-oracle" {
			t.Fatalf("unstable-oracle survivor bucket = %+v", unstable[0].Survivors)
		}
	}
}

// The run's stale-reason enrichment reuses the run's own subject views: when
// the record's oracle matches the target's, attribution builds no second
// view; a recorded oracle the current target no longer names builds exactly
// one supplementary view for the difference (REQ-result-stale's naming arm).
func TestRunStaleReasonReusesTheRunsViews(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	ctx := context.Background()
	target := Target{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestAdd"}}
	first, err := tr.Run(ctx, []Target{target}, Options{Budget: 1})
	if err != nil {
		t.Fatal(err)
	}

	var supplementary [][]string
	inspectionSupplementaryViewHook = func(symbols []string) {
		supplementary = append(supplementary, append([]string(nil), symbols...))
	}
	defer func() { inspectionSupplementaryViewHook = nil }()

	tampered := append([]Finding(nil), first...)
	tampered[0].TargetEvidence.MaximalClosure = "not-the-current-closure"
	var decisions []RunDecision
	if _, err := tr.Run(ctx, []Target{target}, Options{Budget: 1, Prior: tampered, Decision: func(d RunDecision) {
		decisions = append(decisions, d)
	}}); err != nil {
		t.Fatal(err)
	}
	if len(supplementary) != 0 {
		t.Fatalf("stale-reason attribution built supplementary views for run-covered symbols: %v", supplementary)
	}
	if len(decisions) != 1 || !strings.HasPrefix(decisions[0].Reason, "stale: ") || !strings.Contains(decisions[0].Reason, "target") {
		t.Fatalf("decisions = %+v; want the moved pin still attributed through the run's views", decisions)
	}

	// A recorded oracle outside the current target's oracle set builds one
	// supplementary view for exactly the uncovered symbol.
	foreign := append([]Finding(nil), first...)
	extra := foreign[0].OracleEvidence[0]
	extra.Symbol = "example.com/fixture/plain.TestPlain"
	foreign[0].OracleEvidence = append(append([]SubjectEvidence(nil), foreign[0].OracleEvidence...), extra)
	supplementary = nil
	decisions = nil
	if _, err := tr.Run(ctx, []Target{target}, Options{Budget: 1, Prior: foreign, Decision: func(d RunDecision) {
		decisions = append(decisions, d)
	}}); err != nil {
		t.Fatal(err)
	}
	if len(supplementary) != 1 || len(supplementary[0]) != 1 || supplementary[0][0] != "example.com/fixture/plain.TestPlain" {
		t.Fatalf("supplementary views = %v; want exactly the record-only oracle symbol", supplementary)
	}
	if len(decisions) != 1 || decisions[0].Action != "measure" || !strings.Contains(decisions[0].Reason, "oracle example.com/fixture/plain.TestPlain") {
		t.Fatalf("decisions = %+v; want a re-measure whose reason names the record-only oracle through the supplementary view", decisions)
	}
}

// A per-target freshness-proof failure is target-local (the exact rule
// drift refusal follows, REQ-exec-quiescence): a broken package faults
// its module group out of the shared union pass instead of failing the
// campaign, every faulted target funnels into its own bounded retry,
// and the target whose breakage persists skips with the cause on its
// decision line while a recovered sibling measures — one target's
// broken evidence never exits the run.
func TestFreshnessProofFailureSkipsTargetLocally(t *testing.T) {
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
	targets := []Target{
		{Symbol: "example.com/fixture/plain.Ok", Oracle: []string{"example.com/fixture/plain.TestPlain"}},
		{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestAdd"}},
	}
	libPath := filepath.Join(tmp, "lib", "lib.go")
	libSource, err := os.ReadFile(libPath)
	if err != nil {
		t.Fatal(err)
	}
	var decisions []RunDecision
	var attempts []string
	findings, err := tr.Run(context.Background(), targets, Options{
		Budget: 1,
		// Decisions emit after the prepare loop: restoring here puts the
		// byte-identical tree back before measurement and revalidation,
		// so the induced failure stays scoped to proof construction.
		Decision: func(d RunDecision) {
			decisions = append(decisions, d)
			if d.Symbol == targets[1].Symbol {
				if wErr := os.WriteFile(libPath, libSource, 0o644); wErr != nil {
					t.Fatal(wErr)
				}
			}
		},
		proofAttempt: func(symbol string, attempt int) {
			attempts = append(attempts, fmt.Sprintf("%s@%d", symbol, attempt))
			switch {
			case symbol == "" && attempt == 1:
				// Break one package before the union pass: the fixture
				// is one module, so the whole group faults and both
				// targets funnel into their bounded retries.
				if rmErr := os.Remove(libPath); rmErr != nil {
					t.Fatal(rmErr)
				}
			case symbol == targets[0].Symbol && attempt == 2:
				// The healthy target's retry finds the transient gone...
				if wErr := os.WriteFile(libPath, libSource, 0o644); wErr != nil {
					t.Fatal(wErr)
				}
			case symbol == targets[1].Symbol && attempt == 2:
				// ...and the broken target's breakage persists through
				// its retry.
				if rmErr := os.Remove(libPath); rmErr != nil {
					t.Fatal(rmErr)
				}
			}
		},
	})
	if err != nil {
		t.Fatalf("one target's proof failure escalated to a campaign abort: %v", err)
	}
	wantAttempts := []string{"@1", targets[0].Symbol + "@2", targets[1].Symbol + "@2"}
	if !slices.Equal(attempts, wantAttempts) {
		t.Fatalf("proof attempts = %v, want %v (union, then each faulted target's retry)", attempts, wantAttempts)
	}
	bySym := map[string]Finding{}
	for _, f := range findings {
		bySym[f.Symbol] = f
	}
	if skipped := bySym[targets[1].Symbol].Skipped; !strings.Contains(skipped, "freshness proof unavailable") {
		t.Fatalf("broken target's skip = %q, want the proof-unavailable cause", skipped)
	}
	if healthy := bySym[targets[0].Symbol]; healthy.Skipped != "" || healthy.Generated == 0 {
		t.Fatalf("sibling target did not measure: %+v", healthy)
	}
	var skipDecision *RunDecision
	for i := range decisions {
		if decisions[i].Symbol == targets[1].Symbol {
			skipDecision = &decisions[i]
		}
	}
	if skipDecision == nil || skipDecision.Action != "skipped" || !strings.Contains(skipDecision.Reason, "freshness proof unavailable") {
		t.Fatalf("skip decision = %+v, want the per-target cause line", skipDecision)
	}
}

// The proof site retries once before degrading: a transiently failing
// construction (the field mode - momentary load) measures on the second
// attempt instead of skipping.
func TestFreshnessProofRetriesOnceBeforeSkipping(t *testing.T) {
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
	target := Target{Symbol: "example.com/fixture/plain.Ok", Oracle: []string{"example.com/fixture/plain.TestPlain"}}
	plainPath := filepath.Join(tmp, "plain", "plain.go")
	plainSource, err := os.ReadFile(plainPath)
	if err != nil {
		t.Fatal(err)
	}
	var attempts []int
	findings, err := tr.Run(context.Background(), []Target{target}, Options{
		Budget: 1,
		proofAttempt: func(symbol string, attempt int) {
			attempts = append(attempts, attempt)
			switch attempt {
			case 1:
				if rmErr := os.Remove(plainPath); rmErr != nil {
					t.Fatal(rmErr)
				}
			case 2:
				if wErr := os.WriteFile(plainPath, plainSource, 0o644); wErr != nil {
					t.Fatal(wErr)
				}
			}
		},
	})
	if err != nil {
		t.Fatalf("transient proof failure aborted the run: %v", err)
	}
	if !slices.Equal(attempts, []int{1, 2}) {
		t.Fatalf("proof attempts = %v, want [1 2]", attempts)
	}
	if len(findings) != 1 || findings[0].Skipped != "" || findings[0].Generated == 0 {
		t.Fatalf("retry did not recover the target: %+v", findings)
	}
}

// Cancellation of the campaign itself during proof construction stays
// an abort with a legible name (REQ-exec-quiescence's legibility arm;
// the skip degrade is only for target-local conditions under a live
// campaign): the shared union pass names the union and its subject
// count — every subject is in flight — and a faulted target's bounded
// retry names the exact subject whose view was being built.
func TestFreshnessProofCancellationAbortsWithNamedTarget(t *testing.T) {
	if testing.Short() {
		t.Skip("builds producer views")
	}
	target := Target{Symbol: "example.com/fixture/plain.Ok", Oracle: []string{"example.com/fixture/plain.TestPlain"}}

	// Cancellation at the union pass names the union.
	tr := fixtureTree(t)
	ctx, cancel := context.WithCancel(context.Background())
	findings, err := tr.Run(ctx, []Target{target}, Options{
		Budget:       1,
		proofAttempt: func(string, int) { cancel() },
	})
	if err == nil || findings != nil {
		t.Fatalf("canceled campaign completed: findings %v, err %v", findings, err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error class lost: %v", err)
	}
	if !strings.Contains(err.Error(), "freshness proofs (union over") {
		t.Fatalf("cancellation during the union pass lost its union-naming wrap: %v", err)
	}

	// Cancellation inside a faulted target's retry names the target.
	tmp := t.TempDir()
	if err := os.CopyFS(tmp, os.DirFS(fixtureDir)); err != nil {
		t.Fatal(err)
	}
	retryTree, err := Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	plainPath := filepath.Join(tmp, "plain", "plain.go")
	retryCtx, retryCancel := context.WithCancel(context.Background())
	defer retryCancel()
	findings, err = retryTree.Run(retryCtx, []Target{target}, Options{
		Budget: 1,
		proofAttempt: func(symbol string, attempt int) {
			if attempt == 1 {
				// Fault the union so the bounded retry runs at all.
				if rmErr := os.Remove(plainPath); rmErr != nil {
					t.Fatal(rmErr)
				}
				return
			}
			retryCancel()
		},
	})
	if err == nil || findings != nil {
		t.Fatalf("canceled campaign completed: findings %v, err %v", findings, err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error class lost: %v", err)
	}
	if !strings.Contains(err.Error(), "freshness proof for target "+target.Symbol) {
		t.Fatalf("cancellation during the retry lost its target-naming wrap: %v", err)
	}
}

// A skipped target's modules never enroll in end-of-run producer
// validation: in a workspace, one member's persistent breakage skips
// exactly its own target and the sibling member's campaign completes —
// no campaign-level drift report for a target-local condition
// (REQ-exec-quiescence).
func TestFreshnessProofSkipDoesNotEnrollItsModules(t *testing.T) {
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
	targets := []Target{
		{Symbol: "example.com/ws/sub.Nested", Oracle: []string{"example.com/ws/sub.TestNested"}},
		{Symbol: "example.com/ws.Root", Oracle: []string{"example.com/ws.TestRoot"}},
	}
	findings, err := tr.Run(context.Background(), targets, Options{
		Budget: 1,
		// Break the SECOND target's module before the union pass — and
		// never restore: its own module group faults while the sibling
		// member's group builds, and with the broken module never
		// enrolled, final validation covers only the measured member.
		proofAttempt: func(symbol string, attempt int) {
			if symbol == "" && attempt == 1 {
				if rmErr := os.Remove(filepath.Join(tmp, "root.go")); rmErr != nil {
					t.Fatal(rmErr)
				}
			}
		},
	})
	if err != nil {
		t.Fatalf("skipped member's persistent breakage escalated to a campaign error: %v", err)
	}
	bySym := map[string]Finding{}
	for _, f := range findings {
		bySym[f.Symbol] = f
	}
	if skipped := bySym[targets[1].Symbol].Skipped; !strings.Contains(skipped, "freshness proof unavailable") {
		t.Fatalf("broken member's skip = %q", skipped)
	}
	if healthy := bySym[targets[0].Symbol]; healthy.Skipped != "" || healthy.Generated == 0 {
		t.Fatalf("sibling member did not measure: %+v", healthy)
	}
}

// The strict view build refuses an unresolvable symbol with an error —
// never a silently smaller set: every strict caller indexes the result
// by the symbols it asked for, so a dropped symbol would surface as a
// nil dereference far from its cause. (The union build is the one
// tolerant path; its faults are recorded per symbol and re-checked at
// narrowing time.)
func TestStrictViewBuildRefusesUnresolvableSymbol(t *testing.T) {
	if testing.Short() {
		t.Skip("builds views")
	}
	tr := fixtureTree(t)
	if _, err := tr.newSubjectViews(context.Background(), []string{"example.com/fixture/nosuchpackage.F"}); err == nil {
		t.Fatal("strict view build tolerated an unresolvable symbol, want refusal")
	}
}

// A canceled campaign's proof abort keeps both its legibility wrap and
// the cancellation class even when the underlying error is a stored
// union fault predating the cancellation (the bounded retry is skipped
// on a canceled campaign, so the fault alone carries no cancellation).
// Only an asynchronous cancellation landing between one target's
// context checks reaches that shape — no test seam sits inside the
// window — so the construction is pinned directly.
func TestProofAbortErrorKeepsCancellationClass(t *testing.T) {
	staleFault := errors.New("go list: exit status 1")
	err := proofAbortError("example.com/fixture/lib.Add", []string{"example.com/fixture/lib.TestAdd"}, staleFault, context.Canceled)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("stored-fault abort lost the cancellation class: %v", err)
	}
	if !errors.Is(err, staleFault) {
		t.Fatalf("abort lost the stored fault: %v", err)
	}
	if !strings.Contains(err.Error(), "freshness proof for target example.com/fixture/lib.Add (oracle example.com/fixture/lib.TestAdd)") {
		t.Fatalf("abort lost its target-naming wrap: %v", err)
	}
}

// TestManifestInternerSharesIdenticalManifestsAndPreservesObservations pins the
// retention fix: two observations of the same content intern to one backing
// manifest string, a distinct observation keeps its own, and interning never
// alters an observation's state, processes, or merge behavior.
func TestManifestInternerSharesIdenticalManifestsAndPreservesObservations(t *testing.T) {
	root := t.TempDir()
	env := os.Environ()
	first, err := runtimeinput.FromTestLogEnv([]byte("# test log\n"), root, root, env, runtimeinput.WithCompletedProcess("first"), runtimeinput.WithBracket(testBracket(t, root)))
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtimeinput.FromTestLogEnv([]byte("# test log\n"), root, root, env, runtimeinput.WithCompletedProcess("second"), runtimeinput.WithBracket(testBracket(t, root)))
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest != second.Digest || first.Manifest != second.Manifest {
		t.Fatalf("fixture expectation: identical observations, got digests %q vs %q", first.Digest, second.Digest)
	}
	in := &manifestInterner{byDigest: map[string]string{}}
	internedFirst := in.intern(first)
	internedSecond := in.intern(second)
	if internedFirst.State != first.State || internedSecond.State != second.State {
		t.Fatalf("interning altered a state: %+v vs %+v", internedFirst.State, first.State)
	}
	fp := unsafe.StringData(internedFirst.Manifest)
	sp := unsafe.StringData(internedSecond.Manifest)
	if fp != sp {
		t.Fatal("identical manifests were not shared after interning")
	}
	// A distinct manifest keeps its own backing and its own content.
	incomplete, err := runtimeinput.IncompleteEnv(root, "third", "distinct content", env)
	if err != nil {
		t.Fatal(err)
	}
	internedThird := in.intern(incomplete)
	if internedThird.State != incomplete.State {
		t.Fatalf("interning altered the distinct state: %+v vs %+v", internedThird.State, incomplete.State)
	}
	// The interned observations still merge exactly as the originals do.
	fromInterned, err := mergeFindingObservations(root, env, internedFirst, internedSecond)
	if err != nil {
		t.Fatal(err)
	}
	fromOriginals, err := mergeFindingObservations(root, env, first, second)
	if err != nil {
		t.Fatal(err)
	}
	mergedInterned, err := runtimeinput.CompletedState(fromInterned)
	if err != nil {
		t.Fatal(err)
	}
	mergedOriginal, err := runtimeinput.CompletedState(fromOriginals)
	if err != nil {
		t.Fatal(err)
	}
	if mergedInterned.Digest != mergedOriginal.Digest || mergedInterned.Manifest != mergedOriginal.Manifest {
		t.Fatal("interned observations merged to a different union")
	}
	// A zero observation passes through untouched.
	if got := in.intern(runtimeinput.Observation{}); got.State != (runtimeinput.State{}) {
		t.Fatalf("zero observation changed by interning: %+v", got.State)
	}
}

// TestRunExtendsCappedFindingMeasuringOnlyTheSuffix pins the budget-extension
// resume (REQ-mut-budget, REQ-result-stale's budget-extension carve-out): a
// wider request against a capped record whose every other pin holds measures
// only the unmeasured candidate suffix, splices the merged truth, carries the
// prefix survivor's attestation verbatim, round-trips the document, still
// serves a narrower request without measurement, and extends again to the
// exhaustive set under budget zero — while force, a moved non-budget pin, and
// prior candidate evidence each re-measure the whole target with the refusal
// named on the decision.
func TestRunExtendsCappedFindingMeasuringOnlyTheSuffix(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	ctx := context.Background()
	target := Target{Symbol: "example.com/fixture/lib.Weak", Oracle: []string{"example.com/fixture/lib.TestWeak"}}

	capped, err := tr.Run(ctx, []Target{target}, Options{Budget: 1})
	if err != nil {
		t.Fatal(err)
	}
	f := capped[0]
	if f.Generated != 1 || len(f.Survivors) != 1 || len(f.CandidateEvidence) != 0 || f.CandidateCount <= 3 {
		t.Fatalf("budget-1 fixture = %+v, want one measured survivor, no candidate evidence, and more candidates available", f)
	}
	prefixSurvivor := f.Survivors[0]
	if err := capped[0].Attest(prefixSurvivor.Position, prefixSurvivor.Operator, "equivalent by inspection"); err != nil {
		t.Fatal(err)
	}
	doc, err := Export(capped)
	if err != nil {
		t.Fatal(err)
	}
	prior, err := ParseFindings(doc)
	if err != nil {
		t.Fatal(err)
	}

	collect := func(opts Options) ([]Finding, []RunDecision, []int) {
		t.Helper()
		var decisions []RunDecision
		var dispatched []int
		opts.Decision = func(decision RunDecision) { decisions = append(decisions, decision) }
		opts.dispatched = func(_ string, mi int) { dispatched = append(dispatched, mi) }
		findings, err := tr.Run(ctx, []Target{target}, opts)
		if err != nil {
			t.Fatal(err)
		}
		sort.Ints(dispatched)
		return findings, decisions, dispatched
	}

	extendedFindings, decisions, dispatched := collect(Options{Budget: 3, Prior: prior})
	want := RunDecision{Symbol: target.Symbol, Action: "measure", Reason: "served: prefix of 1 candidate stands; measuring 2 more", Candidates: 2}
	if len(decisions) != 1 || decisions[0] != want {
		t.Fatalf("extension decision = %+v, want %+v", decisions, want)
	}
	if !slices.Equal(dispatched, []int{1, 2}) {
		t.Fatalf("dispatched candidate indexes = %v, want exactly the suffix [1 2]", dispatched)
	}
	extended := extendedFindings[0]
	if extended.Cached {
		t.Fatal("an extended finding reported itself cached")
	}
	if extended.Budget != 3 || extended.Generated != 3 || extended.CandidateCount != f.CandidateCount {
		t.Fatalf("extended pins = %+v, want budget 3, generated 3, candidate count conserved", extended)
	}
	if extended.Generated != extended.Mutants+extended.Discarded || extended.Mutants != extended.Killed+len(extended.Survivors) {
		t.Fatalf("extended totals do not conserve: %+v", extended)
	}
	generated, discarded, killed, survived := 0, 0, 0, 0
	for _, summary := range extended.Operators {
		if summary.Generated != summary.Discarded+summary.Killed+summary.Survived {
			t.Fatalf("operator summary does not conserve: %+v", summary)
		}
		generated += summary.Generated
		discarded += summary.Discarded
		killed += summary.Killed
		survived += summary.Survived
	}
	if generated != extended.Generated || discarded != extended.Discarded || killed != extended.Killed || survived != len(extended.Survivors) {
		t.Fatalf("operator totals do not reconcile: %+v", extended.Operators)
	}
	if len(extended.Survivors) == 0 || extended.Survivors[0].Position != prefixSurvivor.Position || extended.Survivors[0].Operator != prefixSurvivor.Operator {
		t.Fatalf("extended survivors = %+v, want the recorded prefix survivor carried first", extended.Survivors)
	}
	// Advisory buckets: the carried prefix survivor keeps its recorded
	// bucket verbatim; suffix survivors earn fresh probed buckets
	// (REQ-exec-survivor-evidence).
	if prefixSurvivor.Execution == "" || extended.Survivors[0].Execution != prefixSurvivor.Execution {
		t.Fatalf("prefix survivor bucket = %q, want the recorded %q carried verbatim", extended.Survivors[0].Execution, prefixSurvivor.Execution)
	}
	for _, survivor := range extended.Survivors[1:] {
		if survivor.Execution == "" {
			t.Fatalf("suffix survivor unbucketed after a verifiable extension: %+v", extended.Survivors)
		}
	}
	wantAttestation := Attestation{Position: prefixSurvivor.Position, Operator: prefixSurvivor.Operator, Reason: "equivalent by inspection"}
	if len(extended.Attested) != 1 || extended.Attested[0] != wantAttestation {
		t.Fatalf("extended attestations = %+v, want the prefix survivor's disposition carried verbatim", extended.Attested)
	}
	if extended.TargetEvidence.RuntimeUnverifiable ||
		extended.TargetEvidence.RuntimeInputs != f.TargetEvidence.RuntimeInputs ||
		extended.TargetEvidence.RuntimeDigest != f.TargetEvidence.RuntimeDigest {
		t.Fatalf("extended evidence = %+v, want the served union untouched when the suffix read only recorded pins", extended.TargetEvidence)
	}

	// The merged record round-trips the versioned document unchanged.
	extendedDoc, err := Export(extendedFindings)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseFindings(extendedDoc)
	if err != nil {
		t.Fatal(err)
	}
	roundTripped := parsed[0]
	roundTripped.Cached = extended.Cached
	if len(parsed) != 1 || !reflect.DeepEqual(roundTripped, extended) {
		t.Fatalf("round-tripped finding = %+v, want %+v", parsed[0], extended)
	}

	// A narrower request is covered by the extended record and serves without
	// measurement (REQ-result-stale).
	narrower, narrowerDecisions, narrowerDispatched := collect(Options{Budget: 1, Prior: parsed})
	if !narrower[0].Cached || len(narrowerDispatched) != 0 || len(narrowerDecisions) != 1 || narrowerDecisions[0].Action != "cached" {
		t.Fatalf("budget shrink = %+v, decisions %+v, dispatched %v; want a measurement-free serve", narrower[0], narrowerDecisions, narrowerDispatched)
	}

	// The exhaustive request (budget zero) extends the extended record again,
	// measuring only the remaining candidates; pre-execution discards are
	// never dispatched, so the suffix bound is what pins the skip.
	exhaustive, exhaustiveDecisions, exhaustiveDispatched := collect(Options{Prior: parsed})
	wantExhaustive := RunDecision{Symbol: target.Symbol, Action: "measure",
		Reason:     fmt.Sprintf("served: prefix of 3 candidates stands; measuring %d more", extended.CandidateCount-3),
		Candidates: extended.CandidateCount - 3}
	if len(exhaustiveDecisions) != 1 || exhaustiveDecisions[0] != wantExhaustive {
		t.Fatalf("exhaustive extension decision = %+v, want %+v", exhaustiveDecisions, wantExhaustive)
	}
	for _, mi := range exhaustiveDispatched {
		if mi < 3 {
			t.Fatalf("exhaustive extension re-executed prefix candidate %d: %v", mi, exhaustiveDispatched)
		}
	}
	full := exhaustive[0]
	if full.Budget != 0 || full.Generated != full.CandidateCount || full.CandidateCount != extended.CandidateCount ||
		full.Generated != full.Mutants+full.Discarded || full.Mutants != full.Killed+len(full.Survivors) {
		t.Fatalf("exhaustive extension = %+v, want generated == candidateCount under budget zero with totals conserved", full)
	}
	if len(full.Attested) != 1 || full.Attested[0] != wantAttestation {
		t.Fatalf("exhaustive extension attestations = %+v, want the prefix disposition still carried", full.Attested)
	}
	if len(full.Survivors) < 2 {
		t.Fatalf("exhaustive fixture survivors = %+v, want the never-executed branch to keep surviving mutants", full.Survivors)
	}
	for _, survivor := range full.Survivors {
		if survivor.Execution == "" {
			t.Fatalf("exhaustive extension left a survivor unbucketed: %+v", full.Survivors)
		}
	}
	if _, err := Export(exhaustive); err != nil {
		t.Fatalf("exporting exhaustively extended finding: %v", err)
	}

	// Force re-measures the whole target, never extending.
	forced, forcedDecisions, forcedDispatched := collect(Options{Budget: 3, Prior: prior, Force: true})
	if forced[0].Cached || len(forcedDecisions) != 1 || forcedDecisions[0].Reason != "forced" || !slices.Equal(forcedDispatched, []int{0, 1, 2}) {
		t.Fatalf("forced run = %+v, dispatched %v; want a whole re-measure with reason forced", forcedDecisions, forcedDispatched)
	}

	// A moved non-budget pin refuses the extension: the whole target
	// re-measures, the attestation sheds, and the decision names both the
	// budget shortfall and the moved pin (REQ-result-stale).
	tampered := append([]Finding(nil), prior...)
	tampered[0].TargetEvidence.MaximalClosure = "not-the-current-closure"
	moved, movedDecisions, movedDispatched := collect(Options{Budget: 3, Prior: tampered})
	if len(movedDecisions) != 1 || !strings.HasPrefix(movedDecisions[0].Reason, "budget: ") ||
		!strings.Contains(movedDecisions[0].Reason, "stale") || !strings.Contains(movedDecisions[0].Reason, "target") {
		t.Fatalf("moved-pin decisions = %+v, want the budget shortfall with the moved pin attributed", movedDecisions)
	}
	if !slices.Equal(movedDispatched, []int{0, 1, 2}) || len(moved[0].Attested) != 0 {
		t.Fatalf("moved-pin run dispatched %v with attestations %+v, want a whole re-measure shedding the disposition", movedDispatched, moved[0].Attested)
	}

	// Prior candidate evidence never composes with the extension: the whole
	// target re-measures with the refusal named (REQ-result-stale).
	composed := append([]Finding(nil), prior...)
	composed[0].CandidateEvidence = []CandidateEvidence{{Position: prefixSurvivor.Position, Operator: prefixSurvivor.Operator, Reason: "test process produced no runtime-input log", Disposition: "survived"}}
	_, composedDecisions, composedDispatched := collect(Options{Budget: 3, Prior: composed})
	if len(composedDecisions) != 1 || !strings.HasPrefix(composedDecisions[0].Reason, "budget: ") ||
		!strings.Contains(composedDecisions[0].Reason, "prior candidate evidence re-executes only under its recorded budget") {
		t.Fatalf("composition decisions = %+v, want the fail-closed refusal named", composedDecisions)
	}
	if !slices.Equal(composedDispatched, []int{0, 1, 2}) {
		t.Fatalf("composition run dispatched %v, want the whole target re-measured", composedDispatched)
	}

	// A record whose measured prefix cannot be re-identified — its every
	// other pin holding — refuses the extension: the whole target re-measures
	// with the regeneration refusal appended to the budget reason
	// (REQ-result-stale's budget-extension carve-out).
	unidentifiable := append([]Finding(nil), prior...)
	unidentifiable[0].Operators = append([]OperatorSummary(nil), prior[0].Operators...)
	unidentifiable[0].Operators[0].Operator = "not-an-operator-the-enumeration-selects"
	_, refusedDecisions, refusedDispatched := collect(Options{Budget: 3, Prior: unidentifiable})
	if len(refusedDecisions) != 1 || !strings.HasPrefix(refusedDecisions[0].Reason, "budget: ") ||
		!strings.Contains(refusedDecisions[0].Reason, "deterministic regeneration cannot re-identify the measured prefix") {
		t.Fatalf("refused-extension decisions = %+v, want the regeneration refusal appended", refusedDecisions)
	}
	if !slices.Equal(refusedDispatched, []int{0, 1, 2}) {
		t.Fatalf("refused extension dispatched %v, want the whole target re-measured", refusedDispatched)
	}
}

// TestEmitOracleGuidanceGuards pins the guidance emission shared by the fresh
// measure and the budget extension (REQ-exec-oracle-guidance): an
// unverifiable merged record under a package-derived oracle emits attribution
// through the per-oracle-set cache, while a nil callback, verifiable
// evidence, or an explicit oracle emits nothing.
func TestEmitOracleGuidanceGuards(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/empty\n\ngo 1.26.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "empty.go"), []byte("package empty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tree, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	oracle := []string{"example.com/empty.TestB", "example.com/empty.TestA"}
	cache := map[string]oracleAttribution{"example.com/empty.TestA\x00example.com/empty.TestB": {}}
	w := work{oracle: oracle}
	unstable := Finding{TargetEvidence: SubjectEvidence{RuntimeUnverifiable: true, RuntimeReason: "diverged"}}

	var got []OracleGuidance
	opts := Options{Guidance: func(g OracleGuidance) { got = append(got, g) }}
	if err := tree.emitOracleGuidance(ctx, unstable, w, "example.com/empty.F", opts, nil, cache); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Symbol != "example.com/empty.F" {
		t.Fatalf("guidance = %+v, want one attribution for the symbol", got)
	}

	got = nil
	if err := tree.emitOracleGuidance(ctx, Finding{}, w, "example.com/empty.F", opts, nil, cache); err != nil {
		t.Fatal(err)
	}
	explicit := unstable
	explicit.OracleExplicit = true
	if err := tree.emitOracleGuidance(ctx, explicit, w, "example.com/empty.F", opts, nil, cache); err != nil {
		t.Fatal(err)
	}
	if err := tree.emitOracleGuidance(ctx, unstable, w, "example.com/empty.F", Options{}, nil, cache); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("guarded emissions leaked guidance: %+v", got)
	}
}

// TestBucketSurvivorExecutionKeepsCarriedPrefixBuckets pins the splice's
// advisory-bucket boundary: survivors below from keep their recorded buckets
// verbatim — even under the unverifiable stamp, which classifies only the
// re-measured tail — so a suffix-local divergence never rewrites advisory
// data the served prefix measured under verifiable conditions.
func TestBucketSurvivorExecutionKeepsCarriedPrefixBuckets(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/empty\n\ngo 1.26.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "empty.go"), []byte("package empty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tree, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	f := Finding{
		TargetEvidence: SubjectEvidence{RuntimeUnverifiable: true},
		Survivors: []Survivor{
			{Position: "f.go:1:1", Operator: "op-a", Execution: "executed-and-passed"},
			{Position: "f.go:2:2", Operator: "op-a"},
		},
	}
	if err := tree.bucketSurvivorExecution(context.Background(), &f, work{}, Options{}, nil, nil, 1); err != nil {
		t.Fatal(err)
	}
	if f.Survivors[0].Execution != "executed-and-passed" {
		t.Fatalf("carried prefix bucket rewritten: %+v", f.Survivors)
	}
	if f.Survivors[1].Execution != "unstable-oracle" {
		t.Fatalf("re-measured tail not classified under the stamp: %+v", f.Survivors)
	}
}

// TestExtendFindingCountsAppendsSuffixOutcomes pins the budget-extension
// splice accounting under INV-RESULT-CANDIDATE-CONSERVATION: every prefix
// candidate keeps its recorded disposition, survivor identity, and
// attestation, each suffix outcome is appended per operator and in the totals
// (including an operator the record never saw and a pre-execution discard),
// the suffix run's candidate evidence becomes the record's, and budget and
// generated record the merged truth.
func TestExtendFindingCountsAppendsSuffixOutcomes(t *testing.T) {
	runnable := []engine.Replacement{{File: "f.go", Source: []byte("x")}}
	candidates := []engine.Candidate{
		{Symbol: "p.F", Operator: "op-a", Position: "f.go:1:1", Replacements: runnable}, // prefix kill
		{Symbol: "p.F", Operator: "op-b", Position: "f.go:2:2", Replacements: runnable}, // prefix survivor, attested
		{Symbol: "p.F", Operator: "op-a", Position: "f.go:3:3", Replacements: runnable}, // suffix survivor
		{Symbol: "p.F", Operator: "op-c", Position: "f.go:4:4", Replacements: runnable}, // suffix kill, new operator
		{Symbol: "p.F", Operator: "op-b", Position: "f.go:5:5"},                         // suffix pre-execution discard
	}
	rec := Finding{
		Symbol: "p.F", Budget: 2, CandidateCount: 5, Generated: 2, Mutants: 2, Killed: 1,
		Operators: []OperatorSummary{
			{Operator: "op-a", Generated: 1, Killed: 1},
			{Operator: "op-b", Generated: 1, Survived: 1},
		},
		Survivors: []Survivor{{Position: "f.go:2:2", Operator: "op-b", Execution: "executed-and-passed"}},
		Attested:  []Attestation{{Position: "f.go:2:2", Operator: "op-b", Reason: "equivalent"}},
	}
	outcomes := []engine.MutantOutcome{0, 0, engine.MutantSurvived, engine.MutantKilled, engine.MutantDiscarded}
	fresh := []CandidateEvidence{{Position: "f.go:3:3", Operator: "op-a", Reason: "test process produced no runtime-input log", Disposition: "survived"}}
	extended, err := extendFindingCounts(context.Background(), rec, candidates, 2, outcomes, fresh, 5)
	if err != nil {
		t.Fatal(err)
	}
	if extended.Budget != 5 || extended.Generated != 5 || extended.CandidateCount != 5 ||
		extended.Mutants != 4 || extended.Killed != 2 || extended.Discarded != 1 {
		t.Fatalf("extended totals = %+v, want the suffix appended onto the recorded prefix", extended)
	}
	wantOperators := []OperatorSummary{
		{Operator: "op-a", Generated: 2, Killed: 1, Survived: 1},
		{Operator: "op-b", Generated: 2, Discarded: 1, Survived: 1},
		{Operator: "op-c", Generated: 1, Killed: 1},
	}
	if !slices.Equal(extended.Operators, wantOperators) {
		t.Fatalf("extended operators = %+v, want %+v", extended.Operators, wantOperators)
	}
	wantSurvivors := []Survivor{
		{Position: "f.go:2:2", Operator: "op-b", Execution: "executed-and-passed"},
		{Position: "f.go:3:3", Operator: "op-a"},
	}
	if !slices.Equal(extended.Survivors, wantSurvivors) {
		t.Fatalf("extended survivors = %+v, want the prefix survivor carried and the suffix survivor appended", extended.Survivors)
	}
	if len(extended.Attested) != 1 || extended.Attested[0] != rec.Attested[0] {
		t.Fatalf("extended attestations = %+v, want the prefix disposition carried verbatim", extended.Attested)
	}
	if !slices.Equal(extended.CandidateEvidence, fresh) {
		t.Fatalf("extended candidate evidence = %+v, want the suffix run's flags only", extended.CandidateEvidence)
	}
	for _, summary := range extended.Operators {
		if summary.Generated != summary.Discarded+summary.Killed+summary.Survived {
			t.Fatalf("operator summary does not conserve: %+v", summary)
		}
	}
	if extended.Generated != extended.Mutants+extended.Discarded || extended.Mutants != extended.Killed+len(extended.Survivors) {
		t.Fatalf("finding totals do not conserve: %+v", extended)
	}
}

// TestExtendedPrefixStandsFallsBackOnMismatch: REQ-result-stale's
// budget-extension regeneration bound — a regenerated enumeration extends a
// capped record only when the complete candidate count is unchanged, the
// selection is strictly longer, every identity is unique, the recorded
// per-operator selected counts equal the prefix's, and every recorded
// survivor re-identifies inside the prefix; any mismatch refuses the
// extension so the whole target re-measures.
func TestExtendedPrefixStandsFallsBackOnMismatch(t *testing.T) {
	generation := engine.Generation{
		CandidateCount: 3,
		Candidates: []engine.Candidate{
			{Position: "a.go:1:1", Operator: "op-a"},
			{Position: "a.go:2:1", Operator: "op-b"},
			{Position: "a.go:3:1", Operator: "op-a"},
		},
	}
	rec := Finding{
		CandidateCount: 3,
		Generated:      2,
		Operators: []OperatorSummary{
			{Operator: "op-a", Generated: 1, Killed: 1},
			{Operator: "op-b", Generated: 1, Survived: 1},
		},
		Survivors: []Survivor{{Position: "a.go:2:1", Operator: "op-b"}},
	}
	if !extendedPrefixStands(generation, rec) {
		t.Fatal("matching regeneration refused the extension")
	}
	drifted := generation
	drifted.CandidateCount = 4
	if extendedPrefixStands(drifted, rec) {
		t.Fatal("candidate-count drift accepted")
	}
	covered := rec
	covered.Generated = 3
	covered.Operators = []OperatorSummary{
		{Operator: "op-a", Generated: 2, Killed: 2},
		{Operator: "op-b", Generated: 1, Survived: 1},
	}
	if extendedPrefixStands(generation, covered) {
		t.Fatal("a selection no longer than the recorded prefix accepted")
	}
	duplicated := generation
	duplicated.Candidates = []engine.Candidate{
		generation.Candidates[0],
		generation.Candidates[1],
		generation.Candidates[0],
	}
	if extendedPrefixStands(duplicated, rec) {
		t.Fatal("duplicate candidate identity accepted")
	}
	extraOperator := rec
	extraOperator.Operators = []OperatorSummary{{Operator: "op-a", Generated: 2, Killed: 2}}
	extraOperator.Survivors = nil
	if extendedPrefixStands(generation, extraOperator) {
		t.Fatal("prefix operator-set drift accepted")
	}
	miscounted := rec
	miscounted.Operators = []OperatorSummary{
		{Operator: "op-a", Generated: 2, Killed: 2},
		{Operator: "op-b", Generated: 0},
	}
	if extendedPrefixStands(generation, miscounted) {
		t.Fatal("prefix per-operator count drift accepted")
	}
	outside := rec
	outside.Survivors = []Survivor{{Position: "a.go:3:1", Operator: "op-a"}}
	if extendedPrefixStands(generation, outside) {
		t.Fatal("a recorded survivor outside the prefix accepted")
	}
}

// TestFoldRecordedUnionKeepsRecordedPinsAndStampsNewReads: the budget
// extension's union reconciliation (REQ-result-stale's fail-closed bound) — a
// suffix union that read only inputs the served record already pinned folds
// to exactly the persisted union and leaves the evidence untouched, while a
// suffix read beyond the record's pins survives the fold, diverges, and
// stamps the extended finding explicitly non-reusable.
func TestFoldRecordedUnionKeepsRecordedPinsAndStampsNewReads(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/empty\n\ngo 1.26.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "empty.go"), []byte("package empty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "data.txt"), []byte("observed"), 0o644); err != nil {
		t.Fatal(err)
	}
	tree, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	env := os.Environ()
	ctx := context.Background()
	recorded, err := runtimeinput.FromTestLogEnv([]byte("open data.txt\n"), root, root, env, runtimeinput.WithCompletedProcess("prior"), runtimeinput.WithBracket(testBracket(t, root)))
	if err != nil {
		t.Fatal(err)
	}
	recordedState, err := runtimeinput.CompletedState(recorded)
	if err != nil {
		t.Fatal(err)
	}
	evidence := SubjectEvidence{Symbol: "example.com/empty.Gone", RuntimeInputs: recordedState.Manifest, RuntimeDigest: recordedState.Digest}
	rec := Finding{TargetEvidence: evidence, OracleEvidence: []SubjectEvidence{evidence}}

	// The suffix read a subset of the recorded pins: the fold restores the
	// persisted union exactly and the evidence stays untouched.
	subset, err := runtimeinput.FromTestLogEnv([]byte("# test log\n"), root, root, env, runtimeinput.WithCompletedProcess("suffix"), runtimeinput.WithBracket(testBracket(t, root)))
	if err != nil {
		t.Fatal(err)
	}
	folded, err := tree.foldRecordedUnion(ctx, env, rec, root, subset)
	if err != nil {
		t.Fatal(err)
	}
	_, same, err := tree.applySplicedUnion(ctx, env, rec, folded)
	if err != nil {
		t.Fatal(err)
	}
	if same.TargetEvidence.RuntimeUnverifiable || same.OracleEvidence[0].RuntimeUnverifiable ||
		same.TargetEvidence.RuntimeInputs != recordedState.Manifest || same.TargetEvidence.RuntimeDigest != recordedState.Digest {
		t.Fatalf("subset suffix rewrote the served evidence: %+v", same.TargetEvidence)
	}

	// The suffix read an input the record never pinned: the fold diverges
	// and every subject's evidence is stamped non-reusable.
	sparse := SubjectEvidence{Symbol: "example.com/empty.Gone"}
	sparseRecorded, err := runtimeinput.FromTestLogEnv([]byte("# test log\n"), root, root, env, runtimeinput.WithCompletedProcess("prior"), runtimeinput.WithBracket(testBracket(t, root)))
	if err != nil {
		t.Fatal(err)
	}
	sparseState, err := runtimeinput.CompletedState(sparseRecorded)
	if err != nil {
		t.Fatal(err)
	}
	sparse.RuntimeInputs = sparseState.Manifest
	sparse.RuntimeDigest = sparseState.Digest
	sparseRec := Finding{TargetEvidence: sparse, OracleEvidence: []SubjectEvidence{sparse}}
	grew, err := runtimeinput.FromTestLogEnv([]byte("open data.txt\n"), root, root, env, runtimeinput.WithCompletedProcess("suffix"), runtimeinput.WithBracket(testBracket(t, root)))
	if err != nil {
		t.Fatal(err)
	}
	folded, err = tree.foldRecordedUnion(ctx, env, sparseRec, root, grew)
	if err != nil {
		t.Fatal(err)
	}
	_, marked, err := tree.applySplicedUnion(ctx, env, sparseRec, folded)
	if err != nil {
		t.Fatal(err)
	}
	if !marked.TargetEvidence.RuntimeUnverifiable || !marked.OracleEvidence[0].RuntimeUnverifiable || marked.TargetEvidence.RuntimeReason == "" {
		t.Fatalf("suffix reads beyond the recorded pins left the evidence reusable: %+v", marked.TargetEvidence)
	}

	// The recorded manifest no longer adopts (its pinned input moved on
	// disk): the fold falls back to the bare suffix union and the resulting
	// divergence stamps the extension non-reusable rather than serving over
	// the moved pin (REQ-result-stale's fail-closed bound).
	if err := os.WriteFile(filepath.Join(root, "data.txt"), []byte("moved"), 0o644); err != nil {
		t.Fatal(err)
	}
	unadoptable, err := runtimeinput.FromTestLogEnv([]byte("# test log\n"), root, root, env, runtimeinput.WithCompletedProcess("suffix"), runtimeinput.WithBracket(testBracket(t, root)))
	if err != nil {
		t.Fatal(err)
	}
	folded, err = tree.foldRecordedUnion(ctx, env, rec, root, unadoptable)
	if err != nil {
		t.Fatal(err)
	}
	_, stamped, err := tree.applySplicedUnion(ctx, env, rec, folded)
	if err != nil {
		t.Fatal(err)
	}
	if !stamped.TargetEvidence.RuntimeUnverifiable || !stamped.OracleEvidence[0].RuntimeUnverifiable {
		t.Fatalf("unadoptable recorded manifest left the evidence reusable: %+v", stamped.TargetEvidence)
	}
}

// Each execution window's findings COMMIT before the next window
// dispatches, so an interrupted campaign keeps every earlier window's
// verdicts instead of losing hours of completed work to one late abort
// (REQ-exec-cancellation's incremental-commit clause).
func TestRunCommitsEarlierWindowsBeforeLaterOnesDispatch(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	old := runWindowCandidates
	runWindowCandidates = 1
	t.Cleanup(func() { runWindowCandidates = old })
	tree := fixtureTree(t)
	targets := []Target{
		{Symbol: "example.com/fixture/plain.Ok", Oracle: []string{"example.com/fixture/plain.TestPlain"}},
		{Symbol: "example.com/fixture/lib.Add", Oracle: []string{"example.com/fixture/lib.TestAdd"}},
	}
	var mu sync.Mutex
	var events []string
	findings, err := tree.Run(context.Background(), targets, Options{
		Budget: 1, Jobs: 2,
		Commit: func(f Finding) error {
			mu.Lock()
			events = append(events, "commit:"+f.Symbol)
			mu.Unlock()
			return nil
		},
		dispatched: func(symbol string, mi int) {
			mu.Lock()
			events = append(events, "dispatch:"+symbol)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 {
		t.Fatalf("findings = %+v", findings)
	}
	firstCommit := -1
	var firstCommitted string
	for i, e := range events {
		if rest, ok := strings.CutPrefix(e, "commit:"); ok {
			firstCommit, firstCommitted = i, rest
			break
		}
	}
	if firstCommit == -1 {
		t.Fatalf("no commits observed: %v", events)
	}
	for i, e := range events {
		if rest, ok := strings.CutPrefix(e, "dispatch:"); ok && rest != firstCommitted && i < firstCommit {
			t.Fatalf("a later window dispatched before the first window committed: %v", events)
		}
	}
}

// A fully-cached run still owes the campaign epilogue — the closing
// validation and its hook run even when no target re-measured. With
// nothing produced the validation is vacuous, so the observable pin is
// the epilogue firing at all; a drift with live producers keeps its own
// pins in the drift tests (REQ-exec-quiescence).
func TestRunFullyCachedStillRunsCampaignEpilogue(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	tree := fixtureTree(t)
	targets := []Target{{Symbol: "example.com/fixture/plain.Ok", Oracle: []string{"example.com/fixture/plain.TestPlain"}}}
	first, err := tree.Run(context.Background(), targets, Options{Budget: 1, Jobs: 1})
	if err != nil || len(first) != 1 {
		t.Fatalf("measuring run = %+v, %v", first, err)
	}
	second := fixtureTree(t)
	fired := false
	cached, err := second.Run(context.Background(), targets, Options{
		Budget: 1, Jobs: 1, Prior: first,
		afterExecution: func() { fired = true },
	})
	if err != nil || len(cached) != 1 || !cached[0].Cached {
		t.Fatalf("cached run = %+v, %v, want a fully served target", cached, err)
	}
	if !fired {
		t.Fatal("fully-cached run skipped the campaign epilogue")
	}
}
