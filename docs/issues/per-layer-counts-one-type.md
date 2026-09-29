# Per-layer record counts are spelled three ways

`LayerCounts{Repo, Local}` (lifecycle.go) counts prune's kept and
retarget's rewritten and touched records per layer of
REQ-result-layers — a disjoint pair. Two adjacent counters spell the
same fact differently: `InspectionResult.Repo/Local` (inspection.go)
is a hand-kept pair whose unknown-layer default is the opposite of
`LayerCounts.add`'s (an unmatched layer string counts local there,
repo here — unreachable today, two constants exist, but two rules for
one question), and `RunTallies.Committed/CommittedLocal` with
`BankedState` (banked.go) is a total-plus-subset encoding rendered "N
to the findings document, M machine-local". On the wire the pair is
already flat twice (`repoCommittable`/`localOnly` on the findings and
explain responses) and an object once (`kept`, `rewrittenCounts`,
`touched` on the lifecycle responses) — three grammars for one fact.

The collapse: one `LayerCounts` the three sites fill, one projection
on the wire (the object), the total derived; the run tallies keep
their total-plus-subset rendering as a projection over the pair, never
a second count. Invariants preserved: a symbol held in both layers is
two records; the unknown-layer default is one rule (repo, as `add`
has it) or a refusal.

Lands: cross-tool train chunk 220 (the one-spelling sweep; filed at
283's change set B from the review's consolidation candidate).
