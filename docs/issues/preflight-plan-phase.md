# No preflight phase: precondition findings surface hours into a run

Lands: when gomutant work resumes after gofresh's analysis-simplification
plan closes.

## Observed (field run, raftstore corpus)

Targets known unverifiable from cached stability evidence produced their
guidance line ~2 hours into the run instead of at minute zero. Every input
for a dry-run already exists: changed-surface analysis, candidate counts,
oracle-stability records, cache-serve checks.

## Direction

A preflight that prints the plan — "50 targets, ~1,950 fresh candidates,
0 cache-servable, 7 targets unverifiable (with reasons)" — and can
optionally abort, so precondition holes (confined-FS classes, unverifiable
oracles) are caught mechanically before execution. The workflow splits
into plan → fix preconditions → execute.
