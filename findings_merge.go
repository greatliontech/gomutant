package gomutant

import (
	"context"
	"fmt"
	"os"
	"sort"

	"github.com/greatliontech/gomutant/internal/contextio"
)

// RenderedFindings substitutes each run finding with its post-merge
// document row where one landed, preserving the run-only Cached and
// Skipped markers: what a run surface renders is what the document
// holds (REQ-mcp-findings-doc).
func RenderedFindings(findings []Finding, postMerge map[string]Finding) []Finding {
	rendered := make([]Finding, len(findings))
	for i, f := range findings {
		if m, ok := postMerge[f.recordKeyString()]; ok {
			m.Cached, m.Skipped, m.Unreached = f.Cached, f.Skipped, f.Unreached
			rendered[i] = m
		} else if f.Selection == "" {
			if m, ok := postMerge[f.Symbol]; ok {
				m.Cached, m.Skipped, m.Unreached = f.Cached, f.Skipped, f.Unreached
				rendered[i] = m
				continue
			}
			rendered[i] = f
		} else {
			rendered[i] = f
		}
	}
	return rendered
}

// AnnotateSelectionVerdicts populates each survivor's OtherSelections with
// the advisory verdicts recorded for the same mutant in coexisting records of
// the same symbol under other build selections whose mutation domain
// (BodyHash, OperatorSet, and Site) and oracleExecutionPolicy hold
// (REQ-result-record, REQ-result-findings).
func AnnotateSelectionVerdicts(findings []Finding) []Finding {
	bySymbol := map[string][]int{}
	for i, f := range findings {
		if f.Skipped != "" {
			continue
		}
		bySymbol[f.Symbol] = append(bySymbol[f.Symbol], i)
	}
	out := make([]Finding, len(findings))
	for i := range findings {
		out[i] = cloneFinding(findings[i])
	}
	for _, indices := range bySymbol {
		if len(indices) < 2 {
			continue
		}
		for _, idx := range indices {
			f := &out[idx]
			if OracleExecutionPolicyIssue(*f) != "" {
				for si := range f.Survivors {
					f.Survivors[si].OtherSelection = ""
				}
				continue
			}
			for si := range f.Survivors {
				s := &f.Survivors[si]
				key := survivorKey{s.Position, s.Operator}
				var verdicts []SelectionVerdict
				for _, otherIdx := range indices {
					if otherIdx == idx {
						continue
					}
					other := out[otherIdx]
					if other.Selection == f.Selection {
						continue
					}
					if OracleExecutionPolicyIssue(other) != "" {
						continue
					}
					if other.BodyHash != f.BodyHash || other.OperatorSet != f.OperatorSet {
						continue
					}
					if verdict, ok := selectionVerdictFrom(other, key, s.Site); ok {
						verdicts = append(verdicts, verdict)
					}
				}
				sort.Slice(verdicts, func(i, j int) bool {
					return verdicts[i].Selection < verdicts[j].Selection
				})
				s.OtherSelection = formatSelectionVerdicts(verdicts)
			}
		}
	}
	return out
}

func selectionVerdictFrom(other Finding, key survivorKey, site string) (SelectionVerdict, bool) {
	for _, k := range other.Kills {
		if k.Position == key.position && k.Operator == key.operator {
			return SelectionVerdict{
				Selection:   other.Selection,
				Disposition: "killed",
				Killer:      k.Killer,
			}, true
		}
	}
	for _, os := range other.Survivors {
		if os.Position == key.position && os.Operator == key.operator {
			if site != "" && os.Site != "" && site != os.Site {
				return SelectionVerdict{}, false
			}
			for _, a := range other.Attested {
				if a.Position == key.position && a.Operator == key.operator {
					return SelectionVerdict{
						Selection:   other.Selection,
						Disposition: "attested",
						Reason:      a.Reason,
					}, true
				}
			}
			return SelectionVerdict{
				Selection:   other.Selection,
				Disposition: "survived",
			}, true
		}
	}
	return SelectionVerdict{}, false
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
// records — a fresh record replaces the prior for its (symbol, selection)
// key, a prior record with no fresh counterpart stands — grafting attested
// dispositions against snapshot, the caller's run-start snapshot of
// attested dispositions per record key, and reporting the ones it shed.
// With the snapshot in hand the graft is pin-correct
// (REQ-attest-survivor): a disposition the fresh record already carries
// rode the domain-hold re-measure carry and stays unless withdrawn or
// re-attested concurrently; a disposition present in the live document
// but absent from the snapshot is a concurrent attestation and grafts onto
// a still-reported survivor; every other prior disposition was judged
// afresh and rejected — its mutation domain moved — and sheds loudly.
// Without a snapshot the graft anchors on survivor identity and site alone.
func MergeFindings(prior, fresh []Finding, snapshot map[string][]Attestation) ([]Finding, []AttestationShed) {
	var shed []AttestationShed
	byKey := map[string]Finding{}
	for _, f := range prior {
		byKey[f.recordKeyString()] = f
	}
	for _, f := range fresh {
		// A skipped result measured nothing: it must never shadow a symbol's
		// real record — Export's exclusion rule serializes nothing-measured,
		// the merge's rule is that nothing-measured never overwrites
		// something-measured.
		if f.Skipped != "" {
			continue
		}
		rKey := f.recordKeyString()
		// Replacement never sheds a disposition the document already holds for
		// a survivor the replacement still reports: an attestation added
		// between a run's snapshot — or its incremental commit — and this
		// merge rides survivor identity, so it grafts onto the fresh record
		// rather than being clobbered by it.
		if priorRec, ok := byKey[rKey]; ok {
			var dropped []AttestationShed
			snap, snapped := (map[survivorKey]Attestation)(nil), false
			if snapshot != nil {
				snapped = true
				snap = map[survivorKey]Attestation{}
				snapList, hasSnap := snapshot[rKey]
				if !hasSnap && f.Selection == "" {
					snapList = snapshot[f.Symbol]
				}
				for _, a := range snapList {
					snap[survivorKey{a.Position, a.Operator}] = a
				}
			}
			f.Attested, f.Withdrawn, dropped = graftDispositionsAgainst(priorRec.Attested, f.Attested, priorRec.Withdrawn, f.Withdrawn, f.Survivors, snap, snapped)
			for _, d := range dropped {
				d.Symbol = f.Symbol
				shed = append(shed, d)
			}
		}
		byKey[rKey] = f
	}
	out := make([]Finding, 0, len(byKey))
	for _, f := range byKey {
		out = append(out, f)
	}
	return AnnotateSelectionVerdicts(out), shed
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
	att, _, shed := graftDispositionsAgainst(prior, fresh, nil, nil, survivors, snapshot, snapped)
	return att, shed
}

func graftDispositionsAgainst(prior, fresh []Attestation, priorWithdrawn, freshWithdrawn []WithdrawnAttestation, survivors []Survivor, snapshot map[survivorKey]Attestation, snapped bool) ([]Attestation, []WithdrawnAttestation, []AttestationShed) {
	siteOf := make(map[survivorKey]string, len(survivors))
	surviving := make(map[survivorKey]bool, len(survivors))
	for _, survivor := range survivors {
		key := survivorKey{survivor.Position, survivor.Operator}
		surviving[key] = true
		siteOf[key] = survivor.Site
	}
	priorAttByKey := make(map[survivorKey]Attestation, len(prior))
	for _, a := range prior {
		priorAttByKey[survivorKey{a.Position, a.Operator}] = a
	}
	priorWithdrawnByKey := make(map[survivorKey]WithdrawnAttestation, len(priorWithdrawn))
	for _, w := range priorWithdrawn {
		priorWithdrawnByKey[survivorKey{w.Position, w.Operator}] = w
	}
	attested := make(map[survivorKey]bool, len(fresh))
	out := make([]Attestation, 0, len(fresh))
	for _, attestation := range fresh {
		key := survivorKey{attestation.Position, attestation.Operator}
		if snapped {
			if snapAtt, held := snapshot[key]; held && snapAtt.Reason == attestation.Reason {
				// If the live document withdrew this disposition concurrently,
				// honor the withdrawal rather than resurrecting the snapshot's
				// disposition from the in-run carry.
				if _, stillInPrior := priorAttByKey[key]; !stillInPrior {
					if _, wasWithdrawn := priorWithdrawnByKey[key]; wasWithdrawn {
						continue
					}
				}
				// If the live document re-attested this disposition concurrently
				// with a new reason, adopt the updated reason.
				if pAtt, stillInPrior := priorAttByKey[key]; stillInPrior && pAtt.Reason != attestation.Reason {
					if pAtt.Site == "" || pAtt.Site == siteOf[key] {
						attestation.Reason = pAtt.Reason
						attestation.Site = siteOf[key]
					}
				}
			}
		}
		attested[key] = true
		out = append(out, attestation)
	}
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
		attested[key] = true
		out = append(out, attestation)
	}
	withdrawnSeen := make(map[survivorKey]bool, len(freshWithdrawn)+len(priorWithdrawn))
	var outWithdrawn []WithdrawnAttestation
	appendWithdrawn := func(w WithdrawnAttestation) {
		key := survivorKey{w.Position, w.Operator}
		if attested[key] || withdrawnSeen[key] || !surviving[key] {
			return
		}
		if w.Site != "" && w.Site != siteOf[key] {
			return
		}
		w.Site = siteOf[key]
		withdrawnSeen[key] = true
		outWithdrawn = append(outWithdrawn, w)
	}
	for _, w := range priorWithdrawn {
		appendWithdrawn(w)
	}
	for _, w := range freshWithdrawn {
		appendWithdrawn(w)
	}
	return out, outWithdrawn, shed
}

// MergeWholeFindings is MergeFindings for a whole-tree run under the
// default selection: a prior default-selection record whose symbol the
// run did not discover is dropped — the symbol is gone from the tree
// (REQ-result-hygiene) — while a discovered, shaped, or other-selection
// record stands, re-measured or not; the shed reports against snapshot
// exactly as MergeFindings does.
func MergeWholeFindings(prior, fresh []Finding, discovered []Target, snapshot map[string][]Attestation) ([]Finding, []AttestationShed) {
	return MergeWholeFindingsForSelection(prior, fresh, discovered, snapshot, "")
}

// MergeWholeFindingsForSelection is MergeWholeFindings scoped to selKey:
// only records belonging to selKey whose symbol was not discovered by the
// whole-tree run are dropped as detached; records measured under other
// build selections stand untouched and cross-selection survivor verdicts
// are re-annotated across the merged set.
func MergeWholeFindingsForSelection(prior, fresh []Finding, discovered []Target, snapshot map[string][]Attestation, selKey string) ([]Finding, []AttestationShed) {
	return MergeDiscoveredFindingsForSelection(prior, fresh, discovered, snapshot, selKey, func(string) bool { return true })
}

// MergeDiscoveredFindingsForSelection merges fresh into prior and removes
// unshaped records belonging to selKey for which pruneSymbol returns true
// when their symbol is absent from discovered (REQ-result-hygiene).
func MergeDiscoveredFindingsForSelection(prior, fresh []Finding, discovered []Target, snapshot map[string][]Attestation, selKey string, pruneSymbol func(string) bool) ([]Finding, []AttestationShed) {
	current := make(map[string]bool, len(discovered))
	for _, target := range discovered {
		current[target.Symbol] = true
	}
	merged, shed := MergeFindings(prior, fresh, snapshot)
	if pruneSymbol == nil {
		return AnnotateSelectionVerdicts(merged), shed
	}
	kept := merged[:0]
	for _, finding := range merged {
		// A shaped finding's identity is never discovered: absence from
		// the whole-tree target set is its normal state, so the shed
		// spares it exactly as lifecycle pruning does — retirement is
		// the caller's explicit edit (REQ-target-structural,
		// REQ-target-manual-recipes). Records belonging to another build
		// selection or outside pruneSymbol's discovery scope are likewise spared.
		var inScope bool
		if pkg := finding.TargetEvidence.ObservationProof.Subject.Package; pkg != "" {
			inScope = pruneSymbol(pkg + "._")
		} else {
			inScope = pruneSymbol(finding.Symbol)
		}
		if finding.Selection != selKey || !inScope || current[finding.Symbol] || finding.Shape != nil {
			kept = append(kept, finding)
		}
	}
	return AnnotateSelectionVerdicts(kept), shed
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
	// before runs under the document lock ahead of the prior read and
	// the update: the writer's own record beside the document re-read
	// as it then stands, a malformed one refusing before anything is
	// judged or written. prepare runs once the update decided, ahead of
	// the document's write (and of the after-work on the
	// nothing-to-write arm): the record's write, whose failure refuses
	// the whole write with nothing landed — never a landed document
	// reported as a failed commit (REQ-result-exemptions).
	before  func() error
	prepare func() error
}

func (u documentUpdate) runBefore() error {
	if u.before == nil {
		return nil
	}
	return u.before()
}

func (u documentUpdate) runPrepare() error {
	if u.prepare == nil {
		return nil
	}
	return u.prepare()
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
	if err := u.runBefore(); err != nil {
		return err
	}

	var prior []Finding
	mode, err := recordFileMode(path)
	if err != nil {
		return err
	}
	data, err := contextio.ReadFile(ctx, path)
	switch {
	case os.IsNotExist(err):
	case err != nil:
		return err
	default:
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
		if err := u.runPrepare(); err != nil {
			return err
		}
		if u.after != nil {
			return u.after(nil)
		}
		return nil
	}
	if err := u.runPrepare(); err != nil {
		return err
	}
	written := append(doc, '\n')
	if err := writeRecordFile(ctx, path, written, mode); err != nil {
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

// AttestOptions configures AttestFindingWithOptions: Selection and
// HasSelection scope the target record when a symbol has findings across
// multiple build selections; Reattest replaces an existing attestation's
// reason; Withdraw withdraws an existing attestation and records the
// withdrawal reason alongside the prior reason (REQ-attest-survivor).
type AttestOptions struct {
	Selection    string
	HasSelection bool
	Reattest     bool
	Withdraw     bool
}

// AttestFinding records one equivalence disposition on the named
// symbol's finding in a document's rows — the one disposition both
// faces' update callbacks apply, and the one home of the reasoning
// rule (a blank reasoning refuses, whitespace included, as the
// ephemeral attestation's does) — returning the rows and the record
// as attested; a symbol with no finding refuses (REQ-attest-survivor).
func AttestFinding(all []Finding, symbol, position, operator, reason string) ([]Finding, Finding, error) {
	return AttestFindingWithOptions(all, symbol, position, operator, reason, AttestOptions{})
}

// AttestFindingWithOptions applies an attestation, re-attestation, or
// withdrawal to the finding record matching symbol and opts.Selection,
// re-annotating cross-selection survivor verdicts across the document's rows.
func AttestFindingWithOptions(all []Finding, symbol, position, operator, reason string, opts AttestOptions) ([]Finding, Finding, error) {
	if opts.Reattest && opts.Withdraw {
		return nil, Finding{}, fmt.Errorf("gomutant: cannot combine --reattest and --withdraw")
	}
	if err := ValidateAttestationReason(reason); err != nil {
		return nil, Finding{}, err
	}
	targetIdx := -1
	if opts.HasSelection || opts.Selection != "" {
		for i := range all {
			if all[i].Symbol == symbol && all[i].Selection == opts.Selection {
				targetIdx = i
				break
			}
		}
		if targetIdx < 0 {
			if opts.Selection != "" {
				return nil, Finding{}, fmt.Errorf("no finding for %s [%s]", symbol, opts.Selection)
			}
			return nil, Finding{}, fmt.Errorf("no finding for %s", symbol)
		}
	} else {
		var nonDefault []int
		for i := range all {
			if all[i].Symbol == symbol {
				if all[i].Selection == "" {
					targetIdx = i
					break
				}
				nonDefault = append(nonDefault, i)
			}
		}
		if targetIdx < 0 {
			switch len(nonDefault) {
			case 0:
				return nil, Finding{}, fmt.Errorf("no finding for %s", symbol)
			case 1:
				targetIdx = nonDefault[0]
			default:
				return nil, Finding{}, fmt.Errorf("multiple findings for %s across selections; specify a selection", symbol)
			}
		}
	}
	var err error
	switch {
	case opts.Withdraw:
		err = all[targetIdx].WithdrawAttestation(position, operator, reason)
	case opts.Reattest:
		err = all[targetIdx].Reattest(position, operator, reason)
	default:
		err = all[targetIdx].Attest(position, operator, reason)
	}
	if err != nil {
		return nil, Finding{}, err
	}
	all = AnnotateSelectionVerdicts(all)
	return all, all[targetIdx], nil
}
