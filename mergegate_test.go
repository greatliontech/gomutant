package gomutant

import (
	"strings"
	"testing"
)

// The merge graft is pin-gated, not pin-blind: a disposition already on
// record at run start whose re-measure did not keep it sheds at merge
// instead of re-attaching, only a concurrent attestation grafts, and a
// disposition whose survivor vanished sheds with its mutant
// (REQ-attest-survivor).
func TestMergeGraftGatesDispositionCarryOnTheRunStartSnapshot(t *testing.T) {
	attestation := Attestation{Position: "f.go:3:2", Operator: "comparison: > -> >=", Reason: "boundary equivalent", Site: "aaaa1111aaaa1111"}
	survivor := Survivor{Position: attestation.Position, Operator: attestation.Operator, Site: attestation.Site}
	prior := []Finding{{Symbol: "pkg.F", Survivors: []Survivor{survivor}, Attested: []Attestation{attestation}}}
	snapshot := map[string][]Attestation{"pkg.F": {attestation}}

	// The re-measure reports the survivor again - same site - but did
	// not keep the disposition: its pins moved and the equivalence was
	// judged afresh. The pin-blind graft re-attached here; the gated
	// merge sheds loudly.
	fresh := []Finding{{Symbol: "pkg.F", Survivors: []Survivor{survivor}}}
	merged, shed := MergeFindingsShedAgainst(prior, fresh, snapshot)
	if len(merged) != 1 || len(merged[0].Attested) != 0 {
		t.Fatalf("rejected disposition re-attached across a pin move: %+v", merged)
	}
	if len(shed) != 1 || shed[0].Symbol != "pkg.F" || !strings.Contains(shed[0].Reason, "pins moved") {
		t.Fatalf("pin-move shed not surfaced: %+v", shed)
	}

	// A disposition recorded concurrently during the run - in the live
	// document but absent from the run-start snapshot - grafts onto the
	// still-reported survivor.
	merged, shed = MergeFindingsShedAgainst(prior, fresh, map[string][]Attestation{})
	if len(merged) != 1 || len(merged[0].Attested) != 1 || len(shed) != 0 {
		t.Fatalf("concurrent attestation did not graft: %+v shed %+v", merged, shed)
	}

	// A snapshotted disposition whose survivor's site also moved sheds
	// with the site reason: the specific cause outranks the general one.
	movedSite := Survivor{Position: attestation.Position, Operator: attestation.Operator, Site: "bbbb2222bbbb2222"}
	merged, shed = MergeFindingsShedAgainst(prior, []Finding{{Symbol: "pkg.F", Survivors: []Survivor{movedSite}}}, snapshot)
	if len(merged) != 1 || len(merged[0].Attested) != 0 || len(shed) != 1 || !strings.Contains(shed[0].Reason, "site content changed") {
		t.Fatalf("moved-site snapshotted disposition = %+v shed %+v, want the site reason", merged, shed)
	}

	// A same-mutant disposition whose content differs from the snapshot
	// entry was recorded concurrently - the user re-judged the
	// equivalence against the current record after the original shed -
	// and grafts instead of being mistaken for the rejected original.
	reattested := attestation
	reattested.Reason = "re-judged equivalent against the re-measured record"
	merged, shed = MergeFindingsShedAgainst([]Finding{{Symbol: "pkg.F", Survivors: []Survivor{survivor}, Attested: []Attestation{reattested}}}, fresh, snapshot)
	if len(merged) != 1 || len(merged[0].Attested) != 1 || merged[0].Attested[0].Reason != reattested.Reason || len(shed) != 0 {
		t.Fatalf("concurrent re-attestation mistaken for the rejected original: %+v shed %+v", merged, shed)
	}

	// A disposition the fresh record already carries rode the in-run
	// pin-hold carry: it stays and nothing sheds.
	carried := []Finding{{Symbol: "pkg.F", Survivors: []Survivor{survivor}, Attested: []Attestation{attestation}}}
	merged, shed = MergeFindingsShedAgainst(prior, carried, snapshot)
	if len(merged) != 1 || len(merged[0].Attested) != 1 || len(shed) != 0 {
		t.Fatalf("carried disposition disturbed: %+v shed %+v", merged, shed)
	}
}

// A disposition whose survivor the fresh record no longer reports sheds
// with its mutant - loudly, in every mode, snapshot or not - never
// silently dropped (REQ-attest-survivor).
func TestMergeShedsDispositionsOfVanishedSurvivorsLoudly(t *testing.T) {
	attestation := Attestation{Position: "f.go:3:2", Operator: "comparison: > -> >=", Reason: "boundary equivalent", Site: "aaaa1111aaaa1111"}
	prior := []Finding{{Symbol: "pkg.F", Survivors: []Survivor{{Position: attestation.Position, Operator: attestation.Operator, Site: attestation.Site}}, Attested: []Attestation{attestation}}}
	fresh := []Finding{{Symbol: "pkg.F"}}

	for name, snapshot := range map[string]map[string][]Attestation{
		"without snapshot": nil,
		"with snapshot":    {"pkg.F": {attestation}},
	} {
		merged, shed := MergeFindingsShedAgainst(prior, fresh, snapshot)
		if len(merged) != 1 || len(merged[0].Attested) != 0 {
			t.Fatalf("%s: vanished survivor's disposition survived: %+v", name, merged)
		}
		if len(shed) != 1 || !strings.Contains(shed[0].Reason, "no longer reported") {
			t.Fatalf("%s: vanished-survivor shed not surfaced: %+v", name, shed)
		}
	}
}

// A run surface renders the post-merge document row wherever one
// landed - a concurrent disposition shows, a shed one does not - with
// the run-only Cached and Skipped markers preserved from the run's own
// finding (REQ-mcp-findings-doc).
func TestRenderedFindingsSubstitutePostMergeRows(t *testing.T) {
	fresh := []Finding{
		{Symbol: "pkg.F", Cached: true, Skipped: "why"},
		{Symbol: "pkg.G", Mutants: 3},
	}
	merged := Finding{Symbol: "pkg.F", Attested: []Attestation{{Position: "f.go:1:1", Operator: "op", Reason: "equivalent"}}}
	rendered := RenderedFindings(fresh, map[string]Finding{"pkg.F": merged})
	if len(rendered) != 2 {
		t.Fatalf("rendered %d rows", len(rendered))
	}
	if len(rendered[0].Attested) != 1 || !rendered[0].Cached || rendered[0].Skipped != "why" {
		t.Fatalf("post-merge substitution lost the document row or the run markers: %+v", rendered[0])
	}
	if rendered[1].Mutants != 3 {
		t.Fatalf("unmerged row disturbed: %+v", rendered[1])
	}
}

// One shed mutant owes the reader one line: the first report - the
// in-run carry's specific cause - wins over the merge layer's rederived
// one (REQ-attest-survivor).
func TestDedupeAttestationShedsKeepsTheFirstReport(t *testing.T) {
	sheds := []AttestationShed{
		{Symbol: "pkg.F", Position: "f.go:3:2", Operator: "op", Reason: "site content changed under the position - the surviving mutant is not the attested one"},
		{Symbol: "pkg.F", Position: "f.go:3:2", Operator: "op", Reason: "attestation pins moved - the equivalence is judged afresh"},
		{Symbol: "pkg.G", Position: "g.go:1:1", Operator: "op", Reason: "attestation pins moved - the equivalence is judged afresh"},
	}
	got := DedupeAttestationSheds(sheds)
	if len(got) != 2 {
		t.Fatalf("dedupe kept %d sheds: %+v", len(got), got)
	}
	if !strings.Contains(got[0].Reason, "site content changed") || got[1].Symbol != "pkg.G" {
		t.Fatalf("first report did not win: %+v", got)
	}
}
