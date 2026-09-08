# The execution window's cost models and constants have four homes

`Lands: user decision` — a refactor across run.go, estimate.go, and
schedule.go with no demonstrated fault; surfaced at cross-tool train
chunk 164's review.

Three functions reduce one `work` to a number, each answering a
different question with its own inclusion rule, and nothing names which
question each answers or which may read a measured duration:

- `windowExecutions` (run.go, chunk 164): candidates × the derived
  oracle's test count — an upper bound that is a tree property; it MUST
  read no duration and no findings-document state, since the window
  partition it keys must not move between runs of an unchanged tree.
- `workFullPrice` / `candidatePrice` (estimate.go): the duration-priced
  cost of a target's work for the ready-window ordering and the audit
  share — duration is its input by design.
- `executingIndexes` (schedule.go): the candidates a target actually
  executes this run (served ones execute nothing; a narrowed candidate
  a coverage subset) — findings-document state, correct for execution,
  forbidden for the partition.

`windowPrice`/`mkReady` (duration-based, execution ORDER) and
`windowExecutions` (count-based, window MEMBERSHIP) both reduce a
window to a number; their coexistence is correct and unrecorded.

The window's shape constants live in four blocks: the gather's
(`windowCandidatesPerWorker`, `windowCandidatesFloor`,
`windowCandidatesMin`, `windowExecutionBudget`), the confirmation's
(`confirmStreak`, `confirmStride`), the schedule's
(`scheduleMinCandidates`, `scheduleMinTests`), and the audit's
(`auditNarrowedCap`, `auditShareDivisor`) — with one cross-block
invariant already pinned (the candidate minimum equals the audit share
divisor).

The collapse: one `windowcost` home stating the three questions, their
permitted inputs, and the constants beside the invariants that tie
them, so the next author reaches for the right model. Invariants
preserved: the partition stays a function of tree + order + worker
count; the ordering keeps its durations; the executing set keeps its
document state.
