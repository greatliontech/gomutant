# Rapid-oracle records never plainly serve - every campaign re-measures them

Lands: cross-tool train chunk 31.

## Observed

Diagnosed while pinning the property-regime measurement pin. A finding
measured with a rapid-importing oracle (fixture:
`example.com/fixture/lib.Add` against `TestPropRapidCheck`, full budget)
never serves from cache on an unchanged tree: the immediate re-run takes
the killer-drift path ("served: 0 kills stand on unmoved oracles;
re-measuring N candidates against the current oracle") and re-executes
everything, every run. Plain-oracle records (`TestAdd`) serve normally
(`TestRunEndToEnd`'s cached leg).

Isolation so far:

- Two consecutive fresh runs on one tree record byte-identical
  `TargetEvidence` and `OracleEvidence` structs.
- `evidenceSetMatchesContext` called directly with the record's own
  scalar values (timeout, memory, regime - and with the regime term
  removed) returns false: the failure is past the scalar guard, in
  `evidencePairsValid` / `memo.verify` (gofresh verdict acceptance or
  serve-time runtime-state verification) for the rapid oracle subject.
- The property-regime pin and pinned-seed flags are not the cause: the
  probe fails identically with the regime term neutralized, and the
  recorded evidence is stable across runs regardless.

## Impact

Permanent re-measure cost for every rapid-oracle target on every
campaign - the serve machinery's whole point is defeated for exactly the
suites the property-oracle prerequisites chunk made deterministic. No
wrong verdicts observed: the failure direction is re-measure, never a
stale serve.

## Resolution direction

Identify which evidence pair (target vs rapid-oracle subject) fails
acceptance at serve time and why the serve-time attached evidence
differs from the run-recorded one for rapid-importing test subjects
(candidates: gofresh verdict acceptance over the rapid stub's import
shape, observation attachment, or runtime-state recomputation). A pin
test that a rapid-oracle record serves cached on an unchanged tree
lands with the fix.
