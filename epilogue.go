package gomutant

import (
	"context"
	"fmt"
	"strings"
)

// RunLedger is a run's document side, one implementation both faces
// drive: the run-start snapshot of dispositions per symbol that makes
// the merge graft pin-correct, each finished target's incremental
// commit, the final merge (a whole-tree run's reconcile included, zero
// targets or many), every shed reported exactly once with the first
// report winning, and the counts the epilogue states — records
// promoted from the machine-local overlay, records left machine-local,
// records a reconcile dropped. The post-merge rows are the rendering
// truth on every face: what a response describes is what the document
// holds (REQ-attest-survivor, REQ-mcp-findings-doc).
type RunLedger struct {
	store     *Store
	runID     string
	wholeTree bool
	snapshot  map[string][]Attestation
	// overlaid names the prior records that sat in the machine-local
	// overlay at the run's start — their placement, the baseline the
	// promoted count is judged against.
	overlaid  map[string]bool
	postMerge map[string]Finding
	// reported holds every mutant whose fate a face has already been
	// told — a contradiction, a site shed, a commit's strip — so the
	// merge layer's vaguer re-derivation is never retold
	// (REQ-attest-survivor: one disposition owes the reader one line).
	reported    map[string]bool
	commitSheds []AttestationShed

	// Update is the document write every commit and the final merge go
	// through; nil is the store's own. A face routes it through a seam
	// of its own here.
	Update func(ctx context.Context, change func([]Finding) ([]Finding, error)) error
	// Shed receives each shed the first time it is known — a site shed
	// as the run reports it, a commit's strip after the update returned
	// — never under the document lock, so a slow terminal or client
	// cannot extend the hold. The final merge's residue is returned by
	// Finish instead, for the face to place in its epilogue.
	Shed func(AttestationShed)
	// Committed observes each finding after its commit returned, never
	// before: a face's cumulative progress line banks it as committed
	// work (REQ-exec-run-status), and a failed or cancelled commit is
	// work the document does not hold (REQ-exec-cancellation's
	// claims-only-committed clause).
	Committed func(Finding)
}

// NewRunLedger opens the ledger over the document as read at
// preparation: prior is the run-start snapshot the graft judges
// against, and the store's last read says which of its records sat in
// the overlay — the promoted count's baseline.
func NewRunLedger(store *Store, prior []Finding, runID string, wholeTree bool) *RunLedger {
	l := &RunLedger{
		store:     store,
		runID:     runID,
		wholeTree: wholeTree,
		snapshot:  make(map[string][]Attestation, len(prior)),
		overlaid:  make(map[string]bool, len(prior)),
		postMerge: map[string]Finding{},
		reported:  map[string]bool{},
	}
	for _, f := range prior {
		l.snapshot[f.Symbol] = append([]Attestation(nil), f.Attested...)
		l.overlaid[f.Symbol] = store.Overlaid(f.Symbol)
	}
	return l
}

func (l *RunLedger) update(ctx context.Context, change func([]Finding) ([]Finding, error)) error {
	if l.Update != nil {
		return l.Update(ctx, change)
	}
	return l.store.Update(ctx, change)
}

// Contradiction records that the mutant's fate has been told with the
// shed reasoning attached: its later shed is not retold with a vaguer
// reason. The run emits a contradiction before its target's commit, so
// the filter is complete when the shed arrives.
func (l *RunLedger) Contradiction(c AttestationContradiction) {
	l.reported[mutantKey(c.Symbol, c.Position, c.Operator)] = true
}

// SiteShed is Options.AttestationSiteShed: a carry that refused a moved
// site, reported once with its specific cause.
func (l *RunLedger) SiteShed(d AttestationShed) {
	l.commitSheds = append(l.commitSheds, d)
	l.deliver(d)
}

func (l *RunLedger) deliver(d AttestationShed) {
	key := mutantKey(d.Symbol, d.Position, d.Operator)
	if l.reported[key] {
		return
	}
	l.reported[key] = true
	if l.Shed != nil {
		l.Shed(d)
	}
}

// Commit is Options.Commit: each finished target merges into the
// document under its lock, so an interrupted run keeps its completed
// targets. The incremental commit is where a cross-site shed actually
// happens against the prior document — the final merge sees an
// already-stripped record — so the shed is collected here or it is
// silent, and delivered after the update returned: the strip has
// persisted, and delivery never runs under the lock.
func (l *RunLedger) Commit(ctx context.Context) func(Finding) error {
	return func(finding Finding) error {
		var dropped []AttestationShed
		err := l.update(ctx, func(current []Finding) ([]Finding, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			merged, shed := MergeFindings(current, []Finding{finding}, l.snapshot)
			dropped = shed
			for _, m := range merged {
				if m.Symbol == finding.Symbol {
					l.postMerge[finding.Symbol] = m
				}
			}
			return merged, nil
		})
		if err != nil {
			return err
		}
		if l.Committed != nil {
			l.Committed(finding)
		}
		l.commitSheds = append(l.commitSheds, dropped...)
		for _, d := range dropped {
			l.deliver(d)
		}
		return nil
	}
}

// Rendered is the run's findings with each committed symbol's
// post-merge record in place of the run's own — the rows the document
// holds. A plan, which commits nothing, renders the run's own rows.
func (l *RunLedger) Rendered(findings []Finding) []Finding {
	return RenderedFindings(findings, l.postMerge)
}

// RunOutcome is what the final merge left behind, as data for a face to
// render.
type RunOutcome struct {
	// Rendered is Rendered(findings) after the final merge.
	Rendered []Finding
	// ResidueSheds are the final merge's sheds no earlier report told,
	// deduplicated by mutant, the first report winning.
	ResidueSheds []AttestationShed
	// Promoted counts the records the run's writes carried from the
	// machine-local overlay into the committed document — its own and
	// any standing record the write's re-judgment moved — a document
	// change git only sees when committed (REQ-mcp-findings-doc).
	Promoted int
	// MachineLocal counts the measured records the store routed
	// machine-local, the aggregate REQ-result-local-signpost states
	// beside the per-record disqualifiers.
	MachineLocal int
	// Dropped counts the symbols a whole-tree reconcile removed because
	// their targets left the code — a persisted document write the face
	// owns, never buried in an empty success (REQ-mcp-envelope).
	Dropped int
}

// Finish is the final merge, run before anything renders so the output
// reads the rows the document actually holds: a disposition recorded
// concurrently between a symbol's commit and the end of the run is in
// both or in neither (REQ-mcp-findings-doc). The run's coverage bound
// rides the same write through the store's one recording seam
// (REQ-result-unreached-bound). A whole-tree run reconciles the
// document against its targets, zero targets included — the write that
// drops records whose targets left the code; a scoped selection of
// nothing writes nothing, while a scoped run that selected targets and
// merges nothing (every target refused) still writes, as the document's
// re-judgment of its standing records is that write's. The drop count
// is stated only once the write returned: a failed write persisted
// nothing.
func (l *RunLedger) Finish(ctx context.Context, findings []Finding, targets []Target, sel Selection) (RunOutcome, error) {
	var outcome RunOutcome
	if len(targets) == 0 && !l.wholeTree {
		return outcome, nil
	}
	l.store.RecordRunBound(findings, sel, l.runID, l.wholeTree)
	var finalSheds []AttestationShed
	var merged []Finding
	dropped := 0
	err := l.update(ctx, func(current []Finding) ([]Finding, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if l.wholeTree {
			merged, finalSheds = MergeWholeFindings(current, findings, targets, l.snapshot)
			dropped = droppedSymbols(current, merged)
		} else {
			merged, finalSheds = MergeFindings(current, findings, l.snapshot)
		}
		for _, m := range merged {
			if _, ran := l.postMerge[m.Symbol]; ran {
				l.postMerge[m.Symbol] = m
			}
		}
		return merged, nil
	})
	if err != nil {
		return outcome, err
	}
	outcome.Dropped = dropped
	outcome.Rendered = l.Rendered(findings)
	for _, d := range DedupeAttestationSheds(append(append([]AttestationShed(nil), l.commitSheds...), finalSheds...)) {
		key := mutantKey(d.Symbol, d.Position, d.Operator)
		if l.reported[key] {
			continue
		}
		l.reported[key] = true
		outcome.ResidueSheds = append(outcome.ResidueSheds, d)
	}
	// Promotion is judged over the whole merged document against where
	// each record SAT, not the run's own symbols alone: the write
	// re-judges every standing record against the exemptions in force,
	// so a record untouched by this run can leave the overlay on it — a
	// document change the run owns exactly as it owns its own records'
	// (REQ-mcp-findings-doc).
	for _, m := range merged {
		if !l.overlaid[m.Symbol] {
			continue
		}
		if layer, _ := l.store.Layer(m); layer == "repo" {
			outcome.Promoted++
		}
	}
	for _, f := range outcome.Rendered {
		if f.Skipped != "" {
			continue
		}
		if layer, _ := l.store.Layer(f); layer == "local" {
			outcome.MachineLocal++
		}
	}
	return outcome, nil
}

// DropText states a whole-tree reconcile's drop for a face, or nothing
// when nothing dropped: the one spelling on both faces.
func (o RunOutcome) DropText() string {
	if o.Dropped == 0 {
		return ""
	}
	return fmt.Sprintf("the whole-tree reconcile dropped %d record(s) whose targets left the code", o.Dropped)
}

// droppedSymbols counts the symbols a reconcile removed — a symbol-set
// difference, so a document hand-edited into duplicate records for one
// symbol cannot overcount the drop.
func droppedSymbols(current, merged []Finding) int {
	kept := make(map[string]bool, len(merged))
	for _, m := range merged {
		kept[m.Symbol] = true
	}
	seen := map[string]bool{}
	dropped := 0
	for _, c := range current {
		if !kept[c.Symbol] && !seen[c.Symbol] {
			seen[c.Symbol] = true
			dropped++
		}
	}
	return dropped
}

// PromotedText states the records the run's writes carried from the
// machine-local overlay into the committed document — a document change
// git only sees when committed — or nothing when none was.
func (o RunOutcome) PromotedText() string {
	if o.Promoted == 0 {
		return ""
	}
	return fmt.Sprintf("%d record(s) promoted - findings document changed, commit it", o.Promoted)
}

// PersistedRiding folds the document changes the final merge persisted
// — a reconcile's drop, a promotion — into an error exit after it: a
// face that reports the error alone would hide a document change git
// only sees when committed, so the counts ride the error text on both
// faces, as sheds do on the structured one (REQ-mcp-findings-doc).
func (o RunOutcome) PersistedRiding(err error) error {
	if err == nil {
		return nil
	}
	var parts []string
	for _, line := range []string{o.DropText(), o.PromotedText()} {
		if line != "" {
			parts = append(parts, line)
		}
	}
	if len(parts) == 0 {
		return err
	}
	return fmt.Errorf("%w — additionally, %s (persisted)", err, strings.Join(parts, "; "))
}

// mutantKey identifies one mutant across the run's reports: the
// symbol, the position, the operator.
func mutantKey(symbol, position, operator string) string {
	return symbol + "\x00" + position + "\x00" + operator
}
