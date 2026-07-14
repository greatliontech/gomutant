package engine

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// TestMutants pins the operator set and determinism (REQ-mut-operators,
// REQ-mut-budget): sites in source order, the budget respected, identical
// runs identical, no two mutants of one symbol rendering the same source.
func TestMutants(t *testing.T) {
	if OperatorSet != "go/6" {
		t.Fatalf("operator set = %q, want go/6", OperatorSet)
	}
	tr := fixtureTree(t)
	ms, err := tr.Mutants("example.com/fixture/lib.Add", 0)
	if err != nil {
		t.Fatal(err)
	}
	ops := map[string]bool{}
	for _, m := range ms {
		ops[m.Operator] = true
		if m.Position == "" || len(m.Replacements) != 1 || len(m.Replacements[0].Source) == 0 || m.Replacements[0].File == "" {
			t.Fatalf("incomplete mutant: %+v", m)
		}
	}
	for _, want := range []string{"equality: == -> !=", "condition: negate", "zero return"} {
		if !ops[want] {
			t.Fatalf("operator %q missing: %v", want, ops)
		}
	}

	// The extended families, one site each in the Mixed fixture. The
	// declaration (total := 0) must NOT yield a drop-assignment mutant:
	// removing a declaration proves nothing.
	mixed, err := tr.Mutants("example.com/fixture/lib.Mixed", 0)
	if err != nil {
		t.Fatal(err)
	}
	mixedOps := map[string]int{}
	for _, m := range mixed {
		mixedOps[m.Operator]++
	}
	for _, want := range []string{
		"drop assignment", "+= -> -=", "* -> /", "+ -> -",
		"increment literal", "loop control: continue -> break", "boolean operand: -> false",
		"logical: || -> &&", "logical: && -> ||", "boolean operand: -> true", "++ -> --",
	} {
		if mixedOps[want] == 0 {
			t.Fatalf("operator %q missing: %v", want, mixedOps)
		}
	}
	if got := mixedOps["drop assignment"]; got != 2 { // += and = are stores; := is not
		t.Fatalf("drop assignment sites = %d; a declaration must not count", got)
	}

	// No two mutants of one symbol render the same source: a duplicate would
	// double-count one effective mutant.
	for _, symbol := range []string{"example.com/fixture/lib.Add", "example.com/fixture/lib.Weak", "example.com/fixture/lib.Mixed"} {
		ms, err := tr.Mutants(symbol, 0)
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]string{}
		for _, m := range ms {
			key := string(m.Replacements[0].Source)
			if prev, dup := seen[key]; dup {
				t.Fatalf("%s: mutants %s and %s render identically", symbol, prev, m.Position+" "+m.Operator)
			}
			seen[key] = m.Position + " " + m.Operator
		}
	}
	nested, err := tr.Mutants("example.com/fixture/lib.Nested", 0)
	if err != nil {
		t.Fatal(err)
	}
	identities := map[string]bool{}
	disambiguated := false
	for _, m := range nested {
		identity := m.Position + " " + m.Operator
		if identities[identity] {
			t.Fatalf("duplicate mutant identity %q", identity)
		}
		identities[identity] = true
		disambiguated = disambiguated || strings.Contains(m.Position, "#2")
	}
	if !disambiguated {
		t.Fatal("nested logical mutants did not disambiguate an overlapping position")
	}
	loop, err := tr.Mutants("example.com/fixture/lib.Loop", 0)
	if err != nil {
		t.Fatal(err)
	}
	loopOps := map[string]int{}
	for _, mutant := range loop {
		loopOps[mutant.Operator]++
	}
	if loopOps["condition: negate"] != 1 {
		t.Fatalf("loop condition negations = %d, want 1: %v", loopOps["condition: negate"], loopOps)
	}
	// Two identical statements delete to the same render: dedup collapses
	// them to one effective mutant.
	dup, err := tr.Mutants("example.com/fixture/lib.Dup", 0)
	if err != nil {
		t.Fatal(err)
	}
	dels := 0
	for _, m := range dup {
		if m.Operator == "delete statement" {
			dels++
		}
	}
	if dels != 1 {
		t.Fatalf("Dup delete-statement mutants = %d, want 1 (deduped)", dels)
	}

	// The arithmetic swap must skip non-numeric operands: string
	// concatenation yields no "+ -> -" mutant.
	concat, err := tr.Mutants("example.com/fixture/lib.Concat", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range concat {
		if m.Operator == "+ -> -" {
			t.Fatalf("string concatenation mutated arithmetically: %+v", m)
		}
	}

	// A literal the increment cannot parse renders identically and is
	// dropped as a no-op site; the return still yields its zero mutant.
	big, err := tr.Mutants("example.com/fixture/lib.BigLit", 0)
	if err != nil {
		t.Fatal(err)
	}
	bigOps := map[string]bool{}
	for _, m := range big {
		bigOps[m.Operator] = true
	}
	if bigOps["increment literal"] {
		t.Fatal("unparseable literal produced an increment mutant")
	}
	if !bigOps["zero return"] {
		t.Fatalf("BigLit ops = %v, want zero return present", bigOps)
	}

	// Deleting the statement that alone references an import prunes the
	// orphaned import so the mutant compiles.
	logs, err := tr.Mutants("example.com/fixture/lib.Logs", 0)
	if err != nil {
		t.Fatal(err)
	}
	pruned := false
	for _, m := range logs {
		if m.Operator == "delete statement" && !strings.Contains(string(m.Replacements[0].Source), `"fmt"`) {
			pruned = true
		}
	}
	if !pruned {
		t.Fatal("orphaned import not pruned from the delete-statement mutant")
	}

	capped, err := tr.Mutants("example.com/fixture/lib.Add", 2)
	if err != nil || len(capped) != 2 {
		t.Fatalf("budget ignored: %d %v", len(capped), err)
	}
	again, err := tr.Mutants("example.com/fixture/lib.Add", 0)
	if err != nil || len(again) != len(ms) {
		t.Fatalf("nondeterministic: %d vs %d", len(again), len(ms))
	}
	for i := range ms {
		if ms[i].Operator != again[i].Operator || ms[i].Position != again[i].Position {
			t.Fatal("mutant order not deterministic")
		}
	}
}

func TestComparisonCatalog(t *testing.T) {
	tr := fixtureTree(t)
	want := map[string]int{
		"equality: == -> !=":           6,
		"equality: != -> ==":           3,
		"relational boundary: < -> <=": 4,
		"relational boundary: <= -> <": 2,
		"relational boundary: > -> >=": 2,
		"relational boundary: >= -> >": 2,
		"relational negation: < -> >=": 4,
		"relational negation: <= -> >": 2,
		"relational negation: > -> <=": 2,
		"relational negation: >= -> <": 2,
		"logical: && -> ||":            3,
		"logical: || -> &&":            19,
	}
	got := map[string]int{}
	for _, symbol := range []string{
		"example.com/fixture/lib.Boundary",
		"example.com/fixture/lib.EqualGeneric",
		"example.com/fixture/lib.RelationsGeneric",
		"example.com/fixture/lib.RelationsDefined",
		"example.com/fixture/lib.Logical",
		"example.com/fixture/lib.LogicalDefined",
		"example.com/fixture/lib.LogicalGeneric",
		"example.com/fixture/lib.EqualityConcrete",
		"example.com/fixture/lib.RelationsString",
	} {
		generation, err := tr.CandidatesContext(context.Background(), symbol, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, candidate := range generation.Candidates {
			if _, expected := want[candidate.Operator]; expected {
				got[candidate.Operator]++
			}
		}
	}
	if len(got) != len(want) {
		t.Fatalf("comparison operators = %v, want %v", got, want)
	}
	for operator, count := range want {
		if got[operator] != count {
			t.Errorf("%s count = %d, want %d", operator, got[operator], count)
		}
	}

	generation, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib.Boundary", 0)
	if err != nil {
		t.Fatal(err)
	}
	wantSources := map[string]string{
		"relational boundary: < -> <=": "package lib\n\nfunc Boundary(a, b int) bool {\n\treturn a <= b\n}\n",
		"relational negation: < -> >=": "package lib\n\nfunc Boundary(a, b int) bool {\n\treturn a >= b\n}\n",
	}
	var ordered []string
	for _, candidate := range generation.Candidates {
		wantSource, ok := wantSources[candidate.Operator]
		if !ok {
			continue
		}
		ordered = append(ordered, candidate.Operator)
		if len(candidate.Replacements) != 1 || string(candidate.Replacements[0].Source) != wantSource {
			t.Errorf("%s source = %q, want %q", candidate.Operator, candidate.Replacements, wantSource)
		}
	}
	if !slices.Equal(ordered, []string{"relational boundary: < -> <=", "relational negation: < -> >="}) {
		t.Fatalf("Boundary comparison order = %v", ordered)
	}

	const mappingSource = "package lib\n\nfunc MappingEQ(a, b int) bool   { return a == b }\nfunc MappingNEQ(a, b int) bool  { return a != b }\nfunc MappingLT(a, b int) bool   { return a < b }\nfunc MappingLE(a, b int) bool   { return a <= b }\nfunc MappingGT(a, b int) bool   { return a > b }\nfunc MappingGE(a, b int) bool   { return a >= b }\nfunc MappingAND(a, b bool) bool { return a && b }\nfunc MappingOR(a, b bool) bool  { return a || b }\n"
	mappings := []struct {
		symbol, operator, old, replacement string
	}{
		{"MappingEQ", "equality: == -> !=", "return a == b", "return a != b"},
		{"MappingNEQ", "equality: != -> ==", "return a != b", "return a == b"},
		{"MappingLT", "relational boundary: < -> <=", "return a < b", "return a <= b"},
		{"MappingLT", "relational negation: < -> >=", "return a < b", "return a >= b"},
		{"MappingLE", "relational boundary: <= -> <", "return a <= b", "return a < b"},
		{"MappingLE", "relational negation: <= -> >", "return a <= b", "return a > b"},
		{"MappingGT", "relational boundary: > -> >=", "return a > b", "return a >= b"},
		{"MappingGT", "relational negation: > -> <=", "return a > b", "return a <= b"},
		{"MappingGE", "relational boundary: >= -> >", "return a >= b", "return a > b"},
		{"MappingGE", "relational negation: >= -> <", "return a >= b", "return a < b"},
		{"MappingAND", "logical: && -> ||", "return a && b", "return a || b"},
		{"MappingOR", "logical: || -> &&", "return a || b", "return a && b"},
	}
	for _, mapping := range mappings {
		generation, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib."+mapping.symbol, 0)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, candidate := range generation.Candidates {
			if candidate.Operator != mapping.operator {
				continue
			}
			found = true
			wantSource := strings.Replace(mappingSource, mapping.old, mapping.replacement, 1)
			if len(candidate.Replacements) != 1 || string(candidate.Replacements[0].Source) != wantSource {
				t.Errorf("%s source = %q, want %q", mapping.operator, candidate.Replacements, wantSource)
			}
		}
		if !found {
			t.Errorf("%s did not emit %s", mapping.symbol, mapping.operator)
		}
	}
}

// TestMutantsBodyless pins the no-body edge: a bodyless (assembly) symbol
// yields no mutants and no error — nothing to mutate is not a failure.
func TestMutantsBodyless(t *testing.T) {
	tr := fixtureTree(t)
	ms, err := tr.Mutants("example.com/fixture/bodyless.Ext", 0)
	if err != nil || len(ms) != 0 {
		t.Fatalf("bodyless: %d mutants, err %v", len(ms), err)
	}
}

func TestMutantsProcessImportsOnlyForRemovalSites(t *testing.T) {
	tr := fixtureTree(t)
	processed := map[string]string{}
	tr.importProcessor = func(_ context.Context, filename string, source []byte) ([]byte, error) {
		processed[string(source)] = filename
		return source, nil
	}
	var mutants []Mutant
	for _, symbol := range []string{"example.com/fixture/lib.Mixed", "example.com/fixture/lib.Logs"} {
		generated, err := tr.Mutants(symbol, 0)
		if err != nil {
			t.Fatal(err)
		}
		mutants = append(mutants, generated...)
	}
	removal := map[string]bool{
		"boolean operand: -> false": true, "boolean operand: -> true": true, "delete statement": true,
		"condition: force false": true, "condition: force true": true,
		"drop assignment": true, "zero return": true,
	}
	seenRemoval := map[string]bool{}
	for _, mutant := range mutants {
		processedFile, wasProcessed := processed[string(mutant.Replacements[0].Source)]
		if removal[mutant.Operator] {
			seenRemoval[mutant.Operator] = true
			if !wasProcessed {
				t.Fatalf("removal-capable %q skipped import processing", mutant.Operator)
			}
			if processedFile != mutant.Replacements[0].File {
				t.Fatalf("removal-capable %q processed as %q, want %q", mutant.Operator, processedFile, mutant.Replacements[0].File)
			}
		} else if wasProcessed {
			t.Fatalf("reference-preserving %q processed imports", mutant.Operator)
		}
	}
	for operator := range removal {
		if !seenRemoval[operator] {
			t.Fatalf("removal-capable operator %q not exercised", operator)
		}
	}
}

func TestCandidatesRejectImportProcessingFailure(t *testing.T) {
	tr := fixtureTree(t)
	calls := 0
	tr.importProcessor = func(context.Context, string, []byte) ([]byte, error) {
		calls++
		return nil, errors.New("cannot process imports")
	}
	if _, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib.Logs", 0); err == nil || !strings.Contains(err.Error(), "normalize candidate") {
		t.Fatalf("normalization error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("processing calls = %d, want 1", calls)
	}
}

func TestCandidatesSelectBeforeEffectiveSourceDeduplication(t *testing.T) {
	tr := fixtureTree(t)
	tr.importProcessor = func(context.Context, string, []byte) ([]byte, error) {
		return []byte("package lib\n"), nil
	}
	generation, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib.PruneCollision", 2)
	if err != nil {
		t.Fatal(err)
	}
	if generation.CandidateCount <= 2 || len(generation.Candidates) != 2 {
		t.Fatalf("generation = %+v", generation)
	}
	if generation.Candidates[0].Operator != "delete statement" || len(generation.Candidates[0].Replacements) != 1 ||
		generation.Candidates[1].Operator != "delete statement" || len(generation.Candidates[1].Replacements) != 0 {
		t.Fatalf("selected candidates = %+v", generation.Candidates)
	}
	if got := string(generation.Candidates[0].Replacements[0].Source); got != "package lib\n" {
		t.Fatalf("first exact source = %q", got)
	}
}

func TestCandidatesRenderExactSource(t *testing.T) {
	tr := fixtureTree(t)
	generation, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib.Exact", 1)
	if err != nil {
		t.Fatal(err)
	}
	if generation.CandidateCount < 1 || len(generation.Candidates) != 1 || len(generation.Candidates[0].Replacements) != 1 {
		t.Fatalf("generation = %+v", generation)
	}
	want := "package lib\n\nfunc Exact(a int) int {\n\tif !(a == 0) {\n\t\treturn 1\n\t}\n\treturn a\n}\n"
	if got := string(generation.Candidates[0].Replacements[0].Source); got != want {
		t.Fatalf("generated source:\n%s\nwant:\n%s", got, want)
	}
}

func TestApplySourceEditsRejectsOverlap(t *testing.T) {
	if _, err := applySourceEdits([]byte("abcdef"), nil); err == nil || !strings.Contains(err.Error(), "no source edits") {
		t.Fatalf("empty edit error = %v", err)
	}
	_, err := applySourceEdits([]byte("abcdef"), []sourceEdit{{start: 1, end: 4}, {start: 3, end: 5}})
	if err == nil || !strings.Contains(err.Error(), "overlaps") {
		t.Fatalf("overlap error = %v", err)
	}
	got, err := applySourceEdits([]byte("abcd"), []sourceEdit{
		{start: 1, end: 1, replacement: []byte("X")},
		{start: 1, end: 1, replacement: []byte("Y")},
		{start: 2, end: 3, replacement: []byte("Z")},
	})
	if err != nil || string(got) != "aXYbZd" {
		t.Fatalf("ordered edits = %q, %v", got, err)
	}
	for _, edits := range [][]sourceEdit{
		{{start: 1, end: 1, replacement: []byte("X")}, {start: 1, end: 2, replacement: []byte("R")}},
		{{start: 1, end: 2, replacement: []byte("R")}, {start: 1, end: 1, replacement: []byte("X")}},
	} {
		got, err := applySourceEdits([]byte("ab"), edits)
		if err != nil || string(got) != "aXR" {
			t.Fatalf("boundary edits = %q, %v", got, err)
		}
	}
}

func TestDiscardedCandidateReservesOccurrenceIdentity(t *testing.T) {
	tr := fixtureTree(t)
	generation, err := tr.CandidatesContext(context.Background(), "example.com/fixture/lib.Reserved", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(generation.Candidates) != 2 || generation.Candidates[0].Operator != "boolean operand: -> true" || len(generation.Candidates[0].Replacements) != 0 {
		t.Fatalf("first candidate = %+v", generation.Candidates)
	}
	if second := generation.Candidates[1]; second.Operator != "boolean operand: -> true" || !strings.HasSuffix(second.Position, "#2") || len(second.Replacements) != 1 {
		t.Fatalf("second candidate = %+v", second)
	}
}

func TestMutantsContextCancellationRestoresSyntax(t *testing.T) {
	tr := fixtureTree(t)
	ctx, cancel := context.WithCancel(context.Background())
	tr.importProcessor = func(_ context.Context, _ string, source []byte) ([]byte, error) {
		cancel()
		return source, nil
	}
	mutants, err := tr.MutantsContext(ctx, "example.com/fixture/lib.Logs", 0)
	if !errors.Is(err, context.Canceled) || mutants != nil {
		t.Fatalf("cancelled mutants = %+v, %v", mutants, err)
	}
	tr.importProcessor = nil
	mutants, err = tr.Mutants("example.com/fixture/lib.Logs", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutant := range mutants {
		if mutant.Operator == "delete statement" && !strings.Contains(string(mutant.Replacements[0].Source), `"fmt"`) {
			return
		}
	}
	t.Fatal("syntax was not restored after cancelled import processing")
}
