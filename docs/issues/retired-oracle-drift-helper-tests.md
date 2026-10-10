# Remove non-admitted drift helper fixtures

Lands: gofresh docs/plans/tooling-redesign.md 5.6, when the consumer's test
and implementation residues are retired after its unit-boundary migration.

Oracle-drift work state, dispatch narrowing and publication have no admitted
producer. The remaining forward-reference proposal helpers in freshness.go,
driftRemeasureIndexes and driftFindingCounts in run.go have test callers only.
They establish no independence and confer no reuse authority.

Their removal is a test-surface consolidation: preserve the direct changed-
oracle regressions, historical-format admission, candidate conservation,
withdrawn-killer evidence, site anchoring and attestation contradictions on the
surviving fresh/candidate-local/extension paths. Do not retain a duplicate
implementation in tests as replacement coverage. Retire mechanism-only claims
and enforcement pointers rather than treating their passing tests as proof of
the current execution policy. The public policy gate and complete dispatch
remain enforced independently of these helpers throughout.
