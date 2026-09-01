package gomutant

import "time"

// The window cost model (REQ-exec-run-status's estimate class): an
// advisory PROJECTION of a window's scheduled oracle time at
// measured-baseline pace, derived entirely from measurements the run
// already made — passing-baseline wall-clocks and coverage-probe
// batch wall-clocks. Nothing is fabricated: a candidate any of whose
// parts has no recorded duration counts unpriced instead of guessing,
// and its cost is NOT in the projection. Named exclusions, reconciled
// by the live pace instead of predicted: a candidate that times out
// costs up to its derived budget (a multiple of the priced baseline),
// serial confirmation runs, the once-per-group survivor bucket
// probes, and per-mutant build overlay cost; kills end their schedule
// early. The projection is a pace anchor, not a bound in either
// direction.

// windowEstimate is one window's model: the priced projection, the
// executing candidates classified (narrowed / whole-group / unpriced),
// and the narrowed-survivor audit priced separately (its count is
// capped; its per-run cost is the same baseline-pace projection).
type windowEstimate struct {
	projected time.Duration
	// narrowed counts candidates with at least one narrowing group;
	// full counts whole-group candidates; unknown counts candidates
	// with any unpriced part (excluded from the projection).
	narrowed, full, unknown int
	// auditProjected prices the audit at the SAME savings-derived cap
	// the executed audit uses (every narrowed candidate modeled as a
	// would-be survivor — the conservative upper bound), each re-run
	// at the costliest work's full-oracle baseline pace.
	auditProjected time.Duration
}

// estimateWindow prices one gathered window against the schedule
// store's probed coverage and the run's recorded baseline durations.
// baselineDur answers a group's passing-baseline wall-clock when one
// was measured; a nil func prices nothing (every candidate counts
// unpriced). The narrowing decision is narrowingBatches — the same
// decision the schedule transform executes, so the estimate cannot
// disagree with the schedule about what would run.
func estimateWindow(window []work, store *scheduleStore, baselineDur func(group) (time.Duration, bool), auditCeiling int) windowEstimate {
	var est windowEstimate
	var maxFull, modelSavings time.Duration
	for _, w := range window {
		wf, wfOK := workFullPrice(w, baselineDur)
		if wfOK && wf > maxFull {
			maxFull = wf
		}
		for _, mi := range executingIndexes(w) {
			cost, narrowed, priced := candidatePrice(w, mi, store, baselineDur)
			switch {
			case !priced:
				est.unknown++
			case narrowed:
				est.narrowed++
				est.projected += cost
				// The audit projection models every narrowed candidate
				// as a would-be survivor — the conservative upper
				// bound of the savings the executed cap derives from.
				if wfOK && cost < wf {
					modelSavings += wf - cost
				}
			default:
				est.full++
				est.projected += cost
			}
		}
	}
	audits := 0
	if est.narrowed > 0 {
		audits = min(min(est.narrowed, auditCeiling), derivedAuditCap(modelSavings, maxFull))
	}
	est.auditProjected = time.Duration(audits) * maxFull
	return est
}

// workFullPrice prices a work's full unsplit oracle — Σ of its
// groups' passing-baseline wall-clocks; false when any group (or the
// pricing source itself) is unpriced.
func workFullPrice(w work, baselineDur func(group) (time.Duration, bool)) (time.Duration, bool) {
	if baselineDur == nil {
		return 0, false
	}
	var full time.Duration
	for _, g := range w.groups {
		d, ok := baselineDur(g)
		if !ok {
			return 0, false
		}
		full += d
	}
	return full, true
}

// candidatePrice prices ONE executing candidate's scheduled run: the
// reaching batches where the narrowing decision (narrowingBatches —
// the schedule's own) narrows a group, the group baseline where it
// runs whole. narrowed reports any narrowing group; priced is false
// on ANY unpriced part — the cost is then partial and callers must
// not use it. The audit executes scopedWork, which swaps in
// narrowGroups for drift survivors; those never narrow today
// (coverage probes price w.groups only and coverageKey carries the
// pattern), so pricing w.groups stays faithful — revisit if the
// probes ever widen.
func candidatePrice(w work, mi int, store *scheduleStore, baselineDur func(group) (time.Duration, bool)) (cost time.Duration, narrowed, priced bool) {
	if baselineDur == nil {
		// A narrowed group still prices off its batch wall-clocks;
		// only whole-group parts need the baseline source.
		baselineDur = func(group) (time.Duration, bool) { return 0, false }
	}
	m, runnable := w.candidates[mi].Mutant()
	if !runnable {
		return 0, false, false
	}
	schedulable := store != nil && !w.shaped && w.targetView != nil
	coverPkg := ""
	if schedulable {
		coverPkg = w.targetView.subject.Package
	}
	priced = true
	for _, g := range w.groups {
		if schedulable && m.Extent != "" {
			reaching, ok := narrowingBatches(store.get(coverageKey(g, coverPkg)), coverPkg, Survivor{Position: m.Position, Extent: m.Extent})
			if ok {
				narrowed = true
				for _, b := range reaching {
					if b.dur <= 0 {
						priced = false
					}
					cost += b.dur
				}
				continue
			}
		}
		if d, ok := baselineDur(g); ok {
			cost += d
		} else {
			priced = false
		}
	}
	return cost, narrowed, priced
}

// projectedString renders the priced projection for the estimate event —
// empty when NOTHING is priced, so an all-unpriced window can never
// read as a zero-cost one (the model fabricates no duration).
func (e windowEstimate) projectedString() string {
	if e.narrowed+e.full == 0 {
		return ""
	}
	return roundedDuration(e.projected)
}

// auditString renders the audit projection; empty when no audit is
// priced.
func (e windowEstimate) auditString() string {
	if e.auditProjected <= 0 {
		return ""
	}
	return roundedDuration(e.auditProjected)
}

// roundedDuration rounds to seconds for legibility without erasing a
// real sub-second price to "0s".
func roundedDuration(d time.Duration) string {
	if r := d.Round(time.Second); r > 0 {
		return r.String()
	}
	return d.Round(time.Millisecond).String()
}

// windowPrice is the PRE-PROBE flavor of the window cost model: no
// coverage store yet, so every executing candidate prices whole-group
// off the measured baselines — the driver's ordering price, refined
// by the post-probe estimate event once the window's probes run.
func windowPrice(window []work, baselineDur func(group) (time.Duration, bool)) (cost time.Duration, priced bool) {
	est := estimateWindow(window, nil, baselineDur, auditNarrowedCap)
	return est.projected, est.unknown == 0
}

// auditShareDivisor sets the audit's share of the narrowing's modeled
// savings: the audit may spend at most 1/8 of what the narrowing
// saved this window, so the narrowing keeps at least 7/8 of its win
// on ANY oracle duration — the fixed count it replaces priced four
// full 21-minute oracles onto a window whose narrowing saved less
// than one. The measured campaign share lands with the chunk's gate
// re-run.
const auditShareDivisor = 8

// candidateNarrowingSavings models what narrowing saved on ONE
// candidate: its work's full-oracle price minus its scheduled price
// (whole groups contribute equally to both and cancel). Any unpriced
// part yields zero — savings are never fabricated, so an unpriced
// window derives the floor, not a share.
func candidateNarrowingSavings(w work, mi int, store *scheduleStore, baselineDur func(group) (time.Duration, bool)) time.Duration {
	full, ok := workFullPrice(w, baselineDur)
	if !ok {
		return 0
	}
	cost, narrowed, priced := candidatePrice(w, mi, store, baselineDur)
	if !narrowed || !priced || cost >= full {
		return 0
	}
	return full - cost
}

// derivedAuditCap bounds a window's narrowed-survivor audit by the
// narrowing's own modeled savings: at most savings/auditShareDivisor
// worth of full-oracle re-runs (each priced at unit, the costliest
// work's full oracle), floored at ONE sample — the residual risk is a
// measured quantity in every window that narrowed, never an
// assumption — and ceilinged at the fixed per-window cap. A zero or
// unpriced unit derives the floor.
func derivedAuditCap(savings, unit time.Duration) int {
	if unit <= 0 {
		return 1
	}
	share := int(savings / (auditShareDivisor * unit))
	return max(1, min(share, auditNarrowedCap))
}
