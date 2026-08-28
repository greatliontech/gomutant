package gomutant

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// The exported document scales with UNIQUE evidence: identical
// subject-evidence rows and runtime-inputs manifests collapse to one
// table entry each, so a manifest shared by every record appears in
// the bytes exactly once and a divergent second copy of one fact is
// unrepresentable (REQ-result-export).
func TestExportInternsDuplicatedEvidence(t *testing.T) {
	const manifest = "interned-manifest-marker"
	shared := cleanEvidence("p.SharedTest")
	shared.RuntimeInputs = manifest
	led := CompartmentLedger{Declarations: []CompartmentDeclaration{{File: "a.go", Kind: "func", Name: "A", Hash: "h"}}}
	a := storeFinding("p.A", func(f *Finding) {
		f.TargetEvidence.RuntimeInputs = manifest
		f.OracleEvidence = []SubjectEvidence{shared}
		l := led
		f.CompartmentLedger = &l
	})
	b := storeFinding("p.B", func(f *Finding) {
		f.TargetEvidence.RuntimeInputs = manifest
		f.OracleEvidence = []SubjectEvidence{shared}
		l := led
		f.CompartmentLedger = &l
	})
	c := storeFinding("structural:pin", func(f *Finding) {
		f.Shape = &TargetShape{Structural: &StructuralSpec{Class: "import-boundary", Packages: []string{"p"}, Forbidden: "q"}}
		f.TargetEvidence = SubjectEvidence{}
		f.OracleEvidence = []SubjectEvidence{shared}
	})
	data, err := Export([]Finding{a, b, c})
	if err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(data, []byte(manifest)); got != 1 {
		t.Fatalf("shared manifest appears %d times in the document, want 1", got)
	}
	var doc documentV11
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.RuntimeInputs) != 1 {
		t.Fatalf("runtime-inputs table = %d entries, want 1", len(doc.RuntimeInputs))
	}
	// The oracle evidence both records share is one table entry, not
	// two equal copies.
	if doc.Findings[0].OracleEvidence[0] != doc.Findings[1].OracleEvidence[0] {
		t.Fatalf("shared oracle evidence interned at distinct indexes %d and %d",
			doc.Findings[0].OracleEvidence[0], doc.Findings[1].OracleEvidence[0])
	}
	// Identical ledgers collapse to one table entry; a shaped record
	// interns no target-evidence index at all.
	if len(doc.Ledgers) != 1 {
		t.Fatalf("ledger table = %d entries, want 1", len(doc.Ledgers))
	}
	if doc.Findings[0].CompartmentLedger == nil || doc.Findings[1].CompartmentLedger == nil ||
		*doc.Findings[0].CompartmentLedger != *doc.Findings[1].CompartmentLedger {
		t.Fatalf("shared ledger not interned: %+v %+v", doc.Findings[0].CompartmentLedger, doc.Findings[1].CompartmentLedger)
	}
	if doc.Findings[2].TargetEvidence != nil {
		t.Fatalf("shaped finding interned a target-evidence index %d", *doc.Findings[2].TargetEvidence)
	}
	// Round-trip: expansion restores every record exactly.
	parsed, err := ParseFindings(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(parsed, []Finding{a, b, c}) {
		t.Fatalf("round-trip diverged:\n got %+v\nwant %+v", parsed, []Finding{a, b, c})
	}
}

// An interned document with references outside its tables, an inline
// manifest beside a table reference, or inline heavy fields on a
// record is malformed — the tables are the one home
// (REQ-result-export).
func TestParseFindingsRefusesMalformedInternedDocuments(t *testing.T) {
	shaped := storeFinding("structural:pin", func(f *Finding) {
		f.Shape = &TargetShape{Structural: &StructuralSpec{Class: "import-boundary", Packages: []string{"p"}, Forbidden: "q"}}
		f.TargetEvidence = SubjectEvidence{}
	})
	valid, err := Export([]Finding{survivorFinding("p.A"), shaped})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		mutate func(*documentV11)
		want   string
	}{
		{"dangling oracle-evidence index", func(d *documentV11) { d.Findings[0].OracleEvidence[0] = 99 }, "outside the table"},
		{"dangling runtime-inputs index", func(d *documentV11) { d.Evidence[0].RuntimeInputs = 99 }, "outside the table"},
		{"dangling ledger index", func(d *documentV11) { i := 42; d.Findings[0].CompartmentLedger = &i }, "outside the table"},
		{"inline manifest beside its reference", func(d *documentV11) { d.Evidence[0].Evidence.RuntimeInputs = "sneak" }, "inline runtime-inputs manifest"},
		{"inline oracle evidence on a record", func(d *documentV11) {
			d.Findings[0].Finding.OracleEvidence = []SubjectEvidence{cleanEvidence("p.X")}
		}, "inline heavy fields"},
		{"inline target evidence on a record", func(d *documentV11) {
			d.Findings[0].Finding.TargetEvidence = SubjectEvidence{Symbol: "p.X"}
		}, "inline heavy fields"},
		{"stray target-evidence index on a shaped record", func(d *documentV11) {
			i := 0
			d.Findings[1].TargetEvidence = &i
		}, "missing or has invalid required evidence"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var doc documentV11
			if err := json.Unmarshal(valid, &doc); err != nil {
				t.Fatal(err)
			}
			tc.mutate(&doc)
			data, err := json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ParseFindings(data); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("malformed document accepted or misdiagnosed: %v", err)
			}
		})
	}
	// A missing table is refused, never defaulted to empty: an empty
	// table would turn every reference dangling with a misleading
	// diagnosis.
	for _, table := range []string{"runtimeInputsTable", "evidenceTable", "ledgerTable", "findings"} {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(valid, &raw); err != nil {
			t.Fatal(err)
		}
		delete(raw, table)
		data, err := json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ParseFindings(data); err == nil || !strings.Contains(err.Error(), "missing or null") {
			t.Fatalf("missing %s accepted: %v", table, err)
		}
	}
}

// Two table entries carrying EQUAL manifests are semantically one
// fact: rows citing either entry still satisfy every check the
// inline form they stand for would - cross-row manifest equality is
// content equality, never table-index equality (REQ-result-export).
func TestParseFindingsAcceptsDuplicateManifestEntries(t *testing.T) {
	valid, err := Export([]Finding{survivorFinding("p.A")})
	if err != nil {
		t.Fatal(err)
	}
	var doc documentV11
	if err := json.Unmarshal(valid, &doc); err != nil {
		t.Fatal(err)
	}
	oracle := doc.Findings[0].OracleEvidence[0]
	doc.RuntimeInputs = append(doc.RuntimeInputs, doc.RuntimeInputs[doc.Evidence[oracle].RuntimeInputs])
	entry := doc.Evidence[oracle]
	entry.RuntimeInputs = len(doc.RuntimeInputs) - 1
	doc.Evidence = append(doc.Evidence, entry)
	doc.Findings[0].OracleEvidence[0] = len(doc.Evidence) - 1
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseFindings(data); err != nil {
		t.Fatalf("duplicate manifest entries refused: %v", err)
	}
}

// The inverse pin: an oracle row citing a manifest with DIFFERENT
// content than the finding's anchor refuses exactly as the inline
// form would - the placeholder substitution must discriminate unequal
// content, not merely accept equal content
// (REQ-result-export, REQ-result-record).
func TestParseFindingsRefusesMixedManifestsAcrossAFinding(t *testing.T) {
	valid, err := Export([]Finding{survivorFinding("p.A")})
	if err != nil {
		t.Fatal(err)
	}
	var doc documentV11
	if err := json.Unmarshal(valid, &doc); err != nil {
		t.Fatal(err)
	}
	oracle := doc.Findings[0].OracleEvidence[0]
	doc.RuntimeInputs = append(doc.RuntimeInputs, "a different manifest")
	entry := doc.Evidence[oracle]
	entry.RuntimeInputs = len(doc.RuntimeInputs) - 1
	doc.Evidence = append(doc.Evidence, entry)
	doc.Findings[0].OracleEvidence[0] = len(doc.Evidence) - 1
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseFindings(data); err == nil || !strings.Contains(err.Error(), "not finding-wide") {
		t.Fatalf("mixed manifests accepted or misdiagnosed: %v", err)
	}
}

// A shaped finding carrying a stray target-evidence row refuses at
// export: interning would otherwise silently drop the row, losing
// caller data the shaped contract says must not exist
// (REQ-target-structural).
func TestExportRefusesShapedFindingWithTargetEvidence(t *testing.T) {
	shaped := storeFinding("structural:pin", func(f *Finding) {
		f.Shape = &TargetShape{Structural: &StructuralSpec{Class: "import-boundary", Packages: []string{"p"}, Forbidden: "q"}}
	})
	if _, err := Export([]Finding{shaped}); err == nil || !strings.Contains(err.Error(), "shaped but carries target evidence") {
		t.Fatalf("stray target evidence accepted: %v", err)
	}
}
