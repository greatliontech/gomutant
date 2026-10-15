# Remove non-admitted drift helper fixtures

Lands: tooling-redesign 5.6.

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

Current REQ-result-stale requires whole remeasurement when oracle identities or
evidence change; full-oracle@3 cannot be established by tests of the retired
composition helpers. Remove their mechanism-only callers after preserving
admitted-path regressions, rather than treating a surviving old helper pin as
authority to restore the producer.
