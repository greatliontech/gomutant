package gomutant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/gomutant/internal/contextio"
	"github.com/greatliontech/gomutant/internal/engine"
)

// SubjectEvidence is gomutant's persisted encoding of one Gofresh code-result
// fingerprint plus the completed processes' merged runtime disposition shared
// by the finding; a process that could not prove its log complete is excluded
// here and carried as its candidate's CandidateEvidence instead
// (REQ-result-record). The fingerprint is gofresh's own — its published
// record form under `fingerprint`, every field promoted here — beside
// gomutant's four: the symbol, the module base, and the runtime
// disposition. The type owns its wire form (the embedded record's
// encoders are shadowed, never promoted): a row is the five keys, the
// fingerprint absent only on the zero row a shaped finding carries.
type SubjectEvidence struct {
	Symbol string
	// Fingerprint is the recorded gofresh evidence. Its
	// TestVariantClosure is the subject package's test-variant
	// compartment hash: the gofresh pin that distinguishes "a sibling
	// test moved" from every other drift. The killer-drift gate
	// refreshes a target-package subject's recorded pin to the current
	// one — the refresh its inert ledger diff licenses — and requires
	// the refreshed evidence plainly valid; inspection and attribution
	// surface gofresh's stable "test variants" verdict reason on refusal
	// paths. It is required and never legitimately empty (gofresh
	// defines a non-empty identity even for a package with no test
	// files), so a document lacking it is refused at parse and an
	// in-memory record built without it fails closed to stale. Its
	// DynamicStateVouches and PackageProcessDischarges are audit only —
	// serving derives from the current engine's own vouch set, so a
	// withdrawn vouch resurfaces its culprit without any comparison
	// here, and the attestation-pin view excludes both. Its
	// DynamicStateStrategy is a measured pin (a strategy move — or a
	// record predating the field — re-measures at the evidence check
	// rather than serving verdicts under semantics it was not computed
	// by), never zeroed from the attestation-pin view. Its
	// ClosureStrategy is recorded beside the closure hashes and no pin
	// itself — the hashes are self-describing to the evidence check, and
	// the attestation gate reads them as the pins they are: a
	// derivation change moves them (a shaped disposition sheds, an
	// unshaped one carries with the move named), and a pre-field record
	// (empty) has an unknown derivation, never a moved one
	// (REQ-result-record's subject-evidence term).
	gofresh.Fingerprint
	// ModuleBase is the tree-relative slash base a record's manifest is
	// anchored at when that base is not the tree root: records made
	// since evidence anchored at the tree carry none (their identities
	// are tree-relative and resolve at the store root); a record from
	// the member-anchored era carries its member module, and the store,
	// with no views at write time, resolves that subject's manifest
	// against Join(moduleDir, ModuleBase), as evidenceBase does on the
	// tree side (REQ-result-layers).
	ModuleBase          string
	RuntimeUnverifiable bool
	RuntimeReason       string
}

// subjectEvidenceWire is the row's wire form: gomutant's four fields and
// the fingerprint's own record, the latter absent on the zero row.
type subjectEvidenceWire struct {
	Symbol              string               `json:"symbol"`
	Fingerprint         *gofresh.Fingerprint `json:"fingerprint,omitempty"`
	ModuleBase          string               `json:"moduleBase,omitempty"`
	RuntimeUnverifiable bool                 `json:"runtimeUnverifiable,omitempty"`
	RuntimeReason       string               `json:"runtimeReason,omitempty"`
}

// MarshalJSON encodes the row in its wire form, the fingerprint in
// gofresh's record form (REQ-result-record).
func (e SubjectEvidence) MarshalJSON() ([]byte, error) {
	w := subjectEvidenceWire{Symbol: e.Symbol, ModuleBase: e.ModuleBase, RuntimeUnverifiable: e.RuntimeUnverifiable, RuntimeReason: e.RuntimeReason}
	if e.Fingerprint != (gofresh.Fingerprint{}) {
		fp := e.Fingerprint
		w.Fingerprint = &fp
	}
	return json.Marshal(w)
}

// subjectEvidenceKeys is the row's known key set — the outer shape the
// parser refuses beyond; the fingerprint's own keys are its decoder's.
var subjectEvidenceKeys = map[string]bool{"symbol": true, "fingerprint": true, "moduleBase": true, "runtimeUnverifiable": true, "runtimeReason": true}

// UnmarshalJSON decodes the wire form: a duplicated outer key or a
// null refuses, an unknown outer key is tolerated (REQ-result-tolerant);
// the fingerprint decodes through gofresh's own decoder, which refuses
// every shape its record form does not produce — a key the form does
// not define included, since the record is gofresh's contract and a
// field it grows rides a gofresh release and this document's version;
// an absent fingerprint is the zero row.
func (e *SubjectEvidence) UnmarshalJSON(data []byte) error {
	fields, err := decodeKnownObject(data, subjectEvidenceKeys)
	if err != nil {
		return err
	}
	for name, value := range fields {
		if isJSONNull(value) {
			return fmt.Errorf("field %s is null", name)
		}
	}
	var w subjectEvidenceWire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	decoded := SubjectEvidence{Symbol: w.Symbol, ModuleBase: w.ModuleBase, RuntimeUnverifiable: w.RuntimeUnverifiable, RuntimeReason: w.RuntimeReason}
	if w.Fingerprint != nil {
		decoded.Fingerprint = *w.Fingerprint
	}
	*e = decoded
	return nil
}

func evidenceFromFingerprint(symbol string, fp gofresh.Fingerprint, state runtimeinput.State) SubjectEvidence {
	return SubjectEvidence{Symbol: symbol, Fingerprint: fp, RuntimeUnverifiable: state.Unverifiable, RuntimeReason: state.Reason}
}

// fingerprint is the recorded gofresh evidence as the engine reads it.
func (e SubjectEvidence) fingerprint() gofresh.Fingerprint { return e.Fingerprint }

// CompartmentDeclaration is one entry of the persisted compartment ledger —
// gomutant's wire encoding of gofresh's test-variant declaration record.
type CompartmentDeclaration struct {
	File     string `json:"file"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Receiver string `json:"receiver,omitempty"`
	Hash     string `json:"hash"`
	// Package is the declaring file's package clause name; the killer-drift
	// license resolves a method's receiver type within its own package
	// only, and a recorded entry without one (an older document) leaves
	// method deltas unattributable, fail-closed (REQ-result-stale).
	Package string `json:"package,omitempty"`
}

// CompartmentFileHeader is one compartment file's persisted header identity.
type CompartmentFileHeader struct {
	File     string `json:"file"`
	Hash     string `json:"hash"`
	Embedded bool   `json:"embedded,omitempty"`
}

// CompartmentLedger is the target package's persisted test-variant
// declaration ledger (REQ-result-record): recorded at measure time from the
// same view snapshot the compartment hash pinned, and diffed at serve time
// against the current view's ledger so the killer-drift carve-out can
// classify how the compartment moved (REQ-result-stale).
type CompartmentLedger struct {
	Declarations []CompartmentDeclaration `json:"declarations,omitempty"`
	FileHeaders  []CompartmentFileHeader  `json:"fileHeaders,omitempty"`
}

// compartmentLedgerFromView converts gofresh's ledger to the wire encoding.
func compartmentLedgerFromView(ledger gofresh.TestVariantLedger) *CompartmentLedger {
	out := &CompartmentLedger{
		Declarations: make([]CompartmentDeclaration, 0, len(ledger.Declarations)),
		FileHeaders:  make([]CompartmentFileHeader, 0, len(ledger.FileHeaders)),
	}
	for _, declaration := range ledger.Declarations {
		// Field-by-field: the view's declaration also carries its
		// referenced-name list, which is serve-time input for the
		// killer-drift walk and never persisted — the current view's list
		// speaks for every unchanged declaration: equal hashes pin equal
		// bytes, and the one exception (an omitted-list const spec tracking
		// its governing list) always folds the governing entry's own name,
		// whose movement is the change the walk must observe.
		out.Declarations = append(out.Declarations, CompartmentDeclaration{
			File: declaration.File, Kind: declaration.Kind, Name: declaration.Name,
			Receiver: declaration.Receiver, Hash: declaration.Hash, Package: declaration.Package,
		})
	}
	for _, header := range ledger.FileHeaders {
		out.FileHeaders = append(out.FileHeaders, CompartmentFileHeader(header))
	}
	return out
}

// ledger converts the wire encoding back to gofresh's ledger type.
func (l *CompartmentLedger) ledger() gofresh.TestVariantLedger {
	out := gofresh.TestVariantLedger{
		Declarations: make([]gofresh.TestVariantDeclaration, 0, len(l.Declarations)),
		FileHeaders:  make([]gofresh.TestVariantFileHeader, 0, len(l.FileHeaders)),
	}
	for _, declaration := range l.Declarations {
		out.Declarations = append(out.Declarations, gofresh.TestVariantDeclaration{
			File: declaration.File, Kind: declaration.Kind, Name: declaration.Name,
			Receiver: declaration.Receiver, Hash: declaration.Hash, Package: declaration.Package,
		})
	}
	for _, header := range l.FileHeaders {
		out.FileHeaders = append(out.FileHeaders, gofresh.TestVariantFileHeader(header))
	}
	return out
}

// Survivor is one mutant no oracle test noticed.
type Survivor struct {
	Position string `json:"position"`
	Operator string `json:"operator"`
	// Extent is the mutated node's source range "line:col-line:col"
	// (half-open, in Position's file): the geometry the execution
	// bucket's coverage probe intersects — a point anchor alone sits
	// on toolchain-dependent block boundaries. Empty on records
	// measured before extents existed (the probe falls back to the
	// point). Advisory, never a measurement pin.
	Extent string `json:"extent,omitempty"`
	// Site is the attestation anchor's site component: a hash of the
	// mutated range's line window in the original source, stamped at
	// generation. An attestation's equivalence reasoning is
	// site-specific, so the anchor keys site content beside position
	// and operator - a same-shaped mutant at a different site never
	// inherits a disposition. Empty on records measured before site
	// anchors existed; an attestation anchor only, never a measurement
	// pin (REQ-attest-survivor).
	Site string `json:"site,omitempty"`
	// Execution buckets why the survivor lived (REQ-result-record):
	// "never-executed" - the oracle's baseline coverage never reaches the
	// mutated position, so the survivor is a coverage gap;
	// "executed-and-passed" - the position runs and the oracle still
	// passes, so the survivor is a weak assertion or an equivalent
	// mutant; "covering-passed" - the NARROWED survivor
	// (REQ-exec-oracle-run's narrowed-survivor clause): every covering
	// test ran and passed, and the non-reaching remainder was exempt
	// from execution on sound batch coverage — the same weak-assertion
	// or equivalence reading as executed-and-passed, with the exemption
	// named so the campaign audit can re-score a sample under the full
	// oracle; "overlay-bypassed" - the observed union recorded a read of
	// a mutated file's own on-disk path, so a disk-walking oracle's
	// verdict derived from the unmutated tree and the survivor reading
	// is not evidence the oracle noticed nothing; "unstable-oracle" - the finding's runtime evidence is
	// unverifiable, so execution evidence cannot be trusted;
	// "flipped-kill" - the window run scored a kill and the serial
	// confirmation re-scored the mutant a survivor (the anti-flattering
	// scoring stands; the flip IS the execution evidence - the position
	// demonstrably executes and a test demonstrably can fail on it), so
	// the survivor is oracle nondeterminism to stabilize, never a plain
	// coverage or assertion gap. Empty on
	// records measured before bucketing existed; advisory, never a
	// measurement pin.
	Execution string `json:"execution,omitempty"`
	// WithdrawnKiller names the test whose window-run kill the serial
	// confirmation withdrew - set exactly with the "flipped-kill"
	// bucket (REQ-exec-survivor-evidence): the flip rides the record,
	// not only the event stream, so a false-survivor triage starts from
	// the nondeterministic test by name. Never an equivalence-attestation
	// candidate: a mutant a test has killed is not equivalent.
	WithdrawnKiller string `json:"withdrawnKiller,omitempty"`
}

// SurvivorAdvice maps a survivor's execution bucket to the action it
// prescribes. The vocabulary is the explain surface's contract: the
// bucket says why the mutant lived, the advice says what closes it, and
// both stay advisory — never a verdict (REQ-result-findings).
func SurvivorAdvice(execution string) string {
	switch execution {
	case "never-executed":
		return "no oracle test executes the mutated position - extend a test to reach it"
	case "executed-and-passed":
		return "the position executes and every oracle assertion still passes - sharpen an assertion or attest an equivalence"
	case "covering-passed":
		return "every covering test executes the position and still passes (the non-reaching remainder was exempt on measured coverage) - sharpen an assertion or attest an equivalence"
	case "overlay-bypassed":
		return "the oracle's observed reads include a mutated file's own on-disk path - its verdict came from the unmutated tree, not the built mutant; restructure the test to judge the linked build (a pure core over in-memory inputs) instead of re-reading the tree"
	case "unstable-oracle":
		return "the finding's runtime evidence is unverifiable - stabilize the oracle's runtime inputs before trusting execution evidence"
	case "flipped-kill":
		return "the withdrawn killer is nondeterministic on this mutant (map order, unpinned draws, external state); stabilize that test, never attest equivalence on a mutant a test has killed"
	default:
		return "execution evidence unavailable - the coverage probe was refused or the record predates bucketing; re-measure to bucket this survivor"
	}
}

// Kill is one killed candidate's attribution: the keystone — every reported
// kill rests on an attributed event (REQ-core-attributed-kills) — persisted,
// so reuse can key a kill to its killer's content rather than the whole
// oracle surface (REQ-result-stale's killer-drift carve-out). Killer is the
// killing oracle test's symbol, the timeout marker, or the package-failure
// marker; position and operator identify the candidate under the same
// occurrence discipline as survivors. A record carries either every kill's
// attribution or none (older records): a partial list is refused at parse.
type Kill struct {
	Position string `json:"position"`
	Operator string `json:"operator"`
	Killer   string `json:"killer"`
}

// CandidateEvidence is one candidate's explicit unverifiable runtime
// evidence: the process that measured it could not prove its runtime-input
// log complete, so the incompleteness attaches to this candidate alone while
// every other candidate stays covered by the subject evidence's
// completed-process union (candidate evidence, REQ-result-record). Reuse
// serves the covered candidates and re-executes exactly the flagged ones
// under a passing current baseline probe (REQ-result-stale); Disposition
// records the measured outcome ("killed", "survived", or "discarded") so the
// re-execution splice conserves INV-RESULT-CANDIDATE-CONSERVATION.
type CandidateEvidence struct {
	Position    string `json:"position"`
	Operator    string `json:"operator"`
	Reason      string `json:"reason"`
	Disposition string `json:"disposition"`
}

// Attestation is one survivor disposition carried on the finding: the
// mutant is attested equivalent, with the reasoning (REQ-attest-survivor).
type Attestation struct {
	Position string `json:"position"`
	Operator string `json:"operator"`
	Reason   string `json:"reason"`
	// Site anchors the disposition to the attested survivor's site
	// content, stamped from the survivor at attest time; empty on
	// dispositions recorded before site anchors existed - such a
	// disposition matches by position and operator alone and adopts
	// the matched survivor's site on its next carry
	// (REQ-attest-survivor).
	Site string `json:"site,omitempty"`
}

// OperatorSummary accounts for every selected candidate of one operator.
type OperatorSummary struct {
	Operator  string `json:"operator"`
	Generated int    `json:"generated"`
	Discarded int    `json:"discarded"`
	Killed    int    `json:"killed"`
	Survived  int    `json:"survived"`
}

type survivorKey struct {
	position string
	operator string
}

// Finding is one target's measurement, keyed by the mutated symbol and
// carrying the available evidence for deciding reuse (REQ-result-record).
// Open findings are Survivors less Attested.
type Finding struct {
	Symbol string   `json:"symbol"`
	Labels []string `json:"labels,omitempty"`

	// The pins (REQ-result-stale): any moved pin re-measures the whole
	// target.
	BodyHash    string `json:"bodyHash"`
	OperatorSet string `json:"operatorSet"`
	Budget      int    `json:"budget"`
	// Shape records a shaped target's declared form
	// (REQ-target-structural, REQ-target-manual-recipes): identity and
	// audit, with the shape digest riding BodyHash as the pin. A shaped
	// finding carries no target evidence — its subject is the declared
	// shape, not a resolvable symbol — and no compartment ledger.
	Shape          *TargetShape      `json:"shape,omitempty"`
	TargetEvidence SubjectEvidence   `json:"targetEvidence"`
	OracleEvidence []SubjectEvidence `json:"oracleEvidence"`
	OracleExplicit bool              `json:"oracleExplicit"`
	OracleTimeout  string            `json:"oracleTimeout"`
	// OracleTimeoutDerived marks a record whose oracle bounds were
	// DERIVED from measured baselines: OracleTimeout then records the
	// loosest bound any verdict ran under, and the staleness pin
	// relaxes to the timeout-kill rule — completed verdicts are
	// answers a budget cannot flip, and every "(timeout)" kill rides
	// candidate-local incomplete-observation evidence, so the flagged
	// serve re-executes it under the current derived budget
	// (REQ-result-stale).
	OracleTimeoutDerived bool `json:"oracleTimeoutDerived,omitempty"`
	// OracleMemoryBytes is the effective per-oracle memory ceiling the
	// measurement ran under (REQ-exec-oracle-memory); 0 means no
	// ceiling. A measurement pin exactly like the oracle timeout: a
	// resource bound can change attribution (a mutant near the ceiling
	// dies under a tight one and survives a loose one), so evidence
	// never serves across a moved ceiling. Its addition narrows reuse
	// and rides the version-4 bump (REQ-result-export's precedent).
	OracleMemoryBytes int64 `json:"oracleMemoryBytes,omitempty"`
	// OracleCeilingDecided marks a record with at least one kill whose
	// verdict the oracle memory ceiling decided (a memory-exhaustion
	// signature in the killing run). Such a record pins its exact
	// ceiling; a record without it serves directionally — any current
	// ceiling at least as large as the recorded one preserves every
	// verdict (REQ-result-stale, REQ-exec-oracle-memory). Tolerance:
	// an older reader dropping this field falls back to the exact
	// ceiling compare — strictly more conservative — so the field
	// rides the current document version.
	OracleCeilingDecided bool `json:"oracleCeilingDecided,omitempty"`
	// PropertyRegime records the property-runtime measurement regime the
	// finding's oracle ran under ("" = none; engine.PropertyRegimeRapid =
	// rapid draws pinned): a measurement pin, so a record measured under
	// other draws re-measures instead of serving as reproducible
	// (REQ-exec-property-oracles).
	PropertyRegime string `json:"propertyRegime,omitempty"`
	// CompartmentLedger is the target package's test-variant declaration
	// ledger at measure time; the killer-drift carve-out diffs it against
	// the current tree, and a record persisted without one (an older
	// document) re-measures whole rather than drift-serving (REQ-result-stale).
	CompartmentLedger *CompartmentLedger `json:"compartmentLedger,omitempty"`
	Commit            string             `json:"commit,omitempty"`
	Dirty             bool               `json:"dirty"`
	// Run is the identity of the run that last measured any candidate
	// of the record — a fresh measure, a budget extension, or a serve
	// that re-executed flagged or drifted candidates — never a wholly
	// served record, which keeps the measuring run's identity. Opaque, compared for equality only: an inspection
	// scopes to one campaign's measured set by it. Audit beside the
	// commit provenance, no reuse or attestation pin, so it rides the
	// current document version (REQ-result-record, REQ-result-export).
	Run string `json:"run,omitempty"`
	// StagedTree is the index tree identity a staged run measured
	// (REQ-result-staged) - the tree the eventual commit carries when
	// the staging lands as reviewed; empty for worktree runs.
	// Provenance metadata, never a measurement pin.
	StagedTree string `json:"stagedTree,omitempty"`
	// Exempted stamps the reviewed exemption entries this finding's
	// classification rode (REQ-result-exemptions): audit metadata
	// derived from the committed exemption record at measure or persist
	// time - the record itself stays the live authority on every later
	// classification, so revoking an entry demotes the finding without
	// touching this stamp's history.
	Exempted []Exemption `json:"exempted,omitempty"`

	CandidateCount int               `json:"candidateCount"`
	Generated      int               `json:"generated"`
	Mutants        int               `json:"mutants"`
	Killed         int               `json:"killed"`
	Discarded      int               `json:"discarded"`
	Operators      []OperatorSummary `json:"operators"`
	// Kills attributes every killed candidate when present (complete: one
	// entry per kill), and is absent on records measured before attribution
	// was persisted — those re-measure whole under the killer-drift
	// carve-out rather than serving (REQ-result-stale).
	Kills             []Kill              `json:"kills,omitempty"`
	Survivors         []Survivor          `json:"survivors,omitempty"`
	Attested          []Attestation       `json:"attested,omitempty"`
	CandidateEvidence []CandidateEvidence `json:"candidateEvidence,omitempty"`

	// Run metadata, never persisted: a cached finding was served from the
	// prior document under matching pins; a skipped one names why nothing
	// was measured ("no oracle", "not a function - ..." with the methodology hint).
	Cached  bool   `json:"-"`
	Skipped string `json:"-"`
	// Unreached marks a "no oracle" skip that is the declared
	// selection's coverage bound: the derivation found no test of the
	// selection's build leg reaching the target and no package stood
	// down — nothing to resolve, a leg no oracle covers
	// (REQ-result-unreached-bound). The bound persists at the document
	// level (CoverageBound), never as a record.
	Unreached bool `json:"-"`
}

// CoverageBound is the document's stated coverage bound for one
// declared build selection: the targets that selection's leg discovers
// but no oracle reaches — recorded so a later reader sees the
// population the measurement covered, never a silent zero
// (REQ-result-unreached-bound). The latest WHOLE-TREE run of a
// selection replaces its row, an empty bound clearing it; a scoped run
// or an undeclared selection records none.
type CoverageBound struct {
	// Selection is the declared selection's key (SelectionKey):
	// "tags:a,b", "toolchain:go1.28", or both joined by ";".
	Selection string `json:"selection"`
	// Run is the identity of the run that stated the bound.
	Run string `json:"run,omitempty"`
	// Unreached lists the unreached symbols, sorted.
	Unreached []string `json:"unreached"`
}

// SelectionKey spells a declared selection as the coverage bound's
// key — "tags:a,b", "toolchain:go1.28", or both joined by ";" — the
// tags sorted and deduplicated; empty exactly when nothing is declared
// (Selection.Declared), since an undeclared selection contributes no
// part. A toolchain directive is a leg selector like a tag (a
// release-gated file exists under one toolchain and not another), so
// two toolchains under one tag set are two rows.
func SelectionKey(sel Selection) string {
	var parts []string
	if len(sel.Tags) > 0 {
		tags := slices.Clone(sel.Tags)
		slices.Sort(tags)
		parts = append(parts, "tags:"+strings.Join(slices.Compact(tags), ","))
	}
	if sel.Toolchain != "" {
		parts = append(parts, "toolchain:"+sel.Toolchain)
	}
	return strings.Join(parts, ";")
}

// CoverageBoundOf derives a run's coverage bound from its findings:
// nil under no declared selection; under one, the bound with its
// unreached symbols sorted — EMPTY when the leg reached everything, so
// a whole-tree run's record clears a standing row (the latest run of a
// selection replaces its row, REQ-result-unreached-bound).
func CoverageBoundOf(findings []Finding, sel Selection, runID string) *CoverageBound {
	key := SelectionKey(sel)
	if key == "" {
		return nil
	}
	unreached := []string{}
	for _, f := range findings {
		if f.Unreached {
			unreached = append(unreached, f.Symbol)
		}
	}
	slices.Sort(unreached)
	return &CoverageBound{Selection: key, Run: runID, Unreached: slices.Compact(unreached)}
}

// cloneFinding returns a Finding sharing no mutable state with f, so an
// in-place edit of one copy — an attestation appended into a shared
// backing array, a survivor field rewritten — can never surface through
// the other.
func cloneFinding(f Finding) Finding {
	f.Labels = slices.Clone(f.Labels)
	f.Exempted = slices.Clone(f.Exempted)
	f.OracleEvidence = slices.Clone(f.OracleEvidence)
	f.Operators = slices.Clone(f.Operators)
	f.Kills = slices.Clone(f.Kills)
	f.Survivors = slices.Clone(f.Survivors)
	f.Attested = slices.Clone(f.Attested)
	f.CandidateEvidence = slices.Clone(f.CandidateEvidence)
	if f.CompartmentLedger != nil {
		ledger := *f.CompartmentLedger
		ledger.Declarations = slices.Clone(ledger.Declarations)
		ledger.FileHeaders = slices.Clone(ledger.FileHeaders)
		f.CompartmentLedger = &ledger
	}
	if f.Shape != nil {
		// A field-wise copy, so a shape field added later rides along;
		// only the pointers beneath are re-pointed at copies.
		shape := *f.Shape
		if shape.Structural != nil {
			structural := *shape.Structural
			structural.Packages = slices.Clone(structural.Packages)
			shape.Structural = &structural
		}
		if shape.Manual != nil {
			manual := *shape.Manual
			manual.Edits = slices.Clone(manual.Edits)
			shape.Manual = &manual
		}
		f.Shape = &shape
	}
	return f
}

// Open returns the finding's open survivors — survivors less attested
// dispositions (REQ-attest-survivor, REQ-result-findings).
func (f *Finding) Open() []Survivor {
	attested := map[survivorKey]bool{}
	for _, a := range f.Attested {
		attested[survivorKey{a.Position, a.Operator}] = true
	}
	var open []Survivor
	for _, s := range f.Survivors {
		if !attested[survivorKey{s.Position, s.Operator}] {
			open = append(open, s)
		}
	}
	sort.Slice(open, func(i, j int) bool {
		if open[i].Position != open[j].Position {
			return open[i].Position < open[j].Position
		}
		return open[i].Operator < open[j].Operator
	})
	return open
}

// AttestedDispositions returns a canonical copy of the finding's equivalent-
// mutant dispositions for deterministic views.
func (f *Finding) AttestedDispositions() []Attestation {
	attested := append([]Attestation(nil), f.Attested...)
	sort.Slice(attested, func(i, j int) bool {
		if attested[i].Position != attested[j].Position {
			return attested[i].Position < attested[j].Position
		}
		if attested[i].Operator != attested[j].Operator {
			return attested[i].Operator < attested[j].Operator
		}
		return false
	})
	return attested
}

// Attest records a survivor disposition on the finding, refused unless the
// named mutant is among its current survivors (REQ-attest-survivor).
func (f *Finding) Attest(position, operator, reason string) error {
	if reason == "" {
		return fmt.Errorf("gomutant: attestation needs a reason")
	}
	found := false
	for _, s := range f.Survivors {
		if s.Position == position && s.Operator == operator {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("gomutant: %s has no survivor %s %s", f.Symbol, position, operator)
	}
	for _, a := range f.Attested {
		if a.Position == position && a.Operator == operator {
			return fmt.Errorf("gomutant: survivor %s %s already attested", position, operator)
		}
	}
	site := ""
	for _, s := range f.Survivors {
		if s.Position == position && s.Operator == operator {
			site = s.Site
			break
		}
	}
	f.Attested = append(f.Attested, Attestation{Position: position, Operator: operator, Reason: reason, Site: site})
	return nil
}

// DocumentVersion is the findings document's current version. A bump
// draws a reading boundary: a field that narrows reuse or moves a pin
// (candidate evidence, the compartment pin, the site anchor, the
// property-regime pin, the dynamic-state strategy) or a shape an older
// reader cannot re-derive (positional init targets, shaped targets, the
// interned tables) bumps it, because an older consumer's tolerance would
// serve verdicts computed under semantics its engine does not implement
// or destroy records it cannot resolve; so does a field whose absence an
// older reader would take in the flattering direction (the
// coverage-bounds table: a dropped bound reads as full coverage); an
// audit field, whose absence widens no claim (a discharge list; a
// survivor extent, which falls back to the anchor-point bucket), lands
// without one; version 13 embeds gofresh's published fingerprint record
// in every evidence row (a shape an older reader cannot re-derive, the
// flat rows upgraded on read); version 14 references the tables by
// content key in place of position (a shape an older reader cannot
// resolve, the positional documents of 11-13 read as they were). The
// reading range each boundary draws is ParseDocument's.
const DocumentVersion = 14

// ErrVersionAhead marks a findings document (or overlay entry) written
// by a newer gomutant than this reader: the refusal class a stale
// long-lived process must surface loudly rather than treat as
// corruption (REQ-result-export).
var ErrVersionAhead = errors.New("a newer gomutant likely wrote it - if this reader is a long-lived process (an MCP server), restart it on the upgraded binary")

// ErrVersionBehind marks a findings document (or overlay entry) whose
// version predates the oldest this reader upgrades on read: a
// well-formed record of an older gomutant, never corruption, so a
// reader preserves its bytes - authored attestation reasoning lives
// there - and serves nothing from it (REQ-result-tolerant).
var ErrVersionBehind = errors.New("an older gomutant wrote it - this binary does not read that version")

// RecordFilter selects the records an inspection renders by their
// recorded facts — an opaque label, the mutated symbol, the run that
// last measured the record; each empty field admits every record
// (REQ-result-inspection). The judged state filter is applied after
// inspection, not here.
type RecordFilter struct {
	Label, Symbol, Run string
}

// Admits reports whether f passes every set field of the filter.
func (r RecordFilter) Admits(f Finding) bool {
	if r.Label != "" && !slices.Contains(f.Labels, r.Label) {
		return false
	}
	if r.Symbol != "" && f.Symbol != r.Symbol {
		return false
	}
	if r.Run != "" && f.Run != r.Run {
		return false
	}
	return true
}

// DocumentVersionError is the refusal a document outside the reader's
// version range raises: it carries the version read, and unwraps to
// ErrVersionAhead or ErrVersionBehind so a reader can tell the stale
// binary from the legacy record without re-parsing.
type DocumentVersionError struct {
	Version int
	// Sentinel is ErrVersionAhead or ErrVersionBehind.
	Sentinel error
}

func (e *DocumentVersionError) Error() string {
	return fmt.Sprintf("gomutant: findings document version %d not understood (this binary reads %d-%d): %v", e.Version, OldestReadableDocumentVersion, DocumentVersion, e.Sentinel)
}

func (e *DocumentVersionError) Unwrap() error { return e.Sentinel }

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
	sort.Slice(kept, func(i, j int) bool { return kept[i].Symbol < kept[j].Symbol })
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
		return parseInternedDocument14(data)
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
	return Document{Findings: findings}, nil
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
	"symbol": true, "labels": true, "bodyHash": true, "operatorSet": true,
	"budget": true, "targetEvidence": true, "oracleEvidence": true,
	"oracleExplicit": true, "oracleTimeout": true, "oracleMemoryBytes": true, "propertyRegime": true, "compartmentLedger": true, "commit": true, "dirty": true,
	"candidateCount": true, "generated": true, "mutants": true, "killed": true,
	"discarded": true, "operators": true, "kills": true, "survivors": true, "attested": true,
	"candidateEvidence": true, "oracleCeilingDecided": true,
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
		// a partial list could serve some kills under the killer-drift
		// carve-out while silently dropping others from its accounting.
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
	if raw, ok := fields["survivors"]; ok {
		var records []json.RawMessage
		if err := json.Unmarshal(raw, &records); err != nil {
			return false, fmt.Errorf("survivors: %w", err)
		}
		for i, record := range records {
			if _, err := validateRequiredObject(record, map[string]bool{"position": true, "operator": true, "execution": true, "site": true}, []string{"position", "operator"}); err != nil {
				return false, fmt.Errorf("survivor %d: %w", i, err)
			}
		}
	}
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
	}
	if raw, ok := fields["operators"]; ok {
		var records []json.RawMessage
		if err := json.Unmarshal(raw, &records); err != nil {
			return false, fmt.Errorf("operators: %w", err)
		}
		previous := ""
		remainingGenerated, remainingDiscarded := finding.Generated, finding.Discarded
		remainingKilled, remainingSurvived := finding.Killed, len(finding.Survivors)
		for i, record := range records {
			if _, err := validateRequiredObject(record,
				map[string]bool{"operator": true, "generated": true, "discarded": true, "killed": true, "survived": true},
				[]string{"operator", "generated", "discarded", "killed", "survived"}); err != nil {
				return false, fmt.Errorf("operator summary %d: %w", i, err)
			}
			summary := finding.Operators[i]
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
	if raw, ok := fields["candidateEvidence"]; ok {
		var records []json.RawMessage
		if err := json.Unmarshal(raw, &records); err != nil {
			return false, fmt.Errorf("candidateEvidence: %w", err)
		}
		for i, record := range records {
			if _, err := validateRequiredObject(record,
				map[string]bool{"position": true, "operator": true, "reason": true, "disposition": true},
				[]string{"position", "operator", "reason", "disposition"}); err != nil {
				return false, fmt.Errorf("candidate evidence %d: %w", i, err)
			}
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
	if raw, ok := fields["attested"]; ok {
		var records []json.RawMessage
		if err := json.Unmarshal(raw, &records); err != nil {
			return false, fmt.Errorf("attested: %w", err)
		}
		for i, record := range records {
			if _, err := validateRequiredObject(record, map[string]bool{"position": true, "operator": true, "reason": true, "site": true}, []string{"position", "operator", "reason"}); err != nil {
				return false, fmt.Errorf("attestation %d: %w", i, err)
			}
		}
	}
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

// budgetCovers reports whether a finding's selected candidate prefix covers a
// request (0 = exhaustive) under the same complete candidate set.
func budgetCovers(f Finding, req int) bool {
	needed := f.CandidateCount
	if req > 0 {
		needed = min(req, f.CandidateCount)
	}
	return f.Generated >= needed
}

// Fresh reports whether a prior finding still covers the target at the
// requested budget — the REQ-result-stale pin check as a query, computed
// against the current tree without running anything, under caller-owned
// cancellation. A caller reminding about unhardened or stale-measured
// symbols asks this instead of re-deriving pin arithmetic. It mirrors a
// default run's serve posture: derived oracle budgets, so a derived
// record with no timeout kills reads fresh (completed verdicts are
// budget-independent) while an explicit record reads stale exactly as a
// derive-mode Run would re-measure it; FreshFor is the same query under
// an explicit effective oracle timeout.
func (t *Tree) Fresh(ctx context.Context, f Finding, tg Target, budget int) (bool, error) {
	return t.freshForContext(ctx, f, tg, budget, campaignBaselineLeash, true)
}

// FreshFor is Fresh under an explicit effective oracle timeout.
func (t *Tree) FreshFor(ctx context.Context, f Finding, tg Target, budget int, timeout time.Duration) (bool, error) {
	return t.freshForContext(ctx, f, tg, budget, timeout, false)
}

// standaloneMemoryPin is the ceiling a standalone freshness judgment
// compares a record against: none — inspection runs no oracle, so it
// judges under no ceiling (REQ-result-stale's standalone-inspection
// arm): a directional record serves, a ceiling-decided record's exact
// pin reads as stale until a run's own ceiling judges it — a spurious
// re-measure report, never a spurious serve.
const standaloneMemoryPin int64 = 0

func (t *Tree) freshForContext(ctx context.Context, f Finding, tg Target, budget int, timeout time.Duration, timeoutDerived bool) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if f.Symbol != tg.Symbol {
		return false, fmt.Errorf("gomutant: finding %s checked against target %s", f.Symbol, tg.Symbol)
	}
	oracle, err := t.resolveOracleContext(ctx, tg)
	if err != nil {
		return false, err
	}
	if err := t.eng.ValidateOracleContext(ctx, oracle); err != nil {
		return false, err
	}
	symbols := append([]string{tg.Symbol}, oracle...)
	views, err := t.newSubjectViews(ctx, symbols, packageProcessAttestable(t.PackageOf, tg.Symbol, oracle), 0)
	if err != nil {
		return false, err
	}
	targetView := views.bySymbol[tg.Symbol]
	oracleViews := make([]*subjectView, 0, len(oracle))
	for _, symbol := range oracle {
		oracleViews = append(oracleViews, views.bySymbol[symbol])
	}
	if !budgetCovers(f, budget) {
		return false, nil
	}
	// The advisory boundary reads the installed ceiling once here - the
	// comparison gates themselves take the pin explicitly. A library
	// consumer that never installed a ceiling compares 0 against
	// derived-pinned records and reads stale: the conservative
	// direction; install or derive a ceiling first for parity with Run.
	// The property regime the run would use derives from the oracle's
	// own packages, exactly as Run derives it - a regimeless rapid
	// record reads stale here too (REQ-exec-property-oracles).
	oraclePkgs := make([]string, 0, len(oracle))
	seenPkg := map[string]bool{}
	for _, run := range pkgRuns(oracle) {
		if !seenPkg[run.pkg] {
			seenPkg[run.pkg] = true
			oraclePkgs = append(oraclePkgs, run.pkg)
		}
	}
	rapidPkgs, _, err := t.eng.SplitRapidPkgsContext(ctx, oraclePkgs)
	if err != nil {
		return false, err
	}
	regime := ""
	if len(rapidPkgs) > 0 {
		regime = engine.PropertyRegimeRapid
	}
	// The pin comparison takes the REQUEST's posture, never the
	// record's: an explicit caller timeout invalidates a derived
	// record exactly as an explicit Run would re-measure it, and the
	// derive-posture default relaxes to the timeout-kill rule.
	matches, err := evidenceSetMatchesContext(ctx, f, targetView, oracleViews, tg.OracleExplicit || len(tg.Oracle) != 0, engine.OperatorSet, timeout.String(), timeoutDerived, standaloneMemoryPin, regime)
	if err != nil || !matches {
		return matches, err
	}
	// A record carrying candidate evidence serves only by re-executing its
	// flagged candidates under a passing baseline probe (REQ-result-stale),
	// so it does not cover the target without measurement.
	return len(f.CandidateEvidence) == 0, nil
}

// RenderedFindings substitutes each run finding with its post-merge
// document row where one landed, preserving the run-only Cached and
// Skipped markers: what a run surface renders is what the document
// holds (REQ-mcp-findings-doc).
func RenderedFindings(findings []Finding, postMerge map[string]Finding) []Finding {
	rendered := make([]Finding, len(findings))
	for i, f := range findings {
		if m, ok := postMerge[f.Symbol]; ok {
			m.Cached, m.Skipped, m.Unreached = f.Cached, f.Skipped, f.Unreached
			rendered[i] = m
		} else {
			rendered[i] = f
		}
	}
	return rendered
}

// DedupeAttestationSheds keeps each shed mutant's first report: the
// in-run carry sheds with the specific cause (a moved site) before the
// merge layer re-derives the same disposition's fate from the snapshot,
// and one disposition owes the reader one line (REQ-attest-survivor).
func DedupeAttestationSheds(sheds []AttestationShed) []AttestationShed {
	seen := make(map[string]bool, len(sheds))
	out := sheds[:0:0]
	for _, d := range sheds {
		key := mutantKey(d.Symbol, d.Position, d.Operator)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, d)
	}
	return out
}

// MergeFindings merges a run's fresh findings into the prior document's
// records — a fresh record replaces the prior for its symbol, a prior
// record with no fresh counterpart stands — grafting attested
// dispositions against snapshot, the caller's run-start snapshot of
// attested dispositions per symbol, and reporting the ones it shed.
// With the snapshot in hand the graft is pin-correct
// (REQ-attest-survivor): a disposition the fresh record already carries
// rode the domain-hold re-measure carry and stays; a disposition
// present in the live document but absent from the snapshot is a
// concurrent attestation and grafts onto a still-reported survivor;
// every other prior disposition was judged afresh and rejected — its
// mutation domain moved — and sheds loudly. Without a snapshot the
// graft anchors on survivor identity and site alone.
func MergeFindings(prior, fresh []Finding, snapshot map[string][]Attestation) ([]Finding, []AttestationShed) {
	var shed []AttestationShed
	bySym := map[string]Finding{}
	for _, f := range prior {
		bySym[f.Symbol] = f
	}
	for _, f := range fresh {
		// A skipped result measured nothing: it must never shadow a symbol's
		// real record — Export's exclusion rule serializes nothing-measured,
		// the merge's rule is that nothing-measured never overwrites
		// something-measured.
		if f.Skipped != "" {
			continue
		}
		// Replacement never sheds a disposition the document already holds for
		// a survivor the replacement still reports: an attestation added
		// between a run's snapshot — or its incremental commit — and this
		// merge rides survivor identity, so it grafts onto the fresh record
		// rather than being clobbered by it.
		if prior, ok := bySym[f.Symbol]; ok {
			var dropped []AttestationShed
			snap, snapped := (map[survivorKey]Attestation)(nil), false
			if snapshot != nil {
				snapped = true
				snap = map[survivorKey]Attestation{}
				for _, a := range snapshot[f.Symbol] {
					snap[survivorKey{a.Position, a.Operator}] = a
				}
			}
			f.Attested, dropped = graftAttestationsAgainst(prior.Attested, f.Attested, f.Survivors, snap, snapped)
			for _, d := range dropped {
				d.Symbol = f.Symbol
				shed = append(shed, d)
			}
		}
		bySym[f.Symbol] = f
	}
	out := make([]Finding, 0, len(bySym))
	for _, f := range bySym {
		out = append(out, f)
	}
	return out, shed
}

// AttestationShed is one disposition that failed to carry only because
// the site content under its position changed - the would-have-cross-
// anchored event the site anchor exists to refuse, surfaced so the
// re-anchor rate is visible instead of silent (REQ-attest-survivor).
type AttestationShed struct {
	Symbol   string
	Position string
	Operator string
	Reason   string
}

// Text is the shed's one rendering on every face: the mutant, then its
// reason.
func (d AttestationShed) Text() string {
	return d.Symbol + " " + d.Position + " " + d.Operator + " - " + d.Reason
}

// graftAttestations returns fresh's attestations plus every prior attestation
// whose survivor anchor the fresh record still reports and fresh does not
// already attest. The anchor is position, operator, and site content
// (REQ-attest-survivor): a same-shaped mutant at a different site - a
// neighbor shifted into the old coordinates by an edit - never inherits a
// disposition; a position+operator match whose site moved is returned as a
// shed so the refusal is visible. A pre-site disposition (empty site)
// matches by position and operator and adopts the matched survivor's site.
func graftAttestations(prior, fresh []Attestation, survivors []Survivor) ([]Attestation, []AttestationShed) {
	return graftAttestationsAgainst(prior, fresh, survivors, nil, false)
}

// graftAttestationsAgainst is graftAttestations under the caller's
// run-start snapshot: when snapped, a prior disposition the snapshot
// already held and the fresh record does not carry was judged afresh
// by the domain-hold carry and rejected - its mutation domain moved,
// so it sheds instead of grafting (REQ-attest-survivor's
// shed-on-domain-move); only concurrent attestations (absent from the
// snapshot) graft. A disposition whose survivor the fresh record no
// longer reports sheds loudly in every mode - never silently dropped.
func graftAttestationsAgainst(prior, fresh []Attestation, survivors []Survivor, snapshot map[survivorKey]Attestation, snapped bool) ([]Attestation, []AttestationShed) {
	siteOf := make(map[survivorKey]string, len(survivors))
	surviving := make(map[survivorKey]bool, len(survivors))
	for _, survivor := range survivors {
		key := survivorKey{survivor.Position, survivor.Operator}
		surviving[key] = true
		siteOf[key] = survivor.Site
	}
	attested := make(map[survivorKey]bool, len(fresh))
	for _, attestation := range fresh {
		attested[survivorKey{attestation.Position, attestation.Operator}] = true
	}
	out := append([]Attestation(nil), fresh...)
	var shed []AttestationShed
	for _, attestation := range prior {
		key := survivorKey{attestation.Position, attestation.Operator}
		if attested[key] {
			continue
		}
		if !surviving[key] {
			shed = append(shed, AttestationShed{
				Position: attestation.Position,
				Operator: attestation.Operator,
				Reason:   "the attested survivor is no longer reported - the disposition sheds with its mutant",
			})
			continue
		}
		if attestation.Site != "" && attestation.Site != siteOf[key] {
			// The specific cause outranks the general one: a moved site
			// names the actionable fact even when the pins also moved.
			shed = append(shed, AttestationShed{
				Position: attestation.Position,
				Operator: attestation.Operator,
				Reason:   "site content changed under the position - the surviving mutant is not the attested one",
			})
			continue
		}
		if snapped {
			// The run started with this exact disposition on record and
			// the domain-hold carry did not keep it: its mutation domain
			// moved and the equivalence is judged afresh
			// (REQ-attest-survivor). A same-mutant disposition whose
			// content differs from the snapshot entry was recorded
			// concurrently - a re-judgment against the current record -
			// and grafts instead.
			if snapAtt, held := snapshot[key]; held && snapAtt == attestation {
				shed = append(shed, AttestationShed{
					Position: attestation.Position,
					Operator: attestation.Operator,
					Reason:   "the mutation domain moved or cannot be shown held - the equivalence is judged afresh",
				})
				continue
			}
		}
		attestation.Site = siteOf[key]
		out = append(out, attestation)
	}
	return out, shed
}

// MergeWholeFindings is MergeFindings for a whole-tree run: a prior
// record whose symbol the run did not discover is dropped — the symbol
// is gone from the tree (REQ-result-hygiene) — while a discovered or
// shaped symbol's record stands, re-measured or not; the shed reports
// against snapshot exactly as MergeFindings does.
func MergeWholeFindings(prior, fresh []Finding, discovered []Target, snapshot map[string][]Attestation) ([]Finding, []AttestationShed) {
	current := make(map[string]bool, len(discovered))
	for _, target := range discovered {
		current[target.Symbol] = true
	}
	merged, shed := MergeFindings(prior, fresh, snapshot)
	kept := merged[:0]
	for _, finding := range merged {
		// A shaped finding's identity is never discovered: absence from
		// the whole-tree target set is its normal state, so the shed
		// spares it exactly as lifecycle pruning does — retirement is
		// the caller's explicit edit (REQ-target-structural,
		// REQ-target-manual-recipes).
		if current[finding.Symbol] || finding.Shape != nil {
			kept = append(kept, finding)
		}
	}
	return kept, shed
}

// UpdateDocument applies update to the findings document at path under the
// exclusive document lock, re-reading the document inside the lock so a
// concurrent session's dispositions are never clobbered by a stale snapshot
// (REQ-mcp-findings-doc): load-then-long-run-then-write is the caller's
// shape, but the merge always runs against the freshest document. A missing
// document reads as empty; a lock held elsewhere is waited on briefly and
// then refused with the holder named (REQ-exec-exclusivity's liveness
// discipline: a crashed holder never leaves a stale block on the supported
// platform). The context bounds the wait and the update.
func UpdateDocument(ctx context.Context, path string, update func(prior []Finding) ([]Finding, error)) error {
	return updateDocument(ctx, path, documentUpdate{update: update})
}

// documentUpdate is one atomic replacement of a findings document with
// the store's seams: parse reads the prior document's bytes (nil is
// ParseFindings), export encodes the successor (nil is Export's writer
// carrying the prior document's coverage bounds — read from the prior
// bytes themselves, so any parse keeps the table — with the
// whole-document self-check; a nil document with a nil error means
// nothing to write — the file stands as it is), and after runs with the
// bytes written once the replacement is visible (nil when nothing was
// written), still under the document lock — the store's overlay writes
// follow the repo write there, so nothing between the two is observable
// to another session.
type documentUpdate struct {
	parse  func(data []byte) ([]Finding, error)
	update func(prior []Finding) ([]Finding, error)
	export func(next []Finding) ([]byte, error)
	after  func(written []byte) error
}

// updateDocument is UpdateDocument over a documentUpdate.
func updateDocument(ctx context.Context, path string, u documentUpdate) error {
	parse, export := u.parse, u.export
	if parse == nil {
		parse = ParseFindings
	}
	// The default writer carries the prior document's coverage bounds
	// whole, read from the prior bytes themselves rather than inherited
	// from whichever parse ran: a caller editing records through
	// UpdateDocument never drops the table, the flattering direction
	// (REQ-result-unreached-bound). An absent prior document has none.
	var priorData []byte
	if export == nil {
		export = func(findings []Finding) ([]byte, error) {
			var bounds []CoverageBound
			if priorData != nil {
				doc, err := ParseDocument(priorData)
				if err != nil {
					return nil, err
				}
				bounds = doc.CoverageBounds
			}
			data, _, _, err := writeDocument(findings, bounds)
			if err != nil {
				return nil, err
			}
			if _, err := ParseFindings(data); err != nil {
				return nil, fmt.Errorf("gomutant: export produced an unreadable document: %w", err)
			}
			return data, nil
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	release, err := acquireDocumentLock(ctx, path)
	if err != nil {
		return err
	}
	defer release()

	var prior []Finding
	mode := os.FileMode(0o644)
	data, err := contextio.ReadFile(ctx, path)
	switch {
	case os.IsNotExist(err):
	case err != nil:
		return err
	default:
		if info, statErr := os.Stat(path); statErr != nil {
			return statErr
		} else {
			mode = info.Mode() & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
		}
		priorData = data
		if prior, err = parse(data); err != nil {
			return err
		}
	}
	next, err := u.update(prior)
	if err != nil {
		return err
	}
	doc, err := export(next)
	if err != nil {
		return err
	}
	if doc == nil {
		// Nothing to write: a record verb whose edits touched no repo
		// row leaves the document exactly as it stands — a re-emission
		// would be a change it never reported (REQ-result-lifecycle).
		if u.after != nil {
			return u.after(nil)
		}
		return nil
	}
	writeTemp := func(contents []byte, mode os.FileMode) (string, error) {
		tmp, err := os.CreateTemp(filepath.Dir(path), ".gomutant-findings-*")
		if err != nil {
			return "", err
		}
		tmpPath := tmp.Name()
		for len(contents) > 0 {
			if err := ctx.Err(); err != nil {
				tmp.Close()
				os.Remove(tmpPath)
				return "", err
			}
			chunk := min(len(contents), 32*1024)
			n, err := tmp.Write(contents[:chunk])
			if err != nil {
				tmp.Close()
				os.Remove(tmpPath)
				return "", err
			}
			if n == 0 {
				tmp.Close()
				os.Remove(tmpPath)
				return "", io.ErrShortWrite
			}
			contents = contents[n:]
		}
		if err := tmp.Chmod(mode); err != nil {
			tmp.Close()
			os.Remove(tmpPath)
			return "", err
		}
		if err := tmp.Close(); err != nil {
			os.Remove(tmpPath)
			return "", err
		}
		return tmpPath, nil
	}
	written := append(doc, '\n')
	tmpPath, err := writeTemp(written, mode)
	if err != nil {
		return err
	}
	defer os.Remove(tmpPath)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	if u.after != nil {
		return u.after(written)
	}
	return nil
}

// PackageSkip is one package's skip blast radius in a run's findings:
// Dark reports whether every target the selection carried for the
// package skipped — "package X: all N targets skipped" reads very
// differently from the same N scattered through a count, and the dark
// case is the one that turns a tool gap into a coverage hole
// (REQ-result-skip-radius).
type PackageSkip struct {
	Package string
	Skipped int
	Targets int
}

// Dark reports whether the package's whole target set skipped.
func (p PackageSkip) Dark() bool { return p.Skipped == p.Targets }

// SkippedPackageRadius groups a run's skips by package, package path
// ascending, packages with no skips omitted. packageOf names each
// finding's package: a loaded tree's PackageOf, exact for a dotted
// last path element and a Type.Method spelling alike; nil where no
// tree is loaded, which groups by the string cut's guess.
func SkippedPackageRadius(findings []Finding, packageOf func(string) string) []PackageSkip {
	if packageOf == nil {
		packageOf = symbolPackage
	}
	byPkg := map[string]*PackageSkip{}
	for _, f := range findings {
		pkg := packageOf(f.Symbol)
		entry := byPkg[pkg]
		if entry == nil {
			entry = &PackageSkip{Package: pkg}
			byPkg[pkg] = entry
		}
		entry.Targets++
		if f.Skipped != "" {
			entry.Skipped++
		}
	}
	radius := make([]PackageSkip, 0, len(byPkg))
	for _, entry := range byPkg {
		if entry.Skipped > 0 {
			radius = append(radius, *entry)
		}
	}
	sort.Slice(radius, func(i, j int) bool { return radius[i].Package < radius[j].Package })
	return radius
}

// AttestFinding records one equivalence disposition on the named
// symbol's finding in a document's rows — the one disposition both
// faces' update callbacks apply, and the one home of the reasoning
// rule (a blank reasoning refuses, whitespace included, as the
// ephemeral attestation's does) — returning the rows and the record
// as attested; a symbol with no finding refuses (REQ-attest-survivor).
func AttestFinding(all []Finding, symbol, position, operator, reason string) ([]Finding, Finding, error) {
	if err := ValidateAttestationReason(reason); err != nil {
		return nil, Finding{}, err
	}
	for i := range all {
		if all[i].Symbol == symbol {
			if err := all[i].Attest(position, operator, reason); err != nil {
				return nil, Finding{}, err
			}
			return all, all[i], nil
		}
	}
	return nil, Finding{}, fmt.Errorf("no finding for %s", symbol)
}
