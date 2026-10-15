# Candidate materialization remains a cost question requiring remeasurement

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

Current `materializeCandidates` already calls `imports.Process` conditionally
for removal-capable candidates, rather than unconditionally for every candidate.
The historical percentages above have not been remeasured and are not a current
profile. Formatting, deduplication and import repair help decide the actual
candidate set; an unrendered table count cannot simply replace that result.

Measure the same target/candidate population with cold and warm state and no
oracle omissions before choosing lazy materialization or another sharing scope.
Preserve candidate identities, extents, duplicate/discard behavior and actual
mutated source. Do not revive a never-reached exemption as an import-repair rule.

Secondary constituent: tooling-redesign 7.8 owns generation-scoped sharing once
the residual cost and complete key are established.

Lands: tooling-redesign 7.7.
