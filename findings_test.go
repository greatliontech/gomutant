package gomutant

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"
	"github.com/greatliontech/gofresh/runtimeinput"
)

func TestSubjectEvidencePreservesObservationProof(t *testing.T) {
	for _, observable := range []bool{false, true} {
		reason := ""
		if !observable {
			reason = "unobservable effect"
		}
		fingerprint := gofresh.Fingerprint{
			MaximalClosure: "closure", Guards: guard.Guards{Toolchain: "toolchain", BuildConfig: "build"},
			ObservationAssertion: "caller assertion",
			ObservationProof: gofresh.ObservationProof{
				Strategy: gofresh.ObservationRTA, Subject: gofresh.Subject{Package: "p", Symbol: "F"},
				Observable: observable, Reason: reason, Evidence: "proof",
			},
			PurityAssertion: "source directive", DynamicStateVouches: "a.example/dep.Var",
			PackageProcessDischarges: "a.example/wire.reg", DynamicStateStrategy: gofresh.DynamicStateStrategy,
			RuntimeInputs: "manifest", RuntimeDigest: "digest", ResultKind: gofresh.CodeResult,
		}
		evidence := evidenceFromFingerprint("p.F", fingerprint, runtimeinput.State{})
		if got := evidence.fingerprint(); got != fingerprint {
			t.Fatalf("observable %v round trip = %+v, want %+v", observable, got, fingerprint)
		}
	}
}

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
		{"property regime", func(f *Finding) { f.PropertyRegime = "rapid:nofailfile,seed=1" }},
		{"target evidence", func(f *Finding) { f.TargetEvidence.RuntimeDigest = "moved" }},
		{"dynamic-state strategy", func(f *Finding) { f.TargetEvidence.DynamicStateStrategy = "moved" }},
		{"observation proof", func(f *Finding) { f.TargetEvidence.ObservationEvidence = "moved" }},
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

// A version ahead of the reader names the probable cause and the
// restart signal - the recurring field shape is a long-lived MCP
// server outliving a binary upgrade, its surface dead while the reader
// hunts for document corruption (REQ-result-export).
func TestParseFindingsVersionAheadNamesProbableCause(t *testing.T) {
	_, err := ParseFindings([]byte(`{"version": 99, "findings": []}`))
	if err == nil || !strings.Contains(err.Error(), "newer gomutant likely wrote it") || !strings.Contains(err.Error(), "restart it on the upgraded binary") {
		t.Fatalf("version-ahead error = %v, want the probable cause and restart signal", err)
	}
	if _, err := ParseFindings([]byte(`{"version": 1, "findings": []}`)); err == nil || strings.Contains(err.Error(), "newer gomutant") {
		t.Fatalf("version-behind error = %v, want the plain range refusal", err)
	}
}

// The persisted evidence fields and the declared encoding inventory match
// in both directions: a new pin joining the struct without a
// subjectEvidenceFields row would skip the duplicate-key and null-field
// validations silently, and a stale row outliving a removed field would
// refuse every document if the row is required (REQ-result-record).
func TestSubjectEvidenceFieldInventoryIsComplete(t *testing.T) {
	declared := map[string]bool{}
	for _, field := range subjectEvidenceFields {
		declared[field.name] = true
	}
	typeOf := reflect.TypeFor[SubjectEvidence]()
	persisted := map[string]bool{}
	for i := 0; i < typeOf.NumField(); i++ {
		tag := strings.Split(typeOf.Field(i).Tag.Get("json"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		persisted[tag] = true
		if !declared[tag] {
			t.Errorf("persisted evidence field %q missing from subjectEvidenceFields", tag)
		}
	}
	for _, field := range subjectEvidenceFields {
		if !persisted[field.name] {
			t.Errorf("subjectEvidenceFields row %q names no persisted SubjectEvidence field", field.name)
		}
	}
}

// The audit and strategy evidence fields' wire spellings are pinned by
// golden keys: a tag renamed in lockstep with its inventory row would pass
// the struct-table checks while silently orphaning the field in every
// existing on-disk document (REQ-result-export). The required fields' wire
// names are pinned by the document-literal parse tests; these three are
// omitempty and appear in no literal.
func TestEvidenceWireNamesArePinned(t *testing.T) {
	raw, err := json.Marshal(SubjectEvidence{
		DynamicStateVouches:      "vouches",
		PackageProcessDischarges: "discharges",
		DynamicStateStrategy:     "strategy",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		`"dynamicStateVouches":"vouches"`,
		`"packageProcessDischarges":"discharges"`,
		`"dynamicStateStrategy":"strategy"`,
	} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("wire encoding lacks %s: %s", key, raw)
		}
	}
}

// The consumed gofresh Fingerprint surface is fully mapped: every
// non-exempt field, filled with a distinct non-zero value by reflection,
// must survive the evidence round trip — a name-presence check alone would
// pass a field neither conversion assigns (REQ-result-record).
func TestFingerprintSurfaceIsMappedOrExempt(t *testing.T) {
	// Exemptions with their grounds: SingleSubjectDischarges belongs to an
	// attestation gomutant never sets (single-subject execution), so the
	// round trip drops it; ResultKind is fixed at gofresh.CodeResult by
	// construction; Guards' Machine and RuntimeConfig are measurement
	// guards a code result never carries — its code guards (Toolchain,
	// BuildConfig) round-trip as columns.
	var fp gofresh.Fingerprint
	next := 0
	var fill func(v reflect.Value, path string)
	fill = func(v reflect.Value, path string) {
		switch v.Kind() {
		case reflect.String:
			next++
			v.SetString(fmt.Sprintf("distinct-%d", next))
		case reflect.Bool:
			v.SetBool(true)
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				fill(v.Field(i), path+"."+v.Type().Field(i).Name)
			}
		default:
			t.Fatalf("Fingerprint field %s has unhandled kind %s: extend the filler and the evidence mapping together", path, v.Kind())
		}
	}
	exempt := map[string]bool{"SingleSubjectDischarges": true, "ResultKind": true}
	fpValue := reflect.ValueOf(&fp).Elem()
	for i := 0; i < fpValue.NumField(); i++ {
		if name := fpValue.Type().Field(i).Name; exempt[name] {
			continue
		} else {
			fill(fpValue.Field(i), name)
		}
	}
	fp.Guards.Machine, fp.Guards.RuntimeConfig = "", ""
	// Both legs are valid record shapes (validateSubjectEvidence pairs
	// Observable with an empty Reason and vice versa); together they give
	// every field a non-zero leg, so a dropped assignment fails one of
	// them.
	for _, observable := range []bool{false, true} {
		leg := fp
		leg.ObservationProof.Observable = observable
		if observable {
			leg.ObservationProof.Reason = ""
		}
		ev := evidenceFromFingerprint("pkg.Sym", leg, runtimeinput.State{Unverifiable: true, Reason: "runtime reason"})
		raw, err := json.Marshal(ev)
		if err != nil {
			t.Fatalf("observable=%v: marshal: %v", observable, err)
		}
		if ok, verr := validateSubjectEvidence(raw); verr != nil || !ok {
			t.Errorf("observable=%v: fixture is not a valid record (ok=%v err=%v)", observable, ok, verr)
		}
		want := leg
		want.ResultKind = gofresh.CodeResult
		if got := ev.fingerprint(); !reflect.DeepEqual(got, want) {
			t.Errorf("observable=%v: fingerprint round trip dropped or altered a field:\n got %+v\nwant %+v", observable, got, want)
		}
		if ev.Symbol != "pkg.Sym" || !ev.RuntimeUnverifiable || ev.RuntimeReason != "runtime reason" {
			t.Errorf("observable=%v: evidence-only columns not carried: %+v", observable, ev)
		}
	}
}
