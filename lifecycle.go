package gomutant

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/greatliontech/gomutant/internal/engine"
)

// PrunedRecord is one resolved-dead record a prune removed (or would
// remove, under check) from the layer it sat in — a symbol held in
// both layers is two records — its dispositions echoed so the
// reasoning survives the removal - promote-then-delete, never a silent
// drop (REQ-result-lifecycle).
type PrunedRecord struct {
	Symbol string
	// Layer is where the record sat: LayerRepo or LayerLocal.
	Layer    string
	Attested []Attestation
}

// LayerCounts counts records per layer of REQ-result-layers: Repo the
// committed document's rows, Local the machine-local overlay's — a
// symbol held in both layers is two records, counted once each.
type LayerCounts struct {
	Repo  int
	Local int
}

// add counts one record in its layer.
func (c *LayerCounts) add(layer string) {
	if layer == LayerLocal {
		c.Local++
		return
	}
	c.Repo++
}

// Total is the count over both layers.
func (c LayerCounts) Total() int { return c.Repo + c.Local }

// PruneResult reports a prune's dispositions: Removed the records
// removed, each naming its layer, and Kept the records kept, counted
// per layer (REQ-result-lifecycle).
type PruneResult struct {
	Removed []PrunedRecord
	Kept    LayerCounts
	Check   bool
	// ExemptionsRekeyed names the reviewed exemption entries the write
	// re-keyed to the module-relative spelling (REQ-result-exemptions).
	ExemptionsRekeyed []RekeyedExemption
}

// PruneDetachedContext removes every record whose mutated symbol no
// current declaration resolves - the terminal records no re-measure can
// revive (REQ-result-lifecycle). The declared-symbol snapshot comes
// from the loaded tree, so a tree that fails to load never reaches
// here: a load failure is indistinguishable from a rename at the
// symbol layer, and pruning on it would destroy live records. Every
// stored record is judged in its own layer, so a detached symbol
// leaves the findings document and the machine-local overlay alike and
// each removal names its layer; the write is exactly the removals —
// a document none of whose rows left is not rewritten. Under check the
// store is untouched and the result previews the removals.
func (t *Tree) PruneDetachedContext(ctx context.Context, store *Store, check bool) (PruneResult, error) {
	// A package with load errors "loads" with its declarations silently
	// missing from the partial syntax; judging absence there would
	// destroy live records, so an unhealthy load refuses
	// (REQ-result-lifecycle).
	if err := t.eng.PackagesHealthyContext(ctx); err != nil {
		return PruneResult{}, fmt.Errorf("prune refuses: %w", err)
	}
	declared, err := t.eng.DeclaredSymbolsContext(ctx)
	if err != nil {
		return PruneResult{}, err
	}
	resolves := func(symbol string) bool {
		i := sort.SearchStrings(declared, symbol)
		return i < len(declared) && declared[i] == symbol
	}
	result := PruneResult{Check: check}
	decide := func(layer string, f Finding) (Finding, bool, error) {
		// A shaped finding's identity is declared, never a resolvable
		// symbol: absence from the declaration set is its normal
		// state, so prune keeps it — retirement is the caller's
		// explicit edit of the target set and document
		// (REQ-target-structural, REQ-target-manual-recipes).
		if f.Shape != nil || resolves(f.Symbol) {
			result.Kept.add(layer)
			return f, true, nil
		}
		result.Removed = append(result.Removed, PrunedRecord{Symbol: f.Symbol, Layer: layer, Attested: append([]Attestation(nil), f.AttestedDispositions()...)})
		return f, false, nil
	}
	if _, err := store.Revise(ctx, check, decide); err != nil {
		return PruneResult{}, err
	}
	result.ExemptionsRekeyed = store.RekeyedExemptions()
	return result, nil
}

// RetargetedRecord names one symbol rewrite a retarget performed (or
// previews, under check) on one stored record — a symbol held in both
// layers is two records, each rewritten in its own layer.
type RetargetedRecord struct {
	From string
	To   string
	// Layer is where the record sits: LayerRepo or LayerLocal.
	Layer string
	// Shadowed reports a rewritten document row whose new symbol the
	// machine-local overlay holds once the retarget has written: every
	// read serves the overlay's record for it until that entry leaves
	// (REQ-result-layers).
	Shadowed bool
}

// TouchedRewrite names one evidence-symbol or kill-attribution rewrite
// on a record whose own mutated symbol stayed put - the surfaces no
// resolution gate reaches, echoed so a check preview shows exactly
// what would change (REQ-result-lifecycle).
type TouchedRewrite struct {
	Record string
	// Layer is where the record sits: LayerRepo or LayerLocal.
	Layer string
	From  string
	To    string
}

// RewrittenExemption names one reviewed exemption entry whose subject
// the rename moved: From the subject as reviewed, To its rewritten
// spelling; the reason and rationale stay as reviewed.
type RewrittenExemption struct {
	From string
	To   string
}

// RetargetResult reports a retarget's rewrites: Rewritten names the
// records whose mutated symbol changed; Touched counts, per layer, the
// records the rename's closure updated without renaming their own
// symbol (an oracle or killer in the renamed surface), and
// TouchedRewrites lists those moves; Exemptions names the reviewed
// exemption entries whose subjects the rename moved, rewritten with the
// records (REQ-result-lifecycle).
type RetargetResult struct {
	Rewritten       []RetargetedRecord
	RewrittenCounts LayerCounts
	Touched         LayerCounts
	TouchedRewrites []TouchedRewrite
	// Exemptions names the reviewed exemption entries whose subjects the
	// rename moved, rewritten with the records (the same under check,
	// where nothing is written).
	Exemptions []RewrittenExemption
	Check      bool
	// ExemptionsRekeyed names the reviewed entries the write re-keyed to
	// the module-relative spelling (REQ-result-exemptions).
	ExemptionsRekeyed []RekeyedExemption
}

// retargetSymbol rewrites one symbol identity under the prefix pair;
// ok reports whether the prefix applied. A prefix applies only at a
// segment boundary - the whole identity, a prefix ending in a
// separator, or a match whose next byte is one - so a rename of
// example.com/old never rewrites example.com/oldtime. A "." at the
// edge of a bare prefix refuses instead: it may open the named
// package's local half or continue a dotted sibling package (lib vs
// lib.v2), the split is not lexically recoverable, and a guess would
// write a corrupted identity durably (REQ-result-lifecycle).
func retargetSymbol(symbol, from, to string) (string, bool, error) {
	if !strings.HasPrefix(symbol, from) {
		return symbol, false, nil
	}
	boundary, ambiguous := boundaryAfter(symbol, from)
	if ambiguous {
		return symbol, false, fmt.Errorf("retarget: %s matches %s at a '.' edge, which may open the package's local half or continue a dotted sibling package - terminate both prefixes with '.' to claim the package exactly (subpackages then take a second '/'-terminated pass), or give the full symbol pair", symbol, from)
	}
	if !boundary {
		return symbol, false, nil
	}
	return to + symbol[len(from):], true, nil
}

// boundaryAfter reports whether the prefix match of prefix against s
// ends at a segment boundary: identity separators are "/" (package
// path) and "." (package-to-local and method). A "." edge after a
// bare prefix reports ambiguous, never boundary - the caller decides
// (in the package/symbol space it refuses; in the local space, where
// identifiers cannot contain ".", it is a true boundary).
func boundaryAfter(s, prefix string) (boundary, ambiguous bool) {
	if len(s) == len(prefix) {
		return true, false
	}
	if n := len(prefix); n > 0 && (prefix[n-1] == '.' || prefix[n-1] == '/') {
		return true, false
	}
	switch s[len(prefix)] {
	case '/':
		return true, false
	case '.':
		return false, true
	}
	return false, false
}

// retargetPrefixParts splits a symbol-prefix pair into its package-path
// and local-name projections: "example.com/old." and "example.com/old"
// both project to the package prefix "example.com/old" with no local
// part, while "example.com/x.Old" projects to package "example.com/x"
// and local prefix "Old" - the observation-subject identity stores the
// two halves separately and each rewrites under its own projection.
// A trailing "." marks the WHOLE prefix as a package claim - the
// package half is everything before it - so a dotted final segment
// (gopkg.in/mylib.v2.) projects by its true boundary instead of the
// first dot.
func retargetPrefixParts(prefix string) (pkg, local string) {
	if strings.HasSuffix(prefix, ".") {
		return prefix[:len(prefix)-1], ""
	}
	slash := strings.LastIndex(prefix, "/")
	dot := strings.Index(prefix[slash+1:], ".")
	if dot < 0 {
		// A trailing separator marks the boundary but is no part of the
		// package path - kept, it would strand the path projection,
		// which matches at "/" boundaries.
		return strings.TrimSuffix(prefix, "/"), ""
	}
	return prefix[:slash+1+dot], prefix[slash+1+dot+1:]
}

// retargetPackagePath rewrites a package path under the pair's package
// projection, matching at a path boundary only.
func retargetPackagePath(path, fromPkg, toPkg string) (string, bool) {
	if path == fromPkg {
		return toPkg, true
	}
	if strings.HasPrefix(path, fromPkg+"/") {
		return toPkg + path[len(fromPkg):], true
	}
	return path, false
}

// retargetFinding rewrites every symbol-bearing field of one record
// under the prefix pair. Attestations and survivors anchor on position,
// operator, and site content - never symbol text - so they ride a
// retarget unchanged (REQ-attest-survivor, REQ-result-lifecycle).
// symbolChanged reports whether the record's own mutated symbol
// rewrote; touched whether any field did; moves lists every
// evidence-symbol and kill-attribution rewrite so the caller can echo
// the surfaces no resolution gate reaches. An ambiguous match anywhere
// in the record surfaces as an error - the caller refuses whole.
func retargetFinding(f Finding, from, to string) (rewritten Finding, symbolChanged, touched bool, moves []TouchedRewrite, err error) {
	changed := false
	record := f.Symbol
	rewrite := func(symbol string) string {
		if err != nil {
			return symbol
		}
		next, ok, rerr := retargetSymbol(symbol, from, to)
		if rerr != nil {
			err = rerr
			return symbol
		}
		changed = changed || ok
		return next
	}
	fromPkg, fromLocal := retargetPrefixParts(from)
	toPkg, toLocal := retargetPrefixParts(to)
	before := f.Symbol
	f.Symbol = rewrite(f.Symbol)
	evidence := func(e SubjectEvidence) SubjectEvidence {
		next := rewrite(e.Symbol)
		symbolRewrote := next != e.Symbol
		// The evidence stores its subject's package authoritatively, and
		// the lexical match must agree with that stored split. Where the
		// stored package matches the pair's package projection, the
		// projections rewrite the halves; where the pair instead crosses
		// into the local half of exactly the stored package - a dotted
		// final segment (lib.v2) defeats the lexical projection - the
		// halves derive from the stored fact. Anything else has crossed
		// a dotted package boundary the string cannot express: refuse
		// rather than write the corruption (REQ-result-lifecycle).
		if symbolRewrote && err == nil {
			storedPkg := e.ObservationProof.Subject.Package
			switch {
			case storedPkg == "" || storedPkg == fromPkg || strings.HasPrefix(storedPkg, fromPkg+"/"):
				if pkgNext, ok := retargetPackagePath(storedPkg, fromPkg, toPkg); ok {
					e.ObservationProof.Subject.Package = pkgNext
				}
				if fromLocal != "" && strings.HasPrefix(e.ObservationProof.Subject.Symbol, fromLocal) {
					// In the local half "." is unambiguous - identifiers
					// cannot contain it - so a dot edge is a true boundary.
					if b, dot := boundaryAfter(e.ObservationProof.Subject.Symbol, fromLocal); b || dot {
						e.ObservationProof.Subject.Symbol = toLocal + e.ObservationProof.Subject.Symbol[len(fromLocal):]
					}
				}
			// A dot-terminated from equal to storedPkg+"." projects to
			// fromPkg == storedPkg and is already caught above, so this
			// arm only sees pairs crossing into the local half proper.
			case strings.HasPrefix(from, storedPkg+"."):
				if !strings.HasPrefix(to, storedPkg+".") {
					err = fmt.Errorf("retarget: %s moves out of its recorded package %s - a cross-package symbol move cannot carry the observation identity; retarget the package and the local name separately", e.Symbol, storedPkg)
					return e
				}
				localFrom, localTo := from[len(storedPkg)+1:], to[len(storedPkg)+1:]
				if localFrom != "" && strings.HasPrefix(e.ObservationProof.Subject.Symbol, localFrom) {
					if b, dot := boundaryAfter(e.ObservationProof.Subject.Symbol, localFrom); b || dot {
						e.ObservationProof.Subject.Symbol = localTo + e.ObservationProof.Subject.Symbol[len(localFrom):]
					}
				}
			default:
				err = fmt.Errorf("retarget: evidence for %s records package %s, which %q does not name - the prefix matches across a package boundary", e.Symbol, storedPkg, from)
				return e
			}
		}
		if err != nil {
			return e
		}
		if symbolRewrote {
			moves = append(moves, TouchedRewrite{Record: record, From: e.Symbol, To: next})
		}
		e.Symbol = next
		return e
	}
	f.TargetEvidence = evidence(f.TargetEvidence)
	oracle := append([]SubjectEvidence(nil), f.OracleEvidence...)
	for i := range oracle {
		oracle[i] = evidence(oracle[i])
	}
	f.OracleEvidence = oracle
	kills := append([]Kill(nil), f.Kills...)
	for i := range kills {
		// A probe-confirmed package failure attributes to a package,
		// not a symbol: under a package-shaped pair the embedded path
		// rewrites with the same projection, or the attribution would
		// keep the dead path (REQ-result-lifecycle). A symbol-shaped
		// pair renames no package, so the sentinel stands.
		if inner, ok := strings.CutPrefix(kills[i].Killer, engine.PackageKillerPrefix); ok {
			if fromLocal == "" && toLocal == "" && strings.HasSuffix(inner, ")") {
				if next, ok := retargetPackagePath(strings.TrimSuffix(inner, ")"), fromPkg, toPkg); ok {
					moves = append(moves, TouchedRewrite{Record: record, From: kills[i].Killer, To: engine.PackageKillerPrefix + next + ")"})
					kills[i].Killer = engine.PackageKillerPrefix + next + ")"
					changed = true
				}
			}
			continue
		}
		next := rewrite(kills[i].Killer)
		if next != kills[i].Killer {
			moves = append(moves, TouchedRewrite{Record: record, From: kills[i].Killer, To: next})
		}
		kills[i].Killer = next
	}
	f.Kills = kills
	if err != nil {
		return Finding{}, false, false, nil, err
	}
	return f, f.Symbol != before, changed, moves, nil
}

// RetargetContext rewrites symbol identity across a rename: every
// record whose symbol-bearing fields carry the from prefix is rewritten
// to the to prefix, so surviving attestations follow their mutants by
// their own anchors instead of dying detached (REQ-result-lifecycle).
// Each rewritten target symbol must resolve in the current tree - a
// retarget follows a rename that happened. Every stored record is
// rewritten in its own layer — a rename never re-judges the layer a
// measuring write placed a record in — and a rewrite collides only
// within a layer (the overlay shadowing the document is no collision),
// refusing whole before any write. A retarget rewrites identity only:
// the measured facts a record carries — runtime-input manifests,
// compartment ledgers, exemption stamps — stay as measured. The
// reviewed exemption entries whose subjects the pair moves are
// rewritten with the records, after them and never under check — a
// subject is identity, the reason and rationale the reviewed content;
// a rewrite that would give two entries one subject and reason refuses
// whole before any write, naming both, as a record collision does; an
// entry's rewritten subject need not resolve in the tree (an entry no
// record names is inert either way). Between the two writes a reader
// sees rewritten records beside the entries as reviewed; that reader's
// judgments self-heal at its next read, and a write failing between
// them is named and recovered by a rerun (REQ-result-exemptions,
// REQ-result-lifecycle). Under check the store is untouched and the
// result previews the rewrites.
func (t *Tree) RetargetContext(ctx context.Context, store *Store, from, to string, check bool) (RetargetResult, error) {
	// The pair's shape is decided before any load by the faces; the
	// library entry keeps the same judgment for callers that skipped it.
	if err := ValidateRetargetPair(from, to); err != nil {
		return RetargetResult{}, err
	}
	declared, err := t.eng.DeclaredSymbolsContext(ctx)
	if err != nil {
		return RetargetResult{}, err
	}
	resolves := func(symbol string) bool {
		i := sort.SearchStrings(declared, symbol)
		return i < len(declared) && declared[i] == symbol
	}
	var result RetargetResult
	// The entries as they would be rewritten, judged before any write
	// over the entries loaded (the preview's listing); a committing
	// retarget derives them again under the lock over the entries in
	// force — the record re-read there — so a revocation made since the
	// open is never written back.
	_, result.Exemptions, err = rewrittenExemptionEntries(store.exemptions, from, to)
	if err != nil {
		return RetargetResult{}, err
	}
	result.Check = check
	decide := func(layer string, f Finding) (Finding, bool, error) {
		rewritten, symbolChanged, touched, moves, err := retargetFinding(f, from, to)
		if err != nil {
			return Finding{}, false, err
		}
		switch {
		case symbolChanged:
			// Only a record whose own symbol rewrote owes resolution:
			// the rename it follows must have happened
			// (REQ-result-lifecycle).
			if !resolves(rewritten.Symbol) {
				return Finding{}, false, fmt.Errorf("retarget: %s does not resolve in the current tree - a retarget follows a rename that happened", rewritten.Symbol)
			}
			result.Rewritten = append(result.Rewritten, RetargetedRecord{From: f.Symbol, To: rewritten.Symbol, Layer: layer})
			result.RewrittenCounts.add(layer)
		case touched:
			result.Touched.add(layer)
			for i := range moves {
				moves[i].Layer = layer
			}
			result.TouchedRewrites = append(result.TouchedRewrites, moves...)
		}
		return rewritten, true, nil
	}
	revision, err := store.ReviseExemptions(ctx, check, decide, func(entries []Exemption) ([]Exemption, []RewrittenExemption, error) {
		return rewrittenExemptionEntries(entries, from, to)
	})
	if err != nil {
		return RetargetResult{}, fmt.Errorf("retarget: %w", err)
	}
	// A document row is shadowed when the overlay holds its new symbol
	// once the edits are applied — the revision's own account, whatever
	// the entries were before. Said, not silent: every read serves the
	// overlay's record until it leaves.
	for i := range result.Rewritten {
		r := &result.Rewritten[i]
		r.Shadowed = r.Layer == LayerRepo && revision.Overlay[r.To]
	}
	// The reviewed entries whose subjects the rename moved were written
	// with the records, under the lock, ahead of them — the revision's
	// own listing; the preview's listing above is the derivation over
	// the entries loaded (REQ-result-lifecycle, REQ-result-exemptions).
	if !check {
		result.Exemptions = revision.Exemptions
	}
	result.ExemptionsRekeyed = store.RekeyedExemptions()
	return result, nil
}

// rewrittenExemptionEntries is the exemption record as a retarget
// rewrites it over entries: every subject the prefix pair moves
// rewritten, listed; two entries meeting on one subject and reason
// would leave one acceptance's rationale dead text behind the other —
// a collision refused whole, naming both, as a record collision is
// (the loaded record carries no such pair — the load refuses it — so a
// collision here is one the rewrite makes: a moved entry meeting one
// the pair leaves alone) (REQ-result-lifecycle, REQ-result-exemptions).
func rewrittenExemptionEntries(entries []Exemption, from, to string) ([]Exemption, []RewrittenExemption, error) {
	moved, err := movedExemptionSubjects(entries, from, to)
	if err != nil {
		return nil, nil, err
	}
	rewritten := make([]Exemption, 0, len(entries))
	var listed []RewrittenExemption
	seen := map[[2]string]string{}
	for _, e := range entries {
		reviewed := e.Subject
		if next, ok := moved[e.Subject]; ok {
			listed = append(listed, RewrittenExemption{From: e.Subject, To: next})
			e.Subject = next
		}
		key := [2]string{e.Subject, e.Reason}
		if prior, dup := seen[key]; dup {
			return nil, nil, fmt.Errorf("retarget: the exemption record would carry %s twice for %q (from %s and %s) - two acceptances for one subject; rewrite or delete one first", e.Subject, e.Reason, prior, reviewed)
		}
		seen[key] = reviewed
		rewritten = append(rewritten, e)
	}
	return rewritten, listed, nil
}

// movedExemptionSubjects maps each reviewed exemption subject the
// prefix pair rewrites to its rewritten spelling; a subject the pair
// cannot rewrite unambiguously refuses, attributed to the record.
func movedExemptionSubjects(exemptions []Exemption, from, to string) (map[string]string, error) {
	moved := map[string]string{}
	for _, e := range exemptions {
		next, ok, err := retargetSymbol(e.Subject, from, to)
		if err != nil {
			return nil, fmt.Errorf("exemption record entry for %s: %w", e.Subject, err)
		}
		if ok {
			moved[e.Subject] = next
		}
	}
	return moved, nil
}
