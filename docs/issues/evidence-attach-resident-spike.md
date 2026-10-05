# A target's evidence attach spikes the resident set past the proof pass

Measured 2026-10-05 over gomutant's own derived campaign (`run --changed
b2933b8`, 20 targets, 725 candidates, the machine alone, the process's
VmHWM sampled every fifteen seconds): the mode-wide proof pass grew the
high-water mark from 1.4 GB to 3.6 GB over seventeen minutes; then, at
the first target's confirmation — the rows `analysis observing oracle
runtime inputs (freshness evidence)` and `confirming target 2/20` — the
mark rose to 7.0 GB and 8.4 GB within three minutes, and stayed there.
The campaign's resident ceiling is the per-target evidence attach, not
the proof pass chunk 311 slices: the runtime-input observation of the
oracle process over gomutant's own oracle — the bracket's ingest of
what the process read — held several gigabytes for one target.

Measure the attach alone (one target, the ingest's size and the
manifest's) before deciding where the cost lives — gomutant's
withCandidateEvidence, or gofresh's runtimeinput ingest — and whether
it bounds (the oracle memory ceiling bounds the oracle processes, not
the parent's ingest of their observations).

Lands: cross-tool train chunk 308 (the resident set released — the
attach's cost measured and bounded beside the idle server's).
