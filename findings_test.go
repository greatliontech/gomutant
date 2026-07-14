package gomutant

import "testing"

func TestSameAttestationPins(t *testing.T) {
	target := SubjectEvidence{Symbol: "p.F", MaximalClosure: "f", RuntimeInputs: "manifest", RuntimeDigest: "digest"}
	oracle := SubjectEvidence{Symbol: "p.TestF", MaximalClosure: "test", RuntimeInputs: "manifest", RuntimeDigest: "digest"}
	secondOracle := SubjectEvidence{Symbol: "p.TestG", MaximalClosure: "test-g", RuntimeInputs: "manifest", RuntimeDigest: "digest"}
	base := Finding{OperatorSet: "go/2", Budget: 3, OracleTimeout: "1m0s", TargetEvidence: target, OracleEvidence: []SubjectEvidence{oracle, secondOracle}}
	reordered := base
	reordered.OracleEvidence = []SubjectEvidence{secondOracle, oracle}
	if !sameAttestationPins(base, reordered) {
		t.Fatal("identical pins did not match")
	}
	cases := []struct {
		name string
		mut  func(*Finding)
	}{
		{"operator set", func(f *Finding) { f.OperatorSet = "go/3" }},
		{"oracle selection", func(f *Finding) { f.OracleExplicit = !f.OracleExplicit }},
		{"budget", func(f *Finding) { f.Budget = 2 }},
		{"candidate count", func(f *Finding) { f.CandidateCount = 1 }},
		{"generated candidates", func(f *Finding) { f.Generated = 1 }},
		{"oracle timeout", func(f *Finding) { f.OracleTimeout = "2m0s" }},
		{"target evidence", func(f *Finding) { f.TargetEvidence.RuntimeDigest = "moved" }},
		{"oracle evidence", func(f *Finding) { f.OracleEvidence[0].RuntimeDigest = "moved" }},
		{"oracle removed", func(f *Finding) { f.OracleEvidence = nil }},
		{"oracle duplicated", func(f *Finding) { f.OracleEvidence = []SubjectEvidence{oracle, oracle} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			current := base
			current.OracleEvidence = append([]SubjectEvidence(nil), base.OracleEvidence...)
			tc.mut(&current)
			if sameAttestationPins(base, current) {
				t.Fatal("moved pin matched")
			}
		})
	}
}

func TestFindingDispositionViewsAreCanonical(t *testing.T) {
	finding := Finding{
		Survivors: []Survivor{
			{Position: "z.go:2:1", Operator: "b"}, {Position: "a.go:1:1", Operator: "z"},
			{Position: "m.go:1:1", Operator: "b"}, {Position: "a.go:1:1", Operator: "c"},
			{Position: "a.go:1:1", Operator: "b"}, {Position: "a.go:1:1", Operator: "a"},
		},
		Attested: []Attestation{
			{Position: "z.go:2:1", Operator: "b", Reason: "third"},
			{Position: "a.go:1:1", Operator: "z", Reason: "second"},
			{Position: "a.go:1:1", Operator: "a", Reason: "first"},
		},
	}
	open := finding.Open()
	if len(open) != 3 || open[0].Operator != "b" || open[1].Operator != "c" || open[2].Position != "m.go:1:1" {
		t.Fatalf("open = %+v", open)
	}
	attested := finding.AttestedDispositions()
	if len(attested) != 3 || attested[0].Position != "a.go:1:1" || attested[0].Operator != "a" || attested[1].Operator != "z" || attested[2].Position != "z.go:2:1" {
		t.Fatalf("attested = %+v", attested)
	}
	if finding.Survivors[0].Position != "z.go:2:1" || finding.Attested[0].Position != "z.go:2:1" {
		t.Fatal("canonical views mutated the finding record")
	}
}

// TestBudgetCovers pins the budget coverage rule (REQ-mut-budget): an
// exhaustive record answers anything; a capped record never answers an
// exhaustive or larger request.
func TestBudgetCovers(t *testing.T) {
	cases := []struct {
		finding Finding
		req     int
		want    bool
	}{
		{Finding{CandidateCount: 5, Generated: 5}, 0, true},
		{Finding{CandidateCount: 5, Generated: 5}, 9, true},
		{Finding{CandidateCount: 9, Generated: 5}, 0, false},
		{Finding{CandidateCount: 9, Generated: 5}, 5, true},
		{Finding{CandidateCount: 9, Generated: 5}, 3, true},
		{Finding{CandidateCount: 9, Generated: 5}, 6, false},
	}
	for _, c := range cases {
		if got := budgetCovers(c.finding, c.req); got != c.want {
			t.Errorf("budgetCovers(%+v, %d) = %v, want %v", c.finding, c.req, got, c.want)
		}
	}
}

// TestAttributedKill pins the oracle as sole arbiter (REQ-target-oracle):
// oracle members, timeouts, and probe-confirmed package failures attribute;
// any other killer aborts.
func TestAttributedKill(t *testing.T) {
	oracle := map[string]bool{"p.TestA": true}
	if err := attributedKill("p.TestA", oracle); err != nil {
		t.Fatalf("oracle member rejected: %v", err)
	}
	if err := attributedKill(TimeoutKiller, oracle); err != nil {
		t.Fatalf("timeout rejected: %v", err)
	}
	if err := attributedKill(PackageKillerPrefix+"p)", oracle); err != nil {
		t.Fatalf("package failure rejected: %v", err)
	}
	if err := attributedKill("p.TestOutsider", oracle); err == nil {
		t.Fatal("a killer outside the oracle attributed")
	}
}
