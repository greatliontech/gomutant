# Rapid-oracle records never plainly serve - every campaign re-measures them

Lands: when gofresh's startup-effect-precision chunks 0a-0c (TestMain
observed window, flag-registration startup audit, property-driver
dispatch closure) are consumed by a gomutant dependency bump - the
serve pin test and the tightened regime-pin assertion land with that
bump.

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

## Diagnosis (complete)

The record is born unservable: serve acceptance applies the observed
policy only when the recorded observation proof is Observable, and a
rapid-oracle test subject's proof records non-observable, so serves
fall to the plain policy and refuse on the oracle's environment reads -
drift re-measure every campaign. Measure never consults the proof, so
measuring works while serving cannot. Three stacked observability
blockers, confirmed empirically by patching gofresh locally and
re-running the serve probe after each:

1. A user TestMain classifies as a startup root, so its own
   fmt/os reaches read as pre-bracket startup effects - but the test
   log installs in the generated test-main init (after every dependency
   init, before TestMain), so TestMain's reads are bracketed
   observation inputs. Moving the already-tracked TestMain reachability
   slice from the startup walk into the observed subject walk clears
   this blocker.
2. The rapid driver's computed prop-callback call ("computed function
   call in pgregory.net/rapid.Check") opens the subject world.
3. Standard flag registration from the rapid library's package init
   reads as an unaudited standard startup operation.

## Resolution direction

Identify which evidence pair (target vs rapid-oracle subject) fails
acceptance at serve time and why the serve-time attached evidence
differs from the run-recorded one for rapid-importing test subjects
(candidates: gofresh verdict acceptance over the rapid stub's import
shape, observation attachment, or runtime-state recomputation). A pin
test that a rapid-oracle record serves cached on an unchanged tree
lands with the fix.
