# go1.27 shifts execution buckets and reddens the library suite (pre-existing at HEAD)

Found 2026-08-24 while landing an unrelated change set on the first
go1.27 machine: the library suite fails at HEAD (worktree-verified,
commit e240be9) in two ways.

## Bucket shift (the substantive half)

TestRunEndToEnd pins the go/12 fixture's exact survivor outcomes; on
go1.27 the `block: empty` survivor at lib.go:24:12 reports
`never-executed` where go1.26 measured `executed-and-passed`. The
execution bucket — the axis operators use to choose between
coverage work and assertion work — is toolchain-sensitive. Beyond
the red test, this poisons triage: a tugboat chunk-gate campaign the
same day showed "never-executed" survivors on arms that hand-probes
proved covered-but-unpinned, consistent with under-attribution of
coverage on go1.27. Root candidates: go1.27 coverage/counter
placement changes under the harness, or the gofresh observation
layer's interaction with them (the go1.27 analysis defect is filed
separately: go127-generic-method-closure-analysis.md — the two may
share a root in go1.27 support but the symptom here is bucket
attribution, not closure refusal).

## Suite duration (the operational half)

The full library package now exceeds `go test`'s default 10m timeout
on this machine (the alarm fired with a 1s-old test running — the
time went to earlier tests), so a plain `go test ./...` cannot even
deliver a verdict. Whether go1.27 slowed the oracle-heavy tests or a
failure mode retries internally is undiagnosed.

Lands: with the tool-phase gomutant visit's go1.27 support (beside
the gofresh analysis fix) — the bucket attribution must be
re-verified against known-covered fixtures before any go1.27
campaign's execution buckets are trusted for triage.
