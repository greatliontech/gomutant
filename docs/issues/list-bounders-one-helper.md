# Remaining list bounders need one remainder grammar

The original inventory found four places with four
spellings of the remainder: `boundedNames` (schedule.go, ", +N more"),
`windowEstimate.noSignalList` (estimate.go, "+N more" as a line),
`cappedJoin` (run.go, "; and N more"), and the moved-oracle list in
freshness.go (" and N more"). One helper taking the bound and the
separator would make the remainder's grammar one spelling on every face;
the three existing outputs are pinned by CLI tests, so the fold moves
their expected strings and is a BREAKING CLI text change to declare.

The coverage scheduler and its no-signal producer are removed under
full-oracle@3, and changed-oracle drift has no admitted composition producer.
Re-census live callers such as `cappedJoin` and the retained reporting helpers;
delete dead helpers rather than restoring old producers to justify a fold.
Preserve each surface's cap and omitted count. A common grammar may parameterize
separators where the output shape genuinely differs; declare changed served text.

Lands: tooling-redesign 8.3.
