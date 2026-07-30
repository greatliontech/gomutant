# Per-symbol observation retention scales memory with candidates × observation size

Lands: when the observation union folds incrementally as mutants complete, so a
symbol's memory bound is the running union plus in-flight mutants, never the
full candidate set.

## Observed

A consuming corpus (`candosa/cerebro`) ran an exhaustive eight-package delta
campaign (`--jobs 4`, no budget) whose oracles read a large document corpus:
each `runtimeinput.Observation` carries thousands of filesystem records, and
the delta's symbols are candidate-heavy (567, 273, 113 candidates on single
symbols). The gomutant process itself — not its test children — reached
34.1 GB total-vm / 25.9 GB anon-rss and was taken by the kernel OOM killer
mid-campaign (`task=gomutant … anon-rss:25929608kB`); the host, already
swap-thrashed, went down minutes later. A ninth package with a still larger
symbol (`compileDocuments`) was queued for the next run and would have grown
the peak.

## Mechanism

`completedObservationUnion` (run.go) appends every completed mutant's
observation to a `states` slice for the whole symbol and merges once at the
end (`mergeFindingObservationsContext(ctx, root, env, states...)`). Peak
memory per symbol is candidates × observation size, and `--jobs` multiplies
concurrently-aggregating symbols.

## Shape

Union merging is associative: fold each observation into the running union as
its mutant completes and release it, or batch-merge every K observations. The
`CandidateEvidence` entries for incomplete candidates are small and
unaffected. The symbol's result is byte-identical; the bound becomes
O(union + in-flight) regardless of candidate count. Until then the operator
levers are `--jobs` (bounds concurrent aggregations, not the largest symbol)
and `--budget` (bounds the balloon by capping exactly the coverage a ship
gate wants uncapped).
