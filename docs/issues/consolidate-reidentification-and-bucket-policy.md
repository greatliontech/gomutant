# One re-identification helper; one bucket policy

Three near-identical deterministic re-identification helpers repeat the
complete-count pin, `candidateIdentityIndex` uniqueness check, per-identity
lookup, and runnability check: `flaggedCandidateIndexes`,
`grownSurvivorIndexes`, `driftRemeasureIndexes` (run.go). Candidate: one
`reidentify(generation, rec)` producing the identity index, with
per-carve-out selection predicates on top.

Advisory-bucket re-derivation is likewise expressed three times with three
different gates (growth's inline coverage upgrade, drift's
`bucketSurvivorExecution(…, 0)` gated on moved∪added, extend's
`bucketSurvivorExecution(…, len(prefix))`). The drift added-only bucket gap
fixed in the generalized-drift change set was a direct consequence of this
duplication; a single policy point removes the class.

Lands: with fold-growth-into-generalized-drift.md (same machinery).
