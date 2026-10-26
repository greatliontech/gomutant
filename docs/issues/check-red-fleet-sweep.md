# stipulator check red: two requirements bound only to example-class witnesses (fleet sweep 2026-10-26)

The weekly fleet sweep's `stipulator check` fails here with two
unexcused reds:

- `REQ-mut-comprehensive-basis` — uncovered; 12 bound witnesses
  (`internal/engine.TestArithmeticCatalog` and 11 more), every one
  classified example: no property driver or analyzer call in the
  bound body.
- `REQ-result-candidate-conservation` — uncovered; 7 bound witnesses
  (`TestExtendFindingCountsAppendsSuffixOutcomes` and 6 more), the
  same classification.

Both requirements demand a property witness or an analyzer proof;
every binding is example-grade, so each reads uncovered and no gap
record excuses it. The three further rows the 2026-10-12 and
2026-10-19 sweeps carried (REQ-core-attributed-kills, REQ-result-export,
REQ-target-model — excused then) are gone this week; only the two
unexcused remain. The class first appeared at the 2026-10-12 sweep
(the 2026-10-05 check passed with 0 unexcused), after the
negative-coverage cutoff moved the surfaces these bind; it was tracked
in this index until the index's close-out and persists under the
current tree. The full row list is the sweep's
`report-2026-10-26-0617.rows/check-gomutant.txt`.

Remedy, per requirement: a property-class witness (or analyzer proof)
bound as the required class, or a gap record stating why example
grade is the honest verdict — never a deleted witness (candidate
conservation's example witnesses are the surface the retired
oracle-drift helpers must not take with them).

Lands: 4.1 (the canonical plan, gofresh `docs/plans/plan.md`) —
gomutant's first chunk; its change-set gate runs `stipulator check`,
which forces the diagnosis before anything ships. Closing observation:
the sweep's gomutant check line reading pass with 0 unexcused.
