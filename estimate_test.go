package gomutant

import (
	"testing"
	"time"

	"github.com/greatliontech/gomutant/internal/engine"
)

// Only complete-group baseline durations can price a candidate. Extent,
// coverage, and historical batch timing cannot grant a saving.
func TestEstimateWindowPricesOnlyMeasuredDurations(t *testing.T) {
	w := scheduleTestWork("example.com/p", "example.com/p", []string{"TestA", "TestB"})
	repl := []engine.Replacement{{File: "f.go"}}
	w.candidates = []engine.Candidate{
		{Symbol: "S", Position: "f.go:10:2", Extent: "10:2-12:3", Replacements: repl},
		{Symbol: "S", Position: "f.go:40:2", Extent: "40:2-41:3", Replacements: repl},
		{Symbol: "S", Position: "f.go:50:2", Replacements: repl},
		{Symbol: "S", Position: "f.go:60:2"},
	}
	baseline := func(group) (time.Duration, bool) { return time.Minute, true }
	est := estimateWindow([]work{w}, baseline)
	if est.full != 3 || est.unknown != 0 || est.projected != 3*time.Minute {
		t.Fatalf("estimate=%+v", est)
	}
	est = estimateWindow([]work{w}, nil)
	if est.full != 0 || est.unknown != 3 || est.projected != 0 {
		t.Fatalf("unpriced=%+v", est)
	}
	// Partial prices never leak into the projection.
	w.groups = append(w.groups, group{pkgs: []string{"example.com/q"}, runRegex: "^TestQ$"})
	est = estimateWindow([]work{w}, func(g group) (time.Duration, bool) { return time.Minute, g.pkgs[0] == "example.com/p" })
	if est.full != 0 || est.unknown != 3 || est.projected != 0 {
		t.Fatalf("partly priced=%+v", est)
	}
	est = estimateWindow([]work{w}, baseline)
	if est.full != 3 || est.projected != 6*time.Minute {
		t.Fatalf("two groups=%+v", est)
	}
}

// Candidate selection is shared by pricing and dispatch: pre-execution
// discards, served candidates, and measured prefixes cost nothing this run.
func TestEstimateWindowMirrorsExecutingSelection(t *testing.T) {
	base := scheduleTestWork("example.com/p", "example.com/p", []string{"TestA", "TestB"})
	for range 3 {
		base.candidates = append(base.candidates, engine.Candidate{Replacements: []engine.Replacement{{File: "f.go"}}})
	}
	baseline := func(group) (time.Duration, bool) { return time.Minute, true }
	served := base
	served.serve = &Finding{}
	served.flagged = map[int]bool{1: true}
	extended := base
	extended.extend = &Finding{}
	extended.extendFrom = 2
	drifted := base
	drifted.drift = &Finding{}
	drifted.driftRemeasure = map[int]bool{0: true, 2: true}
	for _, tc := range []struct {
		name  string
		w     work
		count int
	}{
		{"serve", served, 1}, {"extension", extended, 1}, {"drift", drifted, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			est := estimateWindow([]work{tc.w}, baseline)
			if est.full != tc.count || est.unknown != 0 || est.projected != time.Duration(tc.count)*time.Minute {
				t.Fatalf("estimate=%+v", est)
			}
		})
	}
	// Reuse's proved composition chooses its groups before dispatch. The
	// estimate reads that same choice, not a different full-pattern price.
	drifted.driftSurvivors = map[int]bool{0: true}
	drifted.narrowGroups = []group{{pkgs: []string{"example.com/p"}, runRegex: "^TestAdded$"}}
	est := estimateWindow([]work{drifted}, func(g group) (time.Duration, bool) {
		if g.runRegex == "^TestAdded$" {
			return 7 * time.Second, true
		}
		return time.Minute, true
	})
	if est.full != 2 || est.projected != 67*time.Second {
		t.Fatalf("composed scope=%+v", est)
	}
}

func TestAdvanceDoneNeverRegresses(t *testing.T) {
	if got := advanceDone(10, 5, 12); got != 15 {
		t.Fatalf("advance=%d", got)
	}
	if got := advanceDone(10, 0, 12); got != 12 {
		t.Fatalf("drained=%d", got)
	}
	if got := advanceDone(10, 2, 12); got != 12 {
		t.Fatalf("equal=%d", got)
	}
}

func TestNextReadyWindowOrdersByCost(t *testing.T) {
	pool := []readyWindow{{cost: 30 * time.Second, priced: true}, {priced: false}, {cost: 5 * time.Second, priced: true}, {cost: 5 * time.Second, priced: true}}
	if got := nextReadyWindow(pool); got != 2 {
		t.Fatalf("pick=%d", got)
	}
	if got := nextReadyWindow([]readyWindow{{priced: false}, {cost: time.Hour, priced: true}}); got != 1 {
		t.Fatalf("mixed=%d", got)
	}
}

func TestEstimateRenderStringsFabricateNothing(t *testing.T) {
	if s := (windowEstimate{unknown: 3}).projectedString(); s != "" {
		t.Fatalf("unpriced=%q", s)
	}
	if s := (windowEstimate{full: 1, projected: 250 * time.Millisecond}).projectedString(); s != "250ms" {
		t.Fatalf("fraction=%q", s)
	}
	if s := (windowEstimate{full: 2, projected: 90 * time.Second}).projectedString(); s != "1m30s" {
		t.Fatalf("price=%q", s)
	}
	w := scheduleTestWork("example.com/p", "example.com/p", []string{"TestA"})
	w.candidates = []engine.Candidate{{Replacements: []engine.Replacement{{File: "f.go"}}}}
	if cost, ok := windowPrice([]work{w}, nil); cost != 0 || ok {
		t.Fatalf("unpriced window=%v/%v", cost, ok)
	}
	if cost, ok := windowPrice([]work{w}, func(group) (time.Duration, bool) { return 3 * time.Second, true }); cost != 3*time.Second || !ok {
		t.Fatalf("priced window=%v/%v", cost, ok)
	}
}
