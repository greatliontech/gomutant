package gomutant

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"slices"
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
	data, err := Export([]Finding{a, b, c}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(data, []byte(manifest)); got != 1 {
		t.Fatalf("shared manifest appears %d times in the document, want 1", got)
	}
	var doc internedDocument14
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.RuntimeInputs) != 1 {
		t.Fatalf("runtime-inputs table = %d entries, want 1", len(doc.RuntimeInputs))
	}
	// The oracle evidence both records share is one table entry, not
	// two equal copies, and the entry's key is its content's.
	if doc.Findings[0].OracleEvidence[0] != doc.Findings[1].OracleEvidence[0] {
		t.Fatalf("shared oracle evidence interned under distinct keys %s and %s",
			doc.Findings[0].OracleEvidence[0], doc.Findings[1].OracleEvidence[0])
	}
	for _, e := range doc.Evidence {
		if b, _, err := evidenceBytes(e.Evidence, e.RuntimeInputs); err != nil || contentKey(b) != e.Key {
			t.Fatalf("evidence entry %s is not keyed by its content", e.Key)
		}
	}
	// Identical ledgers collapse to one table entry; a shaped record
	// interns no target-evidence key at all.
	if len(doc.Ledgers) != 1 {
		t.Fatalf("ledger table = %d entries, want 1", len(doc.Ledgers))
	}
	if doc.Findings[0].CompartmentLedger == nil || doc.Findings[1].CompartmentLedger == nil ||
		*doc.Findings[0].CompartmentLedger != *doc.Findings[1].CompartmentLedger {
		t.Fatalf("shared ledger not interned: %+v %+v", doc.Findings[0].CompartmentLedger, doc.Findings[1].CompartmentLedger)
	}
	if doc.Findings[2].TargetEvidence != nil {
		t.Fatalf("shaped finding interned a target-evidence key %s", *doc.Findings[2].TargetEvidence)
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

// An interned document with a reference outside its tables, an entry
// whose key is not its content's, two entries under one key, an
// inline manifest beside a table reference, or inline heavy fields on
// a record is malformed — the tables are the one home and a key names
// one content (REQ-result-export).
func TestParseFindingsRefusesMalformedInternedDocuments(t *testing.T) {
	shaped := storeFinding("structural:pin", func(f *Finding) {
		f.Shape = &TargetShape{Structural: &StructuralSpec{Class: "import-boundary", Packages: []string{"p"}, Forbidden: "q"}}
		f.TargetEvidence = SubjectEvidence{}
	})
	valid, err := Export([]Finding{survivorFinding("p.A"), shaped}, nil)
	if err != nil {
		t.Fatal(err)
	}
	dangling := contentKey([]byte("nowhere"))
	cases := []struct {
		name   string
		mutate func(*internedDocument14)
		want   string
	}{
		{"dangling oracle-evidence key", func(d *internedDocument14) { d.Findings[0].OracleEvidence[0] = dangling }, "outside the table"},
		{"dangling runtime-inputs key", func(d *internedDocument14) { d.Evidence[0].RuntimeInputs = dangling }, "outside the table"},
		{"dangling ledger key", func(d *internedDocument14) { k := dangling; d.Findings[0].CompartmentLedger = &k }, "outside the table"},
		{"a manifest entry whose key is another content's", func(d *internedDocument14) { d.RuntimeInputs[0].Manifest += "x" }, "is not its manifest's"},
		{"an evidence entry whose key is another content's", func(d *internedDocument14) { d.Evidence[0].Evidence.Symbol += "x" }, "is not its content's"},
		{"a manifest key held twice", func(d *internedDocument14) { d.RuntimeInputs = append(d.RuntimeInputs, d.RuntimeInputs[0]) }, "held twice"},
		{"an evidence key held twice", func(d *internedDocument14) { d.Evidence = append(d.Evidence, d.Evidence[0]) }, "held twice"},
		{"inline manifest beside its reference", func(d *internedDocument14) { d.Evidence[0].Evidence.RuntimeInputs = "sneak" }, "inline runtime-inputs manifest"},
		{"inline oracle evidence on a record", func(d *internedDocument14) {
			d.Findings[0].Finding.OracleEvidence = []SubjectEvidence{cleanEvidence("p.X")}
		}, "inline heavy fields"},
		{"inline target evidence on a record", func(d *internedDocument14) {
			d.Findings[0].Finding.TargetEvidence = SubjectEvidence{Symbol: "p.X"}
		}, "inline heavy fields"},
		{"stray target-evidence key on a shaped record", func(d *internedDocument14) {
			k := d.Evidence[0].Key
			d.Findings[1].TargetEvidence = &k
		}, "missing or has invalid required evidence"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var doc internedDocument14
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
	for _, table := range []string{"runtimeInputsTable", "evidenceTable", "ledgerTable", "findings", "coverageBounds"} {
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

// Equal content is one entry by construction: a second entry under a
// manifest's key is refused, so two rows citing equal manifests cite
// one key and the finding-wide runtime anchor is judged over content
// (REQ-result-export). The positional documents of versions 11 to 13
// admitted equal entries under distinct indexes; the reader of those
// still does.
func TestParseFindingsKeysEqualManifestsOnce(t *testing.T) {
	valid, err := Export([]Finding{survivorFinding("p.A")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var doc internedDocument14
	if err := json.Unmarshal(valid, &doc); err != nil {
		t.Fatal(err)
	}
	doc.RuntimeInputs = append(doc.RuntimeInputs, doc.RuntimeInputs[0])
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseFindings(data); err == nil || !strings.Contains(err.Error(), "held twice") {
		t.Fatalf("a manifest entry held twice accepted or misdiagnosed: %v", err)
	}
	// The positional reader: a document of version 12 whose oracle row
	// cites a second entry carrying the manifest's equal content still
	// satisfies the finding-wide runtime anchor — cross-row manifest
	// equality is content equality, never table-index equality.
	var top map[string]json.RawMessage
	if err := json.Unmarshal(legacyRows(t, valid), &top); err != nil {
		t.Fatal(err)
	}
	var manifests []string
	var table []map[string]json.RawMessage
	var rows []map[string]json.RawMessage
	for _, pair := range []struct {
		key string
		dst any
	}{{"runtimeInputsTable", &manifests}, {"evidenceTable", &table}, {"findings", &rows}} {
		if err := json.Unmarshal(top[pair.key], pair.dst); err != nil {
			t.Fatal(err)
		}
	}
	var oracleIdx []int
	if err := json.Unmarshal(rows[0]["oracleEvidence"], &oracleIdx); err != nil {
		t.Fatal(err)
	}
	var cited int
	if err := json.Unmarshal(table[oracleIdx[0]]["runtimeInputs"], &cited); err != nil {
		t.Fatal(err)
	}
	manifests = append(manifests, manifests[cited])
	duplicate := map[string]json.RawMessage{"evidence": table[oracleIdx[0]]["evidence"]}
	duplicate["runtimeInputs"], _ = json.Marshal(len(manifests) - 1)
	table = append(table, duplicate)
	oracleIdx[0] = len(table) - 1
	rows[0]["oracleEvidence"], _ = json.Marshal(oracleIdx)
	top["runtimeInputsTable"], _ = json.Marshal(manifests)
	top["evidenceTable"], _ = json.Marshal(table)
	top["findings"], _ = json.Marshal(rows)
	top["version"] = json.RawMessage("12")
	positional, err := json.Marshal(top)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseFindings(positional); err != nil {
		t.Fatalf("a version-12 document whose row cites an equal manifest entry refused: %v", err)
	}
}

// An oracle row citing a manifest with DIFFERENT content than the
// finding's anchor refuses exactly as the inline form would — the
// placeholder substitution must discriminate unequal content, not
// merely accept equal content (REQ-result-export, REQ-result-record).
func TestParseFindingsRefusesMixedManifestsAcrossAFinding(t *testing.T) {
	valid, err := Export([]Finding{survivorFinding("p.A")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var doc internedDocument14
	if err := json.Unmarshal(valid, &doc); err != nil {
		t.Fatal(err)
	}
	other := manifestEntry{Key: contentKey([]byte("a different manifest")), Manifest: "a different manifest"}
	doc.RuntimeInputs = append(doc.RuntimeInputs, other)
	var oracle evidenceEntry
	for _, e := range doc.Evidence {
		if e.Key == doc.Findings[0].OracleEvidence[0] {
			oracle = e
		}
	}
	oracle.RuntimeInputs = other.Key
	b, _, err := evidenceBytes(oracle.Evidence, other.Key)
	if err != nil {
		t.Fatal(err)
	}
	oracle.Key = contentKey(b)
	doc.Evidence = append(doc.Evidence, oracle)
	doc.Findings[0].OracleEvidence[0] = oracle.Key
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
	if _, err := Export([]Finding{shaped}, nil); err == nil || !strings.Contains(err.Error(), "shaped but carries target evidence") {
		t.Fatalf("stray target evidence accepted: %v", err)
	}
}

// The interned document's tables are keyed by content and emitted in
// key order, so the document is one function of its record set and a
// change is local: shuffling the records leaves the document
// byte-identical (records sort by symbol before interning, so this
// pins the export whole), and renaming one record so the records sort
// differently leaves the manifest and ledger tables unchanged, changes
// exactly one evidence entry — the renamed record's own target row —
// and rewrites no other record's row (REQ-result-export). A seeded
// exploration over record sets drawn from a small alphabet of
// manifests, oracles and ledgers.
func TestExportEmitsTablesInCanonicalOrder(t *testing.T) {
	manifests := []string{"manifest-a", "manifest-b", "manifest-c"}
	oracles := []string{"p.TestA", "p.TestB", "p.TestC"}
	ledgers := []CompartmentLedger{
		{Declarations: []CompartmentDeclaration{{File: "a.go", Kind: "func", Name: "A", Hash: "h1"}}},
		{Declarations: []CompartmentDeclaration{{File: "b.go", Kind: "func", Name: "B", Hash: "h2"}}},
	}
	tables := func(findings []Finding) (internedDocument14, []byte) {
		data, err := Export(findings, nil)
		if err != nil {
			t.Fatal(err)
		}
		var doc internedDocument14
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		return doc, data
	}
	rowBytes := func(r findingRow) string { b, _ := json.Marshal(r); return string(b) }
	for seed := int64(1); seed <= 300; seed++ {
		r := rand.New(rand.NewSource(seed))
		var records []Finding
		seen := map[string]bool{}
		for i, n := 0, 1+r.Intn(6); i < n; i++ {
			symbol := fmt.Sprintf("p.F%d", r.Intn(100))
			if seen[symbol] {
				continue // one record per symbol
			}
			seen[symbol] = true
			records = append(records, storeFinding(symbol, func(f *Finding) {
				// One manifest per record: a record's runtime evidence is
				// finding-wide (REQ-result-export).
				manifest := manifests[r.Intn(len(manifests))]
				f.TargetEvidence.RuntimeInputs = manifest
				f.OracleEvidence = nil
				for _, j := range r.Perm(len(oracles))[:1+r.Intn(len(oracles))] {
					e := cleanEvidence(oracles[j])
					e.RuntimeInputs = manifest
					f.OracleEvidence = append(f.OracleEvidence, e)
				}
				if r.Intn(2) == 1 {
					l := ledgers[r.Intn(len(ledgers))]
					f.CompartmentLedger = &l
				}
			}))
		}
		base, baseBytes := tables(records)
		// The tables are in key order.
		if !slices.IsSortedFunc(base.RuntimeInputs, func(a, b manifestEntry) int { return strings.Compare(a.Key, b.Key) }) ||
			!slices.IsSortedFunc(base.Evidence, func(a, b evidenceEntry) int { return strings.Compare(a.Key, b.Key) }) ||
			!slices.IsSortedFunc(base.Ledgers, func(a, b ledgerEntry) int { return strings.Compare(a.Key, b.Key) }) {
			t.Fatalf("seed %d: a table is not in key order", seed)
		}
		// Shuffled input: byte-identical document.
		shuffled := slices.Clone(records)
		r.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		if _, shuffledBytes := tables(shuffled); !bytes.Equal(shuffledBytes, baseBytes) {
			t.Fatalf("seed %d: the document depends on the records' input order", seed)
		}
		// A rename that moves a record's sort position.
		i := r.Intn(len(records))
		renamed := slices.Clone(records)
		renamed[i].Symbol = "a.Z" + records[i].Symbol
		renamed[i].TargetEvidence.Symbol = renamed[i].Symbol
		after, _ := tables(renamed)
		if !reflect.DeepEqual(after.RuntimeInputs, base.RuntimeInputs) || !reflect.DeepEqual(after.Ledgers, base.Ledgers) {
			t.Fatalf("seed %d: a rename re-emitted the manifest or ledger tables", seed)
		}
		baseKeys := map[string]bool{}
		for _, e := range base.Evidence {
			baseKeys[e.Key] = true
		}
		only := 0
		for _, e := range after.Evidence {
			if !baseKeys[e.Key] {
				only++
			}
		}
		if only != 1 {
			t.Fatalf("seed %d: a rename changed %d evidence entries, want the renamed record's target row alone", seed, only)
		}
		// Every other record's row is byte-identical: no reference moved.
		baseRows := map[string]string{}
		for _, row := range base.Findings {
			baseRows[row.Finding.Symbol] = rowBytes(row)
		}
		for _, row := range after.Findings {
			if row.Finding.Symbol == renamed[i].Symbol {
				continue
			}
			if baseRows[row.Finding.Symbol] != rowBytes(row) {
				t.Fatalf("seed %d: a rename of %s rewrote %s's row", seed, records[i].Symbol, row.Finding.Symbol)
			}
		}
	}
}
