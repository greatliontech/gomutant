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
	// auditProjected prices the audit: the per-window cap (or every
	// narrowed candidate, when fewer) of full unsplit oracle re-runs
	// at the window's costliest work, each at baseline pace.
	auditProjected time.Duration
}

// estimateWindow prices one gathered window against the schedule
// store's probed coverage and the run's recorded baseline durations.
// baselineDur answers a group's passing-baseline wall-clock when one
// was measured; a nil func prices nothing (every candidate counts
// unpriced). The narrowing decision is narrowingBatches — the same
// decision the schedule transform executes, so the estimate cannot
// disagree with the schedule about what would run.
func estimateWindow(window []work, store *scheduleStore, baselineDur func(group) (time.Duration, bool), auditCap int) windowEstimate {
	if baselineDur == nil {
		baselineDur = func(group) (time.Duration, bool) { return 0, false }
	}
	var est windowEstimate
	var maxFull time.Duration
	for _, w := range window {
		schedulable := store != nil && !w.shaped && w.targetView != nil
		coverPkg := ""
		if schedulable {
			coverPkg = w.targetView.subject.Package
		}
		// The audit re-scores one candidate under its work's full
		// unsplit oracle: Σ of the work's group baselines. The audit
		// projection uses the costliest fully-priced work.
		var workFull time.Duration
		workFullKnown := true
		for _, g := range w.groups {
			if d, ok := baselineDur(g); ok {
				workFull += d
			} else {
				workFullKnown = false
			}
		}
		if workFullKnown && workFull > maxFull {
			maxFull = workFull
		}
		for _, mi := range executingIndexes(w) {
			// executingIndexes yields runnable candidates only.
			m, _ := w.candidates[mi].Mutant()
			var cost time.Duration
			narrowedCand, unknownCand := false, false
			for _, g := range w.groups {
				if schedulable && m.Extent != "" {
					reaching, ok := narrowingBatches(store.get(coverageKey(g, coverPkg)), coverPkg, Survivor{Position: m.Position, Extent: m.Extent})
					if ok {
						narrowedCand = true
						priced := true
						var sum time.Duration
						for _, b := range reaching {
							if b.dur <= 0 {
								priced = false
								break
							}
							sum += b.dur
						}
						if priced {
							cost += sum
						} else {
							unknownCand = true
						}
						continue
					}
				}
				if d, ok := baselineDur(g); ok {
					cost += d
				} else {
					unknownCand = true
				}
			}
			switch {
			case unknownCand:
				est.unknown++
			case narrowedCand:
				est.narrowed++
				est.projected += cost
			default:
				est.full++
				est.projected += cost
			}
		}
	}
	audits := min(est.narrowed, auditCap)
	est.auditProjected = time.Duration(audits) * maxFull
	return est
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
