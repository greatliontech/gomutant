package gomutant

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"
	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/gomutant/internal/engine"
)

// SubjectEvidence is gomutant's persisted encoding of one Gofresh code-result
// fingerprint plus the runtime disposition shared by the finding.
type SubjectEvidence struct {
	Symbol              string `json:"symbol"`
	MaximalClosure      string `json:"maximalClosure"`
	Toolchain           string `json:"toolchain"`
	BuildConfig         string `json:"buildConfig"`
	PurityAssertion     string `json:"purityAssertion,omitempty"`
	RuntimeInputs       string `json:"runtimeInputs"`
	RuntimeDigest       string `json:"runtimeDigest"`
	RuntimeUnverifiable bool   `json:"runtimeUnverifiable,omitempty"`
	RuntimeReason       string `json:"runtimeReason,omitempty"`
}

func evidenceFromFingerprint(symbol string, fp gofresh.Fingerprint, state runtimeinput.State) SubjectEvidence {
	return SubjectEvidence{
		Symbol:              symbol,
		MaximalClosure:      fp.MaximalClosure,
		Toolchain:           fp.Guards.Toolchain,
		BuildConfig:         fp.Guards.BuildConfig,
		PurityAssertion:     fp.PurityAssertion,
		RuntimeInputs:       fp.RuntimeInputs,
		RuntimeDigest:       fp.RuntimeDigest,
		RuntimeUnverifiable: state.Unverifiable,
		RuntimeReason:       state.Reason,
	}
}

func (e SubjectEvidence) fingerprint() gofresh.Fingerprint {
	return gofresh.Fingerprint{
		MaximalClosure:  e.MaximalClosure,
		Guards:          guard.Guards{Toolchain: e.Toolchain, BuildConfig: e.BuildConfig},
		PurityAssertion: e.PurityAssertion,
		RuntimeInputs:   e.RuntimeInputs,
		RuntimeDigest:   e.RuntimeDigest,
		ResultKind:      gofresh.CodeResult,
	}
}

// Survivor is one mutant no oracle test noticed.
type Survivor struct {
	Position string `json:"position"`
	Operator string `json:"operator"`
}

// Attestation is one survivor disposition carried on the finding: the
// mutant is attested equivalent, with the reasoning (REQ-attest-survivor).
type Attestation struct {
	Position string `json:"position"`
	Operator string `json:"operator"`
	Reason   string `json:"reason"`
}

// OperatorSummary accounts for every generated mutant of one operator.
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
// pinned to the exact inputs that produced it (REQ-result-record). Open
// findings are Survivors less Attested.
type Finding struct {
	Symbol string   `json:"symbol"`
	Labels []string `json:"labels,omitempty"`

	// The pins (REQ-result-stale): any moved pin re-measures the whole
	// target.
	BodyHash       string            `json:"bodyHash"`
	OperatorSet    string            `json:"operatorSet"`
	Budget         int               `json:"budget"`
	TargetEvidence SubjectEvidence   `json:"targetEvidence"`
	OracleEvidence []SubjectEvidence `json:"oracleEvidence"`
	OracleExplicit bool              `json:"oracleExplicit"`
	Timeout        string            `json:"timeout"`
	Commit         string            `json:"commit,omitempty"`
	Dirty          bool              `json:"dirty"`

	Mutants   int               `json:"mutants"`
	Killed    int               `json:"killed"`
	Discarded int               `json:"discarded,omitempty"`
	Operators []OperatorSummary `json:"operators"`
	Survivors []Survivor        `json:"survivors,omitempty"`
	Attested  []Attestation     `json:"attested,omitempty"`

	// Run metadata, never persisted: a cached finding was served from the
	// prior document under matching pins; a skipped one names why nothing
	// was measured ("no oracle", "not a function").
	Cached  bool   `json:"-"`
	Skipped string `json:"-"`
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
	f.Attested = append(f.Attested, Attestation{Position: position, Operator: operator, Reason: reason})
	return nil
}

// DocumentVersion tags the finding document format; a consumer rejects a
// version it does not understand (REQ-result-export), while unknown fields
// within an understood version are discarded (REQ-result-tolerant).
const DocumentVersion = 1

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
	if version != DocumentVersion {
		return nil, fmt.Errorf("gomutant: findings document version %d not understood (want %d)", version, DocumentVersion)
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
		"oracleExplicit": true, "timeout": true, "commit": true, "dirty": true, "mutants": true,
		"killed": true, "discarded": true, "operators": true, "survivors": true, "attested": true,
	}
	required := []string{"symbol", "bodyHash", "operatorSet", "budget", "targetEvidence", "oracleEvidence", "oracleExplicit", "timeout", "dirty", "mutants", "killed", "operators"}
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
		if findings[i].Symbol == "" || findings[i].BodyHash == "" || findings[i].OperatorSet == "" || findings[i].Timeout == "" {
			complete = false
		} else if duration, err := time.ParseDuration(findings[i].Timeout); err != nil || duration <= 0 || duration.String() != findings[i].Timeout {
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
	if raw, ok := fields["targetEvidence"]; ok {
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
		for _, evidence := range finding.OracleEvidence {
			if evidence.RuntimeInputs != finding.TargetEvidence.RuntimeInputs ||
				evidence.RuntimeDigest != finding.TargetEvidence.RuntimeDigest ||
				evidence.RuntimeUnverifiable != finding.TargetEvidence.RuntimeUnverifiable ||
				evidence.RuntimeReason != finding.TargetEvidence.RuntimeReason {
				return false, fmt.Errorf("subject runtime evidence is not finding-wide")
			}
		}
	}
	if finding.Mutants < 0 || finding.Killed < 0 || finding.Discarded < 0 ||
		finding.Killed > finding.Mutants || len(finding.Survivors) != finding.Mutants-finding.Killed {
		return false, fmt.Errorf("mutant counts do not match killed and survivor records")
	}
	generatedTotal, countsSafe := addNonnegative(finding.Mutants, finding.Discarded)
	if !countsSafe || finding.Budget < 0 || finding.Budget > 0 && finding.Budget != generatedTotal {
		return false, fmt.Errorf("budget does not match generated mutant count")
	}
	survivors := make(map[survivorKey]bool, len(finding.Survivors))
	survivorsByOperator := map[string]int{}
	if raw, ok := fields["survivors"]; ok {
		var records []json.RawMessage
		if err := json.Unmarshal(raw, &records); err != nil {
			return false, fmt.Errorf("survivors: %w", err)
		}
		for i, record := range records {
			if _, err := validateRequiredObject(record, map[string]bool{"position": true, "operator": true}, []string{"position", "operator"}); err != nil {
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
		remainingGenerated, remainingDiscarded := generatedTotal, finding.Discarded
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
	attested := map[survivorKey]bool{}
	if raw, ok := fields["attested"]; ok {
		var records []json.RawMessage
		if err := json.Unmarshal(raw, &records); err != nil {
			return false, fmt.Errorf("attested: %w", err)
		}
		for i, record := range records {
			if _, err := validateRequiredObject(record, map[string]bool{"position": true, "operator": true, "reason": true}, []string{"position", "operator", "reason"}); err != nil {
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

func validateSubjectEvidence(raw json.RawMessage) (bool, error) {
	known := map[string]bool{
		"symbol": true, "maximalClosure": true, "toolchain": true, "buildConfig": true,
		"purityAssertion": true, "runtimeInputs": true, "runtimeDigest": true,
		"runtimeUnverifiable": true, "runtimeReason": true,
	}
	required := []string{"symbol", "maximalClosure", "toolchain", "buildConfig", "runtimeInputs", "runtimeDigest"}
	fields, err := decodeKnownObject(raw, known)
	if err != nil {
		return false, err
	}
	for name, value := range fields {
		if isJSONNull(value) {
			return false, fmt.Errorf("field %s is null", name)
		}
	}
	for _, name := range required {
		if _, ok := fields[name]; !ok {
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
	return evidence.Symbol != "" && evidence.MaximalClosure != "" && evidence.Toolchain != "" &&
		evidence.BuildConfig != "" && evidence.RuntimeInputs != "" && evidence.RuntimeDigest != "", nil
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

// budgetCovers reports whether a finding measured under the recorded cap
// answers a request for req mutants per symbol (0 = exhaustive): a capped
// finding never answers a request for more mutants than it generated
// (REQ-mut-budget, REQ-result-stale).
func budgetCovers(recorded, req int) bool {
	if recorded == 0 {
		return true
	}
	return req > 0 && req <= recorded
}

// Fresh reports whether a prior finding still covers the target at the
// requested budget — the REQ-result-stale pin check as a query, computed
// against the current tree without running anything. A caller reminding
// about unhardened or stale-measured symbols asks this instead of
// re-deriving pin arithmetic.
func (t *Tree) Fresh(f Finding, tg Target, budget int) (bool, error) {
	return t.FreshFor(f, tg, budget, 60*time.Second)
}

// FreshFor is Fresh under an explicit effective per-mutant timeout.
func (t *Tree) FreshFor(f Finding, tg Target, budget int, timeout time.Duration) (bool, error) {
	if f.Symbol != tg.Symbol {
		return false, fmt.Errorf("gomutant: finding %s checked against target %s", f.Symbol, tg.Symbol)
	}
	oracle := t.resolveOracle(tg)
	if err := t.eng.ValidateOracle(oracle); err != nil {
		return false, err
	}
	targetView, err := t.newSubjectView(tg.Symbol)
	if err != nil {
		return false, err
	}
	oracleViews := make([]*subjectView, 0, len(oracle))
	for _, symbol := range oracle {
		view, err := t.newSubjectView(symbol)
		if err != nil {
			return false, err
		}
		oracleViews = append(oracleViews, view)
	}
	if !budgetCovers(f.Budget, budget) {
		return false, nil
	}
	return evidenceSetMatches(f, targetView, oracleViews, tg.OracleExplicit || len(tg.Oracle) != 0, engine.OperatorSet, timeout.String())
}

// MergeFindings merges a run's findings over a prior document by symbol — a
// measured or cached finding replaces its symbol's record, untouched symbols
// persist, so a scoped run never drops the rest of the document
// (REQ-result-export; skipped results are excluded by Export, the single
// owner of that rule).
func MergeFindings(prior, fresh []Finding) []Finding {
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
		bySym[f.Symbol] = f
	}
	out := make([]Finding, 0, len(bySym))
	for _, f := range bySym {
		out = append(out, f)
	}
	return out
}

// MergeWholeFindings merges a whole-tree run and removes records whose
// symbols are absent from the complete discovery snapshot
// (REQ-result-hygiene). Scoped callers use MergeFindings instead.
func MergeWholeFindings(prior, fresh []Finding, discovered []Target) []Finding {
	current := make(map[string]bool, len(discovered))
	for _, target := range discovered {
		current[target.Symbol] = true
	}
	merged := MergeFindings(prior, fresh)
	kept := merged[:0]
	for _, finding := range merged {
		if current[finding.Symbol] {
			kept = append(kept, finding)
		}
	}
	return kept
}

// UpdateDocument applies update to the findings document at path under an
// exclusive lockfile, re-reading the document inside the lock so a
// concurrent session's dispositions are never clobbered by a stale snapshot
// (REQ-mcp-findings-doc): load-then-long-run-then-write is the caller's
// shape, but the merge always runs against the freshest document. A missing
// document reads as empty; a lock held elsewhere is retried briefly and then
// surfaced with the lock path, so a crashed holder is operator-removable.
func UpdateDocument(path string, update func(prior []Finding) ([]Finding, error)) error {
	lock := path + ".lock"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	acquired := false
	for range 50 {
		f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			f.Close()
			acquired = true
			break
		}
		if !os.IsExist(err) {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !acquired {
		return fmt.Errorf("gomutant: findings document locked by another session; remove %s if its holder is gone", lock)
	}
	defer os.Remove(lock)

	var prior []Finding
	data, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
	case err != nil:
		return err
	default:
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
	return os.WriteFile(path, append(doc, '\n'), 0o644)
}
