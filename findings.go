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
	"github.com/greatliontech/gofresh/guard"
	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/gomutant/internal/contextio"
	"github.com/greatliontech/gomutant/internal/engine"
)

// SubjectEvidence is gomutant's persisted encoding of one Gofresh code-result
// fingerprint plus the completed processes' merged runtime disposition shared
// by the finding; a process that could not prove its log complete is excluded
// here and carried as its candidate's CandidateEvidence instead
// (REQ-result-record).
type SubjectEvidence struct {
	Symbol         string `json:"symbol"`
	MaximalClosure string `json:"maximalClosure"`
	// TestVariantClosure is the subject package's test-variant compartment
	// hash: the gofresh pin that distinguishes "a sibling test moved" from
	// every other drift. The oracle-growth gate refreshes a target-package
	// subject's recorded pin to the current one — the refresh its inert
	// ledger diff licenses — and requires the refreshed evidence plainly
	// valid; inspection and attribution surface gofresh's stable "test
	// variants" verdict reason on refusal paths. It is required and never
	// legitimately empty (gofresh defines a non-empty identity even for a
	// package with no test files), so a document lacking it is refused at
	// parse and an in-memory record built without it fails closed to stale.
	TestVariantClosure        string `json:"testVariantClosure"`
	Toolchain                 string `json:"toolchain"`
	BuildConfig               string `json:"buildConfig"`
	ObservationAssertion      string `json:"observationAssertion"`
	ObservationStrategy       string `json:"observationStrategy"`
	ObservationSubjectPackage string `json:"observationSubjectPackage"`
	ObservationSubjectSymbol  string `json:"observationSubjectSymbol"`
	ObservationObservable     bool   `json:"observationObservable"`
	ObservationReason         string `json:"observationReason,omitempty"`
	ObservationEvidence       string `json:"observationEvidence"`
	PurityAssertion           string `json:"purityAssertion,omitempty"`
	// DynamicStateVouches names the caller vouches that discharged
	// shared-dynamic-state culprits reachable from this subject at
	// capture (gofresh's sorted comma-joined identities): the
	// acceptance is auditable in the record, never silent. Audit only —
	// serving derives from the current engine's own vouch set, so a
	// withdrawn vouch resurfaces its culprit without any comparison
	// here, and the field rides the current document version (an old
	// reader dropping it changes no verdict).
	DynamicStateVouches string `json:"dynamicStateVouches,omitempty"`
	// ModuleBase is the subject module's tree-relative slash base ("" =
	// the tree root): manifest identities are module-relative, and the
	// store has no views at write time, so committability resolves each
	// subject's manifest against Join(storeDir, ModuleBase). Absent on
	// records predating the field - those resolve at the store root,
	// the prior behavior (REQ-result-layers).
	ModuleBase          string `json:"moduleBase,omitempty"`
	RuntimeInputs       string `json:"runtimeInputs"`
	RuntimeDigest       string `json:"runtimeDigest"`
	RuntimeUnverifiable bool   `json:"runtimeUnverifiable,omitempty"`
	RuntimeReason       string `json:"runtimeReason,omitempty"`
}

func evidenceFromFingerprint(symbol string, fp gofresh.Fingerprint, state runtimeinput.State) SubjectEvidence {
	return SubjectEvidence{
		Symbol:                    symbol,
		MaximalClosure:            fp.MaximalClosure,
		TestVariantClosure:        fp.TestVariantClosure,
		Toolchain:                 fp.Guards.Toolchain,
		BuildConfig:               fp.Guards.BuildConfig,
		ObservationAssertion:      fp.ObservationAssertion,
		ObservationStrategy:       fp.ObservationProof.Strategy,
		ObservationSubjectPackage: fp.ObservationProof.Subject.Package,
		ObservationSubjectSymbol:  fp.ObservationProof.Subject.Symbol,
		ObservationObservable:     fp.ObservationProof.Observable,
		ObservationReason:         fp.ObservationProof.Reason,
		ObservationEvidence:       fp.ObservationProof.Evidence,
		PurityAssertion:           fp.PurityAssertion,
		DynamicStateVouches:       fp.DynamicStateVouches,
		RuntimeInputs:             fp.RuntimeInputs,
		RuntimeDigest:             fp.RuntimeDigest,
		RuntimeUnverifiable:       state.Unverifiable,
		RuntimeReason:             state.Reason,
	}
}

func (e SubjectEvidence) fingerprint() gofresh.Fingerprint {
	return gofresh.Fingerprint{
		MaximalClosure:       e.MaximalClosure,
		TestVariantClosure:   e.TestVariantClosure,
		Guards:               guard.Guards{Toolchain: e.Toolchain, BuildConfig: e.BuildConfig},
		PurityAssertion:      e.PurityAssertion,
		DynamicStateVouches:  e.DynamicStateVouches,
		ObservationAssertion: e.ObservationAssertion,
		ObservationProof: gofresh.ObservationProof{
			Strategy:   e.ObservationStrategy,
			Subject:    gofresh.Subject{Package: e.ObservationSubjectPackage, Symbol: e.ObservationSubjectSymbol},
			Observable: e.ObservationObservable, Reason: e.ObservationReason, Evidence: e.ObservationEvidence,
		},
		RuntimeInputs: e.RuntimeInputs,
		RuntimeDigest: e.RuntimeDigest,
		ResultKind:    gofresh.CodeResult,
	}
}

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
// against the current view's ledger so the oracle-growth carve-out can
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
	// mutant; "overlay-bypassed" - the observed union recorded a read of
	// a mutated file's own on-disk path, so a disk-walking oracle's
	// verdict derived from the unmutated tree and the survivor reading
	// is not evidence the oracle noticed nothing; "unstable-oracle" - the finding's runtime evidence is
	// unverifiable, so execution evidence cannot be trusted. Empty on
	// records measured before bucketing existed; advisory, never a
	// measurement pin.
	Execution string `json:"execution,omitempty"`
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
	case "overlay-bypassed":
		return "the oracle's observed reads include a mutated file's own on-disk path - its verdict came from the unmutated tree, not the built mutant; restructure the test to judge the linked build (a pure core over in-memory inputs) instead of re-reading the tree"
	case "unstable-oracle":
		return "the finding's runtime evidence is unverifiable - stabilize the oracle's runtime inputs before trusting execution evidence"
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
	BodyHash       string            `json:"bodyHash"`
	OperatorSet    string            `json:"operatorSet"`
	Budget         int               `json:"budget"`
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
	// OracleMemoryBytes is the effective per-oracle memory ceiling the
	// measurement ran under (REQ-exec-oracle-memory); 0 means no
	// ceiling. A measurement pin exactly like the oracle timeout: a
	// resource bound can change attribution (a mutant near the ceiling
	// dies under a tight one and survives a loose one), so evidence
	// never serves across a moved ceiling. Its addition narrows reuse
	// and rides the version-4 bump (REQ-result-export's precedent).
	OracleMemoryBytes int64 `json:"oracleMemoryBytes,omitempty"`
	// PropertyRegime records the property-runtime measurement regime the
	// finding's oracle ran under ("" = none; engine.PropertyRegimeRapid =
	// rapid draws pinned): a measurement pin, so a record measured under
	// other draws re-measures instead of serving as reproducible
	// (REQ-exec-property-oracles).
	PropertyRegime string `json:"propertyRegime,omitempty"`
	// CompartmentLedger is the target package's test-variant declaration
	// ledger at measure time; the oracle-growth carve-out diffs it against
	// the current tree, and a record persisted without one (an older
	// document) re-measures whole rather than growing (REQ-result-stale).
	CompartmentLedger *CompartmentLedger `json:"compartmentLedger,omitempty"`
	Commit            string             `json:"commit,omitempty"`
	Dirty             bool               `json:"dirty"`
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
}

// cloneFinding returns a Finding sharing no mutable state with f, so an
// in-place edit of one copy — an attestation appended into a shared
// backing array, a survivor field rewritten — can never surface through
// the other.
func cloneFinding(f Finding) Finding {
	f.Labels = slices.Clone(f.Labels)
	f.OracleEvidence = slices.Clone(f.OracleEvidence)
	f.Operators = slices.Clone(f.Operators)
	f.Kills = slices.Clone(f.Kills)
	f.Survivors = slices.Clone(f.Survivors)
	f.Attested = slices.Clone(f.Attested)
	f.CandidateEvidence = slices.Clone(f.CandidateEvidence)
	if f.CompartmentLedger != nil {
		f.CompartmentLedger = &CompartmentLedger{
			Declarations: slices.Clone(f.CompartmentLedger.Declarations),
			FileHeaders:  slices.Clone(f.CompartmentLedger.FileHeaders),
		}
	}
	if f.Shape != nil {
		shape := TargetShape{}
		if f.Shape.Structural != nil {
			structural := *f.Shape.Structural
			structural.Packages = slices.Clone(structural.Packages)
			shape.Structural = &structural
		}
		if f.Shape.Manual != nil {
			manual := *f.Shape.Manual
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

// DocumentVersion tags the finding document format; a consumer rejects a
// version it does not understand (REQ-result-export), while unknown fields
// within an understood version are discarded (REQ-result-tolerant). Version 2
// introduced candidate evidence: the field narrows reuse, so a version-1
// consumer's field tolerance would have served flagged kills with the
// evidence silently dropped — exactly what the version boundary exists to
// refuse. Version 3 introduced the test-variant compartment pin on every
// subject's evidence: the field is what stales a record across sibling-test
// movement, so an older consumer's tolerance would have dropped the pin and
// served results whose oracle set silently changed. Version 5 introduced
// the survivor site anchor: attestation inheritance narrowed to matching
// site content, so an older consumer's tolerance would have dropped the
// anchor and re-opened cross-site inheritance. Version 4 documents are
// still readable - their empty sites are the grandfathered
// match-by-position form that adopts sites on first carry - while an
// unknown version still refuses.
// Version 6 introduced the
// property-regime measurement pin: the field is what stales a
// rapid-oracle record measured under unpinned draws, so an older
// consumer's tolerance would have dropped it and served verdicts from
// draw sequences the pinned regime never executes.
// Version 8 introduced positional init targets
// (<pkg>.init#<file>#<ordinal>): their symbols resolve in no earlier
// release, so an older consumer's routine prune would classify every
// init finding detached and destroy the records - the version boundary
// refuses the destruction instead.
// Version 9 introduced shaped targets (structural classes and manual
// recipes): their identities resolve to no symbol, so an older
// consumer's routine prune would classify every shaped finding
// detached and destroy the records - the same destruction class the
// version-8 boundary refuses - and its serving would compare a zero
// target-evidence row as if it were measured symbol evidence.
const DocumentVersion = 9

// ErrVersionAhead marks a findings document (or overlay entry) written
// by a newer gomutant than this reader: the refusal class a stale
// long-lived process must surface loudly rather than treat as
// corruption (REQ-result-export).
var ErrVersionAhead = errors.New("a newer gomutant likely wrote it - if this reader is a long-lived process (an MCP server), restart it on the upgraded binary")

// OldestReadableDocumentVersion bounds the known older document versions the
// parser upgrades on read (REQ-result-tolerant).
const OldestReadableDocumentVersion = 4

// document is the portable finding set (REQ-result-export).
type document struct {
	Version  int       `json:"version"`
	Findings []Finding `json:"findings"`
}

// Export serializes findings to the versioned document gomutant owns
// (REQ-result-export), skipped results excluded (nothing was measured),
// deterministically ordered by symbol.
func Export(findings []Finding) ([]byte, error) {
	kept := make([]Finding, 0, len(findings))
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
		kept = append(kept, f)
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].Symbol < kept[j].Symbol })
	data, err := json.MarshalIndent(document{Version: DocumentVersion, Findings: kept}, "", "  ")
	if err != nil {
		return nil, err
	}
	if _, err := ParseFindings(data); err != nil {
		return nil, fmt.Errorf("gomutant: export invalid findings: %w", err)
	}
	return data, nil
}

// ParseFindings loads a finding document: an unknown version is refused
// (REQ-result-export), an unknown field within a known version is discarded
// (REQ-result-tolerant — encoding/json drops unknown fields).
func ParseFindings(data []byte) ([]Finding, error) {
	top, err := decodeKnownObject(data, map[string]bool{"version": true, "findings": true})
	if err != nil {
		return nil, fmt.Errorf("gomutant: parse findings document: %w", err)
	}
	var version int
	if err := json.Unmarshal(top["version"], &version); err != nil {
		return nil, fmt.Errorf("gomutant: parse findings version: %w", err)
	}
	if version > DocumentVersion {
		// A version AHEAD of this reader is nearly always "a newer
		// gomutant wrote this" - the recurring field shape is a
		// long-lived MCP server outliving a binary upgrade at the same
		// path, its surface dead until someone realizes the process
		// itself is stale. Name the probable cause and the signal, so
		// the reader is not sent hunting for document corruption.
		return nil, fmt.Errorf("gomutant: findings document version %d not understood (this binary reads %d-%d): %w", version, OldestReadableDocumentVersion, DocumentVersion, ErrVersionAhead)
	}
	if version < OldestReadableDocumentVersion {
		return nil, fmt.Errorf("gomutant: findings document version %d not understood (want %d-%d)", version, OldestReadableDocumentVersion, DocumentVersion)
	}
	if isJSONNull(top["findings"]) {
		return nil, fmt.Errorf("gomutant: findings must be an array")
	}
	var rawFindings []json.RawMessage
	if err := json.Unmarshal(top["findings"], &rawFindings); err != nil {
		return nil, fmt.Errorf("gomutant: parse findings: %w", err)
	}
	known := map[string]bool{
		"symbol": true, "labels": true, "bodyHash": true, "operatorSet": true,
		"budget": true, "targetEvidence": true, "oracleEvidence": true,
		"oracleExplicit": true, "oracleTimeout": true, "oracleMemoryBytes": true, "propertyRegime": true, "compartmentLedger": true, "commit": true, "dirty": true,
		"candidateCount": true, "generated": true, "mutants": true, "killed": true,
		"discarded": true, "operators": true, "kills": true, "survivors": true, "attested": true,
		"candidateEvidence": true,
	}
	required := []string{"symbol", "bodyHash", "operatorSet", "budget", "targetEvidence", "oracleEvidence", "oracleExplicit", "oracleTimeout", "dirty", "candidateCount", "generated", "mutants", "killed", "discarded", "operators"}
	findings := make([]Finding, len(rawFindings))
	symbols := map[string]bool{}
	for i, raw := range rawFindings {
		fields, err := decodeKnownObject(raw, known)
		if err != nil {
			return nil, fmt.Errorf("gomutant: parse finding %d: %w", i, err)
		}
		complete := true
		for _, name := range required {
			value, ok := fields[name]
			if !ok {
				complete = false
			} else if isJSONNull(value) {
				return nil, fmt.Errorf("gomutant: finding %d field %s is null", i, name)
			}
		}
		if value, ok := fields["dirty"]; ok && isJSONNull(value) {
			return nil, fmt.Errorf("gomutant: finding %d field dirty is null", i)
		}
		if err := json.Unmarshal(raw, &findings[i]); err != nil {
			return nil, fmt.Errorf("gomutant: parse finding %d: %w", i, err)
		}
		if findings[i].Symbol == "" || findings[i].BodyHash == "" || findings[i].OperatorSet == "" || findings[i].OracleTimeout == "" {
			complete = false
		} else if duration, err := time.ParseDuration(findings[i].OracleTimeout); err != nil || duration <= 0 || duration.String() != findings[i].OracleTimeout {
			complete = false
		}
		if symbols[findings[i].Symbol] {
			return nil, fmt.Errorf("gomutant: duplicate finding symbol %s", findings[i].Symbol)
		}
		symbols[findings[i].Symbol] = true
		nestedComplete, err := validateFindingEncoding(fields, &findings[i])
		if err != nil {
			return nil, fmt.Errorf("gomutant: parse finding %d: %w", i, err)
		}
		complete = complete && nestedComplete
		if findings[i].Commit == "" && !findings[i].Dirty {
			complete = false
		}
		if !complete {
			return nil, fmt.Errorf("gomutant: finding %d is missing or has invalid required evidence", i)
		}
	}
	return findings, nil
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

// subjectEvidenceFields is the one field inventory of the persisted
// SubjectEvidence encoding: each descriptor names the wire field, whether a
// complete record must carry it, and — for required string pins — the
// accessor whose value must be non-empty. Every validation view (known
// fields, presence, non-emptiness) derives from this table, so a new pin
// cannot join one view and silently skip another.
var subjectEvidenceFields = []struct {
	name     string
	required bool
	pin      func(SubjectEvidence) string // non-empty when required; nil for non-string or condition-checked fields
}{
	{"symbol", true, func(e SubjectEvidence) string { return e.Symbol }},
	{"maximalClosure", true, func(e SubjectEvidence) string { return e.MaximalClosure }},
	{"testVariantClosure", true, func(e SubjectEvidence) string { return e.TestVariantClosure }},
	{"toolchain", true, func(e SubjectEvidence) string { return e.Toolchain }},
	{"buildConfig", true, func(e SubjectEvidence) string { return e.BuildConfig }},
	{"observationAssertion", true, func(e SubjectEvidence) string { return e.ObservationAssertion }},
	{"observationStrategy", true, func(e SubjectEvidence) string { return e.ObservationStrategy }},
	{"observationSubjectPackage", true, func(e SubjectEvidence) string { return e.ObservationSubjectPackage }},
	{"observationSubjectSymbol", true, func(e SubjectEvidence) string { return e.ObservationSubjectSymbol }},
	{"observationObservable", true, nil},
	{"observationReason", false, nil},
	{"observationEvidence", true, func(e SubjectEvidence) string { return e.ObservationEvidence }},
	{"purityAssertion", false, nil},
	{"moduleBase", false, nil},
	{"runtimeInputs", true, func(e SubjectEvidence) string { return e.RuntimeInputs }},
	{"runtimeDigest", true, func(e SubjectEvidence) string { return e.RuntimeDigest }},
	{"runtimeUnverifiable", false, nil},
	{"runtimeReason", false, nil},
}

func validateSubjectEvidence(raw json.RawMessage) (bool, error) {
	known := make(map[string]bool, len(subjectEvidenceFields))
	for _, field := range subjectEvidenceFields {
		known[field.name] = true
	}
	fields, err := decodeKnownObject(raw, known)
	if err != nil {
		return false, err
	}
	for name, value := range fields {
		if isJSONNull(value) {
			return false, fmt.Errorf("field %s is null", name)
		}
	}
	for _, field := range subjectEvidenceFields {
		if _, ok := fields[field.name]; field.required && !ok {
			return false, nil
		}
	}
	var evidence SubjectEvidence
	if err := json.Unmarshal(raw, &evidence); err != nil {
		return false, err
	}
	if evidence.RuntimeUnverifiable != (evidence.RuntimeReason != "") {
		return false, nil
	}
	// The module base is written only by treeRelModuleBase, which never
	// produces an absolute or escaping form; admitting one from a
	// hand-edited document would draw the portable-containment line
	// outside the tree (REQ-result-layers).
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
	if evidence.ObservationObservable == (evidence.ObservationReason != "") {
		return false, nil
	}
	for _, field := range subjectEvidenceFields {
		if field.pin != nil && field.pin(evidence) == "" {
			return false, nil
		}
	}
	return true, nil
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

func decodeKnownObject(data []byte, known map[string]bool) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return nil, fmt.Errorf("expected object")
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
// against the current tree without running anything. A caller reminding
// about unhardened or stale-measured symbols asks this instead of
// re-deriving pin arithmetic.
func (t *Tree) Fresh(f Finding, tg Target, budget int) (bool, error) {
	return t.FreshForContext(context.Background(), f, tg, budget, 60*time.Second)
}

// FreshContext is Fresh with caller-owned cancellation.
func (t *Tree) FreshContext(ctx context.Context, f Finding, tg Target, budget int) (bool, error) {
	return t.FreshForContext(ctx, f, tg, budget, 60*time.Second)
}

// FreshFor is Fresh under an explicit effective oracle timeout.
func (t *Tree) FreshFor(f Finding, tg Target, budget int, timeout time.Duration) (bool, error) {
	return t.FreshForContext(context.Background(), f, tg, budget, timeout)
}

// FreshForContext is FreshFor with caller-owned cancellation.
func (t *Tree) FreshForContext(ctx context.Context, f Finding, tg Target, budget int, timeout time.Duration) (bool, error) {
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
	views, err := t.newSubjectViews(ctx, symbols)
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
	matches, err := evidenceSetMatchesContext(ctx, f, targetView, oracleViews, tg.OracleExplicit || len(tg.Oracle) != 0, engine.OperatorSet, timeout.String(), engine.OracleMemoryLimitBytes(), regime)
	if err != nil || !matches {
		return matches, err
	}
	// A record carrying candidate evidence serves only by re-executing its
	// flagged candidates under a passing baseline probe (REQ-result-stale),
	// so it does not cover the target without measurement.
	return len(f.CandidateEvidence) == 0, nil
}

// MergeFindings merges a run's findings over a prior document by symbol — a
// measured or cached finding replaces its symbol's record, untouched symbols
// persist, so a scoped run never drops the rest of the document
// (REQ-result-export; skipped results are excluded by Export, the single
// owner of that rule).
func MergeFindings(prior, fresh []Finding) []Finding {
	merged, _ := MergeFindingsShed(prior, fresh)
	return merged
}

// MergeFindingsShed is MergeFindings additionally reporting the
// dispositions that failed to carry only because the site content under
// their position changed (REQ-attest-survivor).
func MergeFindingsShed(prior, fresh []Finding) ([]Finding, []AttestationShed) {
	return MergeFindingsShedAgainst(prior, fresh, nil)
}

// RenderedFindings substitutes each run finding with its post-merge
// document row where one landed, preserving the run-only Cached and
// Skipped markers: what a run surface renders is what the document
// holds (REQ-mcp-findings-doc).
func RenderedFindings(findings []Finding, postMerge map[string]Finding) []Finding {
	rendered := make([]Finding, len(findings))
	for i, f := range findings {
		if m, ok := postMerge[f.Symbol]; ok {
			m.Cached, m.Skipped = f.Cached, f.Skipped
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
		key := d.Symbol + "\x00" + d.Position + "\x00" + d.Operator
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, d)
	}
	return out
}

// MergeFindingsShedAgainst is MergeFindingsShed with the caller's
// run-start snapshot of attested dispositions per symbol. With the
// snapshot in hand the graft is pin-correct (REQ-attest-survivor):
// a disposition the fresh record already carries rode the pin-hold
// re-measure carry and stays; a disposition present in the live
// document but absent from the snapshot is a concurrent attestation
// and grafts onto a still-reported survivor; every other prior
// disposition was judged afresh and rejected - its pins moved - and
// sheds loudly. Without a snapshot the graft anchors on survivor
// identity and site alone, the pre-snapshot behavior.
func MergeFindingsShedAgainst(prior, fresh []Finding, snapshot map[string][]Attestation) ([]Finding, []AttestationShed) {
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
// by the pin-hold carry and rejected - it sheds as pins-moved instead
// of grafting (REQ-attest-survivor's shed-whenever-any-pin-moves);
// only concurrent attestations (absent from the snapshot) graft. A
// disposition whose survivor the fresh record no longer reports sheds
// loudly in every mode - never silently dropped.
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
			// the pin-hold carry did not keep it: its pins moved and the
			// equivalence is judged afresh (REQ-attest-survivor). A
			// same-mutant disposition whose content differs from the
			// snapshot entry was recorded concurrently - a re-judgment
			// against the current record - and grafts instead.
			if snapAtt, held := snapshot[key]; held && snapAtt == attestation {
				shed = append(shed, AttestationShed{
					Position: attestation.Position,
					Operator: attestation.Operator,
					Reason:   "attestation pins moved - the equivalence is judged afresh",
				})
				continue
			}
		}
		attestation.Site = siteOf[key]
		out = append(out, attestation)
	}
	return out, shed
}

// MergeWholeFindings merges a whole-tree run and removes records whose
// symbols are absent from the complete discovery snapshot
// (REQ-result-hygiene). Scoped callers use MergeFindings instead.
func MergeWholeFindings(prior, fresh []Finding, discovered []Target) []Finding {
	merged, _ := MergeWholeFindingsShed(prior, fresh, discovered)
	return merged
}

// MergeWholeFindingsShed is MergeWholeFindings additionally reporting
// site-anchored attestation sheds (REQ-attest-survivor).
func MergeWholeFindingsShed(prior, fresh []Finding, discovered []Target) ([]Finding, []AttestationShed) {
	return MergeWholeFindingsShedAgainst(prior, fresh, discovered, nil)
}

// MergeWholeFindingsShedAgainst is MergeWholeFindingsShed under the
// caller's run-start snapshot (REQ-attest-survivor).
func MergeWholeFindingsShedAgainst(prior, fresh []Finding, discovered []Target, snapshot map[string][]Attestation) ([]Finding, []AttestationShed) {
	current := make(map[string]bool, len(discovered))
	for _, target := range discovered {
		current[target.Symbol] = true
	}
	merged, shed := MergeFindingsShedAgainst(prior, fresh, snapshot)
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

// UpdateDocument applies update to the findings document at path under an
// exclusive lockfile, re-reading the document inside the lock so a
// concurrent session's dispositions are never clobbered by a stale snapshot
// (REQ-mcp-findings-doc): load-then-long-run-then-write is the caller's
// shape, but the merge always runs against the freshest document. A missing
// document reads as empty; a lock held elsewhere is retried briefly and then
// surfaced with the lock path, so a crashed holder is operator-removable.
func UpdateDocument(path string, update func(prior []Finding) ([]Finding, error)) error {
	return UpdateDocumentContext(context.Background(), path, update)
}

// UpdateDocumentContext is UpdateDocument with cancellation serialized against
// the atomic replacement: cancellation that wins before commit leaves the
// prior document byte-for-byte unchanged.
func UpdateDocumentContext(ctx context.Context, path string, update func(prior []Finding) ([]Finding, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	lock := path + ".lock"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	acquired := false
	for range 50 {
		if err := ctx.Err(); err != nil {
			return err
		}
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			f.Close()
			acquired = true
			break
		}
		if !os.IsExist(err) {
			return err
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	if !acquired {
		return fmt.Errorf("gomutant: findings document locked by another session; remove %s if its holder is gone", lock)
	}
	defer os.Remove(lock)

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
		if prior, err = ParseFindings(data); err != nil {
			return err
		}
	}
	next, err := update(prior)
	if err != nil {
		return err
	}
	doc, err := Export(next)
	if err != nil {
		return err
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
	tmpPath, err := writeTemp(append(doc, '\n'), mode)
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
	return nil
}
