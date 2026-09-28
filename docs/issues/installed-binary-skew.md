# Installed gomutant binary skews from HEAD (fleet sweeps 2026-09-21, 2026-09-28)

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

Re-observed by the 2026-09-28 sweep as a fresh instance: the 278
close-out's install happened — the installed binary is now
`3a0f3d29d370`, the chunk-278 landing ("every go command rides one
runner under gofresh's policy") — and HEAD has moved past it to
`ab2ca4106a87`, carrying chunk 282's two landed change sets: "prune
acts on every layer through one record applier" and "retarget rewrites
every layer's record in its own layer" (both `feat(store)!`, 2026-09-22;
nothing has landed since). Both change the record verbs' behaviour over
the machine-local overlay — a store the installed binary prunes or
retargets is written under the pre-282 rule the fix replaces — so the
skew is behavioural, not docs-only. The same cheap response applies.

Scanned: the index and all 51 docs; none touches binary provenance
(the two earlier skew filings, 2026-08-31 and 2026-09-07, were closed
at chunks 113 and 130 and are gone from the index; this doc is the one
open instance and widens rather than duplicates).

Lands: cross-tool train chunk 282 — its close-out's `go install` (the
chunk whose change sets HEAD now carries; any earlier gomutant
session's install closes it sooner — whichever first).
