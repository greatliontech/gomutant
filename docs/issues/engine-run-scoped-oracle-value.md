# The engine's spawn chain repeats operation-owned inputs

The engine's oracle spawn entries (`RunMutant*`,
`TestProbe*`, `CoveredPositions`, their internal chain down to
`runOracleProcess`, the ingest mirror and the merges) take a trailing
`bounds OracleBounds` that is constant for a run's whole life — the
per-run bounds that replaced the process-wide ceiling and width. The
root package already holds the run's constants on one value
(`runOptions`: bounds, evidence env, leash, budgets); the engine has no
counterpart, so a run-scoped fact reaches each spawn as a parameter
beside `env`, `binFlags`, `bracketPaths`, `namespaces` — themselves
run constants threaded the same way. The original fourteen-signature count is
a historical inventory, not a new census. Full-oracle@3 removed the scheduling
producer; retained advisory coverage helpers are not a reason to rebuild it.

The migration should bind immutable operation inputs once while keeping each
transformed executable's producing evidence and closing obligations its own.
Do not let a shared run value lend base-program support to a mutant or merge
mutable transactions. Preserve every spawn's execution
under the run's bounds (`TestConcurrentProbesSpawnUnderTheirOwnBounds`,
`TestCampaignAndProbeKeepTheirOwnBounds`); the test seams
(the root's `seams` fields `runMutantObserved`, `runMutantShaped`,
`baselineProbe`, `killGround`, `advisoryProbe`, `testProbe`,
`runMutantEvidence`, `coveredPositions`) keep observing each spawn's
inputs.

Secondary constituents: tooling-redesign 7.2 separates instrumentation from
policy; 7.4 carries explicit resource controls. The live boundary faults include
unchecked positive MiB conversion (`OracleMemoryBytesFromMiB` shifts an int64,
so overflow can change the resource directive) and selection presence
(`Selection.Declared`/`applyEnv` use length, conflating omitted and explicit-empty
tags and leaving ambient tags in force). Preserve the parent's memory-policy
distinctions and explicit-empty selection in the migration; neither is
implemented by this filing edit.

Lands: tooling-redesign 5.4.
