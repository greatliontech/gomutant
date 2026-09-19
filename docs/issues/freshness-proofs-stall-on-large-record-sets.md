# Delta campaigns stall in freshness proofs on a large record set

Lands: cross-tool train chunk 257 (the freshness-proof bound, directly after the bump chunks 246/250/253; triaged at 246.1)

A `run` over a tree whose findings document holds a few hundred
records never reaches measurement: preparation spends its whole
budget in per-target `freshness` and then "freshness proofs (union
over N subjects)", holding roughly 8 GiB resident, until cancelled.

Observed in greatliontech/pb (findings document of 292 records, Go
1.26, gomutant v0.57.11-0.20260918171351-46d0768fd594 — the current
main — and the build before it):

- `gomutant run --staged --changed HEAD` over nine packages: three
  attempts, each 5–25 minutes in `prepare … freshness` per target,
  then "freshness proofs (union over 155 subjects)"; no target
  measured; ~8 GiB RSS on a 30 GiB host until interrupted.
- `gomutant run --changed HEAD~2` over three packages (internal/plugrun,
  internal/rootpath, internal/plugexec) on the clean tree: the union
  grows to 1608 subjects; cancelled by a watchdog at 6 GiB / 25
  minutes with nothing measured, twice, the second time on the
  freshly installed binary.
- Earlier campaigns on the same host completed when the document held
  fewer records; the record set's oracle closures now union over most
  of the tree, so the proof set scales with the document rather than
  with the delta.

Expected: a delta campaign's preparation cost scales with the delta's
targets and their oracle closures, bounded in memory, or the proofs
are served incrementally (persisted per subject, reused across runs)
so a second run does not repeat the union. The pb-side record is
docs/issues/mutation-campaign-freshness-cost.md in that repo.
