# The library package outgrew go test's default 10m timeout

On go1.27 the root-package suite runs ~16 minutes on the dedicated
dev machine, so a plain `go test ./...` panics at the 10m default
with an arbitrary victim test named — a verdictless run that reads
as a hang. Every scripted invocation needs an explicit `-timeout`
(40m is the working budget); nothing in-repo carries that today.

The same scale blocks gomutant-on-gomutant campaigns outright: every
root-package target derives the root suite as its oracle, so the
campaign's baseline probe times out at the default 1m and stays
infeasible at any budget that would clear it (a ~16m oracle per
mutant across thousands of candidates). A `--changed`-scoped chunk
gate on 2026-08-26 skipped all 15 targets this way; the change set's
mutation evidence stood on scoped `gomutant ephemeral` probes
instead, which name their deciding tests and run in seconds. The
campaign face needs the suite itself decomposed or the derived
oracle scoped — an explicit timeout only converts the timeout into a
day-class run.

Lands: the go-test face with the CI workflows (the explicit timeout
lands in the matrix job), or the first contributor-facing make/task
runner; the campaign face when a root-package oracle baseline
completes in minutes-class time (suite decomposition or oracle
scoping), checked by re-running a `--changed`-scoped campaign.
