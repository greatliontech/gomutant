# The engine's spawn chain threads a run-constant bounds value through fourteen signatures

Every oracle spawn entry of `internal/engine` (`RunMutant*`,
`TestProbe*`, `CoveredPositions`, their internal chain down to
`runOracleProcess`, the ingest mirror and the merges) takes a trailing
`bounds OracleBounds` that is constant for a run's whole life — the
per-run bounds that replaced the process-wide ceiling and width. The
root package already holds the run's constants on one value
(`runOptions`: bounds, evidence env, leash, budgets); the engine has no
counterpart, so a run-scoped fact reaches each spawn as a parameter
beside `env`, `binFlags`, `bracketPaths`, `namespaces` — themselves
run constants threaded the same way.

Collapse: one engine-side run value (`engine.Run{Dir, Env, Bounds,
BinFlags, BracketPaths, Namespaces}` or similar) constructed once by the
root's run and probe entries and passed as the spawn chain's one
context, the per-call arguments shrinking to the mutant, the package
set, the pattern, and the timeout. Invariants preserved: every spawn
under the run's bounds (`TestConcurrentProbesSpawnUnderTheirOwnBounds`,
`TestCampaignAndProbeKeepTheirOwnBounds`); the test seams
(`runMutantObservedEnv`, `groupBaselineProbe`, `testProbe`,
`runMutantEvidence`, `coveredPositions`, `phaseBaselineProbe`,
`campaignCoveredPositions`) keep observing each spawn's inputs.

Lands: cross-tool train chunk 214
signatures.
