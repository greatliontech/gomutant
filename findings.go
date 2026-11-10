package gomutant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	gofresh "github.com/greatliontech/gofresh"
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
	// test moved" from every other drift. Recorded compartment evidence
	// is never refreshed to retain outcomes across oracle changes;
	// inspection and attribution surface gofresh's stable "test variants"
	// verdict reason on refusal
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
	// RuntimeAttribution is the attribution of the classification
	// refusal RuntimeReason names — the harness operation, its logged
	// name, and the producing process's own directory, as gofresh
	// attributes it on the observation the row's state was read from
	// (the union's, so every row of a record carries the union's) —
	// audit riding the row, never a pin: empty where the reason names
	// no attributed refusal, and on records measured before it was
	// recorded (REQ-result-record's subject-evidence term).
	RuntimeAttribution string
}

// subjectEvidenceWire is the row's wire form: gomutant's five fields and
// the fingerprint's own record, the latter absent on the zero row.
type subjectEvidenceWire struct {
	Symbol              string               `json:"symbol"`
	Fingerprint         *gofresh.Fingerprint `json:"fingerprint,omitempty"`
	ModuleBase          string               `json:"moduleBase,omitempty"`
	RuntimeUnverifiable bool                 `json:"runtimeUnverifiable,omitempty"`
	RuntimeReason       string               `json:"runtimeReason,omitempty"`
	RuntimeAttribution  string               `json:"runtimeAttribution,omitempty"`
}

// MarshalJSON encodes the row in its wire form, the fingerprint in
// gofresh's record form (REQ-result-record).
func (e SubjectEvidence) MarshalJSON() ([]byte, error) {
	w := subjectEvidenceWire{Symbol: e.Symbol, ModuleBase: e.ModuleBase, RuntimeUnverifiable: e.RuntimeUnverifiable, RuntimeReason: e.RuntimeReason, RuntimeAttribution: e.RuntimeAttribution}
	if e.Fingerprint != (gofresh.Fingerprint{}) {
		fp := e.Fingerprint
		w.Fingerprint = &fp
	}
	return json.Marshal(w)
}

// subjectEvidenceKeys is the row's known key set — the outer shape the
// parser refuses beyond; the fingerprint's own keys are its decoder's.
var subjectEvidenceKeys = map[string]bool{"symbol": true, "fingerprint": true, "moduleBase": true, "runtimeUnverifiable": true, "runtimeReason": true, "runtimeAttribution": true}

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
	decoded := SubjectEvidence{Symbol: w.Symbol, ModuleBase: w.ModuleBase, RuntimeUnverifiable: w.RuntimeUnverifiable, RuntimeReason: w.RuntimeReason, RuntimeAttribution: w.RuntimeAttribution}
	if w.Fingerprint != nil {
		decoded.Fingerprint = *w.Fingerprint
	}
	*e = decoded
	return nil
}

func evidenceFromFingerprint(symbol string, fp gofresh.Fingerprint, re runtimeEvidence) SubjectEvidence {
	return SubjectEvidence{Symbol: symbol, Fingerprint: fp, RuntimeUnverifiable: re.state.Unverifiable, RuntimeReason: re.state.Reason, RuntimeAttribution: re.attribution}
}

// runtimeEvidence is what a completed observation stamps on an evidence
// row: its state — the manifest, digest, unverifiable disposition and
// reason — and the attribution of the refusal that reason names, read
// from the one observation so a row can never carry one observation's
// reason beside another's attribution (REQ-result-record).
type runtimeEvidence struct {
	state       gofresh.RuntimeState
	attribution string
}

// completedEvidence reads a completed observation's runtime evidence:
// its state, and the attribution of the refusal the state's reason
// names — the observation's own for a classification refusal, else
// the one a resolved-target refusal carries in the reason itself as
// state, split by gofresh's one implementation of that split.
func completedEvidence(observation gofresh.Observation) (runtimeEvidence, error) {
	state, err := gofresh.CompletedObservationState(observation)
	if err != nil {
		return runtimeEvidence{}, err
	}
	return runtimeEvidence{state: state, attribution: evidenceAttribution(observation.Attribution, state.Reason)}, nil
}

// AttributedReason renders a row's reason with its attribution after it
// ("<reason>; attributed to <attribution>"), the one spelling explain
// and the guidance line share: the reason is the union's on every row,
// so the attribution is what says whose process made the read. A
// reason that already carries the attribution as state (a
// resolved-target refusal's) renders once (REQ-result-exemptions).
func AttributedReason(reason, attribution string) string {
	if attribution == "" || refusalAttribution(reason) == attribution {
		return reason
	}
	return reason + "; attributed to " + attribution
}

// evidenceAttribution is the attribution a row records for its reason:
// the observation's own, which gofresh sets for a classification
// refusal, else the one a resolved-target refusal carries in the
// reason itself as state, split by the one refusal-attribution
// implementation — empty where the reason names no attributed refusal.
func evidenceAttribution(observed, reason string) string {
	if observed != "" {
		return observed
	}
	return refusalAttribution(reason)
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
	// Package is the declaring file's package clause name. Older records
	// without it retain that absence rather than borrowing another
	// variant's package identity.
	Package string `json:"package,omitempty"`
}

// CompartmentFileHeader is one compartment file's persisted header identity.
type CompartmentFileHeader struct {
	File     string               `json:"file"`
	Hash     string               `json:"hash"`
	Embedded bool                 `json:"embedded,omitempty"`
	Bindings *CompartmentBindings `json:"bindings,omitempty"`
}

// CompartmentBindings preserves the file-scoped name resolution evidence.
type CompartmentBindings struct {
	Package    string              `json:"package"`
	References []string            `json:"references,omitzero"`
	Imports    []CompartmentImport `json:"imports,omitzero"`
}

// CompartmentImport records the effective local name and imported path.
type CompartmentImport struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// CompartmentLedger is the target package's persisted test-variant
// declaration ledger (REQ-result-record): recorded at measure time from the
// same view snapshot the compartment hash pinned. It is preserved as data;
// its reference graph supplies no oracle-independence license.
type CompartmentLedger struct {
	BindingStrategy string                   `json:"bindingStrategy,omitempty"`
	BaseFiles       []CompartmentFileHeader  `json:"baseFiles,omitzero"`
	Declarations    []CompartmentDeclaration `json:"declarations,omitempty"`
	FileHeaders     []CompartmentFileHeader  `json:"fileHeaders,omitempty"`
}

// compartmentLedgerFromView converts gofresh's ledger to the wire encoding.
func compartmentLedgerFromView(ledger gofresh.TestVariantLedger) *CompartmentLedger {
	out := &CompartmentLedger{
		BindingStrategy: ledger.BindingStrategy,
		BaseFiles:       compartmentHeadersFromView(ledger.BaseFiles),
		Declarations:    make([]CompartmentDeclaration, 0, len(ledger.Declarations)),
		FileHeaders:     compartmentHeadersFromView(ledger.FileHeaders),
	}
	for _, declaration := range ledger.Declarations {
		// Declaration reference lists are not part of this wire form.
		// Preserve the historical projection without inventing them.
		out.Declarations = append(out.Declarations, CompartmentDeclaration{
			File: declaration.File, Kind: declaration.Kind, Name: declaration.Name,
			Receiver: declaration.Receiver, Hash: declaration.Hash, Package: declaration.Package,
		})
	}
	return out
}

// ledger converts the wire encoding back to gofresh's ledger type.
func (l *CompartmentLedger) ledger() gofresh.TestVariantLedger {
	out := gofresh.TestVariantLedger{
		BindingStrategy: l.BindingStrategy,
		BaseFiles:       compartmentHeadersToView(l.BaseFiles),
		Declarations:    make([]gofresh.TestVariantDeclaration, 0, len(l.Declarations)),
		FileHeaders:     compartmentHeadersToView(l.FileHeaders),
	}
	for _, declaration := range l.Declarations {
		out.Declarations = append(out.Declarations, gofresh.TestVariantDeclaration{
			File: declaration.File, Kind: declaration.Kind, Name: declaration.Name,
			Receiver: declaration.Receiver, Hash: declaration.Hash, Package: declaration.Package,
		})
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
	// "coverage-unobserved" - the parent baseline profile reports no
	// reach; child execution and compile-time influence remain unknown.
	// "executed-and-passed" - baseline coverage reaches the extent and
	// the complete required mutant oracle passed. "never-executed" and
	// "covering-passed" are historical buckets, not fresh measurements
	// under the full-oracle policy. "overlay-bypassed" - the observed union recorded a read of
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
	// OtherSelection carries advisory verdicts for the same mutant from
	// coexisting records of the same symbol under other build selections
	// whose mutation domain (BodyHash, OperatorSet, and Site) and
	// oracleExecutionPolicy hold. Advisory only: another selection's kill
	// or attestation never licenses skipping measurement or suppressing an
	// open survivor in this selection (REQ-result-record, REQ-result-findings).
	OtherSelection string `json:"otherSelection,omitempty"`
}

// SelectionVerdict records how another build selection's coexisting
// finding for the same symbol judged the same mutant when both records
// share the same mutation domain and an admitted oracle execution policy.
type SelectionVerdict struct {
	Selection   string `json:"selection"`
	Disposition string `json:"disposition"`
	Killer      string `json:"killer,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// OtherSelections returns the parsed cross-selection verdicts carried on s.
func (s Survivor) OtherSelections() []SelectionVerdict {
	if s.OtherSelection == "" {
		return nil
	}
	verdicts, err := parseSelectionVerdicts(s.OtherSelection)
	if err != nil {
		return nil
	}
	return verdicts
}

func formatSelectionVerdict(v SelectionVerdict) string {
	sel := v.Selection
	if sel == "" {
		sel = "default"
	}
	switch v.Disposition {
	case "killed":
		return sel + ": killed by " + v.Killer
	case "attested":
		return sel + ": attested (" + v.Reason + ")"
	default:
		return sel + ": survived"
	}
}

func formatSelectionVerdicts(verdicts []SelectionVerdict) string {
	if len(verdicts) == 0 {
		return ""
	}
	lines := make([]string, len(verdicts))
	for i, v := range verdicts {
		lines[i] = formatSelectionVerdict(v)
	}
	return strings.Join(lines, "\n")
}

func parseSelectionVerdicts(raw string) ([]SelectionVerdict, error) {
	if raw == "" {
		return nil, nil
	}
	lines := strings.Split(raw, "\n")
	out := make([]SelectionVerdict, 0, len(lines))
	for _, line := range lines {
		sel, rest, ok := strings.Cut(line, ": ")
		if !ok || sel == "" || rest == "" {
			return nil, fmt.Errorf("malformed cross-selection verdict %q", line)
		}
		if sel == "default" {
			sel = ""
		} else if err := validateSelectionKey(sel); err != nil {
			return nil, err
		}
		switch {
		case rest == "survived":
			out = append(out, SelectionVerdict{Selection: sel, Disposition: "survived"})
		case strings.HasPrefix(rest, "killed by "):
			killer := strings.TrimPrefix(rest, "killed by ")
			if killer == "" || strings.TrimSpace(killer) != killer {
				return nil, fmt.Errorf("malformed cross-selection killed verdict %q", line)
			}
			out = append(out, SelectionVerdict{Selection: sel, Disposition: "killed", Killer: killer})
		case strings.HasPrefix(rest, "attested (") && strings.HasSuffix(rest, ")"):
			reason := rest[len("attested (") : len(rest)-1]
			if err := ValidateAttestationReason(reason); err != nil {
				return nil, fmt.Errorf("cross-selection attested verdict: %w", err)
			}
			out = append(out, SelectionVerdict{Selection: sel, Disposition: "attested", Reason: reason})
		default:
			return nil, fmt.Errorf("malformed cross-selection verdict %q", line)
		}
	}
	return out, nil
}

// SurvivorAdvice maps a survivor's execution bucket to the action it
// prescribes. The vocabulary is the explain surface's contract: the
// bucket says why the mutant lived, the advice says what closes it, and
// both stay advisory — never a verdict (REQ-result-findings).
func SurvivorAdvice(execution string) string {
	switch execution {
	case "coverage-unobserved":
		return "the parent baseline profile reports no reach; child execution and compile-time influence are unaccounted for - inspect the deciding oracle before inferring a coverage gap"
	case "never-executed":
		return "historical coverage reported no reach - re-measure under the complete oracle before drawing a coverage conclusion"
	case "executed-and-passed":
		return "the baseline profile reaches the extent and the required mutant oracle passed - sharpen an assertion or attest an equivalence"
	case "covering-passed":
		return "historical measurement omitted non-reaching batches - re-measure under the complete oracle before judging this survivor"
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
// without implying that the killer is independent of the rest of the oracle.
// Killer is the killing oracle test's symbol, the timeout marker, or the
// package-failure
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
// re-execution splice conserves REQ-result-candidate-conservation.
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

// WithdrawnAttestation records a formerly attested equivalence disposition
// that was explicitly withdrawn, returning the survivor to open while
// preserving the withdrawal reasoning on the finding (REQ-attest-survivor).
type WithdrawnAttestation struct {
	Position    string `json:"position"`
	Operator    string `json:"operator"`
	PriorReason string `json:"priorReason"`
	Reason      string `json:"reason"`
	Site        string `json:"site,omitempty"`
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

// RecordKey identifies one finding record by its mutated symbol and
// the canonical build selection key (SelectionKey) it was measured under
// (REQ-result-record).
type RecordKey struct {
	Symbol    string
	Selection string
}

func (k RecordKey) String() string {
	if k.Selection == "" {
		return k.Symbol
	}
	return k.Symbol + " [" + k.Selection + "]"
}

func recordKeyOf(symbol, selection string) string {
	if selection == "" {
		return symbol
	}
	return symbol + "\x00" + selection
}

// Finding is one target's measurement, keyed by the mutated symbol and
// canonical build selection and carrying the available evidence for
// deciding reuse (REQ-result-record). Open findings are Survivors less Attested.
type Finding struct {
	Symbol    string   `json:"symbol"`
	Selection string   `json:"selection,omitempty"`
	Labels    []string `json:"labels,omitempty"`

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
	// OracleExecutionPolicy records the measurement's oracle-execution
	// authority. Missing, policy-1 or unknown policy on a body finding
	// requires whole remeasurement: neither negative coverage nor changed-
	// oracle composition is licensed. Decoding or layer movement never
	// supplies a policy.
	OracleExecutionPolicy string `json:"oracleExecutionPolicy,omitempty"`
	// CompartmentLedger is the target package's test-variant declaration
	// ledger at measure time. It remains inspectable historical evidence,
	// never an oracle-independence license (REQ-result-stale).
	CompartmentLedger *CompartmentLedger `json:"compartmentLedger,omitempty"`
	Commit            string             `json:"commit,omitempty"`
	Dirty             bool               `json:"dirty"`
	// EvidenceFault records a finding-wide evidence failure — a
	// validation proof cut by the analysis budget, a runtime-input
	// observation divergence, or a provenance evidence-walk fault — as
	// record-level state distinct from git worktree drift (Dirty) and
	// from per-subject RuntimeReason.
	EvidenceFault string `json:"evidenceFault,omitempty"`
	// Run is the identity of the run that last measured any candidate
	// of the record — a fresh measure, a budget extension, or a serve
	// that re-executed flagged candidates — never a wholly
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
	// was persisted. Absence supplies no attribution or composition
	// authority; execution policy and all other pins govern reuse.
	Kills             []Kill                 `json:"kills,omitempty"`
	Survivors         []Survivor             `json:"survivors,omitempty"`
	Attested          []Attestation          `json:"attested,omitempty"`
	Withdrawn         []WithdrawnAttestation `json:"withdrawn,omitempty"`
	CandidateEvidence []CandidateEvidence    `json:"candidateEvidence,omitempty"`

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

// RecordKey returns f's identity key (Symbol, Selection).
func (f Finding) RecordKey() RecordKey {
	return RecordKey{Symbol: f.Symbol, Selection: f.Selection}
}

func (f Finding) recordKeyString() string {
	return recordKeyOf(f.Symbol, f.Selection)
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

// parseSelectionKey parses a canonical SelectionKey back into a Selection,
// returning false when s is non-canonical or malformed.
func parseSelectionKey(s string) (Selection, bool) {
	if s == "" {
		return Selection{}, true
	}
	var sel Selection
	for _, part := range strings.Split(s, ";") {
		switch {
		case strings.HasPrefix(part, "tags:"):
			if len(sel.Tags) > 0 {
				return Selection{}, false
			}
			raw := strings.TrimPrefix(part, "tags:")
			if raw == "" {
				return Selection{}, false
			}
			tags := strings.Split(raw, ",")
			for _, t := range tags {
				if t == "" || strings.TrimSpace(t) != t {
					return Selection{}, false
				}
			}
			sel.Tags = tags
		case strings.HasPrefix(part, "toolchain:"):
			if sel.Toolchain != "" {
				return Selection{}, false
			}
			tc := strings.TrimPrefix(part, "toolchain:")
			if tc == "" || strings.TrimSpace(tc) != tc {
				return Selection{}, false
			}
			sel.Toolchain = tc
		default:
			return Selection{}, false
		}
	}
	return sel, SelectionKey(sel) == s
}

func validateSelectionKey(s string) error {
	if _, ok := parseSelectionKey(s); !ok {
		return fmt.Errorf("invalid non-canonical selection key %q", s)
	}
	return nil
}

// SelectionOfKey parses a canonical SelectionKey back into a Selection.
func SelectionOfKey(s string) Selection {
	sel, _ := parseSelectionKey(s)
	return sel
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
	f.Withdrawn = slices.Clone(f.Withdrawn)
	f.CandidateEvidence = slices.Clone(f.CandidateEvidence)
	if f.CompartmentLedger != nil {
		ledger := *f.CompartmentLedger
		ledger.Declarations = slices.Clone(ledger.Declarations)
		ledger.FileHeaders = cloneCompartmentHeaders(ledger.FileHeaders)
		ledger.BaseFiles = cloneCompartmentHeaders(ledger.BaseFiles)
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

// WithdrawnDispositions returns a canonical copy of the finding's withdrawn
// attestations for deterministic views.
func (f *Finding) WithdrawnDispositions() []WithdrawnAttestation {
	withdrawn := append([]WithdrawnAttestation(nil), f.Withdrawn...)
	sort.SliceStable(withdrawn, func(i, j int) bool {
		if withdrawn[i].Position != withdrawn[j].Position {
			return withdrawn[i].Position < withdrawn[j].Position
		}
		return withdrawn[i].Operator < withdrawn[j].Operator
	})
	return withdrawn
}

// Attest records a survivor disposition on the finding, refused unless the
// named mutant is among its current survivors (REQ-attest-survivor).
func (f *Finding) Attest(position, operator, reason string) error {
	if reason == "" {
		return fmt.Errorf("gomutant: attestation needs a reason")
	}
	if why := OracleExecutionPolicyIssue(*f); why != "" {
		return fmt.Errorf("gomutant: %s: %s", f.Symbol, why)
	}
	found := false
	site := ""
	for _, s := range f.Survivors {
		if s.Position == position && s.Operator == operator {
			found = true
			site = s.Site
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
	f.Withdrawn = slices.DeleteFunc(f.Withdrawn, func(w WithdrawnAttestation) bool {
		return w.Position == position && w.Operator == operator
	})
	f.Attested = append(f.Attested, Attestation{Position: position, Operator: operator, Reason: reason, Site: site})
	return nil
}

// Reattest replaces an existing survivor disposition's reason on the finding,
// refused unless the named mutant is among its current survivors, is already
// attested, and the finding's oracle execution policy is admitted
// (REQ-attest-survivor).
func (f *Finding) Reattest(position, operator, reason string) error {
	if reason == "" {
		return fmt.Errorf("gomutant: attestation needs a reason")
	}
	if why := OracleExecutionPolicyIssue(*f); why != "" {
		return fmt.Errorf("gomutant: %s: %s", f.Symbol, why)
	}
	found := false
	site := ""
	for _, s := range f.Survivors {
		if s.Position == position && s.Operator == operator {
			found = true
			site = s.Site
			break
		}
	}
	if !found {
		return fmt.Errorf("gomutant: %s has no survivor %s %s", f.Symbol, position, operator)
	}
	for i := range f.Attested {
		if f.Attested[i].Position == position && f.Attested[i].Operator == operator {
			f.Attested[i].Reason = reason
			f.Attested[i].Site = site
			return nil
		}
	}
	return fmt.Errorf("gomutant: survivor %s %s is not attested", position, operator)
}

// WithdrawAttestation withdraws an existing survivor disposition on the
// finding, returning the survivor to open and recording the withdrawal reason
// alongside the prior attestation reason (REQ-attest-survivor).
func (f *Finding) WithdrawAttestation(position, operator, reason string) error {
	if reason == "" {
		return fmt.Errorf("gomutant: withdrawal needs a reason")
	}
	site := ""
	for _, s := range f.Survivors {
		if s.Position == position && s.Operator == operator {
			site = s.Site
			break
		}
	}
	for i, a := range f.Attested {
		if a.Position == position && a.Operator == operator {
			if site == "" {
				site = a.Site
			}
			f.Attested = slices.Delete(f.Attested, i, i+1)
			f.Withdrawn = slices.DeleteFunc(f.Withdrawn, func(w WithdrawnAttestation) bool {
				return w.Position == position && w.Operator == operator
			})
			f.Withdrawn = append(f.Withdrawn, WithdrawnAttestation{
				Position:    position,
				Operator:    operator,
				PriorReason: a.Reason,
				Reason:      reason,
				Site:        site,
			})
			return nil
		}
	}
	return fmt.Errorf("gomutant: survivor %s %s is not attested", position, operator)
}

// DocumentVersion is the findings document's current version. A bump
// draws a reading boundary: a field that narrows reuse or moves a pin
// (candidate evidence, the compartment pin, the site anchor, the
// property-regime pin, the dynamic-state strategy) or a shape an older
// reader cannot re-derive (positional init targets, shaped targets, the
// interned tables, selection-coexisting records) bumps it, because an older
// consumer's tolerance would serve verdicts computed under semantics its
// engine does not implement or destroy records it cannot resolve; so does a
// field whose absence an older reader would take in the flattering direction
// (the coverage-bounds table: a dropped bound reads as full coverage); an
// audit field, whose absence widens no claim (a discharge list; a
// survivor extent, which falls back to the anchor-point bucket), lands
// without one; version 13 embeds gofresh's published fingerprint record
// in every evidence row (a shape an older reader cannot re-derive, the
// flat rows upgraded on read); version 14 references the tables by
// content key in place of position (a shape an older reader cannot
// resolve, the positional documents of 11-13 read as they were); version
// 17 keys records by (symbol, selection) so build selections coexist in
// one document and carries withdrawn attestation history and cross-selection
// survivor verdicts. The reading range each boundary draws is ParseDocument's.
const DocumentVersion = 17

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
// last measured the record, or the build selection key; each empty field
// admits every record unless HasSelection scopes explicitly to the
// default selection (REQ-result-inspection). The judged state filter is
// applied after inspection, not here.
type RecordFilter struct {
	Label, Symbol, Run, Selection string
	HasSelection                  bool
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
	if (r.HasSelection || r.Selection != "") && f.Selection != r.Selection {
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
	if OracleExecutionPolicyIssue(f) != "" {
		return false, nil
	}
	oracle, err := t.resolveOracleContext(ctx, tg)
	if err != nil {
		return false, err
	}
	if err := t.eng.ValidateOracleContext(ctx, oracle); err != nil {
		return false, err
	}
	symbols := append([]string{tg.Symbol}, oracle...)
	groups, err := t.resolveModuleGroups(ctx, symbols, t.eng.PackageContextContext, func(_ string, err error) error { return err })
	if err != nil {
		return false, err
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
	// Check view-free pin predicates before constructing a gofresh Snapshot:
	// a record carrying candidate evidence serves only by re-executing its
	// flagged candidates under a passing baseline probe (REQ-result-stale),
	// so it does not cover the target without measurement.
	oracleExplicit := tg.OracleExplicit || len(tg.Oracle) != 0
	if !budgetCovers(f, budget) || len(f.CandidateEvidence) != 0 ||
		f.EvidenceFault != "" || f.OperatorSet != engine.OperatorSet ||
		f.OracleExplicit != oracleExplicit || !timeoutPinMatches(f, timeout.String(), timeoutDerived) ||
		memoryPinStale(f, standaloneMemoryPin) || f.PropertyRegime != regime ||
		len(f.OracleEvidence) != len(oracle) {
		return false, nil
	}
	faults := map[string]error{}
	views, err := t.buildGroupViews(ctx, symbols, groups, t.newSubjectEngines(nil, packageProcessAttestable(t.PackageOf, tg.Symbol, oracle), engine.OracleBounds{}, 0), faults)
	if err != nil {
		return false, err
	}
	if err := firstFault(symbols, faults); err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	targetView := views.bySymbol[tg.Symbol]
	oracleViews := make([]*subjectView, 0, len(oracle))
	for _, symbol := range oracle {
		oracleViews = append(oracleViews, views.bySymbol[symbol])
	}
	// The pin comparison takes the REQUEST's posture, never the
	// record's: an explicit caller timeout invalidates a derived
	// record exactly as an explicit Run would re-measure it, and the
	// derive-posture default relaxes to the timeout-kill rule.
	return evidenceSetMatchesContext(ctx, f, targetView, oracleViews, oracleExplicit, engine.OperatorSet, timeout.String(), timeoutDerived, standaloneMemoryPin, regime)
}
