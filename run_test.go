package gomutant

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	gofresh "github.com/greatliontech/gofresh"
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
	first, err := tr.Run(ctx, []Target{targets[0], targets[2]}, Options{Decision: func(decision RunDecision) {
		firstDecisions = append(firstDecisions, decision)
	}})
	if err != nil {
		t.Fatal(err)
	}
	add, iface := first[0], first[1]
	wantAddSurvivors := []Survivor{
		{Position: "lib.go:24:2", Operator: "statement: delete"},
		{Position: "lib.go:24:5", Operator: "condition: force false"},
		{Position: "lib.go:24:12", Operator: "block: empty"},
		{Position: "lib.go:25:3", Operator: "statement: delete"},
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
	if iface.Skipped != "not a function" {
		t.Fatalf("interface target = %+v, want skipped as not a function", iface)
	}
	if len(firstDecisions) != 2 || firstDecisions[0].Reason != "no-prior" || firstDecisions[1].Action != "skipped" || firstDecisions[1].Reason != "not a function" {
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
	if len(widerDecisions) != 1 || !strings.HasPrefix(widerDecisions[0].Reason, "budget: ") {
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
func TestRunCompileDiscardIsCandidateLocalEvidence(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	findings, err := tr.Run(context.Background(), []Target{{
		Symbol: "example.com/fixture/lib.Idx",
		Oracle: []string{"example.com/fixture/lib.TestAdd"},
	}}, Options{})
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
	flagged := findings[0].CandidateEvidence
	if len(flagged) == 0 {
		t.Fatalf("compile-discard finding carries no candidate evidence: %+v", findings[0])
	}
	discardEvidence := 0
	for _, candidate := range flagged {
		if strings.Contains(candidate.Reason, "failed to build") && candidate.Disposition == "discarded" {
			discardEvidence++
		}
	}
	if discardEvidence == 0 {
		t.Fatalf("candidate evidence = %+v, want an explicit build-incomplete discard", flagged)
	}
	// A record carrying candidate evidence is not coverable without the
	// flagged re-execution, so Fresh reports false while the pins hold.
	if ok, err := tr.Fresh(findings[0], Target{Symbol: findings[0].Symbol, Oracle: []string{"example.com/fixture/lib.TestAdd"}}, 0); err != nil || ok {
		t.Fatalf("compile-discard finding coverable without execution = %v, %v", ok, err)
	}
	if _, err := Export(findings); err != nil {
		t.Fatalf("exporting candidate evidence: %v", err)
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
	fs, err := ParseFindings([]byte(`{"version":2,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":0,"targetEvidence":{"symbol":"p.F","maximalClosure":"c","toolchain":"go","buildConfig":"b","observationAssertion":"caller assertion","observationStrategy":"gofresh/observation-rta@2","observationSubjectPackage":"p","observationSubjectSymbol":"F","observationObservable":true,"observationEvidence":"proof","runtimeInputs":"m","runtimeDigest":"d"},"oracleEvidence":[{"symbol":"p.TestF","maximalClosure":"tc","toolchain":"go","buildConfig":"b","observationAssertion":"caller assertion","observationStrategy":"gofresh/observation-rta@2","observationSubjectPackage":"p","observationSubjectSymbol":"TestF","observationObservable":true,"observationEvidence":"proof","runtimeInputs":"m","runtimeDigest":"d"}],"oracleExplicit":true,"oracleTimeout":"1m0s","dirty":true,"candidateCount":0,"generated":0,"mutants":0,"killed":0,"discarded":0,"operators":[],"futureField":{"nested":true}}]}`))
	if err != nil || len(fs) != 1 || fs[0].Symbol != "p.F" {
		t.Fatalf("tolerant parse failed: %v %+v", err, fs)
	}
	for name, doc := range map[string]string{
		"null budget":                    `{"version":2,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":null,"targetEvidence":{},"oracleEvidence":[],"oracleTimeout":"1m0s","mutants":1,"killed":1}]}`,
		"null dirty":                     `{"version":2,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":1,"targetEvidence":{},"oracleEvidence":[],"oracleTimeout":"1m0s","dirty":null,"mutants":1,"killed":1}]}`,
		"duplicate budget":               `{"version":2,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":1,"budget":0,"targetEvidence":{},"oracleEvidence":[],"oracleTimeout":"1m0s","mutants":1,"killed":1}]}`,
		"duplicate version":              `{"version":2,"version":99,"findings":[]}`,
		"missing survivors":              `{"version":2,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":1,"targetEvidence":{},"oracleEvidence":[],"oracleTimeout":"1m0s","mutants":1,"killed":0}]}`,
		"empty attestation reason":       `{"version":2,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":1,"targetEvidence":{},"oracleEvidence":[],"oracleTimeout":"1m0s","mutants":1,"killed":0,"survivors":[{"position":"f.go:1:1","operator":"op"}],"attested":[{"position":"f.go:1:1","operator":"op","reason":""}]}]}`,
		"duplicate nested evidence":      `{"version":2,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":1,"targetEvidence":{"symbol":"p.F","symbol":"p.G"},"oracleEvidence":[],"oracleTimeout":"1m0s","mutants":0,"killed":0}]}`,
		"inflated budget":                `{"version":2,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":2,"targetEvidence":{},"oracleEvidence":[],"oracleTimeout":"1m0s","mutants":1,"killed":1}]}`,
		"colliding attestation identity": `{"version":2,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":1,"targetEvidence":{},"oracleEvidence":[],"oracleTimeout":"1m0s","mutants":1,"killed":0,"survivors":[{"position":"a|b.go:1:1","operator":"zero return"}],"attested":[{"position":"a","operator":"b.go:1:1|zero return","reason":"not the survivor"}]}]}`,
		"duplicate symbols":              `{"version":2,"findings":[{"symbol":"p.F","mutants":0,"killed":0},{"symbol":"p.F","mutants":0,"killed":0}]}`,
		"duplicate oracle symbols":       `{"version":2,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":0,"targetEvidence":{},"oracleEvidence":[{"symbol":"p.TestF"},{"symbol":"p.TestF"}],"oracleTimeout":"1m0s","dirty":true,"mutants":0,"killed":0}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseFindings([]byte(doc)); err == nil {
				t.Fatal("malformed known field accepted")
			}
		})
	}
	nonGit := `{"version":2,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":0,"targetEvidence":{"symbol":"p.F","maximalClosure":"c","toolchain":"go","buildConfig":"b","observationAssertion":"caller assertion","observationStrategy":"gofresh/observation-rta@2","observationSubjectPackage":"p","observationSubjectSymbol":"F","observationObservable":true,"observationEvidence":"proof","runtimeInputs":"m","runtimeDigest":"d"},"oracleEvidence":[{"symbol":"p.TestF","maximalClosure":"tc","toolchain":"go","buildConfig":"b","observationAssertion":"caller assertion","observationStrategy":"gofresh/observation-rta@2","observationSubjectPackage":"p","observationSubjectSymbol":"TestF","observationObservable":true,"observationEvidence":"proof","runtimeInputs":"m","runtimeDigest":"d"}],"oracleExplicit":true,"oracleTimeout":"1m0s","dirty":true,"candidateCount":0,"generated":0,"mutants":0,"killed":0,"discarded":0,"operators":[]}]}`
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
	emptyOracle := `{"version":2,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/2","budget":0,"targetEvidence":{"symbol":"p.F","maximalClosure":"c","toolchain":"go","buildConfig":"b","runtimeInputs":"m","runtimeDigest":"d"},"oracleEvidence":[],"oracleExplicit":true,"oracleTimeout":"1m0s","dirty":true,"candidateCount":0,"generated":0,"mutants":0,"killed":0,"discarded":0,"operators":[]}]}`
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
	legacy := `{"version":2,"findings":[{"symbol":"p.F","mutants":1,"killed":0,"survivors":[{"position":"f.go:1:1","operator":"op"}],"attested":[{"position":"f.go:1:1","operator":"op","reason":"legacy"}]}]}`
	if _, err := ParseFindings([]byte(legacy)); err == nil {
		t.Fatal("legacy finding accepted")
	}
	emptyPins := `{"version":2,"findings":[{"symbol":"p.F","bodyHash":"","operatorSet":"","budget":1,"targetEvidence":{"symbol":"","maximalClosure":"","toolchain":"","buildConfig":"","runtimeInputs":"","runtimeDigest":""},"oracleEvidence":[],"oracleTimeout":"","dirty":true,"mutants":1,"killed":0,"survivors":[{"position":"f.go:1:1","operator":"op"}],"attested":[{"position":"f.go:1:1","operator":"op","reason":"unsupported"}]}]}`
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
		Reason:     fmt.Sprintf("served: pins unchanged; %d candidate(s) re-execute", len(f.CandidateEvidence)),
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
	valid := `{"version":2,"findings":[{"symbol":"p.F","bodyHash":"h","operatorSet":"go/12","budget":0,"targetEvidence":{"symbol":"p.F","maximalClosure":"c","toolchain":"go","buildConfig":"b","observationAssertion":"caller assertion","observationStrategy":"gofresh/observation-rta@2","observationSubjectPackage":"p","observationSubjectSymbol":"F","observationObservable":true,"observationEvidence":"proof","runtimeInputs":"m","runtimeDigest":"d"},"oracleEvidence":[{"symbol":"p.TestF","maximalClosure":"tc","toolchain":"go","buildConfig":"b","observationAssertion":"caller assertion","observationStrategy":"gofresh/observation-rta@2","observationSubjectPackage":"p","observationSubjectSymbol":"TestF","observationObservable":true,"observationEvidence":"proof","runtimeInputs":"m","runtimeDigest":"d"}],"oracleExplicit":true,"oracleTimeout":"1m0s","dirty":true,"candidateCount":2,"generated":2,"mutants":2,"killed":1,"discarded":0,"operators":[{"operator":"op","generated":2,"discarded":0,"killed":1,"survived":1}],"survivors":[{"position":"f.go:2:2","operator":"op"}],"candidateEvidence":[{"position":"f.go:1:1","operator":"op","reason":"mutant test process panicked before observation finalization","disposition":"killed"}]}]}`
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
		Survivors: []Survivor{{Position: "f.go:3:3", Operator: "op-b"}, {Position: "f.go:4:4", Operator: "op-b"}},
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
	wantSurvivors := []Survivor{{Position: "f.go:2:2", Operator: "op-a"}, {Position: "f.go:4:4", Operator: "op-b"}}
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
