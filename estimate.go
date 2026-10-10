package gomutant

import "time"

// windowEstimate projects complete oracle executions at measured baseline
// pace. An unpriced candidate is counted, never assigned a fabricated cost.
// Serial confirmations, advisory coverage, overlay builds and timeout-bound
// execution are not predicted. A kill may end its oracle early.
type windowEstimate struct {
	projected     time.Duration
	full, unknown int
}

func estimateWindow(window []work, baselineDur func(group) (time.Duration, bool)) windowEstimate {
	var est windowEstimate
	for _, w := range window {
		for _, mi := range executingIndexes(w) {
			// Historical composition may choose a proved remeasurement
			// scope. Price precisely the groups the dispatcher will run.
			cost, priced := workFullPrice(scopedWork(w, mi), baselineDur)
			if !priced {
				est.unknown++
				continue
			}
			est.full++
			est.projected += cost
		}
	}
	return est
}

// workFullPrice prices every unsplit group from its passing baseline.
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

func (e windowEstimate) projectedString() string {
	if e.full == 0 {
		return ""
	}
	return roundedDuration(e.projected)
}

// roundedDuration preserves a real sub-second price.
func roundedDuration(d time.Duration) string {
	if r := d.Round(time.Second); r > 0 {
		return r.String()
	}
	return d.Round(time.Millisecond).String()
}

func windowPrice(window []work, baselineDur func(group) (time.Duration, bool)) (cost time.Duration, priced bool) {
	est := estimateWindow(window, baselineDur)
	return est.projected, est.unknown == 0
}
