# Stale-reason attribution builds a second subject view per stale target

Lands: when the moved-pin attribution derives from the matcher's own failed
check or reuses the run's prebuilt subject views. The warm-path measurement
(fixture, 3 records: ~3.4s of freshness analysis per record per invocation)
confirmed per-target view construction is the dominant per-run cost class.
The persistent observability memo (gofresh v0.32.0) did not subsume this
cost: it covers only the proving leg, and view construction dominates -
see warm-floor-view-construction.md for the post-memo numbers.

## Observed

The stale-decision enrichment calls `inspectFindingStateContext` per
non-matching prior, which re-runs declared-symbol listing, oracle
re-resolution and validation, and a fresh gofresh subject-view build — package
load plus closure captures — immediately before the run's already-built shared
views measure the same target. On the hot-loop case the attribution serves (an
edit staling many targets), that is one extra package-load-scale construction
per stale target, serialized before measurement starts.

Bounded above by the measurement that follows (mutant executions dwarf a view
build), so shipped as proportionate — but it is real, linear waste.

## Resolution

Either derive the moved axis from `evidenceSetMatchesContext` itself (it knows
which check failed) or add an inspection variant that accepts the run's
prebuilt views. Measure the delta in the warm-path chunk before choosing.
