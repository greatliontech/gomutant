# A plan's cost is candidate materialization, not observation

Measured 2026-09-07 over gomutant's own `internal/engine` (224 targets,
8,162 candidates, warm memos, `--plan`, no priors): 73 s wall, of which
64% of CPU sits under `engine.(*Tree).materializeCandidates` —
`go/format.Source` (47%) and `golang.org/x/tools/imports.Process`
(30%) run once per candidate over the candidate's whole file — while
the decision views and the observed union together are a small share
(one observation per module group after the one-view-set fold; the
warm construction is a memo hit). A plan decides budget and prices
candidates; it never executes a mutant, yet it pays the source
rendering of every one.

Collapse candidates, in order of what a plan and a decision actually
read: price and count candidates from the mutation table without
rendering (materialize lazily at execution, per window); or render
without the whole-file reformat and import pass (a byte-range splice
at the mutation site keeps the file's formatting and imports as they
were, and the import pruning the never-reached survivor rule needs is
per package, not per candidate). Invariants preserved: the candidate
set, its identities and extents (REQ-target-model), the mutated
source that executes.

Lands: with the next change set touching candidate materialization
(`internal/engine` materializeCandidates or the mutation table's
rendering).
