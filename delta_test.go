package gomutant

import (
	"context"
	"testing"
)

// A record's open survivors split by the cut through the mutated
// symbol's package directory, and only when the record can be placed:
// the symbol is one the cut names canonically changed and the record's
// body is the symbol's current one. A symbol the cut does not name, a
// stale body, an unresolved package, an unreadable position, or an
// empty cut all count as remainder — the cut never widens the delta
// it cannot place (REQ-exec-run-status).
func TestCutSurvivorsPlacesOnlyCurrentRecordsOfChangedSymbols(t *testing.T) {
	tr := fixtureTree(t)
	ctx := context.Background()
	current, err := tr.eng.BodyHashContext(ctx, "example.com/fixture/lib.Weak")
	if err != nil {
		t.Fatal(err)
	}
	cut := DeltaCut{Ref: "HEAD", Added: map[string][]LineRange{"lib/lib.go": {{From: 39, To: 41}}}, Changed: map[string]bool{"example.com/fixture/lib.Weak": true}}
	f := Finding{Symbol: "example.com/fixture/lib.Weak", BodyHash: current, Survivors: []Survivor{
		{Position: "lib.go:39:5", Operator: "condition: negate"},
		{Position: "lib.go:40:3#2", Operator: "statement: delete"},
		{Position: "lib.go:42:2", Operator: "statement: delete"},
		{Position: "garbage", Operator: "x"},
	}}
	split, err := tr.CutSurvivorsContext(ctx, f, cut)
	if err != nil {
		t.Fatal(err)
	}
	if len(split.OnDelta) != 2 || split.OnDelta[0].Position != "lib.go:39:5" || split.OnDelta[1].Position != "lib.go:40:3#2" {
		t.Fatalf("on delta = %+v, want the two survivors on lines 39-40", split.OnDelta)
	}
	if len(split.Remainder) != 2 {
		t.Fatalf("remainder = %+v, want the line-42 survivor and the unreadable position", split.Remainder)
	}
	// The marks follow the record's open order, whatever that is.
	for i, s := range f.Open() {
		onDelta := s.Position == "lib.go:39:5" || s.Position == "lib.go:40:3#2"
		if split.IsOnDelta(i) != onDelta {
			t.Fatalf("mark for %s = %v, want %v", s.Position, split.IsOnDelta(i), onDelta)
		}
	}
	// An attested survivor is not open and is cut nowhere.
	attested := f
	attested.Attested = []Attestation{{Position: "lib.go:39:5", Operator: "condition: negate", Reason: "equivalent"}}
	if split, err = tr.CutSurvivorsContext(ctx, attested, cut); err != nil || len(split.OnDelta) != 1 {
		t.Fatalf("attested survivor still cut: %+v, %v", split.OnDelta, err)
	}
	allRemainder := func(name string, f Finding, cut DeltaCut) {
		t.Helper()
		split, err := tr.CutSurvivorsContext(ctx, f, cut)
		if err != nil || len(split.OnDelta) != 0 || len(split.Remainder) != len(f.Open()) || split.IsOnDelta(0) {
			t.Fatalf("%s: cut = %+v, %v; want everything as remainder", name, split, err)
		}
	}
	stale := f
	stale.BodyHash = "measured against another body"
	allRemainder("stale body", stale, cut)
	unnamed := DeltaCut{Ref: "HEAD", Added: cut.Added, Changed: map[string]bool{"example.com/fixture/lib.Other": true}}
	allRemainder("symbol the cut does not name", f, unnamed)
	unresolved := f
	unresolved.Symbol = "example.com/nowhere.Gone"
	allRemainder("unresolved package", unresolved, DeltaCut{Ref: "HEAD", Added: cut.Added, Changed: map[string]bool{"example.com/nowhere.Gone": true}})
	allRemainder("empty cut", f, DeltaCut{Ref: "HEAD", Changed: cut.Changed})
}
