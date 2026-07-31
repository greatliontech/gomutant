# Post-completion CPU-burning tail after the last target commits

Lands: the post-commit tail is profiled (SIGQUIT stack dump at reproduction
rather than an external kill) and the spin is closed — or shown benign and
bounded, with the bound stated.

## Observed

After the last of 49 targets committed to the findings document, the
`gomutant run` process continued at ~100% of one core (state `futex_do_wait`
on the main thread, no child test processes, no further file writes) for 30+
minutes until externally killed; cumulative CPU ≈ 4 hours for a package
whose clean suite runs in ~3 s. Same run as the oracle-enumeration-tree-lag
observation (`--force`, one package, Linux, go1.26.5).

## Candidates

- The per-target observed-capture/view-construction passes
  (`freshness.go:141-198`, `CaptureObservedBatch` at :159-165) re-entered
  post-commit.
- `probeOracleInstability` (`run.go:293+`) without spawning probes.
- A spin in the final serve/splice path.
