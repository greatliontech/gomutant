# Root-suite scale blocks gomutant-on-gomutant campaign baselines

The root-package suite runs ~16 minutes on the dedicated dev machine
(CI carries that measured budget as an explicit `-timeout`; the
Taskfile carries the dev-host one). Campaigns on this repo derive
that same suite as every root-package target's oracle, so the
campaign's baseline probe times out at the default 1m and stays
infeasible at any budget that would clear it: a ~16m oracle per
mutant across thousands of candidates. A `--changed`-scoped chunk
gate on 2026-08-26 skipped all 15 targets this way; the change set's
mutation evidence stood on scoped `gomutant ephemeral` probes
instead, which name their deciding tests and run in seconds. The fix
is oracle scoping or suite decomposition — an explicit timeout only
converts the skip into a day-class run.

The gofresh repo is the same defect's second face, measured
2026-08-29: its root-package targets derive the ~6.5-minute root
suite as oracle, so a `--changed` chunk gate first went dark at a
10m oracle bound (both baselines timed out), then at a 30m bound
ran 2h21m without committing one of 303 candidates before being
killed — the arithmetic is tens of oracle-hours per campaign. That
chunk gate, too, stands on ephemeral probes (each invariant's
deciding test named, all killed); the re-run condition below covers
both repos.

Lands: with cross-tool train chunk 113 (the oracle
scheduling/robustness batch — coverage-guided-oracle-ordering is the
same family), checked by re-running a `--changed`-scoped campaign.
