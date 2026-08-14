# Post-completion CPU-burning tail after the last target commits

Lands: cross-tool train chunk 39.
## Observed

After the last of 49 targets committed to the findings document, the
`gomutant run` process continued at ~100% of one core (state `futex_do_wait`
on the main thread, no child test processes, no further file writes) for 30+
minutes until externally killed; cumulative CPU ≈ 4 hours for a package
whose clean suite runs in ~3 s. Same run as the enumeration-lag observation
whose hardening landed as the derived-oracle direct-parse cross-check
(REQ-target-default; history: `git log --all -- docs/issues/oracle-enumeration-tree-lag.md`)
(`--force`, one package, Linux, go1.26.5).

## Candidates

- The per-target observed-capture/view-construction passes
  (`freshness.go:141-198`, `CaptureObservedBatch` at :159-165) re-entered
  post-commit.
- `probeOracleInstability` (`run.go:293+`) without spawning probes.
- A spin in the final serve/splice path.

## Observed (2026-08-14, protodb, mid-prepare variant — stacks lost)

`gomutant run --changed HEAD --oracle-timeout 5m` over a 6-file /
7-target delta in the protodb tree (targets spanning
`internal/model/document` and the large `internal/db` package): the
progress stream emitted resolve+freshness events for all 7 targets in
the first minute, then went silent BEFORE any target decision or
baseline probe; the process spun at ~194% CPU (state Sl, no child
processes, no further file writes) for 3h05m until externally killed.
Differs from the original observation in phase — between the last
freshness event and decision output, i.e. plausibly the decision-view
batch build (see decision-build-locality.md), not post-commit — but
matches the signature class: multi-core spin, no children, no writes.
The `internal/db` target's oracle closure is a very large package (a
known heavy typed-load); candidate trigger. Stacks were not captured
(process was killed before SIGQUIT was attempted); the reproducing
tree is protodb @ 411caec9 + a small staged delta, recoverable.
Next reproduction: SIGQUIT first for the goroutine dump this issue's
profiling trigger wants.

## Probe (2026-08-01, did not reproduce)

An instrumented library-level campaign (54 targets, `Force`, jobs=4, one
package, gofresh v0.42.1 pins) returned 0.00s after the last commit — no
tail. A reusable watchdog harness now exists for arming on future
campaigns: it stamps every Progress/Decision/AnalysisProgress/Commit event,
records a whole-run CPU profile, and dumps all goroutine stacks if `Run`
has not returned 60s after the last committed finding (the reproduction
signature this issue's `Lands:` wants). Harness: a ~150-line `main`
importing gomutant with `Options` callbacks + `runtime/pprof`; recoverable
from the probe session if needed.

Cost-center evidence from the same run, relevant to whatever the spin is:
in-process CPU averaged 1.8 cores for the whole run; GC accounted for
roughly a third of samples (allocation pressure under AST walking and
type-checking), and ~25% of in-process CPU sat under repeated
`packages.Load` calls issued by gofresh `closure.loadView`/`loadEnv`. The
suspected per-target observed-capture machinery ran at a steady ~2.7s per
target post-execution — expensive but bounded and terminating here. The
original 4-CPU-hour tail remains unexplained; the issue stays parked on
its profiling trigger.
