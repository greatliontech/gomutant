package gomutant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/greatliontech/gofresh/runtimeinput"
)

// Exemption is one reviewed entry of the committed exemption record
// (REQ-result-exemptions): a named subject whose runtime evidence is
// accepted as classification-stable under exactly one recorded
// unverifiable reason, with the reviewer's reasoning on the record.
// The record is the live authority - classification consults it on
// every read, so deleting an entry revokes it for every later
// decision - and the matched entries are stamped onto each finding
// they touch, so a reviewer inheriting the repo document sees the
// acceptance beside the evidence it excuses. A subject is identity: a
// retarget rewrites the entries whose subjects the rename moves, with
// the records, and leaves the reason and rationale — the reviewed
// content — untouched; the stamp a record carries names the old
// subject until the next measurement re-derives it
// (REQ-result-lifecycle).
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
	// The record's form is the contract: a key the form does not name,
	// data past the document, or two entries for one subject and reason
	// are refused at load, never carried silently past a rewrite — a
	// second document appended behind the first would otherwise load as
	// the first alone, and a second acceptance for one subject would be
	// dead text behind the first (REQ-result-exemptions,
	// REQ-result-lifecycle).
	var doc exemptionsDocument
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("gomutant: exemption record %s: %w", path, err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("trailing data")
		}
		return nil, fmt.Errorf("gomutant: exemption record %s: %w", path, err)
	}
	if doc.Version != 1 {
		return nil, fmt.Errorf("gomutant: exemption record %s: unsupported version %d", path, doc.Version)
	}
	seen := map[[2]string]int{}
	for i, e := range doc.Exemptions {
		if carriesAttribution(e.Reason) {
			return nil, fmt.Errorf("gomutant: exemption record %s: entry %d names a reason with its attribution %q — the attribution is fresh per measurement; name the clause alone (a refused path itself spelled like an attribution is matched by the clause before it)", path, i, e.Reason[len(reasonClause(e.Reason)):])
		}
		if e.Subject == "" || e.Reason == "" || e.Rationale == "" {
			return nil, fmt.Errorf("gomutant: exemption record %s: entry %d needs subject, reason, and rationale", path, i)
		}
		// Judged after the entry's own refusals, so a malformed pair is
		// named for its own fault before it is named as a pair.
		if prior, dup := seen[[2]string{e.Subject, e.Reason}]; dup {
			return nil, fmt.Errorf("gomutant: exemption record %s: entries %d and %d both accept %s for %q - two acceptances for one subject; delete one", path, prior, i, e.Subject, e.Reason)
		}
		seen[[2]string{e.Subject, e.Reason}] = i
	}
	return doc.Exemptions, nil
}

// writeExemptions writes the committed exemption record whole — version
// 1, the entries in their order, two-space JSON with a trailing newline
// — atomically beside the findings document, keeping the file's mode:
// a retarget rewriting the subjects a rename moved leaves the reviewed
// content as it was, and a torn write never replaces the record
// (REQ-result-exemptions, REQ-result-lifecycle).
func writeExemptions(ctx context.Context, path string, exemptions []Exemption) error {
	data, err := json.MarshalIndent(exemptionsDocument{Version: 1, Exemptions: exemptions}, "", "  ")
	if err != nil {
		return fmt.Errorf("gomutant: encoding exemption record: %w", err)
	}
	mode, err := recordFileMode(path)
	if err != nil {
		return fmt.Errorf("gomutant: exemption record %s: %w", path, err)
	}
	if err := writeRecordFile(ctx, path, append(data, '\n'), mode); err != nil {
		return fmt.Errorf("gomutant: exemption record %s: %w", path, err)
	}
	return nil
}

// exemptionFor returns the entry accepting (subject, reason) exactly,
// or nil. Matching is exact on the subject and on the reason's clause:
// a clause drifting even one byte is a different instability the
// record never reviewed. The producer may append an attribution to a
// clause — a classification refusal's operation, name, and directory,
// or the files that moved a bracket, and when — that is diagnostic
// detail, fresh per measurement, which the clause does not include
// (REQ-result-exemptions).
func exemptionFor(exemptions []Exemption, subject, reason string) *Exemption {
	clause := reasonClause(reason)
	for i := range exemptions {
		if exemptions[i].Subject == subject && exemptions[i].Reason == clause {
			return &exemptions[i]
		}
	}
	return nil
}

// movedBracketClause prefixes the one reason the producer attributes
// in the bracket form: a moved observation bracket, whose trailing
// " [...]" names the members that moved. Every other reason ends in a
// path, and a path may legitimately end in a bracketed segment, so the
// strip is gated on this prefix and touches no other clause.
const movedBracketClause = "observation bracket moved: "

// reasonClause is a recorded reason without the attribution after
// gofresh's separator — a resolved-target refusal's recorded-path
// spelling and target, and the operation, logged name and directory
// a classification refusal carried before gofresh moved its
// attribution off the reason onto the observation — split by
// gofresh's one implementation (runtimeinput.RefusalClause), and the
// moved-bracket clause's trailing bracketed member list, which
// gofresh publishes no split for. The exemption record's readers —
// the match and the dead-acceptance refusal — key on it; the freshness
// judgments of recorded evidence compare a reason whole, the
// attribution included, since there the attribution (a recorded
// path's resolved target) is part of the state being reproduced
// (REQ-result-layers).
func reasonClause(reason string) string {
	return bracketClause(runtimeinput.RefusalClause(reason))
}

// bracketClause strips the moved-bracket clause's trailing bracketed
// attribution, present only on that clause.
func bracketClause(reason string) string {
	if !strings.HasPrefix(reason, movedBracketClause) || !strings.HasSuffix(reason, "]") {
		return reason
	}
	if i := strings.LastIndex(reason, " ["); i > len(movedBracketClause) {
		return reason[:i]
	}
	return reason
}

// carriesAttribution reports whether an entry's reason is a clause
// pasted with its attribution, in either form: such an entry can never
// match (the attribution is fresh per measurement), so the record
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
