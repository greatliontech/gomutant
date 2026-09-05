package gomutant

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Exemption is one reviewed entry of the committed exemption record
// (REQ-result-exemptions): a named subject whose runtime evidence is
// accepted as classification-stable under exactly one recorded
// unverifiable reason, with the reviewer's reasoning on the record.
// The record is the live authority - classification consults it on
// every read, so deleting an entry revokes it for every later
// decision - and the matched entries are stamped onto each finding
// they touch, so a reviewer inheriting the repo document sees the
// acceptance beside the evidence it excuses.
type Exemption struct {
	Subject   string `json:"subject"`
	Reason    string `json:"reason"`
	Rationale string `json:"rationale"`
}

type exemptionsDocument struct {
	Version    int         `json:"version"`
	Exemptions []Exemption `json:"exemptions"`
}

// ExemptionsPathFor is the committed exemption record's home beside a
// findings document: the review unit is the pair - the evidence and
// the acceptances that let it commit travel together.
func ExemptionsPathFor(findingsPath string) string {
	return filepath.Join(filepath.Dir(findingsPath), "exemptions.json")
}

// LoadExemptions reads and validates the committed exemption record; a
// missing file is an empty record. Every entry needs its subject, the
// exact recorded reason it accepts, and the reviewer's rationale - an
// unreasoned or reason-free acceptance would be the silent global
// switch the record exists to avoid.
func LoadExemptions(path string) ([]Exemption, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("gomutant: reading exemption record: %w", err)
	}
	var doc exemptionsDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("gomutant: exemption record %s: %w", path, err)
	}
	if doc.Version != 1 {
		return nil, fmt.Errorf("gomutant: exemption record %s: unsupported version %d", path, doc.Version)
	}
	for i, e := range doc.Exemptions {
		if carriesAttribution(e.Reason) {
			return nil, fmt.Errorf("gomutant: exemption record %s: entry %d names a moved-bracket reason with its attribution %q — the attribution is fresh per measurement; name the clause alone", path, i, e.Reason[strings.LastIndex(e.Reason, " ["):])
		}
		if e.Subject == "" || e.Reason == "" || e.Rationale == "" {
			return nil, fmt.Errorf("gomutant: exemption record %s: entry %d needs subject, reason, and rationale", path, i)
		}
	}
	return doc.Exemptions, nil
}

// exemptionFor returns the entry accepting (subject, reason) exactly,
// or nil. Matching is exact on the subject and on the reason's clause:
// a clause drifting even one byte is a different instability the
// record never reviewed. The producer may append a bracketed
// attribution to a clause — which files moved a bracket, and when —
// that is diagnostic detail, fresh per measurement, which the clause
// does not include (REQ-result-exemptions).
func exemptionFor(exemptions []Exemption, subject, reason string) *Exemption {
	clause := reasonClause(reason)
	for i := range exemptions {
		if exemptions[i].Subject == subject && exemptions[i].Reason == clause {
			return &exemptions[i]
		}
	}
	return nil
}

// movedBracketClause prefixes the one reason the producer attributes:
// a moved observation bracket, whose trailing " [...]" names the
// members that moved. Every other reason ends in a path, and a path may
// legitimately end in a bracketed segment, so the strip is gated on
// this prefix and touches no other clause.
const movedBracketClause = "observation bracket moved: "

// reasonClause is a recorded reason without the producer's trailing
// bracketed attribution — present only on the moved-bracket clause.
func reasonClause(reason string) string {
	if !strings.HasPrefix(reason, movedBracketClause) || !strings.HasSuffix(reason, "]") {
		return reason
	}
	if i := strings.LastIndex(reason, " ["); i > len(movedBracketClause) {
		return reason[:i]
	}
	return reason
}

// carriesAttribution reports whether an entry's reason is a
// moved-bracket clause pasted with its attribution: such an entry can
// never match (the attribution is fresh per measurement), so the record
// refuses it rather than holding a dead acceptance.
func carriesAttribution(reason string) bool {
	return reasonClause(reason) != reason
}

// coveredExemptions reports whether every runtime-unverifiable subject
// evidence of f is accepted by the record, and the matched entries in
// evidence order. The target's union evidence - unverifiable because a
// constituent process's read sealed it - is additionally covered by an
// entry naming any of the finding's oracle subjects under the same
// reason: the union inherits the acceptance of the read that tainted
// it, and only that read.
func coveredExemptions(f *Finding, exemptions []Exemption) ([]Exemption, bool) {
	if len(exemptions) == 0 {
		return nil, false
	}
	var matched []Exemption
	seen := map[Exemption]bool{}
	note := func(e *Exemption) {
		if !seen[*e] {
			seen[*e] = true
			matched = append(matched, *e)
		}
	}
	for _, ev := range f.OracleEvidence {
		if !ev.RuntimeUnverifiable {
			continue
		}
		e := exemptionFor(exemptions, ev.Symbol, ev.RuntimeReason)
		if e == nil {
			return nil, false
		}
		note(e)
	}
	if ev := f.TargetEvidence; ev.RuntimeUnverifiable {
		e := exemptionFor(exemptions, ev.Symbol, ev.RuntimeReason)
		if e == nil {
			for _, oracle := range f.OracleEvidence {
				if !oracle.RuntimeUnverifiable || oracle.RuntimeReason != ev.RuntimeReason {
					continue
				}
				e = exemptionFor(exemptions, oracle.Symbol, ev.RuntimeReason)
				if e != nil {
					break
				}
			}
		}
		if e == nil {
			return nil, false
		}
		note(e)
	}
	if len(matched) == 0 {
		return nil, false
	}
	return matched, true
}

// stampExemptions records the accepted entries on the finding when the
// record covers all of its unverifiable evidence; otherwise the stamp
// clears - a finding's stamp is derived state, never carried past the
// record that justified it.
func stampExemptions(f *Finding, exemptions []Exemption) {
	matched, ok := coveredExemptions(f, exemptions)
	if !ok {
		f.Exempted = nil
		return
	}
	f.Exempted = matched
}

// unstableForBuckets is the survivor-bucketing judgment
// (REQ-exec-survivor-evidence): runtime evidence counts unstable when
// it is unverifiable and the exemption record does not accept it.
func unstableForBuckets(f *Finding, exemptions []Exemption) bool {
	if !f.TargetEvidence.RuntimeUnverifiable {
		return false
	}
	_, ok := coveredExemptions(f, exemptions)
	return !ok
}
