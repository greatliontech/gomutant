package gomutant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
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

// RekeyedExemption names one reviewed entry whose clause the load
// re-keyed from a checkout's absolute spelling of an in-module path to
// the module-relative spelling Gofresh gives such paths.
type RekeyedExemption struct {
	Subject string `json:"subject"`
	From    string `json:"from"`
	To      string `json:"to"`
}

// LoadExemptions reads and validates the committed exemption record
// beside the document of the module rooted at moduleDir; a missing
// file is an empty record. Every entry needs its subject, the exact
// recorded reason it accepts, and the reviewer's rationale - an
// unreasoned or reason-free acceptance would be the silent global
// switch the record exists to avoid. An entry whose clause spells an
// in-module path by this checkout's absolute spelling — a record
// authored before Gofresh spelled such paths module-relative — is read
// as the module-relative clause it now matches, and named in the
// second result so a committing verb persists the re-key
// (REQ-result-exemptions).
func LoadExemptions(path, moduleDir string) ([]Exemption, []RekeyedExemption, error) {
	entries, rekeyed, _, err := loadExemptionsFile(path, moduleDir)
	return entries, rekeyed, err
}

// loadExemptionsFile is LoadExemptions returning the file's bytes
// beside the entries — what a committing write compares the file
// against under the document lock before persisting the re-key.
func loadExemptionsFile(path, moduleDir string) ([]Exemption, []RekeyedExemption, []byte, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil, nil, nil
	}
	if err != nil {
		return nil, nil, nil, fmt.Errorf("gomutant: reading exemption record: %w", err)
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
		return nil, nil, nil, fmt.Errorf("gomutant: exemption record %s: %w", path, err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("trailing data")
		}
		return nil, nil, nil, fmt.Errorf("gomutant: exemption record %s: %w", path, err)
	}
	if doc.Version != 1 {
		return nil, nil, nil, fmt.Errorf("gomutant: exemption record %s: unsupported version %d", path, doc.Version)
	}
	roots := moduleRootSpellings(moduleDir)
	var rekeyed []RekeyedExemption
	rekeyedAt := map[int]string{}
	seen := map[[2]string]int{}
	for i := range doc.Exemptions {
		e := &doc.Exemptions[i]
		if carriesAttribution(e.Reason) {
			return nil, nil, nil, fmt.Errorf("gomutant: exemption record %s: entry %d names a reason with its attribution %q — the attribution is fresh per measurement; name the clause alone (a refused path itself spelled like an attribution is matched by the clause before it)", path, i, e.Reason[len(reasonClause(e.Reason)):])
		}
		if e.Subject == "" || e.Reason == "" || e.Rationale == "" {
			return nil, nil, nil, fmt.Errorf("gomutant: exemption record %s: entry %d needs subject, reason, and rationale", path, i)
		}
		// The re-key precedes the pair check: two entries spelling one
		// in-module path two ways are one acceptance, named as a pair.
		if to, ok := rekeyClause(e.Reason, roots); ok {
			rekeyed = append(rekeyed, RekeyedExemption{Subject: e.Subject, From: e.Reason, To: to})
			rekeyedAt[i] = e.Reason
			e.Reason = to
		}
		// Judged after the entry's own refusals, so a malformed pair is
		// named for its own fault before it is named as a pair — and
		// after the re-key, naming the spelling the re-key folded.
		if prior, dup := seen[[2]string{e.Subject, e.Reason}]; dup {
			return nil, nil, nil, fmt.Errorf("gomutant: exemption record %s: entries %d and %d both accept %s for %q%s - two acceptances for one subject; delete one", path, prior, i, e.Subject, e.Reason, rekeyedNote(rekeyedAt, prior, i))
		}
		seen[[2]string{e.Subject, e.Reason}] = i
	}
	return doc.Exemptions, rekeyed, data, nil
}

// rekeyedNote names, for a refused pair, the spelling the re-key
// folded onto the clause — the file shows two strings where the pair
// refusal names one.
func rekeyedNote(rekeyedAt map[int]string, prior, i int) string {
	var parts []string
	for _, n := range []int{prior, i} {
		if from, ok := rekeyedAt[n]; ok {
			parts = append(parts, fmt.Sprintf("entry %d re-keyed from %q", n, from))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, "; ") + ")"
}

// moduleRootSpellings is every absolute spelling an in-module path
// could have carried in a clause authored before Gofresh spelled such
// paths module-relative: THIS checkout's module directory as given,
// cleaned, and its symlink-resolved form where that differs (the
// identity's path was the process's own spelling, resolved or not). A
// record authored under another checkout's root spells paths this one
// never had — indistinguishable from an out-of-module path, not
// derivable, left as it is (the recorded residual).
func moduleRootSpellings(moduleDir string) []string {
	clean := filepath.Clean(moduleDir)
	roots := []string{clean}
	if resolved, err := filepath.EvalSymlinks(clean); err == nil && resolved != clean {
		roots = append(roots, resolved)
	}
	return roots
}

// coverageEscapeOpen opens the one parenthetical a Gofresh clause
// carries a second path in: the bracket-coverage refusal names the
// escaping link after its path.
const coverageEscapeOpen = " (symlink outside every bracket root: "

// rekeyClause rewrites the in-module paths a clause spells by one of
// the root spellings to Gofresh's module-relative spelling — by the
// composers' grammar, never by searching the clause's text: every
// composer ends its clause with ": " and the path, whole (a member's
// display/rel spelling and a moved bracket's root included), and the
// bracket-coverage refusal alone closes with " (symlink outside every
// bracket root: <path>)" — so the paths are the text after the last
// ": " of the clause's head and the parenthetical's own. A whole path
// equal to a root becomes ".", one under a root its slash-relative
// remainder; any other path — an out-of-module one carrying the root's
// text inside it, a sibling directory whose name extends the root's —
// is left as it is. A path carrying ": " itself reads as its tail,
// never under a root: nothing is rewritten (the fail-safe residual).
// ok reports a rewrite.
func rekeyClause(clause string, roots []string) (string, bool) {
	head, escaped, hasEscape := clause, "", false
	if strings.HasSuffix(clause, ")") {
		if i := strings.LastIndex(clause, coverageEscapeOpen); i >= 0 {
			head, escaped, hasEscape = clause[:i], clause[i+len(coverageEscapeOpen):len(clause)-1], true
		}
	}
	i := strings.LastIndex(head, ": ")
	if i < 0 {
		return clause, false
	}
	out := head[:i+2] + relativized(head[i+2:], roots)
	if hasEscape {
		out += coverageEscapeOpen + relativized(escaped, roots) + ")"
	}
	return out, out != clause
}

// relativized is path under Gofresh's module-relative spelling when it
// is one of the roots or lies under one, else path itself. A member
// whose name carries bytes Gofresh quotes (strconv's Go-quoted form,
// for a non-UTF-8 or control-bearing name) is unquoted, relativized
// and quoted again.
func relativized(path string, roots []string) string {
	if strings.HasPrefix(path, `"`) {
		if inner, err := strconv.Unquote(path); err == nil {
			if rel := relativized(inner, roots); rel != inner {
				return strconv.Quote(rel)
			}
			return path
		}
	}
	for _, root := range roots {
		if root == "" {
			continue
		}
		if path == root {
			return "."
		}
		if len(path) > len(root) && strings.HasPrefix(path, root) && (path[len(root)] == filepath.Separator || path[len(root)] == '/') {
			return filepath.ToSlash(path[len(root)+1:])
		}
	}
	return path
}

// rekeyedExemptionRoster bounds the re-keyed entries a face lists;
// the count leads and the remainder is counted (REQ-mcp-envelope's
// row rule on the CLI's side too).
const rekeyedExemptionRoster = 20

// RekeyedExemptionsLine is the one line both faces print when a
// committing verb persisted the re-key of the exemption record; empty
// when nothing was re-keyed (REQ-result-exemptions).
func RekeyedExemptionsLine(rekeyed []RekeyedExemption) string {
	n := len(rekeyed)
	if n == 0 {
		return ""
	}
	shown := rekeyed
	more := ""
	if n > rekeyedExemptionRoster {
		shown = shown[:rekeyedExemptionRoster]
		more = fmt.Sprintf(" (+%d more)", n-rekeyedExemptionRoster)
	}
	moves := make([]string, 0, len(shown))
	for _, e := range shown {
		moves = append(moves, fmt.Sprintf("%s %q -> %q", e.Subject, e.From, e.To))
	}
	return fmt.Sprintf("re-keyed %d reviewed exemption clause(s) to the module-relative spelling: %s%s", n, strings.Join(moves, ", "), more)
}

// writeExemptions writes the committed exemption record whole — version
// 1, the entries in their order, two-space JSON with a trailing newline
// — atomically beside the findings document, keeping the file's mode:
// a retarget rewriting the subjects a rename moved leaves the reviewed
// content as it was, and a torn write never replaces the record
// (REQ-result-exemptions, REQ-result-lifecycle).
func writeExemptions(ctx context.Context, path string, exemptions []Exemption) ([]byte, error) {
	data, err := json.MarshalIndent(exemptionsDocument{Version: 1, Exemptions: exemptions}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("gomutant: encoding exemption record: %w", err)
	}
	mode, err := recordFileMode(path)
	if err != nil {
		return nil, fmt.Errorf("gomutant: exemption record %s: %w", path, err)
	}
	data = append(data, '\n')
	if err := writeRecordFile(ctx, path, data, mode); err != nil {
		return nil, fmt.Errorf("gomutant: exemption record %s: %w", path, err)
	}
	return data, nil
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

// reasonClause is a recorded reason without the attribution after
// Gofresh's separator — a resolved-target refusal's recorded-path
// spelling and target, the operation, logged name and directory a
// classification refusal carried before Gofresh moved its attribution
// off the reason onto the observation, and the moved-bracket clause's
// trailing bracketed member list — split by Gofresh's one
// implementation (runtimeinput.RefusalClause; a root name itself
// carrying a bracketed segment keeps it unless the segment, read with
// what follows it to the reason's end, parses as a labelled member
// list — Gofresh's split is prefix-first, and a root spelled so
// collides with the root before the segment).
// The exemption record's readers — the match and the dead-acceptance
// refusal — key on it; the freshness judgments of
// recorded evidence compare a reason whole, the attribution included,
// since there the attribution (a recorded path's resolved target) is
// part of the state being reproduced (REQ-result-layers).
func reasonClause(reason string) string {
	return runtimeinput.RefusalClause(reason)
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
