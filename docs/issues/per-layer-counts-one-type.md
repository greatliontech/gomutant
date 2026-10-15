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
a second count. Preserve the population each counter names: physical records in
two layers, merged findings, and successful commits are not interchangeable
counts. Resolve the opposite fallback rules from the actual layer domain rather
than choosing one because it is older. The text above describes divergent
defaults, not a reachable unknown layer in the current two-layer model.

The current lifecycle object and inspection/run encodings still need one
reader-facing account. Coordinate the projection with selection-coexisting
records; do not derive a total by combining overlapping populations. Any wire
change is explicit and preserves both-layer visibility.

Lands: tooling-redesign 8.3.
