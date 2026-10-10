// Package windowcost owns execution-window membership bounds and serial
// confirmation constants. Membership, ordering and execution have separate
// inputs so measured costs cannot move a window's partition.
//
// MEMBERSHIP — which targets share a window (REQ-exec-oracle-run's
// window rule). Its inputs are the tree, the target order, the worker
// count, and the run's derived oracle lists; never a measured duration
// and never the findings document, because the partition is observable
// across runs of an unchanged tree and must not move between them.
// Bounds and Executions answer it and take integers only, so no
// duration can enter by type.
//
// ORDER — which ready window runs next. Pricing in the root package's
// estimate file reads the measured passing-baseline wall-clock per group
// and the executing set. Coverage grants neither an omission nor a saving.
//
// EXECUTION — which candidates require measurement is decided by the
// reuse gates over the findings document, not by coverage. That selection
// is an execution input, never a partition input. Every executing oracle
// group runs complete and unsplit. Which kills confirm again: the window's
// own candidate-ordered reproduction outcomes and its own evidence's
// verifiability — never the document, never a served record — against
// ConfirmStreak and ConfirmStride, so confirmation stays deterministic
// for the window (REQ-exec-attribution).
package windowcost

// The membership constants are contract: the partition is observable
// across runs. A window closes when its candidate total reaches the
// ceiling — CandidatesPerWorker per worker, CandidatesFloor at least —
// or, once it holds at least the candidate minimum, when its
// test-execution total reaches ExecutionBudget, whichever first; it
// always holds at least one target. The minimum — the worker count,
// CandidatesMin at least — is measured in CANDIDATES, independently of
// how many execute. CandidatesMin is part of the fixed partition rule;
// the worker-count limb offers at least one candidate per worker before
// the execution budget can close a window. A serve-heavy window may offer
// fewer actual executions — the executing set must never key the partition.
const (
	CandidatesPerWorker = 8
	CandidatesFloor     = 64
	CandidatesMin       = 8
	ExecutionBudget     = 512
)

// Confirmation stride gating (REQ-exec-attribution): after ConfirmStreak
// consecutive reproductions within one target's window, further kills
// confirm at every ConfirmStride-th candidate; any flip restores full
// confirmation retroactively. The constants realize the contract's "run
// of consecutive reproductions" and "fixed deterministic stride" — the
// spec deliberately leaves the numbers code-side (nothing persisted or
// wire-visible depends on them).
const (
	ConfirmStreak = 3
	ConfirmStride = 4
)

// Bounds derives the window's candidate ceiling and minimum from the
// worker count: ceiling = max(jobs × CandidatesPerWorker,
// CandidatesFloor), minimum = max(jobs, CandidatesMin). A fixed window
// (override > 0, a test seam's) is the whole rule: the ceiling and the
// minimum both take it, so the execution budget cannot close a fixed
// window early — the minimum IS the ceiling.
func Bounds(jobs, override int) (ceiling, minimum int) {
	if override > 0 {
		return override, override
	}
	return max(jobs*CandidatesPerWorker, CandidatesFloor), max(jobs, CandidatesMin)
}

// Executions is one target's test executions at one full oracle run
// per mutant: candidates times the derived oracle's test count. It is
// an UPPER BOUND, and deliberately so: a served target executes
// nothing and a kill can end its oracle early, but only the bound is a
// tree property — the executing set reads the findings document, which
// the run itself rewrites, and keying the partition on
// it would move the windows between two runs of an unchanged tree.
func Executions(candidates, oracleTests int) int {
	return candidates * oracleTests
}
