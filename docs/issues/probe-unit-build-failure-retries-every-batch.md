# A coverage-probe unit whose test binary fails to build retries every batch, every run

Since the coverage probe banks per batch and continues past a failed
batch, a failure that is UNIT-WIDE — the probe's cover-instrumented test
binary does not build, so every batch of the plan fails the same way —
costs one probe process per batch per run (≈√N builds for N tests),
where the stop-at-first-failure loop it replaced paid one. Each failure
is named on the analysis channel and recorded in the bank, so the cost
is visible, never silent, and no verdict is affected (the group runs
whole either way); but the operator pays the whole plan again on every
run until the build is fixed.

The sound breaker: a batch's refusal whose cause is the harness's own
build-fail event (the classification the baseline already uses,
REQ-exec-ephemeral's harnessBuildFailure over the separated streams) is
the BINARY's, shared by every batch of the unit — stop the unit and
record the one failure for every remaining batch. CoveredPositions
today runs a merged stdout+stderr buffer and formats the tail; it
needs the stream walk the baseline has.

Lands: cross-tool train chunk 269 (gomutant: one `go test -json` stream
walk — the coverage probe joins the decoders it unifies and reads the
build-fail event).
