# Warm floor is subject-view construction, not analysis proving

Lands: when subject-view construction on an unchanged tree stops paying
package-load-scale work per record - a memoized/constructed-once view, or
gofresh's purity scan sharing the view's own typed load - or the user
accepts ~3s per record per invocation as the serving floor.

## Measured (fixture tree, 3 explicit-oracle targets, budget 1, gofresh v0.32.0)

Same protocol as the pre-memo measurement; prior numbers in parentheses.

- cold measure: 23.0s (24.1s) - unchanged, as designed: the memo cannot
  help a first measurement.
- warm full-serve: 15.3s (17.5s) - the persistent observability memo
  removes only the proving leg. Direct attribution on identical findings
  and build cache: memo-cold serve 16.4s, memo-warm 15.2s.
- findings inspection (freshness classification, no execution): 10.0s for
  3 records (10.2s) - unchanged, and the inspection path writes no memo
  entry: it never reaches observability proving at all.
- load+resolve without freshness (discover): 0.1s.

## Attribution

The ~3s/record floor is view construction itself, paid per invocation on
an unchanged tree: the typed go/packages load, gofresh's purity scan -
`scanSubjectsInWithBuildFlagsEnv` runs a second full typed load
(NeedSyntax|NeedTypes|NeedTypesInfo|NeedDeps) of the same packages the
view already loaded (filed gofresh-side:
gofresh/docs/issues/purity-scan-duplicate-typed-load.md) - and closure
capture hashing. The memo covers none of these: its key derivation needs
the closure walk, and proving is the only leg behind it.
