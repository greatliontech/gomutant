# Pipeline preparation with oracle execution

Lands: gomutant train, fourth item — after the kill-cache keying, on
gofresh >= v0.43.4.

Run's phases are strictly serial today: resolve+freshness, then
baselines+mutant generation, then the oracle pool. Nothing about the
evidence model requires that — a target whose preparation is complete
can enter the pool while later targets still prepare, and correctness
is preserved because per-target evidence isolation (one test per
process, per-process testlog, per-target capture brackets) never
shares state across targets. This is pipelining, not parallel mutant
execution, which stays excluded (parallel oracle runs would share the
tree).

Evidence (gofresh d2cf048, the analysis plan's close-out measurement;
campaign: the cerebro reproduction pinned at 9d0fe5a2, 54 targets,
identical outcomes across runs):

- warm caches (gofresh v0.43.4 persistent memos primed): 388s total,
  of which 166s resolve+freshness and ~103s pool-until-first-commit —
  the pre-execution phase is serial against an idle oracle pool, so
  roughly 120-165s of the 388s is hideable behind execution.
- cold caches: 577s total, 263s resolve+freshness — the headroom is
  larger cold, same shape.
- The dominant single stall (~100s warm) sits in gofresh's observation
  pass and shrinks independently under gofresh's observability
  precision pass; pipelining hides whatever remains of it.

Shape sketch: preparation emits ready targets to the pool as they
complete instead of after the last one; the findings document's
incremental per-target commit discipline already tolerates arbitrary
completion order; progress reporting (the preflight/progress train
item) should land first so the interleaved phases stay observable.
