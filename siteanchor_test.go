package gomutant

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gomutant/internal/engine"
)

// The attestation anchor keys site content beside position and operator:
// a same-shaped mutant at a different site never inherits a disposition
// through the concurrent-attest graft, the refusal is surfaced as a
// shed, and a pre-site disposition adopts the matched survivor's site
// (REQ-attest-survivor).
func TestGraftAttestationsAnchorsOnSiteContent(t *testing.T) {
	survivors := []Survivor{{Position: "f.go:3:2", Operator: "comparison: > -> >=", Site: "aaaa1111aaaa1111"}}

	// Same position and operator, moved site: the surviving mutant is a
	// neighbor shifted into the old coordinates - refused and surfaced.
	prior := []Attestation{{Position: "f.go:3:2", Operator: "comparison: > -> >=", Reason: "boundary equivalent", Site: "bbbb2222bbbb2222"}}
	got, shed := graftAttestations(prior, nil, survivors)
	if len(got) != 0 {
		t.Fatalf("cross-site disposition inherited: %+v", got)
	}
	if len(shed) != 1 || shed[0].Position != "f.go:3:2" || !strings.Contains(shed[0].Reason, "site content changed") {
		t.Fatalf("cross-site shed not surfaced: %+v", shed)
	}

	// Matching site grafts.
	prior[0].Site = "aaaa1111aaaa1111"
	got, shed = graftAttestations(prior, nil, survivors)
	if len(got) != 1 || len(shed) != 0 {
		t.Fatalf("same-site graft = %+v, shed %+v", got, shed)
	}

	// A pre-site disposition (empty site) matches by position and
	// operator and adopts the survivor's site.
	prior[0].Site = ""
	got, shed = graftAttestations(prior, nil, survivors)
	if len(got) != 1 || got[0].Site != "aaaa1111aaaa1111" || len(shed) != 0 {
		t.Fatalf("grandfathered disposition = %+v, shed %+v", got, shed)
	}
}

// Attest stamps the named survivor's site onto the recorded disposition,
// and the merge surface reports cross-site sheds by symbol.
func TestAttestStampsSiteAndMergeReportsSheds(t *testing.T) {
	f := Finding{Symbol: "pkg.F", Survivors: []Survivor{{Position: "f.go:3:2", Operator: "op", Site: "cafe0123cafe0123"}}}
	if err := f.Attest("f.go:3:2", "op", "equivalent"); err != nil {
		t.Fatal(err)
	}
	if f.Attested[0].Site != "cafe0123cafe0123" {
		t.Fatalf("attest did not stamp the survivor's site: %+v", f.Attested[0])
	}

	prior := []Finding{{Symbol: "pkg.F", Survivors: f.Survivors, Attested: f.Attested}}
	fresh := []Finding{{Symbol: "pkg.F", Survivors: []Survivor{{Position: "f.go:3:2", Operator: "op", Site: "d00d4567d00d4567"}}}}
	merged, shed := MergeFindingsShed(prior, fresh)
	if len(merged) != 1 || len(merged[0].Attested) != 0 {
		t.Fatalf("cross-site disposition survived the merge: %+v", merged)
	}
	if len(shed) != 1 || shed[0].Symbol != "pkg.F" {
		t.Fatalf("merge did not report the shed with its symbol: %+v", shed)
	}

	// The two-phase document flow (incremental per-target commit, then
	// the final merge): the shed happens at the COMMIT merge - the final
	// merge sees an already-stripped document and reports nothing - so
	// run surfaces must collect sheds from the commit phase or they are
	// silent (REQ-attest-survivor).
	committed, commitShed := MergeFindingsShed(prior, fresh)
	if len(commitShed) != 1 {
		t.Fatalf("commit-phase merge did not shed: %+v", commitShed)
	}
	if _, finalShed := MergeFindingsShed(committed, fresh); len(finalShed) != 0 {
		t.Fatalf("final merge over the stripped document re-shed: %+v", finalShed)
	}
}

// A version-4 document (pre-site) still parses: its siteless survivors
// and dispositions are the grandfathered match-by-position form
// (REQ-result-tolerant).
func TestParseFindingsAcceptsVersionFour(t *testing.T) {
	data, err := Export([]Finding{survivorFinding("pkg.F")})
	if err != nil {
		t.Fatal(err)
	}
	current := fmt.Sprintf("\"version\": %d", DocumentVersion)
	if !strings.Contains(string(data), current) {
		t.Fatalf("fixture does not carry the current version:\n%s", data)
	}
	old := strings.Replace(string(data), current, "\"version\": 4", 1)
	parsed, err := ParseFindings([]byte(old))
	if err != nil {
		t.Fatalf("version-4 document refused: %v", err)
	}
	if len(parsed) != 1 || parsed[0].Survivors[0].Site != "" {
		t.Fatalf("version-4 parse = %+v", parsed)
	}
	if _, err := ParseFindings([]byte(strings.Replace(string(data), current, "\"version\": 3", 1))); err == nil {
		t.Fatal("version-3 document accepted")
	}
	future := fmt.Sprintf("\"version\": %d", DocumentVersion+1)
	if _, err := ParseFindings([]byte(strings.Replace(string(data), current, future, 1))); err == nil {
		t.Fatal("future-version document accepted")
	}
}

// A same-shaped neighbor shifted into an attested mutant's exact
// coordinates by an edit never inherits the disposition: the fresh
// survivor at the old position and operator carries different site
// content, so the merge sheds the attestation and surfaces the shed —
// the cross-site inheritance channel the site anchor closes
// (REQ-attest-survivor).
func TestAttestationNeverInheritsAcrossShiftedSameShapedSites(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test per mutant")
	}
	tmp := t.TempDir()
	if err := os.CopyFS(tmp, os.DirFS(fixtureDir)); err != nil {
		t.Fatal(err)
	}
	twinPath := filepath.Join(tmp, "lib", "twin.go")
	twin := "package lib\n\nfunc Twin(a, b int) int {\n\tif a > b {\n\t\treturn a\n\t}\n\tif a > b {\n\t\treturn b\n\t}\n\treturn 0\n}\n"
	if err := os.WriteFile(twinPath, []byte(twin), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "lib", "twin_test.go"), []byte("package lib\n\nimport \"testing\"\n\nfunc TestTwin(t *testing.T) {\n\tTwin(1, 2)\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tr, err := Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	targets := []Target{{Symbol: "example.com/fixture/lib.Twin", Oracle: []string{"example.com/fixture/lib.TestTwin"}}}
	first, err := tr.Run(ctx, targets, Options{})
	if err != nil {
		t.Fatal(err)
	}
	rec := first[0]
	// Attest a surviving comparison mutant at the FIRST a > b site (line 4).
	var s0 *Survivor
	for i, s := range rec.Survivors {
		if strings.HasPrefix(s.Position, "twin.go:4:") {
			s0 = &rec.Survivors[i]
			break
		}
	}
	if s0 == nil {
		t.Fatalf("no survivor at the first site: %+v", rec.Survivors)
	}
	if s0.Site == "" {
		t.Fatal("survivor carries no site anchor")
	}
	if err := rec.Attest(s0.Position, s0.Operator, "boundary equivalent at the first site"); err != nil {
		t.Fatal(err)
	}
	doc, err := Export([]Finding{rec})
	if err != nil {
		t.Fatal(err)
	}
	prior, err := ParseFindings(doc)
	if err != nil {
		t.Fatal(err)
	}

	// Delete the first if-block: the second same-shaped site shifts up
	// into the attested coordinates.
	shifted := strings.Replace(twin, "\tif a > b {\n\t\treturn a\n\t}\n", "", 1)
	if shifted == twin {
		t.Fatal("fixture edit failed")
	}
	if err := os.WriteFile(twinPath, []byte(shifted), 0o644); err != nil {
		t.Fatal(err)
	}
	tr2, err := Load(tmp)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := tr2.Run(ctx, targets, Options{Prior: prior})
	if err != nil {
		t.Fatal(err)
	}
	// The collision must actually occur: the neighbor now survives at
	// the attested position and operator - otherwise the test is
	// vacuous.
	collided := false
	for _, s := range fresh[0].Survivors {
		if s.Position == s0.Position && s.Operator == s0.Operator {
			collided = true
			if s.Site == s0.Site {
				t.Fatal("shifted neighbor carries the attested site - the anchor cannot discriminate")
			}
		}
	}
	if !collided {
		t.Fatalf("no survivor collided at %s %s; positions: %+v", s0.Position, s0.Operator, fresh[0].Survivors)
	}
	merged, shed := MergeFindingsShed(prior, fresh)
	for _, f := range merged {
		for _, a := range f.Attested {
			if a.Position == s0.Position && a.Operator == s0.Operator {
				t.Fatalf("cross-site disposition inherited: %+v", a)
			}
		}
	}
	if len(shed) == 0 || shed[0].Position != s0.Position {
		t.Fatalf("cross-site shed not surfaced: %+v", shed)
	}
}

// Evidence beats attestation with the killer named: only prior
// dispositions whose mutant the kill ledger names contradict; a
// still-surviving or vanished mutant's disposition is the carry and
// merge layers' business (REQ-attest-survivor).
func TestContradictKilledDispositions(t *testing.T) {
	prior := []Attestation{
		{Position: "f.go:1:1", Operator: "op", Reason: "still surviving"},
		{Position: "f.go:2:2", Operator: "op", Reason: "killed equivalent claim"},
		{Position: "f.go:3:3", Operator: "op", Reason: "vanished"},
	}
	survivors := []Survivor{{Position: "f.go:1:1", Operator: "op"}}
	kills := []Kill{{Position: "f.go:2:2", Operator: "op", Killer: "p.TestKiller"}}
	var got []AttestationContradiction
	contradictKilledDispositions("pkg.F", prior, survivors, kills, func(c AttestationContradiction) { got = append(got, c) })
	want := AttestationContradiction{Symbol: "pkg.F", Position: "f.go:2:2", Operator: "op", Killer: "p.TestKiller", Reason: "killed equivalent claim"}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("contradictions = %+v, want exactly the killed disposition with its killer", got)
	}
	// A nil emitter is a legal drop, never a panic.
	contradictKilledDispositions("pkg.F", prior, survivors, kills, nil)
}

// The carry gate is the mutation domain alone - the mutated body and the
// operator grammar - never a measurement pin: either component moving
// breaks the candidate identity a disposition is a judgment about
// (REQ-attest-survivor).
func TestMutationDomainHeld(t *testing.T) {
	base := Finding{BodyHash: "body", OperatorSet: "go/12", OracleTimeout: "1m0s"}
	if !mutationDomainHeld(base, base) {
		t.Fatal("identical domain not held")
	}
	pinsMoved := base
	pinsMoved.OracleTimeout = "2m0s"
	if !mutationDomainHeld(base, pinsMoved) {
		t.Fatal("a moved measurement pin broke the domain")
	}
	bodyMoved := base
	bodyMoved.BodyHash = "edited"
	if mutationDomainHeld(base, bodyMoved) {
		t.Fatal("a moved body held the domain")
	}
	operatorsMoved := base
	operatorsMoved.OperatorSet = "go/13"
	if mutationDomainHeld(base, operatorsMoved) {
		t.Fatal("a moved operator set held the domain")
	}
}

// carryAnchoredAttestations is the domain-hold carry's core: matched
// dispositions adopt their survivor's site, a moved site is a surfaced
// site shed, a vanished survivor's disposition lands in gone.
func TestCarryAnchoredAttestationsPartition(t *testing.T) {
	survivors := []Survivor{{Position: "f.go:3:2", Operator: "op", Site: "aaaa1111aaaa1111"}}
	prior := []Attestation{
		{Position: "f.go:3:2", Operator: "op", Reason: "match", Site: "aaaa1111aaaa1111"},
		{Position: "f.go:9:9", Operator: "op", Reason: "vanished"},
	}
	kept, siteSheds, gone := carryAnchoredAttestations(prior, survivors)
	if len(kept) != 1 || kept[0].Reason != "match" || len(siteSheds) != 0 || len(gone) != 1 || gone[0].Reason != "vanished" {
		t.Fatalf("partition = kept %+v siteSheds %+v gone %+v", kept, siteSheds, gone)
	}
	prior[0].Site = "bbbb2222bbbb2222"
	kept, siteSheds, gone = carryAnchoredAttestations(prior[:1], survivors)
	if len(kept) != 0 || len(siteSheds) != 1 || len(gone) != 0 {
		t.Fatalf("moved-site partition = kept %+v siteSheds %+v gone %+v", kept, siteSheds, gone)
	}
	prior[0].Site = ""
	kept, siteSheds, _ = carryAnchoredAttestations(prior[:1], survivors)
	if len(kept) != 1 || kept[0].Site != "aaaa1111aaaa1111" || len(siteSheds) != 0 {
		t.Fatalf("grandfathered adopt = kept %+v siteSheds %+v", kept, siteSheds)
	}
}

// The carve-out carries stamp (adopt) sites onto grandfathered pre-site
// dispositions - the pre-site window closes at first contact on every
// carry path.
func TestGrowCarryAdoptsSitesOntoGrandfatheredDispositions(t *testing.T) {
	runnable := []engine.Replacement{{File: "f.go", Source: []byte("x")}}
	candidates := []engine.Candidate{
		{Symbol: "p.F", Operator: "op-b", Position: "f.go:3:3", Site: "cafe0123cafe0123", Replacements: runnable},
	}
	rec := Finding{
		Symbol: "p.F", CandidateCount: 1, Generated: 1, Mutants: 1,
		Operators: []OperatorSummary{{Operator: "op-b", Generated: 1, Survived: 1}},
		Survivors: []Survivor{{Position: "f.go:3:3", Operator: "op-b", Execution: "executed-and-passed"}},
		Attested:  []Attestation{{Position: "f.go:3:3", Operator: "op-b", Reason: "pre-site disposition"}},
	}
	grown, shed, err := growFindingCounts(context.Background(), rec, candidates, map[int]bool{0: true}, []engine.MutantOutcome{engine.MutantSurvived}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(shed) != 0 || len(grown.Attested) != 1 || grown.Attested[0].Site != "cafe0123cafe0123" {
		t.Fatalf("grandfathered disposition not adopted on the growth carry: %+v shed %+v", grown.Attested, shed)
	}
	if len(grown.Survivors) != 1 || grown.Survivors[0].Site != "cafe0123cafe0123" {
		t.Fatalf("survivor site not restamped: %+v", grown.Survivors)
	}
}
