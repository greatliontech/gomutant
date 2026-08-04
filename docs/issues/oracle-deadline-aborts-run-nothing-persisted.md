# One oracle deadline aborts the run; nothing measured is ever persisted mid-run

**Lands:** a run survives any single mutant's oracle outcome, and a
killed run leaves every completed target's verdicts in the findings
document.

Two composing defects, observed together on a real campaign
(2026-08-04, tugboat, `gomutant run --package` ×4, `--budget 0
--timeout 0`, 499 targets / 12,730 candidates, ~5.5 hours of ~25-way
execution):

1. **A single mutant's oracle deadline is treated as a fatal run
   error.** The run aborted with
   `gomutant: github.com/greatliontech/tugboat/node.Pipeline.ApplyCommitted: mutant pipeline.go:339:2 statement: delete: context deadline exceeded`.
   An oracle process exceeding `--oracle-timeout` is a NORMAL outcome
   of mutation testing — a mutant that hangs its oracle is detected
   by that very hang (verdict: killed-by-timeout, or at minimum an
   inconclusive recorded against the target). Whatever the precise
   internal path (the oracle run itself, or a bookkeeping step that
   inherited the oracle's context), no single mutant's outcome may
   abort a campaign. `--timeout 0` was set; the deadline that fired
   was per-oracle and its interpretation, not its existence, is the
   defect.

2. **Zero incremental persistence: the abort lost everything.** After
   ~5.5 hours the findings document was byte-identical to its
   pre-campaign state (mtime untouched) and the machine-local overlay
   was empty — zero of the 499 targets had been committed although
   execution was continuously busy (25 concurrent oracle processes
   observed throughout, orchestrator RSS growing to ~24 GB — the
   results were held resident instead of committed). Whether commit
   granularity is per-target or per-phase, a multi-hour run must
   flush completed verdicts as it goes: with (1), the combined
   behavior turned one slow mutant into the total loss of every
   core-hour spent. This is the same held-resident shape as the
   store-update/overlay report, surviving in the current build even
   with the overlay healthy and empty.

Repro shape: any multi-package exhaustive run over a tree with at
least one mutant whose oracle exceeds `--oracle-timeout` (a mutated
loop bound that makes a test allocate/spin unbounded is the common
carrier — tugboat grows several under `statement: delete`).

Downstream posture meanwhile (tugboat): campaigns relaunch scoped
per-package so an abort's blast radius is one package's work, and
the two FIRED tugboat issue docs (node-delta rerun, wal-install
verification) stay open pending a campaign that completes.
