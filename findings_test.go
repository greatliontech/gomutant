package gomutant

import "testing"

// TestPinsMatch pins the staleness predicate leg by leg (REQ-result-stale):
// any moved pin — body, oracle membership or content, operator set,
// toolchain — re-measures; order of oracle pins never matters.
func TestPinsMatch(t *testing.T) {
	oracle := []OraclePin{{Symbol: "p.TestA", Hash: "ha"}, {Symbol: "p.TestB", Hash: "hb"}}
	base := Finding{BodyHash: "body", OperatorSet: "go/2", Toolchain: "tc",
		Oracle: []OraclePin{{Symbol: "p.TestB", Hash: "hb"}, {Symbol: "p.TestA", Hash: "ha"}}} // reordered
	if !pinsMatch(base, "body", oracle, "go/2", "tc") {
		t.Fatal("matching pins (reordered oracle) read as stale")
	}
	cases := []struct {
		name string
		mut  func(f *Finding)
	}{
		{"body moved", func(f *Finding) { f.BodyHash = "other" }},
		{"operator set moved", func(f *Finding) { f.OperatorSet = "go/3" }},
		{"toolchain moved", func(f *Finding) { f.Toolchain = "tc2" }},
		{"oracle test strengthened", func(f *Finding) { f.Oracle[0].Hash = "hb2" }},
		{"oracle test renamed", func(f *Finding) { f.Oracle[0].Symbol = "p.TestC" }},
		{"oracle test dropped", func(f *Finding) { f.Oracle = f.Oracle[:1] }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			prior := base
			prior.Oracle = append([]OraclePin(nil), base.Oracle...)
			c.mut(&prior)
			if pinsMatch(prior, "body", oracle, "go/2", "tc") {
				t.Fatal("moved pin still matched")
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
