package gomutant

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/greatliontech/gofresh"
)

// The interned document, version 14 (REQ-result-export): subject
// evidence, runtime-input manifests and compartment ledgers live once
// each in document-level tables, and a record references its entries
// by content key — the digest of the entry's own bytes — never by
// position. A key is a function of the entry alone, so a record set is
// one byte sequence, and a record renamed, added or removed diffs as
// its own row and the entries only it referenced: a positional index
// renumbers every reference past a table's moved entry, which is how
// a rename of twenty-one records once rewrote a document of three
// hundred whole. The tables are emitted in key order.
type internedDocument14 struct {
	Version        int             `json:"version"`
	RuntimeInputs  []manifestEntry `json:"runtimeInputsTable"`
	Evidence       []evidenceEntry `json:"evidenceTable"`
	Ledgers        []ledgerEntry   `json:"ledgerTable"`
	Findings       []findingRow    `json:"findings"`
	CoverageBounds []CoverageBound `json:"coverageBounds"`
}

// manifestEntry is one runtime-inputs manifest under its content key.
type manifestEntry struct {
	Key      string `json:"key"`
	Manifest string `json:"manifest"`
}

// evidenceEntry is one unique SubjectEvidence, its manifest replaced by
// the manifest entry's key, under the key of its own bytes and that
// reference; the evidence embeds the ordinary struct with RuntimeInputs
// empty, so a field added to SubjectEvidence rides the table
// automatically.
type evidenceEntry struct {
	Key           string          `json:"key"`
	Evidence      SubjectEvidence `json:"evidence"`
	RuntimeInputs string          `json:"runtimeInputs"`
}

// ledgerEntry is one compartment ledger under its content key.
type ledgerEntry struct {
	Key    string            `json:"key"`
	Ledger CompartmentLedger `json:"ledger"`
}

// findingRow is one Finding with its heavy components cleared and
// referenced by key instead: targetEvidence is absent for a shaped
// finding, which carries none.
type findingRow struct {
	Finding           Finding  `json:"finding"`
	TargetEvidence    *string  `json:"targetEvidence,omitempty"`
	OracleEvidence    []string `json:"oracleEvidence"`
	CompartmentLedger *string  `json:"compartmentLedger,omitempty"`
}

// contentKey is a table entry's key: the first twenty-four hex digits
// of the SHA-256 of its bytes — ninety-six bits, far beyond a
// document's entry count, and short enough to read in a diff.
func contentKey(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:12])
}

// evidenceBytes is the bytes an evidence entry is keyed over: its
// manifest's key, a newline, and the evidence with its manifest
// cleared; the evidence's own marshalling is deterministic (a fixed
// wire struct, no maps).
func evidenceBytes(e SubjectEvidence, manifestKey string) ([]byte, SubjectEvidence, error) {
	e.RuntimeInputs = ""
	body, err := json.Marshal(e)
	if err != nil {
		return nil, e, err
	}
	return append(append([]byte(manifestKey), '\n'), body...), e, nil
}

// internDocument builds the version-14 interned form: identical
// evidence rows, manifests and ledgers collapse to one entry each
// under their content keys, and every table is emitted in key order.
func internDocument(kept []Finding) (internedDocument14, error) {
	doc := internedDocument14{Version: DocumentVersion, RuntimeInputs: []manifestEntry{}, Evidence: []evidenceEntry{}, Ledgers: []ledgerEntry{}}
	manifests := map[string]string{}          // key -> manifest
	evidences := map[string]evidenceEntry{}   // key -> entry
	ledgers := map[string]CompartmentLedger{} // key -> ledger
	// A key names one content: two contents under one key — a
	// collision beyond any document's reach — refuse rather than merge
	// one record's evidence into another's silently.
	internManifest := func(m string) (string, error) {
		k := contentKey([]byte(m))
		if held, ok := manifests[k]; ok && held != m {
			return "", fmt.Errorf("gomutant: content key %s names two manifests", k)
		}
		manifests[k] = m
		return k, nil
	}
	internEvidence := func(e SubjectEvidence) (string, error) {
		mk, err := internManifest(e.RuntimeInputs)
		if err != nil {
			return "", err
		}
		b, cleared, err := evidenceBytes(e, mk)
		if err != nil {
			return "", err
		}
		k := contentKey(b)
		entry := evidenceEntry{Key: k, Evidence: cleared, RuntimeInputs: mk}
		if held, ok := evidences[k]; ok {
			hb, _, err := evidenceBytes(held.Evidence, held.RuntimeInputs)
			if err != nil {
				return "", err
			}
			if string(hb) != string(b) {
				return "", fmt.Errorf("gomutant: content key %s names two evidence entries", k)
			}
		}
		evidences[k] = entry
		return k, nil
	}
	internLedger := func(l CompartmentLedger) (string, error) {
		b, err := json.Marshal(l)
		if err != nil {
			return "", err
		}
		k := contentKey(b)
		if held, ok := ledgers[k]; ok {
			hb, err := json.Marshal(held)
			if err != nil {
				return "", err
			}
			if string(hb) != string(b) {
				return "", fmt.Errorf("gomutant: content key %s names two ledgers", k)
			}
		}
		ledgers[k] = l
		return k, nil
	}
	doc.Findings = make([]findingRow, len(kept))
	for i, f := range kept {
		row := findingRow{}
		// A shaped finding's target row is the zero value by contract
		// (REQ-target-structural); interning would silently drop a
		// stray one, so refuse it here where the caller still sees it.
		if f.Shape != nil && f.TargetEvidence != (SubjectEvidence{}) {
			return doc, fmt.Errorf("gomutant: finding %d (%s) is shaped but carries target evidence", i, f.Symbol)
		}
		if f.Shape == nil {
			k, err := internEvidence(f.TargetEvidence)
			if err != nil {
				return doc, err
			}
			row.TargetEvidence = &k
		}
		row.OracleEvidence = make([]string, len(f.OracleEvidence))
		for j, e := range f.OracleEvidence {
			k, err := internEvidence(e)
			if err != nil {
				return doc, err
			}
			row.OracleEvidence[j] = k
		}
		if f.CompartmentLedger != nil {
			k, err := internLedger(*f.CompartmentLedger)
			if err != nil {
				return doc, err
			}
			row.CompartmentLedger = &k
		}
		f.TargetEvidence = SubjectEvidence{}
		f.OracleEvidence = nil
		f.CompartmentLedger = nil
		row.Finding = f
		doc.Findings[i] = row
	}
	for _, k := range slices.Sorted(maps.Keys(manifests)) {
		doc.RuntimeInputs = append(doc.RuntimeInputs, manifestEntry{Key: k, Manifest: manifests[k]})
	}
	for _, k := range slices.Sorted(maps.Keys(evidences)) {
		doc.Evidence = append(doc.Evidence, evidences[k])
	}
	for _, k := range slices.Sorted(maps.Keys(ledgers)) {
		doc.Ledgers = append(doc.Ledgers, ledgerEntry{Key: k, Ledger: ledgers[k]})
	}
	return doc, nil
}

// expandDocument14 rebuilds the inline finding set from a version-14
// document, validating every entry and reference: an entry whose key is
// not its content's, two entries under one key, a dangling reference,
// an inline manifest beside an entry's reference, or inline heavy
// fields on a record are malformed — the tables are the one home and a
// key names one content (REQ-result-export). With placeholder set, each
// manifest is replaced by an equality-preserving placeholder of its key
// (validateInternedRecords' bounded re-validation).
func expandDocument14(doc internedDocument14, placeholder bool) ([]Finding, error) {
	manifests := map[string]string{}
	for i, m := range doc.RuntimeInputs {
		if m.Key != contentKey([]byte(m.Manifest)) {
			return nil, fmt.Errorf("gomutant: runtime-inputs entry %d: key %s is not its manifest's", i, m.Key)
		}
		if _, dup := manifests[m.Key]; dup {
			return nil, fmt.Errorf("gomutant: runtime-inputs entry %d: key %s held twice", i, m.Key)
		}
		manifests[m.Key] = m.Manifest
	}
	evidences := map[string]SubjectEvidence{}
	for i, e := range doc.Evidence {
		if e.Evidence.RuntimeInputs != "" {
			return nil, fmt.Errorf("gomutant: evidence entry %d carries an inline runtime-inputs manifest beside its table reference", i)
		}
		manifest, ok := manifests[e.RuntimeInputs]
		if !ok {
			return nil, fmt.Errorf("gomutant: evidence entry %d references runtime-inputs %s outside the table", i, e.RuntimeInputs)
		}
		b, _, err := evidenceBytes(e.Evidence, e.RuntimeInputs)
		if err != nil {
			return nil, err
		}
		if e.Key != contentKey(b) {
			return nil, fmt.Errorf("gomutant: evidence entry %d: key %s is not its content's", i, e.Key)
		}
		if _, dup := evidences[e.Key]; dup {
			return nil, fmt.Errorf("gomutant: evidence entry %d: key %s held twice", i, e.Key)
		}
		row := e.Evidence
		// A row whose fingerprint is the zero value (a flat legacy row
		// dropped to nothing) stays zero: a manifest re-inlined onto it
		// would make a fingerprint the record form cannot encode, and
		// the row is incomplete either way.
		if row.Fingerprint != (gofresh.Fingerprint{}) {
			if placeholder && manifest != "" {
				row.RuntimeInputs = "\x00gomutant-runtime-inputs:" + e.RuntimeInputs
			} else {
				row.RuntimeInputs = manifest
			}
		}
		evidences[e.Key] = row
	}
	ledgers := map[string]CompartmentLedger{}
	for i, l := range doc.Ledgers {
		b, err := json.Marshal(l.Ledger)
		if err != nil {
			return nil, err
		}
		if l.Key != contentKey(b) {
			return nil, fmt.Errorf("gomutant: ledger entry %d: key %s is not its content's", i, l.Key)
		}
		if _, dup := ledgers[l.Key]; dup {
			return nil, fmt.Errorf("gomutant: ledger entry %d: key %s held twice", i, l.Key)
		}
		ledgers[l.Key] = l.Ledger
	}
	findings := make([]Finding, len(doc.Findings))
	for i, row := range doc.Findings {
		f := row.Finding
		if len(f.OracleEvidence) != 0 || f.CompartmentLedger != nil || f.TargetEvidence != (SubjectEvidence{}) {
			return nil, fmt.Errorf("gomutant: finding %d carries inline heavy fields beside its table references", i)
		}
		if row.TargetEvidence != nil {
			te, ok := evidences[*row.TargetEvidence]
			if !ok {
				return nil, fmt.Errorf("gomutant: finding %d target evidence: key %s outside the table", i, *row.TargetEvidence)
			}
			f.TargetEvidence = te
		}
		f.OracleEvidence = make([]SubjectEvidence, len(row.OracleEvidence))
		for j, k := range row.OracleEvidence {
			oe, ok := evidences[k]
			if !ok {
				return nil, fmt.Errorf("gomutant: finding %d oracle evidence %d: key %s outside the table", i, j, k)
			}
			f.OracleEvidence[j] = oe
		}
		if row.CompartmentLedger != nil {
			l, ok := ledgers[*row.CompartmentLedger]
			if !ok {
				return nil, fmt.Errorf("gomutant: finding %d ledger key %s outside the table", i, *row.CompartmentLedger)
			}
			f.CompartmentLedger = &l
		}
		findings[i] = f
	}
	return findings, nil
}

// parseInternedDocument14 reads a version-14 document: its tables
// expanded by key and the expanded set re-validated through the inline
// path, one record at a time over manifest placeholders — every
// inline-era semantic check applies verbatim, bounded by the document
// on disk rather than the inline form it stands for.
func parseInternedDocument14(data []byte) (Document, error) {
	top, err := decodeKnownObject(data, map[string]bool{
		"version": true, "runtimeInputsTable": true, "evidenceTable": true, "ledgerTable": true, "findings": true, "coverageBounds": true,
	})
	if err != nil {
		return Document{}, fmt.Errorf("gomutant: parse findings document: %w", err)
	}
	for _, name := range []string{"runtimeInputsTable", "evidenceTable", "ledgerTable", "findings", "coverageBounds"} {
		value, ok := top[name]
		if !ok || isJSONNull(value) {
			return Document{}, fmt.Errorf("gomutant: findings document field %s is missing or null", name)
		}
	}
	var doc internedDocument14
	if err := json.Unmarshal(data, &doc); err != nil {
		return Document{}, fmt.Errorf("gomutant: parse interned findings: %w", err)
	}
	for _, b := range doc.CoverageBounds {
		if b.Selection == "" || len(b.Unreached) == 0 {
			return Document{}, fmt.Errorf("gomutant: findings document coverage bound needs a selection and its unreached symbols")
		}
	}
	expanded, err := expandDocument14(doc, false)
	if err != nil {
		return Document{}, err
	}
	small, err := expandDocument14(doc, true)
	if err != nil {
		return Document{}, err
	}
	symbols := map[string]bool{}
	for i, finding := range small {
		inline, err := json.Marshal(finding)
		if err != nil {
			return Document{}, fmt.Errorf("gomutant: re-validate interned findings: %w", err)
		}
		if _, err := decodeInlineFinding(inline, i); err != nil {
			return Document{}, err
		}
		if symbols[finding.Symbol] {
			return Document{}, fmt.Errorf("gomutant: duplicate finding symbol %s", finding.Symbol)
		}
		symbols[finding.Symbol] = true
	}
	bounds := slices.Clone(doc.CoverageBounds)
	if bounds == nil {
		bounds = []CoverageBound{}
	}
	return Document{Findings: expanded, CoverageBounds: bounds}, nil
}
