package gomutant

import "testing"

func TestSameAttestationPins(t *testing.T) {
	target := SubjectEvidence{Symbol: "p.F", MaximalClosure: "f", RuntimeInputs: "manifest", RuntimeDigest: "digest"}
	oracle := SubjectEvidence{Symbol: "p.TestF", MaximalClosure: "test", RuntimeInputs: "manifest", RuntimeDigest: "digest"}
	secondOracle := SubjectEvidence{Symbol: "p.TestG", MaximalClosure: "test-g", RuntimeInputs: "manifest", RuntimeDigest: "digest"}
	base := Finding{OperatorSet: "go/2", Budget: 3, Timeout: "1m0s", TargetEvidence: target, OracleEvidence: []SubjectEvidence{oracle, secondOracle}}
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
		{"budget", func(f *Finding) { f.Budget = 2 }},
		{"timeout", func(f *Finding) { f.Timeout = "2m0s" }},
		{"target evidence", func(f *Finding) { f.TargetEvidence.RuntimeDigest = "moved" }},
		{"oracle evidence", func(f *Finding) { f.OracleEvidence[0].RuntimeDigest = "moved" }},
		{"oracle removed", func(f *Finding) { f.OracleEvidence = nil }},
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

// TestBudgetCovers pins the budget coverage rule (REQ-mut-budget): an
// exhaustive record answers anything; a capped record never answers an
// exhaustive or larger request.
func TestBudgetCovers(t *testing.T) {
	cases := []struct {
		recorded, req int
		want          bool
	}{
		{0, 0, true}, {0, 5, true}, // exhaustive answers anything
		{5, 0, false},              // capped never answers exhaustive
		{5, 5, true}, {5, 3, true}, // covers equal or smaller
		{5, 6, false}, // never more than it generated
	}
	for _, c := range cases {
		if got := budgetCovers(c.recorded, c.req); got != c.want {
			t.Errorf("budgetCovers(%d, %d) = %v, want %v", c.recorded, c.req, got, c.want)
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
