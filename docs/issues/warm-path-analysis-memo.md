# Warm-path cost is dominated by per-run gofresh re-analysis

Lands: when the user decides the persistent-memo question - a gofresh-side
analysis cache keyed by (toolchain, build config, source closure), or accepted
per-run re-analysis cost.

## Measured (fixture tree, 3 explicit-oracle targets, budget 1, warm GOCACHE)

- cold measure: 24.1s; warm full-serve: 17.5s - serving is only ~27% cheaper
  than measuring at toy scale.
- findings inspection alone (tree load + freshness classification of the same
  3 records, no execution): 10.2s, about 3.4s per record - observation-RTA
  and closure capture re-run per subject on every invocation.
- load+resolve without freshness (discover over a targets doc): 0.1s.

The warm floor is therefore gofresh analysis, not execution: every warm run
re-derives observability proofs and closures that a persistent memo could
serve when nothing moved. At corpus scale (dozens of subjects) a no-op warm
run pays minutes of pure re-analysis.

## Decision surfaced

A persistent analysis memo is a gofresh feature: cache captures keyed by
toolchain, build configuration, and source-closure identity; invalidation is
exactly gofresh's own freshness problem, so the engine can dogfood itself for
its cache. The alternative is accepting the per-run cost as the price of
zero cache-trust surface. User's call; numbers above.
