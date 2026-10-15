# Real-runner execution of Windows process-fact arms remains unverified

The engine's platform-owned process facts — `oracleProcessKilled`'s
windows reading (the jobCommand `cancelled` flag, set by both
ctx-driven job kills: the Cancel closure and `terminateJob`), the
job-object spawn/assign/resume paths, and the timeout-attribution
behavior built on them (`oracleBudgetFired`, `BaselineTimeoutError`
flow) — have compile/build checks, but this filing has no verified Windows
runner execution. `internal/engine/process_windows_test.go` already contains
`TestCommandContextKillsWindowsJob` and its child helper; writing a first job-kill
test is not the missing work. `TestOracleBudgetFiredDiscriminates` still has a
Windows skip, so job termination and attribution coverage must be distinguished.

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

Real Windows runner capability and a caller that uses it are prerequisites for
acceptance. Run the existing job-kill witness and the applicable attribution
cases there, supplementing missing native cases only where needed. Cross-build
success is not execution evidence, and this document update performs neither.

Lands: tooling-redesign 9.3.
