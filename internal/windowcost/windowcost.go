// Package windowcost is the one home of the execution window's cost
// models: the three questions a campaign asks of its work, what each
// may read, and the constants beside the invariants that tie them.
//
// MEMBERSHIP — which targets share a window (REQ-exec-oracle-run's
// window rule). Its inputs are the tree, the target order, the worker
// count, and the run's derived oracle lists; never a measured duration
// and never the findings document, because the partition is observable
// across runs of an unchanged tree and must not move between them.
// Bounds and Executions answer it and take integers only, so no
// duration can enter by type.
//
// ORDER — two questions with one price: which ready window runs next,
// and how much of the narrowing's modeled savings the narrowed-survivor
// audit may spend. The pricing (the root package's estimate file) reads
// the measured passing-baseline wall-clock per group, by design, and
// beside it the executing set and the probed coverage the schedule
// store holds — everything the partition may not read; DerivedAuditCap
// here is the share rule it applies, bounded above by AuditNarrowedCap.
//
// EXECUTION — three questions, each with its own inputs. Which
// candidates a target actually executes this run (a served target
// executes nothing; a narrowed candidate runs a coverage subset): the
// findings document's state, read by the executing set in the root
// package's schedule file, correct for execution and forbidden for the
// partition. Whether those candidates gate the coverage probe: the
// executing count against ScheduleMinCandidates and, per oracle group,
// the group's test functions in the covered package against
// ScheduleMinTests. Which kills confirm again: the window's own
// candidate-ordered reproduction outcomes and its own evidence's
// verifiability — never the document, never a served record — against
// ConfirmStreak and ConfirmStride, so confirmation stays deterministic
// for the window (REQ-exec-attribution).
package windowcost

import "time"

// The membership constants are contract: the partition is observable
// across runs. A window closes when its candidate total reaches the
// ceiling — CandidatesPerWorker per worker, CandidatesFloor at least —
// or, once it holds at least the candidate minimum, when its
// test-execution total reaches ExecutionBudget, whichever first; it
// always holds at least one target. The minimum — the worker count,
// CandidatesMin at least — is the audit's and the pool's guard, in
// CANDIDATES: the narrowed-survivor audit floors one full-oracle sample
// per window, so a window of fewer than CandidatesMin candidates would
// spend more than 1/AuditShareDivisor of itself on the sample (the two
// are one number, pinned), and a window narrower than the worker count
// offers less than one candidate per worker. A serve-heavy window,
// whose candidates mostly do not execute, relaxes both — the executing
// set must never key the partition.
const (
	CandidatesPerWorker = 8
	CandidatesFloor     = 64
	CandidatesMin       = 8
	ExecutionBudget     = 512
)

// AuditShareDivisor sets the audit's share of the narrowing's modeled
// savings: the audit may spend at most 1/AuditShareDivisor of what the
// narrowing saved this window, so the narrowing keeps the rest of its
// win on ANY oracle duration — a fixed count would price several full
// oracles onto a window whose narrowing saved less than one.
// AuditNarrowedCap is the CEILING of that derived cap: however much a
// window's narrowing saved, at most this many full-oracle re-runs
// audit it.
const (
	AuditShareDivisor = 8
	AuditNarrowedCap  = 4
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

// ScheduleMinCandidates and ScheduleMinTests gate the coverage probe:
// below two EXECUTING candidates the probe cannot amortize, and below
// eight tests the split saves less than the extra process it adds.
// Derivation constants, incidental rather than contract. Vars for test
// reach only: no production code writes them, a run's goroutines read
// them unsynchronized, so a test writes them before its run starts and
// restores them after it ends, never while one runs.
var (
	ScheduleMinCandidates = 2
	ScheduleMinTests      = 8
)

// Bounds derives the window's candidate ceiling and minimum from the
// worker count: ceiling = max(jobs × CandidatesPerWorker,
// CandidatesFloor), minimum = max(jobs, CandidatesMin). A fixed window
// (override > 0, a test seam's) is the whole rule: the ceiling and the minimum both
// take it, so the execution budget cannot close a fixed window early —
// the minimum IS the ceiling.
func Bounds(jobs, override int) (ceiling, minimum int) {
	if override > 0 {
		return override, override
	}
	return max(jobs*CandidatesPerWorker, CandidatesFloor), max(jobs, CandidatesMin)
}

// Executions is one target's test executions at one full oracle run
// per mutant: candidates times the derived oracle's test count. It is
// an UPPER BOUND, and deliberately so: a served target executes
// nothing and a narrowed candidate runs a coverage subset, but only the
// bound is a tree property — the executing set reads the findings
// document, which the run itself rewrites, and keying the partition on
// it would move the windows between two runs of an unchanged tree.
func Executions(candidates, oracleTests int) int {
	return candidates * oracleTests
}

// DerivedAuditCap is the audit's per-window sample cap derived from the
// narrowing's own modeled savings: at most savings/(AuditShareDivisor ×
// unit) full-oracle re-runs, where unit prices one re-run at the
// costliest work's full-oracle baseline, floored at one (the
// disagreement rate is measured in every narrowing window) and bounded
// above by AuditNarrowedCap. A non-positive unit — nothing priced —
// derives the floor, never a share.
func DerivedAuditCap(savings, unit time.Duration) int {
	if unit <= 0 {
		return 1
	}
	share := int(savings / (AuditShareDivisor * unit))
	return max(1, min(share, AuditNarrowedCap))
}
