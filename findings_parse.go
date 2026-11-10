package gomutant

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"

	gofresh "github.com/greatliontech/gofresh"
)

// OldestReadableDocumentVersion bounds the known older document versions the
// parser upgrades on read (REQ-result-tolerant); the range is ParseDocument's.
const OldestReadableDocumentVersion = 4

// document is the inline finding set shape of versions 4-10; versions
// 11 and 12 write internedDocument and the parser expands it back
// through this path so every inline-era semantic check applies verbatim
// (REQ-result-export).
type document struct {
	Version  int       `json:"version"`
	Findings []Finding `json:"findings"`
}

// internedDocument is the positional interned document (REQ-result-export,
// versions 11 to 13, read and never written): subject evidence,
// runtime-inputs manifests, and compartment ledgers live once each in
// document-level tables; records reference them by index; version 12
// adds the coverage-bounds table. Version 14 references by content key
// (interned.go).
// The tables exist because those three components dominated the
// inline shape — per-oracle subject evidence was 93% of a 66 MB field
// store at 8.8× duplication, and eight unique runtime-inputs manifests
// stood behind 53 MB of it — so the document scales with unique
// evidence and a divergent second copy of one fact is unrepresentable.
type internedDocument struct {
	Version       int                 `json:"version"`
	RuntimeInputs []string            `json:"runtimeInputsTable"`
	Evidence      []evidenceEntryV11  `json:"evidenceTable"`
	Ledgers       []CompartmentLedger `json:"ledgerTable"`
	Findings      []findingV11        `json:"findings"`
	// CoverageBounds is version 12's one addition: the stated coverage
	// bound per declared selection (REQ-result-unreached-bound). A
	// version-11 document carries none; a reader older than 12 refuses
	// ahead rather than dropping the bound — a dropped bound reads as
	// full coverage, the flattering direction REQ-result-tolerant's
	// argument never admits.
	CoverageBounds []CoverageBound `json:"coverageBounds"`
}

// evidenceEntryV11 carries one unique SubjectEvidence with its
// runtime-inputs manifest replaced by a table index. The evidence
// embeds the ordinary struct with RuntimeInputs empty — no mirrored
// field list to drift: a field added to SubjectEvidence rides v11
// automatically.
type evidenceEntryV11 struct {
	Evidence      SubjectEvidence `json:"evidence"`
	RuntimeInputs int             `json:"runtimeInputs"`
}

// findingV11 carries one Finding with its heavy components cleared
// and referenced by index instead: targetEvidence is a pointer
// because a shaped finding carries none. The embedded Finding keeps
// its own encoding — the vestigial zeroed inline fields cost bytes,
// never drift.
type findingV11 struct {
	Finding           Finding `json:"finding"`
	TargetEvidence    *int    `json:"targetEvidence,omitempty"`
	OracleEvidence    []int   `json:"oracleEvidence"`
	CompartmentLedger *int    `json:"compartmentLedger,omitempty"`
}

// expandV11 rebuilds the inline finding set from an interned
// document, validating every reference: a dangling index, a non-empty
// inline manifest in a table entry, or inline heavy fields on a
// record are malformed — the tables are the one home
// (REQ-result-export).
func expandV11(doc internedDocument) ([]Finding, error) {
	for i, e := range doc.Evidence {
		if e.RuntimeInputs < 0 || e.RuntimeInputs >= len(doc.RuntimeInputs) {
			return nil, fmt.Errorf("gomutant: evidence entry %d references runtime-inputs %d outside the table", i, e.RuntimeInputs)
		}
		if e.Evidence.RuntimeInputs != "" {
			return nil, fmt.Errorf("gomutant: evidence entry %d carries an inline runtime-inputs manifest beside its table reference", i)
		}
	}
	evAt := func(i int) (SubjectEvidence, error) {
		if i < 0 || i >= len(doc.Evidence) {
			return SubjectEvidence{}, fmt.Errorf("gomutant: evidence index %d outside the table", i)
		}
		e := doc.Evidence[i].Evidence
		// A row whose fingerprint is the zero value (a flat legacy row
		// under the current version, dropped to nothing) stays zero: a
		// manifest re-inlined onto it would make a fingerprint the record
		// form cannot encode, and the row is incomplete either way.
		if e.Fingerprint != (gofresh.Fingerprint{}) {
			e.RuntimeInputs = doc.RuntimeInputs[doc.Evidence[i].RuntimeInputs]
		}
		return e, nil
	}
	findings := make([]Finding, len(doc.Findings))
	for i, row := range doc.Findings {
		f := row.Finding
		if len(f.OracleEvidence) != 0 || f.CompartmentLedger != nil || f.TargetEvidence != (SubjectEvidence{}) {
			return nil, fmt.Errorf("gomutant: finding %d carries inline heavy fields beside its table references", i)
		}
		if row.TargetEvidence != nil {
			te, err := evAt(*row.TargetEvidence)
			if err != nil {
				return nil, fmt.Errorf("gomutant: finding %d target evidence: %w", i, err)
			}
			f.TargetEvidence = te
		}
		f.OracleEvidence = make([]SubjectEvidence, len(row.OracleEvidence))
		for j, idx := range row.OracleEvidence {
			oe, err := evAt(idx)
			if err != nil {
				return nil, fmt.Errorf("gomutant: finding %d oracle evidence %d: %w", i, j, err)
			}
			f.OracleEvidence[j] = oe
		}
		if row.CompartmentLedger != nil {
			if *row.CompartmentLedger < 0 || *row.CompartmentLedger >= len(doc.Ledgers) {
				return nil, fmt.Errorf("gomutant: finding %d ledger index %d outside the table", i, *row.CompartmentLedger)
			}
			l := doc.Ledgers[*row.CompartmentLedger]
			f.CompartmentLedger = &l
		}
		findings[i] = f
	}
	return findings, nil
}

// Export serializes findings and a coverage-bounds table to the
// versioned document gomutant owns (REQ-result-export), skipped results
// excluded (nothing was measured), deterministically ordered by symbol,
// and re-parses what it wrote as the self-check that the document is
// readable. The table is written whole as given; the per-selection
// replacement of bounds is the store's merge (REQ-result-unreached-bound),
// so a caller writing one selection's row through Export writes the
// document's whole table.
func Export(findings []Finding, bounds []CoverageBound) ([]byte, error) {
	data, _, _, err := writeDocument(findings, bounds)
	return data, err
}

// persistRecord is one record's persisted bytes — a one-record
// document — beside the record exactly as a parse of those bytes yields
// it: validated and canonicalized (absent lists made empty) in one
// record-sized step. The
// overlay installs entries through it and caches the parsed row; the
// store's document write canonicalizes each row it changed through it,
// so the document cache holds what a reader of the file sees. A record
// that fails is not serializable (REQ-result-export) and refuses with
// Export's wording; a skipped record has no persisted form and refuses
// (REQ-result-export excludes nothing-measured).
func persistRecord(f Finding) ([]byte, Finding, error) {
	if f.Skipped != "" {
		return nil, Finding{}, fmt.Errorf("gomutant: skipped record %s has no persisted form (nothing was measured)", f.Symbol)
	}
	data, _, parsed, err := writeDocument([]Finding{f}, nil)
	if err != nil {
		return nil, Finding{}, err
	}
	return data, parsed[0], nil
}

// parsedForm is the record persistRecord's parse yields.
func parsedForm(f Finding) (Finding, error) {
	_, row, err := persistRecord(f)
	return row, err
}

// writeDocument is the checked document writer: renderDocument's
// document, re-parsed as the self-check that what was written is
// readable (REQ-result-export), the parse returned so a caller that
// needs the persisted form reads it once. Export, the default document
// update, and the record-sized persist are this call; the store's
// per-commit install is renderDocument alone — its changed rows are
// each a parsed form already and its unchanged rows are the prior
// document's persisted forms, so the check would only re-read what a
// parse produced, over a whole document, under the document lock, per
// window — the per-commit cost the store's incremental write does not
// pay.
func writeDocument(findings []Finding, bounds []CoverageBound) (data []byte, kept, parsed []Finding, err error) {
	data, kept, err = renderDocument(findings, bounds)
	if err != nil {
		return nil, nil, nil, err
	}
	parsed, err = ParseFindings(data)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("gomutant: export invalid findings: %w", err)
	}
	return data, kept, parsed, nil
}

// renderDocument marshals findings and the coverage-bounds table —
// sorted by selection (REQ-result-unreached-bound) — as the interned
// document, skipped results excluded (nothing was measured), records
// ordered by symbol; kept is the records the document holds.
func renderDocument(findings []Finding, bounds []CoverageBound) (data []byte, kept []Finding, err error) {
	kept = make([]Finding, 0, len(findings))
	for _, f := range findings {
		if f.Skipped != "" {
			continue
		}
		if f.OracleEvidence == nil {
			f.OracleEvidence = []SubjectEvidence{}
		}
		if f.Operators == nil {
			f.Operators = []OperatorSummary{}
		}
		if f.Shape != nil && f.Shape.Manual != nil && f.Shape.Manual.Edits == nil {
			manual := *f.Shape.Manual
			manual.Edits = []ManualEdit{}
			shape := *f.Shape
			shape.Manual = &manual
			f.Shape = &shape
		}
		kept = append(kept, f)
	}
	sort.Slice(kept, func(i, j int) bool {
		if kept[i].Symbol != kept[j].Symbol {
			return kept[i].Symbol < kept[j].Symbol
		}
		return kept[i].Selection < kept[j].Selection
	})
	interned, err := internDocument(kept)
	if err != nil {
		return nil, nil, err
	}
	interned.CoverageBounds = slices.Clone(bounds)
	if interned.CoverageBounds == nil {
		interned.CoverageBounds = []CoverageBound{}
	}
	slices.SortFunc(interned.CoverageBounds, func(a, b CoverageBound) int { return strings.Compare(a.Selection, b.Selection) })
	data, err = json.MarshalIndent(interned, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return data, kept, nil
}

// ParseFindings loads a finding document: an unknown version is refused
// (REQ-result-export), an unknown field within a known version is discarded
// (REQ-result-tolerant — encoding/json drops unknown fields).
func ParseFindings(data []byte) ([]Finding, error) {
	doc, err := ParseDocument(data)
	if err != nil {
		return nil, err
	}
	return doc.Findings, nil
}

// Document is a parsed findings document: its records and its stated
// coverage bounds (REQ-result-unreached-bound).
type Document struct {
	Findings       []Finding
	CoverageBounds []CoverageBound
}

// ParseDocument loads a finding document with its document-level
// tables. The reading range, stated once here: a version above
// DocumentVersion refuses as ErrVersionAhead and one below
// OldestReadableDocumentVersion as ErrVersionBehind (REQ-result-export);
// versions 4-10 are the inline shape, upgraded on read; 11 interns the
// three measured-dominant components into document-level tables; 12
// adds the coverage-bounds table; 13 embeds the fingerprint's record
// form in every evidence row, the flat rows of every version before it
// upgraded on read; 14 references the tables by content key where 11
// to 13 referenced by position (interned.go); an unknown field within
// a known version is discarded (REQ-result-tolerant).
func ParseDocument(data []byte) (Document, error) {
	top, err := decodeKnownObject(data, map[string]bool{"version": true, "findings": true})
	if err != nil {
		return Document{}, fmt.Errorf("gomutant: parse findings document: %w", err)
	}
	var version int
	if err := json.Unmarshal(top["version"], &version); err != nil {
		return Document{}, fmt.Errorf("gomutant: parse findings version: %w", err)
	}
	if version > DocumentVersion {
		// A version AHEAD of this reader is nearly always "a newer
		// gomutant wrote this" - the recurring field shape is a
		// long-lived MCP server outliving a binary upgrade at the same
		// path, its surface dead until someone realizes the process
		// itself is stale. Name the probable cause and the signal, so
		// the reader is not sent hunting for document corruption.
		return Document{}, &DocumentVersionError{Version: version, Sentinel: ErrVersionAhead}
	}
	if version < OldestReadableDocumentVersion {
		return Document{}, &DocumentVersionError{Version: version, Sentinel: ErrVersionBehind}
	}
	if version >= 14 {
		return parseInternedDocument14(data, version)
	}
	if version >= 11 {
		return parseInternedDocument(data, version)
	}
	if err := upgradeLegacyEvidence(top, false); err != nil {
		return Document{}, err
	}
	findings, err := parseInlineFindings(top)
	if err != nil {
		return Document{}, err
	}
	if err := validateVersion17Features(findings, version); err != nil {
		return Document{}, err
	}
	return Document{Findings: findings}, nil
}

func validateVersion17Features(findings []Finding, version int) error {
	if version >= 17 {
		return nil
	}
	for i, finding := range findings {
		if finding.Selection != "" {
			return fmt.Errorf("gomutant: finding %d carries selection in version-%d document", i, version)
		}
		if len(finding.Withdrawn) > 0 {
			return fmt.Errorf("gomutant: finding %d carries withdrawn attestations in version-%d document", i, version)
		}
		for _, survivor := range finding.Survivors {
			if survivor.OtherSelection != "" {
				return fmt.Errorf("gomutant: finding %d survivor carries cross-selection verdicts in version-%d document", i, version)
			}
		}
	}
	return nil
}

// parseInternedDocument reads an interned document (version 11 or 12),
// expanding its tables and re-validating the expanded set through the
// inline path — every inline-era semantic check applies verbatim, and
// the interned shape adds its own structural checks in expandV11:
// version 12 carries the coverage-bounds table and requires it present
// — a document claiming 12 without the table is malformed, never read
// as unbounded (REQ-result-unreached-bound); a version-11 document
// carries none.
func parseInternedDocument(data []byte, version int) (Document, error) {
	findings, doc, err := parseInternedFindingsAndTables(data, version)
	if err != nil {
		return Document{}, err
	}
	bounds := slices.Clone(doc.CoverageBounds)
	if bounds == nil {
		bounds = []CoverageBound{}
	}
	return Document{Findings: findings, CoverageBounds: bounds}, nil
}

func parseInternedFindingsAndTables(data []byte, version int) ([]Finding, internedDocument, error) {
	top, err := decodeKnownObject(data, map[string]bool{
		"version": true, "runtimeInputsTable": true, "evidenceTable": true, "ledgerTable": true, "findings": true, "coverageBounds": true,
	})
	if err != nil {
		return nil, internedDocument{}, fmt.Errorf("gomutant: parse findings document: %w", err)
	}
	required := []string{"runtimeInputsTable", "evidenceTable", "ledgerTable", "findings"}
	if version >= 12 {
		required = append(required, "coverageBounds")
	}
	for _, name := range required {
		value, ok := top[name]
		if !ok || isJSONNull(value) {
			return nil, internedDocument{}, fmt.Errorf("gomutant: findings document field %s is missing or null", name)
		}
	}
	if err := validateInternedV11JSON(top, version); err != nil {
		return nil, internedDocument{}, err
	}
	if version < 13 {
		if err := upgradeLegacyEvidence(top, true); err != nil {
			return nil, internedDocument{}, err
		}
		if data, err = json.Marshal(top); err != nil {
			return nil, internedDocument{}, err
		}
	}
	var doc internedDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, internedDocument{}, fmt.Errorf("gomutant: parse interned findings: %w", err)
	}
	for _, b := range doc.CoverageBounds {
		if b.Selection == "" || len(b.Unreached) == 0 {
			return nil, internedDocument{}, fmt.Errorf("gomutant: findings document coverage bound needs a selection and its unreached symbols")
		}
	}
	expanded, err := expandV11(doc)
	if err != nil {
		return nil, internedDocument{}, err
	}
	if err := validateInternedRecords(doc); err != nil {
		return nil, internedDocument{}, err
	}
	if err := validateVersion17Features(expanded, version); err != nil {
		return nil, internedDocument{}, err
	}
	return expanded, doc, nil
}

// validateInternedRecords re-runs every inline-era check over the
// interned document, one record at a time and with each
// runtime-inputs manifest replaced by an equality-preserving
// placeholder. Both substitutions keep the reader bounded by the
// document on disk, not the duplicated inline form it stands for:
// per-record streaming caps the transient at one record, and the
// placeholder caps the churn a large manifest would otherwise pay per
// reference. The substitution is sound because manifest CONTENT is
// validation-inert — the checks read only its non-emptiness (the
// required-pin rule) and its cross-row equality (the finding-wide
// runtime anchor), and the placeholder preserves both exactly:
// empty stays empty, and two entries map to the same placeholder iff
// their manifests are equal (equal content collapses to one canonical
// table index, so equality never depends on the index a row happens
// to cite).
func validateInternedRecords(doc internedDocument) error {
	canonical := map[string]int{}
	placeholders := make([]string, len(doc.RuntimeInputs))
	for i, manifest := range doc.RuntimeInputs {
		if manifest == "" {
			continue
		}
		j, ok := canonical[manifest]
		if !ok {
			canonical[manifest] = i
			j = i
		}
		placeholders[i] = fmt.Sprintf("\x00gomutant-runtime-inputs:%d", j)
	}
	small := doc
	small.RuntimeInputs = placeholders
	expanded, err := expandV11(small)
	if err != nil {
		return err
	}
	symbols := map[string]bool{}
	for i, finding := range expanded {
		inline, err := json.Marshal(finding)
		if err != nil {
			return fmt.Errorf("gomutant: re-validate interned findings: %w", err)
		}
		if _, err := decodeInlineFinding(inline, i); err != nil {
			return err
		}
		if symbols[finding.Symbol] {
			return fmt.Errorf("gomutant: duplicate finding symbol %s", finding.Symbol)
		}
		symbols[finding.Symbol] = true
	}
	return nil
}

// parseInlineFindings validates and decodes the inline finding array —
// the inline shape's own path and the re-validation path for every
// expanded interned document (the version mapping is ParseDocument's).
func parseInlineFindings(top map[string]json.RawMessage) ([]Finding, error) {
	if isJSONNull(top["findings"]) {
		return nil, fmt.Errorf("gomutant: findings must be an array")
	}
	var rawFindings []json.RawMessage
	if err := json.Unmarshal(top["findings"], &rawFindings); err != nil {
		return nil, fmt.Errorf("gomutant: parse findings: %w", err)
	}
	findings := make([]Finding, len(rawFindings))
	symbols := map[string]bool{}
	for i, raw := range rawFindings {
		finding, err := decodeInlineFinding(raw, i)
		if err != nil {
			return nil, err
		}
		if symbols[finding.Symbol] {
			return nil, fmt.Errorf("gomutant: duplicate finding symbol %s", finding.Symbol)
		}
		symbols[finding.Symbol] = true
		findings[i] = finding
	}
	return findings, nil
}

// inlineFindingFields is the known field set of one inline finding
// record (REQ-result-tolerant: unknown fields within a known version
// are dropped, never refused).
var inlineFindingFields = map[string]bool{
	"symbol": true, "selection": true, "labels": true, "bodyHash": true, "operatorSet": true,
	"budget": true, "shape": true, "targetEvidence": true, "oracleEvidence": true,
	"oracleExplicit": true, "oracleTimeout": true, "oracleTimeoutDerived": true, "oracleMemoryBytes": true,
	"oracleCeilingDecided": true, "propertyRegime": true, "oracleExecutionPolicy": true,
	"compartmentLedger": true, "commit": true, "dirty": true, "evidenceFault": true, "run": true, "stagedTree": true, "exempted": true,
	"candidateCount": true, "generated": true, "mutants": true, "killed": true,
	"discarded": true, "operators": true, "kills": true, "survivors": true, "attested": true, "withdrawn": true,
	"candidateEvidence": true,
}

func validateJSONNonNullArray(raw json.RawMessage) ([]json.RawMessage, error) {
	if isJSONNull(raw) {
		return nil, fmt.Errorf("expected array")
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	for i, item := range items {
		if isJSONNull(item) {
			return nil, fmt.Errorf("element %d is null", i)
		}
	}
	return items, nil
}

func validateCompartmentFileHeaderJSON(raw json.RawMessage) error {
	fields, err := validateRequiredObject(raw, map[string]bool{"file": true, "hash": true, "embedded": true, "bindings": true}, []string{"file", "hash"})
	if err != nil {
		return err
	}
	if bRaw, ok := fields["bindings"]; ok {
		bFields, err := validateRequiredObject(bRaw, map[string]bool{"package": true, "references": true, "imports": true}, []string{"package"})
		if err != nil {
			return fmt.Errorf("bindings: %w", err)
		}
		if refRaw, ok := bFields["references"]; ok {
			if _, err := validateJSONNonNullArray(refRaw); err != nil {
				return fmt.Errorf("bindings references: %w", err)
			}
		}
		if impRaw, ok := bFields["imports"]; ok {
			imps, err := validateJSONNonNullArray(impRaw)
			if err != nil {
				return fmt.Errorf("bindings imports: %w", err)
			}
			for i, imp := range imps {
				if _, err := validateRequiredObject(imp, map[string]bool{"name": true, "path": true}, []string{"name", "path"}); err != nil {
					return fmt.Errorf("bindings import %d: %w", i, err)
				}
			}
		}
	}
	return nil
}

func validateCompartmentLedgerJSON(raw json.RawMessage) error {
	fields, err := validateRequiredObject(raw, map[string]bool{"bindingStrategy": true, "baseFiles": true, "declarations": true, "fileHeaders": true}, nil)
	if err != nil {
		return err
	}
	for _, listName := range []string{"baseFiles", "fileHeaders"} {
		if listRaw, ok := fields[listName]; ok {
			headers, err := validateJSONNonNullArray(listRaw)
			if err != nil {
				return fmt.Errorf("%s: %w", listName, err)
			}
			for i, h := range headers {
				if err := validateCompartmentFileHeaderJSON(h); err != nil {
					return fmt.Errorf("%s %d: %w", listName, i, err)
				}
			}
		}
	}
	if declRaw, ok := fields["declarations"]; ok {
		decls, err := validateJSONNonNullArray(declRaw)
		if err != nil {
			return fmt.Errorf("declarations: %w", err)
		}
		for i, d := range decls {
			if _, err := validateRequiredObject(d, map[string]bool{"file": true, "kind": true, "name": true, "receiver": true, "hash": true, "package": true}, []string{"file", "kind", "name", "hash"}); err != nil {
				return fmt.Errorf("declaration %d: %w", i, err)
			}
		}
	}
	return nil
}

func validateTargetShapeJSON(raw json.RawMessage) error {
	fields, err := validateRequiredObject(raw, map[string]bool{"structural": true, "manual": true}, nil)
	if err != nil {
		return err
	}
	if sRaw, ok := fields["structural"]; ok {
		sFields, err := validateRequiredObject(sRaw, map[string]bool{"class": true, "packages": true, "forbidden": true, "type": true, "interface": true}, []string{"class"})
		if err != nil {
			return fmt.Errorf("structural: %w", err)
		}
		if pkgRaw, ok := sFields["packages"]; ok {
			if _, err := validateJSONNonNullArray(pkgRaw); err != nil {
				return fmt.Errorf("structural packages: %w", err)
			}
		}
	}
	if mRaw, ok := fields["manual"]; ok {
		mFields, err := validateRequiredObject(mRaw, map[string]bool{"file": true, "edits": true}, []string{"file", "edits"})
		if err != nil {
			return fmt.Errorf("manual: %w", err)
		}
		edits, err := validateJSONNonNullArray(mFields["edits"])
		if err != nil {
			return fmt.Errorf("manual edits: %w", err)
		}
		for i, edit := range edits {
			if _, err := validateRequiredObject(edit, map[string]bool{"find": true, "replace": true}, []string{"find", "replace"}); err != nil {
				return fmt.Errorf("manual edit %d: %w", i, err)
			}
		}
	}
	return nil
}

func validateCoverageBoundsJSON(raw json.RawMessage) error {
	bounds, err := validateJSONNonNullArray(raw)
	if err != nil {
		return fmt.Errorf("gomutant: parse coverageBounds: %w", err)
	}
	for i, b := range bounds {
		fields, err := validateRequiredObject(b, map[string]bool{"selection": true, "unreached": true}, []string{"selection", "unreached"})
		if err != nil {
			return fmt.Errorf("gomutant: coverage bound %d: %w", i, err)
		}
		if _, err := validateJSONNonNullArray(fields["unreached"]); err != nil {
			return fmt.Errorf("gomutant: coverage bound %d unreached: %w", i, err)
		}
	}
	return nil
}

func validateFindingNestedObjects(fields map[string]json.RawMessage) error {
	if raw, ok := fields["labels"]; ok {
		if _, err := validateJSONNonNullArray(raw); err != nil {
			return fmt.Errorf("labels: %w", err)
		}
	}
	if raw, ok := fields["shape"]; ok {
		if err := validateTargetShapeJSON(raw); err != nil {
			return fmt.Errorf("shape: %w", err)
		}
	}
	if raw, ok := fields["compartmentLedger"]; ok {
		if err := validateCompartmentLedgerJSON(raw); err != nil {
			return fmt.Errorf("compartmentLedger: %w", err)
		}
	}
	if raw, ok := fields["kills"]; ok {
		records, err := validateJSONNonNullArray(raw)
		if err != nil {
			return fmt.Errorf("kills: %w", err)
		}
		for i, record := range records {
			if _, err := validateRequiredObject(record, map[string]bool{"position": true, "operator": true, "killer": true}, []string{"position", "operator", "killer"}); err != nil {
				return fmt.Errorf("kill %d: %w", i, err)
			}
		}
	}
	if raw, ok := fields["exempted"]; ok {
		records, err := validateJSONNonNullArray(raw)
		if err != nil {
			return fmt.Errorf("exempted: %w", err)
		}
		for i, record := range records {
			if _, err := validateRequiredObject(record, map[string]bool{"subject": true, "reason": true, "rationale": true}, []string{"subject", "reason", "rationale"}); err != nil {
				return fmt.Errorf("exemption %d: %w", i, err)
			}
		}
	}
	if raw, ok := fields["survivors"]; ok {
		records, err := validateJSONNonNullArray(raw)
		if err != nil {
			return fmt.Errorf("survivors: %w", err)
		}
		for i, record := range records {
			if _, err := validateRequiredObject(record, map[string]bool{
				"position": true, "operator": true, "extent": true, "site": true,
				"execution": true, "withdrawnKiller": true, "otherSelection": true,
			}, []string{"position", "operator"}); err != nil {
				return fmt.Errorf("survivor %d: %w", i, err)
			}
		}
	}
	if raw, ok := fields["operators"]; ok {
		records, err := validateJSONNonNullArray(raw)
		if err != nil {
			return fmt.Errorf("operators: %w", err)
		}
		for i, record := range records {
			if _, err := validateRequiredObject(record,
				map[string]bool{"operator": true, "generated": true, "discarded": true, "killed": true, "survived": true},
				[]string{"operator", "generated", "discarded", "killed", "survived"}); err != nil {
				return fmt.Errorf("operator summary %d: %w", i, err)
			}
		}
	}
	if raw, ok := fields["candidateEvidence"]; ok {
		records, err := validateJSONNonNullArray(raw)
		if err != nil {
			return fmt.Errorf("candidateEvidence: %w", err)
		}
		for i, record := range records {
			if _, err := validateRequiredObject(record,
				map[string]bool{"position": true, "operator": true, "reason": true, "disposition": true},
				[]string{"position", "operator", "reason", "disposition"}); err != nil {
				return fmt.Errorf("candidate evidence %d: %w", i, err)
			}
		}
	}
	if raw, ok := fields["attested"]; ok {
		records, err := validateJSONNonNullArray(raw)
		if err != nil {
			return fmt.Errorf("attested: %w", err)
		}
		for i, record := range records {
			if _, err := validateRequiredObject(record, map[string]bool{"position": true, "operator": true, "reason": true, "site": true}, []string{"position", "operator", "reason"}); err != nil {
				return fmt.Errorf("attestation %d: %w", i, err)
			}
		}
	}
	if raw, ok := fields["withdrawn"]; ok {
		records, err := validateJSONNonNullArray(raw)
		if err != nil {
			return fmt.Errorf("withdrawn: %w", err)
		}
		for i, record := range records {
			if _, err := validateRequiredObject(record, map[string]bool{"position": true, "operator": true, "priorReason": true, "reason": true, "site": true}, []string{"position", "operator", "priorReason", "reason"}); err != nil {
				return fmt.Errorf("withdrawn attestation %d: %w", i, err)
			}
		}
	}
	return nil
}

func validateInternedFindingRowJSON(raw json.RawMessage, i int) error {
	rowFields, err := validateRequiredObject(raw, map[string]bool{
		"finding": true, "targetEvidence": true, "oracleEvidence": true, "compartmentLedger": true,
	}, []string{"finding", "oracleEvidence"})
	if err != nil {
		return fmt.Errorf("gomutant: finding row %d: %w", i, err)
	}
	if _, err := validateJSONNonNullArray(rowFields["oracleEvidence"]); err != nil {
		return fmt.Errorf("gomutant: finding row %d oracleEvidence: %w", i, err)
	}
	fFields, err := decodeKnownObject(rowFields["finding"], inlineFindingFields)
	if err != nil {
		return fmt.Errorf("gomutant: parse finding %d: %w", i, err)
	}
	for name, value := range fFields {
		if name == "oracleEvidence" {
			continue
		}
		if isJSONNull(value) {
			return fmt.Errorf("gomutant: finding %d field %s is null", i, name)
		}
	}
	if err := validateFindingNestedObjects(withoutKey(fFields, "operators")); err != nil {
		return fmt.Errorf("gomutant: parse finding %d: %w", i, err)
	}
	if rawOps, ok := fFields["operators"]; ok {
		records, err := validateJSONNonNullArray(rawOps)
		if err != nil {
			return fmt.Errorf("gomutant: parse finding %d: operators: %w", i, err)
		}
		for j, record := range records {
			if _, err := validateRequiredObject(record,
				map[string]bool{"operator": true, "generated": true, "discarded": true, "killed": true, "survived": true},
				[]string{"operator", "generated", "killed"}); err != nil {
				return fmt.Errorf("gomutant: parse finding %d: operator summary %d: %w", i, j, err)
			}
		}
	}
	return nil
}

func withoutKey(m map[string]json.RawMessage, drop string) map[string]json.RawMessage {
	if _, ok := m[drop]; !ok {
		return m
	}
	cp := make(map[string]json.RawMessage, len(m)-1)
	for k, v := range m {
		if k != drop {
			cp[k] = v
		}
	}
	return cp
}

func validateInternedV11JSON(top map[string]json.RawMessage, version int) error {
	if _, err := validateJSONNonNullArray(top["runtimeInputsTable"]); err != nil {
		return fmt.Errorf("gomutant: parse runtimeInputsTable: %w", err)
	}
	evEntries, err := validateJSONNonNullArray(top["evidenceTable"])
	if err != nil {
		return fmt.Errorf("gomutant: parse evidenceTable: %w", err)
	}
	for i, entry := range evEntries {
		if _, err := validateRequiredObject(entry, map[string]bool{"evidence": true, "runtimeInputs": true}, []string{"evidence", "runtimeInputs"}); err != nil {
			return fmt.Errorf("gomutant: evidence table entry %d: %w", i, err)
		}
	}
	ledgerEntries, err := validateJSONNonNullArray(top["ledgerTable"])
	if err != nil {
		return fmt.Errorf("gomutant: parse ledgerTable: %w", err)
	}
	for i, entry := range ledgerEntries {
		if err := validateCompartmentLedgerJSON(entry); err != nil {
			return fmt.Errorf("gomutant: ledger table entry %d: %w", i, err)
		}
	}
	if version >= 12 {
		if err := validateCoverageBoundsJSON(top["coverageBounds"]); err != nil {
			return err
		}
	}
	rows, err := validateJSONNonNullArray(top["findings"])
	if err != nil {
		return fmt.Errorf("gomutant: parse findings: %w", err)
	}
	for i, row := range rows {
		if err := validateInternedFindingRowJSON(row, i); err != nil {
			return err
		}
	}
	return nil
}

// decodeInlineFinding validates and decodes one inline finding record
// with every inline-era JSON-level and value-level check. The
// duplicate-symbol check is cross-record state and stays with the
// callers.
func decodeInlineFinding(raw json.RawMessage, i int) (Finding, error) {
	var finding Finding
	fields, err := decodeKnownObject(raw, inlineFindingFields)
	if err != nil {
		return finding, fmt.Errorf("gomutant: parse finding %d: %w", i, err)
	}
	complete := true
	required := []string{"symbol", "bodyHash", "operatorSet", "budget", "targetEvidence", "oracleEvidence", "oracleExplicit", "oracleTimeout", "dirty", "candidateCount", "generated", "mutants", "killed", "discarded", "operators"}
	for _, name := range required {
		value, ok := fields[name]
		if !ok {
			complete = false
		} else if isJSONNull(value) {
			return finding, fmt.Errorf("gomutant: finding %d field %s is null", i, name)
		}
	}
	if value, ok := fields["dirty"]; ok && isJSONNull(value) {
		return finding, fmt.Errorf("gomutant: finding %d field dirty is null", i)
	}
	if err := json.Unmarshal(raw, &finding); err != nil {
		return finding, fmt.Errorf("gomutant: parse finding %d: %w", i, err)
	}
	if finding.Symbol == "" || finding.BodyHash == "" || finding.OperatorSet == "" || finding.OracleTimeout == "" {
		complete = false
	} else if duration, err := time.ParseDuration(finding.OracleTimeout); err != nil || duration <= 0 || duration.String() != finding.OracleTimeout {
		complete = false
	}
	nestedComplete, err := validateFindingEncoding(fields, &finding)
	if err != nil {
		return finding, fmt.Errorf("gomutant: parse finding %d: %w", i, err)
	}
	complete = complete && nestedComplete
	if finding.Commit == "" && !finding.Dirty {
		complete = false
	}
	if !complete {
		return finding, fmt.Errorf("gomutant: finding %d is missing or has invalid required evidence", i)
	}
	return finding, nil
}

func validateFindingEncoding(fields map[string]json.RawMessage, finding *Finding) (bool, error) {
	complete := true
	for name, value := range fields {
		if isJSONNull(value) {
			return false, fmt.Errorf("field %s is null", name)
		}
	}
	if err := validateFindingNestedObjects(fields); err != nil {
		return false, err
	}
	if err := validateSelectionKey(finding.Selection); err != nil {
		return false, err
	}
	if finding.Shape != nil {
		// A shaped finding's subject is its declared shape: target
		// evidence must be absent (the zero row), never a measured
		// symbol row a serving path could mistake for evidence
		// (REQ-target-structural, REQ-target-manual-recipes).
		if finding.TargetEvidence != (SubjectEvidence{}) {
			complete = false
		}
		if finding.Shape.Structural == nil && finding.Shape.Manual == nil {
			return false, fmt.Errorf("shape declares no form")
		}
	} else if raw, ok := fields["targetEvidence"]; ok {
		valid, err := validateSubjectEvidence(raw)
		if err != nil {
			return false, fmt.Errorf("targetEvidence: %w", err)
		}
		complete = complete && valid
		if finding.TargetEvidence.Symbol != finding.Symbol {
			complete = false
		}
	}
	if raw, ok := fields["oracleEvidence"]; ok {
		var oracle []json.RawMessage
		if err := json.Unmarshal(raw, &oracle); err != nil {
			return false, fmt.Errorf("oracleEvidence: %w", err)
		}
		if len(oracle) == 0 {
			complete = false
		}
		seenOracle := map[string]bool{}
		for i, evidence := range oracle {
			valid, err := validateSubjectEvidence(evidence)
			if err != nil {
				return false, fmt.Errorf("oracleEvidence %d: %w", i, err)
			}
			complete = complete && valid
			if seenOracle[finding.OracleEvidence[i].Symbol] {
				return false, fmt.Errorf("duplicate oracle evidence symbol %s", finding.OracleEvidence[i].Symbol)
			}
			seenOracle[finding.OracleEvidence[i].Symbol] = true
		}
	}
	if complete {
		// The finding-wide runtime anchor: the target row for symbol
		// findings, the first oracle row for shaped findings (whose
		// target row is the zero value by contract).
		anchor := finding.TargetEvidence
		if finding.Shape != nil && len(finding.OracleEvidence) > 0 {
			anchor = finding.OracleEvidence[0]
		}
		for _, evidence := range finding.OracleEvidence {
			if evidence.RuntimeInputs != anchor.RuntimeInputs ||
				evidence.RuntimeDigest != anchor.RuntimeDigest ||
				evidence.RuntimeUnverifiable != anchor.RuntimeUnverifiable ||
				evidence.RuntimeReason != anchor.RuntimeReason {
				return false, fmt.Errorf("subject runtime evidence is not finding-wide")
			}
		}
	}
	if finding.CandidateCount < 0 || finding.Generated < 0 || finding.Mutants < 0 || finding.Killed < 0 || finding.Discarded < 0 ||
		finding.Killed > finding.Mutants || len(finding.Survivors) != finding.Mutants-finding.Killed {
		return false, fmt.Errorf("mutant counts do not match killed and survivor records")
	}
	if len(finding.Kills) != 0 {
		// Kill attribution is all-or-nothing (REQ-core-attributed-kills):
		// a partial list silently drops reported kills from its accounting.
		if len(finding.Kills) != finding.Killed {
			return false, fmt.Errorf("kill attributions do not cover the killed count")
		}
		survivorIdentities := make(map[survivorKey]bool, len(finding.Survivors))
		for _, survivor := range finding.Survivors {
			survivorIdentities[survivorKey{survivor.Position, survivor.Operator}] = true
		}
		seenKills := make(map[survivorKey]bool, len(finding.Kills))
		for _, kill := range finding.Kills {
			if kill.Position == "" || kill.Operator == "" || kill.Killer == "" {
				return false, fmt.Errorf("kill attribution is missing its position, operator, or killer")
			}
			key := survivorKey{kill.Position, kill.Operator}
			if seenKills[key] {
				return false, fmt.Errorf("duplicate kill attribution %s %s", kill.Position, kill.Operator)
			}
			seenKills[key] = true
			if survivorIdentities[key] {
				return false, fmt.Errorf("kill attribution %s %s names a survivor", kill.Position, kill.Operator)
			}
		}
	}
	generatedTotal, countsSafe := addNonnegative(finding.Mutants, finding.Discarded)
	expectedGenerated := finding.CandidateCount
	if finding.Budget > 0 {
		expectedGenerated = min(finding.Budget, finding.CandidateCount)
	}
	if !countsSafe || finding.Budget < 0 || finding.Generated != generatedTotal || finding.Generated != expectedGenerated {
		return false, fmt.Errorf("candidate, budget, and mutant counts do not reconcile")
	}
	survivors := make(map[survivorKey]bool, len(finding.Survivors))
	survivorsByOperator := map[string]int{}
	for _, survivor := range finding.Survivors {
		if survivor.Position == "" || survivor.Operator == "" {
			return false, fmt.Errorf("survivor identity is incomplete")
		}
		key := survivorKey{survivor.Position, survivor.Operator}
		if survivors[key] {
			return false, fmt.Errorf("duplicate survivor %s %s", survivor.Position, survivor.Operator)
		}
		survivors[key] = true
		survivorsByOperator[survivor.Operator]++
		if survivor.OtherSelection != "" {
			others, err := parseSelectionVerdicts(survivor.OtherSelection)
			if err != nil {
				return false, fmt.Errorf("survivor %s %s otherSelection: %w", survivor.Position, survivor.Operator, err)
			}
			seenSel := map[string]bool{}
			for idx, other := range others {
				if other.Selection == finding.Selection {
					return false, fmt.Errorf("survivor %s %s otherSelection repeats finding selection %q", survivor.Position, survivor.Operator, other.Selection)
				}
				if seenSel[other.Selection] {
					return false, fmt.Errorf("survivor %s %s duplicate otherSelection %q", survivor.Position, survivor.Operator, other.Selection)
				}
				seenSel[other.Selection] = true
				if idx > 0 && others[idx-1].Selection >= other.Selection {
					return false, fmt.Errorf("survivor %s %s otherSelection is not canonically ordered", survivor.Position, survivor.Operator)
				}
			}
		}
	}
	if _, ok := fields["operators"]; ok {
		previous := ""
		remainingGenerated, remainingDiscarded := finding.Generated, finding.Discarded
		remainingKilled, remainingSurvived := finding.Killed, len(finding.Survivors)
		for i, summary := range finding.Operators {
			if summary.Operator == "" || summary.Generated <= 0 || summary.Discarded < 0 || summary.Killed < 0 || summary.Survived < 0 ||
				summary.Discarded > summary.Generated || summary.Killed > summary.Generated-summary.Discarded ||
				summary.Survived != summary.Generated-summary.Discarded-summary.Killed {
				return false, fmt.Errorf("operator summary %d counts are invalid", i)
			}
			if i > 0 && summary.Operator <= previous {
				return false, fmt.Errorf("operator summaries are not canonically ordered")
			}
			if summary.Survived != survivorsByOperator[summary.Operator] {
				return false, fmt.Errorf("operator summary %s does not match survivor identities", summary.Operator)
			}
			if summary.Generated > remainingGenerated || summary.Discarded > remainingDiscarded || summary.Killed > remainingKilled || summary.Survived > remainingSurvived {
				return false, fmt.Errorf("operator summaries exceed finding totals")
			}
			previous = summary.Operator
			remainingGenerated -= summary.Generated
			remainingDiscarded -= summary.Discarded
			remainingKilled -= summary.Killed
			remainingSurvived -= summary.Survived
		}
		if remainingGenerated != 0 || remainingDiscarded != 0 || remainingKilled != 0 || remainingSurvived != 0 {
			return false, fmt.Errorf("operator summaries do not match finding totals")
		}
	}
	flaggedSeen := map[survivorKey]bool{}
	flaggedKilled, flaggedDiscarded := map[string]int{}, map[string]int{}
	for _, evidence := range finding.CandidateEvidence {
		if evidence.Position == "" || evidence.Operator == "" || evidence.Reason == "" {
			return false, fmt.Errorf("candidate evidence is incomplete")
		}
		key := survivorKey{evidence.Position, evidence.Operator}
		if flaggedSeen[key] {
			return false, fmt.Errorf("duplicate candidate evidence %s %s", evidence.Position, evidence.Operator)
		}
		flaggedSeen[key] = true
		switch evidence.Disposition {
		case "survived":
			if !survivors[key] {
				return false, fmt.Errorf("candidate evidence %s %s claims a survivor the record does not carry", evidence.Position, evidence.Operator)
			}
		case "killed":
			flaggedKilled[evidence.Operator]++
		case "discarded":
			flaggedDiscarded[evidence.Operator]++
		default:
			return false, fmt.Errorf("candidate evidence disposition %q is invalid", evidence.Disposition)
		}
		if evidence.Disposition != "survived" && survivors[key] {
			return false, fmt.Errorf("candidate evidence %s %s contradicts the recorded survivor", evidence.Position, evidence.Operator)
		}
	}
	if len(flaggedKilled) != 0 || len(flaggedDiscarded) != 0 {
		byOperator := make(map[string]OperatorSummary, len(finding.Operators))
		for _, summary := range finding.Operators {
			byOperator[summary.Operator] = summary
		}
		for operator, killed := range flaggedKilled {
			if killed > byOperator[operator].Killed {
				return false, fmt.Errorf("candidate evidence kill counts exceed operator %s totals", operator)
			}
		}
		for operator, discarded := range flaggedDiscarded {
			if discarded > byOperator[operator].Discarded {
				return false, fmt.Errorf("candidate evidence discard counts exceed operator %s totals", operator)
			}
		}
	}
	attested := map[survivorKey]bool{}
	for _, attestation := range finding.Attested {
		key := survivorKey{attestation.Position, attestation.Operator}
		if attestation.Position == "" || attestation.Operator == "" || attestation.Reason == "" {
			return false, fmt.Errorf("attestation is incomplete")
		}
		if !survivors[key] {
			return false, fmt.Errorf("attestation does not name a survivor")
		}
		if attested[key] {
			return false, fmt.Errorf("duplicate attestation %s %s", attestation.Position, attestation.Operator)
		}
		attested[key] = true
	}
	withdrawnSeen := map[survivorKey]bool{}
	for _, w := range finding.Withdrawn {
		key := survivorKey{w.Position, w.Operator}
		if w.Position == "" || w.Operator == "" || w.PriorReason == "" || w.Reason == "" {
			return false, fmt.Errorf("withdrawn attestation is incomplete")
		}
		if !survivors[key] {
			return false, fmt.Errorf("withdrawn attestation does not name a survivor")
		}
		if attested[key] {
			return false, fmt.Errorf("withdrawn attestation %s %s is also attested", w.Position, w.Operator)
		}
		if withdrawnSeen[key] {
			return false, fmt.Errorf("duplicate withdrawn attestation %s %s", w.Position, w.Operator)
		}
		withdrawnSeen[key] = true
	}
	return complete, nil
}

func addNonnegative(a, b int) (int, bool) {
	if a < 0 || b < 0 || b > int(^uint(0)>>1)-a {
		return 0, false
	}
	return a + b, true
}

// validateSubjectEvidence decodes one persisted row and judges its
// completeness: the outer shape and the fingerprint's record form are
// their decoders' refusals; completeness is gomutant's — every pin a
// serving path reads non-empty (the symbol, the two closure hashes, the
// two code guards, the observation assertion and proof, the manifest
// and its digest), the disposition rule (unverifiable exactly when a
// reason is recorded), the proof rule (observable exactly when no
// reason is recorded), and a tree-relative module base — so a document
// carrying a row a serving path could not judge is incomplete rather
// than served on empty pins (REQ-result-record).
func validateSubjectEvidence(raw json.RawMessage) (bool, error) {
	var evidence SubjectEvidence
	if err := json.Unmarshal(raw, &evidence); err != nil {
		return false, err
	}
	return subjectEvidenceComplete(evidence)
}

// subjectEvidenceComplete is the completeness judgment over a decoded
// row (validateSubjectEvidence's rules), shared with the legacy reader.
func subjectEvidenceComplete(evidence SubjectEvidence) (bool, error) {
	if evidence.RuntimeUnverifiable != (evidence.RuntimeReason != "") {
		return false, nil
	}
	// No writer produces a module base today: a re-measure carries none,
	// and the attestation-pin view strips it so the absence sheds no
	// disposition. A persisted pre-anchor record still carries one, read
	// by evidenceBase on the tree side and by the store's manifest
	// resolution, and an absolute or escaping form would draw the
	// portable-containment line outside the tree (REQ-result-layers), so
	// the parse refuses it.
	if evidence.ModuleBase != "" {
		if strings.HasPrefix(evidence.ModuleBase, "/") || strings.Contains(evidence.ModuleBase, "\\") {
			return false, fmt.Errorf("module base %q is not a tree-relative slash path", evidence.ModuleBase)
		}
		for _, segment := range strings.Split(evidence.ModuleBase, "/") {
			if segment == "" || segment == "." || segment == ".." {
				return false, fmt.Errorf("module base %q is not a clean tree-relative path", evidence.ModuleBase)
			}
		}
	}
	proof := evidence.ObservationProof
	if proof.Observable == (proof.Reason != "") {
		return false, nil
	}
	for _, pin := range []string{
		evidence.Symbol, evidence.MaximalClosure, evidence.TestVariantClosure,
		evidence.Guards.Toolchain, evidence.Guards.BuildConfig,
		evidence.ObservationAssertion, proof.Strategy, proof.Subject.Package, proof.Subject.Symbol, proof.Evidence,
		evidence.RuntimeInputs, evidence.RuntimeDigest,
	} {
		if pin == "" {
			return false, nil
		}
	}
	return true, nil
}

// legacyEvidenceKeys is the flat row shape documents up to version 12
// carry — the fingerprint's fields beside gomutant's, the proof
// flattened to six scalars, no result kind — read by upgrading each row
// to the current shape before the typed decode; the current validation
// then applies verbatim (REQ-result-export).
var legacyEvidenceKeys = func() map[string]bool {
	keys := map[string]bool{"symbol": true, "observationObservable": true, "moduleBase": true, "runtimeUnverifiable": true, "runtimeReason": true}
	for name := range legacyFieldTargets(&gofresh.Fingerprint{}) {
		keys[name] = true
	}
	return keys
}()

// legacyFieldTargets maps each flat string key of a legacy row onto
// the fingerprint field it fills — the one table the key set and the
// upgrade both read.
func legacyFieldTargets(fp *gofresh.Fingerprint) map[string]*string {
	return map[string]*string{
		"maximalClosure": &fp.MaximalClosure, "testVariantClosure": &fp.TestVariantClosure,
		"toolchain": &fp.Guards.Toolchain, "buildConfig": &fp.Guards.BuildConfig,
		"observationAssertion": &fp.ObservationAssertion, "purityAssertion": &fp.PurityAssertion,
		"dynamicStateVouches": &fp.DynamicStateVouches, "packageProcessDischarges": &fp.PackageProcessDischarges,
		"dynamicStateStrategy": &fp.DynamicStateStrategy, "closureStrategy": &fp.ClosureStrategy,
		"runtimeInputs": &fp.RuntimeInputs, "runtimeDigest": &fp.RuntimeDigest,
		"observationStrategy": &fp.ObservationProof.Strategy, "observationSubjectPackage": &fp.ObservationProof.Subject.Package,
		"observationSubjectSymbol": &fp.ObservationProof.Subject.Symbol, "observationReason": &fp.ObservationProof.Reason,
		"observationEvidence": &fp.ObservationProof.Evidence,
	}
}

// upgradeLegacyEvidenceRow rewrites one flat row into the current wire
// form: the fingerprint's keys move under `fingerprint` — built as the
// record value and encoded by its own encoder, so the bytes are the
// form's canonical ones and the record's decoder reads them back (the
// one shape the encoder admits and the decoder refuses, a positive
// proof carrying a reason, is refused here first) — with the proof
// nested and the code-result kind stamped (every legacy row is a
// code-result fingerprint), gomutant's four stay outer; a duplicated
// key or a null refuses as it always did, an unknown key is tolerated
// as it always was; a row with no fingerprint content at all (the zero
// row) carries no fingerprint. legacyFieldTargets is the one table of
// the flat keys' destinations, the key set derived from it.
func upgradeLegacyEvidenceRow(raw json.RawMessage) (json.RawMessage, error) {
	fields, err := decodeKnownObject(raw, legacyEvidenceKeys)
	if err != nil {
		return nil, err
	}
	for name, value := range fields {
		if isJSONNull(value) {
			return nil, fmt.Errorf("field %s is null", name)
		}
	}
	str := func(name string) (string, error) {
		value, ok := fields[name]
		if !ok {
			return "", nil
		}
		var v string
		if err := json.Unmarshal(value, &v); err != nil {
			return "", fmt.Errorf("field %s: %w", name, err)
		}
		return v, nil
	}
	var fp gofresh.Fingerprint
	for name, dst := range legacyFieldTargets(&fp) {
		v, err := str(name)
		if err != nil {
			return nil, err
		}
		*dst = v
	}
	if value, ok := fields["observationObservable"]; ok {
		if err := json.Unmarshal(value, &fp.ObservationProof.Observable); err != nil {
			return nil, fmt.Errorf("field observationObservable: %w", err)
		}
	}
	w := subjectEvidenceWire{}
	if w.Symbol, err = str("symbol"); err != nil {
		return nil, err
	}
	if w.ModuleBase, err = str("moduleBase"); err != nil {
		return nil, err
	}
	if w.RuntimeReason, err = str("runtimeReason"); err != nil {
		return nil, err
	}
	if value, ok := fields["runtimeUnverifiable"]; ok {
		if err := json.Unmarshal(value, &w.RuntimeUnverifiable); err != nil {
			return nil, fmt.Errorf("field runtimeUnverifiable: %w", err)
		}
	}
	if fp.ObservationProof.Observable && fp.ObservationProof.Reason != "" {
		// The record form refuses a positive proof carrying a reason on
		// decode; refused here in the row's own words, before an encode
		// that would pass it (the encoder runs the kind ladder alone).
		return nil, fmt.Errorf("observable proof carries a reason")
	}
	if fp != (gofresh.Fingerprint{}) {
		fp.ResultKind = gofresh.CodeResult
		w.Fingerprint = &fp
	}
	return json.Marshal(w)
}

// upgradeLegacyEvidence rewrites every evidence row of a document
// written before the fingerprint record form (versions up to 12) —
// the inline findings' target and oracle rows, or the interned
// evidence table's — so the typed decode reads one shape. The rows are
// spliced in place over the raw bytes: nothing else in a finding or a
// table entry is re-encoded, so a duplicated key elsewhere in the
// object still reaches the inline decoder's refusal.
func upgradeLegacyEvidence(top map[string]json.RawMessage, interned bool) error {
	if interned {
		table, err := spliceArray(top["evidenceTable"], func(i int, entry json.RawMessage) (json.RawMessage, error) {
			out, err := spliceMember(entry, "evidence", upgradeLegacyEvidenceRow)
			if err != nil {
				return nil, fmt.Errorf("gomutant: evidence table entry %d: %w", i, err)
			}
			return out, nil
		})
		if err != nil {
			return fmt.Errorf("gomutant: parse evidence table: %w", err)
		}
		top["evidenceTable"] = table
		return nil
	}
	if isJSONNull(top["findings"]) {
		return nil
	}
	findings, err := spliceArray(top["findings"], func(i int, finding json.RawMessage) (json.RawMessage, error) {
		finding, err := spliceMember(finding, "targetEvidence", upgradeLegacyEvidenceRow)
		if err != nil {
			return nil, fmt.Errorf("gomutant: parse finding %d: targetEvidence: %w", i, err)
		}
		finding, err = spliceMember(finding, "oracleEvidence", func(rows json.RawMessage) (json.RawMessage, error) {
			return spliceArray(rows, func(j int, row json.RawMessage) (json.RawMessage, error) {
				out, err := upgradeLegacyEvidenceRow(row)
				if err != nil {
					return nil, fmt.Errorf("oracleEvidence %d: %w", j, err)
				}
				return out, nil
			})
		})
		if err != nil {
			return nil, fmt.Errorf("gomutant: parse finding %d: %w", i, err)
		}
		return finding, nil
	})
	if err != nil {
		return fmt.Errorf("gomutant: parse findings: %w", err)
	}
	top["findings"] = findings
	return nil
}

// spliceMember replaces, in place, the value of every member named key
// in one JSON object's raw bytes — every occurrence, so a duplicated
// key survives to the decoder that refuses it; a null value is left for
// the same reason — and returns the object's bytes otherwise untouched.
func spliceMember(object json.RawMessage, key string, f func(json.RawMessage) (json.RawMessage, error)) (json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(object))
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return nil, errExpectedObject
	}
	type span struct{ start, end int64 }
	var spans []span
	for dec.More() {
		name, err := dec.Token()
		if err != nil {
			return nil, err
		}
		nameStr, ok := name.(string)
		if !ok {
			return nil, fmt.Errorf("object key is not a string")
		}
		start := dec.InputOffset()
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		if nameStr == key && !isJSONNull(value) {
			// The value's own bytes begin after the colon and any space.
			valueStart := start + int64(bytes.IndexByte(object[start:dec.InputOffset()], value[0]))
			spans = append(spans, span{valueStart, dec.InputOffset()})
		}
	}
	if len(spans) == 0 {
		return object, nil
	}
	out := append([]byte(nil), object...)
	for i := len(spans) - 1; i >= 0; i-- {
		replaced, err := f(json.RawMessage(object[spans[i].start:spans[i].end]))
		if err != nil {
			return nil, err
		}
		out = append(append(append([]byte(nil), out[:spans[i].start]...), replaced...), out[spans[i].end:]...)
	}
	return out, nil
}

// spliceArray replaces every element of one JSON array's raw bytes
// through f, the array's brackets and order kept.
func spliceArray(array json.RawMessage, f func(int, json.RawMessage) (json.RawMessage, error)) (json.RawMessage, error) {
	if isJSONNull(array) {
		return array, nil
	}
	dec := json.NewDecoder(bytes.NewReader(array))
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := token.(json.Delim); !ok || delim != '[' {
		return nil, fmt.Errorf("expected a JSON array")
	}
	var out bytes.Buffer
	out.WriteByte('[')
	for i := 0; dec.More(); i++ {
		var element json.RawMessage
		if err := dec.Decode(&element); err != nil {
			return nil, err
		}
		replaced, err := f(i, element)
		if err != nil {
			return nil, err
		}
		if i > 0 {
			out.WriteByte(',')
		}
		out.Write(replaced)
	}
	out.WriteByte(']')
	return out.Bytes(), nil
}

func validateRequiredObject(raw json.RawMessage, known map[string]bool, required []string) (map[string]json.RawMessage, error) {
	fields, err := decodeKnownObject(raw, known)
	if err != nil {
		return nil, err
	}
	for name, value := range fields {
		if isJSONNull(value) {
			return nil, fmt.Errorf("field %s is null", name)
		}
	}
	for _, name := range required {
		if _, ok := fields[name]; !ok {
			return nil, fmt.Errorf("missing field %s", name)
		}
	}
	return fields, nil
}

// errExpectedObject is decodeKnownObject's refusal of a non-object
// document; callers that admit another top-level shape name it.
var errExpectedObject = errors.New("expected object")

func decodeKnownObject(data []byte, known map[string]bool) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return nil, errExpectedObject
	}
	fields := map[string]json.RawMessage{}
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return nil, err
		}
		name, ok := key.(string)
		if !ok {
			return nil, fmt.Errorf("object key is not a string")
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		if known[name] {
			if _, duplicate := fields[name]; duplicate {
				return nil, fmt.Errorf("duplicate field %s", name)
			}
			fields[name] = value
		}
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("trailing data")
		}
		return nil, err
	}
	return fields, nil
}

func isJSONNull(value json.RawMessage) bool {
	return len(value) == 0 || bytes.Equal(bytes.TrimSpace(value), []byte("null"))
}
