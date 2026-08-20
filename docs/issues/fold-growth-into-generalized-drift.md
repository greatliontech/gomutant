# Fold the growth carve-out into the generalized drift carve-out

With the killer-drift carve-out composing grown oracle sets and flagged
candidate evidence, the derived-oracle-growth carve-out is a strict special
case: an inert add-only delta satisfies both gates, and growth wins only by
evaluation order. The two differ in exactly two behaviors:

- re-measure oracle scope: growth runs the re-measured survivors against
  oracle groups built over the ADDED tests alone (cheaper — the recorded
  survival against the retained set stands as evidence); drift re-measures
  against the full current oracle;
- advisory buckets: growth carries recorded buckets (upgraded by the delta
  coverage probe); drift re-derives from the current probe.

The fold: delete `evidenceSetCoversGrowthContext`, `grownSurvivorIndexes`,
`growFindingCounts`, and the growth arm of the serve/execute/splice phases;
express the survivor-scope optimization inside drift as "moved = ∅ ⇒ the
re-measure oracle for SURVIVORS narrows to the added tests" (kills with
moved killers keep the full set — they have no recorded outcomes on the
unmoved oracles). The spec's four carve-outs collapse to three, and the
survivor-delta narrowing generalizes beyond the inert case: survivors'
recorded passes on unmoved oracles stand under any attributable delta, so
survivors need only added ∪ moved — a wall-clock win on minute-class
oracles that neither carve-out delivers today.

Invariants preserved: the growth keystone (added tests only extend the
recorded set's behavior), attributed-kill standing, the fail-closed bounds
(regeneration re-identification, union reconciliation, per-group baselines
over the added tests).

Lands: with the next change touching the serve carve-outs' shared
re-identification or bucket machinery (see
consolidate-reidentification-and-bucket-policy.md — the two land naturally
together).
