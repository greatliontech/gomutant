# Four list bounders spell the remainder four ways

The root package bounds rendered lists in four places with four
spellings of the remainder: `boundedNames` (schedule.go, ", +N more"),
`windowEstimate.noSignalList` (estimate.go, "+N more" as a line),
`cappedJoin` (run.go, "; and N more"), and the moved-oracle list in
freshness.go (" and N more"). One helper taking the bound and the
separator would make the remainder's grammar one spelling on every face;
the three existing outputs are pinned by CLI tests, so the fold moves
their expected strings and is a BREAKING CLI text change to declare.

Lands: cross-tool train chunk 220 (gomutant's smalls: the root's
one-spelling folds).
