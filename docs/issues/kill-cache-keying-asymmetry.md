# Cache invalidation is coarser than verdict dependencies: kills keyed package-wide

Lands: when gomutant work resumes after gofresh's analysis-simplification
plan closes.

## Observed (field run)

A package-wide bracket move invalidated ~1,950 verdicts at once, though
the dependency structure is asymmetric: a survivor legitimately depends on
the whole test surface (any new test might kill it), while a kill depends
only on the mutated code and the content of the tests that killed it —
adding or editing unrelated tests cannot un-kill it.

## Direction

Key kill verdicts to their killing-oracle content (the compartment-ledger
and oracle-growth carve-out machinery is the substrate); survivors keep
the package-wide key. On the observed run this would have carried ~90% of
the cache through the sweep.
