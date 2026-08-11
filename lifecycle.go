package gomutant

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/greatliontech/gomutant/internal/engine"
)

// PrunedRecord is one resolved-dead record a prune removed (or would
// remove, under check), its dispositions echoed so the reasoning
// survives the removal - promote-then-delete, never a silent drop
// (REQ-result-lifecycle).
type PrunedRecord struct {
	Symbol   string
	Attested []Attestation
}

// PruneResult reports a prune's dispositions.
type PruneResult struct {
	Removed []PrunedRecord
	Kept    int
	Check   bool
}

// PruneDetachedContext removes every record whose mutated symbol no
// current declaration resolves - the terminal records no re-measure can
// revive (REQ-result-lifecycle). The declared-symbol snapshot comes
// from the loaded tree, so a tree that fails to load never reaches
// here: a load failure is indistinguishable from a rename at the
// symbol layer, and pruning on it would destroy live records. Under
// check the store is untouched and the result previews the removals.
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
	decide := func(all []Finding) []Finding {
		kept := all[:0:0]
		for _, f := range all {
			if resolves(f.Symbol) {
				kept = append(kept, f)
				continue
			}
			result.Removed = append(result.Removed, PrunedRecord{Symbol: f.Symbol, Attested: append([]Attestation(nil), f.AttestedDispositions()...)})
		}
		result.Kept = len(kept)
		return kept
	}
	if check {
		all, err := store.Load(ctx)
		if err != nil {
			return PruneResult{}, err
		}
		decide(all)
		return result, nil
	}
	if err := store.Update(ctx, func(all []Finding) ([]Finding, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return decide(all), nil
	}); err != nil {
		return PruneResult{}, err
	}
	return result, nil
}

// RetargetedRecord names one symbol rewrite a retarget performed (or
// previews, under check).
type RetargetedRecord struct {
	From string
	To   string
}

// TouchedRewrite names one evidence-symbol or kill-attribution rewrite
// on a record whose own mutated symbol stayed put - the surfaces no
// resolution gate reaches, echoed so a check preview shows exactly
// what would change (REQ-result-lifecycle).
type TouchedRewrite struct {
	Record string
	From   string
	To     string
}

// RetargetResult reports a retarget's rewrites: Rewritten names the
// records whose mutated symbol changed; Touched counts records the
// rename's closure updated without renaming their own symbol (an
// oracle or killer in the renamed surface), and TouchedRewrites lists
// those records' field rewrites - a rewritten record's evidence rides
// its own rename and is not repeated there.
type RetargetResult struct {
	Rewritten       []RetargetedRecord
	Touched         int
	TouchedRewrites []TouchedRewrite
	Check           bool
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
			storedPkg := e.ObservationSubjectPackage
			switch {
			case storedPkg == "" || storedPkg == fromPkg || strings.HasPrefix(storedPkg, fromPkg+"/"):
				if pkgNext, ok := retargetPackagePath(storedPkg, fromPkg, toPkg); ok {
					e.ObservationSubjectPackage = pkgNext
				}
				if fromLocal != "" && strings.HasPrefix(e.ObservationSubjectSymbol, fromLocal) {
					// In the local half "." is unambiguous - identifiers
					// cannot contain it - so a dot edge is a true boundary.
					if b, dot := boundaryAfter(e.ObservationSubjectSymbol, fromLocal); b || dot {
						e.ObservationSubjectSymbol = toLocal + e.ObservationSubjectSymbol[len(fromLocal):]
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
				if localFrom != "" && strings.HasPrefix(e.ObservationSubjectSymbol, localFrom) {
					if b, dot := boundaryAfter(e.ObservationSubjectSymbol, localFrom); b || dot {
						e.ObservationSubjectSymbol = localTo + e.ObservationSubjectSymbol[len(localFrom):]
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
// retarget follows a rename that happened - and a rewrite colliding
// with an existing record refuses whole; under check the store is
// untouched and the result previews the rewrites.
func (t *Tree) RetargetContext(ctx context.Context, store *Store, from, to string, check bool) (RetargetResult, error) {
	if from == "" || to == "" {
		return RetargetResult{}, fmt.Errorf("retarget needs a non-empty from and to prefix")
	}
	if from == to {
		return RetargetResult{}, fmt.Errorf("retarget needs distinct prefixes - from and to are both %q", from)
	}
	// The observation-subject halves rewrite under independent
	// projections of the pair; a structurally asymmetric pair (one half
	// package-shaped, the other symbol-shaped) would mangle the local
	// half into an identity that names nothing, durably
	// (REQ-result-lifecycle).
	fromPkg, fromLocal := retargetPrefixParts(from)
	toPkg, toLocal := retargetPrefixParts(to)
	if (fromLocal == "") != (toLocal == "") {
		return RetargetResult{}, fmt.Errorf("retarget prefixes must be structurally alike - %q and %q split package and symbol differently; use a package pair or a full-symbol pair", from, to)
	}
	// A symbol pair renames within its package: the destination carries
	// no stored fact to validate a package move against, and a dotted
	// destination remainder may continue a package instead of naming a
	// local - so the package halves must agree and the local halves map
	// segment for segment (REQ-result-lifecycle).
	if fromLocal != "" {
		if fromPkg != toPkg {
			return RetargetResult{}, fmt.Errorf("retarget: a symbol pair renames within its package - %q and %q name different packages; move a surface across packages with a package pair", from, to)
		}
		if strings.Count(fromLocal, ".") != strings.Count(toLocal, ".") {
			return RetargetResult{}, fmt.Errorf("retarget: %q -> %q restructures the local name - a rename maps segments one to one, and a dotted remainder may continue a package instead of naming a local; a package move takes a package pair, and a promotion or demotion that reshapes the name re-measures under the new shape", from, to)
		}
	}
	// Unlike-terminated pairs splice across unlike edges: with from
	// "example.com/old." and to "example.com/new", the matched dot is
	// consumed and never re-emitted, writing example.com/newTestF
	// durably. The terminator is part of the claim - both prefixes
	// carry the same one, or neither (REQ-result-lifecycle).
	terminator := func(p string) byte {
		if c := p[len(p)-1]; c == '.' || c == '/' {
			return c
		}
		return 0
	}
	if terminator(from) != terminator(to) {
		return RetargetResult{}, fmt.Errorf("retarget prefixes must be like-terminated - %q and %q end differently, and splicing across unlike edges corrupts identities; terminate both with the same separator or neither", from, to)
	}
	declared, err := t.eng.DeclaredSymbolsContext(ctx)
	if err != nil {
		return RetargetResult{}, err
	}
	resolves := func(symbol string) bool {
		i := sort.SearchStrings(declared, symbol)
		return i < len(declared) && declared[i] == symbol
	}
	result := RetargetResult{Check: check}
	decide := func(all []Finding) ([]Finding, error) {
		next := make([]Finding, 0, len(all))
		seen := make(map[string]bool, len(all))
		for _, f := range all {
			rewritten, symbolChanged, touched, moves, err := retargetFinding(f, from, to)
			if err != nil {
				return nil, err
			}
			switch {
			case symbolChanged:
				// Only a record whose own symbol rewrote owes resolution:
				// the rename it follows must have happened
				// (REQ-result-lifecycle).
				if !resolves(rewritten.Symbol) {
					return nil, fmt.Errorf("retarget: %s does not resolve in the current tree - a retarget follows a rename that happened", rewritten.Symbol)
				}
				result.Rewritten = append(result.Rewritten, RetargetedRecord{From: f.Symbol, To: rewritten.Symbol})
			case touched:
				result.Touched++
				result.TouchedRewrites = append(result.TouchedRewrites, moves...)
			}
			if seen[rewritten.Symbol] {
				return nil, fmt.Errorf("retarget: %s collides with an existing record", rewritten.Symbol)
			}
			seen[rewritten.Symbol] = true
			next = append(next, rewritten)
		}
		return next, nil
	}
	if check {
		all, err := store.Load(ctx)
		if err != nil {
			return RetargetResult{}, err
		}
		if _, err := decide(all); err != nil {
			return RetargetResult{}, err
		}
		return result, nil
	}
	if err := store.Update(ctx, func(all []Finding) ([]Finding, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return decide(all)
	}); err != nil {
		return RetargetResult{}, err
	}
	return result, nil
}
