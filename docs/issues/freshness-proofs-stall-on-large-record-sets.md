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

Second field report (greatliontech/pb at 57c88e3, findings document of
308 records, gomutant v0.57.11-0.20260920055743-9881159d78c3, under
a 25-minute timeout and a 12 GiB watchdog):

- `gomutant run --changed HEAD~1`, one target: "inspecting prior
  findings: closure signpost over 308 prior record(s)" 4 minutes,
  then `prepare freshness` for the one target 5 minutes, then
  measured; 10 minutes in all, 9.5 GiB peak resident.
- `gomutant run --changed HEAD~3`, 73 targets and 863 candidates over
  three commits: the same signpost, then preparation per target;
  the first target committed at 17 minutes, seven at 25 minutes
  when the timeout fired, with the pace estimate at about two hours
  for the run; 7.8 GiB peak resident.

So on this build a delta campaign reaches measurement where the
earlier builds never did, but preparation still spends the first
quarter hour on the document rather than the delta: a one-target run
and a 73-target run pay the same signpost, and a one-target freshness
proof alone takes five minutes.

Expected: a delta campaign's preparation cost scales with the delta's
targets and their oracle closures, bounded in memory, or the proofs
are served incrementally (persisted per subject, reused across runs)
so a second run does not repeat the union. The pb-side record is
docs/issues/mutation-campaign-freshness-cost.md in that repo.
