package gomutant

import (
	"context"
	"encoding/hex"
	"strings"
	"testing"
)

// A record carries the identity of the run that last measured any of
// its candidates: a fresh measure stamps it, a serve keeps the
// measuring run's, and a budget extension re-stamps it
// (REQ-result-record).
func TestRunStampsTheMeasuringRunIdentity(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	ctx := context.Background()
	target := Target{Symbol: "example.com/fixture/lib.Weak", Oracle: []string{"example.com/fixture/lib.TestWeak"}}
	measured, err := tr.Run(ctx, []Target{target}, Options{Budget: 1, RunID: "run-one"})
	if err != nil {
		t.Fatal(err)
	}
	if measured[0].Skipped != "" {
		t.Fatalf("fixture target skipped: %s", measured[0].Skipped)
	}
	if measured[0].Run != "run-one" {
		t.Fatalf("measured record run = %q, want the run's identity", measured[0].Run)
	}
	served, err := tr.Run(ctx, []Target{target}, Options{Budget: 1, RunID: "run-two", Prior: measured})
	if err != nil {
		t.Fatal(err)
	}
	if !served[0].Cached || served[0].Run != "run-one" {
		t.Fatalf("served record = cached %v run %q, want a serve keeping run-one", served[0].Cached, served[0].Run)
	}
	extended, err := tr.Run(ctx, []Target{target}, Options{Budget: 3, RunID: "run-three", Prior: served})
	if err != nil {
		t.Fatal(err)
	}
	if extended[0].Cached || extended[0].Run != "run-three" {
		t.Fatalf("extended record = cached %v run %q, want the extending run's identity", extended[0].Cached, extended[0].Run)
	}
}

// An empty RunID is minted: a library caller passing none still gets
// records that name their run, and the minted form is the documented
// one (REQ-result-record).
func TestRunMintsAnIdentityWhenNoneIsGiven(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tr := fixtureTree(t)
	target := Target{Symbol: "example.com/fixture/lib.Weak", Oracle: []string{"example.com/fixture/lib.TestWeak"}}
	measured, err := tr.Run(context.Background(), []Target{target}, Options{Budget: 1})
	if err != nil {
		t.Fatal(err)
	}
	if measured[0].Skipped != "" {
		t.Fatalf("fixture target skipped: %s", measured[0].Skipped)
	}
	assertRunIDShape(t, measured[0].Run)
}

func assertRunIDShape(t *testing.T, id string) {
	t.Helper()
	if len(id) != 16 {
		t.Fatalf("run identity %q is not 16 characters", id)
	}
	if _, err := hex.DecodeString(id); err != nil || strings.ToLower(id) != id {
		t.Fatalf("run identity %q is not lower-case hex", id)
	}
}

// Two minted identities differ, and each has the documented shape.
func TestNewRunIDIsFreshHex(t *testing.T) {
	a, b := NewRunID(), NewRunID()
	assertRunIDShape(t, a)
	assertRunIDShape(t, b)
	if a == b {
		t.Fatalf("two minted run identities coincide: %s", a)
	}
}

// The run identity is audit beside the provenance, never an
// attestation pin: records differing only in it keep their
// dispositions (REQ-attest-survivor).
func TestAttestationPinsIgnoreTheRunIdentity(t *testing.T) {
	base := Finding{Symbol: "p.S", OperatorSet: "go/12", OracleTimeout: "1m0s",
		TargetEvidence: SubjectEvidence{Symbol: "p.S", MaximalClosure: "h"},
		OracleEvidence: []SubjectEvidence{{Symbol: "p.T", MaximalClosure: "o"}}, Run: "run-one"}
	other := base
	other.Run = "run-two"
	if !sameAttestationPins(base, other) {
		t.Fatal("a differing run identity shed attestation pins")
	}
}

// The identity persists under the wire name `run` and round-trips
// through the document (REQ-result-export).
func TestRunIdentityRoundTripsThroughTheDocument(t *testing.T) {
	f := storeFinding("p.S", func(f *Finding) { f.Run = "run-one" })
	doc, err := Export([]Finding{f}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), `"run":"run-one"`) && !strings.Contains(string(doc), `"run": "run-one"`) {
		t.Fatalf("document does not carry the run under its wire name: %s", doc)
	}
	parsed, err := ParseFindings(doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed) != 1 || parsed[0].Run != "run-one" {
		t.Fatalf("parsed run = %+v, want run-one", parsed)
	}
}

// A run under an exported GOMAXPROCS wider than the oracle width
// measures: the cap replaces the ambient entry, so the producer env
// gofresh records carries the key once (REQ-exec-oracle-parallelism;
// a duplicate key made every target skip as evidence-unavailable).
func TestRunMeasuresUnderAnExportedWiderGOMAXPROCS(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	t.Setenv("GOMAXPROCS", "1024")
	tr := fixtureTree(t)
	target := Target{Symbol: "example.com/fixture/lib.Weak", Oracle: []string{"example.com/fixture/lib.TestWeak"}}
	measured, err := tr.Run(context.Background(), []Target{target}, Options{Budget: 1, Jobs: 2})
	if err != nil {
		t.Fatal(err)
	}
	if measured[0].Skipped != "" {
		t.Fatalf("fixture target skipped under an exported GOMAXPROCS: %s", measured[0].Skipped)
	}
}

// A caller-supplied identity the document and the line-oriented faces
// cannot carry verbatim is refused before any load (REQ-result-record).
func TestRunRefusesAnUncarriableRunIdentity(t *testing.T) {
	for _, id := range []string{"has\nnewline", "has space", strings.Repeat("x", 65), "tab\there"} {
		if err := validateRunID(id); err == nil {
			t.Errorf("run identity %q admitted", id)
		}
	}
	for _, id := range []string{"run-one", "a.b_c-D9", NewRunID(), strings.Repeat("x", 64)} {
		if err := validateRunID(id); err != nil {
			t.Errorf("run identity %q refused: %v", id, err)
		}
	}
	tr := fixtureTree(t)
	target := Target{Symbol: "example.com/fixture/lib.Weak", Oracle: []string{"example.com/fixture/lib.TestWeak"}}
	if _, err := tr.Run(context.Background(), []Target{target}, Options{Budget: 1, RunID: "bad\nid"}); err == nil || !strings.Contains(err.Error(), "run identity") {
		t.Fatalf("run under an uncarriable identity = %v, want the refusal", err)
	}
}
