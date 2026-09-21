# Installed gomutant binary skews from HEAD (fleet sweep 2026-09-21)

The weekly fleet sweep's binary-provenance check reports the installed
gomutant at build input `7e4b8e763b29` against a repo HEAD of
`c06e5d0bf11e`: SKEW. The installed binary is the chunk-257 landing
(the priced, budget-bounded freshness-proof passes); HEAD has since
taken cross-tool train chunk 278's in-flight change sets — the gofresh
bump v0.102.2 → v0.105.0, the faces registering through gofresh's
guidance projections, the phase set / vouch set / reason clause moved
onto gofresh's, and (after the sweep) the evidence row embedding
gofresh's fingerprint record with its DocumentVersion bump. Every one
of those is a behavioural change the installed tool lacks: a consumer
running the installed binary against a store written by HEAD, or the
reverse, meets the skew guards' refusals instead of a measurement.

The standing response is the cheap one the sweep runbook states: `go
install` in this repo once the change set settles, then close this
doc. Nothing here needs design.

Scanned: the index and all 49 docs; none touches binary provenance
(the two earlier skew filings, 2026-08-31 and 2026-09-07, were closed
at chunks 113 and 130 and are gone from the index — this is a fresh
instance, not an overlap).

Lands: cross-tool train chunk 278 — its close-out's `go install`
(any earlier gomutant session's install closes it sooner; whichever
first).
