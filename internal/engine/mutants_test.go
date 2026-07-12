package engine

import (
	"strings"
	"testing"
)

// TestMutants pins the operator set and determinism (REQ-mut-operators,
// REQ-mut-budget): sites in source order, the budget respected, identical
// runs identical, no two mutants of one symbol rendering the same source.
func TestMutants(t *testing.T) {
	tr := fixtureTree(t)
	ms, err := tr.Mutants("example.com/fixture/lib.Add", 0)
	if err != nil {
		t.Fatal(err)
	}
	ops := map[string]bool{}
	for _, m := range ms {
		ops[m.Operator] = true
		if m.Position == "" || len(m.Source) == 0 || m.File == "" {
			t.Fatalf("incomplete mutant: %+v", m)
		}
	}
	for _, want := range []string{"== -> !=", "negate condition", "zero return"} {
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
		"increment literal", "continue -> break", "force false",
		"|| -> &&", "&& -> ||", "force true", "++ -> --",
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
			key := string(m.Source)
			if prev, dup := seen[key]; dup {
				t.Fatalf("%s: mutants %s and %s render identically", symbol, prev, m.Position+" "+m.Operator)
			}
			seen[key] = m.Position + " " + m.Operator
		}
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
		if m.Operator == "delete statement" && !strings.Contains(string(m.Source), `"fmt"`) {
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

// TestMutantsBodyless pins the no-body edge: a bodyless (assembly) symbol
// yields no mutants and no error — nothing to mutate is not a failure.
func TestMutantsBodyless(t *testing.T) {
	tr := fixtureTree(t)
	ms, err := tr.Mutants("example.com/fixture/bodyless.Ext", 0)
	if err != nil || len(ms) != 0 {
		t.Fatalf("bodyless: %d mutants, err %v", len(ms), err)
	}
}
