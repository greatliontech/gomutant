package gomutant

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/greatliontech/gomutant/internal/engine"
)

// OraclePin is one oracle test the finding ran against, pinned by symbol and
// the body hash it ran at (REQ-result-record): an edit to a bound test moves
// its hash and re-stales the record, so a strengthened test never leaves a
// stale survivor.
type OraclePin struct {
	Symbol string `json:"symbol"`
	Hash   string `json:"hash"`
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

// Finding is one target's measurement, keyed by the mutated symbol and
// pinned to the exact inputs that produced it (REQ-result-record). Open
// findings are Survivors less Attested.
type Finding struct {
	Symbol string   `json:"symbol"`
	Labels []string `json:"labels,omitempty"`

	// The pins (REQ-result-stale): any moved pin re-measures the whole
	// target.
	BodyHash    string      `json:"bodyHash"`
	BodyLine    int         `json:"bodyLine,omitempty"`
	Oracle      []OraclePin `json:"oracle,omitempty"`
	OperatorSet string      `json:"operatorSet"`
	Budget      int         `json:"budget,omitempty"`
	Toolchain   string      `json:"toolchain"`

	Mutants   int           `json:"mutants"`
	Killed    int           `json:"killed"`
	Discarded int           `json:"discarded,omitempty"`
	Survivors []Survivor    `json:"survivors,omitempty"`
	Attested  []Attestation `json:"attested,omitempty"`

	// Run metadata, never persisted: a cached finding was served from the
	// prior document under matching pins; a skipped one names why nothing
	// was measured ("no oracle", "not a function").
	Cached  bool   `json:"-"`
	Skipped string `json:"-"`
}

// Open returns the finding's open survivors — survivors less attested
// dispositions (REQ-attest-survivor, REQ-result-findings).
func (f *Finding) Open() []Survivor {
	attested := map[string]bool{}
	for _, a := range f.Attested {
		attested[a.Position+"|"+a.Operator] = true
	}
	var open []Survivor
	for _, s := range f.Survivors {
		if !attested[s.Position+"|"+s.Operator] {
			open = append(open, s)
		}
	}
	return open
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
		kept = append(kept, f)
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].Symbol < kept[j].Symbol })
	return json.MarshalIndent(document{Version: DocumentVersion, Findings: kept}, "", "  ")
}

// ParseFindings loads a finding document: an unknown version is refused
// (REQ-result-export), an unknown field within a known version is discarded
// (REQ-result-tolerant — encoding/json drops unknown fields, and a document
// missing a pin re-stales at the next run).
func ParseFindings(data []byte) ([]Finding, error) {
	var doc document
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("gomutant: parse findings document: %w", err)
	}
	if doc.Version != DocumentVersion {
		return nil, fmt.Errorf("gomutant: findings document version %d not understood (want %d)", doc.Version, DocumentVersion)
	}
	return doc.Findings, nil
}

// pinsMatch reports whether a prior finding's pins cover the current target
// state (REQ-result-stale): same body hash, same oracle content (compared as
// a set — judged on content, not order), same operator set, same toolchain.
func pinsMatch(prior Finding, bodyHash string, oracle []OraclePin, operatorSet, toolchain string) bool {
	if prior.BodyHash != bodyHash || prior.OperatorSet != operatorSet || prior.Toolchain != toolchain {
		return false
	}
	if len(prior.Oracle) != len(oracle) {
		return false
	}
	bySym := make(map[string]string, len(prior.Oracle))
	for _, p := range prior.Oracle {
		bySym[p.Symbol] = p.Hash
	}
	for _, c := range oracle {
		if h, ok := bySym[c.Symbol]; !ok || h != c.Hash {
			return false
		}
	}
	return true
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

// shiftPos rebases a file:line:col position by delta lines; false when the
// position does not parse. Positions are absolute file coordinates, so a
// disposition carried across regenerations rebases against the recorded
// declaration anchor first — drift from edits above the body never sheds it
// (REQ-attest-survivor).
func shiftPos(pos string, delta int) (string, bool) {
	parts := strings.Split(pos, ":")
	if len(parts) != 3 {
		return "", false
	}
	line, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", false
	}
	return fmt.Sprintf("%s:%d:%s", parts[0], line+delta, parts[2]), true
}

// Fresh reports whether a prior finding still covers the target at the
// requested budget — the REQ-result-stale pin check as a query, computed
// against the current tree without running anything. A caller reminding
// about unhardened or stale-measured symbols asks this instead of
// re-deriving pin arithmetic.
func (t *Tree) Fresh(f Finding, tg Target, budget int) (bool, error) {
	if f.Symbol != tg.Symbol {
		return false, fmt.Errorf("gomutant: finding %s checked against target %s", f.Symbol, tg.Symbol)
	}
	toolchain, err := engine.Toolchain(t.dir)
	if err != nil {
		return false, err
	}
	bodyHash, err := t.eng.BodyHash(tg.Symbol)
	if err != nil {
		return false, err
	}
	oracle := t.resolveOracle(tg)
	pins := make([]OraclePin, 0, len(oracle))
	for _, o := range oracle {
		oh, err := t.eng.BodyHash(o)
		if err != nil {
			return false, err
		}
		pins = append(pins, OraclePin{Symbol: o, Hash: oh})
	}
	return pinsMatch(f, bodyHash, pins, engine.OperatorSet, toolchain) && budgetCovers(f.Budget, budget), nil
}
