package gomutant

import (
	"math/rand"
	"testing"

	"github.com/greatliontech/gomutant/internal/engine"
)

// gatherAll partitions a work list exactly as the driver does: one
// gather after another over the same channel.
func gatherAll(items []work, ceiling, minimum int) [][]work {
	ch := make(chan work, len(items))
	for _, w := range items {
		ch <- w
	}
	close(ch)
	var windows [][]work
	for {
		window, ok := gatherWindow(ch, ceiling, minimum)
		if !ok {
			return windows
		}
		windows = append(windows, window)
	}
}

func windowTotals(window []work) (candidates, executions int) {
	for _, w := range window {
		candidates += len(w.candidates)
		executions += windowExecutions(len(w.candidates), len(w.oracle))
	}
	return
}

// The bounds derive from the worker count alone: the ceiling eight per
// worker and sixty-four at least, the minimum the worker count and
// eight at least (REQ-exec-oracle-run's window rule).
func TestWindowBoundsDeriveFromTheWorkerCount(t *testing.T) {
	for _, tc := range []struct{ jobs, ceiling, minimum int }{{1, 64, 8}, {4, 64, 8}, {8, 64, 8}, {9, 72, 9}, {16, 128, 16}} {
		if c, m := windowBounds(tc.jobs, 0); c != tc.ceiling || m != tc.minimum {
			t.Fatalf("windowBounds(%d) = %d, %d; want %d, %d", tc.jobs, c, m, tc.ceiling, tc.minimum)
		}
	}
	// A test's fixed window takes both bounds, so the execution budget
	// can never split it.
	if c, m := windowBounds(16, 5); c != 5 || m != 5 {
		t.Fatalf("windowBounds(16, 5) = %d, %d; want 5, 5", c, m)
	}
	if windowCandidatesPerWorker != 8 || windowCandidatesFloor != 64 || windowCandidatesMin != 8 || windowExecutionBudget != 512 {
		t.Fatalf("the window's constants moved: %d %d %d %d", windowCandidatesPerWorker, windowCandidatesFloor, windowCandidatesMin, windowExecutionBudget)
	}
	if windowCandidatesMin != auditShareDivisor {
		t.Fatalf("the candidate minimum %d is not the audit share divisor %d it exists to honour", windowCandidatesMin, auditShareDivisor)
	}
}

// Over generated work lists the partition meets its bounds and keeps
// order: two gathers of one list agree window for window; every window
// but the last is closed by a bound (its candidate total at the ceiling,
// or its execution total at the budget with at least the minimum of
// candidates) while its proper prefix is closed by neither; no window
// is empty; and the windows concatenate to the list in order
// (REQ-exec-oracle-run's window rule).
func TestWindowPartitionMeetsItsBoundsAndKeepsOrder(t *testing.T) {
	rng := rand.New(rand.NewSource(164))
	budget, minimum := windowBounds(8, 0)
	for round := 0; round < 3000; round++ {
		n := rng.Intn(40)
		items := make([]work, n)
		for i := range items {
			// A probe-class target (one or two tests) or a suite-class
			// one (dozens), with a few candidates each — and the
			// occasional oracle-less target costing no execution.
			tests := rng.Intn(3)
			if rng.Intn(4) == 0 {
				tests = 20 + rng.Intn(120)
			}
			items[i] = work{target: i, candidates: make([]engine.Candidate, rng.Intn(9)), oracle: make([]string, tests)}
		}
		once, twice := gatherAll(items, budget, minimum), gatherAll(items, budget, minimum)
		if len(once) != len(twice) {
			t.Fatalf("round %d: partitions differ in length: %d vs %d", round, len(once), len(twice))
		}
		next := 0
		for wi, window := range once {
			if len(window) == 0 {
				t.Fatalf("round %d: empty window %d", round, wi)
			}
			if len(window) != len(twice[wi]) {
				t.Fatalf("round %d: window %d differs between gathers", round, wi)
			}
			for _, w := range window {
				if w.target != next {
					t.Fatalf("round %d: window %d out of order: target %d, want %d", round, wi, w.target, next)
				}
				next++
			}
			candidates, executions := windowTotals(window)
			closed := candidates >= budget || (executions >= windowExecutionBudget && candidates >= minimum)
			if wi < len(once)-1 && !closed {
				t.Fatalf("round %d: window %d closed early at %d candidates, %d executions", round, wi, candidates, executions)
			}
			if len(window) > 1 {
				pc, pe := windowTotals(window[:len(window)-1])
				if pc >= budget || (pe >= windowExecutionBudget && pc >= minimum) {
					t.Fatalf("round %d: window %d's prefix already met a bound (%d candidates, %d executions) yet the window grew", round, wi, pc, pe)
				}
			}
		}
		if next != n {
			t.Fatalf("round %d: %d targets partitioned of %d", round, next, n)
		}
	}
}

// The anchors behind the property: a probe-class list closes at the
// candidate ceiling exactly as before; a suite-class list — sixty-four
// tests per target, one candidate each — closes at the budget's eight
// candidates; and a heavier suite — three hundred tests, six candidates
// per target — meets the budget on its first target yet closes only at
// the candidate minimum, so the audit's floored sample never exceeds an
// eighth of the window (REQ-exec-oracle-run).
func TestSuiteClassOracleClosesItsWindowOnTheExecutionBudget(t *testing.T) {
	ceiling, minimum := windowBounds(8, 0)
	probe := make([]work, 100)
	for i := range probe {
		probe[i] = work{target: i, candidates: make([]engine.Candidate, 1), oracle: make([]string, 1)}
	}
	if windows := gatherAll(probe, ceiling, minimum); len(windows) != 2 || len(windows[0]) != 64 || len(windows[1]) != 36 {
		t.Fatalf("probe-class partition = %d windows; want 64 then 36", len(windows))
	}
	suite := make([]work, 100)
	for i := range suite {
		suite[i] = work{target: i, candidates: make([]engine.Candidate, 1), oracle: make([]string, 64)}
	}
	perWindow := windowExecutionBudget / 64
	windows := gatherAll(suite, ceiling, minimum)
	if len(windows) != (100+perWindow-1)/perWindow || len(windows[0]) != perWindow {
		t.Fatalf("suite-class partition = %d windows, first %d targets; want windows of %d", len(windows), len(windows[0]), perWindow)
	}
	if c, e := windowTotals(windows[0]); c != perWindow || e != windowExecutionBudget {
		t.Fatalf("first suite window = %d candidates, %d executions; want %d at the budget", c, e, perWindow)
	}
	heavy := make([]work, 20)
	for i := range heavy {
		heavy[i] = work{target: i, candidates: make([]engine.Candidate, 6), oracle: make([]string, 300)}
	}
	windows = gatherAll(heavy, ceiling, minimum)
	if len(windows) != 10 || len(windows[0]) != 2 {
		t.Fatalf("heavy-suite partition = %d windows, first %d targets; want 10 windows of 2 (the minimum of 8 candidates, never width one)", len(windows), len(windows[0]))
	}
	if c, _ := windowTotals(windows[0]); c < minimum {
		t.Fatalf("heavy-suite window holds %d candidates, under the minimum %d", c, minimum)
	}
}
