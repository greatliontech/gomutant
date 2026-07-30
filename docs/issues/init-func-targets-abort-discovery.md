# A changed func init() aborts whole-delta discovery instead of being skipped or targeted

Lands: when `init` functions are either measurable subjects or are excluded from
changed-symbol discovery with a per-symbol note, so one changed init cannot abort
a delta-wide run.

## Observed

A consuming corpus (`candosa/cerebro`) edited a registry literal inside a
`func init()` (`internal/corpus`). A delta-wide `run --changed <ref>` and its
`discover` twin both aborted outright: the changed `init` "does not resolve" as a
target, and the abort took the whole discovery down rather than skipping the one
symbol. The caller worked around it by package-filtering the campaign around
`internal/corpus` and scheduling that package as a second scoped run — a silent
coverage seam a less careful caller would not notice.

## Shape

`init` functions are legitimate Go and routinely carry registry wiring that is
exactly the kind of code a mutation campaign should reach (a dropped
registration is a classic silent fault). Either outcome is admissible:

1. **Measurable**: resolve `init` bodies as subjects like any function —
   package-scoped identity (`pkg.init`, ordinal-suffixed for multiple files) is
   unambiguous in the compiled program.
2. **Excluded, loudly**: skip `init` from changed-symbol discovery with a
   per-symbol decision line in the findings document ("init excluded: not a
   resolvable subject"), so the delta run completes and the exclusion is on
   record instead of aborting the campaign.

Aborting the whole discovery on one unresolvable symbol is the one shape that
should not survive: it converts a per-symbol limitation into a campaign-level
outage and pushes callers toward hand-built package filters whose gaps are
silent.
