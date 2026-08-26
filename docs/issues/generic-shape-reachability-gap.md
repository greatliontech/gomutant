# Generic shapes defeat attributed reachability — whole packages go dark

Field report (bldc campaign, 2026-08-27): 567 of 1164 selected targets
skipped with "decision evidence unavailable: closure: attributed
reachability: unsupported analysis shape: T". The skips are not
scattered: they wipe out 14 packages entirely (persistence core,
serving surfaces, the compile engine — `record/store` 108,
`compile/geometry` 108, `compile/pass` 81, `record/collab` 53, `ops`
42, `mcp` 34, `api` 31, `record/checkout` 25, `compile/imaging` 24,
`compile/run` 22, `compile/quantities` 18, `compile/view` 12,
`cmd/bldc` 7, `compile/rules` 2), and no package holds both measured
and skipped targets — one generic-shaped symbol in the reachability
closure appears to take its whole package's target set down with it.

Half the consumer's tree carries zero campaign evidence while the
summary line reports the skip only as a count with a reason string.
Two halves to the gap: the analysis should handle the generic shapes
(type-parameterized symbols in the attributed-reachability closure),
and until it does, the skip report should surface the blast radius —
"package X: all N targets skipped" reads very differently from 567
scattered skips. Ephemeral probes execute fine in these packages
(they bypass the reachability analysis), which is the consumer's
stopgap.

Lands: user decision.
