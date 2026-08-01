# No preflight phase: precondition findings surface hours into a run

Lands: gomutant train, first item — immediately after the pew bench
evidence path.

## Observed (field runs)

A whole-tree budget slice (budget 3, 2h timeout) on a second corpus spent
its entire two hours in the prepare phase and exited on one target's
observation-proof deadline — zero targets measured, nothing durable
produced. The preflight would have surfaced the per-target proof cost
before committing the budget; prepare-phase durability (partial proof
results surviving a deadline) is the companion gofresh-side lever,
slotted in its analysis plan's memo chunk.

On the raftstore corpus: targets known unverifiable from cached stability evidence produced their
guidance line ~2 hours into the run instead of at minute zero. Every input
for a dry-run already exists: changed-surface analysis, candidate counts,
oracle-stability records, cache-serve checks.

## Direction

A preflight that prints the plan — "50 targets, ~1,950 fresh candidates,
0 cache-servable, 7 targets unverifiable (with reasons)" — and can
optionally abort, so precondition holes (confined-FS classes, unverifiable
oracles) are caught mechanically before execution. The workflow splits
into plan → fix preconditions → execute.
