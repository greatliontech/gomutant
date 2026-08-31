package gomutant

import "testing"

// The package cut absorbs gopkg.in-style version elements and leaves
// method spellings alone: without the absorption a dark versioned
// package merged with its sibling and went unreported
// (REQ-result-skip-radius; the chunk-132 review's L2).
func TestSymbolPackageAbsorbsVersionElements(t *testing.T) {
	cases := map[string]string{
		"example.com/mod/pkg.Func":        "example.com/mod/pkg",
		"example.com/mod/pkg.Recv.Method": "example.com/mod/pkg",
		"gopkg.in/yaml.v3.Marshal":        "gopkg.in/yaml.v3",
		"gopkg.in/yaml.v3.Node.Decode":    "gopkg.in/yaml.v3",
		"example.com/mod.v12.sub.F":       "example.com/mod.v12",
		"pkg.F":                           "pkg",
	}
	for symbol, want := range cases {
		if got := symbolPackage(symbol); got != want {
			t.Fatalf("symbolPackage(%q) = %q, want %q", symbol, got, want)
		}
	}
}

// splitTestSymbol's input-class contract: over package-scope
// test-function symbols the last-dot cut is exact — dotted package
// path elements included, the case symbolPackage's arbitrary-symbol
// grammar must guess at. The method-valued row is the contract
// boundary's named anchor: such an input mis-splits here by design
// and belongs to symbolPackage instead.
func TestSplitTestSymbolHonorsItsInputClassContract(t *testing.T) {
	cases := []struct {
		symbol, pkg, fn string
	}{
		{"example.com/mod/pkg.TestX", "example.com/mod/pkg", "TestX"},
		{"example.com/mod/pkg.beta.TestX", "example.com/mod/pkg.beta", "TestX"},
		{"gopkg.in/yaml.v3.TestMarshal", "gopkg.in/yaml.v3", "TestMarshal"},
		{"nopath.TestX", "nopath", "TestX"},
		{"example.com/mod/pkg.Type.Method", "example.com/mod/pkg.Type", "Method"},
		{"pkg.", "pkg", ""},
		{"example.com/mod/nodot", "", ""},
		{"", "", ""},
	}
	for _, tc := range cases {
		pkg, fn := splitTestSymbol(tc.symbol)
		if pkg != tc.pkg || fn != tc.fn {
			t.Errorf("splitTestSymbol(%q) = %q, %q; want %q, %q", tc.symbol, pkg, fn, tc.pkg, tc.fn)
		}
	}
}

// The oracle-bound pin under derivation (REQ-result-stale's
// timeout-kill rule): exact agreement always matches; derived on both
// sides matches when every "(timeout)" kill carries its candidate-local
// evidence row (the flagged serve re-executes it under the current
// budget — the re-vouching route the relaxation rides) and refuses when
// any lacks one (a shaped record's wholesale serve has no such route);
// an explicit side keeps the exact pin.
func TestTimeoutPinMatchesTimeoutKillRule(t *testing.T) {
	clean := Finding{OracleTimeout: "2m0s", OracleTimeoutDerived: true, Kills: []Kill{{Killer: "p.TestA"}}}
	timedOut := Finding{
		OracleTimeout: "2m0s", OracleTimeoutDerived: true,
		Kills:             []Kill{{Position: "f.go:3:2", Operator: "unary assignment: -- -> ++", Killer: TimeoutKiller}},
		CandidateEvidence: []CandidateEvidence{{Position: "f.go:3:2", Operator: "unary assignment: -- -> ++", Reason: "mutant test process timed out", Disposition: "killed"}},
	}
	unroutedTimeout := Finding{OracleTimeout: "2m0s", OracleTimeoutDerived: true, Kills: []Kill{{Position: "f.go:3:2", Operator: "unary assignment: -- -> ++", Killer: TimeoutKiller}}}
	explicit := Finding{OracleTimeout: "1m0s"}

	if !timeoutPinMatches(explicit, "1m0s", false) {
		t.Fatal("exact explicit pin did not match")
	}
	if timeoutPinMatches(explicit, "2m0s", false) {
		t.Fatal("explicit pin matched across values")
	}
	if timeoutPinMatches(explicit, "1m0s", true) {
		t.Fatal("explicit record matched a derived run at the same value's spelling — the posture is part of the pin")
	}
	if !timeoutPinMatches(clean, "3m0s", true) {
		t.Fatal("derived record with completed verdicts re-measured on a budget change — completed verdicts are budget-independent")
	}
	if !timeoutPinMatches(timedOut, "3m0s", true) {
		t.Fatal("derived record with an evidence-backed timeout kill re-measured wholesale — the flagged serve re-executes it under the current budget, so the pin relaxes")
	}
	if timeoutPinMatches(unroutedTimeout, "3m0s", true) {
		t.Fatal("timeout kill without its evidence row served across a budget change — no re-execution route re-vouches the bound claim")
	}
	mismatched := timedOut
	mismatched.CandidateEvidence = []CandidateEvidence{{Position: "g.go:9:1", Operator: "comparison: > -> <", Reason: "mutant test process timed out", Disposition: "killed"}}
	if timeoutPinMatches(mismatched, "3m0s", true) {
		t.Fatal("timeout kill covered by another candidate's evidence row — the route must belong to the kill itself")
	}
	if !timeoutPinMatches(unroutedTimeout, "2m0s", true) {
		t.Fatal("unrouted timeout kill did not match its own exact bound")
	}
	if timeoutPinMatches(clean, "3m0s", false) {
		t.Fatal("derived record matched an explicit run")
	}

	if !timeoutPinMatchesRecord(clean, Finding{OracleTimeout: "5m0s", OracleTimeoutDerived: true}) {
		t.Fatal("record-to-record derived clean pair did not match")
	}
	if timeoutPinMatchesRecord(unroutedTimeout, Finding{OracleTimeout: "5m0s", OracleTimeoutDerived: true}) {
		t.Fatal("record-to-record unrouted timeout kill matched across bounds")
	}
	if timeoutPinMatchesRecord(clean, explicit) {
		t.Fatal("derived-vs-explicit records matched")
	}
}
