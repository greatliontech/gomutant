package gomutant

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
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
			PackageProcessDischarges: "a.example/wire.reg", DynamicStateStrategy: gofresh.DynamicStateStrategy, ClosureStrategy: gofresh.ClosureStrategy,
			RuntimeInputs: "manifest", RuntimeDigest: "digest", ResultKind: gofresh.CodeResult,
		}
		evidence := evidenceFromFingerprint("p.F", fingerprint, runtimeinput.State{})
		if got := evidence.fingerprint(); got != fingerprint {
			t.Fatalf("observable %v round trip = %+v, want %+v", observable, got, fingerprint)
		}
	}
}

func TestSameAttestationPins(t *testing.T) {
	target := SubjectEvidence{Symbol: "p.F", Fingerprint: gofresh.Fingerprint{MaximalClosure: "f", RuntimeInputs: "manifest", RuntimeDigest: "digest", ResultKind: gofresh.CodeResult}}
	oracle := SubjectEvidence{Symbol: "p.TestF", Fingerprint: gofresh.Fingerprint{MaximalClosure: "test", RuntimeInputs: "manifest", RuntimeDigest: "digest", ResultKind: gofresh.CodeResult}}
	secondOracle := SubjectEvidence{Symbol: "p.TestG", Fingerprint: gofresh.Fingerprint{MaximalClosure: "test-g", RuntimeInputs: "manifest", RuntimeDigest: "digest", ResultKind: gofresh.CodeResult}}
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
		{"observation proof", func(f *Finding) { f.TargetEvidence.ObservationProof.Evidence = "moved" }},
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

// TestSubjectEvidenceWireFormIsTheRecordBesideFourFields pins the row's
// wire form by its literal: the symbol, the fingerprint in gofresh's
// published record form under `fingerprint`, and gomutant's module
// base and runtime disposition — an unknown outer key, a null, and a
// flat pre-13 row refuse; a zero row carries no fingerprint
// (REQ-result-record, REQ-result-export).
func TestSubjectEvidenceWireFormIsTheRecordBesideFourFields(t *testing.T) {
	row := SubjectEvidence{Symbol: "p.F", Fingerprint: gofresh.Fingerprint{
		MaximalClosure: "m", TestVariantClosure: "v", Guards: guard.Guards{Toolchain: "go", BuildConfig: "b"},
		ObservationAssertion: "caller assertion",
		ObservationProof:     gofresh.ObservationProof{Strategy: "s", Subject: gofresh.Subject{Package: "p", Symbol: "F"}, Observable: false, Reason: "r", Evidence: "e"},
		PurityAssertion:      "source directive", DynamicStateVouches: "vouches", SingleSubjectDischarges: "single", PackageProcessDischarges: "discharges",
		DynamicStateStrategy: "strategy", ClosureStrategy: "closure strategy", RuntimeInputs: "manifest", RuntimeDigest: "digest", ResultKind: gofresh.CodeResult,
	}, ModuleBase: "sub", RuntimeUnverifiable: true, RuntimeReason: "why"}
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"symbol":"p.F","fingerprint":{"maximalClosure":"m","testVariantClosure":"v","toolchain":"go","buildConfig":"b","observationAssertion":"caller assertion","observationProof":{"strategy":"s","package":"p","symbol":"F","observable":false,"reason":"r","evidence":"e"},"purityAssertion":"source directive","dynamicStateVouches":"vouches","singleSubjectDischarges":"single","packageProcessDischarges":"discharges","dynamicStateStrategy":"strategy","closureStrategy":"closure strategy","runtimeInputs":"manifest","runtimeDigest":"digest","resultKind":1},"moduleBase":"sub","runtimeUnverifiable":true,"runtimeReason":"why"}`
	if string(raw) != want {
		t.Fatalf("wire form:\n got %s\nwant %s", raw, want)
	}
	var back SubjectEvidence
	if err := json.Unmarshal(raw, &back); err != nil || back != row {
		t.Fatalf("round trip: %v, %+v", err, back)
	}
	if ok, err := validateSubjectEvidence(raw); err != nil || !ok {
		t.Fatalf("a complete row judged %v, %v", ok, err)
	}
	// The zero row carries no fingerprint and decodes back to zero.
	zero, err := json.Marshal(SubjectEvidence{})
	if err != nil || string(zero) != `{"symbol":""}` {
		t.Fatalf("zero row = %s, %v", zero, err)
	}
	if err := json.Unmarshal(zero, &back); err != nil || back != (SubjectEvidence{}) {
		t.Fatalf("zero row round trip: %v, %+v", err, back)
	}
	// An unknown outer key is tolerated (REQ-result-tolerant) — a flat
	// pre-13 row decodes to a zero fingerprint and is incomplete, never
	// served; the fingerprint's own decoder is gofresh's, strict.
	var flat SubjectEvidence
	if err := json.Unmarshal([]byte(`{"symbol":"p.F","toolchain":"go"}`), &flat); err != nil || flat != (SubjectEvidence{Symbol: "p.F"}) {
		t.Fatalf("a flat row under the current shape: %v, %+v", err, flat)
	}
	if ok, err := validateSubjectEvidence([]byte(`{"symbol":"p.F","toolchain":"go"}`)); err != nil || ok {
		t.Fatalf("a flat row judged complete: %v, %v", ok, err)
	}
	for _, c := range []struct{ row, want string }{
		{`{"symbol":"p.F","fingerprint":null}`, "fingerprint is null"},
		{`{"symbol":null}`, "symbol is null"},
		{`{"symbol":"p.F","moduleBase":null}`, "moduleBase is null"},
		{`{"symbol":"p.F","runtimeReason":null}`, "runtimeReason is null"},
		{`{"symbol":"p.F","fingerprint":{"maximalClosure":"m","testVariantClosure":"v","toolchain":"go","buildConfig":"b","resultKind":1,"bogus":1}}`, `unknown field "bogus"`},
		{`{"symbol":"p.F","fingerprint":{"maximalClosure":"m","testVariantClosure":"v","toolchain":"go","buildConfig":"b"}}`, "invalid recorded result kind 0"},
	} {
		var e SubjectEvidence
		if err := json.Unmarshal([]byte(c.row), &e); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.row, err, c.want)
		}
	}
	// Completeness is gomutant's: an empty required pin, a disposition
	// without its reason, or an observable proof carrying one is an
	// incomplete row, never an error.
	for _, mutate := range []func(*SubjectEvidence){
		func(e *SubjectEvidence) { e.MaximalClosure = "" },
		func(e *SubjectEvidence) { e.Guards.BuildConfig = "" },
		func(e *SubjectEvidence) { e.ObservationProof.Evidence = "" },
		func(e *SubjectEvidence) { e.RuntimeDigest = "" },
		func(e *SubjectEvidence) { e.RuntimeReason = "" },
		func(e *SubjectEvidence) { e.ObservationProof.Reason = "" },
	} {
		e := row
		mutate(&e)
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		if ok, err := validateSubjectEvidence(raw); err != nil || ok {
			t.Errorf("mutated row judged complete: %+v (%v)", e, err)
		}
	}
}

// TestLegacyEvidenceRowsUpgradeOnRead reads a version-12 interned
// document whose evidence rows are the flat pre-record shape — the
// fingerprint's fields beside gomutant's, the proof flattened — and
// finds every row in the current shape with the code-result kind
// stamped, then refuses the same flat row inside a version-13 document
// (REQ-result-export).
func TestLegacyEvidenceRowsUpgradeOnRead(t *testing.T) {
	// The target row carries every optional legacy fact; the oracle row a
	// negative proof with its reason and the runtime disposition.
	flat := `{"symbol":"example.com/m.F","maximalClosure":"m","testVariantClosure":"v","toolchain":"go","buildConfig":"b","observationAssertion":"caller assertion","observationStrategy":"s","observationSubjectPackage":"example.com/m","observationSubjectSymbol":"F","observationObservable":true,"observationEvidence":"e","purityAssertion":"source directive","dynamicStateVouches":"a.b","packageProcessDischarges":"a.p","dynamicStateStrategy":"strategy","closureStrategy":"closure","moduleBase":"sub/mod","runtimeInputs":"","runtimeDigest":"digest","runtimeUnverifiable":true,"runtimeReason":"external directory input: /srv"}`
	oracle := `{"symbol":"example.com/m.TestF","maximalClosure":"m","testVariantClosure":"v","toolchain":"go","buildConfig":"b","observationAssertion":"caller assertion","observationStrategy":"s","observationSubjectPackage":"example.com/m","observationSubjectSymbol":"TestF","observationObservable":false,"observationReason":"reaches os.Getenv","observationEvidence":"e","dynamicStateStrategy":"strategy","runtimeInputs":"","runtimeDigest":"digest","runtimeUnverifiable":true,"runtimeReason":"external directory input: /srv"}`
	doc := `{"version":12,"runtimeInputsTable":["eyJ2IjoxfQ"],"evidenceTable":[{"evidence":` + flat + `,"runtimeInputs":0},{"evidence":` + oracle + `,"runtimeInputs":0}],"ledgerTable":[],"coverageBounds":[],"findings":[{"finding":{"symbol":"example.com/m.F","bodyHash":"h","operatorSet":"go/2","oracleTimeout":"1m0s","commit":"c0ffee","candidateCount":1,"generated":1,"mutants":1,"killed":1,"operators":[{"operator":"zero return","generated":1,"killed":1}]},"targetEvidence":0,"oracleEvidence":[1]}]}`
	parsed, err := ParseDocument([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Findings) != 1 {
		t.Fatalf("findings = %d", len(parsed.Findings))
	}
	target := parsed.Findings[0].TargetEvidence
	wantTarget := SubjectEvidence{Symbol: "example.com/m.F", Fingerprint: gofresh.Fingerprint{
		MaximalClosure: "m", TestVariantClosure: "v", Guards: guard.Guards{Toolchain: "go", BuildConfig: "b"},
		ObservationAssertion: "caller assertion",
		ObservationProof:     gofresh.ObservationProof{Strategy: "s", Subject: gofresh.Subject{Package: "example.com/m", Symbol: "F"}, Observable: true, Evidence: "e"},
		PurityAssertion:      "source directive", DynamicStateVouches: "a.b", PackageProcessDischarges: "a.p", DynamicStateStrategy: "strategy", ClosureStrategy: "closure",
		RuntimeInputs: "eyJ2IjoxfQ", RuntimeDigest: "digest", ResultKind: gofresh.CodeResult,
	}, ModuleBase: "sub/mod", RuntimeUnverifiable: true, RuntimeReason: "external directory input: /srv"}
	if target != wantTarget {
		t.Fatalf("upgraded target row:\n got %+v\nwant %+v", target, wantTarget)
	}
	wantOracle := SubjectEvidence{Symbol: "example.com/m.TestF", Fingerprint: gofresh.Fingerprint{
		MaximalClosure: "m", TestVariantClosure: "v", Guards: guard.Guards{Toolchain: "go", BuildConfig: "b"},
		ObservationAssertion: "caller assertion",
		ObservationProof:     gofresh.ObservationProof{Strategy: "s", Subject: gofresh.Subject{Package: "example.com/m", Symbol: "TestF"}, Observable: false, Reason: "reaches os.Getenv", Evidence: "e"},
		DynamicStateStrategy: "strategy", RuntimeInputs: "eyJ2IjoxfQ", RuntimeDigest: "digest", ResultKind: gofresh.CodeResult,
	}, RuntimeUnverifiable: true, RuntimeReason: "external directory input: /srv"}
	if len(parsed.Findings[0].OracleEvidence) != 1 || parsed.Findings[0].OracleEvidence[0] != wantOracle {
		t.Fatalf("upgraded oracle rows:\n got %+v\nwant %+v", parsed.Findings[0].OracleEvidence, wantOracle)
	}
	// A legacy row whose positive proof carries a reason is refused in the
	// row's own words (the old reader called it incomplete; the record
	// form's decoder would refuse it too).
	if _, err := ParseDocument([]byte(strings.Replace(doc, `"observationObservable":true,"observationEvidence":"e"`, `"observationObservable":true,"observationReason":"r","observationEvidence":"e"`, 1))); err == nil || !strings.Contains(err.Error(), "observable proof carries a reason") {
		t.Fatalf("a positive proof with a reason: %v", err)
	}
	// A flat row under the current version is the old shape in a new
	// document: its keys are tolerated and its fingerprint is empty, so
	// the record is incomplete — refused, never upgraded silently.
	if _, err := ParseDocument([]byte(strings.Replace(doc, `"version":12`, `"version":13`, 1))); err == nil || !strings.Contains(err.Error(), "missing or has invalid required evidence") {
		t.Fatalf("a flat row under version 13: %v", err)
	}
	// A legacy row's null still refuses.
	if _, err := ParseDocument([]byte(strings.Replace(doc, `"runtimeDigest":"digest","runtimeUnverifiable"`, `"runtimeDigest":null,"runtimeUnverifiable"`, 1))); err == nil || !strings.Contains(err.Error(), "null") {
		t.Fatalf("a legacy row with a null: %v", err)
	}
}

// The consumed gofresh Fingerprint surface is fully mapped: every
// non-exempt field, filled with a distinct non-zero value by reflection,
// must survive the evidence round trip — a name-presence check alone would
// pass a field neither conversion assigns (REQ-result-record).
func TestFingerprintSurfaceIsMappedOrExempt(t *testing.T) {
	// Exemptions with their grounds: ResultKind is the code result by
	// construction (an int the filler does not draw); Guards' Machine
	// and RuntimeConfig are measurement guards a code result never
	// carries and its record form refuses. With the record embedded the
	// fingerprint() leg is the field itself, so the round trip's live
	// half is the record encode and the completeness judgment over the
	// filled row — a field the record form could not carry fails there.
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
	exempt := map[string]bool{"ResultKind": true}
	fpValue := reflect.ValueOf(&fp).Elem()
	for i := 0; i < fpValue.NumField(); i++ {
		if name := fpValue.Type().Field(i).Name; exempt[name] {
			continue
		} else {
			fill(fpValue.Field(i), name)
		}
	}
	fp.Guards.Machine, fp.Guards.RuntimeConfig = "", ""
	fp.ResultKind = gofresh.CodeResult
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
		if got := ev.fingerprint(); !reflect.DeepEqual(got, want) {
			t.Errorf("observable=%v: fingerprint round trip dropped or altered a field:\n got %+v\nwant %+v", observable, got, want)
		}
		if ev.Symbol != "pkg.Sym" || !ev.RuntimeUnverifiable || ev.RuntimeReason != "runtime reason" {
			t.Errorf("observable=%v: evidence-only columns not carried: %+v", observable, ev)
		}
	}
}

// A package whose whole selected target set skipped is DARK - named in
// the radius and the summary - while a package with any surviving
// evidence never is: the blast radius a scattered count hides
// (REQ-result-run-reporting; the bldc campaign read 567 scattered
// skips over 14 fully dark packages).
func TestSkippedPackageRadiusNamesDarkPackages(t *testing.T) {
	findings := []Finding{
		{Symbol: "example.com/mod/dark.A", Skipped: "unsupported analysis shape: T"},
		{Symbol: "example.com/mod/dark.B", Skipped: "unsupported analysis shape: T"},
		{Symbol: "example.com/mod/mixed.C", Skipped: "oracle baseline probe failed"},
		{Symbol: "example.com/mod/mixed.D", Generated: 3, Mutants: 3, Killed: 3},
		{Symbol: "example.com/mod/clean.E", Generated: 2, Mutants: 2, Killed: 2},
		// A dotted package beside its sibling: the string cut would fold
		// dot.go's two skips into dot's three targets and hide a dark
		// package; the caller's resolution keeps them apart.
		{Symbol: "example.com/mod/dot.go.F", Skipped: "unsupported analysis shape: T"},
		{Symbol: "example.com/mod/dot.go.G", Skipped: "unsupported analysis shape: T"},
		{Symbol: "example.com/mod/dot.H", Generated: 2, Mutants: 2, Killed: 2},
	}
	packageOf := func(symbol string) string {
		if strings.HasPrefix(symbol, "example.com/mod/dot.go.") {
			return "example.com/mod/dot.go"
		}
		return symbolPackage(symbol)
	}
	radius := SkippedPackageRadius(findings, packageOf)
	if len(radius) != 3 {
		t.Fatalf("radius = %+v, want dark, dot.go and mixed only", radius)
	}
	if radius[0].Package != "example.com/mod/dark" || !radius[0].Dark() || radius[0].Targets != 2 {
		t.Fatalf("dark package misreported: %+v", radius[0])
	}
	if radius[1].Package != "example.com/mod/dot.go" || !radius[1].Dark() || radius[1].Targets != 2 {
		t.Fatalf("dotted dark package misreported: %+v", radius[1])
	}
	if radius[2].Package != "example.com/mod/mixed" || radius[2].Dark() {
		t.Fatalf("mixed package misreported as dark: %+v", radius[2])
	}
	summary := SummarizeRun(findings, Selection{}, packageOf)
	if strings.Join(summary.DarkPackages, ",") != "example.com/mod/dark,example.com/mod/dot.go" {
		t.Fatalf("summary dark packages = %+v, want exactly the two dark ones", summary.DarkPackages)
	}
	// Without a loaded tree the cut's guess groups: the dotted sibling
	// folds into dot and reads as partially skipped.
	if guessed := SummarizeRun(findings, Selection{}, nil); strings.Join(guessed.DarkPackages, ",") != "example.com/mod/dark" {
		t.Fatalf("tree-less dark packages = %+v, want the guess to name dark alone", guessed.DarkPackages)
	}
}

// AttestFinding is the one disposition both faces apply: the named
// record carries the attestation, the rows come back whole, and a
// symbol with no finding refuses.
func TestAttestFindingDispositionsTheNamedRecord(t *testing.T) {
	rows := []Finding{{Symbol: "a.B", Survivors: []Survivor{{Position: "p.go:1:1", Operator: "zero return"}}}, {Symbol: "a.C"}}
	all, attested, err := AttestFinding(rows, "a.B", "p.go:1:1", "zero return", "r")
	if err != nil || len(all) != 2 || attested.Symbol != "a.B" || len(attested.AttestedDispositions()) != 1 {
		t.Fatalf("AttestFinding = %v, %+v, %v", len(all), attested, err)
	}
	if all[1].Symbol != "a.C" || len(all[1].AttestedDispositions()) != 0 {
		t.Fatalf("the sibling row moved: %+v", all[1])
	}
	if _, _, err := AttestFinding(rows, "a.X", "p.go:1:1", "zero return", "r"); err == nil {
		t.Fatal("a symbol with no finding was attested")
	}
	fresh := []Finding{{Symbol: "a.B", Survivors: []Survivor{{Position: "p.go:1:1", Operator: "zero return"}}}}
	if _, _, err := AttestFinding(fresh, "a.B", "p.go:1:1", "zero return", "   "); err == nil || len(fresh[0].AttestedDispositions()) != 0 {
		t.Fatalf("a whitespace-only reasoning was recorded: %v", err)
	}
	if _, _, err := AttestFinding(nil, "a.B", "p.go:1:1", "zero return", "r"); err == nil {
		t.Fatal("empty rows attested a record")
	}
}

// Export writes the bounds table it is given, whole, and reads it back
// as written (REQ-result-export, REQ-result-unreached-bound's table).
func TestExportCarriesTheCoverageBounds(t *testing.T) {
	findings := []Finding{storeFinding("p.A", nil)}
	bounds := []CoverageBound{{Selection: "tags:wasm;toolchain:", Unreached: []string{"p.B"}}}
	data, err := Export(findings, bounds)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := ParseDocument(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.CoverageBounds) != 1 || doc.CoverageBounds[0].Selection != bounds[0].Selection || len(doc.CoverageBounds[0].Unreached) != 1 {
		t.Fatalf("bounds = %+v; want the one written", doc.CoverageBounds)
	}
	if again, err := Export(findings, nil); err != nil {
		t.Fatal(err)
	} else if doc, err := ParseDocument(again); err != nil || len(doc.CoverageBounds) != 0 {
		t.Fatalf("no bounds written, %d read (%v)", len(doc.CoverageBounds), err)
	}
}

// TestLegacyRowsAndTheUpgradeAreInverse ties the three spellings of the
// legacy shape together: the test-side downgrader emits exactly the
// key set the reader knows, and a full row survives downgrade then
// upgrade unchanged — so a legacy fact the upgrade dropped, or a key the
// downgrader forgot, fails here rather than silently (REQ-result-export).
func TestLegacyRowsAndTheUpgradeAreInverse(t *testing.T) {
	row := SubjectEvidence{Symbol: "p.F", Fingerprint: gofresh.Fingerprint{
		MaximalClosure: "m", TestVariantClosure: "v", Guards: guard.Guards{Toolchain: "go", BuildConfig: "b"},
		ObservationAssertion: "caller assertion",
		ObservationProof:     gofresh.ObservationProof{Strategy: "s", Subject: gofresh.Subject{Package: "p", Symbol: "F"}, Observable: false, Reason: "r", Evidence: "e"},
		PurityAssertion:      "source directive", DynamicStateVouches: "vouches", PackageProcessDischarges: "discharges",
		DynamicStateStrategy: "strategy", ClosureStrategy: "closure strategy", RuntimeInputs: "manifest", RuntimeDigest: "digest", ResultKind: gofresh.CodeResult,
	}, ModuleBase: "sub", RuntimeUnverifiable: true, RuntimeReason: "why"}
	current, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	flat := legacyRows(t, []byte(`{"findings":[{"targetEvidence":`+string(current)+`}]}`))
	var top struct {
		Findings []struct {
			TargetEvidence json.RawMessage `json:"targetEvidence"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(flat, &top); err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(top.Findings[0].TargetEvidence, &keys); err != nil {
		t.Fatal(err)
	}
	for key := range keys {
		if !legacyEvidenceKeys[key] {
			t.Errorf("the downgrader emits %q, a key the reader does not know", key)
		}
	}
	// singleSubjectDischarges is no legacy key — a pre-13 row could not
	// carry it — so the reader's set never names it and the fixture
	// leaves it unset; every legacy key the downgrader must emit.
	for key := range legacyEvidenceKeys {
		if _, ok := keys[key]; !ok {
			t.Errorf("the reader knows %q, a key the downgrader never emits", key)
		}
	}
	upgraded, err := upgradeLegacyEvidenceRow(top.Findings[0].TargetEvidence)
	if err != nil {
		t.Fatal(err)
	}
	var back SubjectEvidence
	if err := json.Unmarshal(upgraded, &back); err != nil || back != row {
		t.Fatalf("downgrade then upgrade moved the row: %v\n got %+v\nwant %+v", err, back, row)
	}
}

// TestFingerprintRecordKeysRideTheDocumentVersion pins the embedded
// record's key set beside DocumentVersion: a fingerprint field gofresh
// grows lands in every row and an older reader of this version refuses
// the whole document by an unknown key, so the growth rides a version
// bump — this golden moves and the version literal beside it moves with
// it (REQ-result-export, REQ-result-tolerant).
func TestFingerprintRecordKeysRideTheDocumentVersion(t *testing.T) {
	// Every field filled by reflection, so a field gofresh grows is
	// non-zero and renders whatever its omitempty tag says — a
	// hand-written literal would leave it zero and the golden blind.
	var full gofresh.Fingerprint
	next := 0
	var fill func(v reflect.Value)
	fill = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.String:
			next++
			v.SetString(fmt.Sprintf("v%d", next))
		case reflect.Bool:
			v.SetBool(true)
		case reflect.Int:
			v.SetInt(int64(gofresh.Measurement))
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				fill(v.Field(i))
			}
		default:
			t.Fatalf("Fingerprint field of kind %s: extend the filler", v.Kind())
		}
	}
	fill(reflect.ValueOf(&full).Elem())
	raw, err := json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	keysOf := func(raw json.RawMessage) []string {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			t.Fatal(err)
		}
		var got []string
		for key := range object {
			got = append(got, key)
		}
		sort.Strings(got)
		return got
	}
	got := keysOf(raw)
	want := []string{"buildConfig", "closureStrategy", "dynamicStateStrategy", "dynamicStateVouches", "machine", "maximalClosure", "observationAssertion", "observationProof", "packageProcessDischarges", "purityAssertion", "resultKind", "runtimeConfig", "runtimeDigest", "runtimeInputs", "singleSubjectDischarges", "testVariantClosure", "toolchain"}
	if !reflect.DeepEqual(got, want) || DocumentVersion != 14 {
		t.Fatalf("the record's keys = %v (DocumentVersion %d); a moved key set rides a version bump", got, DocumentVersion)
	}
	// The nested proof object has its own strict decoder, so its key
	// set rides the version the same way.
	var outer map[string]json.RawMessage
	if err := json.Unmarshal(raw, &outer); err != nil {
		t.Fatal(err)
	}
	if got := keysOf(outer["observationProof"]); !reflect.DeepEqual(got, []string{"evidence", "observable", "package", "reason", "strategy", "symbol"}) {
		t.Fatalf("the proof's keys = %v; a moved key set rides a version bump", got)
	}
}

// legacyRows rewrites a rendered document's evidence rows — the
// interned table's or the inline findings' — into the flat pre-13
// shape, so a fixture relabeled with an older version carries the rows
// that version wrote; the inverse of the reader's upgrade, kept beside
// the pins that exercise it.
func legacyRows(t *testing.T, data []byte) []byte {
	t.Helper()
	flatten := func(raw json.RawMessage) json.RawMessage {
		var e SubjectEvidence
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Fatal(err)
		}
		flat := map[string]any{
			"symbol": e.Symbol, "maximalClosure": e.MaximalClosure, "testVariantClosure": e.TestVariantClosure,
			"toolchain": e.Guards.Toolchain, "buildConfig": e.Guards.BuildConfig,
			"observationAssertion": e.ObservationAssertion, "observationStrategy": e.ObservationProof.Strategy,
			"observationSubjectPackage": e.ObservationProof.Subject.Package, "observationSubjectSymbol": e.ObservationProof.Subject.Symbol,
			"observationObservable": e.ObservationProof.Observable, "observationEvidence": e.ObservationProof.Evidence,
			"runtimeInputs": e.RuntimeInputs, "runtimeDigest": e.RuntimeDigest,
		}
		for key, value := range map[string]string{
			"observationReason": e.ObservationProof.Reason, "purityAssertion": e.PurityAssertion, "dynamicStateVouches": e.DynamicStateVouches,
			"packageProcessDischarges": e.PackageProcessDischarges, "dynamicStateStrategy": e.DynamicStateStrategy, "closureStrategy": e.ClosureStrategy,
			"moduleBase": e.ModuleBase, "runtimeReason": e.RuntimeReason,
		} {
			if value != "" {
				flat[key] = value
			}
		}
		if e.RuntimeUnverifiable {
			flat["runtimeUnverifiable"] = true
		}
		out, err := json.Marshal(flat)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatal(err)
	}
	if raw, ok := top["evidenceTable"]; ok {
		// A current document references its tables by content key; the
		// positional shape of versions 11 to 13 references by index, so
		// the keyed tables are re-emitted positional first.
		var keyed internedDocument14
		if err := json.Unmarshal(data, &keyed); err != nil {
			t.Fatal(err)
		}
		manifestAt := map[string]int{}
		manifests := []string{}
		for _, m := range keyed.RuntimeInputs {
			manifestAt[m.Key] = len(manifests)
			manifests = append(manifests, m.Manifest)
		}
		evidenceAt := map[string]int{}
		table := []map[string]json.RawMessage{}
		for _, e := range keyed.Evidence {
			evidenceAt[e.Key] = len(table)
			ev, _ := json.Marshal(e.Evidence)
			ri, _ := json.Marshal(manifestAt[e.RuntimeInputs])
			table = append(table, map[string]json.RawMessage{"evidence": flatten(ev), "runtimeInputs": ri})
		}
		ledgerAt := map[string]int{}
		ledgers := []CompartmentLedger{}
		for _, l := range keyed.Ledgers {
			ledgerAt[l.Key] = len(ledgers)
			ledgers = append(ledgers, l.Ledger)
		}
		rows := []findingV11{}
		for _, r := range keyed.Findings {
			row := findingV11{Finding: r.Finding, OracleEvidence: []int{}}
			if r.TargetEvidence != nil {
				i := evidenceAt[*r.TargetEvidence]
				row.TargetEvidence = &i
			}
			for _, k := range r.OracleEvidence {
				row.OracleEvidence = append(row.OracleEvidence, evidenceAt[k])
			}
			if r.CompartmentLedger != nil {
				i := ledgerAt[*r.CompartmentLedger]
				row.CompartmentLedger = &i
			}
			rows = append(rows, row)
		}
		top["runtimeInputsTable"], _ = json.Marshal(manifests)
		top["evidenceTable"], _ = json.Marshal(table)
		top["ledgerTable"], _ = json.Marshal(ledgers)
		top["findings"], _ = json.Marshal(rows)
		_ = raw
	} else {
		var findings []map[string]json.RawMessage
		if err := json.Unmarshal(top["findings"], &findings); err != nil {
			t.Fatal(err)
		}
		for i := range findings {
			if raw, ok := findings[i]["targetEvidence"]; ok {
				findings[i]["targetEvidence"] = flatten(raw)
			}
			if raw, ok := findings[i]["oracleEvidence"]; ok {
				var rows []json.RawMessage
				if err := json.Unmarshal(raw, &rows); err != nil {
					t.Fatal(err)
				}
				for j := range rows {
					rows[j] = flatten(rows[j])
				}
				findings[i]["oracleEvidence"], _ = json.Marshal(rows)
			}
		}
		top["findings"], _ = json.Marshal(findings)
	}
	out, err := json.Marshal(top)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
