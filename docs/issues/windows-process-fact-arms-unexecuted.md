# Windows process-fact arms are compile-checked, never executed

The engine's platform-owned process facts — `oracleProcessKilled`'s
windows reading (the jobCommand `cancelled` flag, set by both
ctx-driven job kills: the Cancel closure and `terminateJob`), the
job-object spawn/assign/resume paths, and the timeout-attribution
behavior built on them (`oracleBudgetFired`, `BaselineTimeoutError`
flow) — are verified on windows only by `GOOS=windows go build` and
`go vet`. No test executes them: the exit-state fixture test
(`TestOracleBudgetFiredDiscriminates`) skips on windows, and the
development loop has no windows host.

The cost is demonstrated, not theoretical: two windows-only
attribution defects (the POSIX-only `ExitCode() == -1` reading, and
the `Run`-path job kill bypassing the `cancelled` flag) passed
compile-checking and were caught only by reviewer state walks, in a
two-round span of one change set.

What lands: execute the engine's process-fact and timeout-attribution
arms on a windows runner — at minimum `TestOracleBudgetFiredDiscriminates`
with windows-native fixtures (cmd.exe equivalents of the three exit
states, plus a ctx-driven kill exercising the `cancelled` flag) and
`TestParentDeadlineIsCancellationNotTimeoutKill`.

Lands: when a windows runner enters the CI matrix (chunk 107's
workflow surface owns the runner set).
